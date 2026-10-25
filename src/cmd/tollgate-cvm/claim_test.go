package main

import (
	"strings"
	"testing"
)

// An unclaimed router must not run the control plane at all. With no
// maintainer_npub there is no discovery tag (nobody can find it legitimately)
// and no write path — but READS would still answer any signed caller, which
// leaks status, wallet balance and configuration to the world. So the daemon
// refuses to serve unclaimed rather than serving a degraded mode.
func TestServeRefusesUnclaimed(t *testing.T) {
	if err := RequireMaintainer(""); err == nil {
		t.Fatal("unclaimed router was allowed to serve — reads would be open to any signed caller")
	}
	if err := RequireMaintainer("npub1nng5mxkdh2mu593twukfr7j3fk5wxfy0v8ujf0e5g8nwwtzlphhqksqpew"); err != nil {
		t.Fatalf("claimed router refused: %v", err)
	}
}

func TestRequireMaintainerMessageIsActionable(t *testing.T) {
	err := RequireMaintainer("")
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	for _, want := range []string{"maintainer_npub", "config set"} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal %q must name the fix (%q)", msg, want)
		}
	}
}
