package tollwallet

// Capability manifest: what a wallet backend advertises about itself. Used by
// the sidecar handshake (`info`) and by the per-target selection tooling so the
// router (or its build) can pick the wallet best suited to the target.
//
// Manifests are dependency-free JSON so any backend (Go, Rust, C++) can emit
// one without sharing a schema library.

import (
	"encoding/json"
	"fmt"
	"os"
)

// StorageInfo describes the backend's on-flash storage behaviour.
type StorageInfo struct {
	Model            string `json:"model"`              // e.g. sqlite, nvs-blob
	WritesPerPayment string `json:"writes_per_payment"` // e.g. "o(1)", "whole_blob"
	CrashConsistent  bool   `json:"crash_consistent"`
	SeedAtRest       string `json:"seed_at_rest"` // e.g. "plaintext", "encrypted"
}

// ContractInfo records which parts of the WalletPort contract a backend meets.
type ContractInfo struct {
	NUT07   bool `json:"nut_07"`
	NUT09   bool `json:"nut_09"`
	Restore bool `json:"restore"`
	CGO     bool `json:"cgo"`
}

// Manifest is a wallet backend's capability descriptor.
type Manifest struct {
	Backend   string            `json:"backend"` // gonuts | cdk | nucula
	Kind      string            `json:"kind"`    // in_process | sidecar
	Version   string            `json:"version"`
	Arches    []string          `json:"arches"`
	SizeBytes map[string]int64  `json:"size_bytes"` // arch -> stripped bytes
	Storage   StorageInfo       `json:"storage"`
	Contract  ContractInfo      `json:"contract"`
	Licence   string            `json:"licence"`
	Extra     map[string]string `json:"extra,omitempty"`
}

// LoadManifest reads a capability manifest from a JSON file.
func LoadManifest(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("tollwallet: invalid manifest %s: %w", path, err)
	}
	if m.Backend == "" {
		return nil, fmt.Errorf("tollwallet: manifest %s has no backend", path)
	}
	return &m, nil
}
