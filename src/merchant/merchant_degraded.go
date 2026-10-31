package merchant

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/tollwallet"
	"github.com/nbd-wtf/go-nostr"
)

type Wallet interface {
	GetBalance() uint64
	GetBalanceByMint(mintUrl string) uint64
	GetAllMintBalances() map[string]uint64
	SendWithOverpayment(amount uint64, mintUrl string, maxOverpaymentPercent uint64, maxOverpaymentAbsolute uint64) (string, error)
	Shutdown() error
}

type WalletFactory func(walletPath string, mintURLs []string) (Wallet, error)

type MerchantDegraded struct {
	configManager     *config_manager.ConfigManager
	mintHealthTracker *MintHealthTracker
	onUpgrade         func(MerchantInterface)
	wallet            Wallet
	walletLoaded      bool
	walletPath        string
	// walletInitErr is why the offline wallet load failed, if it failed.
	// It distinguishes "no wallet yet" causes so operator-facing errors can
	// stop blaming the mints when the mints are fine (#583): a storage
	// filesystem that cannot back bbolt's shared mmap is permanent, while
	// "no reachable mints" promises a recovery that will never come.
	walletInitErr error

	// degradeReason is WHY this merchant is degraded — the honest answer
	// the API surfaces owe the operator. The two classes are different
	// failures with the same symptom: "no reachable mints" (the wallet
	// waits for connectivity) vs "wallet initialization failed: …" (the
	// mints may be perfectly reachable and the WALLET is broken — the
	// #583/#1035 storage family answered "no reachable mints" for days
	// while every mint probed 200 OK). Empty means the legacy default
	// text.
	degradeReason string
	reasonMu      sync.RWMutex

	// The first-reachable trigger is a ONE-SHOT latch on the tracker
	// (hadReachableMint): after the first upgrade attempt fails — say the
	// wallet error is persistent, like the #583/#1035 storage class —
	// nothing ever fires again and the service stays degraded for life.
	// The wallet retry loop below is the repeating half: while at least
	// one mint is reachable, it re-attempts the upgrade on a bounded,
	// backing-off cadence, independent of the latch. It is armed
	// idempotently (WireWalletRetry) wherever the trigger is wired.
	retryStop     chan struct{}
	retryStopOnce sync.Once
	upgradeMu     sync.Mutex
	upgraded      bool

	// onUpgradeMu guards onUpgrade: the retry loop armed by New() can be
	// mid-AttemptUpgrade (reading the callback) while the caller —
	// installMerchant, a test, main.go's downgrade path — is still
	// attaching it.
	onUpgradeMu sync.RWMutex
}

// Retry cadence for the degraded wallet-init loop. First retry soon (an
// operator fixing wallet.db should not wait a proactive cycle), then
// back off to the proactive cadence so a permanent failure costs one
// bounded attempt per interval, not a spin. Package vars so tests can
// shrink them.
var (
	degradedWalletRetryInitial = 30 * time.Second
	degradedWalletRetryMax     = 5 * time.Minute
)

// SetDegradeReason records why the merchant degraded; the degraded API
// stubs surface it verbatim.
func (m *MerchantDegraded) SetDegradeReason(reason string) {
	m.reasonMu.Lock()
	m.degradeReason = reason
	m.reasonMu.Unlock()
}

// DegradeReason returns the recorded reason, or the legacy default.
func (m *MerchantDegraded) DegradeReason() string {
	m.reasonMu.RLock()
	defer m.reasonMu.RUnlock()
	if m.degradeReason == "" {
		return "no reachable mints"
	}
	return m.degradeReason
}

// notInitializedError is the single honest answer every degraded stub
// returns: the reason the wallet is down, not a hardcoded connectivity
// claim.
func (m *MerchantDegraded) notInitializedError() error {
	// The storage-mmap class must name the filesystem and its remedy in
	// every op error — the #583 misdirection these surfaces exist to
	// prevent. Everything else keeps the set degrade reason (or the
	// historic "no reachable mints"), byte-compatible with before.
	if m.StorageIncompatible() {
		return m.walletNotInitializedError()
	}
	return fmt.Errorf("wallet not initialized: %s", m.DegradeReason())
}

// WireWalletRetry arms the repeating wallet-init retry. Idempotent: the
// second call (e.g. New() wiring followed by installMerchant's
// WireStartupDegraded) must not spawn a second loop. The loop skips its
// tick while no mint is reachable — that class belongs to the
// first-reachable trigger — and stops on successful upgrade or Shutdown.
func (m *MerchantDegraded) WireWalletRetry() {
	m.retryStopOnce.Do(func() {
		// Snapshot the cadence ONCE, here: the loop must not read the
		// package vars after arming (tests rewrite them in Cleanup while
		// an earlier loop may still be draining its last tick).
		initial, max := degradedWalletRetryInitial, degradedWalletRetryMax
		m.retryStop = make(chan struct{})
		go func() {
			interval := initial
			for {
				timer := time.NewTimer(interval)
				select {
				case <-m.retryStop:
					timer.Stop()
					return
				case <-timer.C:
				}
				if len(m.mintHealthTracker.GetReachableMintConfigs()) == 0 {
					continue // unreachable-class: the trigger owns recovery
				}
				log.Printf("Degraded wallet retry: mints reachable, re-attempting wallet initialization (every %s while it fails)", interval)
				m.AttemptUpgrade()
				if m.isUpgraded() {
					return
				}
				interval *= 2
				if interval > max {
					interval = max
				}
			}
		}()
	})
}

func (m *MerchantDegraded) isUpgraded() bool {
	m.upgradeMu.Lock()
	defer m.upgradeMu.Unlock()
	return m.upgraded
}

func (m *MerchantDegraded) stopWalletRetry() {
	if m.retryStop != nil {
		select {
		case <-m.retryStop:
		default:
			close(m.retryStop)
		}
	}
}

func NewMerchantDegradedWithWallet(configManager *config_manager.ConfigManager, mintHealthTracker *MintHealthTracker, walletFactory WalletFactory, walletPath string) *MerchantDegraded {
	deg := &MerchantDegraded{
		configManager:     configManager,
		mintHealthTracker: mintHealthTracker,
		walletPath:        walletPath,
	}

	allMints := mintHealthTracker.GetAllConfiguredMintConfigs()
	if len(allMints) == 0 {
		log.Printf("Degraded mode: no configured mints, wallet not loaded")
		return deg
	}

	mintURLs := make([]string, len(allMints))
	for i, mint := range allMints {
		mintURLs[i] = mint.URL
	}

	wallet, err := walletFactory(walletPath, mintURLs)
	if err != nil {
		deg.walletInitErr = err
		if errors.Is(err, tollwallet.ErrWalletLocked) {
			// Not "first boot or no cached data": another process holds the
			// DB, and no mint-state transition can change that (#504).
			log.Printf("Degraded mode: wallet database is held by another process — "+
				"stop the other wallet holder (daemon or sidecar); the mint-recovery "+
				"upgrade cannot succeed while it runs: %v", err)
			return deg
		}
		if tollwallet.IsStorageMmapUnsupported(err) {
			log.Printf("Degraded mode: wallet storage filesystem does not support shared mmap (jffs2 overlay?): %v — the wallet can NEVER initialize on this filesystem; move wallet.db to an mmap-capable filesystem (ext4/f2fs/ubifs), see README storage requirements (#583)", err)
		} else {
			log.Printf("Degraded mode: offline wallet load failed (first boot or no cached data): %v", err)
		}
		return deg
	}

	deg.wallet = wallet
	deg.walletLoaded = true
	balance := wallet.GetBalance()
	log.Printf("Degraded mode: offline wallet loaded successfully, balance=%d sats", balance)

	return deg
}

// WireStartupDegraded performs the full startup wiring for a merchant that
// booted degraded: register the upgrade consumer, then wire the recovery
// trigger so the tracker can actually fire it.
//
// Both steps belong together. #400 added WireRecoveryTrigger to the runtime
// downgrade path, and the startup path (installMerchant in main.go) was missed
// — it registered the onUpgrade consumer but left the tracker's
// onFirstReachable nil, so the service stayed degraded forever even after the
// mint returned (#10).
//
// This exists as one method because the two steps are only correct as a pair,
// and because main.go's startup path and its test must call the same code. The
// previous test re-implemented the sequence inline, so deleting
// WireRecoveryTrigger() from installMerchant left the suite green: the test
// still called the library directly. With the sequence named, main.go calls
// this and the test calls this.
func (m *MerchantDegraded) WireStartupDegraded(onUpgrade func(MerchantInterface)) {
	m.OnUpgrade(onUpgrade)
	m.WireRecoveryTrigger()
	m.WireWalletRetry()
}

func (m *MerchantDegraded) OnUpgrade(callback func(MerchantInterface)) {
	m.onUpgradeMu.Lock()
	m.onUpgrade = callback
	m.onUpgradeMu.Unlock()
}

// getOnUpgrade snapshots the callback under the lock so AttemptUpgrade
// can invoke it outside the critical section.
func (m *MerchantDegraded) getOnUpgrade() func(MerchantInterface) {
	m.onUpgradeMu.RLock()
	defer m.onUpgradeMu.RUnlock()
	return m.onUpgrade
}

// StorageIncompatible reports whether the degraded state is caused by a
// wallet storage filesystem that cannot back bbolt's shared mmap (#583) —
// the one degraded cause a mint recovery can never fix, so boot banners
// and upgrade logs must not promise otherwise.
func (m *MerchantDegraded) StorageIncompatible() bool {
	return tollwallet.IsStorageMmapUnsupported(m.walletInitErr)
}

// walletUnavailableReason says why there is no wallet behind the merchant:
// the storage-mmap class names the filesystem and its remedy; everything
// else keeps the historic "no reachable mints" wording, which stays true
// for its own cause (#583's misleading-errors half).
func (m *MerchantDegraded) walletUnavailableReason() string {
	if m.StorageIncompatible() {
		return "wallet storage does not support shared mmap (jffs2 overlay?) — move wallet.db to an mmap-capable filesystem (ext4/f2fs/ubifs); see README storage requirements"
	}
	return "no reachable mints"
}

func (m *MerchantDegraded) walletNotInitializedError() error {
	return fmt.Errorf("wallet not initialized: %s", m.walletUnavailableReason())
}

// WalletDegradedInfo reports the degraded state and its reason for the
// status surfaces (CLI `status`, board): the degraded merchant is degraded
// by construction, and the reason string distinguishes the permanent
// storage-mmap class from the recoverable no-reachable-mints one (#824).
// Consumed through an interface assertion so MerchantInterface itself is
// untouched.
func (m *MerchantDegraded) WalletDegradedInfo() (bool, string) {
	return true, m.walletUnavailableReason()
}

// WireRecoveryTrigger registers the tracker's first-reachable callback so a
// runtime downgrade (the full -> degraded transition in main) can upgrade
// back once a mint recovers. The startup degraded paths register the same
// sequence inline; without this, a runtime downgrade wires the onUpgrade
// consumer but nothing ever fires it (#400) and the service stays degraded
// until manually restarted. Also arms the wallet retry loop: the trigger is
// a one-shot latch, and a persistent wallet failure after the first
// attempt would otherwise strand the downgrade forever.
func (m *MerchantDegraded) WireRecoveryTrigger() {
	m.mintHealthTracker.SetOnFirstReachableForDegraded(func() {
		m.AttemptUpgrade()
	})
	m.WireWalletRetry()
}

// AttemptUpgrade shuts down the degraded wallet, rebuilds a full merchant on
// the tracker's current reachable set, and fires onUpgrade with it.
// Single-flight: the first-reachable trigger and the wallet retry loop can
// both schedule it around the same mint recovery. On failure the degrade
// reason is updated to the attempt's error — the operator-facing surface
// must never claim a cause older than the last attempt.
func (m *MerchantDegraded) AttemptUpgrade() {
	m.upgradeMu.Lock()
	defer m.upgradeMu.Unlock()
	if m.upgraded {
		return
	}
	log.Printf("Attempting upgrade from degraded mode (degraded because: %s)", m.DegradeReason())
	if err := m.Shutdown(); err != nil {
		log.Printf("ERROR: Failed to shutdown degraded wallet before upgrade: %v", err)
	}
	fullMerchant, err := newFullMerchant(m.configManager, m.mintHealthTracker)
	if err != nil {
		m.SetDegradeReason(fmt.Sprintf("wallet initialization failed: %v", err))
		log.Printf("ERROR: Failed to upgrade from degraded mode: %v", err)
		return
	}
	m.upgraded = true
	m.stopWalletRetry()
	if cb := m.getOnUpgrade(); cb != nil {
		cb(fullMerchant)
	} else if s, ok := fullMerchant.(interface{ Shutdown() error }); ok {
		// An upgraded merchant with nowhere to go must not leak the
		// background work its construction started: nobody will ever
		// Shut it down. Production always wires onUpgrade; this is the
		// defensive seam the upgrade-strand tests exercise.
		_ = s.Shutdown()
	}
}

func NewMerchantDegradedFromFull(configManager *config_manager.ConfigManager, tracker *MintHealthTracker) *MerchantDegraded {
	walletDirPath := walletDirFor(configManager)
	return NewMerchantDegradedWithWallet(configManager, tracker, DefaultWalletFactory, walletDirPath)
}

func (m *MerchantDegraded) Shutdown() error {
	m.stopWalletRetry()
	if m.wallet != nil {
		err := m.wallet.Shutdown()
		m.wallet = nil
		m.walletLoaded = false
		return err
	}
	m.walletLoaded = false
	return nil
}

func (m *MerchantDegraded) SetOnReachableSetChanged(callback func()) {
	m.mintHealthTracker.SetOnReachableSetChanged(callback)
}

func (m *MerchantDegraded) GetMintHealthTracker() *MintHealthTracker {
	return m.mintHealthTracker
}

func (m *MerchantDegraded) CreatePaymentToken(mintURL string, amount uint64) (string, error) {
	if !m.walletLoaded {
		return "", m.notInitializedError()
	}
	return "", fmt.Errorf("CreatePaymentToken not supported in degraded mode; use CreatePaymentTokenWithOverpayment")
}

func (m *MerchantDegraded) CreatePaymentTokenWithOverpayment(mintURL string, amount uint64, maxOverpaymentPercent uint64, maxOverpaymentAbsolute uint64) (string, error) {
	if !m.walletLoaded {
		return "", m.notInitializedError()
	}
	return m.wallet.SendWithOverpayment(amount, mintURL, maxOverpaymentPercent, maxOverpaymentAbsolute)
}

func (m *MerchantDegraded) DrainMint(mintURL string) (string, uint64, error) {
	return "", 0, m.notInitializedError()
}

func (m *MerchantDegraded) RequestLightningInvoice(macAddress, mintURL string, amount uint64) (*LightningInvoice, error) {
	return nil, m.notInitializedError()
}

func (m *MerchantDegraded) GetLightningInvoiceStatus(quoteID, macAddress string) (*LightningQuoteStatus, error) {
	return nil, m.notInitializedError()
}

func (m *MerchantDegraded) GetAcceptedMints() []config_manager.MintConfig {
	return m.mintHealthTracker.GetAllConfiguredMintConfigs()
}

func (m *MerchantDegraded) GetBalance() uint64 {
	if !m.walletLoaded {
		return 0
	}
	return m.wallet.GetBalance()
}

func (m *MerchantDegraded) GetBalanceByMint(mintURL string) uint64 {
	if !m.walletLoaded {
		return 0
	}
	return m.wallet.GetBalanceByMint(mintURL)
}

func (m *MerchantDegraded) GetAllMintBalances() map[string]uint64 {
	if !m.walletLoaded {
		return make(map[string]uint64)
	}
	return m.wallet.GetAllMintBalances()
}

func (m *MerchantDegraded) PurchaseSession(cashuToken string, macAddress string) (*nostr.Event, error) {
	noticeEvent, err := m.CreateNoticeEvent("error", "service-unavailable",
		"TollGate is initializing. No reachable mints. Please try again in a few minutes.", "")
	if err != nil {
		return nil, fmt.Errorf("wallet not initialized and failed to create notice: %w", err)
	}
	return noticeEvent, nil
}

func (m *MerchantDegraded) GetAdvertisement() string {
	if m.StorageIncompatible() {
		// The operator-facing advertisement is the portal's status surface:
		// a storage-class degraded state must not present itself as a
		// transient "initializing" — no mint recovery can ever clear it
		// (#583, #824: one probe, three consumers — log, notice, status).
		noticeEvent, err := m.CreateNoticeEvent("error", "wallet-storage-unsupported",
			"TollGate wallet storage does not support shared mmap (jffs2 overlay?). The wallet cannot initialize on this filesystem. Move wallet.db to an mmap-capable filesystem (ext4/f2fs/ubifs) — see README storage requirements.", "")
		if err != nil {
			return fmt.Sprintf(`{"error": "wallet storage unsupported: %v"}`, err)
		}
		bytes, err := json.Marshal(noticeEvent)
		if err != nil {
			return `{"error": "failed to marshal notice"}`
		}
		return string(bytes)
	}
	noticeEvent, err := m.CreateNoticeEvent("warning", "no-reachable-mints",
		"TollGate is initializing. No reachable mints detected. Service will auto-recover.", "")
	if err != nil {
		return fmt.Sprintf(`{"error": "no reachable mints: %v"}`, err)
	}
	bytes, err := json.Marshal(noticeEvent)
	if err != nil {
		return `{"error": "failed to marshal notice"}`
	}
	return string(bytes)
}

func (m *MerchantDegraded) StartPayoutRoutine() {
	log.Printf("WARNING: Payout routine not started — no reachable mints (degraded mode)")
}

func (m *MerchantDegraded) StartDataUsageMonitoring() {
	log.Printf("WARNING: Data usage monitoring not started — no reachable mints (degraded mode)")
}

func (m *MerchantDegraded) CreateNoticeEvent(level, code, message, customerPubkey string) (*nostr.Event, error) {
	return createNoticeEvent(m.configManager, level, code, message, customerPubkey)
}

func (m *MerchantDegraded) GetSession(macAddress string) (*CustomerSession, error) {
	return nil, m.notInitializedError()
}

// GetSessionState answers "none" in degraded mode: without a wallet no session
// can exist, which is the same answer GetUsage gives ("-1/-1").
func (m *MerchantDegraded) GetSessionState(macAddress string) (SessionState, error) {
	return SessionStateNone, nil
}

func (m *MerchantDegraded) AddAllotment(macAddress, metric string, amount uint64) (*CustomerSession, error) {
	return nil, m.notInitializedError()
}

func (m *MerchantDegraded) GetUsage(macAddress string) (string, error) {
	return "-1/-1", nil
}

// IssueSessionTicket is unavailable in degraded mode for the same reason
// AddAllotment is: there is no wallet, so there is no session to name and
// nothing a ticket could hand over.
func (m *MerchantDegraded) IssueSessionTicket(macAddress string) (string, int64, error) {
	return "", 0, m.notInitializedError()
}

func (m *MerchantDegraded) RebindSession(ticket, macAddress string) (*CustomerSession, error) {
	return nil, m.notInitializedError()
}

func (m *MerchantDegraded) Fund(cashuToken string) (uint64, error) {
	return 0, m.notInitializedError()
}

func (m *MerchantDegraded) WalletLoaded() bool {
	return m.walletLoaded
}

func DefaultWalletFactory(walletPath string, mintURLs []string) (Wallet, error) {
	if err := os.MkdirAll(walletPath, 0700); err != nil {
		return nil, fmt.Errorf("failed to create wallet directory %s: %w", walletPath, err)
	}
	tw, err := tollwallet.New(walletPath, mintURLs, false)
	if err != nil {
		return nil, err
	}
	return tw, nil
}
