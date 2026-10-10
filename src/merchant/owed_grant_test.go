package merchant

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/tollwallet"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/valve"
)

// The owed-entitlement contract of issue #403: once customer value has been
// successfully received, TollGate owes either service or a recoverable claim.
// A gate-open failure after a successful Receive must (1) leave the
// entitlement represented durably, (2) converge to a grant when NDS accepts,
// exactly once, (3) survive a restart in the unresolved state, and (4) never
// require a second payment. The harness reuses the renewal suite's fake
// ndsctl (failAuth toggles the gate-open verdict) and scripted wallet.

const owedGrantMAC = "aa:bb:cc:dd:ee:76"

func newOwedGrantMerchant(t *testing.T, storeDir string) *Merchant {
	t.Helper()

	cm, _ := setupTestConfigManager(t)
	m := &Merchant{
		config: &config_manager.Config{
			Metric:   "milliseconds",
			StepSize: renewalStepMS,
			AcceptedMints: []config_manager.MintConfig{
				{URL: renewalMintURL, PricePerStep: 1, PriceUnit: "sat"},
			},
		},
		configManager:     cm,
		tollwallet:        &renewalWallet{},
		mintHealthTracker: newTestTracker(cm.GetConfig(), nil),
		customerSessions:  make(map[string]*CustomerSession),
		expiredSessions:   make(map[string]int64),
		owedGrants:        make(map[string]*owedGrantRecord),
	}
	if storeDir != "" {
		m.owedGrantStore = newOwedGrantStore(filepath.Join(storeDir, "owed-grants.json"))
	}
	// Compressed monitor retry clock (see newIntentMerchant, #733): the
	// convergence polls below stop being coupled to the production 5 s
	// first-attempt delay, and the exactly-once settle window in
	// TestPaidPurchaseWhoseGateFailsIsOwedThenGrantedExactlyOnce scales with it.
	m.owedGrantRetryBase = 10 * time.Millisecond
	m.owedGrantRetryCap = 40 * time.Millisecond
	retireOwedMonitorsOnCleanup(t, m)

	// The valve keeps its gate/timer/baseline state in package globals shared
	// by the whole test binary; start from a closed gate for the MAC under
	// test (the same discipline as newRenewalMerchant).
	_ = valve.CloseGate(owedGrantMAC)
	return m
}

// owedGrantNoticeToken gives the token a serializable form so the receive
// reference (the claim's key) is stable.
type owedGrantNoticeToken struct{}

func (owedGrantNoticeToken) Mint() string               { return renewalMintURL }
func (owedGrantNoticeToken) Amount() uint64             { return renewalSats }
func (owedGrantNoticeToken) Serialize() (string, error) { return "cashuBowed-grant-fixture-note", nil }
func (owedGrantNoticeToken) Secrets() []string          { return nil }
func (owedGrantNoticeToken) Close()                     {}

type owedGrantWallet struct {
	renewalWallet
}

func (w *owedGrantWallet) DecodeToken(string) (tollwallet.Token, error) {
	return owedGrantNoticeToken{}, nil
}

// readOwedGrantStore decodes the persisted file so tests assert durable
// state, not in-memory state.
func readOwedGrantStore(t *testing.T, dir string) map[string]*persistedOwedGrant {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(dir, "owed-grants.json"))
	if err != nil {
		t.Fatalf("read owed-grants store: %v", err)
	}
	var grants map[string]*persistedOwedGrant
	if err := json.Unmarshal(data, &grants); err != nil {
		t.Fatalf("decode owed-grants store: %v", err)
	}
	return grants
}

func waitForOwed(t *testing.T, timeout time.Duration, cond func() bool, what string) {
	t.Helper()
	if !waitFor(t, timeout, cond) {
		t.Fatalf("timed out waiting for %s", what)
	}
}

// TestPaidPurchaseWhoseGateFailsIsOwedThenGrantedExactlyOnce is the core
// scenario: Receive succeeds, ndsctl auth fails, the entitlement is recorded
// durably before the response, and once NDS accepts, the grant happens
// exactly once with no second payment.
func TestPaidPurchaseWhoseGateFailsIsOwedThenGrantedExactlyOnce(t *testing.T) {
	ndsctl := installRenewalNdsctl(t)
	ndsctl.setRegistered(t, true)
	ndsctl.setAuthenticated(t, false)
	dir := t.TempDir()
	m := newOwedGrantMerchant(t, dir)
	m.tollwallet = &owedGrantWallet{}

	ndsctl.failAuth(t, true)
	event, err := m.PurchaseSession("cashuBowed-grant-fixture-note", owedGrantMAC)
	if err != nil {
		t.Fatalf("PurchaseSession: %v", err)
	}
	if code := noticeErrorCode(t, event); code != "payment-received-grant-pending" {
		t.Fatalf("notice code: want payment-received-grant-pending, got %q", code)
	}
	if !strings.Contains(event.Content, "do NOT need to pay again") {
		t.Fatalf("notice must tell the customer not to pay again: %q", event.Content)
	}

	// The claim is durable before the response completes.
	grants := readOwedGrantStore(t, dir)
	if len(grants) != 1 {
		t.Fatalf("expected exactly one persisted owed grant, got %d", len(grants))
	}
	for _, rec := range grants {
		if rec.State != owedGrantStateOwed {
			t.Fatalf("persisted state: want owed, got %q", rec.State)
		}
		if rec.Allotment == 0 {
			t.Fatal("persisted grant must carry the paid allotment")
		}
	}

	// No session may linger from a failed grant: the gate never opened, so
	// the tentative AddAllotment is rolled back. A monitor attempt in flight
	// transiently holds the session it is trying to grant (observable under
	// the compressed retry clock, #733), so the pinned invariant is "no
	// session whenever no attempt is running" — which still catches exactly
	// the lingering-session failure this assertion exists for.
	waitForOwed(t, 5*time.Second, func() bool {
		m.owedGrantsMu.Lock()
		processing := false
		for _, rec := range m.owedGrants {
			processing = processing || rec.Processing
		}
		m.owedGrantsMu.Unlock()
		if processing {
			return false
		}
		_, err := m.GetSession(owedGrantMAC)
		return err != nil
	}, "no session while the grant is owed and no attempt is in flight")

	authsBefore := ndsctl.count(t, "AUTH ")

	// NDS recovers; the monitor converges. The session exists from the
	// allotment BEFORE the gate opens, and the successful AUTH lands after
	// it — so each observable gets its own poll: asserting the AUTH delta
	// immediately after the session appears races the auth call.
	ndsctl.failAuth(t, false)
	ndsctl.setAuthenticated(t, true)
	waitForOwed(t, 25*time.Second, func() bool {
		_, err := m.GetSession(owedGrantMAC)
		return err == nil
	}, "the owed grant to create the session")
	waitForOwed(t, 10*time.Second, func() bool {
		return ndsctl.count(t, "AUTH ") > authsBefore
	}, "a post-recovery AUTH for the owed grant")

	session, err := m.GetSession(owedGrantMAC)
	if err != nil {
		t.Fatalf("session after convergence: %v", err)
	}
	paidAllotment := uint64(renewalSats * renewalStepMS)
	if session.Allotment != paidAllotment {
		t.Fatalf("granted allotment: want exactly one %d, got %d", paidAllotment, session.Allotment)
	}

	waitForOwed(t, 5*time.Second, func() bool {
		grants := readOwedGrantStore(t, dir)
		for _, rec := range grants {
			return rec.State == owedGrantStateGranted
		}
		return false
	}, "the persisted grant to be marked granted")

	// No double grant: the allotment stays one paid purchase's worth across a
	// settle window of several compressed retry cycles — a monitor that
	// wrongly granted again would have doubled it. (The AUTH log cannot be
	// the exactly-once evidence under the compressed clock: it records every
	// attempt, and the monitor legitimately fires failing attempts while
	// failAuth is still armed during the test's own bookkeeping.)
	time.Sleep(10 * 40 * time.Millisecond) // 10 compressed retry cycles (#733)
	session, err = m.GetSession(owedGrantMAC)
	if err != nil {
		t.Fatalf("session after settle: %v", err)
	}
	if session.Allotment != paidAllotment {
		t.Fatalf("allotment after settle: want %d (exactly once), got %d", paidAllotment, session.Allotment)
	}
}

// TestOwedGrantSurvivesRestartAndConverges pins the restart half: a process
// that persisted the claim and died (crash between the durable write and the
// grant) — a fresh merchant over the same store picks the entitlement up and
// grants it once NDS accepts, exactly once, with no second payment. The store
// is seeded directly (not through a first merchant's monitor) so exactly one
// process owns the claim, as on a real router.
func TestOwedGrantSurvivesRestartAndConverges(t *testing.T) {
	ndsctl := installRenewalNdsctl(t)
	ndsctl.setRegistered(t, true)
	dir := t.TempDir()

	crashed := newOwedGrantMerchant(t, dir)
	const restartReference = "owed-restart-crash-window-reference"
	if err := crashed.owedGrantStore.saveGrants(map[string]*owedGrantRecord{
		restartReference: {
			Reference:  restartReference,
			MacAddress: owedGrantMAC,
			MintURL:    renewalMintURL,
			AmountSats: renewalSats,
			Allotment:  uint64(renewalSats * renewalStepMS),
			Metric:     "milliseconds",
			CreatedAt:  time.Now(),
			State:      owedGrantStateOwed,
		},
	}); err != nil {
		t.Fatalf("seed store: %v", err)
	}

	// The "restart": a fresh merchant over the same durable store, with NDS
	// accepting. The valve's gate state is process-global; closing it models
	// the router restart that precedes the merchant restart (a real restart
	// resets NoDogSplash's authorisations too).
	_ = valve.CloseGate(owedGrantMAC)
	second := newOwedGrantMerchant(t, dir)
	second.tollwallet = &owedGrantWallet{}
	second.loadOwedGrantsFromDisk()

	waitForOwed(t, 25*time.Second, func() bool {
		grants := readOwedGrantStore(t, dir)
		for _, rec := range grants {
			return rec.State == owedGrantStateGranted
		}
		return false
	}, "the restarted merchant to grant the owed entitlement durably")

	session, err := second.GetSession(owedGrantMAC)
	if err != nil {
		t.Fatalf("session after restart convergence: %v", err)
	}
	if session.Allotment != uint64(renewalSats*renewalStepMS) {
		t.Fatalf("restarted grant must be exactly one allotment, got %d", session.Allotment)
	}
}

// TestOwedTimeGrantExpiresPastItsWindow pins the convergence bound: a
// milliseconds entitlement whose paid window has passed converges to the
// expired state with no grant, instead of retrying forever or granting
// worthless time.
func TestOwedTimeGrantExpiresPastItsWindow(t *testing.T) {
	ndsctl := installRenewalNdsctl(t)
	ndsctl.setRegistered(t, true)
	dir := t.TempDir()
	m := newOwedGrantMerchant(t, dir)
	m.tollwallet = &owedGrantWallet{}

	// Seed the store with an owed record whose window already passed.
	stale := &persistedOwedGrant{
		Reference:  "owed-expired-window-reference",
		MacAddress: owedGrantMAC,
		MintURL:    renewalMintURL,
		AmountSats: renewalSats,
		Allotment:  renewalStepMS,
		Metric:     "milliseconds",
		CreatedAt:  time.Now().Add(-2 * time.Hour),
		State:      owedGrantStateOwed,
	}
	if err := m.owedGrantStore.saveGrants(map[string]*owedGrantRecord{
		stale.Reference: {
			Reference:  stale.Reference,
			MacAddress: stale.MacAddress,
			MintURL:    stale.MintURL,
			AmountSats: stale.AmountSats,
			Allotment:  stale.Allotment,
			Metric:     stale.Metric,
			CreatedAt:  stale.CreatedAt,
			State:      stale.State,
		},
	}); err != nil {
		t.Fatalf("seed store: %v", err)
	}

	authsBefore := ndsctl.count(t, "AUTH ")
	m.loadOwedGrantsFromDisk()

	waitForOwed(t, 5*time.Second, func() bool {
		grants := readOwedGrantStore(t, dir)
		return len(grants) == 1 && grants[stale.Reference].State == owedGrantStateExpired
	}, "the stale entitlement to converge to expired")

	if _, err := m.GetSession(owedGrantMAC); err == nil {
		t.Fatal("an expired entitlement must not grant a session")
	}
	if got := ndsctl.count(t, "AUTH "); got > authsBefore {
		t.Fatalf("an expired entitlement must not attempt gate opens; before=%d after=%d", authsBefore, got)
	}
}

// TestOwedGrantIsNotDuplicatedForTheSameReference pins the duplicate rule:
// the same note cannot create two claims, even if its submissions slip past
// the in-flight guard (crash windows, retries).
func TestOwedGrantIsNotDuplicatedForTheSameReference(t *testing.T) {
	ndsctl := installRenewalNdsctl(t)
	ndsctl.setRegistered(t, true)
	dir := t.TempDir()
	m := newOwedGrantMerchant(t, dir)
	m.tollwallet = &owedGrantWallet{}

	ndsctl.failAuth(t, true)
	m.recordOwedGrant("dup-reference", owedGrantMAC, renewalMintURL, renewalSats, renewalStepMS)
	m.recordOwedGrant("dup-reference", owedGrantMAC, renewalMintURL, renewalSats, renewalStepMS)

	m.owedGrantsMu.Lock()
	count := len(m.owedGrants)
	m.owedGrantsMu.Unlock()
	if count != 1 {
		t.Fatalf("the same reference must yield exactly one owed grant, got %d", count)
	}

	grants := readOwedGrantStore(t, dir)
	if len(grants) != 1 {
		t.Fatalf("persisted store must hold exactly one owed grant, got %d", len(grants))
	}
}
