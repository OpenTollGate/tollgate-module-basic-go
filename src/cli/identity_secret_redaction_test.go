package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
)

// The identity-secret half of #635: the `config get` payload must never carry
// the owned identities' Nostr private keys (they sign payouts and
// advertisements — strictly more damaging than the WPA passphrase the same
// payload already blanks), and the `save-identities` round-trip of that
// blanked payload must preserve the stored keys instead of wiping them.

func seedOwnedIdentity(t *testing.T, s *CLIServer, name, privateKey string) {
	t.Helper()

	identities := s.configManager.GetIdentities()
	identities.OwnedIdentities = append(identities.OwnedIdentities, config_manager.OwnedIdentity{
		Name:       name,
		PrivateKey: privateKey,
	})
	if err := config_manager.SaveIdentities(s.configManager.IdentitiesFilePath, identities); err != nil {
		t.Fatalf("seed identities: %v", err)
	}
	if err := s.configManager.ReloadIdentities(); err != nil {
		t.Fatalf("reload identities: %v", err)
	}
}

// TestConfigGetNeverReturnsIdentityPrivateKeys pins the read half: a stored
// owned identity's key is blanked in the payload, and the payload says the
// key is set (secret_set marker) so a UI can label it without seeing it.
func TestConfigGetNeverReturnsIdentityPrivateKeys(t *testing.T) {
	s := newConfigCLIServer(t)
	seedOwnedIdentity(t, s, "owner", "5100000000000000000000000000000000000000000000000000000000000001")

	resp := s.handleConfigGet()
	if !resp.Success {
		t.Fatalf("config get failed: %s", resp.Error)
	}

	payload, err := json.Marshal(resp.Data)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	if strings.Contains(string(payload), "5100000000000000000000000000000000000000000000000000000000000001") {
		t.Fatal("the config get payload carries an owned identity's Nostr private key")
	}

	var payloadMap map[string]json.RawMessage
	if err := json.Unmarshal(payload, &payloadMap); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	var secretSet map[string]bool
	if err := json.Unmarshal(payloadMap["secret_set"], &secretSet); err != nil {
		t.Fatalf("unmarshal secret_set: %v", err)
	}
	if !secretSet["identities.owner"] {
		t.Fatalf("secret_set must report identities.owner (got %v)", secretSet)
	}
}

// TestIdentitiesSavePreservesBlankedKeys pins the round-trip half: exactly
// what a board sends after rendering `config get` and saving without touching
// the identity card — owned identities with EMPTY keys — keeps the stored
// keys. An explicit key (a new identity, or a deliberate rotation) is taken
// as given.
func TestIdentitiesSavePreservesBlankedKeys(t *testing.T) {
	s := newConfigCLIServer(t)
	seedOwnedIdentity(t, s, "owner", "5100000000000000000000000000000000000000000000000000000000000001")

	getPayload, err := json.Marshal(s.handleConfigGet().Data)
	if err != nil {
		t.Fatalf("marshal get payload: %v", err)
	}
	var getFields map[string]json.RawMessage
	if err := json.Unmarshal(getPayload, &getFields); err != nil {
		t.Fatalf("unmarshal get payload: %v", err)
	}
	payload := getFields["identities"]

	resp := s.handleIdentitiesSave(string(payload))
	if !resp.Success {
		t.Fatalf("save-identities failed: %s", resp.Error)
	}

	after := s.configManager.GetIdentities()
	var ownerKey string
	for _, owned := range after.OwnedIdentities {
		if owned.Name == "owner" {
			ownerKey = owned.PrivateKey
		}
	}
	if ownerKey != "5100000000000000000000000000000000000000000000000000000000000001" {
		t.Fatalf("a blanked identity key round-trip wiped the stored key (got %q)", ownerKey)
	}
}

// TestIdentitiesSaveHonoursExplicitRotation pins that preserve-on-save is not
// preserve-forever: sending a real key for a known name replaces the stored
// one — the rotation path stays open.
func TestIdentitiesSaveHonoursExplicitRotation(t *testing.T) {
	s := newConfigCLIServer(t)
	seedOwnedIdentity(t, s, "owner", "5100000000000000000000000000000000000000000000000000000000000001")

	payload := `{"config_version":"v0.0.1","owned_identities":[{"name":"owner","privatekey":"5200000000000000000000000000000000000000000000000000000000000002"}]}`
	resp := s.handleIdentitiesSave(payload)
	if !resp.Success {
		t.Fatalf("save-identities failed: %s", resp.Error)
	}

	after := s.configManager.GetIdentities()
	var ownerKey string
	for _, owned := range after.OwnedIdentities {
		if owned.Name == "owner" {
			ownerKey = owned.PrivateKey
		}
	}
	if ownerKey != "5200000000000000000000000000000000000000000000000000000000000002" {
		t.Fatalf("an explicit rotation must replace the stored key (got %q)", ownerKey)
	}
}
