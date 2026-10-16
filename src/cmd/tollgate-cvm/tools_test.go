package main

import (
	"testing"
)

// The tool surface is the security boundary the ADR fixes
// (docs/architecture/cvm-control-plane-decision.md, D3). These tests pin it:
// money-adjacent commands must not be callable over Nostr, because their
// responses carry spendable instruments (`drain cashu` returns ecash) or
// credential material, and publishing those to a relay is a money-printing bug.

func TestToolSurfaceExcludesMoneyCommands(t *testing.T) {
	surface := ToolSurface()
	for _, tool := range surface {
		for _, banned := range []string{"drain", "fund", "ssl"} {
			if tool.CLICommand == banned || tool.Name == banned {
				t.Errorf("tool %q maps to banned command %q — money-adjacent commands must not be exposed over Nostr", tool.Name, tool.CLICommand)
			}
		}
		if tool.CLICommand == "wallet" && len(tool.CLIArgs) == 1 && tool.CLIArgs[0] == "drain" {
			t.Errorf("tool %q is a wallet drain — banned", tool.Name)
		}
	}
}

func TestToolSurfaceWriteToolsAreMarked(t *testing.T) {
	for _, tool := range ToolSurface() {
		switch tool.Name {
		case "status", "health", "version", "wallet_balance", "wallet_info", "config_get", "config_schema", "private_status", "upstream_list", "upstream_known", "upstream_scan":
			if tool.Write {
				t.Errorf("%s is a read tool but marked Write", tool.Name)
			}
		case "config_set", "private_enable", "private_disable", "private_rename":
			if !tool.Write {
				t.Errorf("%s is a state-changing tool but not marked Write (the maintainer-only gate keys on this)", tool.Name)
			}
		}
	}
}

// A tool that is neither in the read list nor the write list above is not
// automatically wrong — but the surface must be enumerable, because the
// announcement (11317) is generated from it.
func TestToolSurfaceIsNotEmpty(t *testing.T) {
	if len(ToolSurface()) < 10 {
		t.Fatalf("tool surface suspiciously small: %d tools", len(ToolSurface()))
	}
}

// TestAuthorize: the maintainer npub is the authorization boundary. A write
// tool from anyone else is refused — and the refusal is an error string, never
// silence (silence is the failure mode the ADR calls out).
func TestAuthorize(t *testing.T) {
	const maintainer = "npub1nng5mxkdh2mu593twukfr7j3fk5wxfy0v8ujf0e5g8nwwtzlphhqksqpew"
	const stranger = "npub1hw6amg8p24ne08c9gdq8hhpqx0t0pwanpae9z25crn7m9uy7yarse465gr"
	authz := NewAuthorizer(maintainer)
	maintainerHex, err := maintainerHexFromNpub(maintainer)
	if err != nil {
		t.Fatalf("fixture maintainer npub undecodable: %v", err)
	}

	if err := authz.Authorize(maintainerHex, "config_set"); err != nil {
		t.Errorf("maintainer must be authorized for a write tool: %v", err)
	}
	if err := authz.Authorize(stranger, "config_set"); err == nil {
		t.Error("stranger authorized for a write tool — the boundary is broken")
	}
	if err := authz.Authorize(stranger, "status"); err != nil {
		t.Errorf("read tools are open to any signed caller: %v", err)
	}
	if err := authz.Authorize(maintainer, "nonexistent_tool"); err == nil {
		t.Error("unknown tool must be refused, not silently allowed")
	}
	if err := authz.Authorize(stranger, "nonexistent_tool"); err == nil {
		t.Error("unknown tool from stranger must be refused")
	}
}

// An unclaimed router (empty maintainer_npub) has no write path at all: there
// is nobody to authorize. Reads stay open — the router still serves customers.
func TestAuthorizeUnclaimed(t *testing.T) {
	const stranger = "npub1hw6amg8p24ne08c9gdq8hhpqx0t0pwanpae9z25crn7m9uy7yarse465gr"
	authz := NewAuthorizer("")
	if err := authz.Authorize(stranger, "status"); err != nil {
		t.Errorf("reads must stay open on an unclaimed router: %v", err)
	}
	for _, tool := range ToolSurface() {
		if tool.Write {
			if err := authz.Authorize(stranger, tool.Name); err == nil {
				t.Errorf("unclaimed router authorized a write tool (%s) — with no maintainer there is nobody to authorize", tool.Name)
			}
		}
	}
}

// Client and server keys MUST differ — a shared key makes the server receive
// its own gift-wrapped requests and the response correlation loop eats itself.
// The check lives with the key configuration (cvm.go), and the transport test
// exercises it against generated keys; this test pins the pure rule.
func TestKeysMustDiffer(t *testing.T) {
	same := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if err := ValidateKeyPair(same, same); err == nil {
		t.Fatal("ValidateKeyPair accepted identical server and client keys — echo loop")
	}
	if err := ValidateKeyPair("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", same); err != nil {
		t.Fatalf("ValidateKeyPair refused distinct keys: %v", err)
	}
}
