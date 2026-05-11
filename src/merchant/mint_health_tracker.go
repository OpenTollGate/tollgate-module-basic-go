package merchant

import (
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
)

type MintHealthTracker struct {
	mu               sync.RWMutex
	mintConfigs      []config_manager.MintConfig
	reachable        map[string]bool
	reachableCount   int
	hadReachableMint bool

	onFirstReachable      func()
	onReachableSetChanged func()

	stopCh        chan struct{}
	stopOnce      sync.Once
	probeInterval time.Duration
	probeTimeout  time.Duration

	consecutiveSuccess  map[string]int
	requiredConsecutive int
}

func NewMintHealthTracker(mintConfigs []config_manager.MintConfig) *MintHealthTracker {
	return &MintHealthTracker{
		mintConfigs:         mintConfigs,
		reachable:           make(map[string]bool),
		consecutiveSuccess:  make(map[string]int),
		requiredConsecutive: 3,
		probeInterval:       5 * time.Minute,
		probeTimeout:        5 * time.Second,
		stopCh:              make(chan struct{}),
	}
}

func (m *MintHealthTracker) RunInitialProbe() []config_manager.MintConfig {
	var reachable []config_manager.MintConfig

	for _, cfg := range m.mintConfigs {
		if m.probeMint(cfg.URL) {
			m.reachable[cfg.URL] = true
			m.reachableCount++
			m.consecutiveSuccess[cfg.URL] = m.requiredConsecutive
			reachable = append(reachable, cfg)
		} else {
			m.reachable[cfg.URL] = false
			m.consecutiveSuccess[cfg.URL] = 0
		}
	}

	if m.reachableCount > 0 {
		m.hadReachableMint = true
	}

	return reachable
}

func (m *MintHealthTracker) Start() {
	ticker := time.NewTicker(m.probeInterval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-m.stopCh:
				return
			case <-ticker.C:
				m.runProactiveCheck()
			}
		}
	}()
	log.Printf("MintHealthTracker: started proactive checks every %s", m.probeInterval)
}

func (m *MintHealthTracker) Stop() {
	m.stopOnce.Do(func() {
		close(m.stopCh)
		log.Printf("MintHealthTracker: stopped")
	})
}

func (m *MintHealthTracker) probeMint(mintURL string) bool {
	endpoint := strings.TrimRight(mintURL, "/") + "/v1/info"
	client := &http.Client{Timeout: m.probeTimeout}
	resp, err := client.Get(endpoint)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func (m *MintHealthTracker) runProactiveCheck() {
	var callbacks []func()
	previousCount := 0
	firstJustBecameReachable := false

	m.mu.Lock()
	for _, cfg := range m.mintConfigs {
		reachable := m.probeMint(cfg.URL)

		if reachable {
			m.consecutiveSuccess[cfg.URL]++
		} else {
			m.consecutiveSuccess[cfg.URL] = 0
		}

		wasReachable := m.reachable[cfg.URL]
		nowReachable := m.consecutiveSuccess[cfg.URL] >= m.requiredConsecutive

		if nowReachable != wasReachable {
			m.reachable[cfg.URL] = nowReachable
			if nowReachable {
				log.Printf("MintHealthTracker: mint %s became reachable (hysteresis: %d consecutive successes)", cfg.URL, m.consecutiveSuccess[cfg.URL])
			} else {
				log.Printf("MintHealthTracker: mint %s became unreachable", cfg.URL)
			}
		}
	}

	count := 0
	for _, r := range m.reachable {
		if r {
			count++
		}
	}
	previousCount = m.reachableCount
	countChanged := count != previousCount
	m.reachableCount = count

	if !m.hadReachableMint && count > 0 {
		firstJustBecameReachable = true
		m.hadReachableMint = true
	}

	if firstJustBecameReachable && m.onFirstReachable != nil {
		callbacks = append(callbacks, m.onFirstReachable)
	}
	if countChanged && m.onReachableSetChanged != nil {
		callbacks = append(callbacks, m.onReachableSetChanged)
	}
	m.mu.Unlock()

	for _, cb := range callbacks {
		cb()
	}
}

func (m *MintHealthTracker) GetReachableMintConfigs() []config_manager.MintConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []config_manager.MintConfig
	for _, cfg := range m.mintConfigs {
		if m.reachable[cfg.URL] {
			result = append(result, cfg)
		}
	}
	return result
}

func (m *MintHealthTracker) GetReachableCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.reachableCount
}

// SetOnFirstReachable sets a callback that fires once when the first mint
// comes back after a total outage. RunInitialProbe sets hadReachableMint to
// true if any mint was reachable during the initial probe, which suppresses
// this callback until all mints go down and one recovers.
func (m *MintHealthTracker) SetOnFirstReachable(fn func()) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onFirstReachable = fn
}

func (m *MintHealthTracker) SetOnReachableSetChanged(fn func()) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onReachableSetChanged = fn
}

func (m *MintHealthTracker) String() string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var parts []string
	for _, cfg := range m.mintConfigs {
		status := "unreachable"
		if m.reachable[cfg.URL] {
			status = "reachable"
		}
		parts = append(parts, fmt.Sprintf("%s=%s", cfg.URL, status))
	}
	return fmt.Sprintf("MintHealthTracker{%s}", strings.Join(parts, ", "))
}
