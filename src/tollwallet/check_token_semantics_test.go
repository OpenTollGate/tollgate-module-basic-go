package tollwallet

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// Verdict-semantics tests for CheckTokenSpendable — the NUT-07 primitive
// wallet recovery is built on, whose contract (see
// WalletPort.CheckTokenSpendable) splits recovery into two types the mint
// cannot distinguish: recovering owned tokens stranded mid-operation, and
// clawing back handed-out tokens the recipient never redeemed.

// lifecycleMint is a NUT-07 checkstate endpoint whose per-Y verdicts can
// change over the life of the test, modelling a real mint as a stranded
// token moves through the redemption lifecycle: UNSPENT -> PENDING ->
// SPENT. Ys without an explicit entry answer UNSPENT.
type lifecycleMint struct {
	mu     sync.Mutex
	states map[string]string
	server *httptest.Server
}

func newLifecycleMint(t *testing.T) *lifecycleMint {
	t.Helper()
	lm := &lifecycleMint{states: map[string]string{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/checkstate", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Ys []string `json:"Ys"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		lm.mu.Lock()
		defer lm.mu.Unlock()
		type proofStateJSON struct {
			Y     string `json:"Y"`
			State string `json:"state"`
		}
		resp := struct {
			States []proofStateJSON `json:"states"`
		}{}
		for _, y := range req.Ys {
			state, ok := lm.states[y]
			if !ok {
				state = "UNSPENT"
			}
			resp.States = append(resp.States, proofStateJSON{Y: y, State: state})
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})
	lm.server = httptest.NewServer(mux)
	t.Cleanup(lm.server.Close)
	return lm
}

func (lm *lifecycleMint) setState(yHex, state string) {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	lm.states[yHex] = state
}

// TestRecoverScenario_IntermediaryState_Lifecycle is the PR's headline
// situation: a token stranded mid-flight by a failure (drain response
// lost to a crash, SSH drop, or partial drain). Local state cannot say
// whether the token is still value — only the mint's spent-secret list
// can. The same journal entry must therefore yield three different
// verdicts as the redemption lifecycle progresses, and only the third is
// final:
//
//	UNSPENT -> live        (value recoverable: print the token)
//	PENDING -> unknown     (a redemption is in flight; outcome undecided —
//	                        MUST surface as an error, never as "spent", or
//	                        recoverable funds get misreported as gone)
//	SPENT   -> spent       (redeemed elsewhere; nothing to do)
func TestRecoverScenario_IntermediaryState_Lifecycle(t *testing.T) {
	const secret = "crash-window-secret-50sats"
	mint := newLifecycleMint(t)
	token := buildTestToken(t, mint.server.URL, []string{secret})
	y := proofYHex(t, secret)

	// Phase 1: the mint never saw the proofs — the token is stranded but
	// unspent, i.e. exactly the recoverable case.
	spendable, err := CheckTokenSpendable(token)
	if err != nil || !spendable {
		t.Fatalf("UNSPENT phase: want (live, nil), got (%v, %v)", spendable, err)
	}

	// Phase 2: someone starts redeeming the token found in, say, an old
	// terminal scrollback — proofs go PENDING. The verdict must be an
	// error (unknown), not a definitive not-spendable.
	mint.setState(y, "PENDING")
	spendable, err = CheckTokenSpendable(token)
	if err == nil {
		t.Fatalf("PENDING phase: want error (unknown), got spendable=%v", spendable)
	}
	if !strings.Contains(err.Error(), "PENDING") {
		t.Fatalf("PENDING phase: error should name the state, got: %v", err)
	}

	// Phase 3: the redemption completes — now and only now is the
	// not-spendable verdict definitive.
	mint.setState(y, "SPENT")
	spendable, err = CheckTokenSpendable(token)
	if err != nil || spendable {
		t.Fatalf("SPENT phase: want (not spendable, nil), got (%v, %v)", spendable, err)
	}
}

// TestRecoverScenario_PartiallyRedeemedTokenIsNotLive pins the bearer-
// instrument rule for multi-proof tokens: a token is only "live" if EVERY
// proof is unspent. A recipient who redeemed any proof has taken value;
// re-importing the token string must not be presented as recovery.
func TestRecoverScenario_PartiallyRedeemedTokenIsNotLive(t *testing.T) {
	const (
		liveSecret  = "scenario-multi-live"
		spentSecret = "scenario-multi-spent"
	)
	server := newCheckStateMint(t, map[string]string{
		proofYHex(t, spentSecret): "SPENT",
	})
	token := buildTestToken(t, server.URL, []string{liveSecret, spentSecret})

	spendable, err := CheckTokenSpendable(token)
	if err != nil {
		t.Fatalf("partially spent token: %v", err)
	}
	if spendable {
		t.Fatal("token with any SPENT proof must not be reported spendable")
	}
}

// TestRecoverClawbackSemantics_UnspentHandedOutTokenIsStillSpendableByHolder
// documents the SECOND recovery type the NUT-07 primitive serves, and the
// reason its verdicts must not be conflated with drain recovery:
//
//   - OWNED tokens (PR #549, the drain journal): "live" means our stranded
//     value is recoverable — re-importing is uncontroversial, nobody else
//     holds the secrets.
//   - HANDED-OUT tokens (scripts/token-recovery, tokens-to-recover.txt,
//     issue #423, the reseller interrupted hand-off): we paid someone with
//     a token, the payment failed after the hand-off, and we still hold
//     the proof secrets. "live" means the recipient has NOT redeemed —
//     the clawback window is open, and re-spending (Receive back) is a
//     double-spend that races the recipient's redemption. "spent" no
//     longer means "secured", it means "the counterparty took the value"
//     — a payment dispute, not a closed case.
//
// Same checkstate call, opposite meaning per verdict, different action
// and risk profile. This test pins the primitive's behavior for the
// handed-out case: a token the recipient never redeemed still checks as
// spendable by whoever holds the secrets.
func TestRecoverClawbackSemantics_UnspentHandedOutTokenIsStillSpendableByHolder(t *testing.T) {
	const paidSecret = "handed-to-upstream-then-errored"
	// The upstream TollGate errored after we handed it this token; it never
	// redeemed the proofs.
	server := newCheckStateMint(t, nil)
	token := buildTestToken(t, server.URL, []string{paidSecret})

	spendable, err := CheckTokenSpendable(token)
	if err != nil {
		t.Fatalf("handed-out token check: %v", err)
	}
	if !spendable {
		t.Fatal("unredeemed handed-out token must check spendable — the clawback window is open")
	}
}
