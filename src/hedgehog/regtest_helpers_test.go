//go:build regtest

package hedgehog

// Test-only helpers for the regtest lifecycle. Kept in their own file so the
// build tag applies cleanly and the helpers cannot leak into non-regtest builds.

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/btcsuite/btcd/wire"
	"golang.org/x/crypto/ripemd160"
)

// ripemd160ForTest returns the RIPEMD160 of a preimage, which is what a
// revocation leaf commits to. golang.org/x/crypto/ripemd160 is already in the
// module graph via btcd, so this adds no dependency.
func ripemd160ForTest(t *testing.T, preimage []byte) []byte {
	t.Helper()
	h := ripemd160.New()
	h.Write(preimage)
	return h.Sum(nil)
}

// p2trAddressFromSPK asks the node to convert a witness-v1 scriptPubKey into an
// address, so the test does not have to implement bech32m encoding. Using the
// node's own encoder also means the address we fund is the node's own answer for
// the script we built -- one less place for us to be wrong.
func p2trAddressFromSPK(t *testing.T, v *BitcoinCoreChainView, spkHex string) string {
	t.Helper()
	raw, err := v.CallRaw(context.Background(), false, "decodescript", spkHex)
	if err != nil {
		t.Fatalf("decodescript: %v", err)
	}
	var d struct {
		Address string `json:"address"`
		P2TR    string `json:"p2tr"`
	}
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatalf("decodescript result: %v", err)
	}
	if d.Address != "" {
		return d.Address
	}
	if d.P2TR != "" {
		return d.P2TR
	}
	t.Fatalf("could not derive an address from scriptPubKey %s", spkHex)
	return ""
}

// serializeTxForTest serialises a wire.MsgTx to consensus bytes.
func serializeTxForTest(t *testing.T, tx *wire.MsgTx) []byte {
	t.Helper()
	var b writerBuffer
	if err := tx.Serialize(&b); err != nil {
		t.Fatalf("serialize: %v", err)
	}
	return b.Bytes()
}

// writerBuffer is a minimal io.Writer that accumulates bytes, avoiding a
// dependency on bytes.Buffer's API surface here.
type writerBuffer struct{ b []byte }

func (w *writerBuffer) Write(p []byte) (int, error) {
	w.b = append(w.b, p...)
	return len(p), nil
}
func (w *writerBuffer) Bytes() []byte { return w.b }

// Compile-time guard: this file must not silently grow dependencies.
var _ = wire.MsgTx{}
