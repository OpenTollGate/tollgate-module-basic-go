package merchant

import (
	"github.com/nbd-wtf/go-nostr"
	"github.com/stretchr/testify/mock"
)

// MockMerchant is a mock implementation of merchant.MerchantService
type MockMerchant struct {
	mock.Mock
}

// Ensure MockMerchant implements merchant.MerchantService
var _ MerchantService = (*MockMerchant)(nil)

func (m *MockMerchant) PurchaseSession(event nostr.Event) (*nostr.Event, error) {
	args := m.Called(event)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*nostr.Event), args.Error(1)
}

func (m *MockMerchant) CreateNoticeEvent(level, code, message, customerPubkey string) (*nostr.Event, error) {
	args := m.Called(level, code, message, customerPubkey)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*nostr.Event), args.Error(1)
}

func (m *MockMerchant) GetAdvertisement() string {
	args := m.Called()
	return args.String(0)
}

func (m *MockMerchant) StartPayoutRoutine() {
	m.Called()
}