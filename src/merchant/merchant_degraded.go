package merchant

import (
	"fmt"
	"log"
	"sync"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"github.com/nbd-wtf/go-nostr"
)

var errDegraded = fmt.Errorf("service degraded: wallet unavailable, mints unreachable")

type MerchantDegraded struct {
	configManager *config_manager.ConfigManager
	advertisement string
	sessions      map[string]*CustomerSession
	sessionMu     sync.RWMutex
}

func NewDegraded(configManager *config_manager.ConfigManager) (MerchantInterface, error) {
	advertisementStr, err := CreateAdvertisement(configManager)
	if err != nil {
		return nil, fmt.Errorf("failed to create advertisement in degraded mode: %w", err)
	}
	log.Printf("=== Merchant starting in DEGRADED mode (mints unreachable) ===")
	return &MerchantDegraded{
		configManager: configManager,
		advertisement: advertisementStr,
		sessions:      make(map[string]*CustomerSession),
	}, nil
}

func (m *MerchantDegraded) CreatePaymentToken(string, uint64) (string, error)                          { return "", errDegraded }
func (m *MerchantDegraded) CreatePaymentTokenWithOverpayment(string, uint64, uint64, uint64) (string, error) { return "", errDegraded }
func (m *MerchantDegraded) DrainMint(string) (string, uint64, error)                                   { return "", 0, errDegraded }
func (m *MerchantDegraded) RequestLightningInvoice(string, string, uint64) (*LightningInvoice, error)  { return nil, errDegraded }
func (m *MerchantDegraded) GetLightningInvoiceStatus(string, string) (*LightningQuoteStatus, error)    { return nil, errDegraded }
func (m *MerchantDegraded) GetAcceptedMints() []config_manager.MintConfig                             { return nil }
func (m *MerchantDegraded) GetBalance() uint64                                                         { return 0 }
func (m *MerchantDegraded) GetBalanceByMint(string) uint64                                             { return 0 }
func (m *MerchantDegraded) GetAllMintBalances() map[string]uint64                                      { return nil }
func (m *MerchantDegraded) PurchaseSession(string, string) (*nostr.Event, error)                       { return nil, errDegraded }
func (m *MerchantDegraded) GetAdvertisement() string                                                   { return m.advertisement }
func (m *MerchantDegraded) StartPayoutRoutine()                                                        {}
func (m *MerchantDegraded) StartDataUsageMonitoring()                                                  {}
func (m *MerchantDegraded) CreateNoticeEvent(level, code, message, customerPubkey string) (*nostr.Event, error) {
	identities := m.configManager.GetIdentities()
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
	if err := noticeEvent.Sign(merchantIdentity.PrivateKey); err != nil {
		return nil, fmt.Errorf("failed to sign notice event: %w", err)
	}
	return noticeEvent, nil
}
func (m *MerchantDegraded) GetSession(macAddress string) (*CustomerSession, error) { return nil, fmt.Errorf("no active sessions in degraded mode") }
func (m *MerchantDegraded) AddAllotment(string, string, uint64) (*CustomerSession, error) {
	return nil, errDegraded
}
func (m *MerchantDegraded) GetUsage(string) (string, error) { return "", errDegraded }
func (m *MerchantDegraded) Fund(string) (uint64, error)     { return 0, errDegraded }
