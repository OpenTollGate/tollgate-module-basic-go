package config_manager

import (
	"strings"
	"testing"
)

// TestValidateUnitPricing covers the misconfiguration that costs real money:
// a custom-unit mint priced with a step_size from a different unit.
//
// The case that prompted this: unit="mb" with step_size=22020096 (a *bytes*
// default, 21 MiB) means one "megabyte" of ecash buys 21 MiB of access — the
// operator gives away 21x on every sale, silently, and every single-payment
// test still passes because the arithmetic is self-consistent. It was measured
// on hardware in the 2026-10 CDK integration before anyone noticed.
func TestValidateUnitPricing(t *testing.T) {
	tests := []struct {
		name     string
		unit     string
		metric   string
		stepSize uint64
		wantErr  bool
		wantIn   string // substring that must appear in the error, for operator actionability
	}{
		{
			name:     "bytes unit with bytes step size is consistent",
			unit:     "mb",
			metric:   "bytes",
			stepSize: 1048576,
			wantErr:  false,
		},
		{
			name:     "decimal megabyte step is also consistent",
			unit:     "mb",
			metric:   "bytes",
			stepSize: 1000000,
			wantErr:  false,
		},
		{
			name:     "THE INCIDENT: mb priced with the 21 MiB bytes default",
			unit:     "mb",
			metric:   "bytes",
			stepSize: 22020096,
			wantErr:  true,
			wantIn:   "22020096",
		},
		{
			name:     "seconds unit with the bytes default",
			unit:     "min",
			metric:   "milliseconds",
			stepSize: 60000,
			wantErr:  false,
		},
		{
			name:     "THE OTHER INCIDENT: minutes priced with a bytes step size",
			unit:     "min",
			metric:   "milliseconds",
			stepSize: 22020096,
			wantErr:  true,
			wantIn:   "22020096",
		},
		{
			name:     "megabyte unit with a minutes step size",
			unit:     "mb",
			metric:   "bytes",
			stepSize: 60000,
			wantErr:  true,
			wantIn:   "60000",
		},
		{
			name:     "sat mint keeps any step size (legacy configs)",
			unit:     "",
			metric:   "bytes",
			stepSize: 22020096,
			wantErr:  false,
		},
		{
			name:     "explicit sat unit likewise",
			unit:     "sat",
			metric:   "bytes",
			stepSize: 22020096,
			wantErr:  false,
		},
		{
			name:     "unknown custom unit cannot be judged, and is left alone",
			unit:     "parks",
			metric:   "bytes",
			stepSize: 22020096,
			wantErr:  false,
		},
		{
			name:     "zero step size is not this check's business",
			unit:     "mb",
			metric:   "bytes",
			stepSize: 0,
			wantErr:  false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateUnitPricing(tc.unit, tc.metric, tc.stepSize)
			if tc.wantErr && err == nil {
				t.Fatalf("want error for unit=%q metric=%q step_size=%d, got nil", tc.unit, tc.metric, tc.stepSize)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("want no error for unit=%q metric=%q step_size=%d, got %v", tc.unit, tc.metric, tc.stepSize, err)
			}
			if tc.wantErr && tc.wantIn != "" && !strings.Contains(err.Error(), tc.wantIn) {
				t.Fatalf("error must name the offending number %q so an operator can act on it; got %q", tc.wantIn, err.Error())
			}
		})
	}
}

// TestValidateUnitPricingNamesTheMultiplier pins the operator-facing content:
// the error has to state how much access the misconfiguration actually grants,
// because "invalid step_size" gives an operator nothing to fix it with.
func TestValidateUnitPricingNamesTheMultiplier(t *testing.T) {
	err := ValidateUnitPricing("mb", "bytes", 22020096)
	if err == nil {
		t.Fatal("expected an error")
	}
	msg := err.Error()
	for _, want := range []string{"22020096", "1 MiB", "mb"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error should mention %q; got %q", want, msg)
		}
	}
}
