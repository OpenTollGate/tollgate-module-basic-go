package merchant

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// Startup-probe budget regression tests.
//
// The defect these guard: merchant.New() runs RunInitialProbe() before main()
// reaches net/http's ListenAndServe, and the probe used to walk every accepted
// mint to its own probeTimeout. When the uplink is not up yet — a cold boot —
// nothing answers, so the cost is N x probeTimeout before :2121 binds and
// /var/run/tollgate.sock exists, while procd's `status` already reports
// "running" because the process (not the API) is alive. Measured on the bench
// GL-MT3000 with the shipped pre17/pre18 pin: 7 mints, minutes of a dead money
// path, then `PASS TCP 2121` with no change made — a boot race, not a failure
// to start.

// hangingMintServer accepts the probe and never answers, so every probe against
// it burns the client timeout instead of failing fast. That is the cold-boot
// shape (uplink up, nothing answering / DNS hanging), not a refused connection.
func hangingMintServer(t *testing.T) *httptest.Server {
	t.Helper()
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	t.Cleanup(func() {
		// Unblock the handlers before Close, which otherwise waits for them.
		close(release)
		srv.Close()
	})
	return srv
}

// alwaysReachableServer answers a valid NUT-01 keyset body on every path, so a
// mint URL with a suffix is usable without the helper's path check.
func alwaysReachableServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeKeysetsOK(w)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func mintURLs(srv *httptest.Server, n int) []string {
	urls := make([]string, n)
	for i := range urls {
		urls[i] = fmt.Sprintf("%s/mint-%d", srv.URL, i)
	}
	return urls
}

// TestRunInitialProbeIsBoundedByTheStartupBudget is the headline guard. With 7
// non-answering mints and a 2 s client timeout, unbounded probing costs 14 s
// before RunInitialProbe — and therefore the whole API — returns.
func TestRunInitialProbeIsBoundedByTheStartupBudget(t *testing.T) {
	srv := hangingMintServer(t)
	tracker := newTestTracker(mintConfigWithURLs(mintURLs(srv, 7)...), &http.Client{Timeout: 2 * time.Second})
	tracker.startupProbeBudget = 1 * time.Second

	start := time.Now()
	tracker.RunInitialProbe()
	elapsed := time.Since(start)

	// Slack for a loaded box, but far below the unbounded 14 s.
	if elapsed > 5*time.Second {
		t.Errorf("RunInitialProbe took %s with a 1 s startup budget and 7 non-answering mints: the startup probe is unbounded again (7 x 2 s client timeout)", elapsed)
	}
	if tracker.reachableCount != 0 {
		t.Errorf("reachableCount = %d, want 0: no mint answered", tracker.reachableCount)
	}
}

// TestRunInitialProbeLeavesUnprobedMintsUnlearned pins the second half of the
// contract: a probe the budget never reached must record nothing at all. Marking
// those mints failed would be a lie the probe never earned, and it is the same
// "a skipped probe learns nothing" convention runProactiveCheck uses for a mint
// inside its Retry-After window.
func TestRunInitialProbeLeavesUnprobedMintsUnlearned(t *testing.T) {
	srv := hangingMintServer(t)
	urls := mintURLs(srv, 7)
	tracker := newTestTracker(mintConfigWithURLs(urls...), &http.Client{Timeout: 2 * time.Second})
	tracker.startupProbeBudget = 1 * time.Second

	tracker.RunInitialProbe()

	unlearned := 0
	for _, u := range urls {
		_, recorded := tracker.reachableMints[u]
		if recorded || tracker.consecutiveFailures[u] != 0 || tracker.consecutiveSuccesses[u] != 0 {
			continue
		}
		unlearned++
	}
	// Only the first mint can have been probed inside a 1 s budget.
	if unlearned < len(urls)-1 {
		t.Errorf("only %d of %d mints were left unlearned after the budget expired; the probe recorded a verdict for mints it never reached", unlearned, len(urls))
	}
}

// TestRunInitialProbeFastPathIsUnchanged is the counterweight: when mints answer
// promptly the budget must be invisible — every mint is still probed and the
// reachable set is what the proactive path would have produced.
func TestRunInitialProbeFastPathIsUnchanged(t *testing.T) {
	srv := alwaysReachableServer(t)
	urls := mintURLs(srv, 3)
	tracker := newTestTracker(mintConfigWithURLs(urls...), nil)
	tracker.startupProbeBudget = 5 * time.Second

	start := time.Now()
	tracker.RunInitialProbe()
	elapsed := time.Since(start)

	if tracker.reachableCount != len(urls) {
		t.Fatalf("reachableCount = %d, want %d: the budget changed the fast path", tracker.reachableCount, len(urls))
	}
	for _, u := range urls {
		if !tracker.IsReachable(u) {
			t.Errorf("mint %s not reachable after a prompt probe", u)
		}
	}
	if elapsed > 3*time.Second {
		t.Errorf("prompt probes took %s; the budget is being paid even when mints answer", elapsed)
	}
}

// TestStartupProbeBudgetDefaultAndOverride pins the knob's arithmetic: the
// default is one probeTimeout, and the per-router override is honoured — a
// nonsensical override falls back rather than wrapping (envIntOr refuses
// non-positive input).
func TestStartupProbeBudgetDefaultAndOverride(t *testing.T) {
	t.Setenv("TOLLGATE_STARTUP_PROBE_BUDGET_SECONDS", "")
	if got := NewMintHealthTracker(&mockConfigProvider{config: mintConfigWithURLs("https://mint.test")}).startupProbeBudget; got != defaultStartupProbeBudget {
		t.Errorf("default startupProbeBudget = %s, want %s", got, defaultStartupProbeBudget)
	}

	t.Setenv("TOLLGATE_STARTUP_PROBE_BUDGET_SECONDS", "7")
	if got := NewMintHealthTracker(&mockConfigProvider{config: mintConfigWithURLs("https://mint.test")}).startupProbeBudget; got != 7*time.Second {
		t.Errorf("override startupProbeBudget = %s, want 7s", got)
	}

	t.Setenv("TOLLGATE_STARTUP_PROBE_BUDGET_SECONDS", "0")
	if got := NewMintHealthTracker(&mockConfigProvider{config: mintConfigWithURLs("https://mint.test")}).startupProbeBudget; got != defaultStartupProbeBudget {
		t.Errorf("nonsensical override startupProbeBudget = %s, want the default %s", got, defaultStartupProbeBudget)
	}
}
