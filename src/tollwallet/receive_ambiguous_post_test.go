package tollwallet

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/OpenTollGate/gonuts-tollgate/cashu"
)

// The repo-side pin for tollgate #640: after an ambiguous swap outcome (the
// mint processed the request and the response was dropped), the deterministic
// derivation outputs that may already have reached the mint must never be
// re-exposed. The fix lives in gonuts-tollgate's mint client (no same-body
// POST retry on network errors, v0.12.2); this test fails the release gate if
// a future repin reintroduces the re-exposure — the #535 conformance lane
// measured it as the same blinded-message digests sighted twice on swap
// routes, the #257/#266/#480 brick class.

type swapSightingLog struct {
	mu       sync.Mutex
	requests int
	outputs  map[string]int
}

func (l *swapSightingLog) record(body []byte) {
	var payload struct {
		Outputs []struct {
			B_ string `json:"B_"`
		} `json:"outputs"`
	}
	_ = json.Unmarshal(body, &payload)
	l.mu.Lock()
	defer l.mu.Unlock()
	l.requests++
	if l.outputs == nil {
		l.outputs = make(map[string]int)
	}
	for _, out := range payload.Outputs {
		l.outputs[out.B_]++
	}
}

func (l *swapSightingLog) snapshot() (requests int, outputs map[string]int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.requests, l.outputs
}

// TestReceive_DroppedSwapResponseNeverReExposesOutputs drives the full
// in-process wallet against a mint that accepts the swap, records every
// blinded output it sees, and then drops the connection without answering.
// Whatever error comes back, the mint must have seen each output exactly
// once: a duplicate-tolerant mint hides the violation from the payment flow,
// which is exactly how the conformance lane caught it and a unit payment test
// could not.
func TestReceive_DroppedSwapResponseNeverReExposesOutputs(t *testing.T) {
	const keysetID = "009a1f293253e41e"
	const amount1Key = "0279be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798"

	keysetsJSON := `{"keysets":[{"id":"` + keysetID + `","unit":"sat","active":true,` +
		`"keys":{"1":"` + amount1Key + `"}}]}`

	log := &swapSightingLog{}
	mint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/keys", "/v1/keysets":
			fmt.Fprint(w, keysetsJSON)
		case "/v1/swap":
			buf := make([]byte, r.ContentLength)
			for total := 0; total < len(buf); {
				n, err := r.Body.Read(buf[total:])
				total += n
				if err != nil {
					break
				}
			}
			log.record(buf)
			hj, ok := w.(http.Hijacker)
			if !ok {
				http.Error(w, "no hijack", http.StatusInternalServerError)
				return
			}
			conn, _, err := hj.Hijack()
			if err == nil {
				_ = conn.Close()
			}
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

	proof := cashu.Proof{Amount: 1, Id: keysetID, Secret: "ambiguous-outcome-secret", C: amount1Key}
	token, err := cashu.NewTokenV3(cashu.Proofs{proof}, mint.URL, cashu.Sat, false)
	if err != nil {
		t.Fatal(err)
	}

	_, err = tw.Receive(token)
	if err == nil {
		t.Fatal("a dropped swap response must surface as an error, not a success")
	}

	requests, outputs := log.snapshot()
	if requests != 1 {
		t.Fatalf("the swap request must reach the mint exactly once; got %d", requests)
	}
	for digest, sightings := range outputs {
		if sightings != 1 {
			t.Fatalf("blinded output %s was exposed %d times — the derivation range is compromised (mint error 10002 class)", digest, sightings)
		}
	}
}

// TestReceive_WalletUsableAfterDroppedSwapResponse pins the recovery half of
// the #640 invariant: after an ambiguous outcome the wallet carries no
// poisoned state — a later Receive of a different note against the same mint,
// once it answers again, completes normally.
func TestReceive_WalletUsableAfterDroppedSwapResponse(t *testing.T) {
	const keysetID = "009a1f293253e41e"
	const amount1Key = "0279be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798"

	keysetsJSON := `{"keysets":[{"id":"` + keysetID + `","unit":"sat","active":true,` +
		`"keys":{"1":"` + amount1Key + `"}}]}`

	var answerMu sync.Mutex
	answers := false
	dropped := 0
	mint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/keys", "/v1/keysets":
			fmt.Fprint(w, keysetsJSON)
		case "/v1/swap":
			answerMu.Lock()
			answering := answers
			answerMu.Unlock()
			if !answering {
				dropped++
				hj, ok := w.(http.Hijacker)
				if !ok {
					http.Error(w, "no hijack", http.StatusInternalServerError)
					return
				}
				conn, _, err := hj.Hijack()
				if err == nil {
					_ = conn.Close()
				}
				return
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"signatures":[{"amount":1,"C_":"`+amount1Key+`","id":"`+keysetID+`"}]}`)
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

	firstProof := cashu.Proof{Amount: 1, Id: keysetID, Secret: "ambiguous-outcome-secret-first", C: amount1Key}
	firstToken, err := cashu.NewTokenV3(cashu.Proofs{firstProof}, mint.URL, cashu.Sat, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Receive(firstToken); err == nil {
		t.Fatal("the dropped swap must surface as an error")
	}

	answerMu.Lock()
	answers = true
	answerMu.Unlock()

	secondProof := cashu.Proof{Amount: 1, Id: keysetID, Secret: "post-recovery-secret-second", C: amount1Key}
	secondToken, err := cashu.NewTokenV3(cashu.Proofs{secondProof}, mint.URL, cashu.Sat, false)
	if err != nil {
		t.Fatal(err)
	}
	amount, err := tw.Receive(secondToken)
	if err != nil {
		t.Fatalf("the wallet must remain usable after an ambiguous outcome: %v", err)
	}
	if amount != 1 {
		t.Fatalf("unexpected received amount: %d", amount)
	}
}
