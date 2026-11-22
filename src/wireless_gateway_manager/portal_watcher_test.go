package wireless_gateway_manager

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func portalWatcherFixture(t *testing.T) (*PortalWatcher, *int, *time.Time) {
	t.Helper()
	now := time.Unix(1700000000, 0)
	attempts := new(int)
	w := NewPortalWatcher([]PortalUplinkConfig{{
		Interface: "phy1-sta0",
		SSID:      "venue",
		Recipe:    PortalRecipe{Mode: portalLoginModeClickThrough, Timeout: time.Second},
		Mark:      0x20000,
	}}, time.Minute)
	w.now = func() time.Time { return now }
	w.addrFn = func(string) (string, bool) { return "10.0.0.2", true }
	w.bytesFn = func(string) int64 { return 0 }
	w.attempt = func(context.Context, PortalBinding, PortalRecipe, func(context.Context) bool) PortalResult {
		*attempts++
		return PortalResult{Outcome: PortalStillCaptive, Reason: "still captive"}
	}
	w.probe = func(context.Context, PortalBinding) bool { return false }
	return w, attempts, &now
}

func TestPortalWatcherSucceedsOnceThenStops(t *testing.T) {
	w, attempts, _ := portalWatcherFixture(t)
	w.attempt = func(context.Context, PortalBinding, PortalRecipe, func(context.Context) bool) PortalResult {
		*attempts++
		return PortalResult{Outcome: PortalOK, PortalHost: "portal.example"}
	}

	w.Tick(context.Background())
	w.Tick(context.Background())

	require.Equal(t, 1, *attempts)
	status := w.Status()
	require.Equal(t, "authorized", status[0].State)
	require.Equal(t, 1, status[0].Attempts)
}

func TestPortalWatcherExhaustsAfterMaxAttempts(t *testing.T) {
	w, attempts, now := portalWatcherFixture(t)
	for i := 0; i < w.maxAttempts; i++ {
		w.Tick(context.Background())
		*now = now.Add(time.Hour)
	}
	require.Equal(t, w.maxAttempts, *attempts)
	require.Equal(t, "exhausted", w.Status()[0].State)

	// The exhausted state is sticky: no further portal POSTs, even much later.
	*now = now.Add(24 * time.Hour)
	w.Tick(context.Background())
	require.Equal(t, w.maxAttempts, *attempts)
}

func TestPortalWatcherResetsOnNewLease(t *testing.T) {
	w, attempts, _ := portalWatcherFixture(t)
	addr := "10.0.0.2"
	w.addrFn = func(string) (string, bool) { return addr, true }

	w.Tick(context.Background())
	require.Equal(t, 1, *attempts)

	addr = "10.0.0.3"
	w.Tick(context.Background())
	require.Equal(t, 2, *attempts)
}

func TestPortalWatcherBudgetSecondsExhausts(t *testing.T) {
	w, _, now := portalWatcherFixture(t)
	w.configs[0].BudgetSeconds = 60
	w.attempt = func(context.Context, PortalBinding, PortalRecipe, func(context.Context) bool) PortalResult {
		return PortalResult{Outcome: PortalOK, PortalHost: "portal.example"}
	}

	w.Tick(context.Background())
	require.Equal(t, "authorized", w.Status()[0].State)

	*now = now.Add(61 * time.Second)
	w.Tick(context.Background())
	require.Equal(t, "exhausted", w.Status()[0].State)
}

func TestPortalWatcherBudgetBytesExhausts(t *testing.T) {
	w, _, _ := portalWatcherFixture(t)
	w.configs[0].BudgetBytes = 500
	bytes := int64(1000)
	w.bytesFn = func(string) int64 { return bytes }
	w.attempt = func(context.Context, PortalBinding, PortalRecipe, func(context.Context) bool) PortalResult {
		return PortalResult{Outcome: PortalOK, PortalHost: "portal.example"}
	}

	w.Tick(context.Background())
	require.Equal(t, "authorized", w.Status()[0].State)

	bytes = 1600
	w.Tick(context.Background())
	require.Equal(t, "exhausted", w.Status()[0].State)
}

func TestPortalWatcherRunsRecipeSpec(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	spec := PortalRecipeSpec{
		ID:   "watch-recipe",
		Name: "watch-recipe",
		Flow: PortalRecipeFlow{Type: "http-api", Steps: []PortalRecipeStep{{Type: "request", URL: srv.URL}}},
	}
	w := NewPortalWatcher([]PortalUplinkConfig{{
		Interface:  "lo",
		SSID:       "venue",
		RecipeSpec: &spec,
	}}, time.Minute)
	w.now = func() time.Time { return time.Unix(1700000000, 0) }
	w.addrFn = func(string) (string, bool) { return "127.0.0.1", true }
	w.bytesFn = func(string) int64 { return 0 }
	var probes atomic.Int32
	w.probe = func(context.Context, PortalBinding) bool { return probes.Add(1) > 1 }
	w.attempt = func(context.Context, PortalBinding, PortalRecipe, func(context.Context) bool) PortalResult {
		t.Fatal("the single-form path must not run when RecipeSpec is set")
		return PortalResult{}
	}

	w.Tick(context.Background())
	require.Equal(t, "authorized", w.Status()[0].State)
}
