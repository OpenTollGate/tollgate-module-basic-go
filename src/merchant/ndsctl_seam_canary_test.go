package merchant

import (
	"os"
	"strings"
	"testing"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/valve"
)

// ndsctlSeamLogPath is where the package's default ndsctl seam (TestMain in
// ndsctl_seam_test.go) pointed the staged fake's NDSCTL_LOG. Empty when no
// seam staged — the canary below turns that into a named failure instead of
// host-dependent silence.
var ndsctlSeamLogPath string

// TestDefaultNdsctlSeamAnswersGrants pins the class #853 named after #835
// fixed its one instance: a merchant harness whose assertion walks a
// successful grant — built with NO per-test ndsctl fake — must be answered
// by the package's DEFAULT seam, on every host, or fail loudly naming why.
//
// The three failure shapes this canary distinguishes:
//
//   - bare host, no seam: the grant dies as a kind-21023
//     payment-received-grant-pending notice (exec: "ndsctl": executable
//     file not found), the owed-grant noise that polluted #731/#732/#733;
//   - host with an answerless ndsctl stub (exit 0, no output): the auth may
//     well succeed, so the grant passes — but nothing records it, which is
//     exactly the silent host dependence #853 is about: the suite was green
//     by accident of the host, and `ndsctl json` reads degraded silently;
//   - seam staged: the grant succeeds AND the staged fake's AUTH line for
//     this purchase's MAC is in the seam's own NDSCTL_LOG, proving the
//     default seam — not a host binary — answered.
//
// The harness deliberately does NOT call installRenewalNdsctl: tests that
// need failure controls keep installing their own fake (their per-test PATH
// prepend shadows the seam for their duration); this canary exists to fail
// the day someone removes the seam or adds a grant-exercising harness shape
// the seam no longer covers.
func TestDefaultNdsctlSeamAnswersGrants(t *testing.T) {
	wallet := &scriptedReceiveWallet{outcomes: []receiveResult{{amount: 1}}}

	cm, _ := setupTestConfigManager(t)
	cfg := cm.GetConfig()
	cfg.AcceptedMints = append(cfg.AcceptedMints, config_manager.MintConfig{
		URL: "https://preflight-mint.example.com", PricePerStep: 1, PriceUnit: "sat", MinPurchaseSteps: 1,
	})
	m := &Merchant{
		config:            cfg,
		configManager:     cm,
		tollwallet:        wallet,
		mintHealthTracker: newTestTracker(cm.GetConfig(), nil),
	}
	stubPreflightProbe(t, m, func(string) (valve.ClientState, error) {
		return valve.ClientState{Registered: true}, nil
	})

	// A MAC unique to this test, so the AUTH line in the seam's log is
	// unambiguous evidence THIS purchase opened the gate.
	const mac = "AA:BB:CC:DD:85:03"
	event, err := m.PurchaseSession("cashuBseam-canary-note", mac)
	if err != nil {
		t.Fatalf("the canary purchase returned an error instead of an event: %v", err)
	}
	if event == nil || event.Kind != 1022 {
		if ndsctlSeamLogPath == "" {
			t.Fatalf("the canary grant failed (got %+v) and the default ndsctl seam did not stage — on a host without a working ndsctl this is #853's class symptom: a grant-exercising assertion dying as owed-grant noise", event)
		}
		t.Fatalf("the canary grant failed under the staged default seam, got %+v", event)
	}

	if ndsctlSeamLogPath == "" {
		t.Fatal("the canary grant succeeded but the default ndsctl seam did not stage — a host binary answered instead (the #853 masquerade): nothing records the authorization, `ndsctl json` answers degrade silently, and the suite is host-dependent by accident")
	}

	body, readErr := os.ReadFile(ndsctlSeamLogPath)
	if readErr != nil {
		t.Fatalf("read the default seam's ndsctl log: %v", readErr)
	}
	if !strings.Contains(string(body), "AUTH "+strings.ToLower(mac)) {
		t.Fatalf("no AUTH %s line in the default seam's log — a binary other than the staged seam answered the grant (log: %q)", mac, string(body))
	}
}
