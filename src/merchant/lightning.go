// Merchant-side Lightning quote tracking lives here; the src/lightning package
// is only used for outgoing LNURL payout helpers.
package merchant

import (
	"errors"
	"fmt"
	"log"
	"math/rand"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/tollwallet"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/utils"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/valve"
	"golang.org/x/time/rate"
)

var ErrQuoteNotFound = errors.New("lightning quote not found")

// ErrTooManyQuotes is returned when the in-flight quote table is full and
// nothing may be evicted to make room. It is a *local* refusal: the mint was
// never contacted, so it is not evidence about the mint's health. The API maps
// it to 429 with a distinct code so an operator can tell "we are being flooded"
// from "the network is slow".
var ErrTooManyQuotes = errors.New("too many active lightning quotes")

// ErrMintBusyLocal is returned when our own outbound quote budget toward a mint
// is exhausted. The request is refused at the edge instead of being sent, which
// is what keeps our traffic from being the reason the mint answers 429.
var ErrMintBusyLocal = errors.New("local mint quote budget exhausted")

// ErrAccessGrantNotApplied reports a purchase that was PAID and whose access
// could NOT be applied: the invoice settled (the value is in the operator's
// wallet), and the gate could not be opened for the client, so the customer has
// no allotment. The allotment is rolled back, the grant is retried, and the
// error is what the caller must surface: a paid purchase that cannot be granted
// is never a silent no-op for the caller and never a success for the module.
//
// It is returned instead of the underlying error so a caller can tell it apart
// from "the invoice is unpaid" and from a lookup failure, and it names the
// client and the quote in its message so the operator log line and the client
// response both say whose purchase is stuck.
var ErrAccessGrantNotApplied = errors.New("payment received but access could not be granted")

const (
	lightningQuoteStateCacheTTL     = 2 * time.Second
	lightningQuoteMonitorInterval   = 5 * time.Second
	lightningQuoteMonitorMaxBackoff = 30 * time.Second
	lightningQuoteMonitorMaxJitter  = 500 * time.Millisecond
	lightningQuoteCleanupInterval   = 1 * time.Minute
	lightningQuoteExpiryGracePeriod = 5 * time.Minute
	lightningQuoteMaxAge            = 30 * time.Minute
	lightningQuoteSettledRetention  = 10 * time.Minute

	// Bounds on the in-flight quote table. A customer needs one quote per
	// purchase; the per-client cap allows a couple of abandoned retries, and the
	// global cap is ~128 KiB of RAM on a 256 MB router (records carry a bolt11).
	// Without these, the table is a memory primitive an unauthenticated caller
	// fills at will.
	maxActiveQuotesPerMAC = 3
	maxActiveQuotesGlobal = 128
	// Eviction starts here, at 80% of the hard cap, so the table holds headroom
	// for a real customer instead of filling to the cap first.
	quoteTableEvictionWatermark = maxActiveQuotesGlobal * 8 / 10
	// Only quotes older than this are ever evicted. Reaping a quote the customer
	// may be paying right now would drop the record that recognises their
	// payment (their bolt11 would no longer be found by any poll), so eviction
	// stays strictly off recent records and a full table of fresh quotes is
	// refused instead.
	quoteEvictionMinAge = 5 * time.Minute

	// The self-imposed outbound budget toward a mint, so our own quote traffic
	// can never be what drives the mint into answering 429.
	mintQuoteBudgetDefaultRPS   = 2
	mintQuoteBudgetDefaultBurst = 5
	mintQuoteBudgetMaxMints     = 64
)

type LightningInvoice struct {
	QuoteID string
	Invoice string
	MintURL string
	Amount  uint64
	Expiry  uint64
	State   string
}

type LightningQuoteStatus struct {
	QuoteID       string
	MintURL       string
	Amount        uint64
	State         string
	AccessGranted bool
	Allotment     uint64
	Metric        string
}

type lightningQuoteRecord struct {
	Bolt11         string
	MacAddress     string
	MintURL        string
	Amount         uint64
	Expiry         uint64
	Allotment      uint64
	CreatedAt      time.Time
	CompletedAt    time.Time
	SessionGranted bool
	Processing     bool
	CachedState    tollwallet.MintQuoteState
	CachedStateAt  time.Time
	HasCachedState bool
}

func (m *Merchant) RequestLightningInvoice(macAddress, mintURL string, amount uint64) (*LightningInvoice, error) {
	macAddress = NormalizeMACAddress(macAddress)

	if !utils.ValidateMACAddress(macAddress) {
		return nil, fmt.Errorf("invalid MAC address: %s", macAddress)
	}
	if amount == 0 {
		return nil, fmt.Errorf("amount must be greater than zero")
	}

	// The client does not choose the spelling of the mint it pays: the portal
	// echoes the advertisement's price_per_step tag verbatim, and that tag is
	// accepted_mints[].url as configured — which the shipped default writes
	// WITHOUT a trailing slash. The wallet, meanwhile, registers and keys the
	// mint in canonical form ("<url>/"), so passing the caller's string through
	// unchanged missed the mint map and answered "mint does not exist" on every
	// default install. Canonicalise it once, here at the boundary where the
	// client-supplied value enters, so the allotment lookup, the mint quote and
	// the quote record all address the one registered mint (issue #375).
	mintURL = tollwallet.NormalizeMintURL(mintURL)

	if _, err := m.calculateAllotment(amount, mintURL); err != nil {
		return nil, err
	}

	now := time.Now()
	m.cleanupStaleLightningQuotes(now)

	// Bound the table before spending a mint round trip on a record that would
	// only make it bigger. Eviction (if it happened) is durable state, so it is
	// persisted immediately.
	m.lightningQuoteMu.Lock()
	activeForClient := m.countActiveQuotesLocked(macAddress)
	total := len(m.lightningQuotes)
	evicted, admitted := m.admitLightningQuoteLocked(macAddress, now)
	m.lightningQuoteMu.Unlock()
	if evicted {
		m.persistLightningQuotes()
	}
	if !admitted {
		return nil, fmt.Errorf("%w: %s holds %d active quote(s) and the table holds %d record(s)",
			ErrTooManyQuotes, macAddress, activeForClient, total)
	}

	// Our own outbound budget toward this mint. The refusal is local: the request
	// never leaves the router, so it cannot contribute to the mint's rate limit
	// (and so can never be the reason the mint answers 429).
	if !m.mintQuoteBudget.allow(mintURL) {
		return nil, fmt.Errorf("%w: %s", ErrMintBusyLocal, mintURL)
	}

	quote, err := m.tollwallet.RequestMintQuote(amount, mintURL)
	if err != nil {
		return nil, err
	}

	m.lightningQuoteMu.Lock()
	m.lightningQuotes[quote.QuoteID] = &lightningQuoteRecord{
		Bolt11:     quote.Request,
		MacAddress: macAddress,
		MintURL:    mintURL,
		Amount:     amount,
		Expiry:     quote.Expiry,
		CreatedAt:  time.Now(),
	}
	m.lightningQuoteMu.Unlock()
	m.persistLightningQuotes()

	go m.monitorLightningQuote(quote.QuoteID)

	return &LightningInvoice{
		QuoteID: quote.QuoteID,
		Invoice: quote.Request,
		MintURL: mintURL,
		Amount:  amount,
		Expiry:  quote.Expiry,
		State:   quote.State.String(),
	}, nil
}

// countActiveQuotesLocked counts the quotes a client still has outstanding: a
// record whose session was already granted has been served and does not represent
// a pending purchase. The caller must hold lightningQuoteMu.
func (m *Merchant) countActiveQuotesLocked(macAddress string) int {
	count := 0
	for _, record := range m.lightningQuotes {
		if record == nil || record.SessionGranted {
			continue
		}
		if NormalizeMACAddress(record.MacAddress) == macAddress {
			count++
		}
	}
	return count
}

// evictableLightningQuote reports whether a record may be dropped to make room for
// a new one. A record being processed must never be dropped (its grant is in
// flight), nor one that already granted access (its status is still being
// polled), and neither must a record younger than quoteEvictionMinAge: the
// customer may be paying that invoice right now, and its record is the only thing
// that recognises the payment.
func evictableLightningQuote(record *lightningQuoteRecord, now time.Time) bool {
	if record == nil || record.Processing || record.SessionGranted {
		return false
	}
	if record.CreatedAt.IsZero() {
		return false
	}
	return now.Sub(record.CreatedAt) >= quoteEvictionMinAge
}

// evictOldestEvictableQuoteLocked drops the oldest evictable record, restricted to
// onlyMAC when it is not empty, and reports whether it found one. The caller must
// hold lightningQuoteMu.
func (m *Merchant) evictOldestEvictableQuoteLocked(now time.Time, onlyMAC string) bool {
	var oldestID string
	var oldest time.Time
	for quoteID, record := range m.lightningQuotes {
		if onlyMAC != "" && NormalizeMACAddress(record.MacAddress) != onlyMAC {
			continue
		}
		if !evictableLightningQuote(record, now) {
			continue
		}
		if oldestID == "" || record.CreatedAt.Before(oldest) {
			oldestID, oldest = quoteID, record.CreatedAt
		}
	}
	if oldestID == "" {
		return false
	}
	delete(m.lightningQuotes, oldestID)
	log.Printf("evictOldestEvictableQuoteLocked: reaped abandoned quote %s (client=%s)", oldestID, onlyMAC)
	return true
}

// admitLightningQuoteLocked decides whether one more quote may be tracked, making
// room by reaping abandoned records when it must. It returns whether it evicted
// anything (so the caller can persist) and whether the request is admitted. The
// caller must hold lightningQuoteMu.
func (m *Merchant) admitLightningQuoteLocked(macAddress string, now time.Time) (evicted bool, admitted bool) {
	if m.countActiveQuotesLocked(macAddress) >= maxActiveQuotesPerMAC {
		if !m.evictOldestEvictableQuoteLocked(now, macAddress) {
			return evicted, false
		}
		evicted = true
	}

	// Hold the table at the watermark so a real customer always finds room, then
	// refuse at the hard cap rather than growing.
	for len(m.lightningQuotes) >= quoteTableEvictionWatermark {
		if !m.evictOldestEvictableQuoteLocked(now, "") {
			break
		}
		evicted = true
	}
	if len(m.lightningQuotes) >= maxActiveQuotesGlobal {
		return evicted, false
	}

	return evicted, true
}

// mintQuoteBucket is one mint's outbound budget entry.
type mintQuoteBucket struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// mintQuoteBudget is the self-imposed outbound budget toward each mint. It is the
// only thing standing between a local flood and the mint's own rate limiter: with
// it, the flood is refused at the router and the mint never sees it, so the
// mint's answer stays an honest health signal instead of a report of our own
// load. The zero value is usable, which keeps `&Merchant{}` literals in tests
// working.
type mintQuoteBudget struct {
	mu      sync.Mutex
	buckets map[string]*mintQuoteBucket
	rps     float64
	burst   int
	loaded  bool
}

// allow consumes one token for mintURL, creating the mint's bucket on first use.
// Mints are matched with tollwallet.MintURLMatches so two spellings of one mint
// share one budget.
func (b *mintQuoteBudget) allow(mintURL string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	if !b.loaded {
		b.rps = float64(envIntOr("TOLLGATE_MINT_QUOTE_RPS", mintQuoteBudgetDefaultRPS))
		b.burst = envIntOr("TOLLGATE_MINT_QUOTE_BURST", mintQuoteBudgetDefaultBurst)
		b.buckets = make(map[string]*mintQuoteBucket, 4)
		b.loaded = true
	}

	var entry *mintQuoteBucket
	for known, bucket := range b.buckets {
		if known == mintURL || tollwallet.MintURLMatches(known, mintURL) {
			entry = bucket
			break
		}
	}
	if entry == nil {
		if len(b.buckets) >= mintQuoteBudgetMaxMints {
			var oldestKey string
			var oldest time.Time
			for key, bucket := range b.buckets {
				if oldestKey == "" || bucket.lastSeen.Before(oldest) {
					oldestKey, oldest = key, bucket.lastSeen
				}
			}
			delete(b.buckets, oldestKey)
		}
		entry = &mintQuoteBucket{limiter: rate.NewLimiter(rate.Limit(b.rps), b.burst)}
		b.buckets[mintURL] = entry
	}
	entry.lastSeen = time.Now()
	return entry.limiter.Allow()
}

// envIntOr reads a positive integer override from the environment.
func envIntOr(name string, fallback int) int {
	raw := os.Getenv(name)
	if raw == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func (m *Merchant) GetLightningInvoiceStatus(quoteID, macAddress string) (*LightningQuoteStatus, error) {
	record, err := m.getLightningQuoteRecordForMAC(quoteID, macAddress)
	if err != nil {
		return nil, err
	}

	state, err := m.getLightningQuoteState(quoteID)
	if err != nil {
		return nil, err
	}

	switch state {
	case tollwallet.StatePaid, tollwallet.StateIssued:
		if err := m.ensureLightningAccessGranted(quoteID, state); err != nil {
			return nil, err
		}
	}

	record, err = m.getLightningQuoteRecordForMAC(quoteID, macAddress)
	if err != nil {
		return nil, err
	}

	statusState := state.String()
	if record.SessionGranted {
		statusState = tollwallet.StateIssued.String()
	}

	return &LightningQuoteStatus{
		QuoteID:       quoteID,
		MintURL:       record.MintURL,
		Amount:        record.Amount,
		State:         statusState,
		AccessGranted: record.SessionGranted,
		Allotment:     record.Allotment,
		Metric:        m.config.Metric,
	}, nil
}

func (m *Merchant) getLightningQuoteRecord(quoteID string) (*lightningQuoteRecord, error) {
	m.lightningQuoteMu.RLock()
	defer m.lightningQuoteMu.RUnlock()

	record, ok := m.lightningQuotes[quoteID]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrQuoteNotFound, quoteID)
	}

	copy := *record
	return &copy, nil
}

func (m *Merchant) getLightningQuoteRecordForMAC(quoteID, macAddress string) (*lightningQuoteRecord, error) {
	record, err := m.getLightningQuoteRecord(quoteID)
	if err != nil {
		return nil, err
	}
	if NormalizeMACAddress(record.MacAddress) != NormalizeMACAddress(macAddress) {
		return nil, fmt.Errorf("%w: %s", ErrQuoteNotFound, quoteID)
	}

	return record, nil
}

func (m *Merchant) getLightningQuoteState(quoteID string) (tollwallet.MintQuoteState, error) {
	m.lightningQuoteMu.RLock()
	record, ok := m.lightningQuotes[quoteID]
	if ok && record.HasCachedState && time.Since(record.CachedStateAt) < lightningQuoteStateCacheTTL {
		state := record.CachedState
		m.lightningQuoteMu.RUnlock()
		return state, nil
	}
	m.lightningQuoteMu.RUnlock()

	state, err := m.tollwallet.GetMintQuoteState(quoteID)
	if err != nil {
		return 0, err
	}

	m.lightningQuoteMu.Lock()
	if record, ok := m.lightningQuotes[quoteID]; ok {
		record.CachedState = state
		record.CachedStateAt = time.Now()
		record.HasCachedState = true
	}
	m.lightningQuoteMu.Unlock()

	return state, nil
}

func (m *Merchant) startLightningQuoteJanitor() {
	ticker := time.NewTicker(lightningQuoteCleanupInterval)
	go func() {
		defer ticker.Stop()
		for range ticker.C {
			m.cleanupStaleLightningQuotes(time.Now())
		}
	}()
}

func (m *Merchant) monitorLightningQuote(quoteID string) {
	backoff := lightningQuoteMonitorInterval

	for {
		record, err := m.getLightningQuoteRecord(quoteID)
		if err != nil {
			log.Printf("monitorLightningQuote: stopping for %s — record not found: %v", quoteID, err)
			return
		}

		now := time.Now()
		if m.shouldDeleteLightningQuote(record, now) {
			m.deleteLightningQuote(quoteID)
			log.Printf("monitorLightningQuote: stopping for %s — quote expired/settled", quoteID)
			return
		}

		state, err := m.getLightningQuoteState(quoteID)
		if err != nil {
			log.Printf("monitorLightningQuote: mint state check failed for %s: %v", quoteID, err)
			backoff = nextLightningBackoff(backoff)
			jitterSleep(backoff)
			continue
		}

		if state == tollwallet.StatePaid || state == tollwallet.StateIssued {
			if err := m.ensureLightningAccessGranted(quoteID, state); err == nil {
				log.Printf("monitorLightningQuote: stopping for %s — access granted", quoteID)
				return
			} else {
				log.Printf("monitorLightningQuote: ensureLightningAccessGranted failed for %s: %v", quoteID, err)
				backoff = nextLightningBackoff(backoff)
				jitterSleep(backoff)
				continue
			}
		}

		backoff = lightningQuoteMonitorInterval
		jitterSleep(lightningQuoteMonitorInterval)
	}
}

// nextLightningBackoff doubles the current backoff interval, capped at
// lightningQuoteMonitorMaxBackoff.
func nextLightningBackoff(current time.Duration) time.Duration {
	next := current * 2
	if next > lightningQuoteMonitorMaxBackoff {
		next = lightningQuoteMonitorMaxBackoff
	}
	return next
}

// jitterSleep sleeps for d plus a random jitter in [0, lightningQuoteMonitorMaxJitter).
func jitterSleep(d time.Duration) {
	jitter := time.Duration(rand.Int63n(int64(lightningQuoteMonitorMaxJitter)))
	time.Sleep(d + jitter)
}

func (m *Merchant) cleanupStaleLightningQuotes(now time.Time) {
	m.lightningQuoteMu.Lock()
	deleted := false
	for quoteID, record := range m.lightningQuotes {
		if m.shouldDeleteLightningQuote(record, now) {
			delete(m.lightningQuotes, quoteID)
			deleted = true
		}
	}
	m.lightningQuoteMu.Unlock()
	if deleted {
		m.persistLightningQuotes()
	}
}

func (m *Merchant) shouldDeleteLightningQuote(record *lightningQuoteRecord, now time.Time) bool {
	if record == nil || record.Processing {
		return false
	}

	if record.SessionGranted && !record.CompletedAt.IsZero() && now.Sub(record.CompletedAt) >= lightningQuoteSettledRetention {
		return true
	}

	if expiryTime, ok := lightningQuoteExpiryTime(record); ok && now.After(expiryTime.Add(lightningQuoteExpiryGracePeriod)) {
		return true
	}

	if !record.CreatedAt.IsZero() && now.Sub(record.CreatedAt) >= lightningQuoteMaxAge {
		return true
	}

	return false
}

func lightningQuoteExpiryTime(record *lightningQuoteRecord) (time.Time, bool) {
	if record == nil || record.Expiry == 0 {
		return time.Time{}, false
	}

	if record.Expiry > 1_000_000_000 {
		return time.Unix(int64(record.Expiry), 0), true
	}

	if record.CreatedAt.IsZero() {
		return time.Time{}, false
	}

	return record.CreatedAt.Add(time.Duration(record.Expiry) * time.Second), true
}

func (m *Merchant) deleteLightningQuote(quoteID string) {
	m.lightningQuoteMu.Lock()
	delete(m.lightningQuotes, quoteID)
	m.lightningQuoteMu.Unlock()
	m.persistLightningQuotes()
}

// persistLightningQuotes writes a snapshot of every lightning quote to disk so
// the in-flight set survives a restart. It is a no-op when no quoteStore is
// configured (e.g. unit tests that construct &Merchant{} directly). Write
// errors are logged but never propagated: losing the persistence side-effect
// must not break payment processing.
func (m *Merchant) persistLightningQuotes() {
	if m.quoteStore == nil {
		return
	}

	m.lightningQuoteMu.RLock()
	snapshot := make(map[string]*lightningQuoteRecord, len(m.lightningQuotes))
	for id, rec := range m.lightningQuotes {
		cp := *rec
		snapshot[id] = &cp
	}
	m.lightningQuoteMu.RUnlock()

	if err := m.quoteStore.saveQuotes(snapshot); err != nil {
		log.Printf("ERROR: failed to persist lightning quotes: %v", err)
	}
}

// loadLightningQuotesFromDisk restores persisted quotes at startup. Expired or
// fully-settled quotes are dropped; unsettled (unpaid or recently-paid) quotes
// are reloaded into the in-memory map and their monitor goroutines relaunched
// so access is granted if the invoice was paid while the process was down.
func (m *Merchant) loadLightningQuotesFromDisk() {
	if m.quoteStore == nil {
		return
	}

	persisted, err := m.quoteStore.loadQuotes()
	if err != nil {
		log.Printf("ERROR: failed to load persisted lightning quotes: %v", err)
		return
	}
	if len(persisted) == 0 {
		return
	}

	now := time.Now()
	relaunched := 0
	for quoteID, pq := range persisted {
		rec := &lightningQuoteRecord{
			Bolt11:         pq.Bolt11,
			MacAddress:     pq.MacAddress,
			MintURL:        pq.MintURL,
			Amount:         pq.Amount,
			Expiry:         pq.Expiry,
			Allotment:      pq.Allotment,
			CreatedAt:      pq.CreatedAt,
			CompletedAt:    pq.CompletedAt,
			SessionGranted: pq.SessionGranted,
		}

		// Drop quotes that are expired, too old, or past the settled
		// retention window — the janitor would remove them anyway.
		if m.shouldDeleteLightningQuote(rec, now) {
			continue
		}

		m.lightningQuoteMu.Lock()
		m.lightningQuotes[quoteID] = rec
		m.lightningQuoteMu.Unlock()

		// Only quotes that still need monitoring are relaunched. A quote
		// whose session was already granted is kept (so status lookups
		// succeed) but does not need a polling goroutine.
		if !rec.SessionGranted {
			go m.monitorLightningQuote(quoteID)
			relaunched++
		}
	}

	if relaunched > 0 {
		log.Printf("Restored %d lightning quote(s) from disk, %d monitor(s) relaunched", len(persisted), relaunched)
	}

	// Persist again so the on-disk file reflects any quotes dropped above.
	m.persistLightningQuotes()
}

func (m *Merchant) ensureLightningAccessGranted(quoteID string, state tollwallet.MintQuoteState) error {
	m.lightningQuoteMu.Lock()
	record, ok := m.lightningQuotes[quoteID]
	if !ok {
		m.lightningQuoteMu.Unlock()
		return fmt.Errorf("%w: %s", ErrQuoteNotFound, quoteID)
	}
	if record.SessionGranted || record.Processing {
		m.lightningQuoteMu.Unlock()
		return nil
	}
	record.Processing = true
	recordCopy := *record
	m.lightningQuoteMu.Unlock()

	amountToGrant := recordCopy.Amount
	if state == tollwallet.StatePaid {
		mintedAmount, err := m.tollwallet.MintTokens(quoteID)
		if err != nil {
			m.lightningQuoteMu.Lock()
			if record, ok := m.lightningQuotes[quoteID]; ok {
				record.Processing = false
			}
			m.lightningQuoteMu.Unlock()
			// The customer paid and the tokens could not be issued. That is a
			// paid purchase with nothing to show for it, so it is logged at
			// ERROR naming the client (the operator's only surface) rather
			// than left to a caller that might swallow it.
			log.Printf("ERROR: a PAID purchase could not be completed: client %s (quote %s, mint %s) paid %d sat and the mint could not issue the tokens: %v — no access was granted and no allotment was applied; the purchase is NOT complete and the grant is retried",
				recordCopy.MacAddress, quoteID, recordCopy.MintURL, amountToGrant, err)
			return fmt.Errorf("%w: client %s, quote %s: the mint could not issue the tokens: %v", ErrAccessGrantNotApplied, recordCopy.MacAddress, quoteID, err)
		}
		amountToGrant = mintedAmount
	}

	_, allotment, err := m.grantAccessForAmount(recordCopy.MacAddress, amountToGrant, recordCopy.MintURL)
	if err != nil {
		m.lightningQuoteMu.Lock()
		if record, ok := m.lightningQuotes[quoteID]; ok {
			record.Processing = false
		}
		m.lightningQuoteMu.Unlock()
		// The money is in the operator's wallet and the customer has nothing:
		// the exact failure measured on the bench MT3000 (2026-09-26) as
		// "state=PAID, merchant wallet +1 sat, access_granted never true". It
		// must never be a silent no-op — the operator gets one ERROR line that
		// names the client, and the caller gets an error it can surface.
		log.Printf("ERROR: a PAID purchase could not be granted: client %s (quote %s, mint %s, %d sat) paid and the grant could not be applied: %v — no allotment was applied (it was rolled back), the purchase is NOT complete, and access is retried; this must not be reported as a success",
			recordCopy.MacAddress, quoteID, recordCopy.MintURL, amountToGrant, err)
		return fmt.Errorf("%w: client %s, quote %s: %v", ErrAccessGrantNotApplied, recordCopy.MacAddress, quoteID, err)
	}

	m.lightningQuoteMu.Lock()
	if record, ok := m.lightningQuotes[quoteID]; ok {
		record.Processing = false
		record.SessionGranted = true
		record.Allotment = allotment
		record.CompletedAt = time.Now()
	}
	m.lightningQuoteMu.Unlock()
	m.persistLightningQuotes()

	return nil
}

func (m *Merchant) grantAccessForAmount(macAddress string, amountSats uint64, mintURL string) (*CustomerSession, uint64, error) {
	allotment, err := m.calculateAllotment(amountSats, mintURL)
	if err != nil {
		return nil, 0, err
	}

	session, err := m.grantSessionAccess(macAddress, allotment)
	if err != nil {
		return nil, 0, err
	}

	return session, allotment, nil
}

func (m *Merchant) grantSessionAccess(macAddress string, allotment uint64) (*CustomerSession, error) {
	macAddress = NormalizeMACAddress(macAddress)

	previousSession, hadSession := m.snapshotSession(macAddress)

	session, err := m.AddAllotment(macAddress, m.config.Metric, allotment)
	if err != nil {
		return nil, err
	}

	if err := openGateForSession(macAddress, session); err != nil {
		m.restoreSession(macAddress, previousSession, hadSession)
		return nil, err
	}

	return session, nil
}

func openGateForSession(macAddress string, session *CustomerSession) error {
	switch session.Metric {
	case "milliseconds":
		endTimestamp := session.StartTime + int64(session.Allotment/1000)
		if err := valve.OpenGateUntil(macAddress, endTimestamp); err != nil {
			return fmt.Errorf("failed to open gate: %w", err)
		}
	case "bytes":
		// Always call OpenGate — ndsctl auth is idempotent.
		// Previous code skipped this when a data baseline existed, but after
		// a deauth the gate is closed while the baseline persists, causing
		// Cashu payments to return success without actually opening the gate.
		if err := valve.OpenGate(macAddress); err != nil {
			return fmt.Errorf("failed to open gate: %w", err)
		}
	default:
		return fmt.Errorf("unsupported metric: %s", session.Metric)
	}

	return nil
}
