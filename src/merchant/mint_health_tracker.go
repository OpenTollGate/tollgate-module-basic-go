package merchant

import (
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
)

const (
	defaultRecoveryThreshold uint8 = 3
	probeTimeout            = 5 * time.Second
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
	go func() {
		ticker := time.NewTicker(probeInterval)
		defer ticker.Stop()

		for range ticker.C {
			t.runProactiveCheck()
		}
	}()
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

func (t *MintHealthTracker) MarkUnreachable(mintURL string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.reachableMints[mintURL] = false
	t.consecutiveSuccesses[mintURL] = 0
}

func (t *MintHealthTracker) SetOnFirstReachable(callback func()) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.onFirstReachable = callback
	t.hadReachableMint = false
}

func (t *MintHealthTracker) RunInitialProbe() {
	config := t.configProvider.GetConfig()
	if config == nil {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	for _, mint := range config.AcceptedMints {
		if t.probeMint(mint.URL) {
			t.reachableMints[mint.URL] = true
			t.consecutiveSuccesses[mint.URL] = t.recoveryThreshold
		} else {
			t.reachableMints[mint.URL] = false
			t.consecutiveSuccesses[mint.URL] = 0
		}
	}

	for _, mint := range config.AcceptedMints {
		if t.reachableMints[mint.URL] {
			t.hadReachableMint = true
			break
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

	t.mu.Lock()

	for _, mint := range config.AcceptedMints {
		if t.probeMint(mint.URL) {
			t.consecutiveSuccesses[mint.URL]++

			if !t.reachableMints[mint.URL] && t.consecutiveSuccesses[mint.URL] >= t.recoveryThreshold {
				t.reachableMints[mint.URL] = true
			}
		} else {
			t.consecutiveSuccesses[mint.URL] = 0
			t.reachableMints[mint.URL] = false
		}
	}

	if !t.hadReachableMint && t.onFirstReachable != nil {
		for _, mint := range config.AcceptedMints {
			if t.reachableMints[mint.URL] {
				t.hadReachableMint = true
				cb := t.onFirstReachable
				t.mu.Unlock()
				go cb()
				t.mu.Lock()
				break
			}
		}
	}

	t.mu.Unlock()
}

func (t *MintHealthTracker) probeMint(mintURL string) bool {
	url := strings.TrimRight(mintURL, "/") + "/v1/info"

	resp, err := t.httpClient.Get(url)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	return resp.StatusCode >= 200 && resp.StatusCode < 300
}
