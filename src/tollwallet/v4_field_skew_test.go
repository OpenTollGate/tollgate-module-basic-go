package tollwallet

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/fxamacker/cbor/v2"
)

// encodeV4 builds a V4 token payload with a chosen field name for the mint URL,
// so the test can produce both the shape cdk's decoder requires ("m") and the
// shape cdk-mintd 0.18.0 actually emits ("f").
func encodeV4(t *testing.T, mintField string) string {
	t.Helper()
	payload := map[string]any{
		mintField: "http://192.168.1.1:3398",
		"u":       "mb",
		"t": []any{
			map[string]any{"i": []byte{1, 2, 3, 4}, "p": []any{}},
		},
	}
	raw, err := cbor.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return "cashuB" + base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString(raw)
}

// TestNormalizeV4MintFieldName covers the cdk version skew measured on the A04E
// bench: cdk-mintd 0.18.0 serializes TokenV4 with the mint URL under "f", but
// the cdk-go FFI decoder requires "m" (cdk's own TokenV4 declares
// #[serde(rename = "m")]). A token the router issues was therefore rejected by
// the same router with "Invalid token: Semantic(None, missing field `m`)".
func TestNormalizeV4MintFieldName(t *testing.T) {
	tests := []struct {
		name       string
		in         string
		wantChange bool
	}{
		{name: "the mint's 'f' spelling is rewritten to 'm'", in: encodeV4(t, "f"), wantChange: true},
		{name: "an already-correct 'm' token is left byte-identical", in: encodeV4(t, "m"), wantChange: false},
		{name: "a V3 token is not touched", in: "cashuAeyJ0b2tlbiI6W119", wantChange: false},
		{name: "an empty string is not touched", in: "", wantChange: false},
		{name: "arbitrary junk is returned unchanged rather than erroring", in: "cashuBnot-base64-at-all!!!", wantChange: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizeV4MintFieldName(tc.in)
			if tc.wantChange {
				if got == tc.in {
					t.Fatal("expected the payload to be rewritten")
				}
				// The rewritten token must now decode with "m" present.
				raw := strings.TrimPrefix(got, "cashuB")
				b, err := base64.URLEncoding.WithPadding(base64.NoPadding).DecodeString(raw)
				if err != nil {
					t.Fatalf("rewritten token is not valid base64url: %v", err)
				}
				var m map[string]any
				if err := cbor.Unmarshal(b, &m); err != nil {
					t.Fatalf("rewritten token is not valid CBOR: %v", err)
				}
				if _, ok := m["m"]; !ok {
					t.Error("rewritten payload has no 'm' field")
				}
				if _, ok := m["f"]; ok {
					t.Error("rewritten payload still carries 'f'")
				}
				if m["u"] != "mb" {
					t.Errorf("unit must survive the rewrite; got %v", m["u"])
				}
			} else if got != tc.in {
				t.Errorf("input must pass through unchanged:\n got %q\nwant %q", got, tc.in)
			}
		})
	}
}
