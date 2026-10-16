package main

import (
	"fmt"
	"strings"
	"sync"
)

// Tool is one CVM tool backed by one CLI socket command. The surface below IS
// the security boundary the ADR fixes (cvm-control-plane-decision.md, D3):
// every entry here becomes a callable tool over Nostr, so an addition to this
// list is a security decision, not a feature toggle.
type Tool struct {
	// Name is the MCP tool name (e.g. "wallet_balance" — exposed as
	// tool:wallet_balance over CVM).
	Name string
	// Description is what tools/list tells a remote caller.
	Description string
	// CLICommand and CLIArgs are the socket message this tool maps to.
	CLICommand string
	CLIArgs    []string
	// Write marks state-changing tools. Write tools are maintainer-only
	// (Authorizer keys on this flag).
	Write bool
}

// ToolSurface returns the complete, enumerable tool list. The kind-11317
// announcement is generated from it, so what is here is what the world sees.
//
// Deliberately ABSENT (ADR D3): `drain cashu` (its response IS spendable
// ecash — publishing it to a relay is a money-printing bug), `wallet fund`
// (pushes a token through a relay), and all of `ssl *` (credential material).
// Absent here means absent everywhere: there is no config flag that adds them
// back without a maintainer-reviewed change to this file and its ADR.
func ToolSurface() []Tool {
	return []Tool{
		// — read: open to any signed caller —
		{Name: "status", Description: "Service status: uptime, running state, config/wallet/network health", CLICommand: "status"},
		{Name: "health", Description: "Liveness probe: returns healthy only when components are up", CLICommand: "health"},
		{Name: "version", Description: "Version, commit and build info of the running service", CLICommand: "version"},
		{Name: "wallet_balance", Description: "Wallet balance in sats (no secrets)", CLICommand: "wallet", CLIArgs: []string{"balance"}},
		{Name: "wallet_info", Description: "Wallet info: balance and addresses (no key material)", CLICommand: "wallet", CLIArgs: []string{"info"}},
		{Name: "config_get", Description: "Current configuration (secrets redacted by the service)", CLICommand: "config", CLIArgs: []string{"get"}},
		{Name: "config_schema", Description: "Configuration schema: every field, its type and editability", CLICommand: "config", CLIArgs: []string{"schema"}},
		{Name: "private_status", Description: "Private network state: SSID, enabled", CLICommand: "network", CLIArgs: []string{"private", "status"}},
		{Name: "upstream_list", Description: "Configured upstream STA interfaces", CLICommand: "upstream", CLIArgs: []string{"list-upstream"}},
		{Name: "upstream_known", Description: "Known upstream networks from the last scan", CLICommand: "upstream", CLIArgs: []string{"known"}},
		{Name: "upstream_scan", Description: "Scan for upstream WiFi networks now (read-only; does not connect)", CLICommand: "upstream", CLIArgs: []string{"scan"}},

		// — write: maintainer npub only —
		{Name: "config_set", Description: "Set one config key (e.g. maintainer_npub); validated by the same rules as `tollgate config set`", CLICommand: "config", CLIArgs: []string{"set"}, Write: true},
		{Name: "private_enable", Description: "Enable the private network", CLICommand: "network", CLIArgs: []string{"private", "enable"}, Write: true},
		{Name: "private_disable", Description: "Disable the private network", CLICommand: "network", CLIArgs: []string{"private", "disable"}, Write: true},
		{Name: "private_rename", Description: "Rename the private network (arg: new SSID)", CLICommand: "network", CLIArgs: []string{"private", "rename"}, Write: true},
	}
}

// Authorizer decides who may call what. Authorization is identity-based: the
// caller is the npub that signed the request event (the CVM layer refuses
// unsigned/invalid signatures before this), so Authorize only answers "is
// this identity allowed this tool".
//
// Reads are open to any signed caller — a maintainer's dashboard, a monitoring
// probe, a curious customer. Writes require the maintainer npub. An unclaimed
// router (empty maintainer_npub) has no write path at all: with no maintainer
// configured there is nobody to authorize, and allowing writes would mean
// allowing them to everyone.
type Authorizer struct {
	mu sync.RWMutex
	// maintainerNpub is the config field's bech32 spelling, kept verbatim for
	// reporting and for the 15 s poll's change detection (which compares
	// against the config form).
	maintainerNpub string
	// maintainerHex is the SAME key decoded to the hex form the wire carries:
	// a NIP-01 caller identity (the gift-wrap seal author) is always hex, so
	// the write gate must compare hex against hex — comparing the config's
	// bech32 npub against the caller's hex could never match, leaving every
	// write tool permanently refused for the legitimate maintainer.
	maintainerHex string
	byName        map[string]Tool
}

func NewAuthorizer(maintainerNpub string) *Authorizer {
	byName := make(map[string]Tool, len(ToolSurface()))
	for _, tool := range ToolSurface() {
		byName[tool.Name] = tool
	}
	a := &Authorizer{maintainerNpub: maintainerNpub, byName: byName}
	a.maintainerHex = canonicalMaintainerHex(maintainerNpub)
	return a
}

// canonicalMaintainerHex decodes the config-form npub into the hex pubkey the
// wire compares against. Empty input (the unclaimed router) canonicalizes to
// empty. A malformed value canonicalizes to empty as well — fail-closed: an
// unparseable maintainer authorizes nobody, which is the same state as no
// maintainer at all (the config path's ValidateMaintainerNpub refuses
// malformed values before they can reach here; this is the belt to that
// braces).
func canonicalMaintainerHex(npub string) string {
	if npub == "" {
		return ""
	}
	hex, err := maintainerHexFromNpub(npub)
	if err != nil {
		return ""
	}
	return strings.ToLower(hex)
}

// SetMaintainer re-points authorization. It exists because resolving the
// maintainer ONCE at boot is a real defect (found by audit): a maintainer who
// hands a router over with `config set maintainer_npub <new>` would leave the
// OLD key holding full write authority — including further config_set — until
// the process restarted, silently and indefinitely. The inverse is as bad:
// unclaiming a router left a live write path, contradicting the claim gate.
func (a *Authorizer) SetMaintainer(npub string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.maintainerNpub = npub
	a.maintainerHex = canonicalMaintainerHex(npub)
}

// Maintainer reports the identity currently authorized for writes.
func (a *Authorizer) Maintainer() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.maintainerNpub
}

// Authorize returns nil when callerNpub may invoke toolName.
// Every refusal is an error string — silence is the failure mode.
func (a *Authorizer) Authorize(callerNpub, toolName string) error {
	tool, ok := a.byName[toolName]
	if !ok {
		return fmt.Errorf("unknown tool %q — tools/list returns the available surface", toolName)
	}
	if !tool.Write {
		return nil
	}
	a.mu.RLock()
	maintainerHex := a.maintainerHex
	a.mu.RUnlock()
	if maintainerHex == "" {
		return fmt.Errorf("router is unclaimed (maintainer_npub empty): no write tools are authorized until a maintainer claims it")
	}
	if strings.ToLower(callerNpub) != maintainerHex {
		return fmt.Errorf("tool %q is maintainer-only", toolName)
	}
	return nil
}
