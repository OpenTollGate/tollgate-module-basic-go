package wireless_gateway_manager

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func portalAutoManager(serverURL string) *UpstreamManager {
	m := NewUpstreamManager(nil, nil, nil, UpstreamManagerConfig{
		PortalAutoLogin:    true,
		PortalLoginURL:     serverURL,
		PortalLoginTimeout: 2 * time.Second,
	})
	m.DiscoveryLog = nil
	return m
}

func TestPortalAutoLoginClickThrough(t *testing.T) {
	var mu sync.Mutex
	var got url.Values
	submitted := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/portal":
			fmt.Fprint(w, `<html><body><form action="/submit" method="post">
				<input type="hidden" name="tok" value="abc123">
				<input type="text" name="note" value="hello">
				<input type="submit" name="accept" value="yes">
			</form></body></html>`)
		case "/submit":
			require.NoError(t, r.ParseForm())
			mu.Lock()
			got = r.PostForm
			submitted++
			mu.Unlock()
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	m := portalAutoManager(srv.URL + "/portal")
	m.connectivityProbeFn = func() ConnectivityResult { return ConnectivityResult{OK: true, Reason: "ok"} }

	require.True(t, m.attemptPortalLogin(upstreamIdentity{generation: 1}))
	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, 1, submitted)
	require.Equal(t, "abc123", got.Get("tok"))
	require.Equal(t, "yes", got.Get("accept"))
}

func TestPortalAutoLoginFormCredentials(t *testing.T) {
	var got url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/portal":
			fmt.Fprint(w, `<form action="/login" method="post">
				<input name="user"><input type="password" name="pass">
				<input type="hidden" name="csrf" value="t">
				<button name="go" value="1">Sign in</button></form>`)
		case "/login":
			require.NoError(t, r.ParseForm())
			got = r.PostForm
			w.WriteHeader(http.StatusFound)
		}
	}))
	defer srv.Close()

	m := portalAutoManager(srv.URL + "/portal")
	m.config.PortalLoginMode = "form"
	m.config.PortalLoginUserField = "user"
	m.config.PortalLoginPassField = "pass"
	m.config.PortalLoginUsername = "guest"
	m.config.PortalLoginPassword = "s3cret"
	m.config.PortalLoginExtra = "room=101&lastname=Smith"
	m.connectivityProbeFn = func() ConnectivityResult { return ConnectivityResult{OK: true, Reason: "ok"} }

	require.True(t, m.attemptPortalLogin(upstreamIdentity{generation: 1}))
	require.Equal(t, "guest", got.Get("user"))
	require.Equal(t, "s3cret", got.Get("pass"))
	require.Equal(t, "t", got.Get("csrf"))
	require.Equal(t, "1", got.Get("go"))
	require.Equal(t, "101", got.Get("room"))
	require.Equal(t, "Smith", got.Get("lastname"))
}

func TestPortalAutoLoginRefusesCrossHostFormAction(t *testing.T) {
	var evilHits atomic.Int32
	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		evilHits.Add(1)
	}))
	defer evil.Close()

	portal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `<form action="%s/steal" method="post"><input name="password" value="x"></form>`, evil.URL)
	}))
	defer portal.Close()

	m := portalAutoManager(portal.URL + "/portal")
	m.connectivityProbeFn = func() ConnectivityResult { return ConnectivityResult{OK: true, Reason: "ok"} }

	require.False(t, m.attemptPortalLogin(upstreamIdentity{generation: 1}))
	require.Zero(t, evilHits.Load())
}

func TestPortalAutoLoginOncePerGeneration(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/submit" {
			hits.Add(1)
		}
		fmt.Fprint(w, `<form action="/submit" method="post"><input type="submit" name="accept" value="yes"></form>`)
	}))
	defer srv.Close()

	m := portalAutoManager(srv.URL + "/portal")
	m.connectivityProbeFn = func() ConnectivityResult { return ConnectivityResult{OK: true, Reason: "ok"} }

	require.True(t, m.attemptPortalLogin(upstreamIdentity{generation: 7}))
	require.False(t, m.attemptPortalLogin(upstreamIdentity{generation: 7}))
	require.Equal(t, int32(1), hits.Load())
}

func TestPortalAutoLoginDisabledDoesNothing(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
	}))
	defer srv.Close()

	m := NewUpstreamManager(nil, nil, nil, UpstreamManagerConfig{PortalLoginURL: srv.URL})
	m.DiscoveryLog = nil
	require.False(t, m.tryAutoPortalLogin(upstreamIdentity{generation: 1}))
	require.Zero(t, hits.Load())
}

func TestPortalAutoLoginObservationPath(t *testing.T) {
	m, _, id := portalTestManager(t)
	m.config.PortalAutoLogin = true
	called := false
	m.portalAutoLoginFn = func(upstreamIdentity) bool {
		called = true
		return true
	}
	m.connectivityProbeFn = func() ConnectivityResult { return ConnectivityResult{OK: true, Reason: "ok"} }

	require.Equal(t, portalOrdinary, m.observePortalLogin(id, true, ConnectivityResult{Reason: "captive"}))
	require.True(t, called)
}

func TestPortalAutoLoginObservationFallsBackToManualWindow(t *testing.T) {
	m, _, id := portalTestManager(t)
	m.config.PortalAutoLogin = true
	m.portalAutoLoginFn = func(upstreamIdentity) bool { return false }
	m.connectivityProbeFn = func() ConnectivityResult { return ConnectivityResult{Reason: "captive"} }

	require.Equal(t, portalHold, m.observePortalLogin(id, true, ConnectivityResult{Reason: "captive"}))
}

func TestPortalAutoLoginRunsOutsideWriterLock(t *testing.T) {
	m, _, id := portalTestManager(t)
	m.config.PortalAutoLogin = true
	called := false
	m.portalAutoLoginFn = func(upstreamIdentity) bool {
		called = true
		return true
	}
	m.connectivityProbeFn = func() ConnectivityResult { return ConnectivityResult{OK: true, Reason: "ok"} }

	m.writerMu.Lock()
	defer m.writerMu.Unlock()
	// The HTTP attempt must still run; only the state transition needs the
	// writer, and it is correctly deferred while the lock is held.
	require.Equal(t, portalDeferred, m.observePortalLogin(id, true, ConnectivityResult{Reason: "captive"}))
	require.True(t, called)
}

func TestDiscoverPortalURLFollowsRedirect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/captive" {
			w.Header().Set("Location", "/login")
			w.WriteHeader(http.StatusFound)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	old := portalProbeURL
	portalProbeURL = srv.URL + "/captive"
	defer func() { portalProbeURL = old }()

	require.Equal(t, srv.URL+"/login", discoverPortalURL(2*time.Second))
}
