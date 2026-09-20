package tollwallet

// Wallet selection policy: a small, dependency-free table that maps a router
// target (arch + flash tier) to the backend that should be used by default.
// This encodes "best wallet for the router we are targeting" as data, so the
// build/CI matrix and the router's first-boot setup agree on the same choice.
//
// The policy is a DEFAULT, never a lock: an operator can override the backend
// at runtime (config), and the sidecar protocol makes switching a restart, not
// a rebuild.

import (
	"encoding/json"
	"fmt"
	"os"
)

// PolicyMatch selects the targets a rule applies to. Empty fields match any.
type PolicyMatch struct {
	Arch    string `json:"arch,omitempty"`     // e.g. mipsel_24kc; empty = any
	FlashMB int    `json:"flash_mb,omitempty"` // upper bound; 0 = any
}

// PolicyRule maps a target to a backend.
type PolicyRule struct {
	Match   PolicyMatch `json:"match"`
	Backend string      `json:"backend"` // gonuts | cdk | nucula
	Socket  string      `json:"socket,omitempty"`
	Note    string      `json:"note,omitempty"`
}

// WalletPolicy is an ordered list of rules; the first match wins.
type WalletPolicy struct {
	Rules []PolicyRule `json:"rules"`
}

// LoadPolicy reads a wallet selection policy from a JSON file.
func LoadPolicy(path string) (*WalletPolicy, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var p WalletPolicy
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("tollwallet: invalid policy %s: %w", path, err)
	}
	return &p, nil
}

// SelectBackend returns the backend (and optional socket) for a target.
// A rule matches when its arch is empty or equal, and its flash bound is 0 or
// the target flash is within it. First match wins.
func (p *WalletPolicy) SelectBackend(arch string, flashMB int) (backend, socket string, ok bool) {
	for _, r := range p.Rules {
		if r.Match.Arch != "" && r.Match.Arch != arch {
			continue
		}
		if r.Match.FlashMB != 0 && flashMB > r.Match.FlashMB {
			continue
		}
		return r.Backend, r.Socket, true
	}
	return "", "", false
}
