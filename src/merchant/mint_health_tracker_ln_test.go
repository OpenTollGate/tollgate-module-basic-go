package merchant

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
)

// mintInfoBody is a minimal Cashu /v1/info response. The LN probe does not
// depend on its contents; it is only here so the reachability probe (/v1/info)
// returns a valid 200 body like a real mint would.
const mintInfoBody = `{"name":"test-mint"}`

// mintQuoteResponse mirrors the relevant fields of a Cashu NUT-04
// POST /v1/mint/quote/bolt11 response. A non-empty "request" (bolt11 invoice)
// is the signal that the mint's Lightning backend is actually working.
const mintQuoteResponse = `{"quote":"q1","request":"lnbc10n1pj...","expiry":600,"state":"UNPAID"}`

// lnMintServer returns a test mint that answers /v1/info and the NUT-04
// mint-quote endpoint. If lnOK is false the quote endpoint simulates a dead
// Lightning backend (503). It also records how many LN quote probes hit it.
func lnMintServer(lnOK bool) (*httptest.Server, *int) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/info":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(mintInfoBody))
		case "/v1/mint/quote/bolt11":
			hits++
			if !lnOK {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			// Only POST is valid; reject others so a wrong method is visible.
			if r.Method != http.MethodPost {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(mintQuoteResponse))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	return srv, &hits
}

func TestSupportsLN_UnknownMint_FalseByDefault(t *testing.T) {
	tracker := newTestTracker(mintConfigWithURLs("https://mint-a.test"), nil)
	if tracker.SupportsLN("https://mint-a.test") {
		t.Error("expected SupportsLN=false for a mint that was never probed")
	}
}

func TestRunInitialProbe_LNWorking_SupportsLNTrue(t *testing.T) {
	srv, lnHits := lnMintServer(true)
	defer srv.Close()

	tracker := newTestTracker(mintConfigWithURLs(srv.URL), nil)
	tracker.RunInitialProbe()

	if !tracker.IsReachable(srv.URL) {
		t.Fatal("expected mint to be reachable")
	}
	if !tracker.SupportsLN(srv.URL) {
		t.Error("expected SupportsLN=true when the LN quote probe succeeds")
	}
	if *lnHits != 1 {
		t.Errorf("expected exactly 1 LN probe during initial probe, got %d", *lnHits)
	}
}

func TestRunInitialProbe_LNBackendDown_SupportsLNFalseButStillReachable(t *testing.T) {
	srv, _ := lnMintServer(false)
	defer srv.Close()

	tracker := newTestTracker(mintConfigWithURLs(srv.URL), nil)
	tracker.RunInitialProbe()

	// The mint itself is reachable (its /v1/info works)...
	if !tracker.IsReachable(srv.URL) {
		t.Fatal("expected mint to remain reachable when only its LN backend is down")
	}
	// ...but Lightning must NOT be advertised.
	if tracker.SupportsLN(srv.URL) {
		t.Error("expected SupportsLN=false when the LN quote probe fails (503)")
	}
}

func TestRunInitialProbe_UnreachableMint_NotProbedForLN(t *testing.T) {
	// A mint that is completely down (connection refused). It must be marked
	// neither reachable nor LN-capable, and no LN probe should be attempted.
	tracker := newTestTracker(mintConfigWithURLs("http://127.0.0.1:1"), nil)
	tracker.RunInitialProbe()

	if tracker.IsReachable("http://127.0.0.1:1") {
		t.Error("expected mint to be unreachable")
	}
	if tracker.SupportsLN("http://127.0.0.1:1") {
		t.Error("expected SupportsLN=false for an unreachable mint")
	}
}

func TestRunInitialProbe_MixedLN_MintsAdvertisedIndependently(t *testing.T) {
	srvLN, _ := lnMintServer(true)
	defer srvLN.Close()
	srvNoLN, _ := lnMintServer(false)
	defer srvNoLN.Close()

	tracker := newTestTracker(mintConfigWithURLs(srvLN.URL, srvNoLN.URL), nil)
	tracker.RunInitialProbe()

	if !tracker.IsReachable(srvLN.URL) || !tracker.IsReachable(srvNoLN.URL) {
		t.Fatal("expected both mints reachable (both serve /v1/info)")
	}
	if !tracker.SupportsLN(srvLN.URL) {
		t.Error("mint with working LN should advertise LN support")
	}
	if tracker.SupportsLN(srvNoLN.URL) {
		t.Error("mint with broken LN must NOT advertise LN support")
	}
}

func TestProactiveCheck_LNRecoversAfterBackendComesBack(t *testing.T) {
	// Start with a mint whose LN backend is down.
	srv, lnHits := lnMintServer(false)
	defer srv.Close()

	tracker := newTestTracker(mintConfigWithURLs(srv.URL), nil)
	tracker.recoveryThreshold = 1 // reachability recovers fast; irrelevant here
	tracker.RunInitialProbe()
	if tracker.SupportsLN(srv.URL) {
		t.Fatal("expected SupportsLN=false initially")
	}

	// Flip the backend to healthy and force a fresh server that answers LN.
	srv.Close()
	srvOK, lnHitsOK := lnMintServer(true)
	defer srvOK.Close()
	tracker.configProvider.(*mockConfigProvider).config = mintConfigWithURLs(srvOK.URL)

	tracker.RunProactiveCheck()

	if !tracker.SupportsLN(srvOK.URL) {
		t.Error("expected SupportsLN to flip true after the backend recovers")
	}
	if *lnHitsOK < 1 {
		t.Errorf("expected at least 1 LN probe during proactive check, got %d", *lnHitsOK)
	}
	_ = lnHits
}

func TestMarkLNUnavailable_ReachableButLNDown(t *testing.T) {
	srv, _ := lnMintServer(true)
	defer srv.Close()

	tracker := newTestTracker(mintConfigWithURLs(srv.URL), nil)
	tracker.RunInitialProbe()
	if !tracker.SupportsLN(srv.URL) {
		t.Fatal("expected SupportsLN=true before reactive marking")
	}

	// Simulate a real invoice request failing at runtime → degrade reactively.
	tracker.MarkLNUnavailable(srv.URL)

	if tracker.SupportsLN(srv.URL) {
		t.Error("expected SupportsLN=false after MarkLNUnavailable")
	}
	if !tracker.IsReachable(srv.URL) {
		t.Error("MarkLNUnavailable must not affect reachability")
	}
}

// runAggressiveCheck is the startup fast-retry path used when no mints are
// reachable yet (e.g. WiFi still connecting). It must also probe Lightning so a
// mint that comes online during aggressive retry advertises LN correctly.
func TestRunAggressiveCheck_ProbesLightning(t *testing.T) {
	srv, lnHits := lnMintServer(true)
	defer srv.Close()

	tracker := newTestTracker(mintConfigWithURLs(srv.URL), nil)
	aggressiveClient := &http.Client{Timeout: 5 * time.Second}

	// Drive the aggressive path directly (it is normally launched as a goroutine
	// by StartProactiveChecks). Use an explicit assertion instead of timing.
	recovered := tracker.runAggressiveCheck(aggressiveClient)
	if !recovered {
		t.Fatal("expected runAggressiveCheck to report a mint recovery")
	}
	if !tracker.IsReachable(srv.URL) {
		t.Error("expected mint to be reachable after aggressive check")
	}
	if !tracker.SupportsLN(srv.URL) {
		t.Error("expected SupportsLN=true after aggressive check with working LN backend")
	}
	if *lnHits < 1 {
		t.Errorf("expected at least 1 LN probe during aggressive check, got %d", *lnHits)
	}
}

func TestCreateAdvertisement_OnlyAdvertisesLNForCapableMints(t *testing.T) {
	srvLN, _ := lnMintServer(true)
	defer srvLN.Close()
	srvNoLN, _ := lnMintServer(false)
	defer srvNoLN.Close()

	cm := newConfigManagerWithMints(t, srvLN.URL, srvNoLN.URL)
	tracker := newTestTracker(cm.GetConfig(), nil)
	tracker.RunInitialProbe()

	adStr, err := CreateAdvertisement(cm, tracker)
	if err != nil {
		t.Fatalf("CreateAdvertisement failed: %v", err)
	}

	var evt struct {
		Tags [][]string `json:"tags"`
	}
	if err := json.Unmarshal([]byte(adStr), &evt); err != nil {
		t.Fatalf("advertisement is not valid JSON: %v", err)
	}

	lnSupported := map[string]bool{}
	for _, tag := range evt.Tags {
		if len(tag) >= 3 && tag[0] == "supports_ln" {
			lnSupported[tag[1]] = tag[2] == "true"
		}
	}
	if !lnSupported[srvLN.URL] {
		t.Errorf("expected supports_ln tag for LN-capable mint %s; got tags=%v", srvLN.URL, evt.Tags)
	}
	if lnSupported[srvNoLN.URL] {
		t.Errorf("did not expect supports_ln tag for LN-broken mint %s; got tags=%v", srvNoLN.URL, evt.Tags)
	}
}

// newConfigManagerWithMints builds a real ConfigManager (auto-provisioning a
// merchant identity so CreateAdvertisement can sign) whose accepted mints are
// exactly the given URLs. It writes a valid config file to a temp dir first so
// NewConfigManager loads OUR mints (no dev-build mint injection), making the
// advertisement test fully hermetic.
func newConfigManagerWithMints(t *testing.T, urls ...string) *config_manager.ConfigManager {
	t.Helper()
	testDir := t.TempDir()
	t.Setenv("TOLLGATE_TEST_CONFIG_DIR", testDir)
	configPath := filepath.Join(testDir, "config.json")
	installPath := filepath.Join(testDir, "install.json")
	identitiesPath := filepath.Join(testDir, "identities.json")

	mints := make([]config_manager.MintConfig, len(urls))
	for i, u := range urls {
		mints[i] = config_manager.MintConfig{URL: u, PricePerStep: 1, PriceUnit: "sats"}
	}
	seed := &config_manager.Config{
		ConfigVersion: "v0.0.8",
		Metric:        "milliseconds",
		StepSize:      1000,
		AcceptedMints: mints,
		// ValidateProfitShare requires at least one entry summing to 1.0.
		ProfitShare: []config_manager.ProfitShareConfig{{Identity: "merchant", Factor: 1.0}},
	}
	if err := config_manager.SaveConfig(configPath, seed); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	cm, err := config_manager.NewConfigManager(configPath, installPath, identitiesPath)
	if err != nil {
		t.Fatalf("NewConfigManager: %v", err)
	}
	return cm
}
