package config_manager

import (
	"os"
	"path/filepath"
	"testing"
)

// TestWalletBackendLegacyConfigParsesAsDefault pins the upgrade path
// (#1139): a config written before the wallet section existed parses with
// an empty Backend, and the selection seam (tollwallet.OpenWallet) treats
// empty as the in-process default — the pre-selection behavior.
func TestWalletBackendLegacyConfigParsesAsDefault(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "config.json")
	body := `{"config_version":"v0.0.11","log_level":"info","metric":"bytes","step_size":22020096,"show_setup":true,"reseller_mode":true,"accepted_mints":[],"profit_share":[]}`
	if err := os.WriteFile(configPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Wallet.Backend != "" || cfg.Wallet.Socket != "" {
		t.Fatalf("legacy config must parse with an empty wallet section, got %+v", cfg.Wallet)
	}
}

// TestDefaultConfigCarriesWalletSection pins the shipped default: the
// default backend is the in-process wallet, and the socket names the
// packaged cdk-walletd daemon's path so backend=cdk needs no extra config.
func TestDefaultConfigCarriesWalletSection(t *testing.T) {
	cfg := NewDefaultConfig()
	if cfg.Wallet.Backend != "default" {
		t.Fatalf("default config backend = %q, want %q", cfg.Wallet.Backend, "default")
	}
	if cfg.Wallet.Socket != "/var/run/cdk-walletd.sock" {
		t.Fatalf("default config socket = %q, want /var/run/cdk-walletd.sock", cfg.Wallet.Socket)
	}
}

// TestWalletBackendDotPath pins the operator surface: `config set
// wallet.backend cdk` writes through the schema enum, a value outside the
// enum is refused, and the socket key round-trips.
func TestWalletBackendDotPath(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "config.json")
	installPath := filepath.Join(tmp, "install.json")
	identitiesPath := filepath.Join(tmp, "identities.json")
	cm, err := NewConfigManager(configPath, installPath, identitiesPath)
	if err != nil {
		t.Fatalf("NewConfigManager: %v", err)
	}

	if err := SetDotPath(cm, "wallet.backend", "cdk"); err != nil {
		t.Fatalf("SetDotPath(wallet.backend, cdk): %v", err)
	}
	loaded, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if loaded.Wallet.Backend != "cdk" {
		t.Fatalf("round-tripped backend = %q, want cdk", loaded.Wallet.Backend)
	}

	if err := SetDotPath(cm, "wallet.socket", "/tmp/other.sock"); err != nil {
		t.Fatalf("SetDotPath(wallet.socket): %v", err)
	}
	loaded, err = LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if loaded.Wallet.Socket != "/tmp/other.sock" {
		t.Fatalf("round-tripped socket = %q, want /tmp/other.sock", loaded.Wallet.Socket)
	}

	if err := SetDotPath(cm, "wallet.backend", "bogus"); err == nil {
		t.Fatal("a backend outside the schema enum must be refused")
	}
}
