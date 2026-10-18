package tollwallet

import (
	"path/filepath"
	"testing"
)

func TestOpenWalletSidecar(t *testing.T) {
	sock := fakeSidecar(t, testHandler)
	port, manifest, err := OpenWallet(WalletConfig{Backend: "cdk", Socket: sock})
	if err != nil {
		t.Fatalf("OpenWallet(cdk): %v", err)
	}
	if manifest == nil || manifest.Backend != "fake" {
		t.Fatalf("manifest = %+v", manifest)
	}
	if _, ok := port.(*SidecarWallet); !ok {
		t.Fatalf("expected *SidecarWallet, got %T", port)
	}
}

func TestOpenWalletSidecarNeedsSocket(t *testing.T) {
	if _, _, err := OpenWallet(WalletConfig{Backend: "nucula"}); err == nil {
		t.Fatal("expected error when socket is missing")
	}
}

func TestOpenWalletUnknownBackend(t *testing.T) {
	if _, _, err := OpenWallet(WalletConfig{Backend: "wat"}); err == nil {
		t.Fatal("expected error for unknown backend")
	}
}

func TestOpenWalletInProcessDefault(t *testing.T) {
	port, manifest, err := OpenWallet(WalletConfig{Backend: "", Path: t.TempDir(), AcceptedMints: []string{"https://testmint.example.com"}})
	if err != nil {
		t.Fatalf("OpenWallet(default): %v", err)
	}
	if manifest != nil {
		t.Errorf("in-process should have no manifest, got %+v", manifest)
	}
	if port == nil {
		t.Fatal("nil port")
	}
	_ = port
}

func TestPolicyOpenFor(t *testing.T) {
	p, err := LoadPolicy(filepath.Join("manifests", "wallet-policy.json"))
	if err != nil {
		t.Fatalf("LoadPolicy: %v", err)
	}
	sock := fakeSidecar(t, testHandler)

	// aarch64 -> cdk per policy; but the policy's socket path is fake, so an
	// explicit socket override must win.
	port, manifest, err := p.OpenFor("aarch64_cortex-a53", 128, WalletConfig{Socket: sock})
	if err != nil {
		t.Fatalf("OpenFor(aarch64): %v", err)
	}
	if manifest == nil {
		t.Fatal("expected sidecar manifest")
	}
	if _, ok := port.(*SidecarWallet); !ok {
		t.Fatalf("expected sidecar port, got %T", port)
	}

	// 16MB mipsel -> cdk sidecar per policy (every arch now selects cdk), so an
	// explicit socket override is required or the policy's fake path is used.
	if _, _, err := p.OpenFor("mipsel_24kc", 16, WalletConfig{Socket: sock}); err != nil {
		t.Fatalf("OpenFor(mipsel, policy default): %v", err)
	}

	// The policy is a DEFAULT, never a lock: an operator can still force the
	// in-process backend, which is what keeps the gonuts path usable as a
	// fallback for as long as we maintain both.
	port, manifest, err = p.OpenFor("mipsel_24kc", 16, WalletConfig{
		Backend: "gonuts", Path: t.TempDir(), AcceptedMints: []string{"https://testmint.example.com"},
	})
	if err != nil {
		t.Fatalf("OpenFor(mipsel, explicit gonuts): %v", err)
	}
	if manifest != nil {
		t.Errorf("in-process should have no manifest, got %+v", manifest)
	}
	_ = port
}
