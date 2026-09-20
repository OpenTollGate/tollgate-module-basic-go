package tollwallet

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadManifest(t *testing.T) {
	m, err := LoadManifest(filepath.Join("manifests", "cdk.json"))
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if m.Backend != "cdk" || m.Kind != "sidecar" {
		t.Errorf("backend/kind = %s/%s", m.Backend, m.Kind)
	}
	if m.SizeBytes["aarch64_cortex-a53"] != 4346264 {
		t.Errorf("size not decoded: %+v", m.SizeBytes)
	}
	if m.Licence == "" {
		t.Error("licence empty")
	}
	if len(m.Arches) != 2 {
		t.Errorf("arches = %v", m.Arches)
	}
}

func TestLoadManifestMissing(t *testing.T) {
	if _, err := LoadManifest(filepath.Join("manifests", "does-not-exist.json")); err == nil {
		t.Fatal("expected error for missing manifest")
	}
}

func TestLoadManifestRejectsEmptyBackend(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(path, []byte(`{"kind":"sidecar"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadManifest(path); err == nil {
		t.Fatal("expected error for manifest with no backend")
	}
}
