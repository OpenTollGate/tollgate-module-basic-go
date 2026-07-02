package merchant

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
)

const (
	defaultRecoveryThreshold uint8 = 3
	// lnRecoveryThreshold controls how many consecutive successful LN probe
	// responses are required before re-declaring a mint Lightning-capable after
	// an LN failure. It is lower than defaultRecoveryThreshold (3 for Cashu)
	// because Cashu failures block ALL payment methods — requiring a higher bar
	// for recovery. LN failures only affect one payment method (Lightning)
	// while Cashu payments continue working. A threshold of 2 provides noise
	// immunity against transient network blips (single-packet-loss false
	// positives) while still recovering faster than the Cashu path.
	lnRecoveryThreshold uint8 = 2
	probeTimeout              = 30 * time.Second
	probeInterval             = 5 * time.Minute

	// Aggressive retry: when no mints are reachable at startup (e.g. WiFi STA
	// not yet connected), probe every 15s with immediate recovery (threshold=1)
	// for up to 5 minutes. This complements the OpenWrt hotplug script that
	// restarts tollgate when the wwan interface comes up.
	aggressiveProbeInterval = 15 * time.Second
	aggressiveProbeTimeout  = 10 * time.Second
	aggressiveDuration      = 5 * time.Minute

	// Lightning capability probe. We verify a mint's LN backend is actually
	// working by requesting a minimal 1-sat mint quote (NUT-04). The mint's
	// /v1/info only advertises protocol-level NUT-04 support — it does NOT tell
	// us whether the backing Lightning node (e.g. coinos.io) is reachable, so a
	// real quote request is the only reliable signal. A 1-sat invoice is the
	// smallest side effect that proves end-to-end LN availability.
	lnProbeTimeout  = 15 * time.Second
	lnProbeAmount   = 1
	lnQuoteEndpoint = "/v1/mint/quote/bolt11"
)

type mintConfigProvider interface {
	GetConfig() *config_manager.Config
}

type MintHealthTracker struct {
	mu                     sync.RWMutex
	reachableMints         map[string]bool
	supportsLN             map[string]bool
	consecutiveSuccesses   map[string]uint8
	lnConsecutiveSuccesses map[string]uint8
	httpClient             *http.Client
	lnProbeClient          *http.Client
	configProvider         mintConfigProvider
	recoveryThreshold      uint8
	onFirstReachable       func()
	hadReachableMint       bool
	onReachableSetChanged  func()
	reachableCount         int
	stopCh                 chan struct{}
}

func NewMintHealthTracker(configProvider mintConfigProvider) *MintHealthTracker {
	return &MintHealthTracker{
		reachableMints:         make(map[string]bool),
		supportsLN:             make(map[string]bool),
		consecutiveSuccesses:   make(map[string]uint8),
		lnConsecutiveSuccesses: make(map[string]uint8),
		httpClient: &http.Client{
			Timeout: probeTimeout,
		},
		lnProbeClient: &http.Client{
			Timeout: lnProbeTimeout,
		},
		configProvider:    configProvider,
		recoveryThreshold: defaultRecoveryThreshold,
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
		var aggressiveDone chan struct{}
		if needAggressive {
			log.Printf("StartProactiveChecks: starting aggressive retry (no reachable mints at startup)")
			aggressiveDone = t.runAggressiveRetry(stopCh)
			go func() {
				<-aggressiveDone
				log.Printf("StartProactiveChecks: aggressive retry completed")
			}()
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

func (t *MintHealthTracker) runAggressiveRetry(stopCh chan struct{}) chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		aggressiveClient := &http.Client{Timeout: aggressiveProbeTimeout}
		ticker := time.NewTicker(aggressiveProbeInterval)
		defer ticker.Stop()
		timer := time.NewTimer(aggressiveDuration)
		defer timer.Stop()

		for {
			select {
			case <-ticker.C:
				if t.runAggressiveCheck(aggressiveClient) {
					log.Printf("runAggressiveRetry: mint became reachable, stopping aggressive mode")
					return
				}
			case <-timer.C:
				log.Printf("runAggressiveRetry: aggressive period ended (%v), falling back to normal interval", aggressiveDuration)
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

// SupportsLN reports whether a mint's Lightning backend was verified working
// during the most recent probe. It is only meaningful for reachable mints; an
// unreachable mint is always Lightning-incapable. Lightning capability is
// probed by requesting a minimal mint quote (NUT-04), which exercises the
// mint's backing Lightning node (e.g. coinos.io) end-to-end.
func (t *MintHealthTracker) SupportsLN(mintURL string) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.supportsLN[mintURL]
}

// MarkLNUnavailable reactively degrades a mint's Lightning capability without
// affecting its reachability. Call this when a real invoice request fails at
// runtime (e.g. the mint returned an error mid-purchase) so Lightning is no
// longer advertised until the next proactive probe re-verifies it.
func (t *MintHealthTracker) MarkLNUnavailable(mintURL string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.supportsLN[mintURL] {
		log.Printf("MarkLNUnavailable: degrading Lightning capability for mint %s (reactive)", mintURL)
	}
	t.supportsLN[mintURL] = false
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
	defer t.mu.Unlock()

	if t.reachableMints[mintURL] {
		t.reachableCount--
	}
	t.reachableMints[mintURL] = false
	t.consecutiveSuccesses[mintURL] = 0
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
	reachable := make(map[string]bool, len(config.AcceptedMints))
	lnSupported := make(map[string]bool, len(config.AcceptedMints))
	for _, mint := range config.AcceptedMints {
		ok := t.probeMint(mint.URL)
		reachable[mint.URL] = ok
		// Only reachable mints can be Lightning-capable; probing LN for a mint
		// we can't even reach would just add latency with no signal.
		if ok {
			lnSupported[mint.URL] = t.probeLightningCapability(mint.URL, t.lnProbeClient)
		}
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	for url, ok := range reachable {
		if ok {
			t.reachableMints[url] = true
			t.consecutiveSuccesses[url] = t.recoveryThreshold
		} else {
			t.reachableMints[url] = false
			t.consecutiveSuccesses[url] = 0
		}
	}

	for url, lnOK := range lnSupported {
		if !reachable[url] {
			t.supportsLN[url] = false
			t.lnConsecutiveSuccesses[url] = 0
			continue
		}
		t.supportsLN[url] = lnOK
		if lnOK {
			t.lnConsecutiveSuccesses[url] = lnRecoveryThreshold
			log.Printf("RunInitialProbe: mint %s supports Lightning", url)
		} else {
			t.lnConsecutiveSuccesses[url] = 0
			log.Printf("RunInitialProbe: mint %s Lightning backend DEGRADED (reachable but LN quote probe failed) — Lightning will not be advertised", url)
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
	reachable := make(map[string]bool, len(config.AcceptedMints))
	lnSupported := make(map[string]bool, len(config.AcceptedMints))
	for _, mint := range config.AcceptedMints {
		ok := t.probeMint(mint.URL)
		reachable[mint.URL] = ok
		if ok {
			lnSupported[mint.URL] = t.probeLightningCapability(mint.URL, t.lnProbeClient)
		}
	}

	t.mu.Lock()

	for _, mint := range config.AcceptedMints {
		if reachable[mint.URL] {
			t.consecutiveSuccesses[mint.URL]++

			if !t.reachableMints[mint.URL] && t.consecutiveSuccesses[mint.URL] >= t.recoveryThreshold {
				t.reachableMints[mint.URL] = true
			}
		} else {
			t.consecutiveSuccesses[mint.URL] = 0
			t.reachableMints[mint.URL] = false
		}
	}

	// Refresh Lightning capability. A mint is only LN-capable when it has
	// accumulated lnRecoveryThreshold consecutive successful LN probes, providing
	// noise immunity against transient network blips. If the LN probe fails the
	// counter resets and Lightning is degraded immediately.
	for _, mint := range config.AcceptedMints {
		if !t.reachableMints[mint.URL] {
			t.supportsLN[mint.URL] = false
			t.lnConsecutiveSuccesses[mint.URL] = 0
			continue
		}
		wasLN := t.supportsLN[mint.URL]
		nowLN := lnSupported[mint.URL]
		if nowLN {
			t.lnConsecutiveSuccesses[mint.URL]++
		} else {
			t.lnConsecutiveSuccesses[mint.URL] = 0
		}
		t.supportsLN[mint.URL] = t.lnConsecutiveSuccesses[mint.URL] >= lnRecoveryThreshold
		if t.supportsLN[mint.URL] && !wasLN {
			log.Printf("runProactiveCheck: mint %s Lightning backend recovered — Lightning re-advertised", mint.URL)
		} else if !t.supportsLN[mint.URL] && wasLN {
			log.Printf("runProactiveCheck: mint %s Lightning backend DEGRADED (reachable but LN quote probe failed) — Lightning will not be advertised", mint.URL)
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
	reachable := make(map[string]bool, len(config.AcceptedMints))
	lnSupported := make(map[string]bool, len(config.AcceptedMints))
	for _, mint := range config.AcceptedMints {
		ok := t.probeMintWith(mint.URL, aggressiveClient)
		reachable[mint.URL] = ok
		if ok {
			lnSupported[mint.URL] = t.probeLightningCapability(mint.URL, t.lnProbeClient)
		}
	}

	t.mu.Lock()

	recovered := false
	for _, mint := range config.AcceptedMints {
		if reachable[mint.URL] {
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

	for _, mint := range config.AcceptedMints {
		if !t.reachableMints[mint.URL] {
			t.supportsLN[mint.URL] = false
			t.lnConsecutiveSuccesses[mint.URL] = 0
			continue
		}
		t.supportsLN[mint.URL] = lnSupported[mint.URL]
		if lnSupported[mint.URL] {
			t.lnConsecutiveSuccesses[mint.URL] = lnRecoveryThreshold
		} else {
			t.lnConsecutiveSuccesses[mint.URL] = 0
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

func (t *MintHealthTracker) probeMintWith(mintURL string, client *http.Client) bool {
	url := strings.TrimRight(mintURL, "/") + "/v1/info"

	start := time.Now()
	resp, err := client.Get(url)
	elapsed := time.Since(start)
	if err != nil {
		log.Printf("mint probe FAILED: url=%s elapsed=%s error=%v", url, elapsed, err)
		return false
	}
	defer resp.Body.Close()

	ok := resp.StatusCode >= 200 && resp.StatusCode < 300
	log.Printf("mint probe: url=%s status=%d elapsed=%s ok=%v", url, resp.StatusCode, elapsed, ok)
	return ok
}

// probeLightningCapability verifies that a mint can actually issue a Lightning
// invoice by requesting a minimal mint quote (Cashu NUT-04,
// POST /v1/mint/quote/bolt11). This exercises the mint's backing Lightning
// node end-to-end — a mint whose /v1/info is healthy but whose LN backend
// (e.g. coinos.io) is down will fail here, letting us withhold Lightning as a
// payment option instead of failing silently at purchase time.
//
// The probe creates a real 1-sat invoice on the mint; that is the smallest
// side effect that proves end-to-end LN availability and is the documented
// trade-off (per the LN capability probe task). A 2xx response carrying a
// non-empty bolt11 invoice ("request" field) counts as success.
func (t *MintHealthTracker) probeLightningCapability(mintURL string, client *http.Client) bool {
	url := strings.TrimRight(mintURL, "/") + lnQuoteEndpoint

	body := fmt.Sprintf(`{"amount":%d,"unit":"sat"}`, lnProbeAmount)

	start := time.Now()
	resp, err := client.Post(url, "application/json", strings.NewReader(body))
	elapsed := time.Since(start)
	if err != nil {
		log.Printf("ln probe FAILED: url=%s elapsed=%s error=%v", url, elapsed, err)
		return false
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		log.Printf("ln probe: url=%s status=%d elapsed=%s ok=false (non-2xx; LN backend likely down)", url, resp.StatusCode, elapsed)
		return false
	}

	var quote struct {
		Request string `json:"request"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&quote); err != nil {
		log.Printf("ln probe: url=%s decode error=%v", url, err)
		return false
	}

	ok := quote.Request != ""
	log.Printf("ln probe: url=%s elapsed=%s ok=%v (invoice_len=%d)", url, elapsed, ok, len(quote.Request))
	return ok
}
