package tollwallet

import (
	"encoding/base64"
	"strings"

	"github.com/fxamacker/cbor/v2"
)

// normalizeV4MintFieldName rewrites a V4 Cashu token whose mint URL is carried
// under "f" so that it uses "m", which is the field name the cdk-go FFI decoder
// requires.
//
// Why this exists (measured on the A04E bench, 2026-10-22): the router's
// cdk-mintd 0.18.0 serializes TokenV4 with the mint URL under "f", while the
// cdk-go bindings used here decode with cdk's own definition, which declares
//
//	#[serde(rename = "m")]
//	pub mint_url: MintUrl,      // crates/cashu/src/nuts/nut00/token.rs
//
// The result is that a token the router ITSELF issued is rejected by that same
// router:
//
//	Invalid cashu token: cdk-go token decode failed: FfiError: Internal:
//	ErrorMessage=Invalid token: Semantic(None, "missing field `m`")
//
// This is a cdk version skew (mint serializer vs wallet decoder), not a
// TollGate defect. The shim is deliberately narrow: it rewrites exactly one
// field name in one token format, and leaves every other token byte-identical.
// It is not a general CBOR rewriter, and it does not attempt to repair
// anything else about the token.
//
// Every non-matching case returns the input unchanged. A token that cannot be
// parsed here is handed to the decoder untouched, so the FFI still produces the
// authoritative error rather than this function inventing one.
func normalizeV4MintFieldName(tokenStr string) string {
	const prefix = "cashuB"
	if !strings.HasPrefix(tokenStr, prefix) {
		return tokenStr // V3 ("cashuA..."), empty, or not a token at all
	}
	raw := tokenStr[len(prefix):]
	// Cashu V4 uses base64url without padding.
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		// Some encoders pad; try the padded spelling before giving up.
		if decoded, err = base64.URLEncoding.DecodeString(raw); err != nil {
			return tokenStr
		}
	}

	var payload map[string]any
	if err := cbor.Unmarshal(decoded, &payload); err != nil {
		return tokenStr
	}
	mintURL, hasF := payload["f"]
	if !hasF {
		return tokenStr // already "m", or some other shape entirely
	}
	if _, hasM := payload["m"]; hasM {
		return tokenStr // ambiguous: do not guess which one is right
	}

	payload["m"] = mintURL
	delete(payload, "f")

	rewritten, err := cbor.Marshal(payload)
	if err != nil {
		return tokenStr
	}
	return prefix + base64.RawURLEncoding.EncodeToString(rewritten)
}
