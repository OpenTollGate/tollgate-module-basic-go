package wireless_gateway_manager

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func portalBoundRecipe(pageURL string) PortalRecipe {
	return PortalRecipe{
		Mode:    portalLoginModeClickThrough,
		PageURL: pageURL,
		Timeout: 2 * time.Second,
	}
}

func TestPortalHostAllowedMatchesHostOrHostPort(t *testing.T) {
	u := mustURL(t, "https://signup.example.com:8443/en/guest")
	require.True(t, portalHostAllowed(u, []string{"signup.example.com"}))
	require.True(t, portalHostAllowed(u, []string{"SIGNUP.EXAMPLE.COM:8443"}))
	require.False(t, portalHostAllowed(u, []string{"signup.example.com:443"}))
	require.False(t, portalHostAllowed(u, []string{"evil.example.com"}))
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	require.NoError(t, err)
	return u
}

func TestAttemptPortalLoginSharesCookieJarAcrossFetchAndSubmit(t *testing.T) {
	var gotCookie atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/portal":
			http.SetCookie(w, &http.Cookie{Name: "sid", Value: "abc123", Path: "/"})
			fmt.Fprint(w, `<form action="/submit" method="post"><input type="submit" name="go" value="1"></form>`)
		case "/submit":
			c, err := r.Cookie("sid")
			if err == nil {
				gotCookie.Store(c.Value)
			}
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	res := AttemptPortalLogin(context.Background(), PortalBinding{}, portalBoundRecipe(srv.URL+"/portal"), func(context.Context) bool { return true })
	require.Equal(t, PortalOK, res.Outcome)
	require.Equal(t, "abc123", gotCookie.Load())
}

func TestAttemptPortalLoginFollowsAllowListedCrossHostForm(t *testing.T) {
	var authHits atomic.Int32
	auth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer auth.Close()

	portal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `<form action="%s/submit" method="post"><input type="submit" name="go" value="1"></form>`, auth.URL)
	}))
	defer portal.Close()

	recipe := portalBoundRecipe(portal.URL + "/portal")
	recipe.AllowHosts = []string{strings.TrimPrefix(auth.URL, "http://")}
	res := AttemptPortalLogin(context.Background(), PortalBinding{}, recipe, func(context.Context) bool { return true })
	require.Equal(t, PortalOK, res.Outcome)
	require.Equal(t, int32(1), authHits.Load())
}

func TestAttemptPortalLoginRefusesUnlistedCrossHostForm(t *testing.T) {
	var authHits atomic.Int32
	auth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHits.Add(1)
	}))
	defer auth.Close()

	portal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `<form action="%s/submit" method="post"><input type="submit" name="go" value="1"></form>`, auth.URL)
	}))
	defer portal.Close()

	res := AttemptPortalLogin(context.Background(), PortalBinding{}, portalBoundRecipe(portal.URL+"/portal"), func(context.Context) bool { return true })
	require.Equal(t, PortalNoForm, res.Outcome)
	require.Zero(t, authHits.Load())
}

func TestAttemptPortalLoginBindsToDevice(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("SO_BINDTODEVICE is Linux-only")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<form action="/submit" method="get"><input type="submit" value="go"></form>`)
	}))
	defer srv.Close()

	res := AttemptPortalLogin(context.Background(), PortalBinding{Interface: "lo"}, portalBoundRecipe(srv.URL+"/portal"), func(context.Context) bool { return true })
	require.Equal(t, PortalOK, res.Outcome)
}

func TestProbeUplinkRequiresThe204Sentinel(t *testing.T) {
	old := portalProbeURL
	defer func() { portalProbeURL = old }()

	status := http.StatusNoContent
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
	}))
	defer srv.Close()
	portalProbeURL = srv.URL + "/probe"

	require.True(t, ProbeUplink(context.Background(), PortalBinding{}))

	status = http.StatusOK
	require.False(t, ProbeUplink(context.Background(), PortalBinding{}))

	status = http.StatusFound
	require.False(t, ProbeUplink(context.Background(), PortalBinding{}))
}
