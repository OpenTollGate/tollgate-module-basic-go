package merchant

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
)

// fakeConfigProvider implements mintConfigProvider so tests can drive
// runAggressiveCheck without the real config_manager wiring.
type fakeConfigProvider struct {
	cfg *config_manager.Config
}

func (f *fakeConfigProvider) GetConfig() *config_manager.Config { return f.cfg }

// newAggressiveTestTracker builds a tracker wired to a single mint URL.
func newAggressiveTestTracker(mintURL string) *MintHealthTracker {
	return NewMintHealthTracker(&fakeConfigProvider{
		cfg: &config_manager.Config{
			AcceptedMints: []config_manager.MintConfig{{URL: mintURL}},
		},
	})
}

// TestRunAggressiveCheck_RecoversWhenMintBecomesReachable verifies the core
// startup-retry contract: a mint that was unreachable at startup and now
// responds is marked reachable, fires onFirstReachable, bumps reachableCount,
// and reports recovered=true. The aggressive path uses threshold=1, so a
// single successful probe is enough — this is what makes startup recovery
// ~15s instead of ~15min (the normal path's threshold-3 x 5min interval).
func TestRunAggressiveCheck_RecoversWhenMintBecomesReachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	tracker := newAggressiveTestTracker(srv.URL)
	tracker.reachableMints[srv.URL] = false // boot state: nothing reachable

	firstReachableCh := make(chan struct{}, 1)
	tracker.onFirstReachable = func() {
		select {
		case firstReachableCh <- struct{}{}:
		default:
		}
	}

	recovered := tracker.runAggressiveCheck(&http.Client{Timeout: 3 * time.Second})

	if !recovered {
		t.Fatal("expected recovered=true when a previously-unreachable mint responds")
	}
	if !tracker.reachableMints[srv.URL] {
		t.Error("expected reachableMints[url]=true after a successful probe")
	}
	if tracker.reachableCount != 1 {
		t.Errorf("expected reachableCount=1, got %d", tracker.reachableCount)
	}
	if !tracker.hadReachableMint {
		t.Error("expected hadReachableMint=true after first reachable mint")
	}
	// runAggressiveCheck fires callbacks via `go cb()` (async), so block on the
	// receive with a timeout rather than a non-blocking select.
	select {
	case <-firstReachableCh:
	case <-time.After(2 * time.Second):
		t.Error("expected onFirstReachable callback to fire exactly once")
	}
}

// TestRunAggressiveCheck_NoRecoveryWhenMintStillUnreachable verifies the
// negative path: a mint that still does not respond stays unreachable, does
// not fire onFirstReachable, leaves reachableCount at 0, and reports
// recovered=false. The aggressive retry loop relies on this so it keeps
// probing every 15s until the mint comes back.
func TestRunAggressiveCheck_NoRecoveryWhenMintStillUnreachable(t *testing.T) {
	// A server that hijacks and immediately closes the connection — the probe
	// GET errors out, modelling an unreachable mint without depending on DNS.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "no hijack", http.StatusInternalServerError)
			return
		}
		conn, _, _ := hj.Hijack()
		conn.Close()
	}))
	defer srv.Close()

	tracker := newAggressiveTestTracker(srv.URL)
	tracker.reachableMints[srv.URL] = false

	tracker.onFirstReachable = func() {
		t.Error("onFirstReachable must NOT fire when the mint stays unreachable")
	}

	recovered := tracker.runAggressiveCheck(&http.Client{Timeout: 2 * time.Second})

	if recovered {
		t.Fatal("expected recovered=false when the mint stays unreachable")
	}
	if tracker.reachableMints[srv.URL] {
		t.Error("expected reachableMints[url]=false after a failed probe")
	}
	if tracker.reachableCount != 0 {
		t.Errorf("expected reachableCount=0, got %d", tracker.reachableCount)
	}
	if tracker.hadReachableMint {
		t.Error("expected hadReachableMint=false when no mint has ever been reachable")
	}
}
