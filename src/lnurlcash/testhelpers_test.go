package lnurlcash

import (
	"encoding/hex"
	"fmt"
	"net/url"
	"testing"

	"github.com/btcsuite/btcd/btcutil/bech32"
)

// sprintf exists so the redaction tests can format a Note through every verb a
// careless log line might use, without importing fmt into every call site and
// without a linter complaining about a constant format string.
func sprintf(format string, args ...any) string { return fmt.Sprintf(format, args...) }

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("fixture URL %q: %v", raw, err)
	}
	return u
}

func bytesRepeat(b byte, n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = b
	}
	return out
}

// reencodeCW1WithWitness rebuilds a `cw1` with a different witness item, keeping
// the script and control block. It is how a test constructs a spend that does
// not open its own leaf — a case the parser must refuse before any network call.
func reencodeCW1WithWitness(t *testing.T, cw1 string, witness []byte) (string, error) {
	t.Helper()

	hrp, data, err := bech32.DecodeNoLimit(cw1)
	if err != nil {
		return "", err
	}
	if hrp != "cw" {
		return "", fmt.Errorf("fixture is %s1, not cw1", hrp)
	}
	raw, err := bech32.ConvertBits(data, 5, 8, false)
	if err != nil {
		return "", err
	}
	decoded, err := DecodeCW1(cw1)
	if err != nil {
		return "", err
	}

	out := append([]byte(nil), raw[:8]...)
	for _, item := range append([][]byte{decoded.Script, decoded.ControlBlock}, witness) {
		if len(item) > 0xffff {
			return "", fmt.Errorf("item too long")
		}
		out = append(out, byte(len(item)>>8), byte(len(item)))
		out = append(out, item...)
	}
	converted, err := bech32.ConvertBits(out, 8, 5, true)
	if err != nil {
		return "", err
	}
	return bech32.EncodeM("cw", converted)
}

// testOnlyHex keeps a compile-time use for hex in this file's helpers.
var _ = hex.EncodeToString
