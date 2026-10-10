package merchant

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"

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

func (m *MerchantDegraded) OnUpgrade(callback func(MerchantInterface)) {
	m.onUpgrade = callback
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
// until manually restarted.
func (m *MerchantDegraded) WireRecoveryTrigger() {
	m.mintHealthTracker.SetOnFirstReachableForDegraded(func() {
		m.AttemptUpgrade()
	})
}

// AttemptUpgrade shuts down the degraded wallet, rebuilds a full merchant on
// the tracker's current reachable set, and fires onUpgrade with it.
func (m *MerchantDegraded) AttemptUpgrade() {
	log.Printf("Mint became reachable — attempting upgrade from degraded mode")
	if err := m.Shutdown(); err != nil {
		log.Printf("ERROR: Failed to shutdown degraded wallet before upgrade: %v", err)
	}
	fullMerchant, err := newFullMerchant(m.configManager, m.mintHealthTracker)
	if err != nil {
		log.Printf("ERROR: Failed to upgrade from degraded mode: %v", err)
		return
	}
	if m.onUpgrade != nil {
		m.onUpgrade(fullMerchant)
	}
}

func NewMerchantDegradedFromFull(configManager *config_manager.ConfigManager, tracker *MintHealthTracker) *MerchantDegraded {
	walletDirPath := filepath.Dir(configManager.ConfigFilePath)
	return NewMerchantDegradedWithWallet(configManager, tracker, DefaultWalletFactory, walletDirPath)
}

func (m *MerchantDegraded) Shutdown() error {
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
		return "", m.walletNotInitializedError()
	}
	return "", fmt.Errorf("CreatePaymentToken not supported in degraded mode; use CreatePaymentTokenWithOverpayment")
}

func (m *MerchantDegraded) CreatePaymentTokenWithOverpayment(mintURL string, amount uint64, maxOverpaymentPercent uint64, maxOverpaymentAbsolute uint64) (string, error) {
	if !m.walletLoaded {
		return "", m.walletNotInitializedError()
	}
	return m.wallet.SendWithOverpayment(amount, mintURL, maxOverpaymentPercent, maxOverpaymentAbsolute)
}

func (m *MerchantDegraded) DrainMint(mintURL string) (string, uint64, error) {
	return "", 0, m.walletNotInitializedError()
}

func (m *MerchantDegraded) RequestLightningInvoice(macAddress, mintURL string, amount uint64) (*LightningInvoice, error) {
	return nil, m.walletNotInitializedError()
}

func (m *MerchantDegraded) GetLightningInvoiceStatus(quoteID, macAddress string) (*LightningQuoteStatus, error) {
	return nil, m.walletNotInitializedError()
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
		"TollGate is initializing. No reachable mints. Please try again in a few minutes.", macAddress)
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
	return nil, m.walletNotInitializedError()
}

// GetSessionState answers "none" in degraded mode: without a wallet no session
// can exist, which is the same answer GetUsage gives ("-1/-1").
func (m *MerchantDegraded) GetSessionState(macAddress string) (SessionState, error) {
	return SessionStateNone, nil
}

func (m *MerchantDegraded) AddAllotment(macAddress, metric string, amount uint64) (*CustomerSession, error) {
	return nil, m.walletNotInitializedError()
}

func (m *MerchantDegraded) GetUsage(macAddress string) (string, error) {
	return "-1/-1", nil
}

// IssueSessionTicket is unavailable in degraded mode for the same reason
// AddAllotment is: there is no wallet, so there is no session to name and
// nothing a ticket could hand over.
func (m *MerchantDegraded) IssueSessionTicket(macAddress string) (string, int64, error) {
	return "", 0, m.walletNotInitializedError()
}

func (m *MerchantDegraded) RebindSession(ticket, macAddress string) (*CustomerSession, error) {
	return nil, m.walletNotInitializedError()
}

func (m *MerchantDegraded) Fund(cashuToken string) (uint64, error) {
	return 0, m.walletNotInitializedError()
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
