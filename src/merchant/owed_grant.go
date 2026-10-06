// The owed-entitlement half of issue #403: a Cashu payment whose Receive
// succeeded but whose gate could not be opened must leave the customer with
// service or a recoverable claim — never a silently consumed note. The
// value is already in the operator's wallet; what is owed is the grant.
//
// The mechanism mirrors the Lightning quote machinery (lightning.go,
// quote_store.go): a durably persisted record per paid-but-ungranted
// purchase, a per-record monitor goroutine with backoff that retries the
// grant (allotment + gate open) until it succeeds, an expiry bound so a
// permanently broken gate converges to a loud terminal state instead of
// retrying forever, and a startup loader that relaunches the monitors so a
// restart cannot erase an already-received customer's entitlement.
package merchant

import (
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	owedGrantStateOwed    = "owed"
	owedGrantStateGranted = "granted"
	owedGrantStateExpired = "expired"

	owedGrantRetryInterval = 5 * time.Second
	owedGrantMaxBackoff    = 30 * time.Second
	owedGrantMaxJitter     = 500 * time.Millisecond

	// owedGrantBytesMaxAge bounds how long a data (bytes) entitlement keeps
	// retrying: bytes do not expire on their own, but the customer's
	// expectation does. A bytes grant older than this converges to the
	// expired state with an operator-actionable ERROR instead of retrying
	// forever; the record is kept for audit.
	owedGrantBytesMaxAge = 24 * time.Hour
)

// persistedOwedGrant is the serializable form of owedGrantRecord. Processing
// is transient in-memory state and is intentionally excluded, exactly like
// the lightning quote record.
type persistedOwedGrant struct {
	Reference     string    `json:"reference"`
	MacAddress    string    `json:"mac_address"`
	MintURL       string    `json:"mint_url"`
	AmountSats    uint64    `json:"amount_sats"`
	Allotment     uint64    `json:"allotment"`
	Metric        string    `json:"metric"`
	CreatedAt     time.Time `json:"created_at"`
	GrantedAt     time.Time `json:"granted_at"`
	ExpiredAt     time.Time `json:"expired_at"`
	State         string    `json:"state"`
	Attempts      int       `json:"attempts"`
	LastError     string    `json:"last_error"`
	LastAttemptAt time.Time `json:"last_attempt_at"`
}

type owedGrantRecord struct {
	Reference     string
	MacAddress    string
	MintURL       string
	AmountSats    uint64
	Allotment     uint64
	Metric        string
	CreatedAt     time.Time
	GrantedAt     time.Time
	ExpiredAt     time.Time
	State         string
	Attempts      int
	LastError     string
	LastAttemptAt time.Time
	Processing    bool
}

// owedGrantStore persists owed-grant records with atomic writes (temp file +
// rename + fsync), the same discipline as the quote store.
type owedGrantStore struct {
	filePath string
	mu       sync.Mutex
}

func newOwedGrantStore(filePath string) *owedGrantStore {
	return &owedGrantStore{filePath: filePath}
}

func (s *owedGrantStore) saveGrants(grants map[string]*owedGrantRecord) error {
	persisted := make(map[string]*persistedOwedGrant, len(grants))
	for ref, rec := range grants {
		if rec == nil {
			continue
		}
		persisted[ref] = &persistedOwedGrant{
			Reference:     rec.Reference,
			MacAddress:    rec.MacAddress,
			MintURL:       rec.MintURL,
			AmountSats:    rec.AmountSats,
			Allotment:     rec.Allotment,
			Metric:        rec.Metric,
			CreatedAt:     rec.CreatedAt,
			GrantedAt:     rec.GrantedAt,
			ExpiredAt:     rec.ExpiredAt,
			State:         rec.State,
			Attempts:      rec.Attempts,
			LastError:     rec.LastError,
			LastAttemptAt: rec.LastAttemptAt,
		}
	}

	data, err := json.MarshalIndent(persisted, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal owed grants: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if dir := filepath.Dir(s.filePath); dir != "" {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return fmt.Errorf("create owed-grants dir %s: %w", dir, err)
		}
	}

	tmp, err := os.CreateTemp(filepath.Dir(s.filePath), ".owed-grants-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp owed-grants file: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp owed-grants file: %w", err)
	}
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return fmt.Errorf("chmod temp owed-grants file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync temp owed-grants file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp owed-grants file: %w", err)
	}
	if err := os.Rename(tmpName, s.filePath); err != nil {
		return fmt.Errorf("rename temp owed-grants file: %w", err)
	}
	cleanup = false
	return nil
}

func (s *owedGrantStore) loadGrants() (map[string]*persistedOwedGrant, error) {
	data, err := os.ReadFile(s.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]*persistedOwedGrant), nil
		}
		return nil, fmt.Errorf("read owed-grants file: %w", err)
	}

	grants := make(map[string]*persistedOwedGrant)
	if err := json.Unmarshal(data, &grants); err != nil {
		return nil, fmt.Errorf("unmarshal owed grants: %w", err)
	}
	return grants, nil
}

// recordOwedGrant durably records a paid purchase whose grant failed, then
// starts (or re-arms) the monitor that retries the grant. The record is the
// customer's recoverable claim: it must exist on disk before the HTTP
// response that tells the customer their payment is being applied — a crash
// after the response but before the write would otherwise erase the
// entitlement. A persist failure does not cancel the entitlement: the record
// is still tracked in memory and the monitor still runs, but the operator
// gets an ERROR because a restart would lose the claim.
func (m *Merchant) recordOwedGrant(reference, macAddress, mintURL string, amountSats, allotment uint64) {
	if reference == "" {
		// Without a reference the claim cannot be keyed. The payment is
		// logged loudly below (the caller also logs); nothing else can be
		// done without a key to deduplicate on.
		log.Printf("ERROR: a paid purchase could not be granted and has no quotable reference — operator must reconcile manually: client %s mint %s amount %d",
			macAddress, mintURL, amountSats)
		return
	}

	rec := &owedGrantRecord{
		Reference:  reference,
		MacAddress: NormalizeMACAddress(macAddress),
		MintURL:    mintURL,
		AmountSats: amountSats,
		Allotment:  allotment,
		Metric:     m.config.Metric,
		CreatedAt:  time.Now(),
		State:      owedGrantStateOwed,
	}

	m.owedGrantsMu.Lock()
	if m.owedGrants == nil {
		m.owedGrants = make(map[string]*owedGrantRecord)
	}
	if existing, ok := m.owedGrants[reference]; ok && existing.State == owedGrantStateOwed {
		// The same note is already owed (a retried submission that slipped
		// past every guard): the claim exists, do not duplicate it.
		m.owedGrantsMu.Unlock()
		return
	}
	m.owedGrants[reference] = rec
	m.owedGrantsMu.Unlock()

	if m.owedGrantStore == nil {
		// No store configured (a &Merchant{} literal): the entitlement is
		// in-memory only. The loud ERROR below is the operator surface.
		log.Printf("WARNING: owed entitlement for client %s (reference %s) has no durable store — it survives only until restart",
			macAddress, reference)
	} else {
		m.persistOwedGrants()
	}

	log.Printf("ERROR: a PAID purchase could not be granted: client %s (mint %s, %d sat, allotment %d %s, reference %s) — the value is in the operator's wallet and the grant is owed; it will be retried until it succeeds or its deadline passes. This must not be reported as a failure to the customer's wallet.",
		macAddress, mintURL, amountSats, allotment, rec.Metric, reference)

	go m.monitorOwedGrant(reference)
}

// persistOwedGrants writes a snapshot of every owed grant. No-op without a
// store (unit tests constructing &Merchant{} directly), errors logged.
func (m *Merchant) persistOwedGrants() {
	if m.owedGrantStore == nil {
		return
	}

	m.owedGrantsMu.Lock()
	snapshot := make(map[string]*owedGrantRecord, len(m.owedGrants))
	for ref, rec := range m.owedGrants {
		cp := *rec
		snapshot[ref] = &cp
	}
	m.owedGrantsMu.Unlock()

	if err := m.owedGrantStore.saveGrants(snapshot); err != nil {
		log.Printf("ERROR: failed to persist owed grants: %v", err)
	}
}

// owedGrantDeadline is when the entitlement stops being grantable: a time
// grant is worthless after its window has passed; a data grant is bounded by
// owedGrantBytesMaxAge.
func owedGrantDeadline(rec *owedGrantRecord) time.Time {
	if rec.Metric == "milliseconds" {
		return rec.CreatedAt.Add(time.Duration(rec.Allotment) * time.Millisecond)
	}
	return rec.CreatedAt.Add(owedGrantBytesMaxAge)
}

// monitorOwedGrant retries one owed grant with backoff until it is granted,
// expires, or its record disappears. Exits are terminal: a granted or expired
// record is never revisited by this goroutine.
func (m *Merchant) monitorOwedGrant(reference string) {
	backoff := owedGrantRetryInterval

	for {
		m.owedGrantsMu.Lock()
		rec, ok := m.owedGrants[reference]
		if !ok || rec.State != owedGrantStateOwed || rec.Processing {
			m.owedGrantsMu.Unlock()
			return
		}
		deadline := owedGrantDeadline(rec)
		m.owedGrantsMu.Unlock()

		if time.Now().After(deadline) {
			m.expireOwedGrant(reference)
			return
		}

		// Wait before every attempt, including the first: the grant this
		// monitor is retrying failed moments ago inside the caller's
		// request (the valve's own auth-retry loop already spent its budget
		// on the same socket), and immediately re-running it only hammers a
		// wedged ndsctl — the storm that stops a PAID purchase from being
		// authorised at all.
		owedGrantSleep(backoff)
		if backoff < owedGrantMaxBackoff {
			backoff *= 2
			if backoff > owedGrantMaxBackoff {
				backoff = owedGrantMaxBackoff
			}
		}

		if err := m.ensureOwedGrantGranted(reference); err == nil {
			return
		}
	}
}

// owedGrantSleep sleeps for d plus a random jitter in [0, owedGrantMaxJitter).
func owedGrantSleep(d time.Duration) {
	jitter := time.Duration(rand.Int63n(int64(owedGrantMaxJitter)))
	time.Sleep(d + jitter)
}

// expireOwedGrant converges an ungrantable entitlement to its terminal state:
// the record is kept (audit + operator visibility) and the operator gets one
// ERROR naming the client and the reference, because the value is in the
// operator's wallet while the customer has neither service nor a live claim.
func (m *Merchant) expireOwedGrant(reference string) {
	m.owedGrantsMu.Lock()
	rec, ok := m.owedGrants[reference]
	if !ok || rec.State != owedGrantStateOwed {
		m.owedGrantsMu.Unlock()
		return
	}
	rec.State = owedGrantStateExpired
	rec.ExpiredAt = time.Now()
	m.owedGrantsMu.Unlock()
	m.persistOwedGrants()

	log.Printf("ERROR: an owed entitlement EXPIRED ungranted: client %s (reference %s, allotment %d %s) — the customer's value is in the operator's wallet and the grant window has passed; manual remedy (grant or refund) is operator action",
		rec.MacAddress, reference, rec.Allotment, rec.Metric)
}

// ensureOwedGrantGranted attempts the grant exactly once per call, with
// in-memory exactly-once semantics across concurrent callers: only one
// processor at a time may run grantSessionAccess for a reference.
func (m *Merchant) ensureOwedGrantGranted(reference string) error {
	m.owedGrantsMu.Lock()
	rec, ok := m.owedGrants[reference]
	if !ok {
		m.owedGrantsMu.Unlock()
		return nil
	}
	if rec.State != owedGrantStateOwed || rec.Processing {
		m.owedGrantsMu.Unlock()
		return nil
	}
	rec.Processing = true
	copy := *rec
	m.owedGrantsMu.Unlock()

	fail := func(err error) error {
		m.owedGrantsMu.Lock()
		if current, ok := m.owedGrants[reference]; ok && current.State == owedGrantStateOwed {
			current.Processing = false
			current.Attempts++
			current.LastError = err.Error()
			current.LastAttemptAt = time.Now()
		}
		m.owedGrantsMu.Unlock()
		m.persistOwedGrants()
		return err
	}

	_, err := m.grantSessionAccess(copy.MacAddress, copy.Allotment)
	if err != nil {
		return fail(err)
	}

	m.owedGrantsMu.Lock()
	if current, ok := m.owedGrants[reference]; ok {
		current.State = owedGrantStateGranted
		current.GrantedAt = time.Now()
		current.Processing = false
		current.LastError = ""
	}
	m.owedGrantsMu.Unlock()
	m.persistOwedGrants()

	log.Printf("Owed grant applied: client %s (reference %s, allotment %d %s) — the paid purchase now has its session",
		copy.MacAddress, reference, copy.Allotment, copy.Metric)
	return nil
}

// loadOwedGrantsFromDisk restores persisted owed grants at startup and
// relaunches their monitors, so a restart during the unresolved state
// converges instead of erasing the entitlement.
func (m *Merchant) loadOwedGrantsFromDisk() {
	if m.owedGrantStore == nil {
		return
	}

	persisted, err := m.owedGrantStore.loadGrants()
	if err != nil {
		log.Printf("ERROR: failed to load persisted owed grants: %v", err)
		return
	}
	if len(persisted) == 0 {
		return
	}

	now := time.Now()
	relaunched := 0
	for ref, pg := range persisted {
		rec := &owedGrantRecord{
			Reference:     pg.Reference,
			MacAddress:    pg.MacAddress,
			MintURL:       pg.MintURL,
			AmountSats:    pg.AmountSats,
			Allotment:     pg.Allotment,
			Metric:        pg.Metric,
			CreatedAt:     pg.CreatedAt,
			GrantedAt:     pg.GrantedAt,
			ExpiredAt:     pg.ExpiredAt,
			State:         pg.State,
			Attempts:      pg.Attempts,
			LastError:     pg.LastError,
			LastAttemptAt: pg.LastAttemptAt,
		}

		if rec.State != owedGrantStateOwed {
			continue
		}
		if now.After(owedGrantDeadline(rec)) {
			// The window passed while the process was down.
			m.owedGrantsMu.Lock()
			m.owedGrants[ref] = rec
			m.owedGrantsMu.Unlock()
			continue
		}

		m.owedGrantsMu.Lock()
		m.owedGrants[ref] = rec
		m.owedGrantsMu.Unlock()
		go m.monitorOwedGrant(ref)
		relaunched++
	}

	// Converge records whose deadline passed while the process was down, and
	// drop nothing: terminal records stay for audit.
	m.owedGrantsMu.Lock()
	var expired []string
	for ref, rec := range m.owedGrants {
		if rec.State == owedGrantStateOwed && now.After(owedGrantDeadline(rec)) {
			expired = append(expired, ref)
		}
	}
	m.owedGrantsMu.Unlock()
	for _, ref := range expired {
		m.expireOwedGrant(ref)
	}

	if relaunched > 0 {
		log.Printf("Restored %d owed entitlement(s) from disk, %d monitor(s) relaunched", len(persisted), relaunched)
	}

	m.persistOwedGrants()
}
