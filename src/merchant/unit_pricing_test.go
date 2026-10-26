package merchant

import (
	"testing"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/tollwallet"
)

// The CDK-mint issuance integration (draft: "operator issues megabytes/minutes
// from their router") requires the gateway to PRICE tokens by their unit, not
// by a raw number silently assumed to be satoshis.
//
// Today the Token interface (tollwallet/port.go) exposes Amount() with the
// doc "total token value in the token's unit (typically satoshis)" — and
// calculateAllotment (merchant.go) divides that number by PricePerStep in
// sats. A token denominated in a custom unit (an operator-run CDK mint
// issuing "mb" proofs via the pecan-style payment-processor pattern) would be
// silently mispriced: 64 "mb" proofs would price as 64 sats.
//
// This test pins the contract the integration needs: the wallet surface must
// expose the token's unit, so the merchant can (a) refuse mismatched units
// it cannot price, and (b) price unit-matched tokens correctly.

// unitStubToken carries an explicit unit — the shape the Token interface
// must grow (Unit() string) for any unit-aware pricing to be possible.
type unitStubToken struct {
	mint   string
	unit   string
	amount uint64
}

func (t *unitStubToken) Mint() string      { return t.mint }
func (t *unitStubToken) Amount() uint64   { return t.amount }
func (t *unitStubToken) Unit() string     { return t.unit }
func (t *unitStubToken) Serialize() (string, error) {
	return "", nil
}
func (t *unitStubToken) Secrets() []string { return nil }
func (t *unitStubToken) Close()             {}

func TestTokenInterfaceExposesUnit(t *testing.T) {
	// The compile-time contract: a Token must be able to report its unit.
	// RED until tollwallet.Token grows Unit() string.
	var tok tollwallet.Token = &unitStubToken{mint: "http://127.0.0.1:3338", unit: "mb", amount: 64}
	if tok.Unit() != "mb" {
		t.Fatalf("Token.Unit() = %q, want %q", tok.Unit(), "mb")
	}
}

func TestCalculateAllotment_RefusesUnknownUnit(t *testing.T) {
	// A mint configured WITHOUT an explicit unit says its tokens are priced
	// in sats (today's only supported unit). If a token arrives in a custom
	// unit, the price table has no row for it: refusing is the only safe
	// answer — silently treating 64 mb as 64 sats is the bug this pins.
	m := &Merchant{
		config: &config_manager.Config{
			Metric:   "bytes",
			StepSize: 10485760,
			AcceptedMints: []config_manager.MintConfig{
				{URL: "http://127.0.0.1:3338", PricePerStep: 1},
			},
		},
	}
	_, err := m.calculateAllotmentForUnit(64, "mb", "http://127.0.0.1:3338")
	if err == nil {
		t.Fatal("expected error pricing an mb-denominated token against a sat-priced mint config, got nil — the value would be silently mispriced as sats")
	}
}

func TestCalculateAllotment_UnitsPricedWithExplicitUnit(t *testing.T) {
	// A mint configured with unit "mb" and price_per_step 1 prices 64 mb
	// tokens as 64 steps of StepSize — the operator-issued-megabytes flow.
	m := &Merchant{
		config: &config_manager.Config{
			Metric:   "bytes",
			StepSize: 10485760, // 10 MiB
			AcceptedMints: []config_manager.MintConfig{
				{URL: "http://127.0.0.1:3338", PricePerStep: 1, Unit: "mb"},
			},
		},
	}
	allotment, err := m.calculateAllotmentForUnit(64, "mb", "http://127.0.0.1:3338")
	if err != nil {
		t.Fatalf("unit-matched pricing failed: %v", err)
	}
	if want := uint64(64 * 10485760); allotment != want {
		t.Fatalf("allotment = %d, want %d (64 steps x 10MiB)", allotment, want)
	}
}