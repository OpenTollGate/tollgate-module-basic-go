// Per-uplink captive-portal watcher.
//
// The single-STA upstream manager owns one portal window at a time. A bond has
// more than one uplink, and the non-default one is exactly the one that needs
// a login before its subflow can carry payload. This watcher gives each
// configured uplink its own bounded attempt, state and budget.
//
// It is deliberately not an identity machine: one attempt per association/lease
// generation, a small attempt cap, and a sticky exhausted state. It never
// changes a MAC, a key or an SSID to obtain another session.
package wireless_gateway_manager

import (
	"context"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// PortalUplinkConfig is one uplink's portal recipe plus its session budget.
type PortalUplinkConfig struct {
	// Interface is the netdev the login must leave through.
	Interface string
	// SSID is advisory and only used in logs.
	SSID   string
	Recipe PortalRecipe
	// RecipeSpec selects the multi-step engine. When set it supersedes Recipe.
	RecipeSpec  *PortalRecipeSpec
	RecipeVars  map[string]string
	Credentials PortalRecipeCredentials
	// Mark and DNS override the recipe's socket mark/resolver.
	Mark int
	DNS  string
	// BudgetSeconds/BudgetBytes bound one authorized session. Zero means no
	// budget. When a budget is exceeded the uplink is exhausted and is not
	// logged in again.
	BudgetSeconds int
	BudgetBytes   int64
}

// PortalUplinkStatus is a read-only snapshot for logs and tests.
type PortalUplinkStatus struct {
	Interface    string
	SSID         string
	State        string // pending | authorized | exhausted
	Attempts     int
	AuthorizedAt time.Time
	LastError    string
}

type portalUplinkState struct {
	addr         string
	authorized   bool
	exhausted    bool
	attempts     int
	nextAttempt  time.Time
	authorizedAt time.Time
	startBytes   int64
	lastError    string
}

// PortalWatcher drives one portal attempt per configured uplink.
type PortalWatcher struct {
	configs     []PortalUplinkConfig
	interval    time.Duration
	maxAttempts int

	now     func() time.Time
	probe   func(context.Context, PortalBinding) bool
	attempt func(context.Context, PortalBinding, PortalRecipe, func(context.Context) bool) PortalResult
	addrFn  func(string) (string, bool)
	bytesFn func(string) int64

	mu     sync.Mutex
	states map[string]*portalUplinkState
}

// NewPortalWatcher builds a watcher. interval <= 0 uses 30 seconds.
func NewPortalWatcher(configs []PortalUplinkConfig, interval time.Duration) *PortalWatcher {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	return &PortalWatcher{
		configs:     append([]PortalUplinkConfig(nil), configs...),
		interval:    interval,
		maxAttempts: 3,
		now:         time.Now,
		probe:       ProbeUplink,
		attempt:     AttemptPortalLogin,
		addrFn:      portalInterfaceIPv4,
		bytesFn:     portalInterfaceBytes,
		states:      make(map[string]*portalUplinkState),
	}
}

// Run ticks until ctx is cancelled.
func (w *PortalWatcher) Run(ctx context.Context) {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	w.Tick(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.Tick(ctx)
		}
	}
}

// Tick performs at most one state transition per uplink.
func (w *PortalWatcher) Tick(ctx context.Context) {
	for _, cfg := range w.configs {
		iface := strings.TrimSpace(cfg.Interface)
		if iface == "" {
			continue
		}
		w.tickUplink(ctx, cfg)
	}
}

func (w *PortalWatcher) tickUplink(ctx context.Context, cfg PortalUplinkConfig) {
	now := w.now()

	addr, up := w.addrFn(cfg.Interface)
	if !up || addr == "" {
		return
	}

	w.mu.Lock()
	state := w.states[cfg.Interface]
	if state == nil {
		state = &portalUplinkState{}
		w.states[cfg.Interface] = state
	}
	if addr != state.addr {
		// A new lease/association is a new session opportunity, not a new
		// identity. Reset the bounded attempt state for this generation.
		state.addr = addr
		state.authorized = false
		state.exhausted = false
		state.attempts = 0
		state.nextAttempt = time.Time{}
		state.lastError = ""
	}

	if state.exhausted {
		w.mu.Unlock()
		return
	}

	if state.authorized {
		exceeded := w.budgetExceeded(cfg, state, now)
		if exceeded {
			state.exhausted = true
		}
		w.mu.Unlock()
		if exceeded {
			logger.WithFields(map[string]interface{}{
				"interface": cfg.Interface,
				"ssid":      cfg.SSID,
			}).Warn("Portal session budget exhausted; uplink will not be logged in again")
		}
		return
	}

	if now.Before(state.nextAttempt) {
		w.mu.Unlock()
		return
	}
	if state.attempts >= w.maxAttempts {
		state.exhausted = true
		lastError := state.lastError
		attempts := state.attempts
		w.mu.Unlock()
		logger.WithFields(map[string]interface{}{
			"interface": cfg.Interface,
			"ssid":      cfg.SSID,
			"attempts":  attempts,
			"error":     lastError,
		}).Error("Portal login attempts exhausted; uplink left for the operator")
		return
	}
	state.attempts++
	attempts := state.attempts
	w.mu.Unlock()

	binding := PortalBinding{
		Interface: cfg.Interface,
		SourceIP:  addr,
		Mark:      cfg.Mark,
		DNS:       strings.TrimSpace(cfg.DNS),
	}
	// An uplink that already has real internet needs no portal POST. This also
	// keeps a previously authorized session from being re-submitted on every
	// daemon restart.
	if w.probe(ctx, binding) {
		w.mu.Lock()
		state.authorized = true
		state.authorizedAt = now
		state.startBytes = w.bytesFn(cfg.Interface)
		state.lastError = ""
		w.mu.Unlock()
		logger.WithFields(map[string]interface{}{
			"interface": cfg.Interface,
			"ssid":      cfg.SSID,
		}).Info("Uplink already has internet; no portal login needed")
		return
	}

	var result PortalResult
	if cfg.RecipeSpec != nil {
		spec := *cfg.RecipeSpec
		if len(cfg.RecipeVars) > 0 {
			spec.SeedVars = make(map[string]string, len(cfg.RecipeVars))
			for name, value := range cfg.RecipeVars {
				spec.SeedVars[name] = value
			}
		}
		recipeResult := ExecutePortalRecipe(ctx, binding, spec, cfg.Credentials, func(ctx context.Context) bool {
			return w.probe(ctx, binding)
		})
		if recipeResult.OK {
			result.Outcome = PortalOK
			result.Reason = recipeResult.Reason
		} else {
			result.Outcome = PortalStillCaptive
			result.Reason = recipeResult.Reason
		}
	} else {
		result = w.attempt(ctx, binding, cfg.Recipe, func(ctx context.Context) bool {
			return w.probe(ctx, binding)
		})
	}

	w.mu.Lock()
	if result.Outcome == PortalOK {
		state.authorized = true
		state.authorizedAt = now
		state.startBytes = w.bytesFn(cfg.Interface)
		state.lastError = ""
	} else {
		state.lastError = result.Reason
		if attempts >= w.maxAttempts {
			state.exhausted = true
		} else {
			state.nextAttempt = now.Add(time.Duration(attempts) * 30 * time.Second)
		}
	}
	w.mu.Unlock()

	if result.Outcome == PortalOK {
		logger.WithFields(map[string]interface{}{
			"interface":   cfg.Interface,
			"ssid":        cfg.SSID,
			"portal_host": result.PortalHost,
		}).Info("Per-uplink portal login succeeded")
		return
	}
	logger.WithFields(map[string]interface{}{
		"interface": cfg.Interface,
		"ssid":      cfg.SSID,
		"outcome":   result.Outcome,
		"reason":    result.Reason,
		"attempt":   attempts,
	}).Warn("Per-uplink portal login failed")
}

func (w *PortalWatcher) budgetExceeded(cfg PortalUplinkConfig, state *portalUplinkState, now time.Time) bool {
	if cfg.BudgetSeconds > 0 && !state.authorizedAt.IsZero() &&
		now.Sub(state.authorizedAt) >= time.Duration(cfg.BudgetSeconds)*time.Second {
		return true
	}
	if cfg.BudgetBytes > 0 && state.startBytes > 0 {
		used := w.bytesFn(cfg.Interface) - state.startBytes
		if used >= cfg.BudgetBytes {
			return true
		}
	}
	return false
}

// Status returns a copy of every uplink's current state.
func (w *PortalWatcher) Status() []PortalUplinkStatus {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]PortalUplinkStatus, 0, len(w.configs))
	for _, cfg := range w.configs {
		state := w.states[cfg.Interface]
		if state == nil {
			out = append(out, PortalUplinkStatus{Interface: cfg.Interface, SSID: cfg.SSID, State: "pending"})
			continue
		}
		status := PortalUplinkStatus{
			Interface:    cfg.Interface,
			SSID:         cfg.SSID,
			State:        "pending",
			Attempts:     state.attempts,
			AuthorizedAt: state.authorizedAt,
			LastError:    state.lastError,
		}
		if state.exhausted {
			status.State = "exhausted"
		} else if state.authorized {
			status.State = "authorized"
		}
		out = append(out, status)
	}
	return out
}

// portalInterfaceIPv4 returns the first IPv4 address on a netdev.
func portalInterfaceIPv4(name string) (string, bool) {
	ifi, err := net.InterfaceByName(name)
	if err != nil {
		return "", false
	}
	addrs, err := ifi.Addrs()
	if err != nil {
		return "", false
	}
	for _, a := range addrs {
		ipnet, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		if ip4 := ipnet.IP.To4(); ip4 != nil {
			return ip4.String(), true
		}
	}
	return "", false
}

// portalInterfaceBytes returns rx+tx bytes from sysfs. It is the coarse
// per-uplink meter the session budget uses; a failed read is treated as zero,
// which disables the byte budget rather than inventing usage.
func portalInterfaceBytes(name string) int64 {
	var total int64
	for _, file := range []string{"rx_bytes", "tx_bytes"} {
		b, err := os.ReadFile("/sys/class/net/" + name + "/statistics/" + file)
		if err != nil {
			continue
		}
		n, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
		if err != nil {
			continue
		}
		total += n
	}
	return total
}
