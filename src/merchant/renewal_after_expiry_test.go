package merchant

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/tollwallet"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/valve"
	"github.com/nbd-wtf/go-nostr"
)

// This file pins the renewal-after-expiry contract on the merchant side:
//
//	a client whose purchased session ran out and was deauthorised must be able
//	to buy again — the purchase starts a FRESH session for that MAC (it neither
//	fails nor resurrects the spent record) and the client is re-authorised
//	through the valve seam.
//
// The harness drives the real PurchaseSession/grantSessionAccess/valve path with
// a stub wallet and a fake ndsctl on PATH, so the assertions are about the
// module's own behaviour, not about a router. Hardware confirmation on the
// physical router (expire a session, renew, check `ndsctl clients` shows
// Authenticated) remains an operator step.

const (
	renewalMintURL = "https://renewal-mint.example.com"
	renewalMAC     = "aa:bb:cc:dd:ee:ff"
	renewalStepMS  = 60000 // ms per step for the milliseconds lab
	renewalSats    = 10    // sats received per purchase
)

// renewalFreshAllotment is the allotment one purchase must produce:
// renewalSats sats / price-per-step 1 × renewalStepMS ms.
const renewalFreshAllotment = renewalSats * renewalStepMS

type renewalToken struct{}

func (renewalToken) Mint() string               { return renewalMintURL }
func (renewalToken) Amount() uint64             { return renewalSats }
func (renewalToken) Serialize() (string, error) { return "cashuBrenewal", nil }
func (renewalToken) Close()                     {}

// renewalWallet stubs the wallet seam: DecodeToken and Receive always succeed
// and Receive counts calls, so a test can tell "the token was processed" from
// "the purchase was refused before Receive".
type renewalWallet struct {
	tollwallet.WalletPort
	receives int32
}

func (w *renewalWallet) DecodeToken(string) (tollwallet.Token, error) { return renewalToken{}, nil }

func (w *renewalWallet) Receive(tollwallet.Token) (uint64, error) {
	atomic.AddInt32(&w.receives, 1)
	return renewalSats, nil
}

func (w *renewalWallet) SwapFeeSats(tollwallet.Token) (uint64, error) { return 0, nil }

func (w *renewalWallet) received() int { return int(atomic.LoadInt32(&w.receives)) }

// renewalNdsctl is a fake `ndsctl` on PATH. It logs every auth/deauth (the valve
// seam's only observable effect) and answers `ndsctl json <mac>` either with a
// client entry or with "{}" — the answer that means NDS does not list the client
// at all (post-deauth, or after NDS dropped its record for an idle device).
//
// It can also be told to FAIL deauth (failDeauth), which is how the gate
// close-failure contract is exercised: a deauth that fails is not a close.
type renewalNdsctl struct {
	logPath   string
	statePath string
	usagePath string
	failPath  string
}

func installRenewalNdsctl(t *testing.T) *renewalNdsctl {
	t.Helper()

	dir := t.TempDir()
	n := &renewalNdsctl{
		logPath:   filepath.Join(dir, "ndsctl.log"),
		statePath: filepath.Join(dir, "ndsctl.state"),
		usagePath: filepath.Join(dir, "ndsctl.usage"),
		failPath:  filepath.Join(dir, "ndsctl.deauthfail"),
	}

	script := fmt.Sprintf(`#!/bin/sh
LOG=%q
STATE=%q
USAGE=%q
FAIL=%q
mac="$2"
case "$1" in
  auth)
    echo "AUTH $mac" >> "$LOG"
    echo "Auth: $mac - Granted"
    exit 0
    ;;
  deauth)
    echo "DEAUTH $mac" >> "$LOG"
    if [ -r "$FAIL" ]; then
      echo "Failed to deauthenticate client"
      exit 1
    fi
    echo "Auth: $mac - Removed"
    exit 0
    ;;
  json)
    if [ -r "$STATE" ] && [ "$(cat "$STATE")" = "registered" ]; then
      down=1024
      up=512
      if [ -r "$USAGE" ] && [ "$(wc -w < "$USAGE")" -eq 2 ]; then
        down=$(awk '{print $1}' "$USAGE")
        up=$(awk '{print $2}' "$USAGE")
      fi
      printf '{"id":1,"ip":"192.0.2.50","mac":"%%s","added":1,"active":1,"duration":60,"token":"t","state":"Authenticated","downloaded":%%s,"avg_down_speed":0,"uploaded":%%s,"avg_up_speed":0}\n' "$mac" "$down" "$up"
    else
      echo '{}'
    fi
    exit 0
    ;;
esac
echo OK
exit 0
`, n.logPath, n.statePath, n.usagePath, n.failPath)

	if err := os.WriteFile(filepath.Join(dir, "ndsctl"), []byte(script), 0o755); err != nil {
		t.Fatalf("write fake ndsctl: %v", err)
	}

	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	n.setRegistered(t, true)
	return n
}

// setRegistered models whether NDS currently lists the client. false makes
// `ndsctl json <mac>` answer "{}".
func (n *renewalNdsctl) setRegistered(t *testing.T, registered bool) {
	t.Helper()

	state := "unlisted"
	if registered {
		state = "registered"
	}
	if err := os.WriteFile(n.statePath, []byte(state), 0o644); err != nil {
		t.Fatalf("write ndsctl state: %v", err)
	}
}

// setClientKB drives the byte counter `ndsctl json` reports (kB, as on the
// router — the valve multiplies by 1024).
func (n *renewalNdsctl) setClientKB(t *testing.T, downloadedKB, uploadedKB uint64) {
	t.Helper()

	if err := os.WriteFile(n.usagePath, []byte(fmt.Sprintf("%d %d", downloadedKB, uploadedKB)), 0o644); err != nil {
		t.Fatalf("write ndsctl usage: %v", err)
	}
}

// failDeauth makes `ndsctl deauth` fail (exit 1, "Failed to deauthenticate
// client") until it is called again with false. A failed deauth models
// NoDogSplash hanging or refusing the operation, which is the condition the
// gate-close contract has to survive: the client is still authenticated and the
// gate is still open.
func (n *renewalNdsctl) failDeauth(t *testing.T, fail bool) {
	t.Helper()

	if !fail {
		if err := os.Remove(n.failPath); err != nil && !os.IsNotExist(err) {
			t.Fatalf("clear deauth failure: %v", err)
		}
		return
	}
	if err := os.WriteFile(n.failPath, []byte("fail\n"), 0o644); err != nil {
		t.Fatalf("write deauth failure marker: %v", err)
	}
}

// count returns how many log lines start with prefix ("AUTH ", "DEAUTH ").
func (n *renewalNdsctl) count(t *testing.T, prefix string) int {
	t.Helper()

	data, err := os.ReadFile(n.logPath)
	if err != nil {
		if os.IsNotExist(err) {
			return 0
		}
		t.Fatalf("read ndsctl log: %v", err)
	}

	total := 0
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if strings.HasPrefix(line, prefix) {
			total++
		}
	}
	return total
}

// newRenewalMerchant builds a Merchant over a stub wallet and a stub ndsctl,
// with the metric under test.
func newRenewalMerchant(t *testing.T, metric string) (*Merchant, *renewalWallet) {
	t.Helper()

	cm, _ := setupTestConfigManager(t)
	wallet := &renewalWallet{}

	m := &Merchant{
		config: &config_manager.Config{
			Metric:   metric,
			StepSize: renewalStepMS,
			AcceptedMints: []config_manager.MintConfig{
				{URL: renewalMintURL, PricePerStep: 1, PriceUnit: "sat"},
			},
		},
		configManager:     cm,
		tollwallet:        wallet,
		mintHealthTracker: newTestTracker(cm.GetConfig(), nil),
		customerSessions:  make(map[string]*CustomerSession),
		expiredSessions:   make(map[string]int64),
	}

	// The valve keeps its gate/timer/baseline state in package globals shared by
	// the whole test binary; start from a closed gate for the MAC under test.
	_ = valve.CloseGate(renewalMAC)

	return m, wallet
}

// expireSessionAged models the passage of time for a milliseconds session: the
// record is still in memory but its allotment is spent. It returns the aged
// start time so a test can assert the renewal did not reuse it.
func expireSessionAged(t *testing.T, m *Merchant, macAddress string, age time.Duration) int64 {
	t.Helper()

	m.sessionMu.Lock()
	defer m.sessionMu.Unlock()

	session, exists := m.customerSessions[macAddress]
	if !exists {
		t.Fatalf("no session for %s to age", macAddress)
	}
	session.StartTime = time.Now().Add(-age).Unix()
	return session.StartTime
}

func noticeErrorCode(t *testing.T, event *nostr.Event) string {
	t.Helper()

	if event == nil {
		t.Fatal("expected an event, got nil")
	}
	for _, tag := range event.Tags {
		if len(tag) >= 2 && tag[0] == "code" {
			return tag[1]
		}
	}
	return ""
}

// TestRepurchaseAfterExpiryCreatesFreshAllotmentAndReauthorisesClient is the
// operator-reported flow, end to end on the merchant side: buy, the session runs
// out (valve deauths, the portal polls /usage and sees no session), buy again.
//
// Before the fix the pre-flight refused the renewal with
// `{"level":"error","code":"client-not-registered"}` — "No captive-portal
// session found for this device. Reconnect to the TollGate Wi-Fi and try
// again." — which is exactly the "disconnect and reconnect" the operator saw:
// NDS no longer lists a client it deauthorised, so every renewal was refused
// before Receive and no auth was ever attempted.
func TestRepurchaseAfterExpiryCreatesFreshAllotmentAndReauthorisesClient(t *testing.T) {
	ndsctl := installRenewalNdsctl(t)
	m, wallet := newRenewalMerchant(t, "milliseconds")

	// First purchase: 10 sats → 10 × 60000 ms, gate authorised through ndsctl.
	firstEvent, err := m.PurchaseSession("cashuBfirst", renewalMAC)
	if err != nil {
		t.Fatalf("first purchase returned error: %v", err)
	}
	if firstEvent.Kind != 1022 {
		t.Fatalf("first purchase returned kind %d, want 1022 (session event); code=%q", firstEvent.Kind, noticeErrorCode(t, firstEvent))
	}

	firstSession, err := m.GetSession(renewalMAC)
	if err != nil {
		t.Fatalf("GetSession after first purchase: %v", err)
	}
	if firstSession.Allotment != renewalFreshAllotment {
		t.Fatalf("first session allotment = %d, want %d", firstSession.Allotment, renewalFreshAllotment)
	}
	authsAfterFirstPurchase := ndsctl.count(t, "AUTH ")
	if authsAfterFirstPurchase != 1 {
		t.Fatalf("ndsctl auth calls after first purchase = %d, want 1", authsAfterFirstPurchase)
	}

	// The session runs out: time passes, the valve deauthorises the client at
	// the timer, and the portal polls /usage.
	expiredStart := expireSessionAged(t, m, renewalMAC, time.Hour)
	if err := valve.CloseGate(renewalMAC); err != nil {
		t.Fatalf("valve.CloseGate at expiry: %v", err)
	}
	usage, err := m.GetUsage(renewalMAC)
	if err != nil {
		t.Fatalf("GetUsage after expiry returned error: %v", err)
	}
	if usage != "-1/-1" {
		t.Fatalf("GetUsage after expiry = %q, want %q (the deployed portal's contract)", usage, "-1/-1")
	}
	deauthsAfterExpiry := ndsctl.count(t, "DEAUTH ")
	if deauthsAfterExpiry == 0 {
		t.Fatal("expected the expired client to have been deauthorised through the valve seam")
	}

	// NDS no longer lists the deauthorised client: `ndsctl json <mac>` → "{}".
	ndsctl.setRegistered(t, false)

	// The renewal.
	renewalEvent, err := m.PurchaseSession("cashuBrenewal", renewalMAC)
	if err != nil {
		t.Fatalf("re-purchase after expiry returned error: %v", err)
	}
	if renewalEvent.Kind != 1022 {
		t.Fatalf("re-purchase after expiry returned kind %d (code=%q), want 1022 (session event)",
			renewalEvent.Kind, noticeErrorCode(t, renewalEvent))
	}
	if got := wallet.received(); got != 2 {
		t.Fatalf("wallet Receive called %d times, want 2 (the renewal must reach the money path)", got)
	}

	renewed, err := m.GetSession(renewalMAC)
	if err != nil {
		t.Fatalf("GetSession after renewal: %v", err)
	}
	if renewed.Allotment != renewalFreshAllotment {
		t.Fatalf("renewed session allotment = %d, want a fresh %d", renewed.Allotment, renewalFreshAllotment)
	}
	if renewed.StartTime <= expiredStart {
		t.Fatalf("renewed session start time = %d, want newer than the expired session's %d",
			renewed.StartTime, expiredStart)
	}

	authsAfterRenewal := ndsctl.count(t, "AUTH ")
	if authsAfterRenewal <= authsAfterFirstPurchase {
		t.Fatalf("ndsctl auth calls after renewal = %d, want more than %d — the renewed client was never re-authorised",
			authsAfterRenewal, authsAfterFirstPurchase)
	}

	state, err := m.GetSessionState(renewalMAC)
	if err != nil {
		t.Fatalf("GetSessionState after renewal: %v", err)
	}
	if state != SessionStateActive {
		t.Fatalf("session state after renewal = %q, want %q", state, SessionStateActive)
	}
}

// TestRepurchaseAfterExpiryDoesNotResurrectStaleAllotment covers the case where
// nothing polled /usage after the session ran out, so the spent record is still
// in memory when the renewal arrives. Today that record is extended: two
// 600000 ms purchases leave a 1200000 ms session, i.e. the already-consumed time
// is handed back for free and the portal keeps reporting the spent session
// instead of a new one.
func TestRepurchaseAfterExpiryDoesNotResurrectStaleAllotment(t *testing.T) {
	ndsctl := installRenewalNdsctl(t)
	m, _ := newRenewalMerchant(t, "milliseconds")

	if _, err := m.PurchaseSession("cashuBfirst", renewalMAC); err != nil {
		t.Fatalf("first purchase returned error: %v", err)
	}
	expireSessionAged(t, m, renewalMAC, time.Hour)

	// No /usage or /session-state poll in between: the stale record is still here.
	ndsctl.setRegistered(t, true)

	renewalEvent, err := m.PurchaseSession("cashuBrenewal", renewalMAC)
	if err != nil {
		t.Fatalf("re-purchase after expiry returned error: %v", err)
	}
	if renewalEvent.Kind != 1022 {
		t.Fatalf("re-purchase after expiry returned kind %d (code=%q), want 1022 (session event)",
			renewalEvent.Kind, noticeErrorCode(t, renewalEvent))
	}

	renewed, err := m.GetSession(renewalMAC)
	if err != nil {
		t.Fatalf("GetSession after renewal: %v", err)
	}
	if renewed.Allotment != renewalFreshAllotment {
		t.Fatalf("renewed session allotment = %d, want a fresh %d (the spent allotment must not be resurrected)",
			renewed.Allotment, renewalFreshAllotment)
	}
	if renewed.StartTime > time.Now().Unix() {
		t.Fatalf("renewed session start time = %d is in the future", renewed.StartTime)
	}
}

// TestFirstTimePurchaseStillRefusedWhenNdsDoesNotKnowTheClient is the control
// for #403 L1: a MAC with no session history is a first-time visitor, and a
// payment it could never be authorised for is still refused before Receive.
// Without this the renewal fix would silently delete the fund-safety pre-flight.
func TestFirstTimePurchaseStillRefusedWhenNdsDoesNotKnowTheClient(t *testing.T) {
	ndsctl := installRenewalNdsctl(t)
	m, wallet := newRenewalMerchant(t, "milliseconds")

	ndsctl.setRegistered(t, false)

	event, err := m.PurchaseSession("cashuBfirst", renewalMAC)
	if err != nil {
		t.Fatalf("PurchaseSession returned error: %v", err)
	}
	if event.Kind != 21023 {
		t.Fatalf("first-time purchase with an unknown NDS client returned kind %d, want 21023 (notice)", event.Kind)
	}
	if code := noticeErrorCode(t, event); code != "client-not-registered" {
		t.Fatalf("notice code = %q, want %q", code, "client-not-registered")
	}
	if got := wallet.received(); got != 0 {
		t.Fatalf("wallet Receive called %d times, want 0 (refused before the money path)", got)
	}
	if state, err := m.GetSessionState(renewalMAC); err != nil || state != SessionStateNone {
		t.Fatalf("GetSessionState = %q, %v; want %q, nil", state, err, SessionStateNone)
	}
	if got := ndsctl.count(t, "AUTH "); got != 0 {
		t.Fatalf("ndsctl auth calls = %d, want 0 — a refused purchase must not authorise anything", got)
	}
}
