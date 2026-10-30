package main

import (
	"testing"
)

// Audit findings 8.1 and 6.1, both HIGH and both found by reading source
// rather than by any test:
//
//   8.1  authorization was resolved ONCE at boot. A maintainer handing a router
//        over with `config set maintainer_npub <new>` left the OLD key with
//        full write authority — including further config_set — until the
//        process restarted, silently and indefinitely. Unclaiming was as bad:
//        the write path stayed live, contradicting the claim gate.
//
//   6.1  the announcement was never published. BuildAnnouncement had exactly
//        one caller (PublishAnnouncements), which nothing called — so no router
//        ever appeared in a maintainer's dashboard, and the ADR's "read back
//        from a live relay" assertion could not hold. A passing
//        TestAnnouncementShape gave false confidence about a path that never ran.

const (
	npubA = "npub1nng5mxkdh2mu593twukfr7j3fk5wxfy0v8ujf0e5g8nwwtzlphhqksqpew"
	npubB = "npub1hw6amg8p24ne08c9gdq8hhpqx0t0pwanpae9z25crn7m9uy7yarse465gr"
)

// After a handover, the NEW maintainer is authorized and the OLD one is not.
func TestAuthorizerFollowsMaintainerChange(t *testing.T) {
	a := NewAuthorizer(npubA)
	if err := a.Authorize(npubA, "config_set"); err != nil {
		t.Fatalf("initial maintainer refused: %v", err)
	}

	a.SetMaintainer(npubB)

	if err := a.Authorize(npubB, "config_set"); err != nil {
		t.Errorf("new maintainer refused after handover: %v", err)
	}
	if err := a.Authorize(npubA, "config_set"); err == nil {
		t.Error("OLD maintainer still authorized after handover — a handover would not actually transfer control")
	}
	if a.Maintainer() != npubB {
		t.Errorf("Maintainer() = %q, want the new value", a.Maintainer())
	}
}

// Unclaiming must close the write path, matching the claim gate.
func TestAuthorizerUnclaimClosesWrites(t *testing.T) {
	a := NewAuthorizer(npubA)
	a.SetMaintainer("")
	for _, tool := range ToolSurface() {
		if tool.Write {
			if err := a.Authorize(npubA, tool.Name); err == nil {
				t.Errorf("unclaimed router still authorized %s — contradicts RequireMaintainer", tool.Name)
			}
		}
	}
}

// Reads stay open across a handover (they never depended on the maintainer).
func TestAuthorizerReadsUnaffectedByHandover(t *testing.T) {
	a := NewAuthorizer(npubA)
	a.SetMaintainer(npubB)
	if err := a.Authorize("npub1stranger", "status"); err != nil {
		t.Errorf("reads must stay open to any signed caller: %v", err)
	}
}

// The dispatcher must consult the CURRENT maintainer, not a captured one:
// this is the end-to-end form of 8.1 (dispatch.go holds the Authorizer).
func TestDispatchUsesCurrentMaintainer(t *testing.T) {
	bridge := &FakeBridge{fn: func(msg CLIMessage) (CLIResponse, error) {
		return CLIResponse{Success: true, Message: "ok"}, nil
	}}
	a := NewAuthorizer(npubA)
	d := NewDispatcher(a, bridge, "npub1server")

	params := rpcParams(t, "private_disable")

	if resp := d.Dispatch(npubA, req(1, params)); resp.Error != nil {
		t.Fatalf("original maintainer refused before handover: %+v", resp.Error)
	}

	a.SetMaintainer(npubB) // the handover happens while the daemon runs

	if resp := d.Dispatch(npubA, req(2, params)); resp.Error == nil {
		t.Fatal("after handover the OLD maintainer's write still succeeded")
	} else if resp.Error.Code != ErrAuthz {
		t.Fatalf("expected an authz refusal, got %+v", resp.Error)
	}
	if resp := d.Dispatch(npubB, req(3, params)); resp.Error != nil {
		t.Fatalf("new maintainer refused after handover: %+v", resp.Error)
	}
}

// ---- announcement publishing (6.1) ----

// MaintainerForConfig reads the claim from a config_get payload, which is how
// the daemon learns the CURRENT owner. Kept as a function so Serve and the
// re-publish path share one spelling.
func TestMaintainerFromConfigPayload(t *testing.T) {
	cases := []struct {
		name string
		data interface{}
		want string
	}{
		{"direct", map[string]interface{}{"maintainer_npub": npubA}, npubA},
		{"nested under config", map[string]interface{}{"config": map[string]interface{}{"maintainer_npub": npubA}}, npubA},
		{"absent", map[string]interface{}{"log_level": "info"}, ""},
		{"wrong type", map[string]interface{}{"maintainer_npub": 42}, ""},
		{"nil", nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := MaintainerFromConfigPayload(tc.data); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// The announcement must be re-published when the maintainer changes, so a
// handover re-points discovery — otherwise the old npub keeps finding a router
// it no longer owns.
func TestAnnouncementRebuildsOnMaintainerChange(t *testing.T) {
	serverSK := serverKeyForTest(t)

	first, err := BuildAnnouncement(serverSK, npubA)
	if err != nil {
		t.Fatalf("build A: %v", err)
	}
	second, err := BuildAnnouncement(serverSK, npubB)
	if err != nil {
		t.Fatalf("build B: %v", err)
	}
	hexA, err := maintainerHexFromNpub(npubA)
	if err != nil {
		t.Fatalf("decode npubA: %v", err)
	}
	hexB, err := maintainerHexFromNpub(npubB)
	if err != nil {
		t.Fatalf("decode npubB: %v", err)
	}
	if first.Tags.GetFirst([]string{"p"}).Value() != hexA {
		t.Fatalf("first announcement p tag = %q, want hex %q", first.Tags.GetFirst([]string{"p"}).Value(), hexA)
	}
	if second.Tags.GetFirst([]string{"p"}).Value() != hexB {
		t.Fatal("rebuilt announcement does not carry the new maintainer — discovery would not follow the handover")
	}
	// Same d slug: it is a replaceable event, so the new one replaces the old
	// rather than leaving a stale copy findable.
	if first.Tags.GetFirst([]string{"d"}).Value() != second.Tags.GetFirst([]string{"d"}).Value() {
		t.Fatal("d slug changed — the announcement would not replace, leaving a stale claim findable")
	}
}
