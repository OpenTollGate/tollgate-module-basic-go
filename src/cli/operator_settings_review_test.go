package cli

// Tests added in response to the cold cross-family review of PR #604 (round 1,
// 2026-09-27, deepseek/deepseek-v4-flash). Each one pins a property the review
// showed was either unasserted or untrue:
//
//   - MAJOR: `private-net set-password` echoed the passphrase back even when the
//     caller supplied it, contradicting the ADR's "no read path returns the
//     passphrase". It is now echoed only when this call minted it.
//   - MAJOR: preserve-on-save ("an empty value is not an instruction") had no
//     test proving that a blanked field is KEPT and never silently clears the
//     stored value, for any of the four operator settings.
//   - NIT: `secretFieldState` hardcoded `private_key`, so a second secret schema
//     field would have been silently under-reported.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
)

// TestEverySchemaSecretIsRedactedAndReported fails the moment a field is marked
// Secret in the config schema and the redacting read path was not extended with
// it. `handled` is deliberately explicit: a new secret has to be named here (and
// therefore in secretFieldValues) before this suite goes green again.
func TestEverySchemaSecretIsRedactedAndReported(t *testing.T) {
	cfg := config_manager.NewDefaultConfig()
	cfg.PrivateKey = "Unique-Pass-2026"

	handled := map[string]func() string{
		"private_key": func() string { return redactSecretFields(cfg).PrivateKey },
	}

	keys := secretJSONKeys()
	if len(keys) == 0 {
		t.Fatal("the schema declares no secret field; the redaction path is untested")
	}
	for _, key := range keys {
		get, ok := handled[key]
		if !ok {
			t.Fatalf("the schema marks %q Secret but redactSecretFields/secretFieldState do not handle it", key)
		}
		if got := get(); got != "" {
			t.Errorf("%s: redactSecretFields returned %q, want empty", key, got)
		}
		if !secretFieldState(cfg)[key] {
			t.Errorf("%s: secretFieldState did not report the stored secret", key)
		}
		if secretFieldState(config_manager.NewDefaultConfig())[key] {
			t.Errorf("%s: secretFieldState reported a secret where none is stored", key)
		}
	}
}

// TestHandleConfigSaveBlankMeansKeepNotClear pins the deliberate semantics the
// review asked about: for every operator setting whose empty value is already
// "keep what the router has", a blanked value in a wholesale save is KEPT and
// reported as kept. There is no path through `config save` that clears one of
// these — clearing them is not a state the router can be in (an empty SSID or an
// unset administration scope would take a network down, not configure it).
func TestHandleConfigSaveBlankMeansKeepNotClear(t *testing.T) {
	withTempNftablesDir(t)
	s := newConfigCLIServer(t)

	stored := map[string]string{
		"private_ssid":       "c08r4d0r-7F3A",
		"private_key":        "Unique-Pass-2026",
		"private_encryption": "psk2+ccmp",
		"admin_access":       AdminAccessLoopback,
	}
	for key, value := range stored {
		if err := config_manager.SetDotPath(s.configManager, key, value); err != nil {
			t.Fatalf("seed %s: %v", key, err)
		}
	}

	// Exactly what the board sends after the operator saves the form without
	// touching the private-network card and without clearing anything: the
	// payload it got from `config get` (secret blanked) merged with an
	// unrelated edit.
	cfg := config_manager.NewDefaultConfig()
	cfg.LogLevel = "debug"
	cfg.PrivateSSID = ""
	cfg.PrivateKey = ""
	cfg.PrivateEncryption = ""
	cfg.AdminAccess = ""
	payload, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	resp := s.handleConfigSave(string(payload))
	if !resp.Success {
		t.Fatalf("save failed: %s", resp.Error)
	}

	after := s.configManager.GetConfig()
	for key, want := range stored {
		if got := configFieldByName(after, key); got != want {
			t.Errorf("a blanked %s must be KEPT, got %q want %q (a wholesale save must never clear it)", key, got, want)
		}
		if !strings.Contains(resp.Message, key) {
			t.Errorf("the response must name %s as kept unchanged, got: %s", key, resp.Message)
		}
	}
}

// configFieldByName reads one of the operator settings back off a Config.
func configFieldByName(cfg *config_manager.Config, key string) string {
	if cfg == nil {
		return ""
	}
	switch key {
	case "private_ssid":
		return cfg.PrivateSSID
	case "private_key":
		return cfg.PrivateKey
	case "private_encryption":
		return cfg.PrivateEncryption
	case "admin_access":
		return cfg.AdminAccess
	}
	return ""
}

// TestPrivateNetworkPasswordDataEchoesOnlyAGeneratedPassphrase pins the fix for
// the review's first MAJOR: a passphrase the caller supplied is never echoed
// back, while one this call minted has to be returned or the operator could not
// learn it at all.
func TestPrivateNetworkPasswordDataEchoesOnlyAGeneratedPassphrase(t *testing.T) {
	const supplied = "Unique-Pass-2026"

	if data := privateNetworkPasswordData(false, supplied); data != nil {
		t.Errorf("a caller-supplied passphrase must not be echoed back, got %v", data)
	}

	minted := privateNetworkPasswordData(true, "Generated-Pass-2026")
	if minted == nil {
		t.Fatal("a generated passphrase must be returned to its caller")
	}
	if got, ok := minted["new_password"].(string); !ok || got != "Generated-Pass-2026" {
		t.Errorf("the generated passphrase must be the only value returned, got %v", minted)
	}
	if len(minted) != 1 {
		t.Errorf("the response body must carry the passphrase and nothing else, got %v", minted)
	}
}
