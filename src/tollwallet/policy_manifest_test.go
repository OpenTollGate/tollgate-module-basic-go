package tollwallet

import (
	"path/filepath"
	"testing"
)

// knownBackends is the set of wallet backends the selection layer understands.
var knownBackends = map[string]bool{"gonuts": true, "cdk": true, "nucula": true}

// TestPolicyBackendsHaveManifests keeps the selection policy and the capability
// manifests consistent: every backend named by the policy must have a valid
// manifest, and every referenced backend must be a known one. This runs in CI
// (the module's test matrix includes this package), so drift is caught.
func TestPolicyBackendsHaveManifests(t *testing.T) {
	policy, err := LoadPolicy(filepath.Join("manifests", "wallet-policy.json"))
	if err != nil {
		t.Fatalf("LoadPolicy: %v", err)
	}
	if len(policy.Rules) == 0 {
		t.Fatal("wallet-policy.json has no rules")
	}
	seen := map[string]bool{}
	for _, r := range policy.Rules {
		if r.Backend == "" {
			t.Errorf("policy rule with empty backend: %+v", r)
			continue
		}
		if !knownBackends[r.Backend] {
			t.Errorf("policy references unknown backend %q", r.Backend)
			continue
		}
		seen[r.Backend] = true
		m, err := LoadManifest(filepath.Join("manifests", r.Backend+".json"))
		if err != nil {
			t.Errorf("backend %q has no valid manifest: %v", r.Backend, err)
			continue
		}
		if m.Kind == "" || len(m.Arches) == 0 || m.Licence == "" {
			t.Errorf("manifest for %q is missing required fields (kind/arches/licence): %+v", r.Backend, m)
		}
	}
	if len(seen) < 2 {
		t.Errorf("policy should reference at least two backends; saw %v", seen)
	}
}

// TestAllManifestsLoadable ensures every shipped manifest parses and is complete.
func TestAllManifestsLoadable(t *testing.T) {
	for backend := range knownBackends {
		m, err := LoadManifest(filepath.Join("manifests", backend+".json"))
		if err != nil {
			t.Errorf("LoadManifest(%s): %v", backend, err)
			continue
		}
		if m.Backend != backend {
			t.Errorf("manifest %s.json declares backend %q", backend, m.Backend)
		}
		if m.Kind != "in_process" && m.Kind != "sidecar" {
			t.Errorf("manifest %s has invalid kind %q", backend, m.Kind)
		}
	}
}
