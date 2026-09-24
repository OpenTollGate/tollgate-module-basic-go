package merchant

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

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

// ndsClientCheck is a seam over valve.CheckClientState so tests can stub the
// read-only NDS probe without a router.
var ndsClientCheck = valve.CheckClientState

// preflightProbeAttempts mirrors the valve auth-retry budget: the reseller
// flow's upstream NDS registers client sessions asynchronously, so absence at
// first probe is not final.
const preflightProbeAttempts = 5

// preflightRetryDelay is a var so tests can shrink it.
var preflightRetryDelay = 400 * time.Millisecond

// receiveTimeout bounds how long PurchaseSession waits for the mint's answer to
// a money-moving `Receive` before it answers the customer with "outcome
// unknown". It is a var, like preflightRetryDelay, so a test can shrink the
// window instead of waiting it out; nothing in production reassigns it.
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
func recordLateReceiveOutcome(ch <-chan receiveResult, mintURL, macAddress, reference string) {
	res := <-ch
	if res.err != nil {
		log.Printf("PurchaseSession: late Receive FAILED for mint=%s mac=%s reference=%s: %v — the mint did not take the note, no session was granted",
			mintURL, macAddress, reference, res.err)
		return
	}
	log.Printf("PurchaseSession: late Receive COMPLETED for mint=%s mac=%s reference=%s amount=%d — the mint took the note and no session was granted; credit or refund it",
		mintURL, macAddress, reference, res.amount)
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
	unmeteredMu       sync.Mutex
	unmeteredSessions map[string]*unmeteredSession
	lightningQuotes   map[string]*lightningQuoteRecord
	lightningQuoteMu  sync.RWMutex
	quoteStore        *quoteStore
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

	advertisementStr, err := CreateAdvertisement(configManager, mintHealthTracker)
	if err != nil {
		return nil, fmt.Errorf("failed to create advertisement: %w", err)
	}

	log.Printf("Accepted Mints: %v", config.AcceptedMints)
	log.Printf("Wallet Balance: %d", balance)
	log.Printf("Advertisement: %s", advertisementStr)
	log.Printf("=== Merchant ready ===")

	m := &Merchant{
		config:            config,
		configManager:     configManager,
		tollwallet:        tw,
		mintHealthTracker: mintHealthTracker,
		customerSessions:  make(map[string]*CustomerSession),
		expiredSessions:   make(map[string]int64),
		lightningQuotes:   make(map[string]*lightningQuoteRecord),
		quoteStore:        newQuoteStore(filepath.Join(walletDirPath, "quotes.json")),
	}

	m.loadLightningQuotesFromDisk()
	m.StartPayoutRoutine()
	m.StartDataUsageMonitoring()
	m.startLightningQuoteJanitor()

	return m, nil
}

func (m *Merchant) Shutdown() error {
	return m.tollwallet.Shutdown()
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
		// Get data usage since baseline
		usage, err := valve.GetDataUsageSinceBaseline(macAddress)
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

// StartDataUsageMonitoring starts a background routine to monitor data usage for active sessions
func (m *Merchant) StartDataUsageMonitoring() {
	log.Printf("Starting data usage monitoring routine")

	ticker := time.NewTicker(2 * time.Second) // Check every 2 seconds
	go func() {
		defer ticker.Stop()
		for range ticker.C {
			m.checkDataUsage()
		}
	}()
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
type unmeteredSession struct {
	sweeps              int
	lastBaselineAttempt time.Time
}

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

// clearUnmetered forgets the bookkeeping of a session that is enforceable again.
func (m *Merchant) clearUnmetered(macAddress string) {
	m.unmeteredMu.Lock()
	defer m.unmeteredMu.Unlock()

	delete(m.unmeteredSessions, macAddress)
}

// enforceBytesSession is the usage monitor's per-session step. Every sweep takes
// the session one step closer to enforcement — meter it against its allotment,
// re-establish the baseline it is missing, or close a gate that cannot be
// metered at all. The one thing it must never do is skip the session: a bytes
// session the monitor stops looking at keeps an open gate for as long as the
// process lives, which is exactly the free, unmetered internet this module
// exists to prevent (C1-2b, C1-2c).
func (m *Merchant) enforceBytesSession(macAddress string, session *CustomerSession) {
	// A session whose baseline was never established cannot be metered. The old
	// code answered this with `continue` on every sweep, for ever.
	if !valve.HasDataBaseline(macAddress) {
		m.establishBaseline(macAddress)
		return
	}

	usage, err := valve.GetDataUsageSinceBaseline(macAddress)
	if err != nil {
		if errors.Is(err, valve.ErrDataBaselineMissing) {
			m.establishBaseline(macAddress)
			return
		}
		m.closeUnmeterableSession(macAddress, err)
		return
	}
	m.clearUnmetered(macAddress)

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
		log.Printf("ERROR: could not close the gate for %s after its allotment was spent: %v — the session is retained and the close is retried; the client may still hold open, unmetered access (unconfirmed gate closes=%d)",
			macAddress, err, valve.GateCloseFailures())
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
// the close is confirmed, and the gate stays tracked and is retried otherwise.
func (m *Merchant) closeUnmeterableSession(macAddress string, usageErr error) {
	sweeps := m.noteUnmeterableSweep(macAddress)

	if sweeps <= usageMonitorGraceSweeps {
		log.Printf("WARNING: cannot read the usage of the bytes session of %s (%v), sweep %d/%d — the session stays tracked",
			macAddress, usageErr, sweeps, usageMonitorGraceSweeps)
		return
	}

	log.Printf("ERROR: the usage of the bytes session of %s has been unreadable for %d sweeps (%v): the session cannot be metered, so its gate is closed rather than left open unmetered",
		macAddress, sweeps, usageErr)

	if err := valve.CloseGate(macAddress); err != nil {
		log.Printf("ERROR: could not close the gate of the unmeterable session of %s: %v — the session is retained and the close is retried (unconfirmed gate closes=%d)",
			macAddress, err, valve.GateCloseFailures())
		return
	}

	m.sessionMu.Lock()
	m.expireSessionLocked(macAddress)
	m.sessionMu.Unlock()
	log.Printf("Removed unmeterable session for %s", macAddress)
}

func (m *Merchant) StartPayoutRoutine() {
	log.Printf("Starting payout routine")

	for _, mint := range m.config.AcceptedMints {
		go func(mintConfig config_manager.MintConfig) {
			ticker := time.NewTicker(1 * time.Minute)
			defer ticker.Stop()

			for range ticker.C {
				if !m.mintHealthTracker.IsReachable(mintConfig.URL) {
					continue
				}
				m.processPayout(mintConfig)
			}
		}(mint)
	}

	m.mintHealthTracker.StartProactiveChecks()

	log.Printf("Payout routine started")
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
	// unreachable), fall through and let Receive classify the error.
	if fee, feeErr := m.tollwallet.SwapFeeSats(paymentCashuToken); feeErr == nil && fee > 0 {
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
		reference := receiveReference(paymentCashuToken)
		log.Printf("PurchaseSession: Receive outcome unknown after %s for mint=%s mac=%s reference=%s — no session was granted; the customer was told not to resubmit the note",
			receiveTimeout, paymentCashuToken.Mint(), macAddress, reference)

		// The deadline does not cancel the money-moving call: it is still on the
		// wire, and its answer is the one fact that makes the reference above
		// worth quoting. Nothing else reads the channel once this branch
		// returns, so without the recorder the outcome of a `Receive` that
		// completed at t+1s was discarded in silence and the notice handed the
		// customer a reference that led the operator nowhere.
		go recordLateReceiveOutcome(ch, paymentCashuToken.Mint(), macAddress, reference)

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

		if !errors.Is(err, tollwallet.ErrTokenAlreadySpent) && !isExpiredKeysetError(err) {
			m.mintHealthTracker.MarkUnreachable(mintURL)
		}

		var errorCode string
		var errorMessage string

		if errors.Is(err, tollwallet.ErrTokenAlreadySpent) {
			errorCode = "payment-error-token-spent"
			errorMessage = "Token has already been spent"
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
		errorCode := "session-error"
		errorMessage := fmt.Sprintf("Failed to manage session: %v", err)
		if strings.Contains(err.Error(), "failed to open gate:") {
			errorCode = "session-error"
			errorMessage = err.Error()
		}
		noticeEvent, noticeErr := m.CreateNoticeEvent("error", errorCode,
			errorMessage, macAddress)
		if noticeErr != nil {
			return nil, fmt.Errorf("failed to manage session and failed to create notice: %w", noticeErr)
		}
		return noticeEvent, nil
	}

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

	reachableMints := tracker.GetReachableMintConfigs()

	advertisementEvent := nostr.Event{
		Kind: 10021,
		Tags: nostr.Tags{
			{"metric", config.Metric},
			{"step_size", fmt.Sprintf("%d", config.StepSize)},
			{"tips", "1", "2"},
		},
		Content: "",
	}

	for _, mintConfig := range reachableMints {
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

	return cloneCustomerSession(session), nil
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

// clientRegisteredForGate is the pre-Receive pre-flight of issue #403: a
// payment whose MAC NDS does not know cannot have its gate opened, so
// accepting it would consume the customer's token with no session and no
// refund path. Probe errors fail open — a broken probe must not become a
// payment denial of service.
func (m *Merchant) clientRegisteredForGate(macAddress string) bool {
	for attempt := 1; attempt <= preflightProbeAttempts; attempt++ {
		state, err := ndsClientCheck(macAddress)
		if err != nil {
			log.Printf("PurchaseSession pre-flight: NDS probe error, failing open (attempt %d): %v", attempt, err)
			return true
		}
		if state.Registered {
			return true
		}
		if attempt < preflightProbeAttempts {
			time.Sleep(preflightRetryDelay)
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
