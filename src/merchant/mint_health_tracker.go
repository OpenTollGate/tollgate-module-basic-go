package merchant

import (
	"encoding/json"
	"log"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
)

const (
	defaultRecoveryThreshold uint8 = 3
	probeTimeout                   = 30 * time.Second
	probeInterval                  = 5 * time.Minute

	// A mint leaves the reachable set only after this many consecutive failed
	// probes, symmetric with recoveryThreshold on the other side. Recovery
	// already required 3 successes; the failure side required 1, so a single
	// bad probe removed the mint — and on a single-mint deployment that empties
	// the set and stops sales. With the 5-minute cadence the trade is explicit:
	// a genuinely dead mint means up to ~10 minutes of failed purchases, which
	// is the price of never stopping sales because of one transient answer.
	defaultFailureThreshold uint8 = 3

	// persistentThrottleProbes is the number of consecutive throttled probes
	// (HTTP 429 on /v1/keysets) after which a mint is treated as *persistently*
	// throttled and taken out of the advertisement. This is the escape hatch for
	// the gap the "a mint that answers 429 is busy, not broken" fix left open: a
	// front that answers 429 to everything makes the mint reachable for ever, so
	// it stays advertised, the customer's client keeps picking it out of
	// price_per_step, every purchase fails, and nothing self-heals — the
	// aggressive 15-second probe mode only arms when the reachable set is EMPTY.
	//
	// 6 probes at the 5-minute proactive cadence is a ~30-minute window with no
	// successful probe. That is the bottom of the 30-60 minute range the escape
	// hatch was specified with, chosen so: (a) a burst of 429 — the thing the
	// merchant's own outbound budget, mintQuoteBudget, exists to absorb — is over
	// long before it, (b) half an hour of "this mint cannot serve a single
	// customer" is well past the point where an on-router reseller should keep
	// offering it, and (c) it is short enough that a mint that comes back is
	// re-admitted the moment it answers, so the cost of being wrong is bounded by
	// next probe. The streak resets on any OK probe, so it *is* the
	// no-success window. Overridable per router with
	// TOLLGATE_PERSISTENT_THROTTLE_PROBES (this file's knobs are per-instance
	// fields so tests can shorten them; the environment is read once, in the
	// constructor).
	defaultPersistentThrottleProbes uint8 = 6

	// retryAfterCap is the default ceiling on how long a mint's own Retry-After
	// may hold our probes off. The header is untrusted input: a front answering
	// "Retry-After: 86400" must not be able to stop us re-probing the mint for
	// ever, or the persistently-throttled state it reports could never clear. The
	// cap is a multiple of the proactive cadence (6 x 5 minutes) rather than a
	// fraction of it, so an honest Retry-After is honoured in full at the default
	// cadence while a bogus one still lets us re-learn the mint's state within
	// half an hour. Overridable per router with
	// TOLLGATE_RETRY_AFTER_CAP_SECONDS.
	defaultRetryAfterCap = 30 * time.Minute

	// Aggressive retry: when no mints are reachable at startup (e.g. WiFi STA
	// not yet connected) OR after a runtime downgrade to degraded mode, probe
	// every 15s with immediate recovery (threshold=1) for up to 5 minutes.
	// This complements the OpenWrt hotplug script that restarts tollgate when
	// the wwan interface comes up, and keeps a transient mint blip from
	// stranding the service in degraded mode for a whole proactive cycle
	// (~13 min observed in the field, #429). The live values live on the
	// tracker struct (per-instance, settable before any loop starts) so tests
	// can shorten them without package-level mutable state.
	aggressiveProbeInterval = 15 * time.Second
	aggressiveProbeTimeout  = 10 * time.Second
	aggressiveDuration      = 5 * time.Minute
)

type mintConfigProvider interface {
	GetConfig() *config_manager.Config
}

type MintHealthTracker struct {
	mu                    sync.RWMutex
	reachableMints        map[string]bool
	consecutiveSuccesses  map[string]uint8
	consecutiveFailures   map[string]uint8
	httpClient            *http.Client
	configProvider        mintConfigProvider
	recoveryThreshold     uint8
	failureThreshold      uint8
	onFirstReachable      func()
	hadReachableMint      bool
	onReachableSetChanged func()
	reachableCount        int
	stopCh                chan struct{}
	aggressiveArmed       bool
	aggressiveInterval    time.Duration
	aggressiveTimeout     time.Duration
	aggressiveWindow      time.Duration

	// The throttle side of the same question. A 429 is reachable evidence, so it
	// never counts as a failure; these fields record what it *does* mean — that
	// the mint is serving nobody — so a front that answers 429 for ever can be
	// taken out of the advertisement without pretending the mint is down.
	//
	// consecutiveThrottles is reset by any OK probe, which makes a running streak
	// a "throttled on every probe" window by construction. throttleStreakStart is
	// when that window opened and lastSuccess is the last probe the mint served,
	// so the demotion can be logged with the window it is based on.
	consecutiveThrottles     map[string]uint8
	throttleStreakStart      map[string]time.Time
	lastSuccess              map[string]time.Time
	persistentlyThrottled    map[string]bool
	persistentThrottleProbes uint8

	// retryAfterCap is how long a mint's own Retry-After may hold our probes off
	// (see defaultRetryAfterCap). A field rather than a constant read at the call
	// site so a router can tune it and a test can shorten it — the same shape the
	// aggressive-retry timings use.
	retryAfterCap time.Duration

	// nextProbeAfter holds the not-before time a mint's own Retry-After asked
	// for, so a 429 that says "come back in ten minutes" is not answered with a
	// probe every five. It is keyed by the configured mint URL, so it is bounded
	// by the config.
	nextProbeAfter map[string]time.Time

	// clock is the tracker's notion of now. Production leaves it at time.Now; a
	// test replaces it so a Retry-After window is driven without sleeping.
	clock func() time.Time
}

func NewMintHealthTracker(configProvider mintConfigProvider) *MintHealthTracker {
	// The two escape-hatch values are overridable per router (see the constants
	// above). A nonsensical override degrades to the documented default rather
	// than wrapping: envIntOr already refuses a non-positive one, and a probe
	// count above the counter's range would otherwise cast to something small and
	// demote a mint almost immediately.
	probes := envIntOr("TOLLGATE_PERSISTENT_THROTTLE_PROBES", int(defaultPersistentThrottleProbes))
	if probes > math.MaxUint8 {
		probes = int(defaultPersistentThrottleProbes)
	}
	capSeconds := envIntOr("TOLLGATE_RETRY_AFTER_CAP_SECONDS", int(defaultRetryAfterCap/time.Second))

	return &MintHealthTracker{
		reachableMints:       make(map[string]bool),
		consecutiveSuccesses: make(map[string]uint8),
		consecutiveFailures:  make(map[string]uint8),
		httpClient: &http.Client{
			Timeout: probeTimeout,
		},
		configProvider:    configProvider,
		recoveryThreshold: defaultRecoveryThreshold,
		failureThreshold:  defaultFailureThreshold,

		consecutiveThrottles:     make(map[string]uint8),
		throttleStreakStart:      make(map[string]time.Time),
		lastSuccess:              make(map[string]time.Time),
		persistentlyThrottled:    make(map[string]bool),
		persistentThrottleProbes: uint8(probes),
		nextProbeAfter:           make(map[string]time.Time),
		retryAfterCap:            time.Duration(capSeconds) * time.Second,

		clock: time.Now,

		aggressiveInterval: aggressiveProbeInterval,
		aggressiveTimeout:  aggressiveProbeTimeout,
		aggressiveWindow:   aggressiveDuration,
	}
}

// now is the tracker's clock, with a nil guard so a zero-value tracker built in
// a test still works.
func (t *MintHealthTracker) now() time.Time {
	if t.clock == nil {
		return time.Now()
	}
	return t.clock()
}

func (t *MintHealthTracker) StartProactiveChecks() {
	t.mu.Lock()
	if t.stopCh != nil {
		t.mu.Unlock()
		return
	}
	t.stopCh = make(chan struct{})
	stopCh := t.stopCh
	needAggressive := t.reachableCount == 0
	t.mu.Unlock()

	go func() {
		if needAggressive {
			log.Printf("StartProactiveChecks: no reachable mints at startup — arming aggressive retry")
			t.ArmAggressiveRetry()
		}

		ticker := time.NewTicker(probeInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				t.runProactiveCheck()
			case <-stopCh:
				return
			}
		}
	}()
}

// ArmAggressiveRetry starts the aggressive (15 s) probe loop on a tracker
// that is already running proactive checks. Armed on the runtime downgrade
// path (#429): without it, recovery from a transient mint blip waits for
// the next 5-minute proactive cycle — a ~13-minute stuck-degraded window
// was observed live. On success the aggressive check fires the same
// first-reachable and set-changed callbacks as the proactive check, so a
// wired recovery trigger (see MerchantDegraded.WireRecoveryTrigger) fires
// within seconds. Idempotent: a second call while armed is a no-op.
func (t *MintHealthTracker) ArmAggressiveRetry() {
	t.mu.Lock()
	if t.stopCh == nil {
		t.mu.Unlock()
		log.Printf("ArmAggressiveRetry: proactive checks not running — nothing to arm")
		return
	}
	if t.aggressiveArmed {
		t.mu.Unlock()
		return
	}
	t.aggressiveArmed = true
	stopCh := t.stopCh
	t.mu.Unlock()

	done := t.runAggressiveRetry(stopCh)
	go func() {
		<-done
		t.mu.Lock()
		t.aggressiveArmed = false
		t.mu.Unlock()
	}()
}

func (t *MintHealthTracker) runAggressiveRetry(stopCh chan struct{}) chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		aggressiveClient := &http.Client{Timeout: t.aggressiveTimeout}
		ticker := time.NewTicker(t.aggressiveInterval)
		defer ticker.Stop()
		timer := time.NewTimer(t.aggressiveWindow)
		defer timer.Stop()

		for {
			select {
			case <-ticker.C:
				if t.runAggressiveCheck(aggressiveClient) {
					log.Printf("runAggressiveRetry: mint became reachable, stopping aggressive mode")
					return
				}
			case <-timer.C:
				log.Printf("runAggressiveRetry: aggressive period ended (%v), falling back to normal interval", t.aggressiveWindow)
				return
			case <-stopCh:
				return
			}
		}
	}()
	return done
}

func (t *MintHealthTracker) Stop() {
	t.mu.Lock()
	if t.stopCh != nil {
		close(t.stopCh)
		t.stopCh = nil
	}
	t.mu.Unlock()
}

func (t *MintHealthTracker) IsReachable(mintURL string) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.reachableMints[mintURL]
}

func (t *MintHealthTracker) GetReachableMintConfigs() []config_manager.MintConfig {
	config := t.configProvider.GetConfig()
	if config == nil {
		return nil
	}

	t.mu.RLock()
	defer t.mu.RUnlock()

	var reachable []config_manager.MintConfig
	for _, mint := range config.AcceptedMints {
		if t.reachableMints[mint.URL] {
			reachable = append(reachable, mint)
		}
	}
	return reachable
}

func (t *MintHealthTracker) GetAllConfiguredMintConfigs() []config_manager.MintConfig {
	config := t.configProvider.GetConfig()
	if config == nil {
		return nil
	}
	return config.AcceptedMints
}

// IsPersistentlyThrottled reports whether mintURL is reachable but has answered
// 429 to every probe for the persistent window (see
// defaultPersistentThrottleProbes). Such a mint is up and must stay in the
// reachable set, in the wallet and in the registered mints — it is only kept out
// of the advertisement and off the payout path, both of which are decided where
// the customer's client picks a mint and where value leaves the wallet.
func (t *MintHealthTracker) IsPersistentlyThrottled(mintURL string) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.persistentlyThrottled[mintURL]
}

// GetAdvertisedMintConfigs returns the mints a customer may pay with: the
// reachable set minus the persistently throttled mints, so a reseller router
// stops offering a mint whose front answers 429 to everything. The wallet and
// the reachable set are untouched — the mint is still up, and the customer's
// e-cash is still held there.
//
// The advertisement is never emptied: if dropping the persistently throttled
// mints would leave nothing to advertise, the reachable set is returned
// unchanged. A customer with no alternative is better served by a busy mint than
// by an empty advertisement — and the router cannot substitute a mint
// mid-purchase anyway, because the invoice must come from the mint that holds
// the customer's ecash.
func (t *MintHealthTracker) GetAdvertisedMintConfigs() []config_manager.MintConfig {
	config := t.configProvider.GetConfig()
	if config == nil {
		return nil
	}

	t.mu.RLock()
	defer t.mu.RUnlock()

	reachable := make([]config_manager.MintConfig, 0, len(config.AcceptedMints))
	advertised := make([]config_manager.MintConfig, 0, len(config.AcceptedMints))
	for _, mint := range config.AcceptedMints {
		if !t.reachableMints[mint.URL] {
			continue
		}
		reachable = append(reachable, mint)
		if !t.persistentlyThrottled[mint.URL] {
			advertised = append(advertised, mint)
		}
	}

	if len(advertised) == 0 {
		return reachable
	}
	return advertised
}

// probeDue reports whether a mint may be probed now, given the wait it asked for
// with the last Retry-After it sent. A skipped probe learns nothing: the caller
// must leave every counter for that mint untouched.
func (t *MintHealthTracker) probeDue(mintURL string, now time.Time) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()

	notBefore, ok := t.nextProbeAfter[mintURL]
	return !ok || !now.Before(notBefore)
}

func (t *MintHealthTracker) MarkUnreachable(mintURL string) {
	t.mu.Lock()

	// A previously-reachable mint going down changes the reachable set: the
	// callback must fire here (#401), or the probe path's setChanged
	// comparison later runs against this already-updated count and the
	// degraded-mode transition is silently suppressed for the rest of the
	// outage whenever a payment observed it first.
	fireSetChanged := t.reachableMints[mintURL]
	if fireSetChanged {
		t.reachableCount--
	}
	t.reachableMints[mintURL] = false
	t.consecutiveSuccesses[mintURL] = 0

	var callback func()
	if fireSetChanged && t.onReachableSetChanged != nil {
		callback = t.onReachableSetChanged
	}
	t.mu.Unlock()

	if callback != nil {
		log.Printf("MarkUnreachable: reachable set changed (mint=%s), firing callback", mintURL)
		go callback()
	}
}

// SetOnFirstReachableForDegraded registers a callback that fires once when a mint
// becomes reachable after starting with none. The hadReachableMint flag is reset to
// false so the callback fires on the first mint recovery — this is only meaningful
// for the degraded merchant path which starts with all mints unreachable.
func (t *MintHealthTracker) SetOnFirstReachableForDegraded(callback func()) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.onFirstReachable = callback
	t.hadReachableMint = false
}

func (t *MintHealthTracker) SetOnReachableSetChanged(callback func()) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.onReachableSetChanged = callback
}

func (t *MintHealthTracker) RunInitialProbe() {
	config := t.configProvider.GetConfig()
	if config == nil {
		return
	}

	now := t.now()
	log.Printf("RunInitialProbe: probing %d mint(s)", len(config.AcceptedMints))
	results := make(map[string]probeOutcome, len(config.AcceptedMints))
	waits := make(map[string]time.Duration, len(config.AcceptedMints))
	for _, mint := range config.AcceptedMints {
		results[mint.URL], waits[mint.URL] = t.probeMintOutcome(mint.URL, nil)
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	for url, outcome := range results {
		if outcome.reachable() {
			t.reachableMints[url] = true
			t.consecutiveSuccesses[url] = t.recoveryThreshold
			t.consecutiveFailures[url] = 0
		} else {
			t.reachableMints[url] = false
			t.consecutiveSuccesses[url] = 0
			t.consecutiveFailures[url] = 1
		}
		// Only the throttle bookkeeping: the reachability counters above are
		// seeded by this probe itself, so folding the same answer into them a
		// second time would shift both thresholds by one.
		t.applyThrottleAnswerLocked(url, outcome, waits[url], now)
	}

	t.reachableCount = 0
	for _, mint := range config.AcceptedMints {
		if t.reachableMints[mint.URL] {
			t.hadReachableMint = true
			t.reachableCount++
		}
	}
}

func (t *MintHealthTracker) RunProactiveCheck() {
	t.runProactiveCheck()
}

func (t *MintHealthTracker) runProactiveCheck() {
	config := t.configProvider.GetConfig()
	if config == nil {
		return
	}

	now := t.now()
	log.Printf("runProactiveCheck: probing %d mint(s)", len(config.AcceptedMints))
	results := make(map[string]probeOutcome, len(config.AcceptedMints))
	waits := make(map[string]time.Duration, len(config.AcceptedMints))
	// skipped holds the mints whose front told us to wait: they are not probed
	// and therefore learn nothing, so every counter for them must stay put
	// rather than being advanced on a probe that never happened.
	skipped := make(map[string]bool, len(config.AcceptedMints))
	for _, mint := range config.AcceptedMints {
		if !t.probeDue(mint.URL, now) {
			log.Printf("runProactiveCheck: mint=%s is inside the wait its last Retry-After asked for — not probed", mint.URL)
			skipped[mint.URL] = true
			continue
		}
		results[mint.URL], waits[mint.URL] = t.probeMintOutcome(mint.URL, nil)
	}

	t.mu.Lock()

	var demoted []string
	var readmitted []string

	for _, mint := range config.AcceptedMints {
		if skipped[mint.URL] {
			continue
		}

		demotedNow, readmittedNow := t.applyThrottleAnswerLocked(mint.URL, results[mint.URL], waits[mint.URL], now)
		if demotedNow {
			demoted = append(demoted, mint.URL)
		}
		if readmittedNow {
			readmitted = append(readmitted, mint.URL)
		}

		// A throttled answer (HTTP 429) is not a failure: the mint is up and
		// rate-limiting us, which is information about load, not availability.
		// It counts as reachable evidence and never contributes to the
		// consecutive-failure count, so a local flood that drives the mint to
		// 429 cannot trip the merchant's self-downgrade.
		if results[mint.URL].reachable() {
			t.consecutiveSuccesses[mint.URL]++
			t.consecutiveFailures[mint.URL] = 0

			if !t.reachableMints[mint.URL] && t.consecutiveSuccesses[mint.URL] >= t.recoveryThreshold {
				t.reachableMints[mint.URL] = true
			}
		} else {
			t.consecutiveSuccesses[mint.URL] = 0
			failures := t.consecutiveFailures[mint.URL] + 1
			t.consecutiveFailures[mint.URL] = failures
			if failures >= t.failureThreshold {
				t.reachableMints[mint.URL] = false
			}
		}
	}

	newCount := 0
	for _, mint := range config.AcceptedMints {
		if t.reachableMints[mint.URL] {
			newCount++
		}
	}

	setChanged := newCount != t.reachableCount
	t.reachableCount = newCount

	var callbacks []func()

	if !t.hadReachableMint && t.onFirstReachable != nil {
		for _, mint := range config.AcceptedMints {
			if t.reachableMints[mint.URL] {
				t.hadReachableMint = true
				callbacks = append(callbacks, t.onFirstReachable)
				break
			}
		}
	}

	if setChanged && t.onReachableSetChanged != nil {
		callbacks = append(callbacks, t.onReachableSetChanged)
	}

	t.mu.Unlock()

	for _, cb := range callbacks {
		log.Printf("runProactiveCheck: firing callback (hadReachable=%v, setChanged=%v)", t.hadReachableMint, setChanged)
		go cb()
	}

	for _, mintURL := range demoted {
		log.Printf("runProactiveCheck: mint=%s answered 429 to %d consecutive probes with no successful probe in that window — removing it from the advertisement; it stays reachable, registered and in the wallet, and the first successful probe re-admits it",
			mintURL, t.persistentThrottleProbes)
	}
	for _, mintURL := range readmitted {
		log.Printf("runProactiveCheck: mint=%s answered a probe again — re-admitted to the advertisement", mintURL)
	}
}

// runAggressiveCheck probes mints with immediate recovery (threshold=1).
// Returns true if a previously-unreachable mint became reachable.
func (t *MintHealthTracker) runAggressiveCheck(aggressiveClient *http.Client) bool {
	config := t.configProvider.GetConfig()
	if config == nil {
		return false
	}

	now := t.now()
	log.Printf("runAggressiveCheck: probing %d mint(s) with immediate recovery", len(config.AcceptedMints))
	results := make(map[string]probeOutcome, len(config.AcceptedMints))
	waits := make(map[string]time.Duration, len(config.AcceptedMints))
	skipped := make(map[string]bool, len(config.AcceptedMints))
	for _, mint := range config.AcceptedMints {
		// The wait a mint asked for is honoured here too: this loop exists to
		// recover from an empty reachable set, not to defeat a mint's own
		// backpressure. A skipped mint is not probed, so it cannot be the one
		// that reports recovery.
		if !t.probeDue(mint.URL, now) {
			log.Printf("runAggressiveCheck: mint=%s is inside the wait its last Retry-After asked for — not probed", mint.URL)
			skipped[mint.URL] = true
			continue
		}
		results[mint.URL], waits[mint.URL] = t.probeMintOutcome(mint.URL, aggressiveClient)
	}

	t.mu.Lock()

	recovered := false
	for _, mint := range config.AcceptedMints {
		if skipped[mint.URL] {
			continue
		}
		t.applyThrottleAnswerLocked(mint.URL, results[mint.URL], waits[mint.URL], now)
		// Throttled (429) counts as reachable here too: this loop only runs
		// while nothing is reachable, and a mint that answers 429 is up.
		if results[mint.URL].reachable() {
			t.consecutiveSuccesses[mint.URL]++
			t.consecutiveFailures[mint.URL] = 0
			if !t.reachableMints[mint.URL] {
				t.reachableMints[mint.URL] = true
				recovered = true
			}
		} else {
			t.consecutiveSuccesses[mint.URL] = 0
			t.consecutiveFailures[mint.URL]++
			// Nothing to downgrade: aggressive mode only runs when the
			// reachable set is empty, so a failure here keeps the status quo.
		}
	}

	newCount := 0
	for _, mint := range config.AcceptedMints {
		if t.reachableMints[mint.URL] {
			newCount++
		}
	}

	setChanged := newCount != t.reachableCount
	t.reachableCount = newCount

	var callbacks []func()

	if !t.hadReachableMint && t.onFirstReachable != nil {
		for _, mint := range config.AcceptedMints {
			if t.reachableMints[mint.URL] {
				t.hadReachableMint = true
				callbacks = append(callbacks, t.onFirstReachable)
				break
			}
		}
	}

	if setChanged && t.onReachableSetChanged != nil {
		callbacks = append(callbacks, t.onReachableSetChanged)
	}

	t.mu.Unlock()

	for _, cb := range callbacks {
		log.Printf("runAggressiveCheck: firing callback (hadReachable=%v, setChanged=%v)", t.hadReachableMint, setChanged)
		go cb()
	}

	return recovered
}

// probeOutcome classifies what a probe learned about a mint. The distinction
// between "the mint is broken" and "the mint is busy" is the whole point: a
// rate-limit answer proves the mint is UP, and treating it as an outage is what
// turns our own flood into a sales outage on a single-mint deployment.
type probeOutcome uint8

const (
	// probeOK: a 2xx answer carrying usable keysets.
	probeOK probeOutcome = iota
	// probeThrottled: HTTP 429 — reachable, but asking us to slow down.
	probeThrottled
	// probeFailed: no answer, a transport error, or a non-429 non-2xx status.
	probeFailed
)

// reachable reports whether this outcome is evidence that the mint is up.
func (o probeOutcome) reachable() bool { return o != probeFailed }

func (t *MintHealthTracker) probeMint(mintURL string) bool {
	outcome, _ := t.probeMintOutcome(mintURL, nil)
	return outcome.reachable()
}

// probeMintWith is the pre-existing boolean seam, kept as a wrapper for callers
// that only need "is it usable".
func (t *MintHealthTracker) probeMintWith(mintURL string, client *http.Client) bool {
	outcome, _ := t.probeMintOutcome(mintURL, client)
	return outcome.reachable()
}

// applyThrottleAnswerLocked folds the throttle half of one probe answer into the
// tracker state and reports whether this answer moved the mint out of, or back
// into, the advertisement. The caller holds t.mu. The reachability counters are
// deliberately not touched here: they are advanced by the caller, on the same
// answer, and a boot probe seeds them itself.
//
// The returned flags are for logging only — the advertisement is computed from
// this state on every read, so nothing has to be republished for a demotion to
// take effect.
//
// The three outcomes are deliberately asymmetric. A throttled probe advances the
// streak (the mint answered, but it served nobody); an OK probe clears the whole
// throttle history and re-admits immediately, with no recovery threshold; a
// failure is neither, so it leaves the streak alone rather than resetting it —
// a front that alternates 429 with a refusal has still served nobody, and
// letting a failure reset the streak would be a way to evade the escape hatch
// for ever. A failure has its own, stronger handling: it leaves the reachable
// set entirely after defaultFailureThreshold probes.
func (t *MintHealthTracker) applyThrottleAnswerLocked(mintURL string, outcome probeOutcome, wait time.Duration, now time.Time) (demoted, readmitted bool) {
	if wait > 0 {
		t.nextProbeAfter[mintURL] = now.Add(wait)
	} else {
		delete(t.nextProbeAfter, mintURL)
	}

	switch outcome {
	case probeThrottled:
		// Saturating at 255: the counter is a uint8 to mirror the reachability
		// counters, and a wrap would make a streak that is hours old look like a
		// fresh one.
		if t.consecutiveThrottles[mintURL] < math.MaxUint8 {
			t.consecutiveThrottles[mintURL]++
		}
		if t.throttleStreakStart[mintURL].IsZero() {
			t.throttleStreakStart[mintURL] = now
		}

		if t.persistentlyThrottled[mintURL] || t.persistentThrottleProbes == 0 {
			return false, false
		}
		if t.consecutiveThrottles[mintURL] < t.persistentThrottleProbes {
			return false, false
		}
		if !t.noSuccessSince(mintURL, t.throttleStreakStart[mintURL]) {
			return false, false
		}
		t.persistentlyThrottled[mintURL] = true
		return true, false

	case probeOK:
		t.consecutiveThrottles[mintURL] = 0
		t.throttleStreakStart[mintURL] = time.Time{}
		t.lastSuccess[mintURL] = now
		if t.persistentlyThrottled[mintURL] {
			t.persistentlyThrottled[mintURL] = false
			return false, true
		}
		return false, false

	default: // probeFailed
		return false, false
	}
}

// noSuccessSince reports whether the mint has served no successful probe since
// the given instant. The throttle streak is reset by every OK probe, so this
// holds by construction while a streak runs; it is checked explicitly so a
// future caller that records a success without resetting the streak cannot
// demote a mint that has just served us.
func (t *MintHealthTracker) noSuccessSince(mintURL string, since time.Time) bool {
	last := t.lastSuccess[mintURL]
	return last.IsZero() || last.Before(since)
}

// parseRetryAfter interprets a Retry-After header in either form RFC 7231
// allows: delta-seconds ("120") or an HTTP-date ("Wed, 21 Oct 2015 07:28:00
// GMT"), relative to now. A malformed, zero, negative or already-past value
// means "no wait" — never a negative one, and never an unbounded one: the result
// is clamped to the cap, because the header is untrusted input and a front
// answering "Retry-After: 86400" must not be able to silence a mint's probes for
// ever (the persistently-throttled state could then never clear).
func parseRetryAfter(value string, now time.Time, cap time.Duration) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}

	var wait time.Duration
	if seconds, err := strconv.Atoi(value); err == nil {
		if seconds <= 0 {
			return 0
		}
		wait = time.Duration(seconds) * time.Second
	} else if when, err := http.ParseTime(value); err == nil {
		wait = when.Sub(now)
		if wait <= 0 {
			return 0
		}
	} else {
		return 0
	}

	if wait > cap {
		wait = cap
	}
	return wait
}

// keysetsProbeResponse is the subset of the NUT-01 GET /v1/keysets response
// the health probe validates.
type keysetsProbeResponse struct {
	Keysets []json.RawMessage `json:"keysets"`
}

// probeMintOutcome probes the mint's keysets endpoint and classifies the answer,
// returning alongside it the wait the mint asked for when it answered 429 with a
// Retry-After (zero when it did not). A nil client means the tracker's own (see
// the constructor).
func (t *MintHealthTracker) probeMintOutcome(mintURL string, client *http.Client) (probeOutcome, time.Duration) {
	if client == nil {
		client = t.httpClient
	}

	// Probe /v1/keysets, not /v1/info: a mint is only usable for payments if it
	// serves its active keysets, and some fronts answer /v1/info with a 2xx HTML
	// page (parked/hosted error pages), which a status-only check wrongly treats
	// as healthy — leading to advertised mints whose token swaps then fail.
	url := strings.TrimRight(mintURL, "/") + "/v1/keysets"

	start := time.Now()
	resp, err := client.Get(url)
	elapsed := time.Since(start)
	if err != nil {
		log.Printf("mint probe FAILED: url=%s elapsed=%s error=%v", url, elapsed, err)
		return probeFailed, 0
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusTooManyRequests {
		// Busy, not broken: the mint answered, so it is up. It must not count
		// as a failure, or a flood against us becomes an outage for us. The
		// Retry-After is how the mint says how long it expects to be busy; it
		// is honoured on the probe path (see probeDue), clamped by
		// retryAfterCap so an untrusted value cannot silence the mint for ever.
		wait := parseRetryAfter(resp.Header.Get("Retry-After"), t.now(), t.retryAfterCap)
		log.Printf("mint probe: url=%s status=%d elapsed=%s outcome=throttled retry_after=%s (the mint is up and rate-limiting; not an outage)", url, resp.StatusCode, elapsed, wait)
		return probeThrottled, wait
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		log.Printf("mint probe: url=%s status=%d elapsed=%s ok=false", url, resp.StatusCode, elapsed)
		return probeFailed, 0
	}

	var body keysetsProbeResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil || len(body.Keysets) == 0 {
		log.Printf("mint probe: url=%s status=%d elapsed=%s ok=false reason=invalid-or-empty-keysets err=%v", url, resp.StatusCode, elapsed, err)
		return probeFailed, 0
	}

	log.Printf("mint probe: url=%s status=%d keysets=%d elapsed=%s ok=true", url, resp.StatusCode, len(body.Keysets), elapsed)
	return probeOK, 0
}
