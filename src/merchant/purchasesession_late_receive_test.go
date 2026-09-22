//go:build testenv && !cdk_wallet

package merchant

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/tollwallet"
)

// lateToken reports the test config's first accepted mint so
// calculateAllotment resolves pricing for it.
type lateToken struct {
	mint   string
	amount uint64
}

func (t lateToken) Mint() string               { return t.mint }
func (t lateToken) Amount() uint64             { return t.amount }
func (t lateToken) Serialize() (string, error) { return "cashuAstub", nil }
func (t lateToken) Close()                     {}

// lateReceiveWallet succeeds, but slower than the (test-shrunken) response
// deadline — the #498 scenario: the response deadline fires first and the
// completion arrives afterwards.
type lateReceiveWallet struct {
	tollwallet.WalletPort
	mint   string
	delay  time.Duration
	amount uint64
	called chan struct{}
}

func (w *lateReceiveWallet) DecodeToken(tokenStr string) (tollwallet.Token, error) {
	return lateToken{mint: w.mint, amount: w.amount}, nil
}

func (w *lateReceiveWallet) SwapFeeSats(tollwallet.Token) (uint64, error) { return 0, nil }

func (w *lateReceiveWallet) Receive(tollwallet.Token) (uint64, error) {
	time.Sleep(w.delay)
	close(w.called)
	return w.amount, nil
}

// TestPurchaseSessionLateReceiveGrantsSession pins the #498 contract: when
// Receive outlives the response deadline, the timeout notice goes out, but
// the abandoned operation's late success still grants the session the
// customer paid for. Before the fix the late result was logged and dropped
// — the token was consumed with no session and no refund.
func TestPurchaseSessionLateReceiveGrantsSession(t *testing.T) {
	oldTimeout, oldOpen := receiveTimeout, openGateForSession
	receiveTimeout = 50 * time.Millisecond
	gateCalled := make(chan struct{})
	var gateOnce sync.Once
	openGateForSession = func(string, *CustomerSession) error {
		gateOnce.Do(func() { close(gateCalled) })
		return nil
	}
	t.Cleanup(func() {
		<-gateCalled
		receiveTimeout = oldTimeout
		openGateForSession = oldOpen
	})

	cm, _ := setupTestConfigManager(t)
	mintURL := cm.GetConfig().AcceptedMints[0].URL

	wallet := &lateReceiveWallet{
		mint:   mintURL,
		delay:  250 * time.Millisecond,
		amount: 64,
		called: make(chan struct{}),
	}
	m := &Merchant{
		tollwallet:        wallet,
		config:            cm.GetConfig(),
		configManager:     cm,
		mintHealthTracker: newTestTracker(cm.GetConfig(), nil),
		customerSessions:  make(map[string]*CustomerSession),
	}

	event, err := m.PurchaseSession("cashuAstub", "AA:BB:CC:DD:EE:11")
	if err != nil {
		t.Fatalf("PurchaseSession: %v", err)
	}
	if !strings.Contains(event.Content, "timed out") {
		t.Fatalf("expected the timeout notice while Receive is still in flight, got: %s", event.Content)
	}

	select {
	case <-wallet.called:
	case <-time.After(2 * time.Second):
		t.Fatal("stubbed Receive never completed")
	}

	if !waitFor(t, 2*time.Second, func() bool {
		session, serr := m.GetSession("AA:BB:CC:DD:EE:11")
		return serr == nil && session != nil && session.Allotment > 0
	}) {
		t.Fatal("late Receive success did not grant the paid-for session (#498 regression)")
	}
	<-gateCalled
}

// TestPurchaseSessionLateReceiveFailureIsLogged pins the other arm: a late
// failure after the timeout notice must not grant anything.
func TestPurchaseSessionLateReceiveFailureIsLogged(t *testing.T) {
	oldTimeout, oldOpen := receiveTimeout, openGateForSession
	receiveTimeout = 50 * time.Millisecond
	openGateForSession = func(string, *CustomerSession) error { return nil }
	t.Cleanup(func() {
		receiveTimeout = oldTimeout
		openGateForSession = oldOpen
	})

	cm, _ := setupTestConfigManager(t)

	wallet := &failLateReceiveWallet{mint: cm.GetConfig().AcceptedMints[0].URL, delay: 150 * time.Millisecond}
	m := &Merchant{
		tollwallet:        wallet,
		config:            cm.GetConfig(),
		configManager:     cm,
		mintHealthTracker: newTestTracker(cm.GetConfig(), nil),
		customerSessions:  make(map[string]*CustomerSession),
	}

	event, err := m.PurchaseSession("cashuAstub", "AA:BB:CC:DD:EE:22")
	if err != nil {
		t.Fatalf("PurchaseSession: %v", err)
	}
	if !strings.Contains(event.Content, "timed out") {
		t.Fatalf("expected timeout notice, got: %s", event.Content)
	}

	time.Sleep(400 * time.Millisecond)

	if session, serr := m.GetSession("AA:BB:CC:DD:EE:22"); serr == nil && session != nil {
		t.Fatalf("late failure must not grant a session; got allotment %d", session.Allotment)
	}
}

type failLateReceiveWallet struct {
	tollwallet.WalletPort
	mint  string
	delay time.Duration
}

func (w *failLateReceiveWallet) DecodeToken(string) (tollwallet.Token, error) {
	return lateToken{mint: w.mint, amount: 64}, nil
}

func (w *failLateReceiveWallet) SwapFeeSats(tollwallet.Token) (uint64, error) { return 0, nil }

func (w *failLateReceiveWallet) Receive(tollwallet.Token) (uint64, error) {
	time.Sleep(w.delay)
	return 0, errLateReceiveSimulated
}

var errLateReceiveSimulated = &simulatedMintError{}

type simulatedMintError struct{}

func (*simulatedMintError) Error() string { return "simulated mint failure after deadline" }
