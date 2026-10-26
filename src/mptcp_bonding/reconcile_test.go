package mptcp_bonding

import (
	"errors"
	"strings"
	"testing"
)

// The reconciler is the seam between the network monitor and the kernel: it
// turns "an interface came up / went away" into "re-register the endpoints".
//
// A bond is a moving target on a mobile box. A wireless uplink appears when the
// STA associates, disappears when it roams, and its address changes when DHCP
// renews. A script that runs once at boot drifts away from reality within
// minutes, and the failure is invisible: the bond simply stops using the
// interface that came back, and nobody notices until throughput is wrong.
//
// The reconciler is deliberately given its side effect as an injected function
// so the decision logic is testable without a router.

// fakeApplier records what the reconciler asked to be applied.
type fakeApplier struct {
	calls  int
	last   string
	failed error
}

func (f *fakeApplier) apply(script string) error {
	f.calls++
	f.last = script
	return f.failed
}

func TestReconcileAppliesOnUplinkUp(t *testing.T) {
	c := validConfig()
	c.Uplinks = []Uplink{
		{Interface: "lan1", Address: "192.168.2.15"},
		{Interface: "phy0-sta0"},
	}
	ap := &fakeApplier{}
	r := NewReconciler(c, ap.apply)

	// The wireless uplink associates and gets an address.
	err := r.HandleInterfaceUp("phy0-sta0", "192.168.2.44")
	if err != nil {
		t.Fatalf("HandleInterfaceUp returned error: %v", err)
	}
	if ap.calls != 1 {
		t.Fatalf("expected exactly one apply after an uplink came up, got %d", ap.calls)
	}
	if !strings.Contains(ap.last, "192.168.2.44") {
		t.Errorf("the newly-learned address must be registered, got:\n%s", ap.last)
	}
	if !strings.Contains(ap.last, "192.168.2.15") {
		t.Errorf("the existing uplink must still be registered, got:\n%s", ap.last)
	}
}

func TestReconcileIgnoresNonUplink(t *testing.T) {
	// An interface that is not part of the bond must not cause a rewrite. This
	// matters on a router where many interfaces flap (guest AP, loopback,
	// docker); reconciling on all of them would thrash the kernel state.
	c := validConfig()
	c.Uplinks = []Uplink{
		{Interface: "lan1", Address: "192.168.2.15"},
		{Interface: "br-private", Address: "10.63.101.1"},
	}
	ap := &fakeApplier{}
	r := NewReconciler(c, ap.apply)

	if err := r.HandleInterfaceUp("br-lan", "192.168.1.1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ap.calls != 0 {
		t.Errorf("a non-uplink interface must not trigger a reconcile, got %d applies", ap.calls)
	}
}

func TestReconcileRemovesEndpointOnDown(t *testing.T) {
	// When an uplink goes away its endpoint must be dropped, or the kernel
	// keeps advertising an address that is no longer reachable and the peer
	// wastes JOIN attempts on it.
	c := validConfig()
	c.Uplinks = []Uplink{
		{Interface: "lan1", Address: "192.168.2.15"},
		{Interface: "phy0-sta0", Address: "192.168.2.44"},
	}
	ap := &fakeApplier{}
	r := NewReconciler(c, ap.apply)

	if err := r.HandleInterfaceDown("phy0-sta0"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ap.calls != 1 {
		t.Fatalf("expected one apply after an uplink went down, got %d", ap.calls)
	}
	if strings.Contains(ap.last, "192.168.2.44") {
		t.Errorf("the departed uplink's address must no longer be registered, got:\n%s", ap.last)
	}
}

func TestReconcileDoesNotApplyWhenNothingChanged(t *testing.T) {
	// Reconcile is called on every event. Applying an identical script on every
	// flap is wasted work and briefly perturbs kernel state, so a no-op event
	// (setting the same address twice) must not re-apply.
	c := validConfig()
	c.Uplinks = []Uplink{
		{Interface: "lan1", Address: "192.168.2.15"},
		{Interface: "phy0-sta0", Address: "192.168.2.44"},
	}
	ap := &fakeApplier{}
	r := NewReconciler(c, ap.apply)

	if err := r.HandleInterfaceUp("phy0-sta0", "192.168.2.44"); err != nil {
		t.Fatal(err)
	}
	first := ap.calls

	// Same address again: nothing changed.
	if err := r.HandleInterfaceUp("phy0-sta0", "192.168.2.44"); err != nil {
		t.Fatal(err)
	}
	if ap.calls != first {
		t.Errorf("an unchanged address must not re-apply, calls went %d -> %d", first, ap.calls)
	}
}

func TestReconcileDisabledBondIsInert(t *testing.T) {
	c := &Config{}
	ap := &fakeApplier{}
	r := NewReconciler(c, ap.apply)

	if err := r.HandleInterfaceUp("lan1", "192.168.2.15"); err != nil {
		t.Fatalf("disabled bond must not error, got: %v", err)
	}
	if ap.calls != 0 {
		t.Errorf("a disabled bond must never apply anything, got %d calls", ap.calls)
	}
}

func TestReconcilePropagatesApplyError(t *testing.T) {
	// A failed apply must surface, not be swallowed: the caller needs to log it
	// and the operator needs to see the bond is not in the desired state.
	c := validConfig()
	c.Uplinks = []Uplink{
		{Interface: "lan1", Address: "192.168.2.15"},
		{Interface: "phy0-sta0"},
	}
	ap := &fakeApplier{failed: errors.New("ip: not found")}
	r := NewReconciler(c, ap.apply)

	err := r.HandleInterfaceUp("phy0-sta0", "192.168.2.44")
	if err == nil {
		t.Fatal("a failed apply must be returned to the caller, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("the underlying error must be preserved, got: %v", err)
	}
}
