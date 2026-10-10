package tollwallet

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/OpenTollGate/gonuts-tollgate/cashu"
	"github.com/OpenTollGate/gonuts-tollgate/crypto"
)

// #834: CheckTokenSpent is the evidence seam the #502 reconciliation
// decides abandoned-vs-owed on. It must correlate every answered state to
// a Y we actually asked about, BY VALUE — a mint answering for foreign Ys
// or padding a short reply with a duplicate must be rejected, not counted
// (NUT-07: states MUST match the request; correlation makes a lying
// mint's job harder than counting did).

// newLyingCheckStateMint answers the requested Ys UNSPENT and then appends
// the injected extra state entries — the shapes a mint must not be able to
// sneak past: a foreign Y, or a duplicate of a requested one.
func newLyingCheckStateMint(t *testing.T, extraStates []map[string]string) *httptest.Server {
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
			first := true
			emit := func(y, state string) {
				if !first {
					fmt.Fprint(w, ",")
				}
				first = false
				fmt.Fprintf(w, `{"Y":%q,"state":%q}`, y, state)
			}
			for _, y := range req.Ys {
				emit(y, "UNSPENT")
			}
			for _, extra := range extraStates {
				emit(extra["Y"], extra["state"])
			}
			fmt.Fprint(w, `]}`)
		default:
			http.Error(w, "not found", 404)
		}
	}))
}

func checkStateToken(t *testing.T, mintURL, secret string) cashu.Token {
	t.Helper()
	proof := checkStateProof(t, secret)
	token, err := cashu.NewTokenV3(cashu.Proofs{proof}, mintURL, cashu.Sat, false)
	if err != nil {
		t.Fatal(err)
	}
	return &token
}

func TestCheckTokenSpent_ForeignYIsRejected(t *testing.T) {
	foreignY := func() string {
		Y, err := crypto.HashToCurve([]byte("a-y-we-never-asked-about"))
		if err != nil {
			t.Fatal(err)
		}
		return hex.EncodeToString(Y.SerializeCompressed())
	}()
	mint := newLyingCheckStateMint(t, []map[string]string{{"Y": foreignY, "state": "UNSPENT"}})
	defer mint.Close()

	tw, err := New(t.TempDir(), []string{mint.URL}, false)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer tw.Shutdown()

	_, err = tw.CheckTokenSpent(checkStateToken(t, mint.URL, "foreign-y-secret"))
	if err == nil {
		t.Fatal("an answer about a Y we did not ask must be an error, never a verdict")
	}
	if !strings.Contains(err.Error(), "not asked about") {
		t.Errorf("error should name the foreign-Y rejection, got: %v", err)
	}
}

func TestCheckTokenSpent_DuplicateYIsRejected(t *testing.T) {
	askedY := func() string {
		Y, err := crypto.HashToCurve([]byte("duplicate-y-secret"))
		if err != nil {
			t.Fatal(err)
		}
		return hex.EncodeToString(Y.SerializeCompressed())
	}()
	// Pad a one-proof request with a second UNSPENT answer for the SAME Y:
	// under the old counting logic this passed as a two-of-two answer.
	mint := newLyingCheckStateMint(t, []map[string]string{{"Y": askedY, "state": "UNSPENT"}})
	defer mint.Close()

	tw, err := New(t.TempDir(), []string{mint.URL}, false)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer tw.Shutdown()

	_, err = tw.CheckTokenSpent(checkStateToken(t, mint.URL, "duplicate-y-secret"))
	if err == nil {
		t.Fatal("a duplicate answer for one Y must be an error, never a verdict")
	}
	if !strings.Contains(err.Error(), "answered twice") {
		t.Errorf("error should name the duplicate-Y rejection, got: %v", err)
	}
}
