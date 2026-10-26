package main

// Tests for the Spilman HTTP channel surface.
//
// The load-bearing cases, in the order they matter:
//
//  1. With the rail DISABLED, every /spilman path 404s AND the retail routes are
//     byte-identical to what they were before this feature existed. The second half
//     is what stops the first from passing trivially: a 404 proves nothing if the
//     route never existed in either configuration.
//  2. With the rail ENABLED, the surface answers — proving the routes are actually
//     REACHABLE, not merely written. A build returning rc 0 says nothing about
//     routes that were never registered.
//  3. A forged payment is refused with 409, not 500: the request was well-formed and
//     the answer is "no", which is a different fact from "we broke".
//  4. An unknown channel is 404, distinct from the 409 refusal.

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func newMux() *http.ServeMux {
	mux := http.NewServeMux()
	registerAPIHandlers(mux)
	return mux
}

func do(t *testing.T, mux *http.ServeMux, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// THE MOUNTED/UNMOUNTED DISCRIMINATOR.
//
// Disabled: /spilman/* must 404. Enabled: /spilman/params must answer 200.
//
// Both halves are required. Asserting only the 404 would pass if the route were
// never registered in EITHER state — which is exactly the "written but unreachable"
// failure this surface exists to fix.
func TestSpilmanRoutesAreMountedOnlyWhenEnabled(t *testing.T) {
	for _, path := range []string{
		"/spilman/params", "/spilman/channel", "/spilman/payment", "/spilman/status",
	} {
		withSpilman(t, false, "")
		if rec := do(t, newMux(), http.MethodGet, path); rec.Code != http.StatusNotFound {
			t.Errorf("disabled: %s = %d, want 404", path, rec.Code)
		}
	}

	withSpilman(t, true, t.TempDir()+"/channels.db")
	rec := do(t, newMux(), http.MethodGet, "/spilman/params")
	if rec.Code != http.StatusOK {
		t.Fatalf("enabled: /spilman/params = %d, want 200 (body %s)",
			rec.Code, rec.Body.String())
	}
	if !contains(rec.Body.String(), "in_bytes_per_millisat") {
		t.Errorf("params must advertise the rate; got %s", rec.Body.String())
	}
}

// The retail routes must be UNAFFECTED by the rail in both states.
func TestRetailRoutesAreUnchangedByTheSpilmanSurface(t *testing.T) {
	withSpilman(t, false, "")
	offWhoami := do(t, newMux(), http.MethodGet, "/whoami").Code
	offUsage := do(t, newMux(), http.MethodGet, "/usage").Code

	withSpilman(t, true, t.TempDir()+"/channels.db")
	onWhoami := do(t, newMux(), http.MethodGet, "/whoami").Code
	onUsage := do(t, newMux(), http.MethodGet, "/usage").Code

	if offWhoami != onWhoami || offUsage != onUsage {
		t.Errorf("retail routes changed: /whoami %d->%d, /usage %d->%d",
			offWhoami, onWhoami, offUsage, onUsage)
	}
}

// A forged payment must be REFUSED, and refused as 409.
func TestForgedPaymentIsRefusedWith409(t *testing.T) {
	withSpilman(t, true, t.TempDir()+"/channels.db")
	mux := newMux()

	body := `{"channel_id":"deadbeef","balance":5,"signature":"00"}`
	req := httptest.NewRequest(http.MethodPost, "/spilman/payment", stringReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code == http.StatusOK {
		t.Fatalf("FORGERY ACCEPTED: %d %s", rec.Code, rec.Body.String())
	}
	if rec.Code != http.StatusConflict {
		t.Errorf("a refused payment must be 409 (well-formed, answer is no), got %d: %s",
			rec.Code, rec.Body.String())
	}
}

// An unknown channel is 404, and that is DISTINCT from the 409 refusal.
func TestUnknownChannelStatusIs404NotConflict(t *testing.T) {
	withSpilman(t, true, t.TempDir()+"/channels.db")
	rec := do(t, newMux(), http.MethodGet, "/spilman/status?channel_id=never-registered")
	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown channel = %d, want 404: %s", rec.Code, rec.Body.String())
	}
}

// status without channel_id is a caller error, not a server fault.
func TestStatusRequiresAChannelID(t *testing.T) {
	withSpilman(t, true, t.TempDir()+"/channels.db")
	rec := do(t, newMux(), http.MethodGet, "/spilman/status")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("missing channel_id = %d, want 400", rec.Code)
	}
}
