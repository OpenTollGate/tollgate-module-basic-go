// Config loading for the bond.
//
// This package is deliberately a LEAF module: it imports nothing from the rest
// of the tree. That is what lets upstream_detector depend on it without a cycle
// (upstream_detector already depends on config_manager, so the reverse edge
// would close the loop).
//
// The consequence is that the host module (main.go) owns where the bytes come
// from, and this file only turns bytes into a validated Config. The JSON tags on
// Config ARE the on-disk format; there is no parallel schema to drift.
package mptcp_bonding

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// LoadConfig parses the bond configuration and validates it.
//
// Passing nil (or an empty document) yields a disabled bond rather than an
// error. That matters for upgrades: every node deployed before this feature has
// no such section, and failing to load would take the module down on upgrade
// instead of leaving the node exactly as it was.
//
// Unknown keys are rejected. A typo (`enabld: true`) that silently leaves the
// bond off is the class of bug that costs hours in the field, so it is loud.
func LoadConfig(raw []byte) (*Config, error) {
	c := &Config{}

	if len(bytes.TrimSpace(raw)) == 0 {
		return c, nil
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(c); err != nil {
		return nil, fmt.Errorf("mptcp_bonding: parsing config: %w", err)
	}

	if err := c.Validate(); err != nil {
		return nil, err
	}

	return c, nil
}

// ConfigJSONExample returns a documented example of the on-disk shape, for
// `--help` output and for the operator runbook. It is generated from the type
// so it cannot drift from the real field names.
func ConfigJSONExample() (string, error) {
	example := &Config{
		Enabled: true,
		Policy:  DefaultPolicy,
		Server:  "203.0.113.10:65101",
		Method:  "chacha20-ietf-poly1305",
		// Password intentionally omitted: supplied at runtime, never committed.
		Uplinks: []Uplink{
			{Interface: "lan1", Address: "192.168.2.15"},
			{Interface: "br-private", Address: "10.63.101.1"},
		},
		Subflows:     DefaultSubflows,
		SteeringCIDR: "10.115.249.0/24",
		SocksPort:    1081,
		TproxyPort:   1082,
		FwMark:       "0x1",
		FwMarkTable:  100,
	}
	out, err := json.MarshalIndent(example, "", "  ")
	if err != nil {
		return "", fmt.Errorf("mptcp_bonding: rendering example config: %w", err)
	}
	return string(out), nil
}
