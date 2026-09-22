package merchant

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
)

const (
	defaultRecoveryThreshold uint8 = 3
	probeTimeout                   = 30 * time.Second
	probeInterval                  = 5 * time.Minute

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
	httpClient            *http.Client
	configProvider        mintConfigProvider
	recoveryThreshold     uint8
	onFirstReachable      func()
	hadReachableMint      bool
	onReachableSetChanged func()
	reachableCount        int
	stopCh                chan struct{}
	aggressiveArmed       bool
	aggressiveInterval    time.Duration
	aggressiveTimeout     time.Duration
	aggressiveWindow      time.Duration
}

func NewMintHealthTracker(configProvider mintConfigProvider) *MintHealthTracker {
	return &MintHealthTracker{
		reachableMints:       make(map[string]bool),
		consecutiveSuccesses: make(map[string]uint8),
		httpClient: &http.Client{
			Timeout: probeTimeout,
		},
		configProvider:    configProvider,
		recoveryThreshold: defaultRecoveryThreshold,

		aggressiveInterval: aggressiveProbeInterval,
		aggressiveTimeout:  aggressiveProbeTimeout,
		aggressiveWindow:   aggressiveDuration,
	}
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

	log.Printf("RunInitialProbe: probing %d mint(s)", len(config.AcceptedMints))
	results := make(map[string]bool, len(config.AcceptedMints))
	for _, mint := range config.AcceptedMints {
		results[mint.URL] = t.probeMint(mint.URL)
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	for url, ok := range results {
		if ok {
			t.reachableMints[url] = true
			t.consecutiveSuccesses[url] = t.recoveryThreshold
		} else {
			t.reachableMints[url] = false
			t.consecutiveSuccesses[url] = 0
		}
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

	log.Printf("runProactiveCheck: probing %d mint(s)", len(config.AcceptedMints))
	results := make(map[string]bool, len(config.AcceptedMints))
	for _, mint := range config.AcceptedMints {
		results[mint.URL] = t.probeMint(mint.URL)
	}

	t.mu.Lock()

	for _, mint := range config.AcceptedMints {
		if results[mint.URL] {
			t.consecutiveSuccesses[mint.URL]++

			if !t.reachableMints[mint.URL] && t.consecutiveSuccesses[mint.URL] >= t.recoveryThreshold {
				t.reachableMints[mint.URL] = true
			}
		} else {
			t.consecutiveSuccesses[mint.URL] = 0
			t.reachableMints[mint.URL] = false
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
}

// runAggressiveCheck probes mints with immediate recovery (threshold=1).
// Returns true if a previously-unreachable mint became reachable.
func (t *MintHealthTracker) runAggressiveCheck(aggressiveClient *http.Client) bool {
	config := t.configProvider.GetConfig()
	if config == nil {
		return false
	}

	log.Printf("runAggressiveCheck: probing %d mint(s) with immediate recovery", len(config.AcceptedMints))
	results := make(map[string]bool, len(config.AcceptedMints))
	for _, mint := range config.AcceptedMints {
		results[mint.URL] = t.probeMintWith(mint.URL, aggressiveClient)
	}

	t.mu.Lock()

	recovered := false
	for _, mint := range config.AcceptedMints {
		if results[mint.URL] {
			t.consecutiveSuccesses[mint.URL]++
			if !t.reachableMints[mint.URL] {
				t.reachableMints[mint.URL] = true
				recovered = true
			}
		} else {
			t.consecutiveSuccesses[mint.URL] = 0
			t.reachableMints[mint.URL] = false
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

func (t *MintHealthTracker) probeMint(mintURL string) bool {
	return t.probeMintWith(mintURL, t.httpClient)
}

// keysetsProbeResponse is the subset of the NUT-01 GET /v1/keysets response
// the health probe validates.
type keysetsProbeResponse struct {
	Keysets []json.RawMessage `json:"keysets"`
}

func (t *MintHealthTracker) probeMintWith(mintURL string, client *http.Client) bool {
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
		return false
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		log.Printf("mint probe: url=%s status=%d elapsed=%s ok=false", url, resp.StatusCode, elapsed)
		return false
	}

	var body keysetsProbeResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil || len(body.Keysets) == 0 {
		log.Printf("mint probe: url=%s status=%d elapsed=%s ok=false reason=invalid-or-empty-keysets err=%v", url, resp.StatusCode, elapsed, err)
		return false
	}

	log.Printf("mint probe: url=%s status=%d keysets=%d elapsed=%s ok=true", url, resp.StatusCode, len(body.Keysets), elapsed)
	return true
}
