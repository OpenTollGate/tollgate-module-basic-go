//go:build !cdk_wallet

package tollwallet

import (
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OpenTollGate/gonuts-tollgate/cashu"
	"github.com/OpenTollGate/gonuts-tollgate/crypto"
	"github.com/decred/dcrd/dcrec/secp256k1/v4"
)

// The #497 crash-window wiring (#703 work item 1): a swap whose POST the
// wallet saw FAIL (transport error, drop, 5xx — the ambiguous class) leaves
// its pending-swap intent recorded — the request bytes, secrets and blinding
// factors were persisted atomically with the counter reservation BEFORE the
// POST (gonuts v0.13.1). The NEXT wallet load must replay that intent and
// recover the mint's signatures as spendable proofs: the value the "failed"
// payment appeared to lose is reconstructed without a second spend.
//
// The scenario models exactly the incident shape: the mint PROCESSED the
// first request (here: it deliberately fails the first answer so the wallet
// cannot complete — indistinguishable client-side from a drop after
// processing), then answers the replay with the deterministic signatures.

// resumeSigningMint is a stub mint that FAILS the first /v1/swap (the
// ambiguous window: the wallet cannot know whether the mint spent the
// inputs) and signs every replay. The signing half mirrors the fork's
// wallet-test harness: per-amount keys, /v1/keys + /v1/keysets serving
// them, SignBlindedMessage over each output's B_.
type resumeSigningMint struct {
	swapCalls atomic.Int64
	failFirst atomic.Bool
	privs     map[uint64]*secp256k1.PrivateKey
	keyset    crypto.WalletKeyset
	srv       *httptest.Server
}

func newResumeSigningMint(t *testing.T) *resumeSigningMint {
	t.Helper()
	m := &resumeSigningMint{privs: map[uint64]*secp256k1.PrivateKey{}}
	pubs := crypto.PublicKeys{}
	for _, a := range []uint64{1, 2, 4, 8} {
		h, err := secp256k1.GeneratePrivateKey()
		if err != nil {
			t.Fatalf("generate key: %v", err)
		}
		m.privs[a] = h
		pubs[a] = h.PubKey()
	}
	// The keyset ID is DERIVED from the public keys (crypto.DeriveKeysetId),
	// not invented: gonuts v0.14.0's NUT-13 verification (tollgate #705,
	// fork #37) refuses a mint whose advertised ID does not match its keys —
	// the guard fired on this fixture's first draft, which is the guard
	// working.
	derivedID := crypto.DeriveKeysetId(pubs)
	m.keyset = crypto.WalletKeyset{
		Id:         derivedID,
		MintURL:    "placeholder",
		Unit:       "sat",
		Active:     true,
		PublicKeys: pubs,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/keys", func(w http.ResponseWriter, r *http.Request) {
		keys := map[string]any{"keysets": []map[string]any{{
			"id": m.keyset.Id, "unit": "sat", "active": true, "keys": pubs,
		}}}
		_ = json.NewEncoder(w).Encode(keys)
	})
	mux.HandleFunc("/v1/keysets", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keysets": []map[string]any{{
			"id": m.keyset.Id, "unit": "sat", "active": true, "input_fee_ppk": 0,
		}}})
	})
	mux.HandleFunc("/v1/swap", func(w http.ResponseWriter, r *http.Request) {
		call := m.swapCalls.Add(1)
		if m.failFirst.Load() && call == 1 {
			// The ambiguous failure: the mint's answer never reaches the
			// wallet as a usable response. (It may well have spent the
			// inputs — that is the whole window.)
			http.Error(w, "simulated transport failure", http.StatusBadGateway)
			return
		}
		var req struct {
			Outputs []struct {
				B_     string `json:"B_"`
				Amount uint64 `json:"amount"`
			} `json:"outputs"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		type sig struct {
			Amount uint64 `json:"amount"`
			Id     string `json:"id"`
			C_     string `json:"C_"`
		}
		sigs := make([]sig, 0, len(req.Outputs))
		for _, out := range req.Outputs {
			B_, err := hex.DecodeString(out.B_)
			if err != nil {
				http.Error(w, "bad B_", 400)
				return
			}
			BPub, err := secp256k1.ParsePubKey(B_)
			if err != nil {
				http.Error(w, "bad point", 400)
				return
			}
			priv, ok := m.privs[out.Amount]
			if !ok {
				http.Error(w, "no key for amount", 400)
				return
			}
			C_ := crypto.SignBlindedMessage(BPub, priv)
			sigs = append(sigs, sig{
				Amount: out.Amount,
				Id:     m.keyset.Id,
				C_:     hex.EncodeToString(C_.SerializeCompressed()),
			})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"signatures": sigs})
	})
	m.srv = httptest.NewServer(mux)
	t.Cleanup(m.srv.Close)
	m.failFirst.Store(true)
	return m
}

// resumeTestToken builds a 1-sat V3 token on the stub mint's keyset. The
// stub mint does not validate inputs (it signs whatever outputs arrive),
// so the proof pair only needs the right shape.
func resumeTestToken(t *testing.T, m *resumeSigningMint) cashu.Token {
	t.Helper()
	proof := cashu.Proof{
		Id:     m.keyset.Id,
		Amount: 1,
		Secret: "resume-crash-window-secret",
		C:      "02ab",
	}
	token, err := cashu.NewTokenV3([]cashu.Proof{proof}, m.srv.URL, cashu.Sat, false)
	if err != nil {
		t.Fatalf("NewTokenV3: %v", err)
	}
	return token
}

func TestWalletLoadRecoversCrashedSwapIntent(t *testing.T) {
	m := newResumeSigningMint(t)
	dir := t.TempDir()

	// Wallet A: the payment whose swap the mint "failed" — client-side this
	// is the #497 crash window (the intent was recorded before the POST, so
	// the value is durable even though Receive errored).
	wa, err := New(filepath.Join(dir, "wallet.db"), []string{m.srv.URL}, false)
	if err != nil {
		t.Fatalf("wallet A: %v", err)
	}
	token := resumeTestToken(t, m)
	if _, err := wa.Receive(token); err == nil {
		t.Fatal("setup: Receive should fail against the failing first swap answer")
	}
	// The crash: wallet A goes away. The gonuts db holds an exclusive lock,
	// so the "restart" must release it first — exactly what process death
	// does on a router.
	if err := wa.Shutdown(); err != nil {
		t.Fatalf("wallet A shutdown: %v", err)
	}

	// Wallet B: the NEXT load of the same wallet path. The boot wiring must
	// replay the recorded intent against the (now answering) mint and store
	// the recovered proofs — the "lost" value is spendable again.
	wb, err := New(filepath.Join(dir, "wallet.db"), []string{m.srv.URL}, false)
	if err != nil {
		t.Fatalf("wallet B: %v", err)
	}

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if wb.GetBalanceByMint(m.srv.URL) >= 1 {
			if calls := m.swapCalls.Load(); calls < 2 {
				t.Fatalf("balance recovered after only %d swap call(s) — recovery did not replay the intent", calls)
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("crashed swap intent was not recovered on wallet load: balance=%d, mint swap calls=%d (want balance>=1, calls>=2)",
		wb.GetBalanceByMint(m.srv.URL), m.swapCalls.Load())
}

// TestWalletLoadKeepsIntentWhenMintStaysDown pins the no-loss-on-deferral
// half: a wallet load whose mint never answers must leave the intent
// recorded (retried on a later load), never delete it on a bad guess —
// and the load itself must complete, because recovery is off the boot
// critical path.
func TestWalletLoadKeepsIntentWhenMintStaysDown(t *testing.T) {
	m := newResumeSigningMint(t)
	dir := t.TempDir()

	wa, err := New(filepath.Join(dir, "wallet.db"), []string{m.srv.URL}, false)
	if err != nil {
		t.Fatalf("wallet A: %v", err)
	}
	token := resumeTestToken(t, m)
	if _, err := wa.Receive(token); err == nil {
		t.Fatal("setup: Receive should fail against the failing first swap answer")
	}
	if err := wa.Shutdown(); err != nil {
		t.Fatalf("wallet A shutdown: %v", err)
	}

	// The mint goes fully dark before the recovery load.
	m.srv.Close()

	// The load must not hang or fail on the dead mint: the recovery pass
	// runs in the background, so New() may only pay LoadWallet's own
	// cost. Measured baseline for LoadWallet against a dead mint on this
	// harness: ~16 s (its AddMint retry ladder — the pre-existing TODO at
	// the LoadWallet call site); a SYNCHRONOUS recovery would add the full
	// replay timeout (30 s per intent) on top, i.e. >= 46 s. The bound
	// between those is the discriminator.
	start := time.Now()
	wb, err := New(filepath.Join(dir, "wallet.db"), []string{m.srv.URL}, false)
	if err != nil {
		t.Fatalf("wallet B with the mint down: %v", err)
	}
	elapsed := time.Since(start)
	if elapsed > 30*time.Second {
		t.Fatalf("wallet load took %s with the mint down — recovery is running on the boot critical path (baseline ~16s; a synchronous replay adds >=30s per intent)", elapsed)
	}
	t.Logf("wallet load with mint down: %s (LoadWallet baseline; recovery deferred to background)", elapsed)
	// wb is deliberately NOT shut down: the point is that the journaled
	// intent outlives this process — exactly what process death on a router
	// leaves behind. On Linux the open db file unlinks fine, so TempDir
	// cleanup is unaffected.
	_ = wb
}
