package tollwallet

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/OpenTollGate/gonuts-tollgate/cashu"
	"github.com/OpenTollGate/gonuts-tollgate/crypto"
)

// CheckTokenSpent is the NUT-07 evidence seam the merchant's #502 journal
// reconciles on: any-spent ⇒ the payment went through (a receive-swap is
// atomic at the mint), all-unspent ⇒ it never happened, PENDING or fewer
// answers than proofs ⇒ error (undecided, never a guess).

func newCheckStateMint(t *testing.T, states map[string]string) *httptest.Server {
	t.Helper()
	keysetsJSON := `{"keysets":[{"id":"009a1f293253e41e","unit":"sat","active":true,"keys":{"1":"0279be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798"}}]}`
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/keys", "/v1/keysets":
			fmt.Fprint(w, keysetsJSON)
		case "/v1/checkstate":
			var req struct {
				Ys []string `json:"Ys"`
			}
			body := make([]byte, r.ContentLength)
			_, _ = r.Body.Read(body)
			if err := json.Unmarshal(body, &req); err != nil {
				http.Error(w, "bad json", 400)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"states":[`)
			for i, y := range req.Ys {
				if i > 0 {
					fmt.Fprint(w, ",")
				}
				state, ok := states[y]
				if !ok {
					state = "UNSPENT"
				}
				fmt.Fprintf(w, `{"Y":%q,"state":%q}`, y, state)
			}
			fmt.Fprint(w, `]}`)
		default:
			http.Error(w, "not found", 404)
		}
	}))
}

func checkStateProof(t *testing.T, secret string) cashu.Proof {
	t.Helper()
	// The proof's C is never read by checkstate; a valid point keeps the
	// token well-formed.
	return cashu.Proof{
		Amount: 1,
		Id:     "009a1f293253e41e",
		Secret: secret,
		C:      "0279be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798",
	}
}

func TestCheckTokenSpent_AllUnspentIsFalse(t *testing.T) {
	mint := newCheckStateMint(t, nil)
	defer mint.Close()

	tw, err := New(t.TempDir(), []string{mint.URL}, false)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer tw.Shutdown()

	proof := checkStateProof(t, "checkstate-unspent-secret")
	token, err := cashu.NewTokenV3(cashu.Proofs{proof}, mint.URL, cashu.Sat, false)
	if err != nil {
		t.Fatal(err)
	}

	spent, err := tw.CheckTokenSpent(token)
	if err != nil {
		t.Fatalf("CheckTokenSpent: %v", err)
	}
	if spent {
		t.Fatal("all-unspent must answer false")
	}
}

func TestCheckTokenSpent_AnySpentIsTrue(t *testing.T) {
	spentSecret := "checkstate-spent-secret"
	Y, err := crypto.HashToCurve([]byte(spentSecret))
	if err != nil {
		t.Fatal(err)
	}
	mint := newCheckStateMint(t, map[string]string{hex.EncodeToString(Y.SerializeCompressed()): "SPENT"})
	defer mint.Close()

	tw, err := New(t.TempDir(), []string{mint.URL}, false)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer tw.Shutdown()

	proof := checkStateProof(t, spentSecret)
	token, err := cashu.NewTokenV3(cashu.Proofs{proof}, mint.URL, cashu.Sat, false)
	if err != nil {
		t.Fatal(err)
	}

	spent, err := tw.CheckTokenSpent(token)
	if err != nil {
		t.Fatalf("CheckTokenSpent: %v", err)
	}
	if !spent {
		t.Fatal("one spent proof must answer true — a receive-swap is atomic at the mint")
	}
}

func TestCheckTokenSpent_PendingIsAnErrorNeverAGuess(t *testing.T) {
	pendingSecret := "checkstate-pending-secret"
	Y, err := crypto.HashToCurve([]byte(pendingSecret))
	if err != nil {
		t.Fatal(err)
	}
	mint := newCheckStateMint(t, map[string]string{hex.EncodeToString(Y.SerializeCompressed()): "PENDING"})
	defer mint.Close()

	tw, err := New(t.TempDir(), []string{mint.URL}, false)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer tw.Shutdown()

	proof := checkStateProof(t, pendingSecret)
	token, err := cashu.NewTokenV3(cashu.Proofs{proof}, mint.URL, cashu.Sat, false)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := tw.CheckTokenSpent(token); err == nil {
		t.Fatal("a PENDING state must be an error — the outcome is undetermined and a guess either loses the customer's note or the operator's value")
	}
}
