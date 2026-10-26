package tollwallet

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/OpenTollGate/gonuts-tollgate/crypto"
	"github.com/OpenTollGate/gonuts-tollgate/wallet"
	"github.com/decred/dcrd/dcrec/secp256k1/v4"
)

// The #818 self-heal contract at the tollwallet seam: a wallet whose DB
// caches keysets the mint has rotated out must still initialize against
// that same REACHABLE mint, instead of failing into degraded mode with
// "error getting keyset from mint: Unknown Keyset" while the mint answers
// every keyset endpoint. Two layers pin it:
//
//  1. the pinned gonuts fix tolerates retired records at load (heal on
//     first attempt);
//  2. the New() init hook retries once when the mint's own active-keyset
//     fetch refuses unknown-keyset but the mint serves /v1/keysets (the
//     residual reachable-mint flake the fork fix cannot absorb).
//
// Everything else — storage errors (#583/#1033 storage family), unreachable
// mints, real refusals — must keep failing exactly as before.

// fixtureKeyset is one honest fixture keyset: fresh keypair, id derived
// from the key (the v0.14.0 forged-id guard requires it).
type fixtureKeyset struct {
	id      string
	keyJSON string
}

func newFixtureKeyset(t *testing.T) fixtureKeyset {
	t.Helper()
	priv, err := secp256k1.GeneratePrivateKey()
	if err != nil {
		t.Fatalf("generate private key: %v", err)
	}
	pubHex := fmt.Sprintf("%x", priv.PubKey().SerializeCompressed())
	return fixtureKeyset{
		id:      crypto.DeriveKeysetId(crypto.PublicKeys{1: priv.PubKey()}),
		keyJSON: `{"1":"` + pubHex + `"}`,
	}
}

// seedStaleWallet writes a pre-rotation wallet.db: the given keyset is the
// stored ACTIVE record with no cached public keys — exactly the shape a
// rotation leaves behind (inactive/rotated records are stored without
// keys, and the mint has since dropped the id entirely).
func seedStaleWallet(t *testing.T, dir, mintURL string, stale fixtureKeyset) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir wallet dir: %v", err)
	}
	db, err := wallet.InitStorage(dir)
	if err != nil {
		t.Fatalf("seed InitStorage: %v", err)
	}
	rec := crypto.WalletKeyset{Id: stale.id, MintURL: mintURL, Unit: "sat", Active: true}
	if err := db.SaveKeyset(&rec); err != nil {
		t.Fatalf("seed SaveKeyset: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("seed close: %v", err)
	}
}

// rotatingMint is the #818 shape: the mint's current keyset set no longer
// contains staleID at all, and the by-id endpoint answers NUT-19 12001
// ("Unknown Keyset", as testnut-class mints spell it).
func rotatingMint(t *testing.T, active, stale fixtureKeyset) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/keysets":
			writeJSON(w, `{"keysets":[{"id":"`+active.id+`","unit":"sat","active":true,"input_fee_ppk":0}]}`)
		case r.URL.Path == "/v1/keys":
			writeJSON(w, `{"keysets":[{"id":"`+active.id+`","unit":"sat","keys":`+active.keyJSON+`,"active":true,"input_fee_ppk":0}]}`)
		case r.URL.Path == "/v1/keys/"+stale.id || r.URL.Path == "/v1/keys/"+active.id:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"detail":"Unknown Keyset","code":12001}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func writeJSON(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, body)
}

// TestNewSelfHealsRetiredKeysets_Issue818: heal on the FIRST load — the
// pinned fork fix skips the retired record, re-fetches the mint's current
// keyset set (NUT-02) and init proceeds.
func TestNewSelfHealsRetiredKeysets_Issue818(t *testing.T) {
	active := newFixtureKeyset(t)
	stale := newFixtureKeyset(t)
	srv := rotatingMint(t, active, stale)

	dir := t.TempDir()
	seedStaleWallet(t, dir, srv.URL, stale)

	tw, err := New(dir, []string{srv.URL}, false)
	if err != nil {
		t.Fatalf("New must self-heal past the retired keyset (#818), got: %v", err)
	}
	if got := tw.GetBalance(); got != 0 {
		t.Fatalf("balance after self-heal: want 0 got %d", got)
	}
	if !tw.registeredMints[normalizeMintURL(srv.URL)] {
		t.Fatal("current mint must be registered after self-heal")
	}
}

// TestNewInitHookRetriesOnce_Issue818: the residual flake — the mint LISTS
// an active keyset but refuses to serve its keys by any endpoint with
// "Unknown Keyset" (broken /v1/keys/{id} AND an /v1/keys response missing
// the advertised id). The first load fails with the exact #818 signature;
// the mint is reachable (serves /v1/keysets), so the init hook re-fetches
// the keyset set and retries ONCE. The mint heals exactly at the hook's
// probe — the 2nd /v1/keysets call, i.e. the boundary between the two
// loads — so the test pins that it is the RETRY that succeeds, not the
// first attempt healing itself.
func TestNewInitHookRetriesOnce_Issue818(t *testing.T) {
	active := newFixtureKeyset(t)
	stale := newFixtureKeyset(t)

	// The mint's keyset list is served from the first call (reachability);
	// the KEYS become available at the hook's probe — /v1/keysets call #2.
	var keysetsCalls atomic.Int32
	var keysServed atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/keysets":
			if keysetsCalls.Add(1) >= 2 {
				keysServed.Store(true) // the mint heals at the retry boundary
			}
			writeJSON(w, `{"keysets":[{"id":"`+active.id+`","unit":"sat","active":true,"input_fee_ppk":0}]}`)
		case r.URL.Path == "/v1/keys":
			if keysServed.Load() {
				writeJSON(w, `{"keysets":[{"id":"`+active.id+`","unit":"sat","keys":`+active.keyJSON+`,"active":true,"input_fee_ppk":0}]}`)
				return
			}
			writeJSON(w, `{"keysets":[]}`) // advertised-but-unserved active id
		case r.URL.Path == "/v1/keys/"+stale.id || r.URL.Path == "/v1/keys/"+active.id:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"detail":"Unknown Keyset","code":12001}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	seedStaleWallet(t, dir, srv.URL, stale)

	tw, err := New(dir, []string{srv.URL}, false)
	if err != nil {
		t.Fatalf("the init hook must retry a reachable mint's unknown-keyset refusal (#818), got: %v", err)
	}
	if got := tw.GetBalance(); got != 0 {
		t.Fatalf("balance after hook retry: want 0 got %d", got)
	}
	if got := keysetsCalls.Load(); got < 3 {
		t.Fatalf("test premise broken: want first load + hook probe + retried load (>=3 /v1/keysets calls), got %d", got)
	}
	if !keysServed.Load() {
		t.Fatal("test premise broken: the mint never healed at the retry boundary")
	}
}

// The heal must NOT paper over other failures: a storage error with a
// reachable mint still fails init (the #583/#1035 storage family is a
// separate slice and must stay observable at this seam).
func TestNewDoesNotSwallowNonKeysetErrors(t *testing.T) {
	active := newFixtureKeyset(t)
	stale := newFixtureKeyset(t)
	srv := rotatingMint(t, active, stale)

	dir := t.TempDir()
	// A file where bolt expects its database directory structure: InitStorage
	// fails with a storage error that is neither network nor unknown-keyset.
	if err := os.WriteFile(filepath.Join(dir, "wallet.db"), []byte("not a bolt db"), 0o600); err != nil {
		t.Fatalf("plant broken db: %v", err)
	}
	if _, err := New(dir, []string{srv.URL}, false); err == nil {
		t.Fatal("a storage error must still fail init — the self-heal is scoped to unknown-keyset refusals")
	}
}
