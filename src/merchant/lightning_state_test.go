//go:build testenv

package merchant

import (
	"testing"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/tollwallet"
	"github.com/Origami74/gonuts-tollgate/cashu/nuts/nut04"
)

// TestLightningStateMachine pins the observable behavior of the Lightning quote
// state machine that today delegates to gonuts' nut04.State. These characterization
// tests document what the code DOES at the time of writing (not what it should
// ideally do) so that a refactor swapping nut04.State for a local MintQuoteState
// enum can prove it preserves every observable behavior: wire-format strings,
// switch-case recognition, zero-value semantics, and equality comparisons.
func TestLightningStateMachine(t *testing.T) {
	t.Run("state_string_outputs", func(t *testing.T) {
		// Pin the exact String() output for every defined nut04.State constant
		// plus the zero value and out-of-range values.
		// These strings flow to the wire via LightningInvoice.State
		// (lightning.go:100) and LightningQuoteStatus.State
		// (lightning.go:127,129), and to the in-memory CachedState field
		// (lightning.go:57, which is NOT persisted to disk per quote_store.go:13-14).
		cases := []struct {
			name  string
			state nut04.State
			want  string
		}{
			{"Unpaid", nut04.Unpaid, "UNPAID"},
			{"Paid", nut04.Paid, "PAID"},
			{"Issued", nut04.Issued, "ISSUED"},
			{"Pending", nut04.Pending, "PENDING"},
			{"Unknown", nut04.Unknown, "unknown"},
			{"zero value", nut04.State(0), "UNPAID"},
			{"negative", nut04.State(-1), "unknown"},
			{"above Unknown", nut04.State(5), "unknown"},
			{"large out of range", nut04.State(99), "unknown"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				got := tc.state.String()
				if got != tc.want {
					t.Fatalf("nut04.State(%d).String() = %q, want %q", tc.state, got, tc.want)
				}
			})
		}
	})

	t.Run("state_equality", func(t *testing.T) {
		// Pin the equality semantics used at lightning.go:397
		// (if state == nut04.Paid) and the switch cases at lightning.go:116,227.
		if nut04.Paid != nut04.Paid {
			t.Fatal("nut04.Paid must equal itself")
		}
		if nut04.Paid == nut04.Issued {
			t.Fatal("nut04.Paid must not equal nut04.Issued")
		}
		// Pin the specific comparison that drives minting at lightning.go:397.
		state := nut04.Paid
		if state != nut04.Paid {
			t.Fatal("state == nut04.Paid check at lightning.go:397 must be true for Paid")
		}
		state = nut04.Issued
		if state == nut04.Paid {
			t.Fatal("state == nut04.Paid check at lightning.go:397 must be false for Issued")
		}
	})

	t.Run("paid_and_issued_recognized_in_switch", func(t *testing.T) {
		// Pin the switch-case recognition at lightning.go:116 and lightning.go:227.
		// Both use: switch state { case nut04.Paid, nut04.Issued: ... }
		recognized := func(s nut04.State) bool {
			switch s {
			case nut04.Paid, nut04.Issued:
				return true
			}
			return false
		}
		cases := []struct {
			name  string
			state nut04.State
			want  bool
		}{
			{"Unpaid", nut04.Unpaid, false},
			{"Paid", nut04.Paid, true},
			{"Issued", nut04.Issued, true},
			{"Pending", nut04.Pending, false},
			{"Unknown", nut04.Unknown, false},
			{"zero value", nut04.State(0), false},
			{"out of range", nut04.State(99), false},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				if got := recognized(tc.state); got != tc.want {
					t.Fatalf("recognized(nut04.State(%d)) = %v, want %v", tc.state, got, tc.want)
				}
			})
		}
	})

	t.Run("cached_state_default_zero_value", func(t *testing.T) {
		// Pin the zero value of nut04.State and the lightningQuoteRecord.CachedState field.
		var zeroState nut04.State
		if zeroState != nut04.Unpaid {
			t.Fatalf("zero value of nut04.State = %d, want Unpaid (0)", zeroState)
		}
		if zeroState.String() != "UNPAID" {
			t.Fatalf("zero value String() = %q, want %q", zeroState.String(), "UNPAID")
		}
		var record lightningQuoteRecord
		if record.CachedState != tollwallet.StateUnpaid {
			t.Fatalf("lightningQuoteRecord zero-value CachedState = %d, want Unpaid (0)", record.CachedState)
		}
		if record.HasCachedState != false {
			t.Fatal("lightningQuoteRecord zero-value HasCachedState must be false")
		}
	})

	t.Run("getLightningQuoteState_return_type_smoke", func(t *testing.T) {
		// Smoke-test the return-type contract of getLightningQuoteState
		// (lightning.go:168) via the cache fast-path. With HasCachedState=true
		// and CachedStateAt within the cache TTL, the function returns
		// CachedState directly without contacting tollwallet.
		m := &Merchant{
			lightningQuotes: map[string]*lightningQuoteRecord{
				"cached-quote": {
					CachedState:    tollwallet.StatePaid,
					CachedStateAt:  time.Now(),
					HasCachedState: true,
				},
			},
		}
		state, err := m.getLightningQuoteState("cached-quote")
		if err != nil {
			t.Fatalf("getLightningQuoteState returned error: %v", err)
		}
		if state != tollwallet.StatePaid {
			t.Fatalf("getLightningQuoteState returned %d, want Paid (1)", state)
		}
		if got, want := state.String(), "PAID"; got != want {
			t.Fatalf("returned state String() = %q, want %q", got, want)
		}
	})
}
