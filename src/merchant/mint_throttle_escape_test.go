package merchant

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/tollwallet"
)

// --- the forever-429 escape hatch ------------------------------------------
//
// PR #574 fixed the first half of the 429 story: a mint that answers 429 is
// busy, not broken, so it stays in the reachable set and never trips the
// merchant's self-downgrade. The gap it left open is the mirror image: a mint
// whose front answers 429 to EVERY probe is *reachable* for ever, so it stays
// in the advertisement, the customer's client keeps picking it out of
// `price_per_step`, and every purchase fails — and nothing self-heals, because
// the aggressive 15 s probe mode only arms when the reachable set is EMPTY.
//
// The contract these tests pin:
//
//   - a 429 that carries `Retry-After` is still "the mint is up", and the wait
//     it asks for is honoured (both header forms) but capped, so a bogus or
//     stale value cannot silence a mint's probes for ever;
//   - a mint throttled on every probe for the persistent window is demoted from
//     the ADVERTISEMENT (and skipped by the payout routine) while staying in
//     the reachable set, in the wallet and in the registered mints;
//   - the advertisement is never emptied by that demotion (single-mint guard);
//   - the first successful probe re-admits the mint immediately;
//   - a genuine non-429 failure keeps its own, stronger demotion (the reachable
//     set, after defaultFailureThreshold probes) and is never mistaken for a
//     persistent throttle.

// throttledKeysetsServer answers the keysets probe with status(), sends the
// Retry-After retryAfter() returns (empty means "none — a front that
// rate-limits without advising a wait, which a 429 is allowed to do"), and
// counts every probe that reached it. The hit count is how "no probe before the
// stated time" is observed: it is the only externally visible fact.
func throttledKeysetsServer(t *testing.T, status func() int, retryAfter func() string, hits *int32) string {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(hits, 1)
		code := status()
		if code == http.StatusOK {
			writeKeysetsOK(w)
			return
		}
		if ra := retryAfter(); ra != "" {
			w.Header().Set("Retry-After", ra)
		}
		w.WriteHeader(code)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// newClockTracker returns a tracker whose clock the test owns, so a Retry-After
// window or a throttle streak is driven without sleeping — the same per-instance
// settable shape the aggressive-retry timings already use.
func newClockTracker(t *testing.T, config *config_manager.Config, now *time.Time) *MintHealthTracker {
	t.Helper()

	tracker := newTestTracker(config, nil)
	tracker.clock = func() time.Time { return *now }
	return tracker
}

// throttleFor drives a mint to the persistently-throttled state through the
// production path: the boot probe plus proactive checks, all answered 429 with
// no Retry-After. It returns the number of probes that were consumed.
func throttleFor(t *testing.T, tracker *MintHealthTracker) int {
	t.Helper()

	tracker.RunInitialProbe()
	probes := 1
	for probes < int(tracker.persistentThrottleProbes) {
		tracker.RunProactiveCheck()
		probes++
	}
	return probes
}

// A 429 carrying Retry-After is still an answer from a live mint — so it maps to
// probeThrottled (never probeFailed), and the wait it asks for surfaces instead
// of being discarded.
func TestProbe429WithRetryAfterMapsToThrottled(t *testing.T) {
	var hits int32
	srv := throttledKeysetsServer(t, func() int { return http.StatusTooManyRequests },
		func() string { return "120" }, &hits)

	tracker := newTestTracker(mintConfigWithURLs(srv), nil)

	outcome, wait := tracker.probeMintOutcome(srv, nil)
	if outcome != probeThrottled {
		t.Fatalf("a 429 carrying Retry-After mapped to %v; want probeThrottled — the mint answered, so it is up", outcome)
	}
	if !outcome.reachable() {
		t.Fatal("probeThrottled must count as reachable evidence")
	}
	if wait != 120*time.Second {
		t.Fatalf("Retry-After: 120 yielded a %v wait; want 120s — the header must be parsed, not discarded", wait)
	}

	tracker.RunInitialProbe()
	if !tracker.IsReachable(srv) {
		t.Fatal("a 429 removed the mint from the reachable set: busy is still not broken")
	}
	if tracker.IsPersistentlyThrottled(srv) {
		t.Fatal("one throttled probe demoted the mint: a single 429 is a burst, not a forever-429")
	}
}

// An HTTP-date Retry-After means the same thing as delta-seconds, and a
// malformed or already-past value means "probe as usual" rather than an
// unbounded or negative wait.
func TestRetryAfterParsing(t *testing.T) {
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name  string
		value string
		want  time.Duration
	}{
		{"delta-seconds", "120", 120 * time.Second},
		{"http-date", now.Add(3 * time.Minute).Format(http.TimeFormat), 3 * time.Minute},
		{"empty", "", 0},
		{"malformed", "soon please", 0},
		{"zero", "0", 0},
		{"negative", "-30", 0},
		{"already past", now.Add(-time.Minute).Format(http.TimeFormat), 0},
		{"absurd value is capped", "86400", defaultRetryAfterCap},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseRetryAfter(tt.value, now, defaultRetryAfterCap); got != tt.want {
				t.Errorf("parseRetryAfter(%q) = %v, want %v", tt.value, got, tt.want)
			}
		})
	}
}

// Retry-After is honoured in both forms it may arrive in: no probe is sent
// before the time the mint asked for, and the mint is probed again once that
// time passes (the wait must not outlive the header).
func TestRetryAfterIsHonouredInBothHeaderForms(t *testing.T) {
	base := time.Now()
	tests := []struct {
		name    string
		header  func() string
		skipAt  time.Duration
		probeAt time.Duration
	}{
		{
			name:    "delta-seconds",
			header:  func() string { return "600" },
			skipAt:  5 * time.Minute,
			probeAt: 11 * time.Minute,
		},
		{
			name: "http-date",
			header: func() string {
				return base.Add(10 * time.Minute).UTC().Format(http.TimeFormat)
			},
			skipAt:  5 * time.Minute,
			probeAt: 11 * time.Minute,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var hits int32
			srv := throttledKeysetsServer(t, func() int { return http.StatusTooManyRequests },
				tt.header, &hits)

			now := base
			tracker := newClockTracker(t, mintConfigWithURLs(srv), &now)

			tracker.RunProactiveCheck()
			if got := atomic.LoadInt32(&hits); got != 1 {
				t.Fatalf("precondition: the first check must probe once, got %d", got)
			}

			now = base.Add(tt.skipAt)
			tracker.RunProactiveCheck()
			if got := atomic.LoadInt32(&hits); got != 1 {
				t.Fatalf("the mint was probed again %v after asking us to wait (Retry-After %q): the wait was not honoured",
					tt.skipAt, tt.header())
			}

			now = base.Add(tt.probeAt)
			tracker.RunProactiveCheck()
			if got := atomic.LoadInt32(&hits); got != 2 {
				t.Fatalf("the mint was still not probed %v after the Retry-After it sent: a wait must not outlive the header",
					tt.probeAt)
			}
		})
	}
}

// A Retry-After far beyond our cap is clamped: the header is untrusted input,
// and a front answering "Retry-After: 86400" must not be able to silence a
// mint's probes for ever — the persistent-throttle state could then never clear.
func TestRetryAfterIsCapped(t *testing.T) {
	base := time.Now()
	var hits int32
	srv := throttledKeysetsServer(t, func() int { return http.StatusTooManyRequests },
		func() string { return "86400" }, &hits)

	now := base
	tracker := newClockTracker(t, mintConfigWithURLs(srv), &now)
	if tracker.retryAfterCap != defaultRetryAfterCap {
		t.Fatalf("precondition: an override left the cap at %v, want the default %v", tracker.retryAfterCap, defaultRetryAfterCap)
	}

	tracker.RunProactiveCheck()
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Fatalf("precondition: the first check must probe once, got %d", got)
	}

	now = base.Add(tracker.retryAfterCap - time.Minute)
	tracker.RunProactiveCheck()
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Fatalf("Retry-After: 86400 was not honoured inside the %v cap", tracker.retryAfterCap)
	}

	now = base.Add(tracker.retryAfterCap + time.Minute)
	tracker.RunProactiveCheck()
	if got := atomic.LoadInt32(&hits); got != 2 {
		t.Fatalf("Retry-After: 86400 silenced the mint past the %v cap: the mint would never be re-probed and the throttled state could never clear",
			tracker.retryAfterCap)
	}
}

// The core of the escape hatch: a mint throttled on every probe for the
// persistent window is demoted — exactly at N probes, not before — while
// staying in the reachable set (it is up; demotion is about the advertisement,
// not about health).
func TestSustainedThrottleDemotesAfterExactlyNProbes(t *testing.T) {
	var hits int32
	srvThrottled := throttledKeysetsServer(t, func() int { return http.StatusTooManyRequests },
		func() string { return "" }, &hits)
	srvHealthy := reachableServer(t)

	tracker := newTestTracker(mintConfigWithURLs(srvThrottled, srvHealthy.URL), nil)
	tracker.RunInitialProbe() // probe 1

	if tracker.IsPersistentlyThrottled(srvThrottled) {
		t.Fatal("the mint was demoted after one throttled probe: a burst of 429 must not be treated as a forever-429")
	}

	for i := 2; i < int(tracker.persistentThrottleProbes); i++ {
		tracker.RunProactiveCheck()
		if tracker.IsPersistentlyThrottled(srvThrottled) {
			t.Fatalf("the mint was demoted after %d of %d consecutive throttled probes", i, tracker.persistentThrottleProbes)
		}
		if !tracker.IsReachable(srvThrottled) {
			t.Fatalf("a throttled probe removed the mint from the reachable set after %d probes", i)
		}
	}

	tracker.RunProactiveCheck() // probe N
	if !tracker.IsPersistentlyThrottled(srvThrottled) {
		t.Fatalf("the mint answered 429 to %d consecutive probes and is still not demoted: the customer's client keeps picking a mint that cannot serve it",
			tracker.persistentThrottleProbes)
	}
	if !tracker.IsReachable(srvThrottled) {
		t.Fatal("demotion removed the mint from the reachable set: the mint is up, and an empty set arms the aggressive probe mode (and the degraded downgrade) for no reason")
	}
	if !tracker.IsReachable(srvHealthy.URL) {
		t.Fatal("precondition: the healthy mint must stay reachable")
	}
}

// One throttled probe never demotes, and a throttled probe between successes
// does not accumulate into a demotion: the streak is broken by the next OK
// probe.
func TestOneThrottledProbeNeverDemotes(t *testing.T) {
	throttled := true
	var hits int32
	srv := throttledKeysetsServer(t, func() int {
		if throttled {
			return http.StatusTooManyRequests
		}
		return http.StatusOK
	}, func() string { return "" }, &hits)

	tracker := newTestTracker(mintConfigWithURLs(srv), nil)
	tracker.RunInitialProbe()

	if tracker.IsPersistentlyThrottled(srv) {
		t.Fatal("one throttled probe demoted the mint")
	}

	for i := 0; i < int(tracker.persistentThrottleProbes)-1; i++ {
		throttled = false
		tracker.RunProactiveCheck()
		throttled = true
		tracker.RunProactiveCheck()
		if tracker.IsPersistentlyThrottled(srv) {
			t.Fatalf("a throttled probe in the middle of the run demoted the mint after %d round(s)", i+1)
		}
	}
}

// The escape hatch has to reach the advertisement, or the customer's client
// keeps selecting the throttled mint: with another reachable mint available,
// the throttled one is dropped from the advertised set.
func TestAdvertisementExcludesPersistentlyThrottledMintWhenAnotherIsReachable(t *testing.T) {
	var hits int32
	srvThrottled := throttledKeysetsServer(t, func() int { return http.StatusTooManyRequests },
		func() string { return "" }, &hits)
	srvHealthy := reachableServer(t)

	cm, _ := setupTestConfigManager(t)
	cfg := cm.GetConfig()
	cfg.AcceptedMints = []config_manager.MintConfig{
		{URL: srvThrottled, PricePerStep: 1, PriceUnit: "sat"},
		{URL: srvHealthy.URL, PricePerStep: 1, PriceUnit: "sat"},
	}

	tracker := newTestTracker(cfg, nil)
	throttleFor(t, tracker)
	if !tracker.IsPersistentlyThrottled(srvThrottled) {
		t.Fatal("precondition: the mint must be persistently throttled")
	}

	advertised := tracker.GetAdvertisedMintConfigs()
	if len(advertised) != 1 || advertised[0].URL != srvHealthy.URL {
		t.Fatalf("advertised mints = %+v; want exactly the healthy mint %s — the throttled mint is still being offered to customers",
			advertised, srvHealthy.URL)
	}

	ad, err := CreateAdvertisement(cm, tracker)
	if err != nil {
		t.Fatalf("CreateAdvertisement: %v", err)
	}
	if strings.Contains(ad, srvThrottled) {
		t.Errorf("the advertisement still lists the persistently throttled mint %s: %s", srvThrottled, ad)
	}
	if !strings.Contains(ad, srvHealthy.URL) {
		t.Errorf("the advertisement dropped the healthy mint %s: %s", srvHealthy.URL, ad)
	}

	if !tracker.IsReachable(srvThrottled) {
		t.Fatal("the demoted mint left the reachable set: it must stay registered, in the wallet and reachable")
	}
}

// The single-mint guard: a deployment with no alternative keeps advertising its
// only mint. A customer with no choice is better served by a busy mint than by
// an empty advertisement (the router cannot substitute a mint mid-purchase
// anyway — the ecash is held at that mint).
func TestAdvertisementKeepsSoleMintWhenItIsPersistentlyThrottled(t *testing.T) {
	var hits int32
	srvOnly := throttledKeysetsServer(t, func() int { return http.StatusTooManyRequests },
		func() string { return "" }, &hits)

	cm, _ := setupTestConfigManager(t)
	cfg := cm.GetConfig()
	cfg.AcceptedMints = []config_manager.MintConfig{
		{URL: srvOnly, PricePerStep: 1, PriceUnit: "sat"},
	}

	tracker := newTestTracker(cfg, nil)
	throttleFor(t, tracker)
	if !tracker.IsPersistentlyThrottled(srvOnly) {
		t.Fatal("precondition: the only mint must be persistently throttled")
	}

	advertised := tracker.GetAdvertisedMintConfigs()
	if len(advertised) != 1 || advertised[0].URL != srvOnly {
		t.Fatalf("advertised mints = %+v; want the only mint %s kept — never advertise nothing", advertised, srvOnly)
	}

	ad, err := CreateAdvertisement(cm, tracker)
	if err != nil {
		t.Fatalf("CreateAdvertisement: %v", err)
	}
	if !strings.Contains(ad, srvOnly) {
		t.Errorf("the advertisement was emptied by the single-mint guard failing: %s", ad)
	}
}

// The demotion is not sticky: the first successful probe re-admits the mint
// immediately, with no recovery threshold (the moment the mint answers us, we
// know it is serving again).
func TestSingleOKProbeReAdmitsPersistentlyThrottledMint(t *testing.T) {
	throttled := true
	var hits int32
	srv := throttledKeysetsServer(t, func() int {
		if throttled {
			return http.StatusTooManyRequests
		}
		return http.StatusOK
	}, func() string { return "" }, &hits)
	srvOther := reachableServer(t)

	cm, _ := setupTestConfigManager(t)
	cfg := cm.GetConfig()
	cfg.AcceptedMints = []config_manager.MintConfig{
		{URL: srv, PricePerStep: 1, PriceUnit: "sat"},
		{URL: srvOther.URL, PricePerStep: 1, PriceUnit: "sat"},
	}

	tracker := newTestTracker(cfg, nil)
	throttleFor(t, tracker)
	if !tracker.IsPersistentlyThrottled(srv) {
		t.Fatal("precondition: the mint must be persistently throttled")
	}

	throttled = false
	tracker.RunProactiveCheck()

	if tracker.IsPersistentlyThrottled(srv) {
		t.Fatal("one OK probe did not re-admit the mint: the demotion must clear immediately, with no recovery threshold")
	}
	advertised := tracker.GetAdvertisedMintConfigs()
	if len(advertised) != 2 {
		t.Fatalf("advertised mints = %+v; want both mints once the throttled one answers again", advertised)
	}
}

// The counterpart guard: a genuinely dead mint still leaves the reachable set
// after defaultFailureThreshold probes, and a non-429 failure is never counted as
// a throttle — otherwise the escape hatch would disable mint health tracking.
func TestNon429FailureStillDemotesAfterThreeProbes(t *testing.T) {
	var hits int32
	srv := throttledKeysetsServer(t, func() int { return http.StatusServiceUnavailable },
		func() string { return "" }, &hits)

	tracker := newTestTracker(mintConfigWithURLs(srv), nil)
	tracker.RunInitialProbe() // failure 1
	if tracker.IsReachable(srv) {
		t.Fatal("a 503 probe kept the mint reachable")
	}

	for i := 2; i < int(tracker.failureThreshold); i++ {
		tracker.RunProactiveCheck()
		if tracker.IsReachable(srv) {
			t.Fatalf("the mint became reachable again on failure %d", i)
		}
	}
	tracker.RunProactiveCheck() // failure N

	if tracker.IsPersistentlyThrottled(srv) {
		t.Fatal("a 503 was recorded as a throttle: a transport failure is not a rate limit")
	}
	if tracker.IsReachable(srv) {
		t.Fatalf("the mint is still reachable after %d consecutive failed probes", tracker.failureThreshold)
	}
}

// countingBalanceWallet records how many times the payout path asked for a
// mint's balance, which is processPayout's first act — a mint the payout routine
// skips is never asked about at all.
type countingBalanceWallet struct {
	tollwallet.WalletPort
	balance uint64
	lookups int32
}

func (w *countingBalanceWallet) GetBalanceByMint(string) uint64 {
	atomic.AddInt32(&w.lookups, 1)
	return w.balance
}

// The payout routine skips a persistently throttled mint: a mint that
// rate-limits every health probe will rate-limit a melt too, and the value stays
// in the wallet for when the mint comes back.
func TestPayoutRoutineSkipsPersistentlyThrottledMint(t *testing.T) {
	var hits int32
	srv := throttledKeysetsServer(t, func() int { return http.StatusTooManyRequests },
		func() string { return "" }, &hits)

	tracker := newTestTracker(mintConfigWithURLs(srv), nil)
	throttleFor(t, tracker)
	if !tracker.IsPersistentlyThrottled(srv) {
		t.Fatal("precondition: the mint must be persistently throttled")
	}
	if !tracker.IsReachable(srv) {
		t.Fatal("precondition: the demoted mint must stay reachable")
	}

	wallet := &countingBalanceWallet{}
	m := &Merchant{
		config:            &config_manager.Config{},
		mintHealthTracker: tracker,
		tollwallet:        wallet,
	}

	m.runPayoutForMint(config_manager.MintConfig{URL: srv, MinPayoutAmount: 1, MinBalance: 0})

	if got := atomic.LoadInt32(&wallet.lookups); got != 0 {
		t.Fatalf("the payout routine touched the persistently throttled mint %d time(s): a mint that answers 429 to every probe is not going to melt", got)
	}

	// A reachable, non-throttled mint must still be looked at, so the skip
	// above is a real decision and not the payout routine being inert.
	srvOther := reachableServer(t)
	other := newTestTracker(mintConfigWithURLs(srvOther.URL), nil)
	other.RunInitialProbe()
	walletOther := &countingBalanceWallet{}
	mOther := &Merchant{
		config:            &config_manager.Config{},
		mintHealthTracker: other,
		tollwallet:        walletOther,
	}
	mOther.runPayoutForMint(config_manager.MintConfig{URL: srvOther.URL, MinPayoutAmount: 1, MinBalance: 0})

	if got := atomic.LoadInt32(&walletOther.lookups); got != 1 {
		t.Fatalf("a reachable, healthy mint was looked at %d time(s); want 1 — the skip must be specific to the throttled mint", got)
	}
}
