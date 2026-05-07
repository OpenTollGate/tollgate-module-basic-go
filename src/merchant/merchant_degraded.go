package merchant

import (
	"encoding/json"
	"fmt"
	"log"
	"os"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/tollwallet"
	"github.com/nbd-wtf/go-nostr"
)

type Wallet interface {
	GetBalance() uint64
	GetBalanceByMint(mintUrl string) uint64
	GetAllMintBalances() map[string]uint64
	SendWithOverpayment(amount uint64, mintUrl string, maxOverpaymentPercent uint64, maxOverpaymentAbsolute uint64) (string, error)
}

type WalletFactory func(walletPath string, mintURLs []string) (Wallet, error)

type MerchantDegraded struct {
	configManager     *config_manager.ConfigManager
	mintHealthTracker *MintHealthTracker
	onUpgrade         func(MerchantInterface)
	wallet            Wallet
	walletLoaded      bool
	walletPath        string
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
		log.Printf("Degraded mode: offline wallet load failed (first boot or no cached data): %v", err)
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

func (m *MerchantDegraded) CreatePaymentToken(mintURL string, amount uint64) (string, error) {
	if !m.walletLoaded {
		return "", fmt.Errorf("wallet not initialized: no reachable mints")
	}
	return "", fmt.Errorf("CreatePaymentToken not supported in degraded mode; use CreatePaymentTokenWithOverpayment")
}

func (m *MerchantDegraded) CreatePaymentTokenWithOverpayment(mintURL string, amount uint64, maxOverpaymentPercent uint64, maxOverpaymentAbsolute uint64) (string, error) {
	if !m.walletLoaded {
		return "", fmt.Errorf("wallet not initialized: no reachable mints")
	}
	return m.wallet.SendWithOverpayment(amount, mintURL, maxOverpaymentPercent, maxOverpaymentAbsolute)
}

func (m *MerchantDegraded) DrainMint(mintURL string) (string, uint64, error) {
	return "", 0, fmt.Errorf("wallet not initialized: no reachable mints")
}

func (m *MerchantDegraded) RequestLightningInvoice(macAddress, mintURL string, amount uint64) (*LightningInvoice, error) {
	return nil, fmt.Errorf("wallet not initialized: no reachable mints")
}

func (m *MerchantDegraded) GetLightningInvoiceStatus(quoteID, macAddress string) (*LightningQuoteStatus, error) {
	return nil, fmt.Errorf("wallet not initialized: no reachable mints")
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
	return nil, fmt.Errorf("wallet not initialized: no reachable mints")
}

func (m *MerchantDegraded) AddAllotment(macAddress, metric string, amount uint64) (*CustomerSession, error) {
	return nil, fmt.Errorf("wallet not initialized: no reachable mints")
}

func (m *MerchantDegraded) GetUsage(macAddress string) (string, error) {
	return "-1/-1", nil
}

func (m *MerchantDegraded) Fund(cashuToken string) (uint64, error) {
	return 0, fmt.Errorf("wallet not initialized: no reachable mints")
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
