package cli

import "testing"

// The schema marks mptcp_bonding.password as Secret, but secretJSONKeys only
// scanned TOP-LEVEL schema fields and never descended into Children. So a
// nested secret was silently NOT redacted: `config get` would hand the bond's
// shared secret to any authenticated board session.
//
// These tests pin the nested case so a secret cannot be hidden one level down
// again.

func TestSecretJSONKeysFindsNestedSecret(t *testing.T) {
	keys := secretJSONKeys()
	found := false
	for _, k := range keys {
		if k == "password" {
			found = true
		}
	}
	if !found {
		t.Errorf("a secret nested under mptcp_bonding must be reported, got %v", keys)
	}
}

func TestSecretJSONKeysFindsPrivateKey(t *testing.T) {
	// The pre-existing top-level secret must keep working.
	keys := secretJSONKeys()
	found := false
	for _, k := range keys {
		if k == "private_key" {
			found = true
		}
	}
	if !found {
		t.Errorf("the top-level private_key secret must still be reported, got %v", keys)
	}
}
