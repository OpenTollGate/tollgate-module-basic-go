//go:build cgo && linux && (amd64 || arm64)

package main

import (
	"strings"
	"testing"

	cdk_ffi "github.com/cashubtc/cdk-go/bindings/cdkffi"
)

const knownGoodV3Token = "cashuAeyJ0b2tlbiI6IFt7Im1pbnQiOiAiaHR0cHM6Ly90ZXN0bnV0LmNhc2h1LnNwYWNlIiwgInByb29mcyI6IFt7ImlkIjogIjAwOWExZjI5MzI1M2U0MWUiLCAiYW1vdW50IjogMiwgInNlY3JldCI6ICI0MDc5MTViYzIxMmJlNjFhNzdlM2U2ZDJhZWI0YzcyNzk4MGJkYTUxY2QwNmE2YWZjMjllMjg2MTc2OGE3ODM3IiwgIkMiOiAiMDJiYzkwOTc5OTdkODFhZmIyY2M3MzQ2YjVlNDM0NWE5MzQ2YmQyYTUwNmViNzk1ODU5OGE3MmYwY2Y4NTE2M2VhIn1dfV0sICJ1bml0IjogInNhdCJ9"

func TestCdkGoTokenRoundtrip(t *testing.T) {
	t.Run("decode_succeeds", func(t *testing.T) {
		decoded, err := cdk_ffi.TokenDecode(knownGoodV3Token)
		if err != nil {
			t.Fatalf("TokenDecode failed: %v", err)
		}
		if decoded == nil {
			t.Fatal("TokenDecode returned nil Token")
		}
		defer decoded.Destroy()
	})

	t.Run("reencode_roundtrips", func(t *testing.T) {
		decoded, err := cdk_ffi.TokenDecode(knownGoodV3Token)
		if err != nil {
			t.Fatalf("TokenDecode failed: %v", err)
		}
		if decoded == nil {
			t.Fatal("TokenDecode returned nil Token")
		}
		defer decoded.Destroy()

		reencoded := decoded.Encode()
		if reencoded == "" {
			t.Fatal("Encode returned empty string")
		}

		// Note: byte-equal may fail due to canonicalization; compare structural equality
		// by decoding both and comparing proof counts and total value
		originalProofs, _ := decoded.ProofsSimple()
		decodedAgain, _ := cdk_ffi.TokenDecode(reencoded)
		defer decodedAgain.Destroy()
		reencodedProofs, _ := decodedAgain.ProofsSimple()

		if len(originalProofs) != len(reencodedProofs) {
			t.Fatalf("Proof count mismatch: original %d, reencoded %d", len(originalProofs), len(reencodedProofs))
		}

		originalValue, _ := decoded.Value()
		reencodedValue, _ := decodedAgain.Value()
		if originalValue.Value != reencodedValue.Value {
			t.Fatalf("Value mismatch: original %d, reencoded %d", originalValue.Value, reencodedValue.Value)
		}
	})

	t.Run("extracts_value", func(t *testing.T) {
		decoded, err := cdk_ffi.TokenDecode(knownGoodV3Token)
		if err != nil {
			t.Fatalf("TokenDecode failed: %v", err)
		}
		if decoded == nil {
			t.Fatal("TokenDecode returned nil Token")
		}
		defer decoded.Destroy()

		value, err := decoded.Value()
		if err != nil {
			t.Fatalf("Value failed: %v", err)
		}
		if value.Value <= 0 {
			t.Fatalf("Expected value > 0, got %d", value.Value)
		}
	})

	t.Run("extracts_mint_url", func(t *testing.T) {
		decoded, err := cdk_ffi.TokenDecode(knownGoodV3Token)
		if err != nil {
			t.Fatalf("TokenDecode failed: %v", err)
		}
		if decoded == nil {
			t.Fatal("TokenDecode returned nil Token")
		}
		defer decoded.Destroy()

		mintUrl, err := decoded.MintUrl()
		if err != nil {
			t.Fatalf("MintUrl failed: %v", err)
		}
		if !strings.Contains(mintUrl.Url, "testnut.cashu.space") {
			t.Fatalf("Expected mint URL to contain 'testnut.cashu.space', got %s", mintUrl.Url)
		}
	})
}

func TestCdkGoMalformedToken(t *testing.T) {
	t.Run("empty_string_returns_error", func(t *testing.T) {
		decoded, err := cdk_ffi.TokenDecode("")
		if err == nil {
			t.Fatal("Expected error for empty token, got nil")
			decoded.Destroy() // cleanup if somehow created
		}
		if decoded != nil {
			t.Fatal("Expected nil Token for empty input, got non-nil")
			decoded.Destroy()
		}
	})

	t.Run("garbage_returns_error", func(t *testing.T) {
		decoded, err := cdk_ffi.TokenDecode("not-a-cashu-token")
		if err == nil {
			t.Fatal("Expected error for garbage token, got nil")
			decoded.Destroy() // cleanup if somehow created
		}
		if decoded != nil {
			t.Fatal("Expected nil Token for garbage input, got non-nil")
			decoded.Destroy()
		}
	})

	t.Run("missing_prefix_returns_error", func(t *testing.T) {
		decoded, err := cdk_ffi.TokenDecode("eyJ0b2tlbiI6W3si...") // valid base64 JSON but missing cashuA@/cashuB prefix
		if err == nil {
			t.Fatal("Expected error for missing prefix, got nil")
			decoded.Destroy() // cleanup if somehow created
		}
		if decoded != nil {
			t.Fatal("Expected nil Token for missing prefix, got non-nil")
			decoded.Destroy()
		}
	})
}
