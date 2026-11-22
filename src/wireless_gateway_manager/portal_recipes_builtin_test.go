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

func TestBuiltinPurpleRecipeRunsTheMultiStepFlow(t *testing.T) {
	var authHits, planHits, controllerHits atomic.Int32
	var gotEmail, gotCSRF string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/start":
			http.Redirect(w, r, "/access?cmd=login", http.StatusFound)
		case r.URL.Path == "/access" && r.URL.Query().Get("jsrd") != "1":
			fmt.Fprint(w, `<html><script>
				var query = window.location.search.substring(1);
				url = window.location.protocol + '//' + window.location.host + '/access/?' + query + '&jsrd=1';
				window.location.href = url;
			</script></html>`)
		case r.URL.Path == "/access":
			fmt.Fprint(w, `<form action="/access/auth" method="post">
				<input type="email" name="email">
				<input type="hidden" name="form_type" value="LoginForm">
				<input type="hidden" name="csrf_token" value="csrf-abc">
			</form>`)
		case r.URL.Path == "/access/auth":
			authHits.Add(1)
			require.NoError(t, r.ParseForm())
			gotEmail = r.PostForm.Get("email")
			gotCSRF = r.PostForm.Get("csrf_token")
			if r.PostForm.Get("form_type") != "LoginForm" {
				http.Error(w, "wrong form", http.StatusBadRequest)
				return
			}
			http.Redirect(w, r, "/access/check-tiered-bandwidth", http.StatusFound)
		case r.URL.Path == "/access/check-tiered-bandwidth":
			fmt.Fprint(w, `<html><body>Select a plan</body></html>`)
		case r.URL.Path == "/access/paid-bandwidth/purchase":
			planHits.Add(1)
			require.NoError(t, r.ParseForm())
			if r.PostForm.Get("plan") != "739" {
				http.Error(w, "wrong plan", http.StatusBadRequest)
				return
			}
			http.Redirect(w, r, "/access/validate", http.StatusFound)
		case r.URL.Path == "/access/validate":
			http.Redirect(w, r, fmt.Sprintf("http://%s/controller?cmd=authenticate", r.Host), http.StatusFound)
		case r.URL.Path == "/controller":
			controllerHits.Add(1)
			if r.URL.Query().Get("cmd") == "redirect" {
				fmt.Fprint(w, `<html><body>External Welcome Page: success</body></html>`)
				return
			}
			fmt.Fprintf(w, `<html><head><meta http-equiv="refresh" content="0; url=http://%s/controller?cmd=redirect"></head></html>`, r.Host)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	spec := BuiltinPortalRecipes()["man-purple-free"]
	spec.SeedVars = map[string]string{"start_url": srv.URL + "/start"}
	result := ExecutePortalRecipe(context.Background(), PortalBinding{}, spec, PortalRecipeCredentials{
		Username: "guest@example.test",
	}, func(context.Context) bool { return true })

	require.True(t, result.OK, result.Reason)
	require.Equal(t, int32(1), authHits.Load())
	require.Equal(t, int32(1), planHits.Load())
	require.GreaterOrEqual(t, controllerHits.Load(), int32(2))
	require.Equal(t, "guest@example.test", gotEmail)
	require.Equal(t, "csrf-abc", gotCSRF)
}

func TestBuiltinAsk4RecipeUsesTheGuestForm(t *testing.T) {
	var gotFirst, gotSurname, gotToken string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/en/guest-signup":
			if r.Method == http.MethodGet {
				fmt.Fprint(w, `<form action="/en/guest-signup" method="post">
					<input name="personal_details[firstname]">
					<input name="personal_details[surname]">
					<input type="hidden" name="personal_details[_token]" value="tok-1">
				</form>`)
				return
			}
			require.NoError(t, r.ParseForm())
			gotFirst = r.PostForm.Get("personal_details[firstname]")
			gotSurname = r.PostForm.Get("personal_details[surname]")
			gotToken = r.PostForm.Get("personal_details[_token]")
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	spec := BuiltinPortalRecipes()["ask4-guest"]
	spec.SeedVars = map[string]string{"start_url": srv.URL + "/en/guest-signup"}
	spec.AllowHosts = append(spec.AllowHosts, strings.TrimPrefix(srv.URL, "http://"))
	result := ExecutePortalRecipe(context.Background(), PortalBinding{}, spec, PortalRecipeCredentials{}, func(context.Context) bool { return true })

	require.True(t, result.OK, result.Reason)
	require.Equal(t, "TollGate", gotFirst)
	require.Equal(t, "Operator", gotSurname)
	require.Equal(t, "tok-1", gotToken)
}
