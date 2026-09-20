package tollwallet

import (
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/OpenTollGate/gonuts-tollgate/cashu"
	"github.com/OpenTollGate/gonuts-tollgate/wallet"
	"github.com/decred/dcrd/dcrec/secp256k1/v4"
)

// newTestMint stands up a hermetic NUT-01/NUT-02 endpoint set serving one
// active "sat" keyset — the minimum a real gonuts wallet.AddMint needs to
// succeed. This lets the duplicate-alias reproduction below run through
// the real registration path (TollWallet.New → registerMint →
// wallet.AddMint) with no external network.
func newTestMint(t *testing.T) (server *httptest.Server, keysetID, pubKeyHex string) {
	t.Helper()

	priv, err := secp256k1.GeneratePrivateKey()
	if err != nil {
		t.Fatalf("generate private key: %v", err)
	}
	pubKeyHex = hex.EncodeToString(priv.PubKey().SerializeCompressed())
	keysetID = strings.Repeat("ab", 16) // hex-decodable, as AddMint requires

	mux := http.NewServeMux()
	// gonuts requests {mint-base}/v1/keys, i.e. with the mint's path
	// prefix (/Bitcoin) included. Both aliases strip only the trailing
	// slash client-side, so they hit these same paths.
	serveKeys := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"keysets":[{"id":"` + keysetID + `","unit":"sat","keys":{"1":"` + pubKeyHex + `"},"active":true,"input_fee_ppk":0}]}`))
	}
	serveKeysets := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"keysets":[{"id":"` + keysetID + `","unit":"sat","active":true,"input_fee_ppk":0}]}`))
	}
	for _, prefix := range []string{"/v1", "/Bitcoin/v1"} {
		mux.HandleFunc(prefix+"/keys", serveKeys)
		mux.HandleFunc(prefix+"/keysets", serveKeysets)
	}
	server = httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server, keysetID, pubKeyHex
}

// TestDuplicateMintAliases_SingleRegistryEntry_Issue375 reproduces the
// mint-identity defect that triggers the fund loss in issue #375, through
// the real registration path:
//
// The operator config once contained https://mint/Bitcoin/ (trailing
// slash) and later https://mint/Bitcoin. Both aliases address the SAME
// physical mint (identical keysets). The wallet must register ONE
// logical mint, not two.
//
// A 50-sat proof exists on the mint's keyset. With a duplicate registry
// the balance is reported under BOTH aliases ("phantom duplicate
// balance"), which is what makes the CLI drain loop hit the same mint
// twice and lose the first drain's token (see the cli package regression
// tests for that half of the bug).
func TestDuplicateMintAliases_SingleRegistryEntry_Issue375(t *testing.T) {
	server, keysetID, pubKeyHex := newTestMint(t)
	aliasNoSlash := server.URL + "/Bitcoin"
	aliasWithSlash := server.URL + "/Bitcoin/"

	dir := t.TempDir()

	// Seed one 50-sat proof on the mint's keyset before creating the
	// wallet, exactly as if it had been received earlier.
	seedDB, err := wallet.InitStorage(dir)
	if err != nil {
		t.Fatalf("init storage: %v", err)
	}
	if err := seedDB.SaveProofs(cashu.Proofs{
		{Amount: 50, Id: keysetID, Secret: "issue375-seed-secret", C: pubKeyHex},
	}); err != nil {
		t.Fatalf("seed proofs: %v", err)
	}
	if err := seedDB.Close(); err != nil {
		t.Fatalf("close seed db: %v", err)
	}

	tw, err := New(dir, []string{aliasNoSlash, aliasWithSlash}, false)
	if err != nil {
		t.Fatalf("create wallet: %v", err)
	}
	defer tw.Shutdown()

	// registeredMints must hold a single entry: both aliases are one mint.
	if got := len(tw.registeredMints); got != 1 {
		t.Errorf("registeredMints has %d entries for one logical mint (aliases %q, %q); want 1", got, aliasNoSlash, aliasWithSlash)
	}

	// The per-mint balance view must not contain a phantom duplicate.
	balances := tw.GetAllMintBalances()
	if got := len(balances); got != 1 {
		t.Errorf("GetAllMintBalances returned %d entries for one logical mint: %v; want 1 (phantom duplicate balance)", got, balances)
	}
	for alias, bal := range balances {
		if bal != 50 {
			t.Errorf("balance for %s = %d, want 50", alias, bal)
		}
	}

	// Balance lookup must work through either alias.
	if got := tw.GetBalanceByMint(aliasNoSlash); got != 50 {
		t.Errorf("GetBalanceByMint(%q) = %d, want 50", aliasNoSlash, got)
	}
	if got := tw.GetBalanceByMint(aliasWithSlash); got != 50 {
		t.Errorf("GetBalanceByMint(%q) = %d, want 50 (alias of the same logical mint)", aliasWithSlash, got)
	}
}

// TestRegisterMint_CanonicalKey_PreventsDuplicateRegistration verifies the
// registration path itself: once one alias of a logical mint is registered,
// registering a second spelling (trailing slash) is a no-op — neither a
// second registeredMints key nor a second entry in the underlying gonuts
// wallet. A genuinely different mint still registers normally.
func TestRegisterMint_CanonicalKey_PreventsDuplicateRegistration(t *testing.T) {
	server, keysetID, pubKeyHex := newTestMint(t)
	otherServer, _, _ := newTestMint(t)
	aliasNoSlash := server.URL + "/Bitcoin"
	aliasWithSlash := server.URL + "/Bitcoin/"
	otherMint := otherServer.URL + "/Bitcoin"

	dir := t.TempDir()
	seedDB, err := wallet.InitStorage(dir)
	if err != nil {
		t.Fatalf("init storage: %v", err)
	}
	if err := seedDB.SaveProofs(cashu.Proofs{
		{Amount: 50, Id: keysetID, Secret: "register-dedup-secret", C: pubKeyHex},
	}); err != nil {
		t.Fatalf("seed proofs: %v", err)
	}
	seedDB.Close()

	tw, err := New(dir, []string{aliasNoSlash}, false)
	if err != nil {
		t.Fatalf("create wallet: %v", err)
	}
	defer tw.Shutdown()

	tw.registerMint(aliasWithSlash)
	if got := len(tw.registeredMints); got != 1 {
		t.Errorf("registeredMints has %d entries after registering both aliases; want 1", got)
	}
	if got := len(tw.wallet.GetBalanceByMints()); got != 1 {
		t.Errorf("underlying wallet holds %d mint entries after both aliases; want 1", got)
	}

	tw.registerMint(otherMint)
	if got := len(tw.registeredMints); got != 2 {
		t.Errorf("registeredMints has %d entries after registering a second, different mint; want 2", got)
	}
}

// TestGetAllMintBalances_MergesLegacyAliasEntries_Issue375 exercises the
// read-side safety net: a wallet whose underlying gonuts state ALREADY
// holds one logical mint under two spellings (created by an older
// registration path — exactly the installs #375 left behind) must not
// surface a phantom duplicate through GetAllMintBalances. The second
// alias is injected below the registration layer on purpose:
// registerMint canonicalizes and would never create it, so only this
// path reaches the merge the CLI drain loop depends on.
func TestGetAllMintBalances_MergesLegacyAliasEntries_Issue375(t *testing.T) {
	server, keysetID, pubKeyHex := newTestMint(t)
	aliasNoSlash := server.URL + "/Bitcoin"
	aliasWithSlash := server.URL + "/Bitcoin/"

	dir := t.TempDir()

	seedDB, err := wallet.InitStorage(dir)
	if err != nil {
		t.Fatalf("init storage: %v", err)
	}
	if err := seedDB.SaveProofs(cashu.Proofs{
		{Amount: 50, Id: keysetID, Secret: "issue375-legacy-secret", C: pubKeyHex},
	}); err != nil {
		t.Fatalf("seed proofs: %v", err)
	}
	if err := seedDB.Close(); err != nil {
		t.Fatalf("close seed db: %v", err)
	}

	tw, err := New(dir, []string{aliasNoSlash}, false)
	if err != nil {
		t.Fatalf("create wallet: %v", err)
	}
	defer tw.Shutdown()

	// Inject the legacy duplicate below the canonicalizing registration
	// layer, as an older TollWallet version would have left it.
	if _, err := tw.wallet.AddMint(aliasWithSlash); err != nil {
		t.Fatalf("inject legacy alias entry: %v", err)
	}

	// Precondition: the underlying wallet really holds two spellings —
	// otherwise this test exercises nothing (the merge is load-bearing
	// only when there is something to merge).
	raw := tw.wallet.GetBalanceByMints()
	if len(raw) != 2 {
		t.Fatalf("precondition: underlying wallet holds %d mint entries after alias injection; want 2 (%v)", len(raw), raw)
	}

	balances := tw.GetAllMintBalances()
	if got := len(balances); got != 1 {
		t.Errorf("GetAllMintBalances returned %d entries for a legacy-aliased single mint: %v; want 1 (read-side merge must repair existing installs)", got, balances)
	}
	for alias, bal := range balances {
		if bal != 50 {
			t.Errorf("merged balance for %s = %d, want 50 (max of the alias group, not the sum)", alias, bal)
		}
	}
}
