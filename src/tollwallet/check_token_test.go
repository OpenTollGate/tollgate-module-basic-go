package tollwallet

import (
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/OpenTollGate/gonuts-tollgate/cashu"
	gonutscrypto "github.com/OpenTollGate/gonuts-tollgate/crypto"
	"github.com/decred/dcrd/dcrec/secp256k1/v4"
)

// newCheckStateMint stands up a NUT-07 endpoint that reports the given
// state ("UNSPENT", "SPENT", "PENDING") per proof Y; Ys without an entry
// default to "UNSPENT".
func newCheckStateMint(t *testing.T, states map[string]string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/checkstate", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Ys []string `json:"Ys"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		type proofStateJSON struct {
			Y     string `json:"Y"`
			State string `json:"state"`
		}
		resp := struct {
			States []proofStateJSON `json:"states"`
		}{}
		for _, y := range req.Ys {
			state, ok := states[y]
			if !ok {
				state = "UNSPENT"
			}
			resp.States = append(resp.States, proofStateJSON{Y: y, State: state})
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

func proofYHex(t *testing.T, secret string) string {
	t.Helper()
	y, err := gonutscrypto.HashToCurve([]byte(secret))
	if err != nil {
		t.Fatalf("hash to curve: %v", err)
	}
	return hex.EncodeToString(y.SerializeCompressed())
}

func buildTestToken(t *testing.T, mintURL string, secrets []string) string {
	t.Helper()
	priv, err := secp256k1.GeneratePrivateKey()
	if err != nil {
		t.Fatalf("generate private key: %v", err)
	}
	pubHex := hex.EncodeToString(priv.PubKey().SerializeCompressed())

	proofs := cashu.Proofs{}
	for i, secret := range secrets {
		proofs = append(proofs, cashu.Proof{
			Amount: uint64(1 << i),
			Id:     "abababababababababababababababab",
			Secret: secret,
			C:      pubHex,
		})
	}
	token, err := cashu.NewTokenV4(proofs, mintURL, cashu.Sat, true)
	if err != nil {
		t.Fatalf("build token: %v", err)
	}
	tokenStr, err := token.Serialize()
	if err != nil {
		t.Fatalf("serialize token: %v", err)
	}
	return tokenStr
}

func TestCheckTokenSpendable_AllProofsUnspent(t *testing.T) {
	server := newCheckStateMint(t, nil)
	token := buildTestToken(t, server.URL, []string{"recover-secret-1", "recover-secret-2"})

	spendable, err := CheckTokenSpendable(token)
	if err != nil {
		t.Fatalf("CheckTokenSpendable: %v", err)
	}
	if !spendable {
		t.Fatal("token with all proofs UNSPENT must be spendable")
	}
}

func TestCheckTokenSpendable_AnyProofSpent(t *testing.T) {
	spentSecret := "recover-spent-secret"
	server := newCheckStateMint(t, map[string]string{
		proofYHex(t, spentSecret): "SPENT",
	})
	token := buildTestToken(t, server.URL, []string{"recover-live-secret", spentSecret})

	spendable, err := CheckTokenSpendable(token)
	if err != nil {
		t.Fatalf("CheckTokenSpendable: %v", err)
	}
	if spendable {
		t.Fatal("token with a SPENT proof must not be reported spendable")
	}
}

func TestCheckTokenSpendable_PendingProofIsNotSpendable(t *testing.T) {
	pendingSecret := "recover-pending-secret"
	server := newCheckStateMint(t, map[string]string{
		proofYHex(t, pendingSecret): "PENDING",
	})
	token := buildTestToken(t, server.URL, []string{pendingSecret})

	// PENDING means a redemption is in flight and its outcome is not yet
	// determined: the only honest verdict is an error (unknown) so callers
	// re-check later — never a definitive "not spendable"/"spent".
	_, err := CheckTokenSpendable(token)
	if err == nil {
		t.Fatal("PENDING proof must yield an error (unknown), not a definitive not-spendable verdict")
	}
	if !strings.Contains(err.Error(), "PENDING") {
		t.Fatalf("error should name the PENDING state, got: %v", err)
	}
}

func TestCheckTokenSpendable_ShortStateResponseIsAnError(t *testing.T) {
	// A mint answering 200 but covering fewer Ys than asked must not be
	// treated as all-UNSPENT: partial attestation is not attestation.
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/checkstate", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Ys []string `json:"Ys"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		type proofStateJSON struct {
			Y     string `json:"Y"`
			State string `json:"state"`
		}
		resp := struct {
			States []proofStateJSON `json:"states"`
		}{}
		for _, y := range req.Ys[:len(req.Ys)-1] { // drop the last Y
			resp.States = append(resp.States, proofStateJSON{Y: y, State: "UNSPENT"})
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	token := buildTestToken(t, server.URL, []string{"short-state-secret-1", "short-state-secret-2"})

	_, err := CheckTokenSpendable(token)
	if err == nil {
		t.Fatal("short states response must yield an error, not a spendable verdict")
	}
	if !strings.Contains(err.Error(), "of") || !strings.Contains(err.Error(), "2") {
		t.Fatalf("error should report the coverage gap, got: %v", err)
	}
}

func TestCheckTokenSpendable_MintUnreachable(t *testing.T) {
	server := newCheckStateMint(t, nil)
	url := server.URL
	server.Close()

	token := buildTestToken(t, url, []string{"recover-secret"})
	if _, err := CheckTokenSpendable(token); err == nil {
		t.Fatal("unreachable mint must yield an error, not a verdict")
	}
}

func TestCheckTokenSpendable_InvalidTokenString(t *testing.T) {
	if _, err := CheckTokenSpendable("not-a-cashu-token"); err == nil {
		t.Fatal("invalid token string must yield an error")
	}
}

func TestGonutsWallet_CheckTokenSpendable_Delegates(t *testing.T) {
	server := newCheckStateMint(t, nil)
	token := buildTestToken(t, server.URL, []string{"recover-delegate-secret"})

	w := &GonutsWallet{}
	spendable, err := w.CheckTokenSpendable(token)
	if err != nil {
		t.Fatalf("GonutsWallet.CheckTokenSpendable: %v", err)
	}
	if !spendable {
		t.Fatal("expected spendable via GonutsWallet delegation")
	}
}
