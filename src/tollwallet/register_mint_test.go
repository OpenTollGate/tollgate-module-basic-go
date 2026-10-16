package tollwallet

import (
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
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

// newTogglableMint is newTestMint with a health switch: while healthy is false
// every keyset request answers 503, so a caller can model "down at boot, up at
// first use" against the real gonuts registration path.
func newTogglableMint(t *testing.T) (baseURL string, setHealthy func(bool)) {
	t.Helper()
	priv, err := secp256k1.GeneratePrivateKey()
	if err != nil {
		t.Fatalf("generate private key: %v", err)
	}
	pubKeyHex := hex.EncodeToString(priv.PubKey().SerializeCompressed())
	// The #705 guard (gonuts v0.14.0) refuses a mint whose advertised keyset
	// id is not the NUT-02 derivation of its published keys — derive it for
	// real so the fixture registers like a genuine mint.
	keysetID := mustDeriveKeysetID(`{"1":"` + pubKeyHex + `"}`)
	var healthy atomic.Bool
	healthy.Store(true)

	keys := func(w http.ResponseWriter, r *http.Request) {
		if !healthy.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"keysets":[{"id":"` + keysetID + `","unit":"sat","keys":{"1":"` + pubKeyHex + `"},"active":true,"input_fee_ppk":0}]}`))
	}
	keysets := func(w http.ResponseWriter, r *http.Request) {
		if !healthy.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"keysets":[{"id":"` + keysetID + `","unit":"sat","active":true,"input_fee_ppk":0}]}`))
	}
	mux := http.NewServeMux()
	for _, prefix := range []string{"/v1", "/Bitcoin/v1"} {
		mux.HandleFunc(prefix+"/keys", keys)
		mux.HandleFunc(prefix+"/keysets", keysets)
	}
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server.URL + "/Bitcoin", healthy.Store
}

// TestRegisterMintDefersThenRecovers pins the INTEGRATION claim this change
// rests on: a mint that is DOWN at boot is left unregistered (deferred, not
// blocking), and once it is reachable again ensureMintRegistered — the path
// Receive takes on first use — registers it through the real gonuts wallet.
func TestRegisterMintDefersThenRecovers(t *testing.T) {
	healthyServer, _, _ := newTestMint(t) // the healthy first mint New() loads with
	upURL := healthyServer.URL + "/Bitcoin"

	mintURL, setHealthy := newTogglableMint(t)
	setHealthy(false) // DOWN at boot

	dir := t.TempDir()
	w, err := New(dir, []string{upURL, mintURL}, false)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	canonical := normalizeMintURL(mintURL)
	if w.registeredMints[canonical] {
		t.Fatalf("a mint that is DOWN at boot must NOT be registered")
	}

	// It comes back — "up at first use": Receive calls ensureMintRegistered.
	setHealthy(true)
	w.ensureMintRegistered(mintURL)
	if !w.registeredMints[canonical] {
		t.Fatalf("ensureMintRegistered must register a mint that recovered")
	}
}
