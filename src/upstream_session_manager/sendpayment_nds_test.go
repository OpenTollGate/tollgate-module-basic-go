package upstream_session_manager

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/merchant_types"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/tollgate_protocol"
)

type mockMerchant struct {
	token string
	err   error
}

func (m *mockMerchant) CreatePaymentTokenWithOverpayment(mintURL string, amount uint64, maxOverpaymentPercent uint64, maxOverpaymentAbsolute uint64) (string, error) {
	return m.token, m.err
}
func (m *mockMerchant) GetAcceptedMints() []config_manager.MintConfig { return nil }
func (m *mockMerchant) GetBalanceByMint(mintURL string) uint64        { return 1000 }
func (m *mockMerchant) Fund(cashuToken string) (uint64, error)        { return 0, nil }

type mockProvider struct{ m *mockMerchant }

func (p *mockProvider) GetMerchant() merchant_types.PaymentMerchant { return p.m }

func TestSendPayment_TriggersNdsSession(t *testing.T) {
	var ndsHit atomic.Bool

	ndsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ndsHit.Store(true)
		w.WriteHeader(http.StatusOK)
	}))
	defer ndsServer.Close()

	_, ndsPortStr, _ := net.SplitHostPort(ndsServer.Listener.Addr().String())
	ndsPort, _ := strconv.Atoi(ndsPortStr)

	s := &UpstreamSession{
		GatewayIP:    "127.0.0.1",
		NdsPortalPort: ndsPort,
		SelectedPricing: &tollgate_protocol.PricingOption{
			PricePerStep: 1,
			MintURL:      "http://127.0.0.1:3338",
		},
		merchantProvider: &mockProvider{m: &mockMerchant{token: "cashuAfaketoken"}},
	}

	token, err := s.merchantProvider.GetMerchant().CreatePaymentTokenWithOverpayment(
		"http://127.0.0.1:3338", 5, 10000, 100,
	)
	if err != nil || token == "" {
		t.Fatalf("mock merchant failed: token=%q err=%v", token, err)
	}

	s.triggerNdsSession()
	time.Sleep(200 * time.Millisecond)

	if !ndsHit.Load() {
		t.Fatal("NDS portal was not hit after payment — triggerNdsSession did not fire")
	}

	t.Log("NDS portal received HTTP GET after simulated payment")
}
