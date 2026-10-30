//go:build testenv

package tollwallet

// The corpus-decode offline gate: the parity flag's no-mint leg. Drives
// BOTH backends' DecodeToken over OpenTollGate/tollgate's frozen token
// corpus (manifest verdicts recorded 2026-10-16; the corpus is the
// prediction set — a divergence is triaged against the corpus, never
// against the test). Offline by design: construction with an unreachable
// mint + pure decode, so it runs anywhere, both tags, no CDK_MINT_URL.
//
// Usage:
//
//	WALLET_CONTRACT_CORPUS=/path/to/tollgate/corpus \
//	  go test -tags testenv -run TestWalletPortCorpusDecode .
//	  (and the same with -tags testenv,cdk_wallet)
//
// Known divergence (measured 2026-10-30, pinned here so the seed can be
// removed the day it heals): cdk-go 0.18.2's FFI decode rejects 5 of the
// 7 fixtures ("Invalid token: Invalid byte 10, offset N") — the same
// shapes cdk-cli 0.18.1 and Nutshell decode per the corpus verdicts, so
// the gap is cdk-go's token parser, not the tokens. The gonuts backend
// decodes 7/7. The gate stays STRICT for both backends (no backend
// branch in this file): run it under the gonuts tags as the release
// gate; a cdk-tags run documents the upstream gap until it closes.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

type corpusLifecycle struct {
	Fixture string `json:"fixture"`
	Shape   struct {
		TokenVersion int      `json:"token_version"`
		Mint         string   `json:"mint"`
		Amounts      []uint64 `json:"amounts"`
	} `json:"shape"`
}

func TestWalletPortCorpusDecode(t *testing.T) {
	corpusDir := os.Getenv("WALLET_CONTRACT_CORPUS")
	if corpusDir == "" {
		t.Skip("WALLET_CONTRACT_CORPUS not set — point it at OpenTollGate/tollgate's corpus/ checkout")
	}
	lifecycleDir := filepath.Join(corpusDir, "lifecycle")
	entries, err := os.ReadDir(lifecycleDir)
	if err != nil {
		t.Fatalf("read corpus lifecycle dir: %v", err)
	}

	// Construction must work offline for BOTH backends (an unreachable
	// mint exercises the degraded-construction path; decode itself never
	// touches the network).
	w, err := NewWalletPort(t.TempDir(), []string{"http://corpus.invalid"}, false)
	if err != nil {
		t.Fatalf("offline NewWalletPort: %v", err)
	}
	t.Cleanup(func() { _ = w.Shutdown() })

	ran := 0
	for _, e := range entries {
		name := e.Name()
		if filepath.Ext(name) != ".json" {
			continue
		}
		t.Run(name, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(lifecycleDir, name))
			if err != nil {
				t.Fatalf("read lifecycle: %v", err)
			}
			var lc corpusLifecycle
			if err := json.Unmarshal(raw, &lc); err != nil {
				t.Fatalf("parse lifecycle: %v", err)
			}
			tokenStr, err := os.ReadFile(filepath.Join(lifecycleDir, lc.Fixture))
			if err != nil {
				t.Fatalf("read fixture %s: %v", lc.Fixture, err)
			}
			tok, err := w.DecodeToken(string(tokenStr))
			if err != nil {
				t.Fatalf("DecodeToken (corpus verdict for this shape is decode-accept): %v", err)
			}
			t.Cleanup(tok.Close)

			want := uint64(0)
			for _, a := range lc.Shape.Amounts {
				want += a
			}
			if got := tok.Amount(); got != want {
				t.Errorf("amount = %d, corpus says %d", got, want)
			}
			if m := tok.Mint(); m != lc.Shape.Mint && m != normalizeMintURL(lc.Shape.Mint) {
				t.Errorf("mint = %q, corpus says %q", m, lc.Shape.Mint)
			}
			ran++
		})
	}
	if ran == 0 {
		t.Fatal("corpus carried no lifecycle fixtures — wrong dir?")
	}
}
