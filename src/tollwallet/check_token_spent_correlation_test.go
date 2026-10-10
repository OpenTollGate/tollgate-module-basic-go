package tollwallet

// #834: the NUT-07 answer is mint-controlled input, so every returned
// state must answer a Y the wallet actually asked about. A foreign,
// duplicated or missing Y is a tampered or buggy answer, and the
// pending-intent decisions downstream (abandoned vs owed) must never be
// made on it.

import (
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/OpenTollGate/gonuts-tollgate/cashu"
	"github.com/OpenTollGate/gonuts-tollgate/crypto"
)

// newRawCheckStateMint serves one fixed /v1/checkstate body regardless of
// what was asked — the shape of a tampered or buggy mint.
func newRawCheckStateMint(t *testing.T, body string) *httptest.Server {
	t.Helper()
	keysetsJSON := `{"keysets":[{"id":"009a1f293253e41e","unit":"sat","active":true,"keys":{"1":"0279be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798"}}]}`
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/keys", "/v1/keysets":
			fmt.Fprint(w, keysetsJSON)
		case "/v1/checkstate":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, body)
		default:
			http.Error(w, "not found", 404)
		}
	}))
}

func correlationToken(t *testing.T, mintURL string, secrets ...string) cashu.Token {
	t.Helper()
	proofs := make(cashu.Proofs, len(secrets))
	for i, s := range secrets {
		proofs[i] = checkStateProof(t, s)
	}
	token, err := cashu.NewTokenV3(proofs, mintURL, cashu.Sat, false)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func yOf(t *testing.T, secret string) string {
	t.Helper()
	Y, err := crypto.HashToCurve([]byte(secret))
	if err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(Y.SerializeCompressed())
}

func TestCheckTokenSpent_ForeignYIsRefusedNeverAVerdict(t *testing.T) {
	mint := newRawCheckStateMint(t, `{"states":[{"Y":"02deadbeef","state":"UNSPENT"}]}`)
	defer mint.Close()
	tw, err := New(t.TempDir(), []string{mint.URL}, false)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer tw.Shutdown()

	spent, err := tw.CheckTokenSpent(correlationToken(t, mint.URL, "correlation-foreign-secret"))
	if err == nil || spent {
		t.Fatal("a foreign-Y answer must be an error, never a verdict")
	}
	if !strings.Contains(err.Error(), "never asked about") {
		t.Fatalf("the refusal must say the Y was never asked about, got: %v", err)
	}
}

func TestCheckTokenSpent_DuplicateYIsRefused(t *testing.T) {
	y1 := yOf(t, "correlation-dup-secret-1")
	mint := newRawCheckStateMint(t,
		`{"states":[{"Y":"`+y1+`","state":"UNSPENT"},{"Y":"`+y1+`","state":"UNSPENT"}]}`)
	defer mint.Close()
	tw, err := New(t.TempDir(), []string{mint.URL}, false)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer tw.Shutdown()

	_, err = tw.CheckTokenSpent(correlationToken(t, mint.URL, "correlation-dup-secret-1", "correlation-dup-secret-2"))
	if err == nil {
		t.Fatal("answering the same Y twice must be refused")
	}
	if !strings.Contains(err.Error(), "2 times") {
		t.Fatalf("the refusal must name the duplicate, got: %v", err)
	}
}

func TestCheckTokenSpent_MissingYIsRefused(t *testing.T) {
	y1 := yOf(t, "correlation-missing-secret-1")
	mint := newRawCheckStateMint(t, `{"states":[{"Y":"`+y1+`","state":"UNSPENT"}]}`)
	defer mint.Close()
	tw, err := New(t.TempDir(), []string{mint.URL}, false)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer tw.Shutdown()

	_, err = tw.CheckTokenSpent(correlationToken(t, mint.URL, "correlation-missing-secret-1", "correlation-missing-secret-2"))
	if err == nil {
		t.Fatal("answering fewer Ys than asked must be refused")
	}
	if !strings.Contains(err.Error(), "1 of 2") {
		t.Fatalf("the refusal must state the shortfall, got: %v", err)
	}
}
