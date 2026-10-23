package tollwallet

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/OpenTollGate/gonuts-tollgate/cashu"
)

// The #952 release-readiness question: can the wallet path consume tokens
// from V2-keyset mints in BOTH on-the-wire id spellings — the 16-hex short
// form cdk serializes natively (nut02 ShortKeysetId) and the full 66-hex
// canonical form wallet-side minters (Nutshell, cashu-ts) emit? The swap
// request the mint receives must carry the FULL canonical id either way
// (gonuts resolveShortKeysetIds, wallet.go:592). Everything runs against an
// in-process stub mint — no live-mint traffic.

const v2KeysetFull = "01e73e75a0809604372487732ee917e8f881cc4c882ccd9fbac094fbfd3e3a42e6"

const stubKeysJSON = `{"keysets":[{"id":"` + v2KeysetFull + `","unit":"sat","active":true,"input_fee_ppk":0,"keys":{"1":"0279be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798","2":"02c6047f9441ed7d6d3045406e95c07cd85c778e4b8cef3ca7abac09b95c709ee5","4":"02e493dbf1c10d80f3581e4904930b1404cc6c13900ee0758474fa94abe8c4cd13"}}]}`

type swapRecorder struct {
	mu       sync.Mutex
	requests []swapRequestBody
}

type swapRequestBody struct {
	Inputs []struct {
		Id string `json:"id"`
	} `json:"inputs"`
}

func (r *swapRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.requests)
}

func (r *swapRecorder) lastIds() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.requests) == 0 {
		return nil
	}
	var ids []string
	for _, in := range r.requests[len(r.requests)-1].Inputs {
		ids = append(ids, in.Id)
	}
	return ids
}

// newV2StubMint serves the NUT-01/02 surface of a V2-keyset mint and records
// swap requests instead of executing them (a stub mint cannot sign blinded
// outputs, so Receive errors after the swap POST — the ids on the wire are
// the assertion target, not the swap's success).
func newV2StubMint(t *testing.T) (*httptest.Server, *swapRecorder) {
	t.Helper()
	rec := &swapRecorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/keysets" || r.URL.Path == "/v1/keys" || strings.HasPrefix(r.URL.Path, "/v1/keys/"):
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, stubKeysJSON)
		case r.URL.Path == "/v1/swap":
			var body swapRequestBody
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, "bad json", 400)
				return
			}
			rec.mu.Lock()
			rec.requests = append(rec.requests, body)
			rec.mu.Unlock()
			http.Error(w, `{"code":9999,"error":"stub mint cannot swap"}`, 500)
		default:
			http.Error(w, "not found", 404)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, rec
}

func v3TokenWithIdSpelling(t *testing.T, mintURL, id string) string {
	t.Helper()
	token := map[string]interface{}{
		"token": []map[string]interface{}{{
			"mint": mintURL,
			"proofs": []map[string]interface{}{{
				"amount": 4,
				"id":     id,
				"secret": "6162636465666768696a6b6c6d6e6f707172737475767778797a30313233343536",
				"C":      "0279be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798",
			}},
		}},
		"unit": "sat",
	}
	b, err := json.Marshal(token)
	if err != nil {
		t.Fatalf("marshal v3 token: %v", err)
	}
	return "cashuA" + base64.RawURLEncoding.EncodeToString(b)
}

func receiveExpectingSwapRejection(t *testing.T, mintURL, tokenStr string) {
	t.Helper()
	walletDir := t.TempDir()
	tw, err := New(walletDir, []string{mintURL}, false)
	if err != nil {
		t.Fatalf("tollwallet.New: %v", err)
	}
	token, err := cashu.DecodeToken(tokenStr)
	if err != nil {
		t.Fatalf("DecodeToken: %v", err)
	}
	if _, err := tw.Receive(token); err == nil {
		t.Fatalf("stub mint cannot sign; Receive was expected to fail, got success")
	}
}

func TestV2Keyset_BothIdSpellingsReachSwapWithFullId(t *testing.T) {
	srv, rec := newV2StubMint(t)

	t.Run("cdk native short id resolves to full", func(t *testing.T) {
		tokenStr := v3TokenWithIdSpelling(t, srv.URL, v2KeysetFull[:16])
		receiveExpectingSwapRejection(t, srv.URL, tokenStr)
		if rec.count() == 0 {
			t.Fatal("no swap request reached the mint")
		}
		for _, id := range rec.lastIds() {
			if id != v2KeysetFull {
				t.Fatalf("swap request carried id %q (len %d) — the 16-hex short spelling must be resolved to the full canonical id before the mint sees it (#952)", id, len(id))
			}
		}
	})

	rec.requests = nil

	t.Run("wallet-side full id passes through unchanged", func(t *testing.T) {
		tokenStr := v3TokenWithIdSpelling(t, srv.URL, v2KeysetFull)
		receiveExpectingSwapRejection(t, srv.URL, tokenStr)
		if rec.count() == 0 {
			t.Fatal("no swap request reached the mint")
		}
		for _, id := range rec.lastIds() {
			if id != v2KeysetFull {
				t.Fatalf("swap request carried id %q — the full spelling must pass through unchanged (#952)", id)
			}
		}
	})
}

func TestV2Keyset_V4TokenShortIdResolves(t *testing.T) {
	srv, rec := newV2StubMint(t)

	proofs := cashu.Proofs{{
		Amount: 4,
		Id:     v2KeysetFull[:16],
		Secret: "6162636465666768696a6b6c6d6e6f707172737475767778797a30313233343536",
		C:      "0279be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798",
	}}
	v4, err := cashu.NewTokenV4(proofs, srv.URL, cashu.Sat, false)
	if err != nil {
		t.Fatalf("NewTokenV4: %v", err)
	}
	tokenStr, err := v4.Serialize()
	if err != nil {
		t.Fatalf("Serialize V4: %v", err)
	}

	receiveExpectingSwapRejection(t, srv.URL, tokenStr)
	if rec.count() == 0 {
		t.Fatal("no swap request reached the mint")
	}
	for _, id := range rec.lastIds() {
		if id != v2KeysetFull {
			t.Fatalf("swap request carried id %q — the V4 8-byte keyset id must be resolved to the full canonical id (#952)", id)
		}
	}
}
