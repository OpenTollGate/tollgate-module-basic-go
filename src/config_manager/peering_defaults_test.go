package config_manager

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCobradorPeeringDefaultsOn(t *testing.T) {
	cfg := NewDefaultConfig()
	if !cfg.ResellerMode {
		t.Error("reseller_mode: default must be true (opt-out)")
	}
	if !cfg.UpstreamWifi.VendorIEDiscovery {
		t.Error("vendor_ie_discovery: default must be true (opt-out)")
	}
}

func TestPeeringDefaultsFillMissingFieldsOnly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	// A pre-Cobrador config that has no reseller_mode and no
	// vendor_ie_discovery: both take the new defaults.
	old := `{"config_version":"v0.0.10","log_level":"info","accepted_mints":[],"profit_share":[],"step_size":60000,"metric":"milliseconds","show_setup":true,"upstream_detector":{},"upstream_session_manager":{},"upstream_wifi":{"scan_interval_seconds":300,"manual_pause_seconds":120}}`
	if err := os.WriteFile(path, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	migrated, err := EnsureDefaultConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if !migrated.ResellerMode {
		t.Error("missing reseller_mode on upgrade: want true")
	}
	if !migrated.UpstreamWifi.VendorIEDiscovery {
		t.Error("missing vendor_ie_discovery on upgrade: want true")
	}

	// An explicit false is an opt-out and must survive.
	optOut := `{"config_version":"v0.0.11","log_level":"info","accepted_mints":[],"profit_share":[],"step_size":60000,"metric":"milliseconds","show_setup":true,"reseller_mode":false,"upstream_detector":{},"upstream_session_manager":{},"upstream_wifi":{"scan_interval_seconds":300,"manual_pause_seconds":120,"vendor_ie_discovery":false}}`
	if err := os.WriteFile(path, []byte(optOut), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := EnsureDefaultConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ResellerMode {
		t.Error("explicit reseller_mode=false must be preserved")
	}
	if loaded.UpstreamWifi.VendorIEDiscovery {
		t.Error("explicit vendor_ie_discovery=false must be preserved")
	}
}
