package merchant

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	gonutsclient "github.com/OpenTollGate/gonuts-tollgate/wallet/client"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/lightning"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/tollwallet"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/utils"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/valve"
	"github.com/nbd-wtf/go-nostr"
)

// CustomerSession represents an active session
type CustomerSession struct {
	MacAddress string
	StartTime  int64  // Unix timestamp
	Metric     string // "milliseconds" or "bytes"
	Allotment  uint64 // Total allotment for this session

	// Consumed is the byte total this session carries in from attachments it has
	// already left behind: a MAC rotation (a rebind) carries it forward, so N
	// rotations can never restart the meter. The session's effective usage is
	// Consumed plus what the CURRENT attachment has used since its own baseline
	// (sessionBytesUsage), which is why the per-MAC baseline stays a measurement
	// detail rather than the ledger.
	Consumed uint64

	// attachmentUsage is the highest usage seen for the CURRENT attachment since
	// its own baseline. It is what a rebind carries when the attachment's counters
	// are already gone from NoDogSplash, so the last interval before a rotation
	// cannot vanish. Unexported: it is the merchant's own bookkeeping, not part of
	// the session's API.
	attachmentUsage uint64

	// ticketHandle is the session-ticket handle this record was issued for. A
	// ticket names one session, and only the one it was issued for: a rebind
	// through an older ticket cannot move a session this record never issued one
	// for. Unexported, and never on the wire.
	ticketHandle string

	// ticketExpiresAt is the horizon (Unix seconds) of the ticket issued for this
	// session; zero means no ticket was ever issued. It lives on the record rather
	// than being read from the ticket store on purpose: the package's documented
	// lock order is ts.mu then sessionMu, and the retirement paths below hold
	// sessionMu alone, so consulting the store there would invert that order. It is
	// what lets the janitor and the unmeterable-session path tell a session whose
	// remainder is still claimable from one nobody can come back for.
	ticketExpiresAt int64
}

// SessionState is the machine-readable lifecycle state of the session of one
// client MAC. It exists because the usage contract cannot express it: `/usage`
// answers "-1/-1" both for a device that has never paid and for one whose paid
// session ran out, so a portal cannot tell a first-time visitor from a customer
// whose session just ended — and cannot offer a renewal.
type SessionState string

const (
	// SessionStateNone — no session, and none observed to expire while this
	// process has been running.
	SessionStateNone SessionState = "none"
	// SessionStateActive — a session exists with allotment left.
	SessionStateActive SessionState = "active"
	// SessionStateExpired — the MAC had a session that is used up: the record
	// was retired by the milliseconds lookup, by the usage monitor reaching the
	// allotment, or by the renewal that superseded it.
	SessionStateExpired SessionState = "expired"
)

// Sentinels for the two "no usable session" answers of GetSession, so callers
// can tell them apart without matching messages. The wrapped text keeps the
// original wording ("session expired for MAC address: %s").
var (
	ErrSessionNotFound = errors.New("session not found")
	ErrSessionExpired  = errors.New("session expired")
)

// sessionHistoryTTL bounds how long an observed expiry is remembered. The
// history exists so /session-state can keep answering "expired" after the record
// itself is gone; a day covers the window in which a customer renews, and the
// entry is a hint, not a ledger (it is process-memory, like the sessions).
const sessionHistoryTTL = 24 * time.Hour

// sessionHistoryMaxEntries caps the history so a busy router cannot grow it
// without bound between TTL sweeps.
const sessionHistoryMaxEntries = 4096

// sessionHasExpired reports whether a session's allotment is used up. Only the
// milliseconds metric can be judged from the record alone: byte allotments are
// measured against NDS counters by checkDataUsage, which closes the gate and
// retires the record when it sees the allotment reached.
func sessionHasExpired(session *CustomerSession, now time.Time) bool {
	if session == nil || session.Metric != "milliseconds" {
		return false
	}
	elapsed := now.Sub(time.Unix(session.StartTime, 0))
	if elapsed < 0 {
		return false
	}
	return uint64(elapsed.Milliseconds()) >= session.Allotment
}

// expireSessionLocked retires the session record of macAddress and remembers
// that this MAC has spent one, so /session-state keeps answering "expired" for
// it afterwards. Caller must hold sessionMu for writing.
func (m *Merchant) expireSessionLocked(macAddress string) {
	delete(m.customerSessions, macAddress)
	m.rememberExpiredSessionLocked(macAddress)
}

// retireSessionOrParkForTicketLocked retires the session record of macAddress —
// unless the session still holds a LIVE session ticket, in which case the record is
// PARKED: kept, with no access attached to it.
//
// Why parking exists. Entitlement is keyed to a MAC address, and the address
// belongs to the device that chose it, so a device that rotates its address leaves
// its record behind. The stale-binding janitor retires such a record ~60s after the
// address drops off the NDS client list, and the unmeterable-session path does the
// same when the meter cannot be read — which is what a departed client looks like on
// the bytes metric. Both are right about ACCESS: the gate is closed either way and
// nothing is left authorised. But retiring the record used to destroy the only
// record of what the customer paid for, so a device that took longer than the grace
// window to come back presented a perfectly valid ticket and got ErrTicketUnknown:
// the remainder was gone. This branch exists to carry that remainder to the new
// address, so retirement now yields to a live ticket.
//
// What parking does NOT do: it grants nothing. The gate is already deauthorised and
// NoDogSplash no longer lists the address, and a rebind still has to authorise its
// new attachment before the customer has any access. Parking only keeps the meter,
// the StartTime and the handle mapping alive long enough to be carried.
//
// Bound: a parked record lives only until the ticket's own horizon
// (defaultSessionTicketTTL, 12h). After that the next pass that asks retires it, so
// parking cannot accumulate records indefinitely.
//
// Caller must hold sessionMu.
func (m *Merchant) retireSessionOrParkForTicketLocked(macAddress string, now time.Time) (retired, parked bool) {
	session, exists := m.customerSessions[macAddress]
	if !exists {
		return false, false
	}
	if session.ticketExpiresAt > now.Unix() {
		return false, true
	}

	m.expireSessionLocked(macAddress)
	return true, false
}

// rememberExpiredSessionLocked records an observed expiry. Caller must hold
// sessionMu for writing.
func (m *Merchant) rememberExpiredSessionLocked(macAddress string) {
	now := time.Now()
	if m.expiredSessions == nil {
		m.expiredSessions = make(map[string]int64)
	}
	m.expiredSessions[macAddress] = now.Unix()

	cutoff := now.Add(-sessionHistoryTTL).Unix()
	oldestMAC := ""
	oldest := now.Unix()
	for mac, when := range m.expiredSessions {
		if when < cutoff {
			delete(m.expiredSessions, mac)
			continue
		}
		if when < oldest {
			oldest, oldestMAC = when, mac
		}
	}
	if len(m.expiredSessions) > sessionHistoryMaxEntries && oldestMAC != "" && oldestMAC != macAddress {
		delete(m.expiredSessions, oldestMAC)
	}
}

// sessionKnownToHaveExpiredLocked reports whether macAddress is remembered as
// having had a session that ran out. Caller must hold sessionMu for reading.
func (m *Merchant) sessionKnownToHaveExpiredLocked(macAddress string) bool {
	when, ok := m.expiredSessions[macAddress]
	if !ok {
		return false
	}
	return time.Since(time.Unix(when, 0)) <= sessionHistoryTTL
}

// The pre-flight's test seam is per merchant (clientProbe and
// clientProbeDelay below), not a package-level variable. It used to be one, and
// a test writing it raced with the usage monitor goroutine of an earlier test's
// merchant, which reads the default probe through the reconciliation's
// fallback; the full-suite `-race` gate caught it. See setClientProbe.

// preflightProbeAttempts mirrors the valve auth-retry budget: the reseller
// flow's upstream NDS registers client sessions asynchronously, so absence at
// first probe is not final.
const preflightProbeAttempts = 5

// preflightRetryDelayDefault is how long the pre-flight waits between two
// probes. It is the zero-value default of Merchant.clientProbeDelay.
const preflightRetryDelayDefault = 400 * time.Millisecond

// receiveTimeout bounds how long PurchaseSession waits for the mint's answer to
// a money-moving `Receive` before it answers the customer with "outcome
// unknown". It is a var so a test can shrink the window instead of waiting it
// out; nothing in production reassigns it, and — unlike the pre-flight's delay
// (Merchant.clientProbeDelay, which is per merchant because the usage monitor
// reads it from its own goroutine) — every reader of this one is on the
// goroutine that called PurchaseSession.
var receiveTimeout = 30 * time.Second

// receiveResult is the answer of one money-moving `Receive` call.
type receiveResult struct {
	amount uint64
	err    error
}

// recordLateReceiveOutcome waits for the answer of a `Receive` that outlived the
// response deadline and writes it to the log beside the reference the notice gave
// the customer. It is deliberately log-only: it grants no session and moves no
// money, so it must not read as if the customer had been served — the journal
// that credits or refunds a late outcome is separate work. Its whole job is to
// make the reference the customer quotes answerable today, because the operator
// cannot tell a late success (the mint took the note) from a late failure (it
// did not) without it.
func (m *Merchant) recordLateReceiveOutcome(ch <-chan receiveResult, mintURL, macAddress, reference string, tokenAmount uint64) {
	res := <-ch
	if res.err != nil {
		// An error is not a refusal. A `Receive` that answers after the deadline
		// most often answers with a transport error, because the wallet's own
		// HTTP client spends the same 30-second budget as receiveTimeout: a swap
		// POST that starts a beat later times out a beat after this module's
		// deadline, and a mint 5xx answered after it processed the swap is
		// ambiguous the same way. Only a refusal the mint itself returned proves
		// the note was not taken. The intent journal (#502) now turns each
		// answer into a decision: a definitive refusal abandons the attempt
		// (the customer keeps a spendable note), an ambiguous answer triggers
		// the NUT-07 reconciler — evidence, never a guess.
		if isAmbiguousMintOutcomeError(res.err) {
			log.Printf("PurchaseSession: late Receive FAILED (outcome still ambiguous) for mint=%s mac=%s reference=%s: %v — reconciling on NUT-07 evidence",
				mintURL, macAddress, reference, res.err)
			m.reconcileReceiveIntent(reference)
			return
		}
		log.Printf("PurchaseSession: late Receive FAILED for mint=%s mac=%s reference=%s: %v — the mint did not take the note",
			mintURL, macAddress, reference, res.err)
		m.resolveReceiveIntent(reference, intentStateAbandoned, fmt.Sprintf("late definitive failure: %v", res.err))
		return
	}
	// The mint answered success after our deadline: the value is recoverable
	// by the operator (NUT-09 restore) and the customer is owed service. The
	// fee-adjusted amount is not knowable here (only the pre-swap token
	// amount is), so the owed grant prices from the token amount — bounded
	// over-grant by the swap fee, noted in the journal record.
	log.Printf("PurchaseSession: late Receive COMPLETED for mint=%s mac=%s reference=%s amount=%d — recording the owed entitlement",
		mintURL, macAddress, reference, res.amount)
	allotment, allotErr := m.calculateAllotment(tokenAmount, mintURL)
	if allotErr != nil {
		log.Printf("ReceiveIntent: late success for reference %s could not be priced (%v) — reconciler will retry", reference, allotErr)
		return
	}
	m.resolveReceiveIntent(reference, intentStateOwed, "late success after deadline; owed entitlement recorded")
	m.recordOwedGrant(reference, macAddress, mintURL, tokenAmount, allotment)
}

// receiveReference is the operator-facing handle for one money-moving attempt:
// the salted fingerprint of the customer's note, which the customer can quote
// and the operator can find in the log next to the MAC, the mint and the time.
// It is never the note itself — the note is spendable by whoever reads it — and
// it is deliberately opaque: the reference identifies one attempt without
// telling a reader anything they could act on. An empty string means the note
// could not be serialized, in which case the notice omits the reference rather
// than inventing one.
func receiveReference(token tollwallet.Token) string {
	if token == nil {
		return ""
	}
	serialized, err := token.Serialize()
	if err != nil {
		log.Printf("PurchaseSession: could not serialise the note for a reference: %v", err)
		return ""
	}
	return utils.TokenFingerprint(serialized)
}

// beginReceive marks one note's Receive as in flight and reports whether the
// caller is the first to do so. A false return means the same note is already
// being processed by another PurchaseSession on this router: the caller must
// refuse WITHOUT touching the mint — the in-flight Receive may already have
// spent the proofs, and a second submission is at best a wasted round trip and
// at worst a second grant when both swaps race past the mint's spend-state
// (#639). An empty reference means the note could not be serialised; the guard
// is then unusable and the payment proceeds (the mint still refuses a
// sequential resubmit as spent) rather than failing commerce over guard
// machinery.
func (m *Merchant) beginReceive(reference string) bool {
	if reference == "" {
		return true
	}
	m.receiveInFlightMu.Lock()
	defer m.receiveInFlightMu.Unlock()
	if m.receiveInFlight == nil {
		m.receiveInFlight = make(map[string]struct{})
	}
	if _, inFlight := m.receiveInFlight[reference]; inFlight {
		return false
	}
	m.receiveInFlight[reference] = struct{}{}
	return true
}

// endReceive releases the in-flight mark once the Receive outcome has been
// consumed — by the main path or, on the outcome-unknown timeout path, by the
// late recorder that owns the result channel from the deadline onward.
func (m *Merchant) endReceive(reference string) {
	if reference == "" {
		return
	}
	m.receiveInFlightMu.Lock()
	delete(m.receiveInFlight, reference)
	m.receiveInFlightMu.Unlock()
}

// MerchantInterface defines the interface for merchant payment operations
type MerchantInterface interface {
	CreatePaymentToken(mintURL string, amount uint64) (string, error)
	CreatePaymentTokenWithOverpayment(mintURL string, amount uint64, maxOverpaymentPercent uint64, maxOverpaymentAbsolute uint64) (string, error)
	DrainMint(mintURL string) (string, uint64, error)
	RequestLightningInvoice(macAddress, mintURL string, amount uint64) (*LightningInvoice, error)
	GetLightningInvoiceStatus(quoteID, macAddress string) (*LightningQuoteStatus, error)
	GetAcceptedMints() []config_manager.MintConfig
	GetBalance() uint64
	GetBalanceByMint(mintURL string) uint64
	GetAllMintBalances() map[string]uint64
	PurchaseSession(cashuToken string, macAddress string) (*nostr.Event, error)
	GetAdvertisement() string
	StartPayoutRoutine()
	StartDataUsageMonitoring()
	CreateNoticeEvent(level, code, message, customerPubkey string) (*nostr.Event, error)
	GetSession(macAddress string) (*CustomerSession, error)
	GetSessionState(macAddress string) (SessionState, error)
	AddAllotment(macAddress, metric string, amount uint64) (*CustomerSession, error)
	GetUsage(macAddress string) (string, error)
	Fund(cashuToken string) (uint64, error)
	SetOnReachableSetChanged(callback func())
	IssueSessionTicket(macAddress string) (string, int64, error)
	RebindSession(ticket, macAddress string) (*CustomerSession, error)
}

// Merchant represents the financial decision maker for the tollgate
type Merchant struct {
	config            *config_manager.Config
	configManager     *config_manager.ConfigManager
	tollwallet        tollwallet.WalletPort
	mintHealthTracker *MintHealthTracker
	customerSessions  map[string]*CustomerSession
	expiredSessions   map[string]int64
	sessionMu         sync.RWMutex
	// receiveInFlight holds the receiveReference of every PurchaseSession
	// whose money-moving Receive has started and whose outcome has not been
	// consumed yet (including the outcome-unknown window past the deadline,
	// where the late recorder owns the result). It exists because the mint's
	// spend-state is the only sequential-duplicate guard, and two concurrent
	// Receives of the same note both pass it before either swap settles —
	// measured by the #535 conformance lane as a double grant (issue #639).
	// The guard is per note, not per MAC: a household may buy twice from two
	// devices, and only the same note twice is the customer's double-click or
	// double-tab. Guarded by receiveInFlightMu; lazily initialised so
	// `&Merchant{}` literals keep working.
	receiveInFlightMu sync.Mutex
	receiveInFlight   map[string]struct{}
	// Owed entitlements (#403): purchases whose Receive succeeded but whose
	// grant failed. Keyed by the note's receive reference; durably persisted
	// through owedGrantStore so a restart cannot erase an already-received
	// customer's claim. See owed_grant.go.
	owedGrantsMu   sync.Mutex
	owedGrants     map[string]*owedGrantRecord
	owedGrantStore *owedGrantStore
	// The receive-intent journal (#502): one durable record per
	// money-moving Receive attempt, keyed by the receive reference,
	// written before the call and resolved only on evidence. See
	// receive_intent.go.
	receiveIntentMu    sync.Mutex
	receiveIntents     map[string]*receiveIntentRecord
	receiveIntentStore *receiveIntentStore
	unmeteredMu        sync.Mutex
	unmeteredSessions  map[string]*unmeteredSession
	staleBindings      staleBindingJanitor
	// ndsClients is the periodic client-list reconciliation's bookkeeping: the
	// pass that asks `ndsctl json` the OTHER question — which clients does
	// NoDogSplash authorise that this module has no record of at all? Like the
	// janitor, its cadence, its read seam and its reporting state are per
	// merchant because it runs on the usage monitor's own goroutine.
	ndsClients       ndsClientReconciler
	lightningQuotes  map[string]*lightningQuoteRecord
	lightningQuoteMu sync.RWMutex
	quoteStore       *quoteStore
	// mintQuoteBudget is the self-imposed outbound budget toward each mint. It is
	// a value so `&Merchant{}` literals keep working; its zero value is usable.
	mintQuoteBudget mintQuoteBudget

	// clientProbe overrides the read-only NDS identity probe the payment
	// pre-flight uses for this merchant; nil means valve.CheckClientState, which
	// is what production uses. clientProbeDelay overrides
	// preflightRetryDelayDefault the same way.
	//
	// Both are per merchant rather than package-level seams because the usage
	// monitor runs on its own goroutine, and a leaked monitor goroutine of an
	// earlier test's merchant reads the default probe: a test writing a
	// package-level seam is then a write racing against that read, which is what
	// the full-suite `-race` gate caught. The reconciliation's policy and probe
	// are per merchant for the same reason. Guarded so a test may also drive the
	// pre-flight from its own goroutine.
	clientProbeMu    sync.RWMutex
	clientProbe      func(string) (valve.ClientState, error)
	clientProbeDelay time.Duration
	// monitorMu guards the usage sweep's stop/done channels. They are created by
	// startUsageSweep and closed by stopDataUsageMonitoring, which exists so a
	// test that drives the real startup path can end the sweep it started: the
	// goroutine writes through the standard logger and touches the valve's
	// process-global gate state, so a monitor left running logs into the next
	// test's capture (a data race under -race) and keeps sweeping a merchant the
	// test has finished with. In the shipped binary the sweep is
	// process-lifetime and nothing calls the stop.
	monitorMu   sync.Mutex
	monitorStop chan struct{}
	monitorDone chan struct{}
	monitorOn   bool

	// Session tickets (see session_ticket.go): a per-process signing key and the
	// handle -> attachment store. Both are memory-only on purpose.
	ticketMu sync.Mutex
	tickets  *ticketState
}

func New(configManager *config_manager.ConfigManager) (MerchantInterface, error) {
	log.Printf("=== Merchant Initializing ===")

	config := configManager.GetConfig()
	if config == nil {
		return nil, fmt.Errorf("main config is nil")
	}

	mintHealthTracker := NewMintHealthTracker(configManager)
	mintHealthTracker.RunInitialProbe()

	reachableMints := mintHealthTracker.GetReachableMintConfigs()
	if len(reachableMints) == 0 {
		log.Printf("WARNING: No reachable mints detected. Starting in degraded mode.")
		walletDirPath := filepath.Dir(configManager.ConfigFilePath)
		deg := NewMerchantDegradedWithWallet(configManager, mintHealthTracker, DefaultWalletFactory, walletDirPath)
		mintHealthTracker.StartProactiveChecks()
		mintHealthTracker.SetOnFirstReachableForDegraded(func() {
			log.Printf("Mint became reachable — attempting to upgrade from degraded mode")
			if err := deg.Shutdown(); err != nil {
				log.Printf("ERROR: Failed to shutdown degraded wallet before upgrade: %v", err)
			}
			fullMerchant, err := newFullMerchant(configManager, mintHealthTracker)
			if err != nil {
				log.Printf("ERROR: Failed to upgrade from degraded mode: %v", err)
				return
			}
			if deg.onUpgrade != nil {
				deg.onUpgrade(fullMerchant)
			}
		})
		return deg, nil
	}

	return newFullMerchant(configManager, mintHealthTracker)
}

func newFullMerchant(configManager *config_manager.ConfigManager, mintHealthTracker *MintHealthTracker) (MerchantInterface, error) {
	config := configManager.GetConfig()
	if config == nil {
		return nil, fmt.Errorf("main config is nil")
	}

	reachableMints := mintHealthTracker.GetReachableMintConfigs()
	if len(reachableMints) == 0 {
		return nil, fmt.Errorf("no reachable mints")
	}

	mintURLs := make([]string, len(reachableMints))
	for i, mint := range reachableMints {
		mintURLs[i] = mint.URL
	}

	log.Printf("Setting up wallet...")
	walletDirPath := filepath.Dir(configManager.ConfigFilePath)
	if err := os.MkdirAll(walletDirPath, 0700); err != nil {
		return nil, fmt.Errorf("failed to create wallet directory %s: %w", walletDirPath, err)
	}
	tw, walletErr := tollwallet.NewWalletPort(walletDirPath, mintURLs, false)

	if walletErr != nil {
		log.Printf("WARNING: Wallet initialization failed (%v) — starting in degraded mode", walletErr)
		deg := NewMerchantDegradedWithWallet(configManager, mintHealthTracker, DefaultWalletFactory, walletDirPath)
		mintHealthTracker.StartProactiveChecks()
		mintHealthTracker.SetOnFirstReachableForDegraded(func() {
			log.Printf("Mint became reachable — attempting to upgrade from degraded mode")
			if err := deg.Shutdown(); err != nil {
				log.Printf("ERROR: Failed to shutdown degraded wallet before upgrade: %v", err)
			}
			fullMerchant, err := newFullMerchant(configManager, mintHealthTracker)
			if err != nil {
				log.Printf("ERROR: Failed to upgrade from degraded mode: %v", err)
				return
			}
			if deg.onUpgrade != nil {
				deg.onUpgrade(fullMerchant)
			}
		})
		return deg, nil
	}
	balance := tw.GetBalance()

	resumePendingSwapsAtBoot(tw)

	advertisementStr, err := CreateAdvertisement(configManager, mintHealthTracker)
	if err != nil {
		return nil, fmt.Errorf("failed to create advertisement: %w", err)
	}

	log.Printf("Accepted Mints: %v", config.AcceptedMints)
	log.Printf("Wallet Balance: %d", balance)
	log.Printf("Advertisement: %s", advertisementStr)
	log.Printf("=== Merchant ready ===")

	m := &Merchant{
		config:             config,
		configManager:      configManager,
		tollwallet:         tw,
		mintHealthTracker:  mintHealthTracker,
		customerSessions:   make(map[string]*CustomerSession),
		expiredSessions:    make(map[string]int64),
		lightningQuotes:    make(map[string]*lightningQuoteRecord),
		quoteStore:         newQuoteStore(filepath.Join(walletDirPath, "quotes.json")),
		owedGrants:         make(map[string]*owedGrantRecord),
		owedGrantStore:     newOwedGrantStore(filepath.Join(walletDirPath, "owed-grants.json")),
		receiveIntents:     make(map[string]*receiveIntentRecord),
		receiveIntentStore: newReceiveIntentStore(filepath.Join(walletDirPath, "receive-intents.json")),
	}

	m.loadLightningQuotesFromDisk()
	m.loadOwedGrantsFromDisk()
	m.loadReceiveIntentsFromDisk()
	m.StartPayoutRoutine()
	m.StartDataUsageMonitoring()
	m.startLightningQuoteJanitor()

	return m, nil
}

func (m *Merchant) Shutdown() error {
	return m.tollwallet.Shutdown()
}

// resumePendingSwapsAtBoot drives the wallet half of crash recovery
// (#497/#719): the previous process may have died between the mint's
// acceptance of a swap and the wallet's proof save, and the journaled
// intent replays exactly the bytes that recover the value. The customer
// half is converged separately (receive intents + owed grants restored
// from disk below), so the two halves never block each other. Every
// failure shape is non-fatal: an unrecoverable intent stays journaled,
// the next boot retries it, and a boot must never wedge on a mint that
// is still down.
func resumePendingSwapsAtBoot(tw tollwallet.WalletPort) {
	recovered, failed, err := tw.ResumePendingSwaps()
	switch {
	case err != nil:
		log.Printf("WARNING: wallet boot resume incomplete: %v (pending swap intents stay journaled; the next boot retries)", err)
	case recovered > 0 || failed > 0:
		log.Printf("Wallet boot resume: recovered %d sats from pending swap intents (%d unrecoverable this pass)", recovered, failed)
	}
}

func (m *Merchant) SetOnReachableSetChanged(callback func()) {
	m.mintHealthTracker.SetOnReachableSetChanged(callback)
}

// AdmitReachableMints grows the wallet's accepted set with every mint the
// health tracker currently sees as reachable. The wallet's set is frozen
// at construction from the boot probe; without this, a configured mint
// that was unreachable at boot stays rejected forever — even after it
// recovers (#481). Idempotent: mints already accepted are untouched.
func (m *Merchant) AdmitReachableMints() {
	for _, mint := range m.mintHealthTracker.GetReachableMintConfigs() {
		if err := m.tollwallet.AcceptMint(mint.URL); err != nil {
			log.Printf("AdmitReachableMints: failed to admit %s: %v", mint.URL, err)
		}
	}
}

func (m *Merchant) GetMintHealthTracker() *MintHealthTracker {
	return m.mintHealthTracker
}

// GetUsage returns the current usage in format "[usage]/[allotment]"
// Returns "-1" if no session exists
// Returns error for actual errors (caller should return 500)
func (m *Merchant) GetUsage(macAddress string) (string, error) {
	macAddress = NormalizeMACAddress(macAddress)

	// Get session for this MAC
	session, err := m.GetSession(macAddress)
	if err != nil {
		return "-1/-1", nil
	}

	var usageStr string
	switch session.Metric {
	case "bytes":
		// The usage reported is the session's whole ledger: what it carried in
		// from attachments it has already left behind, plus what the current
		// attachment has used since its own baseline. A MAC rotation therefore
		// answers `consumed/allotment` instead of starting over at zero.
		usage, _, err := m.sessionBytesUsage(macAddress, session)
		if err != nil {
			return "", fmt.Errorf("error getting data usage: %w", err)
		}
		usageStr = fmt.Sprintf("%d/%d", usage, session.Allotment)

	case "milliseconds":
		// Calculate time usage in milliseconds
		elapsed := time.Now().Unix() - session.StartTime
		elapsedMs := uint64(elapsed * 1000)
		usageStr = fmt.Sprintf("%d/%d", elapsedMs, session.Allotment)

	default:
		return "", fmt.Errorf("unknown session metric: %s", session.Metric)
	}

	return usageStr, nil
}

// StartDataUsageMonitoring starts a background routine to monitor data usage for
// active sessions and to reconcile the bindings of clients that have left the
// network (see the stale-binding reconciliation section).
//
// Before the first sweep it runs the STARTUP reconciliation (see
// startup_reconciliation.go), which is the other direction of the same drift: a
// module restart starts from an empty session set while NoDogSplash keeps every
// client it had authorised, so a client this module holds no session for would
// keep an open, unmetered gate. It runs here — synchronously, during merchant
// construction and therefore before the merchant is installed behind the API —
// so it can never race a purchase that is being served, and it asks
// NoDogSplash's own client list (`ndsctl json`, no argument), which is the only
// surface that survives the module.
func (m *Merchant) StartDataUsageMonitoring() {
	log.Printf("Starting data usage monitoring routine")

	m.ReconcileNdsAuthorisationsOnStartup()

	m.startUsageSweep()
}

// startUsageSweep starts the 2-second sweep, once per merchant, and keeps the
// handles stopDataUsageMonitoring needs to end it.
func (m *Merchant) startUsageSweep() {
	m.monitorMu.Lock()
	defer m.monitorMu.Unlock()

	if m.monitorOn {
		// One sweep per merchant: a second goroutine would be unstoppable.
		return
	}

	stop := make(chan struct{})
	done := make(chan struct{})
	m.monitorStop, m.monitorDone, m.monitorOn = stop, done, true

	ticker := time.NewTicker(2 * time.Second) // Check every 2 seconds
	go func() {
		defer ticker.Stop()
		defer close(done)
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				m.checkDataUsage()
			}
		}
	}()
}

// stopDataUsageMonitoring stops the sweep startUsageSweep started and waits for
// the sweep that was in flight to return, so that after it returns nothing of
// this merchant writes through the standard logger any more.
//
// It exists for the tests that drive the REAL startup path (StartDataUsageMonitoring
// is where the startup reconciliation runs): they verify behaviour by reading the
// standard logger, so a sweep belonging to an earlier test must not still be
// reporting into the capture — and a sweep must not keep reading the valve's
// process-global gate state for a merchant the test has finished with. Left
// running, those monitors made `go test -race ./...` in this package fail with
// "race detected during execution of test" (the full suite, 2026-09-27).
//
// Safe to call before the sweep was ever started, and safe to call twice. A
// merchant started again afterwards gets a fresh, stoppable sweep.
func (m *Merchant) stopDataUsageMonitoring() {
	m.monitorMu.Lock()
	stop, done := m.monitorStop, m.monitorDone
	m.monitorStop, m.monitorDone, m.monitorOn = nil, nil, false
	m.monitorMu.Unlock()

	if stop == nil {
		return
	}
	close(stop)
	<-done
}

// checkDataUsage checks all active data-based sessions and closes gates when allotment is reached
func (m *Merchant) checkDataUsage() {
	m.sessionMu.RLock()
	sessions := make(map[string]*CustomerSession)
	for mac, session := range m.customerSessions {
		if session.Metric == "bytes" {
			sessions[mac] = session
		}
	}
	m.sessionMu.RUnlock()

	for mac, session := range sessions {
		m.enforceBytesSession(mac, session)
	}

	// Metering answers "has this client used what it paid for?". It cannot
	// answer "is this client still here?" — and an address whose client left is
	// exactly the binding that stays authorised for ever, because a frozen
	// counter never reaches its allotment. One pass of the reconciliation runs
	// on its own (slower) cadence, after the metering, so a session that has
	// genuinely spent its allotment is still closed by the meter first.
	m.reconcileSweep()

	// And the question in the OTHER direction: which clients does NoDogSplash
	// authorise that this module holds no record of AT ALL? The startup pass
	// (startup_reconciliation.go) asks it once, and only ever sees the drift
	// that exists at the instant it runs; this asks it again on the same slow
	// cadence, so an authorisation that never went through the module's purchase
	// path is closed within a pass rather than lasting NoDogSplash's whole
	// session timeout. A failed read changes nothing.
	m.ndsClientReconcileSweep()
}

// usageMonitorGraceSweeps bounds how many consecutive sweeps a bytes session may
// stay unenforceable — a baseline that cannot be established, or counters the
// module cannot read — before the monitor closes its gate. At the 2s sweep
// interval that is a minute of grace: long enough for a NoDogSplash restart or a
// transient ndsctl failure to clear, short enough that a session the module
// cannot meter is never an unlimited free ride. A var so tests can shrink it.
var usageMonitorGraceSweeps = 30

// usageMonitorBaselineRetryDelay is the minimum time between two attempts to
// establish a missing metering baseline for the same session. A var so tests can
// shrink it.
var usageMonitorBaselineRetryDelay = 2 * time.Second

// unmeteredSession is the monitor's bookkeeping for one bytes session it cannot
// currently enforce. It exists so the monitor can tell "just became unmeterable"
// from "has been unmeterable for a minute", which is what decides between
// waiting and closing the gate.
//
// The three close-report fields exist so the FORCE-CLOSE is an escalation rather
// than a line per sweep: forceCloseEscalated is the first sweep past the grace
// window (the escalation, once), lastCloseAbandoned is the valve's state on the
// previous attempt (a CHANGE of state is not a repeat and is reported at once),
// and lastForceCloseReport throttles everything else.
type unmeteredSession struct {
	sweeps              int
	lastBaselineAttempt time.Time

	forceCloseEscalated  bool
	lastForceCloseReport time.Time
	lastCloseAbandoned   bool
}

// unmeterableSessionReportInterval is how often the module repeats the state of a
// session it can neither meter nor close. The sweep runs every 2 s, so without
// this the module wrote an ERROR — and the close failure under it — on every
// sweep for as long as NoDogSplash refused (the bench log's "has been unreadable
// for 39 sweeps"). A var so tests can shrink it.
var unmeterableSessionReportInterval = time.Minute

// unmeteredStateLocked returns the bookkeeping of mac, creating it on first use.
// Caller must hold unmeteredMu.
func (m *Merchant) unmeteredStateLocked(macAddress string) *unmeteredSession {
	if m.unmeteredSessions == nil {
		m.unmeteredSessions = make(map[string]*unmeteredSession)
	}
	state, exists := m.unmeteredSessions[macAddress]
	if !exists {
		state = &unmeteredSession{}
		m.unmeteredSessions[macAddress] = state
	}
	return state
}

// baselineAttemptDue reports whether enough time has passed to try establishing
// the metering baseline of macAddress again, and marks the attempt.
func (m *Merchant) baselineAttemptDue(macAddress string) bool {
	m.unmeteredMu.Lock()
	defer m.unmeteredMu.Unlock()

	state := m.unmeteredStateLocked(macAddress)
	if time.Since(state.lastBaselineAttempt) < usageMonitorBaselineRetryDelay {
		return false
	}
	state.lastBaselineAttempt = time.Now()
	return true
}

// noteUnmeterableSweep records one sweep on which the usage of macAddress could
// not be read, and returns how many sweeps in a row that has now been true.
func (m *Merchant) noteUnmeterableSweep(macAddress string) int {
	m.unmeteredMu.Lock()
	defer m.unmeteredMu.Unlock()

	state := m.unmeteredStateLocked(macAddress)
	state.sweeps++
	return state.sweeps
}

// noteUnmeterableCloseFailure records one failed force-close of macAddress and
// reports how this sweep should be reported: escalate is the FIRST sweep past the
// grace window (the escalation, which is written once), changed is a change in
// what the valve says about that close (a transition is not a repeat, so it is
// reported at once), and repeat is a state that has been reported before and is
// due again.
//
// Caller supplies closeErr and now so the decision is made under the lock that
// owns the state.
func (m *Merchant) noteUnmeterableCloseFailure(macAddress string, closeErr error, now time.Time) (escalate, changed, repeat bool) {
	abandoned := errors.Is(closeErr, valve.ErrGateCloseAbandoned)

	m.unmeteredMu.Lock()
	defer m.unmeteredMu.Unlock()

	state := m.unmeteredStateLocked(macAddress)
	escalate = !state.forceCloseEscalated
	changed = !escalate && abandoned != state.lastCloseAbandoned
	repeat = escalate || now.Sub(state.lastForceCloseReport) >= unmeterableSessionReportInterval

	if escalate {
		state.forceCloseEscalated = true
	}
	state.lastCloseAbandoned = abandoned
	if escalate || changed || repeat {
		state.lastForceCloseReport = now
	}
	return escalate, changed, repeat
}

// clearUnmetered forgets the bookkeeping of a session that is enforceable again.
func (m *Merchant) clearUnmetered(macAddress string) {
	m.unmeteredMu.Lock()
	defer m.unmeteredMu.Unlock()

	delete(m.unmeteredSessions, macAddress)
}

// closeRetryStateClause is what the module will do NEXT about a close that
// ndsctl has not confirmed, and it is derived from the error rather than
// assumed. A close whose retry budget is spent (ErrGateCloseAbandoned) is NOT
// re-attempted by the sweep machinery — only the reconciliation re-attempts it,
// under fresh evidence about the client — so a log line that says "the close is
// retried" for that state is the same class of false claim as the
// unmetered-access wording this release removes. Every caller that reports an
// unconfirmed close must use this rather than assert a retry in prose.
func closeRetryStateClause(err error) string {
	if errors.Is(err, valve.ErrGateCloseAbandoned) {
		return "the module has stopped re-attempting this close (the close budget for this gate is spent and the valve's UNRESOLVED line carries the operator action); only the reconciliation re-attempts it, under fresh evidence about the client"
	}
	return "the close is retried within the gate's close budget"
}

// enforceBytesSession is the usage monitor's per-session step. Every sweep takes
// the session one step closer to enforcement — meter it against its allotment,
// re-establish the baseline it is missing, or close a gate that cannot be
// metered at all. The one thing it must never do is skip the session: a bytes
// session the monitor stops looking at keeps an open gate for as long as the
// process lives, which is exactly the free, unmetered internet this module
// exists to prevent (C1-2b, C1-2c). Its failure log line derives its retry claim
// from closeRetryStateClause, never from prose.
func (m *Merchant) enforceBytesSession(macAddress string, session *CustomerSession) {
	// A session whose baseline was never established cannot be metered. The old
	// code answered this with `continue` on every sweep, for ever.
	if !valve.HasDataBaseline(macAddress) {
		m.establishBaseline(macAddress)
		return
	}

	usage, attachmentUsage, err := m.sessionBytesUsage(macAddress, session)
	if err != nil {
		if errors.Is(err, valve.ErrDataBaselineMissing) {
			m.establishBaseline(macAddress)
			return
		}
		m.closeUnmeterableSession(macAddress, err)
		return
	}
	m.clearUnmetered(macAddress)
	// Remember this attachment's highest reading: it is what a rebind carries
	// when the attachment's counters are already gone by the time it happens.
	m.noteAttachmentUsage(macAddress, attachmentUsage)

	// Check if allotment is reached
	if usage < session.Allotment {
		// Log progress periodically (every ~10 checks = 20 seconds)
		if usage > 0 && usage%(10*1024*1024) < 2*1024*1024 { // Log around every 10MB
			log.Printf("Data usage for %s: %s / %s (%.1f%%)",
				macAddress,
				utils.BytesToHumanReadable(usage),
				utils.BytesToHumanReadable(session.Allotment),
				float64(usage)/float64(session.Allotment)*100)
		}
		return
	}

	log.Printf("Data allotment reached for %s: %s / %s",
		macAddress,
		utils.BytesToHumanReadable(usage),
		utils.BytesToHumanReadable(session.Allotment))

	// Close the gate, and retire the session only once that close is CONFIRMED.
	// A failed close leaves the client Authenticated through an open gate, and
	// the record this branch would otherwise delete is the only thing that says
	// the client must be closed — retiring it is how the customer kept free,
	// unmetered internet with nothing left to retry (C1-2b).
	if err := valve.CloseGate(macAddress); err != nil {
		log.Printf("ERROR: could not close the gate for %s after its allotment was spent: %v — the session is retained and %s; whether the client still has access is UNVERIFIED until ndsctl confirms the deauthorization, and the valve logs the verified state of every attempt (unconfirmed gate closes=%d)",
			macAddress, err, closeRetryStateClause(err), valve.GateCloseFailures())
		return
	}
	log.Printf("Successfully closed gate for %s", macAddress)

	// Retire the record: the allotment is spent, so GetUsage answers
	// -1/-1 and /session-state answers "expired" — the record itself
	// carries no expiry flag, so the removal is also what tells a
	// portal (and the next purchase) that this session is over.
	m.sessionMu.Lock()
	m.expireSessionLocked(macAddress)
	m.sessionMu.Unlock()
	log.Printf("Removed expired session for %s", macAddress)
}

// sessionBytesUsage returns the bytes consumed by the bytes session of
// macAddress: the total the session carried in from attachments it has already
// left behind (`session.Consumed`), plus the usage of the CURRENT attachment
// since its own baseline. The second return value is that attachment-local
// figure, which the caller records as the highest observed
// (noteAttachmentUsage).
//
// The attachment-local figure is the HIGHER of the live counter reading and the
// highest reading already recorded. NoDogSplash's counters live in its own
// process: a restart, or a client record that is dropped and re-created, walks
// them back to zero, and a meter that followed them down would hand the customer
// a fresh allotment without a rebind ever happening.
func (m *Merchant) sessionBytesUsage(macAddress string, session *CustomerSession) (uint64, uint64, error) {
	since, err := valve.GetDataUsageSinceBaseline(macAddress)
	if err != nil {
		return 0, 0, err
	}
	if since < session.attachmentUsage {
		since = session.attachmentUsage
	}
	return session.Consumed + since, since, nil
}

// noteAttachmentUsage records the highest usage observed for the current
// attachment of macAddress's session, so a rebind can carry it forward when the
// counters are already gone by the time the rebind happens. A session that is no
// longer tracked is skipped: there is nothing left to carry.
func (m *Merchant) noteAttachmentUsage(macAddress string, since uint64) {
	m.sessionMu.Lock()
	defer m.sessionMu.Unlock()

	session, exists := m.customerSessions[macAddress]
	if !exists {
		return
	}
	if since > session.attachmentUsage {
		session.attachmentUsage = since
	}
}

// establishBaseline gives a bytes session the metering baseline it needs, so the
// session is metered instead of being skipped for ever. The baseline is recorded
// even when ndsctl cannot report the client's counters yet (from zero, which
// under-counts the customer's pre-baseline usage rather than over-granting), and
// the gap is escalated.
func (m *Merchant) establishBaseline(macAddress string) {
	if !m.baselineAttemptDue(macAddress) {
		return
	}

	log.Printf("WARNING: the bytes session of %s has no metering baseline — establishing one so its allotment is actually enforced", macAddress)

	if err := valve.SetDataBaseline(macAddress); err != nil {
		log.Printf("ERROR: metering baseline for %s had to be recorded from zero (%v): usage before the baseline is not counted, and the baseline is re-established while the session runs", macAddress, err)
		return
	}
	m.clearUnmetered(macAddress)
}

// closeUnmeterableSession handles a session whose usage the module cannot read:
// it is given usageMonitorGraceSweeps of grace and then its gate is closed, since
// an unmeasurable session left open is unmetered internet. The close is subject
// to the same contract as every other close — the session is retired only when
// the close is confirmed, and the gate stays tracked otherwise (and the valve
// logs the verified state of the client, which is what distinguishes "ndsctl
// refused" from "NoDogSplash does not know this client": the second is a
// completed close and retires the session at once).
//
// The REPORTING of that state is bounded, because the sweep runs every 2 s: the
// force-close is escalated once, a change of state (the close being abandoned, or
// coming back) is reported at once, and the same state repeating is reported at
// most once per unmeterableSessionReportInterval. Before this, one session NoDogSplash
// would not deauthorize wrote two ERROR lines every second for ever, which is how
// the bench log's real escalation ("has been unreadable for 39 sweeps") became
// unreadable itself.
func (m *Merchant) closeUnmeterableSession(macAddress string, usageErr error) {
	sweeps := m.noteUnmeterableSweep(macAddress)

	if sweeps <= usageMonitorGraceSweeps {
		log.Printf("WARNING: cannot read the usage of the bytes session of %s (%v), sweep %d/%d — the session stays tracked",
			macAddress, usageErr, sweeps, usageMonitorGraceSweeps)
		return
	}

	if err := valve.CloseGate(macAddress); err != nil {
		escalate, changed, repeat := m.noteUnmeterableCloseFailure(macAddress, err, time.Now())

		switch {
		case escalate:
			log.Printf("ERROR: the usage of the bytes session of %s has been unreadable for %d sweeps (%v): the session cannot be metered, so its gate is closed rather than left open unmetered (escalated once: the session stays TRACKED until the close is confirmed, and this state is repeated at most every %s — a change of state is reported at once)",
				macAddress, sweeps, usageErr, unmeterableSessionReportInterval)
			log.Printf("ERROR: could not close the gate of the unmeterable session of %s: %v — the session is retained and %s (unconfirmed gate closes=%d)",
				macAddress, err, closeRetryStateClause(err), valve.GateCloseFailures())
		case changed:
			log.Printf("WARNING: the state of the unmeterable session of %s changed: %v — %s (sweeps with unreadable counters: %d)",
				macAddress, err, closeRetryStateClause(err), sweeps)
		case repeat:
			log.Printf("WARNING: the bytes session of %s is still unmeterable and its gate is still not confirmed closed after %d sweeps: %v — the session stays tracked and the module keeps attempting the close (%s)",
				macAddress, sweeps, usageErr, closeRetryStateClause(err))
		}
		return
	}

	m.sessionMu.Lock()
	retired, parked := m.retireSessionOrParkForTicketLocked(macAddress, time.Now())
	m.sessionMu.Unlock()
	// The episode is over: the bookkeeping belongs to THIS unmeterable session,
	// so it is forgotten with it. Leaving it behind would leave
	// forceCloseEscalated set for the MAC, and a later session that cannot be
	// metered would start in the throttled "repeat" branch — the operator would
	// never get its ERROR escalation, and the sweeps counter would continue from
	// the previous session's grace window instead of granting this one its own.
	m.clearUnmetered(macAddress)
	log.Printf("Removed unmeterable session for %s", macAddress)

	switch {
	case retired:
		log.Printf("Removed unmeterable session for %s", macAddress)
	case parked:
		log.Printf("Parked the unmeterable session of %s instead of retiring it: its gate is closed and it cannot be metered, but it still holds a live session ticket, so a rebind can still carry the remainder the customer paid for to the address they moved to", macAddress)
	}
}

// ---------------------------------------------------------------------------
// Stale-binding reconciliation: the address a client leaves behind
//
// Entitlement is keyed to the client's MAC address, and the address belongs to
// the device that chose it. A client that changes its Wi-Fi address mid-session
// (iOS picks a rotating private address by default on a weak or open SSID, which
// is what a captive portal usually is; Linux and Android can be asked to
// randomise theirs) therefore leaves this module holding a gate for an address
// no device holds any more. On the default `bytes` metric the per-MAC meter has
// nothing left to measure — the counters of an address nobody uses simply stop
// moving — so nothing that reads traffic can ever end that session: the gate
// stays authorised until the process dies, and any later holder of that address
// (a MAC fallback to the hardware address, a spoof, a collision) inherits
// metered internet for free.
//
// The reconciliation asks the one question a meter cannot ask — is the client
// that bought this address still on the network? — and, after a grace window of
// consecutive "gone" answers, tears the binding down under the same rule as
// every other close (a failed close is not a close: the record is retained and
// retried). It never touches a client that is still listed, which is what keeps
// an idle customer's session alive.
// ---------------------------------------------------------------------------

// defaultStaleBindingReconcileEvery is how many usage-monitor sweeps (2 s each)
// pass between two reconciliation passes by default, i.e. how long the module
// waits before it spends an ndsctl probe on a session (~30 s).
const defaultStaleBindingReconcileEvery = 15

// defaultStaleBindingGracePasses is how many consecutive reconciliation passes
// may find a client gone before its binding is torn down by default: a device
// that roams out and back, or one whose NDS record blinks, must not lose its
// session (~60 s of absence).
const defaultStaleBindingGracePasses = 2

// staleBindingJanitor is the reconciliation's bookkeeping: how many sweeps have
// passed (to run on a slower cadence than the meter) and how many consecutive
// passes found each address's client gone.
//
// The cadence and grace are fields rather than package variables on purpose: the
// reconciliation runs on the usage monitor's own goroutine, so a policy a test
// writes while that goroutine reads it would be a data race — and was, until a
// full-suite `-race` run caught it. Per merchant, read under this mutex, the
// policy has exactly one writer and one reader.
type staleBindingJanitor struct {
	mu          sync.Mutex
	sweeps      int
	absent      map[string]int
	everySweeps int
	gracePasses int

	// probe overrides the read-only NDS identity probe for this merchant. It
	// exists so a test can drive the reconciliation without writing a
	// package-level seam that a monitor goroutine in the same test binary is
	// reading; nil means valve.CheckClientState, which is what production uses.
	probe func(string) (valve.ClientState, error)
}

// staleBindingProbeLocked returns the identity probe this merchant's
// reconciliation uses. Caller must hold j.mu.
func (j *staleBindingJanitor) staleBindingProbeLocked() func(string) (valve.ClientState, error) {
	if j.probe != nil {
		return j.probe
	}
	return valve.CheckClientState
}

// setStaleBindingProbe replaces the identity probe for this merchant only. It
// exists for tests; production leaves it nil and uses valve.CheckClientState.
func (m *Merchant) setStaleBindingProbe(probe func(string) (valve.ClientState, error)) {
	m.staleBindings.mu.Lock()
	defer m.staleBindings.mu.Unlock()

	m.staleBindings.probe = probe
}

// reconcileEveryLocked returns how many sweeps pass between two reconciliation
// passes. Caller must hold j.mu.
func (j *staleBindingJanitor) reconcileEveryLocked() int {
	if j.everySweeps < 1 {
		return defaultStaleBindingReconcileEvery
	}
	return j.everySweeps
}

// graceLocked returns how many consecutive "the client is gone" passes a binding
// may survive. Caller must hold j.mu.
func (j *staleBindingJanitor) graceLocked() int {
	if j.gracePasses < 1 {
		return defaultStaleBindingGracePasses
	}
	return j.gracePasses
}

// tuneStaleBindingReconciliation replaces the reconciliation's cadence and grace
// window for this merchant. It exists for tests: the policy is per merchant
// (read under the janitor's lock) rather than a package-level variable, because
// the reconciliation runs on the usage monitor's own goroutine.
func (m *Merchant) tuneStaleBindingReconciliation(everySweeps, gracePasses int) {
	m.staleBindings.mu.Lock()
	defer m.staleBindings.mu.Unlock()

	m.staleBindings.everySweeps, m.staleBindings.gracePasses = everySweeps, gracePasses
}

// reconcileSweep runs the reconciliation when its cadence is due.
func (m *Merchant) reconcileSweep() {
	m.staleBindings.mu.Lock()
	m.staleBindings.sweeps++
	due := m.staleBindings.sweeps%m.staleBindings.reconcileEveryLocked() == 0
	m.staleBindings.mu.Unlock()

	if due {
		m.reconcileStaleBindings()
	}
}

// noteStaleBindingPass records one pass that found macAddress's client gone and
// returns how many consecutive passes have now found it gone.
func (m *Merchant) noteStaleBindingPass(macAddress string) int {
	m.staleBindings.mu.Lock()
	defer m.staleBindings.mu.Unlock()

	if m.staleBindings.absent == nil {
		m.staleBindings.absent = make(map[string]int)
	}
	m.staleBindings.absent[macAddress]++
	return m.staleBindings.absent[macAddress]
}

// forgetStaleBinding drops the absence bookkeeping of an address whose client is
// present again (or whose binding has been dealt with), so the grace window
// always counts CONSECUTIVE absences.
func (m *Merchant) forgetStaleBinding(macAddress string) {
	m.staleBindings.mu.Lock()
	delete(m.staleBindings.absent, macAddress)
	m.staleBindings.mu.Unlock()
}

// staleBindingCandidates lists the addresses whose binding this pass checks:
// every bytes session — the metric whose meter cannot finish once the client
// leaves — plus every gate the module still holds, including the gates it has no
// session record for, which nothing else looks at.
//
// Milliseconds sessions are deliberately NOT candidates: their gate is bounded
// by its own expiry timer, so the leak is not indefinite, and closing one early
// would take away paid time from a customer who may simply have closed a laptop
// lid. Their gate ends when its timer says so, not when we stop seeing them.
func (m *Merchant) staleBindingCandidates() []string {
	excluded := make(map[string]bool)
	candidates := make(map[string]bool)

	m.sessionMu.RLock()
	for macAddress, session := range m.customerSessions {
		if session.Metric == "milliseconds" {
			excluded[macAddress] = true
			continue
		}
		candidates[macAddress] = true
	}
	m.sessionMu.RUnlock()

	for _, macAddress := range valve.TrackedGates() {
		if excluded[macAddress] {
			continue
		}
		candidates[macAddress] = true
	}

	macs := make([]string, 0, len(candidates))
	for macAddress := range candidates {
		macs = append(macs, macAddress)
	}
	sort.Strings(macs)
	return macs
}

// reconcileStaleBindings is one reconciliation pass over every candidate
// binding. A client the module cannot ask about is left alone (a failed probe is
// not evidence that a customer left), and a client that is still listed resets
// the grace window.
func (m *Merchant) reconcileStaleBindings() {
	m.staleBindings.mu.Lock()
	probe := m.staleBindings.staleBindingProbeLocked()
	m.staleBindings.mu.Unlock()

	for _, macAddress := range m.staleBindingCandidates() {
		state, err := probe(macAddress)
		if err != nil {
			// Fail open: an unreadable probe must never cost a paying customer
			// their session, and it must not accumulate towards the grace
			// window either.
			m.forgetStaleBinding(macAddress)
			log.Printf("Stale-binding check: could not ask NoDogSplash whether %s is still on the network (%v) — leaving its binding alone", macAddress, err)
			continue
		}

		if state.Registered {
			m.forgetStaleBinding(macAddress)
			continue
		}

		m.staleBindings.mu.Lock()
		grace := m.staleBindings.graceLocked()
		m.staleBindings.mu.Unlock()

		if passes := m.noteStaleBindingPass(macAddress); passes < grace {
			log.Printf("WARNING: NoDogSplash no longer lists %s (%d/%d passes) — its binding stays authorised for now", macAddress, passes, grace)
			continue
		}

		m.reconcileStaleBinding(macAddress)
	}
}

// reconcileStaleBinding tears down the binding of an address whose client is
// gone: the gate is closed (and, only when that close is CONFIRMED, the session
// record is retired, so the address cannot be inherited by a later holder) —
// unless the session still holds a live session ticket, in which case the record is
// PARKED instead of retired, so the remainder the customer paid for can still be
// carried to the address they moved to. Entitlement now travels with a session
// ticket, which is the work this paragraph used to defer (see
// retireSessionOrParkForTicketLocked). The usage of the last covered sweep is
// reported either way.
//
// The close goes through ReconcileGateClose rather than CloseGate because this is
// the one caller that brings EVIDENCE about the client (the probe above has just
// established that NoDogSplash no longer lists the address): that is what lets it
// re-attempt the close of a gate whose own budget is spent, which is the only way
// such a gate can still converge.
func (m *Merchant) reconcileStaleBinding(macAddress string) {
	usage, usageErr := valve.GetDataUsageSinceBaseline(macAddress)

	if err := valve.ReconcileGateClose(macAddress); err != nil {
		log.Printf("ERROR: could not close the gate of the stale binding of %s: %v — the record is retained and the next reconciliation pass re-attempts the close (unconfirmed gate closes=%d)",
			macAddress, err, valve.GateCloseFailures())
		return
	}

	m.forgetStaleBinding(macAddress)

	m.sessionMu.Lock()
	retired, parked := m.retireSessionOrParkForTicketLocked(macAddress, time.Now())
	m.sessionMu.Unlock()

	lastUsage := "nothing was metered for it"
	if usageErr == nil {
		lastUsage = fmt.Sprintf("%s of covered usage", utils.BytesToHumanReadable(usage))
	}

	if parked {
		log.Printf("Reconciled the stale binding of %s: its client is gone and the gate is deauthorised, but the session is PARKED rather than retired because it still holds a live session ticket (%s) — a rebind inside that ticket's horizon still carries the purchased remainder to the address the customer moved to, and the first pass after the horizon retires the record", macAddress, lastUsage)
		return
	}
	if retired {
		log.Printf("Reconciled the stale binding of %s: its client is gone, the gate is deauthorised and the session is retired (%s; a session with no live ticket has nothing left to claim — entitlement travels with a session ticket)", macAddress, lastUsage)
		return
	}
	log.Printf("Reconciled the stale binding of %s: its client is gone and the gate is deauthorised, and there was no session record to retire (%s)", macAddress, lastUsage)
}

func (m *Merchant) StartPayoutRoutine() {
	log.Printf("Starting payout routine")

	for _, mint := range m.config.AcceptedMints {
		go func(mintConfig config_manager.MintConfig) {
			ticker := time.NewTicker(1 * time.Minute)
			defer ticker.Stop()

			for range ticker.C {
				m.runPayoutForMint(mintConfig)
			}
		}(mint)
	}

	m.mintHealthTracker.StartProactiveChecks()

	log.Printf("Payout routine started")
}

// runPayoutForMint runs one payout pass for one mint, or does nothing when the
// mint is not usable right now. Two reasons to skip, and they are different:
//
//   - not reachable: we do not know the mint is up at all;
//   - persistently throttled: the mint is up but has answered 429 to every probe
//     for the persistent window, so a melt would be rate-limited too. The
//     balance stays in the wallet and the next cycle after the mint answers
//     again picks it up — this is a delay, not a loss of value.
func (m *Merchant) runPayoutForMint(mintConfig config_manager.MintConfig) {
	if !m.mintHealthTracker.IsReachable(mintConfig.URL) {
		return
	}
	if m.mintHealthTracker.IsPersistentlyThrottled(mintConfig.URL) {
		log.Printf("Skipping payout %s: the mint has answered 429 to every probe for the persistent window; the balance stays in the wallet", mintConfig.URL)
		return
	}
	m.processPayout(mintConfig)
}

// payoutInvoiceRetries is the invoice-fetch retry count for the reachability probe.
const payoutInvoiceRetries = 5

// fetchInvoiceWithRetry fetches an invoice, retrying up to payoutInvoiceRetries times.
func fetchInvoiceWithRetry(lightningAddr string, amountSats uint64) (string, error) {
	var lastErr error
	for attempt := 1; attempt <= payoutInvoiceRetries; attempt++ {
		invoice, err := lightning.GetInvoiceFromLightningAddress(lightningAddr, amountSats)
		if err == nil {
			return invoice, nil
		}
		lastErr = err
		log.Printf("fetchInvoiceWithRetry(%s, %d) attempt %d/%d failed: %v", lightningAddr, amountSats, attempt, payoutInvoiceRetries, err)
	}
	return "", lastErr
}

// processPayout pays the owner first, then reachable maintainers. Unreachable
// recipients are skipped (their share stays in the wallet); the owner must be
// reachable and paid before any maintainer. Payee failures don't fault the mint.
func (m *Merchant) processPayout(mintConfig config_manager.MintConfig) {
	balance := m.tollwallet.GetBalanceByMint(mintConfig.URL)

	if balance < mintConfig.MinPayoutAmount {
		log.Printf("Skipping payout %s, Balance %d does not meet threshold of %d", mintConfig.URL, balance, mintConfig.MinPayoutAmount)
		return
	}

	if balance <= mintConfig.MinBalance {
		log.Printf("Skipping payout %s, Balance %d does not exceed min_balance %d", mintConfig.URL, balance, mintConfig.MinBalance)
		return
	}
	aimedPaymentAmount := balance - mintConfig.MinBalance

	identities := m.configManager.GetIdentities()
	if identities == nil {
		return
	}

	// Build the recipient list in config order.
	type recipient struct {
		identity  string
		amount    uint64
		lightning string
		isOwner   bool
	}
	var recipients []recipient
	for _, ps := range m.config.ProfitShare {
		amt := uint64(math.Round(float64(aimedPaymentAmount) * ps.Factor))
		if amt == 0 {
			log.Printf("Skipping payout for %s: aimedAmount rounded to 0 (aimedPaymentAmount=%d, factor=%.4f)", ps.Identity, aimedPaymentAmount, ps.Factor)
			continue
		}
		id, err := identities.GetPublicIdentity(ps.Identity)
		if err != nil {
			log.Printf("Warning: Could not find public identity for profit share %q: %v", ps.Identity, err)
			continue
		}
		recipients = append(recipients, recipient{
			identity:  ps.Identity,
			amount:    amt,
			lightning: id.LightningAddress,
			isOwner:   ps.Identity == "owner",
		})
	}
	if len(recipients) == 0 {
		return
	}

	// Phase 1 — reachability probe.
	reachable := make([]recipient, 0, len(recipients))
	for _, r := range recipients {
		if _, err := fetchInvoiceWithRetry(r.lightning, r.amount); err != nil {
			log.Printf("Payout %s: %s unreachable (no invoice after %d attempts: %v) — skipping, share stays in wallet",
				mintConfig.URL, r.identity, payoutInvoiceRetries, err)
			continue
		}
		reachable = append(reachable, r)
	}

	// Phase 2 — owner must be reachable and paid first.
	var owner *recipient
	for i := range reachable {
		if reachable[i].isOwner {
			owner = &reachable[i]
			break
		}
	}
	if owner == nil {
		log.Printf("Payout %s: owner is unreachable — aborting all payouts this cycle", mintConfig.URL)
		return
	}
	if err := m.PayoutShare(mintConfig, owner.amount, owner.lightning); err != nil {
		log.Printf("Payout %s: owner payout failed (%v) — aborting dev-split payouts; e-cash retained", mintConfig.URL, err)
		return
	}

	// Phase 3 — reachable maintainers.
	for _, r := range reachable {
		if r.isOwner {
			continue
		}
		if err := m.PayoutShare(mintConfig, r.amount, r.lightning); err != nil {
			log.Printf("Payout %s: payout to %s failed (%v) — e-cash retained for next cycle", mintConfig.URL, r.identity, err)
			continue
		}
	}

	log.Printf("Payout completed for mint %s", mintConfig.URL)
}

// PayoutShare melts aimedPaymentAmount sats to lightningAddress, retrying the
// melt up to 5 times. It does not fault the mint on payee failures (resolves #27).
func (m *Merchant) PayoutShare(mintConfig config_manager.MintConfig, aimedPaymentAmount uint64, lightningAddress string) error {
	tolerancePaymentAmount := aimedPaymentAmount + (aimedPaymentAmount * mintConfig.BalanceTolerancePercent / 100)

	log.Printf("Processing payout for mint %s: aiming for %d sats with %d sats tolerance", mintConfig.URL, aimedPaymentAmount, tolerancePaymentAmount)

	maxCost := aimedPaymentAmount + tolerancePaymentAmount
	return m.tollwallet.MeltToLightning(mintConfig.URL, aimedPaymentAmount, maxCost, lightningAddress)
}

type PurchaseSessionResult struct {
	Status      string
	Description string
}

// PurchaseSession processes a payment with cashu token and MAC address, returns either a session event or a notice event
// Spec (NUT 00) verification quote lives above TollWallet.Receive — single source; duplicate quotes flag in speccheck.
func (m *Merchant) PurchaseSession(cashuToken string, macAddress string) (*nostr.Event, error) {
	macAddress = NormalizeMACAddress(macAddress)

	// Validate MAC address
	if !utils.ValidateMACAddress(macAddress) {
		noticeEvent, noticeErr := m.CreateNoticeEvent("error", "invalid-mac-address",
			fmt.Sprintf("Invalid MAC address: %s", macAddress), macAddress)
		if noticeErr != nil {
			return nil, fmt.Errorf("invalid MAC address and failed to create notice: %w", noticeErr)
		}
		return noticeEvent, nil
	}

	// Process payment
	paymentCashuToken, err := m.tollwallet.DecodeToken(cashuToken)
	if err != nil {
		noticeEvent, noticeErr := m.CreateNoticeEvent("error", "payment-error-invalid-token",
			fmt.Sprintf("Invalid cashu token: %v", err), macAddress)
		if noticeErr != nil {
			return nil, fmt.Errorf("invalid cashu token and failed to create notice: %w", noticeErr)
		}
		return noticeEvent, nil
	}

	// Pre-check the mint's swap fee: a token whose value is entirely consumed
	// by the fee fails the swap with an opaque mint error. Fail fast with a
	// clear message. If the fee can't be determined (cdk adapter, mint
	// unreachable — or the precheck overruns its budget against a wedged
	// mint, #525), fall through and let Receive classify the error.
	if fee, feeErr, ok := swapFeeSatsBounded(m.tollwallet.SwapFeeSats, paymentCashuToken); ok && feeErr == nil && fee > 0 {
		if amount := paymentCashuToken.Amount(); amount <= fee {
			msg := fmt.Sprintf(
				"This e-cash note is %d sat but mint %s charges a %d sat swap fee, so there is nothing left to spend. Use a larger token or a mint without fees.",
				amount, paymentCashuToken.Mint(), fee)
			noticeEvent, noticeErr := m.CreateNoticeEvent("error", "payment-error-below-swap-fee", msg, macAddress)
			if noticeErr != nil {
				return nil, fmt.Errorf("token below swap fee and failed to create notice: %w", noticeErr)
			}
			return noticeEvent, nil
		}
	}

	// Pre-flight of issue #403 L1: a payment whose MAC NDS does not know cannot
	// have its gate opened, so accepting it would consume the customer's token
	// with no session and no refund path.
	//
	// A returning customer is the exception, and the reason this pre-flight used
	// to block every renewal: once the session ran out we deauthorised the MAC,
	// and NDS then reports it as not listed — so the next purchase was refused
	// before Receive with `client-not-registered` ("No captive-portal session
	// found for this device. Reconnect to the TollGate Wi-Fi and try again."),
	// the exact "disconnect and reconnect" the portal showed. But a MAC with an
	// active session, or one that expired here, is a device we know: the client
	// is demonstrably present (it just submitted a token through the captive
	// portal) and the valve's bounded auth retry is what re-registers it. So the
	// renewal proceeds and the gate-open decides.
	//
	// Residual risk, unchanged in kind from #403: if NDS genuinely cannot
	// re-authorise the client after the valve's retries, the token has been
	// received and grantSessionAccess rolls the session back. That exposure is
	// deliberate — a hard refusal here guarantees no renewal can ever work.
	if !m.sessionIsRenewal(macAddress) && !m.clientRegisteredForGate(macAddress) {
		noticeEvent, noticeErr := m.CreateNoticeEvent("error", "client-not-registered",
			"No captive-portal session found for this device. Reconnect to the TollGate Wi-Fi and try again.", macAddress)
		if noticeErr != nil {
			return nil, fmt.Errorf("client not registered and failed to create notice: %w", noticeErr)
		}
		return noticeEvent, nil
	}

	log.Printf("PurchaseSession: calling Receive for mint=%s token_amount=%d mac=%s", paymentCashuToken.Mint(), paymentCashuToken.Amount(), macAddress)

	// The same note may already be in flight from a concurrent POST (a
	// double-click, two tabs, a portal retry). From here to the consumption of
	// the Receive result — including the outcome-unknown window past the
	// deadline — the note is marked; a second submission is refused locally,
	// before any money moves, because the mint's spend-state cannot arbitrate
	// two concurrent swaps of the same proofs (#639, measured by the #535
	// conformance lane: both concurrent POSTs granted, allotment 2x, four
	// derivation digests re-exposed).
	reference := receiveReference(paymentCashuToken)
	if !m.beginReceive(reference) {
		log.Printf("PurchaseSession: refusing concurrent duplicate of an in-flight note for mac=%s reference=%s — no second Receive was sent to the mint", macAddress, reference)
		noticeEvent, noticeErr := m.CreateNoticeEvent("error", "payment-duplicate-inflight",
			"This e-cash note is already being processed by this TollGate. Do not send it again — if the mint has taken it, a second attempt will be refused as already spent. Reload this page in a couple of minutes.", macAddress)
		if noticeErr != nil {
			return nil, fmt.Errorf("duplicate in-flight note and failed to create notice: %w", noticeErr)
		}
		return noticeEvent, nil
	}

	// The receive-intent journal (#502): the attempt is durable before the
	// money moves, so a crash, a dropped response or a restart can still be
	// reconciled into service-or-evidence. A known reference here means the
	// note reached this router before — the in-flight guard above already
	// excluded a live call, so a pending intent belongs to a dead or
	// ambiguous attempt and is reconciled now, on the evidence.
	if reference != "" {
		serialized, serErr := paymentCashuToken.Serialize()
		if serErr == nil {
			intentMetric := ""
			if m.config != nil {
				intentMetric = m.config.Metric
			}
			if prior, resolved := m.beginReceiveIntent(reference, macAddress, paymentCashuToken.Mint(), paymentCashuToken.Amount(), serialized, intentMetric); prior != nil && prior.State != intentStateAbandoned {
				if !resolved {
					m.reconcileReceiveIntent(reference)
					m.receiveIntentMu.Lock()
					state := m.receiveIntents[reference]
					m.receiveIntentMu.Unlock()
					if state != nil && state.State == intentStatePending {
						// Evidence could not decide (checkstate unreachable):
						// refuse without a second Receive — the mint may have
						// taken the note on the first attempt.
						noticeEvent, noticeErr := m.CreateNoticeEvent("error", "payment-outcome-unknown",
							"This e-cash note was already submitted to this TollGate and its outcome is still being verified. Do not send it again. Reload this page in a couple of minutes.", macAddress)
						if noticeErr != nil {
							return nil, fmt.Errorf("pending-intent resubmission and failed to create notice: %w", noticeErr)
						}
						return noticeEvent, nil
					}
				}
				// The prior attempt reached a terminal state (granted, owed,
				// abandoned). Granted and owed both mean this router already
				// owes or delivered service for this exact note: the honest
				// answer is the grant-pending notice, never a second spend.
				noticeEvent, noticeErr := m.CreateNoticeEvent("error", "payment-received-grant-pending",
					"This e-cash note was already received by this TollGate. Do not pay again — if access has not started yet, it will begin automatically (reload in a minute).", macAddress)
				if noticeErr != nil {
					return nil, fmt.Errorf("resolved-intent resubmission and failed to create notice: %w", noticeErr)
				}
				return noticeEvent, nil
			}
		} else {
			log.Printf("PurchaseSession: could not serialize note for the intent journal (reference %s): %v — journaling skipped, payment proceeds", reference, serErr)
		}
	}

	ch := make(chan receiveResult, 1)
	go func() {
		// A panic can only fire before the normal send, so this never
		// double-sends; the channel buffer guarantees it never blocks.
		defer func() {
			if r := recover(); r != nil {
				ch <- receiveResult{0, fmt.Errorf("payment processing panicked: %v", r)}
			}
		}()
		amount, err := m.tollwallet.Receive(paymentCashuToken)
		ch <- receiveResult{amount, err}
	}()

	var amountAfterSwap uint64
	err = nil
	select {
	case res := <-ch:
		amountAfterSwap = res.amount
		err = res.err
		m.endReceive(reference)
		log.Printf("PurchaseSession: Receive completed, amount=%d, err=%v", amountAfterSwap, err)
	case <-time.After(receiveTimeout):
		// A money-moving request has been sent and its outcome is not known yet:
		// the mint may already have taken the customer's proofs into the
		// operator's wallet, or the request may still fail. The one thing that
		// must not happen is the customer submitting the same note again — if the
		// mint did receive it, the retry is refused as already-spent and the value
		// is gone with no session (the repository's own rule: "decide refund vs
		// late-grant explicitly; do not silently drop it"). The notice therefore
		// says the outcome is unknown rather than "timed out, try again", and it
		// carries a reference the customer can quote and the operator can find.
		//
		// The journal that will collect a late outcome and grant it is a separate
		// piece of work; until it exists, this branch grants nothing and says so
		// by not claiming that access will arrive on its own.
		log.Printf("PurchaseSession: Receive outcome unknown after %s for mint=%s mac=%s reference=%s — no session was granted; the customer was told not to resubmit the note",
			receiveTimeout, paymentCashuToken.Mint(), macAddress, reference)

		// The deadline does not cancel the money-moving call: it is still on the
		// wire, and its answer is the one fact that makes the reference above
		// worth quoting. Nothing else reads the channel once this branch
		// returns, so without the recorder the outcome of a `Receive` that
		// completed at t+1s was discarded in silence and the notice handed the
		// customer a reference that led the operator nowhere. The recorder also
		// owns the in-flight mark now: the note stays guarded until the outcome
		// is consumed, so a resubmission arriving after the deadline but before
		// the mint answers is refused exactly like a concurrent one.
		go func() {
			m.recordLateReceiveOutcome(ch, paymentCashuToken.Mint(), macAddress, reference, paymentCashuToken.Amount())
			m.endReceive(reference)
		}()

		message := "Your payment has not been confirmed yet: the mint has not answered this TollGate. Do not send this e-cash note again — if the mint did receive it, the note is already spent and a second attempt will be refused. Reload this page in a couple of minutes."
		if reference != "" {
			message += fmt.Sprintf(" If access does not start, show the operator this reference: %s.", reference)
		}
		noticeEvent, noticeErr := m.CreateNoticeEvent("error", "payment-outcome-unknown", message, macAddress)
		if noticeErr != nil {
			return nil, fmt.Errorf("payment outcome unknown and failed to create notice: %w", noticeErr)
		}
		return noticeEvent, nil
	}
	if err != nil {
		mintURL := paymentCashuToken.Mint()

		// The mint itself refused — stable evidence this attempt took no
		// value (spent-refusal, keyset-expired, fee-refusal). The intent
		// resolves abandoned: the customer may resubmit a still-spendable
		// note, and a sequential duplicate still lands in the mint's own
		// refusal below. Ambiguous outcomes (ErrOutcomeUnknown) stay
		// pending for the reconciler.
		if errors.Is(err, tollwallet.ErrTokenAlreadySpent) || isExpiredKeysetError(err) || isBelowSwapFeeError(err) || isRateLimitError(err) {
			m.resolveReceiveIntent(reference, intentStateAbandoned, fmt.Sprintf("mint refused: %v", err))
		}

		if !errors.Is(err, tollwallet.ErrTokenAlreadySpent) &&
			!errors.Is(err, tollwallet.ErrOutcomeUnknown) &&
			!isExpiredKeysetError(err) &&
			!isRateLimitError(err) {
			// A rate-limit answer means the mint is UP and telling us to slow
			// down, so it must not condemn the mint. On a single-mint
			// deployment a single 429 used to empty the reachable set, fire the
			// set-changed callback and downgrade the merchant to degraded mode —
			// i.e. one rate-limited request stopped every sale on the router
			// (a revenue DoS). The edge quota on POST /ln-invoice in main.go is
			// the other half of this fix: it keeps our own flood from being
			// what provokes the 429. An unanswered request (outcome unknown)
			// is likewise not evidence the mint is down — it may be happily
			// processing the swap whose response was dropped (#640).
			m.mintHealthTracker.MarkUnreachable(mintURL)
		}

		var errorCode string
		var errorMessage string

		if errors.Is(err, tollwallet.ErrTokenAlreadySpent) {
			errorCode = "payment-error-token-spent"
			errorMessage = "Token has already been spent"
		} else if errors.Is(err, tollwallet.ErrOutcomeUnknown) {
			// The mint never answered a request it may have processed: the
			// note may already be spent, so the customer must not resubmit
			// it — the same guidance the deadline branch above gives, reached
			// here when the client surfaces the ambiguity as an error before
			// the merchant deadline fires (#640).
			errorCode = "payment-outcome-unknown"
			reference := receiveReference(paymentCashuToken)
			errorMessage = "Your payment has not been confirmed yet: the mint has not answered this TollGate. Do not send this e-cash note again — if the mint did receive it, the note is already spent and a second attempt will be refused. Reload this page in a couple of minutes."
			if reference != "" {
				errorMessage += fmt.Sprintf(" If access does not start, show the operator this reference: %s.", reference)
			}
		} else if isRateLimitError(err) {
			errorCode = "mint-rate-limited"
			errorMessage = "Mint is rate-limiting requests. Please try again in a moment."
		} else if isBelowSwapFeeError(err) {
			errorCode = "payment-error-below-swap-fee"
			errorMessage = fmt.Sprintf(
				"This e-cash note is %d sat but mint %s charges a swap fee that leaves nothing left to spend. Use a larger token or a mint without fees.",
				paymentCashuToken.Amount(), mintURL)
		} else if isExpiredKeysetError(err) {
			errorCode = "payment-error-keyset-expired"
			errorMessage = fmt.Sprintf(
				"This e-cash note was issued on a keyset that mint %s has retired (expired): the proofs are no longer spendable there. The note cannot be recovered by retrying; obtain a new token. Cause: %v",
				mintURL, err)
		} else if isMintUnreachableError(err) {
			errorCode = "payment-error-mint-unreachable"
			errorMessage = fmt.Sprintf(
				"Mint %s is temporarily unavailable. Please try again, or use a token from another mint.",
				mintURL)
		} else {
			errorCode = "payment-processing-failed"
			errorMessage = fmt.Sprintf("Payment processing failed: %v", err)
		}

		noticeEvent, noticeErr := m.CreateNoticeEvent("error", errorCode, errorMessage, macAddress)
		if noticeErr != nil {
			return nil, fmt.Errorf("payment processing failed and failed to create notice: %w", noticeErr)
		}
		return noticeEvent, nil
	}

	log.Printf("Amount after swap: %d", amountAfterSwap)

	// Calculate allotment using the configured metric and mint-specific pricing
	mintURL := paymentCashuToken.Mint()
	allotment, err := m.calculateAllotment(amountAfterSwap, mintURL)
	if err != nil {
		noticeEvent, noticeErr := m.CreateNoticeEvent("error", "session-error",
			fmt.Sprintf("Failed to calculate allotment: %v", err), macAddress)
		if noticeErr != nil {
			return nil, fmt.Errorf("failed to calculate allotment and failed to create notice: %w", noticeErr)
		}
		return noticeEvent, nil
	}

	// Add allotment to the session and only persist the update if gate access opens.
	session, err := m.grantSessionAccess(macAddress, allotment)
	if err != nil {
		// The customer's value is already in the operator's wallet: Receive
		// succeeded, and only the grant failed (#403). What is owed is the
		// grant, so it is recorded durably and retried until it succeeds or
		// its window passes — the customer is told their access will start,
		// never to pay again.
		m.recordOwedGrant(reference, macAddress, paymentCashuToken.Mint(), amountAfterSwap, allotment)
		m.resolveReceiveIntent(reference, intentStateOwed, "gate could not open; owed entitlement recorded")

		message := fmt.Sprintf("Your payment was received, but this TollGate could not open your access just now (the portal manager did not answer). Your access will start automatically — you do NOT need to pay again. Reload this page in a minute. Reference: %s", reference)
		if reference == "" {
			message = "Your payment was received, but this TollGate could not open your access just now (the portal manager did not answer). Your access will start automatically — you do NOT need to pay again. Reload this page in a minute."
		}
		noticeEvent, noticeErr := m.CreateNoticeEvent("error", "payment-received-grant-pending", message, macAddress)
		if noticeErr != nil {
			return nil, fmt.Errorf("failed to manage session and failed to create notice: %w", noticeErr)
		}
		return noticeEvent, nil
	}

	m.resolveReceiveIntent(reference, intentStateGranted, "session granted")

	// Create a success session event (using MAC address as identifier in logs)
	sessionEvent, err := m.createSessionEvent(session, macAddress)
	if err != nil {
		return nil, fmt.Errorf("failed to create session event: %w", err)
	}

	return sessionEvent, nil
}

func isRateLimitError(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "429") ||
		strings.Contains(msg, "rate limit") ||
		strings.Contains(msg, "too many requests")
}

// swapFeePrecheckBudget bounds the fee precheck (#525): the wallet client's
// retry ladder (30 s per attempt, up to 5 attempts, chained endpoints) means
// a wedged mint — one that accepts nothing, docker pause reproduces it —
// parks SwapFeeSats for minutes, freezing the payment lane before anything
// with a deadline runs. The fee check is an optimization, not a gate: past
// the budget the payment proceeds and Receive's own error classification
// (or its receive timeout) governs.
const swapFeePrecheckBudget = 3 * time.Second

// swapFeeSatsBounded runs fee with swapFeePrecheckBudget. ok=false means the
// budget expired without an answer — treat the fee as unknown. The parked
// call is abandoned (it self-clears when the retry ladder exhausts); the
// result channel is buffered so a late answer does not leak a goroutine.
func swapFeeSatsBounded(fee func(tollwallet.Token) (uint64, error), token tollwallet.Token) (feeSats uint64, err error, ok bool) {
	type feeResult struct {
		fee uint64
		err error
	}
	res := make(chan feeResult, 1)
	go func() {
		f, err := fee(token)
		res <- feeResult{f, err}
	}()
	select {
	case r := <-res:
		return r.fee, r.err, true
	case <-time.After(swapFeePrecheckBudget):
		log.Printf("PurchaseSession: fee precheck overran its %s budget (wedged mint?) — proceeding without the fee check", swapFeePrecheckBudget)
		return 0, nil, false
	}
}

// isBelowSwapFeeError reports whether err is the "the token cannot cover the
// mint's swap fee" condition. gonuts returns "nothing to swap" when the total
// is below the fee, and mints reject a zero-output swap with "no outputs
// provided".
func isBelowSwapFeeError(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "nothing to swap") ||
		strings.Contains(msg, "no outputs provided") ||
		strings.Contains(msg, "swap fees")
}

// isExpiredKeysetError reports whether err is a mint refusal because the
// proofs sit on a keyset the mint has expired (NUT-02 rotation). cdk-mintd
// 0.17.6 refuses such swaps outright ("Keyset has expired"); the mint itself
// is healthy and a fresh-keyset payment succeeds — so this is a dead token,
// not an outage (#440).
func isExpiredKeysetError(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "keyset") && strings.Contains(msg, "expired")
}

// isMintUnreachableError reports whether err indicates the mint's keysets (or
// the mint itself) could not be reached, as opposed to a token rejection.
func isMintUnreachableError(err error) bool {
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "no such host") ||
		strings.Contains(msg, "i/o timeout") ||
		strings.Contains(msg, "temporarily unavailable") {
		return true
	}
	// Keyset resolution failures: "short keyset ID ... not found in mint
	// keysets", "could not resolve short keyset IDs", "error getting keyset ...".
	// Expired-keyset refusals ("Keyset has expired") are deliberately NOT
	// unreachable-class: the mint is healthy and the token is dead (#440).
	if strings.Contains(msg, "keyset") &&
		(strings.Contains(msg, "not found") ||
			strings.Contains(msg, "could not") ||
			strings.Contains(msg, "error getting") ||
			strings.Contains(msg, "resolve")) {
		return !isExpiredKeysetError(err)
	}
	return false
}

// isAmbiguousMintOutcomeError reports whether a `Receive` error leaves the fate
// of the note undecided, so the operator may not be told the value is safe to
// resubmit. It is deliberately the inverse of the explicit-refusal list rather
// than a list of ambiguous shapes: only the mint can establish that it did not
// take the note, and an error return cannot. The wallet's HTTP client is built
// with the same 30-second timeout as this module's `receiveTimeout`, so the
// answer that arrives late on a slow mint is normally a client-side `context
// deadline exceeded` — which says nothing about what the mint did with the
// proofs — and a 5xx answered after the mint processed the swap is ambiguous the
// same way. It composes `isMintUnreachableError` (transport- and keyset-
// resolution failures are the same kind of non-answer), and it treats anything
// unrecognised as ambiguous too: the hedged record costs the operator one
// balance check, while the confident one costs the customer their note, which
// the mint then refuses as already spent (#498).
func isAmbiguousMintOutcomeError(err error) bool {
	if err == nil {
		return false
	}
	// The wallet client's explicit no-answer verdict is ambiguous by
	// construction (gonuts-tollgate v0.13.0): the request may have been
	// processed and its signatures lost. Matched positively so a future
	// tightening of the refusal list below can never flip it silently.
	var ambiguous *gonutsclient.AmbiguousOutcomeError
	if errors.As(err, &ambiguous) {
		return true
	}
	return !isDefinitiveMintRefusal(err)
}

// isDefinitiveMintRefusal reports whether err is a refusal the mint itself
// returned — the only class of late error that establishes the note was not
// taken: the token is already spent, its keyset is retired, it cannot cover the
// swap fee, the mint is rate-limiting the request (429, refused before it was
// processed), or the mint answered with an explicit 4xx. Unreachable-class
// errors, timeouts and unrecognised errors are deliberately NOT refusals.
func isDefinitiveMintRefusal(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, tollwallet.ErrTokenAlreadySpent) ||
		isExpiredKeysetError(err) ||
		isBelowSwapFeeError(err) ||
		isRateLimitError(err) {
		return true
	}
	msg := strings.ToLower(err.Error())
	// The mint's own wording for a proof it has already seen (NUT-03/NUT-07),
	// relayed by gonuts as plain text.
	if strings.Contains(msg, "already spent") || strings.Contains(msg, "already signed") {
		return true
	}
	// An explicit HTTP rejection: the mint answered, and the answer is "no".
	for _, code := range []string{
		"status 400", "status 401", "status 403", "status 404", "status 409", "status 422",
	} {
		if strings.Contains(msg, code) {
			return true
		}
	}
	return false
}

func (m *Merchant) GetAdvertisement() string {
	ad, err := CreateAdvertisement(m.configManager, m.mintHealthTracker)
	if err != nil {
		return ""
	}
	return ad
}

func CreateAdvertisement(configManager *config_manager.ConfigManager, tracker *MintHealthTracker) (string, error) {
	config := configManager.GetConfig()
	if config == nil {
		return "", fmt.Errorf("main config is nil")
	}

	// The advertisement is the one thing the customer's client selects a mint
	// from, so it is the advertised set that is used here and not the reachable
	// set: a mint whose front answers 429 to everything is still up (reachable)
	// but cannot serve a purchase, and leaving it in means the client keeps
	// picking it while nothing self-heals. See
	// MintHealthTracker.GetAdvertisedMintConfigs for the single-mint guard that
	// keeps this from ever advertising nothing.
	advertisedMints := tracker.GetAdvertisedMintConfigs()

	advertisementEvent := nostr.Event{
		Kind: 10021,
		Tags: nostr.Tags{
			{"metric", config.Metric},
			{"step_size", fmt.Sprintf("%d", config.StepSize)},
			{"tips", "1", "2"},
		},
		Content: "",
	}

	for _, mintConfig := range advertisedMints {
		advertisementEvent.Tags = append(advertisementEvent.Tags, nostr.Tag{
			"price_per_step",
			"cashu",
			fmt.Sprintf("%d", mintConfig.PricePerStep),
			mintConfig.PriceUnit,
			mintConfig.URL,
			fmt.Sprintf("%d", mintConfig.MinPurchaseSteps),
		})
	}

	identities := configManager.GetIdentities()
	if identities == nil {
		return "", fmt.Errorf("identities config is nil")
	}
	merchantIdentity, err := identities.GetOwnedIdentity("merchant")
	if err != nil {
		return "", fmt.Errorf("merchant identity not found: %w", err)
	}
	// Sign
	err = advertisementEvent.Sign(merchantIdentity.PrivateKey)
	if err != nil {
		return "", fmt.Errorf("Error signing advertisement event: %v", err)
	}

	// Convert to JSON string for storage
	detailsBytes, err := json.Marshal(advertisementEvent)
	if err != nil {
		return "", fmt.Errorf("Error marshaling advertisement event: %v", err)
	}

	return string(detailsBytes), nil
}

// extractPaymentToken extracts the payment token from a payment event
func (m *Merchant) extractPaymentToken(paymentEvent nostr.Event) (string, error) {
	for _, tag := range paymentEvent.Tags {
		if len(tag) >= 2 && tag[0] == "payment" {
			return tag[1], nil
		}
	}
	return "", fmt.Errorf("no payment tag found in event")
}

// extractDeviceIdentifier extracts the device identifier (MAC address) from a payment event
func (m *Merchant) extractDeviceIdentifier(paymentEvent nostr.Event) (string, error) {
	for _, tag := range paymentEvent.Tags {
		if len(tag) >= 3 && tag[0] == "device-identifier" {
			return tag[2], nil // Return the actual identifier value
		}
	}
	return "", fmt.Errorf("no device-identifier tag found in event")
}

// calculateAllotment calculates allotment using the configured metric and mint-specific pricing
func (m *Merchant) calculateAllotment(amountSats uint64, mintURL string) (uint64, error) {
	// Find the mint configuration for this mint
	var mintConfig *config_manager.MintConfig
	for _, mint := range m.config.AcceptedMints {
		if tollwallet.MintURLMatches(mint.URL, mintURL) {
			mintConfig = &mint
			break
		}
	}

	if mintConfig == nil {
		return 0, fmt.Errorf("mint configuration not found for URL: %s", mintURL)
	}

	if mintConfig.PricePerStep == 0 {
		return 0, fmt.Errorf("price_per_step is 0 for mint %s (division by zero)", mintURL)
	}

	steps := amountSats / mintConfig.PricePerStep

	// Check if payment meets minimum purchase requirement
	if steps < mintConfig.MinPurchaseSteps {
		return 0, fmt.Errorf("payment only covers %d steps, but minimum purchase is %d steps", steps, mintConfig.MinPurchaseSteps)
	}

	switch m.config.Metric {
	case "milliseconds":
		return m.calculateAllotmentMs(steps)
	case "bytes":
		return m.calculateAllotmentBytes(steps)
	default:
		return 0, fmt.Errorf("unsupported metric: %s", m.config.Metric)
	}
}

// calculateAllotmentMs calculates allotment in milliseconds from steps
func (m *Merchant) calculateAllotmentMs(steps uint64) (uint64, error) {
	// Convert steps to milliseconds using configured step size
	totalMs := steps * m.config.StepSize

	log.Printf("Converting %d steps to %d ms using step size %d",
		steps, totalMs, m.config.StepSize)

	return totalMs, nil
}

// calculateAllotmentBytes calculates allotment in bytes from steps
func (m *Merchant) calculateAllotmentBytes(steps uint64) (uint64, error) {
	// Convert steps to bytes using configured step size
	totalBytes := steps * m.config.StepSize

	log.Printf("Converting %d steps to %d bytes using step size %d",
		steps, totalBytes, m.config.StepSize)

	return totalBytes, nil
}

// createSessionEvent creates a session event from the MAC-address based session
func (m *Merchant) createSessionEvent(session *CustomerSession, customerPubkey string) (*nostr.Event, error) {
	deviceIdentifier := session.MacAddress

	identities := m.configManager.GetIdentities()
	if identities == nil {
		return nil, fmt.Errorf("identities config is nil")
	}
	merchantIdentity, err := identities.GetOwnedIdentity("merchant")
	if err != nil {
		return nil, fmt.Errorf("merchant identity not found: %w", err)
	}

	// Get the public key from the private key
	tollgatePubkey, err := nostr.GetPublicKey(merchantIdentity.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to get public key: %w", err)
	}

	sessionEvent := &nostr.Event{
		Kind:      1022,
		PubKey:    tollgatePubkey,
		CreatedAt: nostr.Now(),
		Tags: nostr.Tags{
			{"p", customerPubkey},
			{"device-identifier", "mac", deviceIdentifier},
			{"allotment", fmt.Sprintf("%d", session.Allotment)},
			{"metric", session.Metric},
			{"start-time", fmt.Sprintf("%d", session.StartTime)},
		},
		Content: "",
	}

	// Sign with tollgate private key
	err = sessionEvent.Sign(merchantIdentity.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to sign session event: %w", err)
	}

	return sessionEvent, nil
}

// extendSessionEvent creates a new session event with extended duration
func (m *Merchant) extendSessionEvent(existingSession *nostr.Event, additionalAllotment uint64) (*nostr.Event, error) {
	// Extract existing allotment from the session
	existingAllotment, err := m.extractAllotment(existingSession)
	if err != nil {
		return nil, fmt.Errorf("failed to extract existing allotment: %w", err)
	}

	// Calculate leftover allotment based on metric type
	var leftoverAllotment uint64 = 0
	if m.config.Metric == "milliseconds" {
		// For time-based metrics, calculate how much time has passed
		sessionCreatedAt := time.Unix(int64(existingSession.CreatedAt), 0)
		timePassed := time.Since(sessionCreatedAt)
		timePassedInMetric := uint64(timePassed.Milliseconds())

		if existingAllotment > timePassedInMetric {
			leftoverAllotment = existingAllotment - timePassedInMetric
		}

		log.Printf("Session extension: existing=%d %s, passed=%d %s, leftover=%d %s, additional=%d %s",
			existingAllotment, m.config.Metric, timePassedInMetric, m.config.Metric,
			leftoverAllotment, m.config.Metric, additionalAllotment, m.config.Metric)
	} else {
		// For non-time metrics (like bytes), keep the full existing allotment
		leftoverAllotment = existingAllotment
		log.Printf("Session extension: existing=%d %s, leftover=%d %s (no decay), additional=%d %s",
			existingAllotment, m.config.Metric, leftoverAllotment, m.config.Metric,
			additionalAllotment, m.config.Metric)
	}

	// Calculate new total allotment
	newTotalAllotment := existingAllotment + additionalAllotment

	// Extract customer and device info from existing session
	customerPubkey := ""
	deviceIdentifier := ""

	for _, tag := range existingSession.Tags {
		if len(tag) >= 2 && tag[0] == "p" {
			customerPubkey = tag[1]
		}
		if len(tag) >= 3 && tag[0] == "device-identifier" {
			deviceIdentifier = tag[2]
		}
	}

	if customerPubkey == "" || deviceIdentifier == "" {
		return nil, fmt.Errorf("failed to extract customer or device info from existing session")
	}

	identities := m.configManager.GetIdentities()
	if identities == nil {
		return nil, fmt.Errorf("identities config is nil")
	}
	merchantIdentity, err := identities.GetOwnedIdentity("merchant")
	if err != nil {
		return nil, fmt.Errorf("merchant identity not found: %w", err)
	}
	// Get the public key from the private key
	tollgatePubkey, err := nostr.GetPublicKey(merchantIdentity.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to get public key: %w", err)
	}

	// Create new session event with extended duration
	sessionEvent := &nostr.Event{
		Kind:      1022,
		PubKey:    tollgatePubkey,
		CreatedAt: nostr.Now(),
		Tags: nostr.Tags{
			{"p", customerPubkey},
			{"device-identifier", "mac", deviceIdentifier},
			{"allotment", fmt.Sprintf("%d", newTotalAllotment)},
			{"metric", "milliseconds"},
		},
		Content: "",
	}

	// Sign with tollgate private key
	err = sessionEvent.Sign(merchantIdentity.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to sign extended session event: %w", err)
	}

	return sessionEvent, nil
}

// extractAllotment extracts allotment from a session event
func (m *Merchant) extractAllotment(sessionEvent *nostr.Event) (uint64, error) {
	for _, tag := range sessionEvent.Tags {
		if len(tag) >= 2 && tag[0] == "allotment" {
			allotment, err := strconv.ParseUint(tag[1], 10, 64)
			if err != nil {
				return 0, fmt.Errorf("failed to parse allotment: %w", err)
			}
			return allotment, nil
		}
	}
	return 0, fmt.Errorf("no allotment tag found in session event")
}

// CreateNoticeEvent creates a notice event for error communication
func createNoticeEvent(configManager *config_manager.ConfigManager, level, code, message, customerPubkey string) (*nostr.Event, error) {
	identities := configManager.GetIdentities()
	if identities == nil {
		return nil, fmt.Errorf("identities config is nil")
	}
	merchantIdentity, err := identities.GetOwnedIdentity("merchant")
	if err != nil {
		return nil, fmt.Errorf("merchant identity not found: %w", err)
	}
	tollgatePubkey, err := nostr.GetPublicKey(merchantIdentity.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to get public key: %w", err)
	}
	noticeEvent := &nostr.Event{
		Kind:      21023,
		PubKey:    tollgatePubkey,
		CreatedAt: nostr.Now(),
		Tags: nostr.Tags{
			{"level", level},
			{"code", code},
		},
		Content: message,
	}
	if customerPubkey != "" {
		noticeEvent.Tags = append(noticeEvent.Tags, nostr.Tag{"p", customerPubkey})
	}
	err = noticeEvent.Sign(merchantIdentity.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to sign notice event: %w", err)
	}
	return noticeEvent, nil
}

func (m *Merchant) CreateNoticeEvent(level, code, message, customerPubkey string) (*nostr.Event, error) {
	return createNoticeEvent(m.configManager, level, code, message, customerPubkey)
}

// MerchantInterface method implementations

// CreatePaymentToken creates a payment token for the specified mint and amount
func (m *Merchant) CreatePaymentToken(mintURL string, amount uint64) (string, error) {
	// Check balance before attempting to send
	balance := m.tollwallet.GetBalanceByMint(mintURL)
	totalBalance := m.tollwallet.GetBalance()

	log.Printf("Creating payment token: amount=%d, mintURL=%s, balance_by_mint=%d, total_balance=%d",
		amount, mintURL, balance, totalBalance)

	if balance < amount {
		return "", fmt.Errorf("insufficient balance: need %d sats, have %d sats for mint %s (total balance: %d)",
			amount, balance, mintURL, totalBalance)
	}

	// Use the tollwallet to create a payment token with basic send
	token, err := m.tollwallet.Send(amount, mintURL, true)
	if err != nil {
		return "", fmt.Errorf("failed to create payment token: %w", err)
	}

	// Validate token has proofs
	if token == nil {
		return "", fmt.Errorf("token creation returned nil token")
	}

	// Serialize token to string
	tokenString, err := token.Serialize()
	if err != nil {
		return "", fmt.Errorf("failed to serialize token: %w", err)
	}

	// Validate serialized token is not empty
	if tokenString == "" {
		return "", fmt.Errorf("token serialization returned empty string")
	}

	// Never log the token: it is spendable by whoever reads the line. The length
	// and the salted fingerprint are what an operator can actually use — the
	// fingerprint to match this note against a log line or a customer report.
	log.Printf("Successfully created payment token: length=%d, token_fingerprint=%s",
		len(tokenString), utils.TokenFingerprint(tokenString))

	return tokenString, nil
}

// DrainMint drains all available balance from a specific mint
// This method is designed for wallet draining and does NOT include fees
// to avoid insufficient funds errors when extracting all available balance
func (m *Merchant) DrainMint(mintURL string) (string, uint64, error) {
	// Check balance before attempting to drain
	balance := m.tollwallet.GetBalanceByMint(mintURL)

	log.Printf("Draining mint: mintURL=%s, balance=%d", mintURL, balance)

	if balance == 0 {
		return "", 0, fmt.Errorf("no balance available for mint %s", mintURL)
	}

	// Use the tollwallet's Drain method which doesn't include fees
	token, actualAmount, err := m.tollwallet.Drain(mintURL)
	if err != nil {
		return "", 0, fmt.Errorf("failed to drain mint: %w", err)
	}

	// Validate token has proofs
	if token == nil {
		return "", 0, fmt.Errorf("drain returned nil token")
	}

	// Serialize token to string
	tokenString, err := token.Serialize()
	if err != nil {
		return "", 0, fmt.Errorf("failed to serialize drain token: %w", err)
	}

	// Validate serialized token is not empty
	if tokenString == "" {
		return "", 0, fmt.Errorf("drain token serialization returned empty string")
	}

	log.Printf("Successfully drained mint %s: amount=%d, token_length=%d",
		mintURL, actualAmount, len(tokenString))

	return tokenString, actualAmount, nil
}

// CreatePaymentTokenWithOverpayment creates a payment token with overpayment capability
func (m *Merchant) CreatePaymentTokenWithOverpayment(mintURL string, amount uint64, maxOverpaymentPercent uint64, maxOverpaymentAbsolute uint64) (string, error) {
	// Use the tollwallet's new SendWithOverpayment method
	tokenString, err := m.tollwallet.SendWithOverpayment(amount, mintURL, maxOverpaymentPercent, maxOverpaymentAbsolute)
	if err != nil {
		return "", fmt.Errorf("failed to create payment token with overpayment: %w", err)
	}
	return tokenString, nil
}

// GetAcceptedMints returns the list of accepted mints from the configuration
func (m *Merchant) GetAcceptedMints() []config_manager.MintConfig {
	return m.mintHealthTracker.GetReachableMintConfigs()
}

// GetBalance returns the total balance across all mints
func (m *Merchant) GetBalance() uint64 {
	return m.tollwallet.GetBalance()
}

// GetBalanceByMint returns the balance for a specific mint
func (m *Merchant) GetBalanceByMint(mintURL string) uint64 {
	return m.tollwallet.GetBalanceByMint(mintURL)
}

// GetAllMintBalances returns a map of all mints and their balances in the wallet
func (m *Merchant) GetAllMintBalances() map[string]uint64 {
	return m.tollwallet.GetAllMintBalances()
}

// NormalizeMACAddress returns the canonical form of a client MAC address:
// trimmed and lowercased. Sessions (customerSessions) and lightning quotes
// (lightningQuoteRecord.MacAddress) are keyed and compared as case-sensitive
// strings, while every producer in the system — nodogsplash preauth, the
// DHCP-lease and ARP lookups behind getMacAddress, /whoami — is lowercase.
// Normalising on the way in and on the way out makes one address in any casing
// resolve to the same session and the same quote, instead of reporting an
// existing quote as "not found". This only trims and lowercases; MAC validity
// stays the job of ValidateMACAddress.
func NormalizeMACAddress(macAddress string) string {
	return strings.ToLower(strings.TrimSpace(macAddress))
}

// GetSession retrieves a customer session by MAC address. It answers
// ErrSessionNotFound when the MAC has no record and ErrSessionExpired when a
// milliseconds session has spent its allotment (the spent record is retired on
// the way out, as it always was).
func (m *Merchant) GetSession(macAddress string) (*CustomerSession, error) {
	macAddress = NormalizeMACAddress(macAddress)

	m.sessionMu.RLock()
	session, exists := m.customerSessions[macAddress]
	if exists {
		// Clone INSIDE the read lock. `attachmentUsage` is written in place by the
		// usage monitor on every sweep (noteAttachmentUsage), so a clone taken
		// after RUnlock reads a field of a record that is still live: a data race
		// (the shipped suites never polled concurrently with the monitor, which is
		// how it stayed invisible) and, on the 32-bit mips/mipsel router targets, a
		// torn uint64 read is a real misread. Cloning here keeps the invariant the
		// rest of this method already assumes: nothing reads a shared record
		// outside sessionMu.
		session = cloneCustomerSession(session)
	}
	m.sessionMu.RUnlock()
	if !exists {
		return nil, fmt.Errorf("%w for MAC address: %s", ErrSessionNotFound, macAddress)
	}

	if sessionHasExpired(session, time.Now()) {
		m.sessionMu.Lock()
		// Re-check under the write lock: a replacement session created in
		// between (a renewal) must not be dropped by this lookup's conclusion
		// about the record it read.
		if currentSession, exists := m.customerSessions[macAddress]; exists && sessionHasExpired(currentSession, time.Now()) {
			m.expireSessionLocked(macAddress)
		}
		m.sessionMu.Unlock()
		return nil, fmt.Errorf("%w for MAC address: %s", ErrSessionExpired, macAddress)
	}

	// Already a clone: it was taken under the read lock above.
	return session, nil
}

// GetSessionState reports the machine-readable session state of a MAC, so a
// portal can tell a first-time visitor (none) from a customer whose paid session
// ran out (expired) — a distinction /usage's "-1/-1" cannot express.
//
// The lookup is read-only apart from retiring a spent milliseconds record, which
// is exactly what any /usage poll already does; /usage, /balance and the money
// path are unchanged by it.
func (m *Merchant) GetSessionState(macAddress string) (SessionState, error) {
	macAddress = NormalizeMACAddress(macAddress)
	if macAddress == "" {
		return SessionStateNone, nil
	}

	session, err := m.GetSession(macAddress)
	if err == nil && session != nil {
		return SessionStateActive, nil
	}
	if err != nil && !errors.Is(err, ErrSessionNotFound) && !errors.Is(err, ErrSessionExpired) {
		return SessionStateNone, err
	}

	m.sessionMu.RLock()
	observedExpiry := m.sessionKnownToHaveExpiredLocked(macAddress)
	m.sessionMu.RUnlock()
	if observedExpiry {
		return SessionStateExpired, nil
	}

	return SessionStateNone, nil
}

// sessionIsRenewal reports whether this MAC is a returning customer: it has a
// session now, or had one that expired. Used by the payment pre-flight, which
// must not treat a returning customer as a device NDS has never seen.
func (m *Merchant) sessionIsRenewal(macAddress string) bool {
	state, err := m.GetSessionState(macAddress)
	if err != nil {
		return false
	}
	return state == SessionStateActive || state == SessionStateExpired
}

func cloneCustomerSession(session *CustomerSession) *CustomerSession {
	if session == nil {
		return nil
	}

	copy := *session
	return &copy
}

func (m *Merchant) snapshotSession(macAddress string) (*CustomerSession, bool) {
	macAddress = NormalizeMACAddress(macAddress)

	m.sessionMu.RLock()
	defer m.sessionMu.RUnlock()

	session, exists := m.customerSessions[macAddress]
	if !exists {
		return nil, false
	}

	return cloneCustomerSession(session), true
}

func (m *Merchant) restoreSession(macAddress string, previousSession *CustomerSession, hadSession bool) {
	macAddress = NormalizeMACAddress(macAddress)

	m.sessionMu.Lock()
	defer m.sessionMu.Unlock()

	if hadSession {
		m.customerSessions[macAddress] = cloneCustomerSession(previousSession)
		return
	}

	delete(m.customerSessions, macAddress)
}

// clientProbeSeam returns the read-only NDS identity probe and the retry delay
// this merchant's payment pre-flight uses.
//
// A nil probe means valve.CheckClientState and a delay <= 0 means
// preflightRetryDelayDefault, so a zero-value &Merchant{} behaves exactly as it
// did before this seam existed. The coercion is deliberate and applies to
// negatives too: "no delay" is not expressible, because the retry only runs when
// the probe answered "not registered" with no error, and a test that wants that
// path to be effectively instant asks for a nanosecond rather than zero.
// Production never sets either field (nothing outside tests calls setClientProbe).
func (m *Merchant) clientProbeSeam() (func(string) (valve.ClientState, error), time.Duration) {
	m.clientProbeMu.RLock()
	defer m.clientProbeMu.RUnlock()

	probe := m.clientProbe
	if probe == nil {
		probe = valve.CheckClientState
	}
	delay := m.clientProbeDelay
	if delay <= 0 {
		delay = preflightRetryDelayDefault
	}
	return probe, delay
}

// setClientProbe replaces the payment pre-flight's identity probe and retry
// delay for this merchant only. It exists for tests; production leaves both
// unset and uses valve.CheckClientState and preflightRetryDelayDefault. It is a
// per-merchant seam, like setStaleBindingProbe, because the usage monitor runs
// on its own goroutine in the same test binary.
func (m *Merchant) setClientProbe(probe func(string) (valve.ClientState, error), delay time.Duration) {
	m.clientProbeMu.Lock()
	defer m.clientProbeMu.Unlock()

	m.clientProbe, m.clientProbeDelay = probe, delay
}

// clientRegisteredForGate is the pre-Receive pre-flight of issue #403: a
// payment whose MAC NDS does not know cannot have its gate opened, so
// accepting it would consume the customer's token with no session and no
// refund path. Probe errors fail open — a broken probe must not become a
// payment denial of service.
func (m *Merchant) clientRegisteredForGate(macAddress string) bool {
	probe, retryDelay := m.clientProbeSeam()
	for attempt := 1; attempt <= preflightProbeAttempts; attempt++ {
		state, err := probe(macAddress)
		if err != nil {
			log.Printf("PurchaseSession pre-flight: NDS probe error, failing open (attempt %d): %v", attempt, err)
			return true
		}
		if state.Registered {
			return true
		}
		if attempt < preflightProbeAttempts {
			time.Sleep(retryDelay)
		}
	}

	log.Printf("PurchaseSession pre-flight: MAC %s not registered in NDS after %d probes; refusing payment before Receive",
		macAddress, preflightProbeAttempts)
	return false
}

// AddAllotment adds allotment to a customer session, creating it if it doesn't
// exist. A session whose allotment is already spent (a milliseconds record that
// outlived its time because nothing looked it up) is retired first: extending it
// would hand back the allotment that was consumed — two 600 s purchases left a
// 1200 s session — and would keep reporting the spent session to /usage and
// /session-state instead of the renewal's fresh one.
func (m *Merchant) AddAllotment(macAddress, metric string, amount uint64) (*CustomerSession, error) {
	macAddress = NormalizeMACAddress(macAddress)

	m.sessionMu.Lock()
	defer m.sessionMu.Unlock()

	session, exists := m.customerSessions[macAddress]
	if exists && sessionHasExpired(session, time.Now()) {
		m.expireSessionLocked(macAddress)
		exists = false
	}
	if !exists {
		if m.customerSessions == nil {
			// Bare-literal merchants (test harnesses) now reach this path
			// through the owed-grant monitor the #502 journal can trigger
			// from any payment path — the map gets the same lazy init the
			// other journals have.
			m.customerSessions = make(map[string]*CustomerSession)
		}
		// Create new session
		session = &CustomerSession{
			MacAddress: macAddress,
			StartTime:  time.Now().Unix(),
			Metric:     metric,
			Allotment:  amount,
		}
		m.customerSessions[macAddress] = session
	} else {
		// Add to existing session and reset start time to now
		session.Allotment += amount
		session.StartTime = time.Now().Unix()
	}

	return session, nil
}

// Fund adds a cashu token to the wallet
func (m *Merchant) Fund(cashuToken string) (uint64, error) {
	log.Printf("Funding wallet with cashu token (length: %d)", len(cashuToken))

	// Basic validation - cashu tokens typically start with "cashuA" and are much longer
	if len(cashuToken) < 10 {
		return 0, fmt.Errorf("invalid cashu token: token too short (expected cashu token format)")
	}

	// Parse the cashu token with error recovery. The token itself is never
	// logged (it is spendable by whoever reads the line): length plus the salted
	// fingerprint, which is stable for the same note and useless to a reader.
	log.Printf("Attempting to decode token (length: %d, token_fingerprint: %s)",
		len(cashuToken), utils.TokenFingerprint(cashuToken))

	parsedToken, err := tollwallet.DecodeToken(cashuToken)
	if err != nil {
		log.Printf("Failed to decode cashu token (length: %d): %v", len(cashuToken), err)
		return 0, fmt.Errorf("invalid cashu token format: %w", err)
	}

	if m.tollwallet == nil {
		parsedToken.Close()
		return 0, fmt.Errorf("failed to receive token: %w", tollwallet.ErrWalletNotInitialized)
	}

	amountReceived, err := m.tollwallet.Receive(parsedToken)
	if err != nil {
		log.Printf("Failed to receive cashu token: %v", err)
		return 0, fmt.Errorf("failed to receive token: %w", err)
	}

	log.Printf("Successfully funded wallet with %d sats", amountReceived)
	return amountReceived, nil
}
