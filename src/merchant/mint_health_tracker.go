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

// RunInitialProbe probes all mints synchronously and populates internal state.
//
// TODO(c3): This method is NOT thread-safe — it modifies reachable, reachableCount,
// consecutiveSuccess, and hadReachableMint without holding mu. It MUST be called
// before Start() (which launches the background goroutine). Calling it after Start()
// would race with runProactiveCheck. Currently safe because init() calls
// RunInitialProbe → SetOnFirstReachable → Start in strict order.
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
	// Probe all mints outside the lock (network I/O).
	type probeResult struct {
		url       string
		reachable bool
	}
	results := make([]probeResult, len(m.mintConfigs))
	for i, cfg := range m.mintConfigs {
		results[i] = probeResult{url: cfg.URL, reachable: m.probeMint(cfg.URL)}
	}

	// Now lock only to update shared state and collect callbacks.
	var callbacks []func()

	m.mu.Lock()
	for _, r := range results {
		if r.reachable {
			m.consecutiveSuccess[r.url]++
		} else {
			m.consecutiveSuccess[r.url] = 0
		}

		wasReachable := m.reachable[r.url]
		nowReachable := m.consecutiveSuccess[r.url] >= m.requiredConsecutive

		if nowReachable != wasReachable {
			m.reachable[r.url] = nowReachable
			if nowReachable {
				log.Printf("MintHealthTracker: mint %s became reachable (hysteresis: %d consecutive successes)", r.url, m.consecutiveSuccess[r.url])
			} else {
				log.Printf("MintHealthTracker: mint %s became unreachable", r.url)
			}
		}
	}

	count := 0
	for _, r := range m.reachable {
		if r {
			count++
		}
	}
	previousCount := m.reachableCount
	countChanged := count != previousCount
	m.reachableCount = count

	firstJustBecameReachable := false
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

	// TODO(c2): Callbacks fire on the timer goroutine. If onFirstReachable performs
	// heavy work (e.g. merchant.New which does wallet loading + network I/O), the
	// timer goroutine blocks until recovery completes. This prevents subsequent
	// proactive checks and delays Stop() response. Acceptable today because
	// onFirstReachable is one-shot and the probe interval is 5 minutes, but if
	// callbacks grow heavier, fire them in a separate goroutine.
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

// ResetFirstReachable allows the onFirstReachable callback to fire again
// on the next proactive check that finds a reachable mint.
func (m *MintHealthTracker) ResetFirstReachable() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.hadReachableMint = false
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
