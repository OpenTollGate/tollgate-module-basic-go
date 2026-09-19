package tollwallet

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/OpenTollGate/gonuts-tollgate/cashu"
)

// TestReceive_SpentTokenMapsToSentinel is the two-layer pin for the gonuts
// bump: a mock mint rejects the swap with a CDK-style "inputs have already
// been spent"; gonuts (>= the bumped version) surfaces that rejection —
// older versions swallowed it into an empty error, which made the
// ErrTokenAlreadySpent mapping below unreachable. The test fails on either
// regression: swallowing at the gonuts layer, or match drift at this layer.
func TestReceive_SpentTokenMapsToSentinel(t *testing.T) {
	const keysetID = "009a1f293253e41e"
	const amount1Key = "0279be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798"

	keysetsJSON := `{"keysets":[{"id":"` + keysetID + `","unit":"sat","active":true,` +
		`"keys":{"1":"` + amount1Key + `"}}]}`
	mint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/keys", "/v1/keysets":
			fmt.Fprint(w, keysetsJSON)
		case "/v1/swap":
			http.Error(w, `{"code":3,"detail":"inputs have already been spent"}`, http.StatusBadRequest)
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	defer mint.Close()

	tw, err := New(t.TempDir(), []string{mint.URL}, false)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer tw.Shutdown()

	proof := cashu.Proof{Amount: 1, Id: keysetID, Secret: "sentinel-e2e-secret", C: amount1Key}
	token, err := cashu.NewTokenV3(cashu.Proofs{proof}, mint.URL, cashu.Sat, false)
	if err != nil {
		t.Fatal(err)
	}

	_, err = tw.Receive(token)
	if !errors.Is(err, ErrTokenAlreadySpent) {
		t.Fatalf("want ErrTokenAlreadySpent via errors.Is, got: %v", err)
	}
}
