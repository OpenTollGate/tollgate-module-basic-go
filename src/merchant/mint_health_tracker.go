package merchant

import (
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
)

const (
	defaultRecoveryThreshold uint8 = 3
	probeTimeout            = 30 * time.Second
	probeInterval           = 5 * time.Minute
)

type mintConfigProvider interface {
	GetConfig() *config_manager.Config
}

type MintHealthTracker struct {
	mu                   sync.RWMutex
	reachableMints       map[string]bool
	consecutiveSuccesses map[string]uint8
	httpClient           *http.Client
	configProvider       mintConfigProvider
	recoveryThreshold    uint8
	onFirstReachable     func()
	hadReachableMint     bool
	onReachableSetChanged func()
	reachableCount       int
	stopCh               chan struct{}
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
	t.mu.Unlock()

	go func() {
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

func (t *MintHealthTracker) probeMint(mintURL string) bool {
	url := strings.TrimRight(mintURL, "/") + "/v1/info"

	start := time.Now()
	resp, err := t.httpClient.Get(url)
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
