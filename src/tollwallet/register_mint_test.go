package tollwallet

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// registerMint now guards AddMint with OUR bounded keyset check, because
// gonuts' AddMint has no HTTP timeout and a mint that accepts the connection and
// never answers stalled the whole wallet construction (the 113-347 s the module
// measured). These cases pin that guard's behaviour.
func TestMintServesKeysets(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/keysets" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"keysets":[{"id":"x","unit":"sat"}]}`))
	}))
	defer ok.Close()
	if !mintServesKeysets(ok.URL, 2*time.Second) {
		t.Errorf("a mint that serves keysets must be accepted")
	}

	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"keysets":[]}`))
	}))
	defer empty.Close()
	if mintServesKeysets(empty.URL, 2*time.Second) {
		t.Errorf("an empty keyset list must be rejected (a parked front serves HTML, not keysets)")
	}

	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(800 * time.Millisecond)
		_, _ = w.Write([]byte(`{"keysets":[{"id":"x"}]}`))
	}))
	defer slow.Close()
	if mintServesKeysets(slow.URL, 200*time.Millisecond) {
		t.Errorf("a mint that outruns the bound must be rejected — this is the unbounded-construction bug")
	}
}
