package wireless_gateway_manager

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

func recipeProbeOK(context.Context) bool { return true }

func TestExecutePortalRecipeChainsRequestsAndTemplates(t *testing.T) {
	var sawToken string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			fmt.Fprint(w, `{"token":"abc123"}`)
		case "/use":
			sawToken = r.Header.Get("X-Token")
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	spec := PortalRecipeSpec{
		ID:   "chain",
		Name: "chain",
		Flow: PortalRecipeFlow{Type: "http-api", Steps: []PortalRecipeStep{
			{Type: "request", URL: srv.URL + "/token", Vars: map[string]string{"token": "$.token"}},
			{Type: "request", URL: srv.URL + "/use", Headers: map[string]string{"X-Token": "{{token}}"}},
		}},
	}
	result := ExecutePortalRecipe(context.Background(), PortalBinding{}, spec, PortalRecipeCredentials{}, recipeProbeOK)
	require.True(t, result.OK, result.Reason)
	require.Equal(t, "abc123", sawToken)
}

func TestExecutePortalRecipeFormGeneric(t *testing.T) {
	var gotEmail, gotPass, gotCSRF string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/portal":
			fmt.Fprint(w, `<form action="/login" method="post">
				<input type="email" name="email">
				<input type="password" name="pass">
				<input type="hidden" name="csrf" value="t">
				<input type="submit" value="Sign in">
			</form>`)
		case "/login":
			require.NoError(t, r.ParseForm())
			gotEmail = r.PostForm.Get("email")
			gotPass = r.PostForm.Get("pass")
			gotCSRF = r.PostForm.Get("csrf")
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	spec := PortalRecipeSpec{
		ID:            "form",
		Name:          "form",
		Credentials:   true,
		Flow:          PortalRecipeFlow{Type: "http-api", Steps: []PortalRecipeStep{{Type: "form_generic", URL: srv.URL + "/portal"}}},
		AllowHosts:    []string{strings.TrimPrefix(srv.URL, "http://")},
		SchemaVersion: 1,
	}
	result := ExecutePortalRecipe(context.Background(), PortalBinding{}, spec, PortalRecipeCredentials{Username: "guest@example.test", Password: "s3cret"}, recipeProbeOK)
	require.True(t, result.OK, result.Reason)
	require.Equal(t, "guest@example.test", gotEmail)
	require.Equal(t, "s3cret", gotPass)
	require.Equal(t, "t", gotCSRF)
}

func TestExecutePortalRecipeWispr(t *testing.T) {
	var gotUser, gotPass string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/portal":
			fmt.Fprintf(w, `<WISPAccessGatewayParam><Redirect><LoginURL>%s/login</LoginURL></Redirect></WISPAccessGatewayParam>`, srvURL(r))
		case "/login":
			require.NoError(t, r.ParseForm())
			gotUser = r.PostForm.Get("LoginUsername")
			gotPass = r.PostForm.Get("LoginPassword")
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	spec := PortalRecipeSpec{
		ID:          "wispr",
		Name:        "wispr",
		Credentials: true,
		Flow:        PortalRecipeFlow{Type: "http-api", Steps: []PortalRecipeStep{{Type: "wispr", URL: srv.URL + "/portal"}}},
		AllowHosts:  []string{strings.TrimPrefix(srv.URL, "http://")},
	}
	result := ExecutePortalRecipe(context.Background(), PortalBinding{}, spec, PortalRecipeCredentials{Username: "room304", Password: "guest"}, recipeProbeOK)
	require.True(t, result.OK, result.Reason)
	require.Equal(t, "room304", gotUser)
	require.Equal(t, "guest", gotPass)
}

func TestLoadPortalRecipeRejectsIdentityStrategy(t *testing.T) {
	raw := []byte(`{
		"id":"bad","name":"bad","schema_version":1,
		"match":{"host_regex":"example"},
		"identity":{"strategy":"mac-rotate"},
		"flow":{"type":"http-api","steps":[{"type":"request","url":"http://example.test/"}]}
	}`)
	_, err := LoadPortalRecipe(raw)
	require.Error(t, err)
	require.Contains(t, err.Error(), "identity strategies are not supported")
}

func TestExecutePortalRecipeRefusesPaid(t *testing.T) {
	spec := PortalRecipeSpec{ID: "paid", Name: "paid", Paid: true, Flow: PortalRecipeFlow{Type: "http-api", Steps: []PortalRecipeStep{{Type: "request", URL: "http://example.test/"}}}}
	result := ExecutePortalRecipe(context.Background(), PortalBinding{}, spec, PortalRecipeCredentials{}, recipeProbeOK)
	require.False(t, result.OK)
	require.Equal(t, "paid-recipe-requires-human", result.Reason)
}

func TestExecutePortalRecipeRefusesCrossHostRequest(t *testing.T) {
	var evilHits atomic.Int32
	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		evilHits.Add(1)
	}))
	defer evil.Close()
	portal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer portal.Close()

	spec := PortalRecipeSpec{ID: "cross", Name: "cross", Flow: PortalRecipeFlow{Type: "http-api", Steps: []PortalRecipeStep{
		{Type: "request", URL: portal.URL + "/first"},
		{Type: "request", URL: evil.URL + "/steal"},
	}}}
	result := ExecutePortalRecipe(context.Background(), PortalBinding{}, spec, PortalRecipeCredentials{}, recipeProbeOK)
	require.False(t, result.OK)
	require.Zero(t, evilHits.Load())
}

func TestExecutePortalRecipeRefusesPaymentForm(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<form action="/pay" method="post">
			<input type="password" name="pass">
			<input name="cardnumber">
		</form>`)
	}))
	defer srv.Close()

	spec := PortalRecipeSpec{ID: "pay", Name: "pay", Flow: PortalRecipeFlow{Type: "http-api", Steps: []PortalRecipeStep{{Type: "form_generic", URL: srv.URL + "/portal"}}}}
	result := ExecutePortalRecipe(context.Background(), PortalBinding{}, spec, PortalRecipeCredentials{Username: "u", Password: "p"}, recipeProbeOK)
	require.False(t, result.OK)
	require.Contains(t, result.Reason, "payment form")
}

func TestExecutePortalRecipeAttemptsStopsAtFirstOnlineCandidate(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path == "/a" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	spec := PortalRecipeSpec{ID: "attempts", Name: "attempts", Flow: PortalRecipeFlow{Type: "http-api", Steps: []PortalRecipeStep{{
		Type: "attempts",
		Candidates: [][]PortalRecipeStep{
			{{Type: "request", URL: srv.URL + "/a"}},
			{{Type: "request", URL: srv.URL + "/b"}},
		},
	}}}}
	result := ExecutePortalRecipe(context.Background(), PortalBinding{}, spec, PortalRecipeCredentials{}, recipeProbeOK)
	require.True(t, result.OK, result.Reason)
	require.Equal(t, int32(2), hits.Load())
}

func TestExecutePortalRecipeSkipIfStopsFlow(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"session":1}`)
	}))
	defer srv.Close()

	spec := PortalRecipeSpec{ID: "skip", Name: "skip", Flow: PortalRecipeFlow{Type: "http-api", Steps: []PortalRecipeStep{
		{Type: "request", URL: srv.URL + "/state", Vars: map[string]string{"session": "$.session"}},
		{Type: "skip_if", Var: "session", Min: 1},
		{Type: "request", URL: srv.URL + "/must-not-run"},
	}}}
	result := ExecutePortalRecipe(context.Background(), PortalBinding{}, spec, PortalRecipeCredentials{}, recipeProbeOK)
	require.True(t, result.OK, result.Reason)
	require.Equal(t, "satisfied", result.Reason)
}

func srvURL(r *http.Request) string {
	return "http://" + r.Host
}
