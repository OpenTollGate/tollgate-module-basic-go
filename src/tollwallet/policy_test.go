package tollwallet

import (
	"path/filepath"
	"testing"
)

func TestPolicySelect(t *testing.T) {
	p, err := LoadPolicy(filepath.Join("manifests", "wallet-policy.json"))
	if err != nil {
		t.Fatalf("LoadPolicy: %v", err)
	}

	// 16 MB mipsel -> in-process gonuts (first rule).
	if b, _, ok := p.SelectBackend("mipsel_24kc", 16); !ok || b != "gonuts" {
		t.Errorf("mipsel/16MB = %q, %v; want gonuts", b, ok)
	}
	// roomier mipsel -> cdk sidecar.
	if b, s, ok := p.SelectBackend("mipsel_24kc", 64); !ok || b != "cdk" || s == "" {
		t.Errorf("mipsel/64MB = %q/%q, %v; want cdk + socket", b, s, ok)
	}
	// aarch64 -> cdk sidecar.
	if b, _, ok := p.SelectBackend("aarch64_cortex-a53", 128); !ok || b != "cdk" {
		t.Errorf("aarch64/128MB = %q, %v; want cdk", b, ok)
	}
	// unknown arch -> no rule.
	if _, _, ok := p.SelectBackend("riscv64", 128); ok {
		t.Error("expected no match for unknown arch")
	}
}

func TestPolicyMissing(t *testing.T) {
	if _, err := LoadPolicy(filepath.Join("manifests", "nope.json")); err == nil {
		t.Fatal("expected error for missing policy")
	}
}
