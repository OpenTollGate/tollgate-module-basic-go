package mptcp_bonding

import (
	"reflect"
	"strings"
	"testing"
)

// Covers the address-level event entry points and the introspection helper.
// These are the paths the network monitor actually calls for DHCP renewals and
// STA association, so leaving them untested would mean the most common real
// events in production are the least verified.

func TestHandleAddressAddedRegistersNewAddress(t *testing.T) {
	c := validConfig()
	c.Uplinks = []Uplink{
		{Interface: "lan1", Address: "192.168.2.15"},
		{Interface: "phy0-sta0"},
	}
	ap := &fakeApplier{}
	r := NewReconciler(c, ap.apply)

	// A DHCP lease arrives on the wireless uplink.
	if err := r.HandleAddressAdded("phy0-sta0", "192.168.2.44"); err != nil {
		t.Fatalf("HandleAddressAdded returned error: %v", err)
	}
	if ap.calls != 1 {
		t.Fatalf("a new address on an uplink must reconcile, got %d applies", ap.calls)
	}
	if !containsUplink(r.RegisteredUplinks(), "phy0-sta0") {
		t.Errorf("the uplink should be registered after acquiring an address, got %v",
			r.RegisteredUplinks())
	}
}

func TestHandleAddressDeletedWithdrawsAddress(t *testing.T) {
	c := validConfig()
	c.Uplinks = []Uplink{
		{Interface: "lan1", Address: "192.168.2.15"},
		{Interface: "phy0-sta0", Address: "192.168.2.44"},
	}
	ap := &fakeApplier{}
	r := NewReconciler(c, ap.apply)

	if err := r.HandleAddressDeleted("phy0-sta0", "192.168.2.44"); err != nil {
		t.Fatalf("HandleAddressDeleted returned error: %v", err)
	}
	if ap.calls != 1 {
		t.Fatalf("losing an uplink address must reconcile, got %d applies", ap.calls)
	}
	if containsUplink(r.RegisteredUplinks(), "phy0-sta0") {
		t.Errorf("the uplink must be withdrawn after its address goes away, got %v",
			r.RegisteredUplinks())
	}
	if !containsUplink(r.RegisteredUplinks(), "lan1") {
		t.Errorf("the untouched uplink must remain registered, got %v", r.RegisteredUplinks())
	}
}

func TestRegisteredUplinksIsSortedAndReflectsState(t *testing.T) {
	c := validConfig()
	c.Uplinks = []Uplink{
		{Interface: "phy0-sta0", Address: "192.168.2.44"},
		{Interface: "br-private", Address: "10.63.101.1"},
		{Interface: "lan1"}, // declared, no address yet
	}
	r := NewReconciler(c, nil)

	got := r.RegisteredUplinks()
	want := []string{"br-private", "phy0-sta0"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("RegisteredUplinks must be sorted and exclude addressless uplinks\n got: %v\nwant: %v", got, want)
	}
}

func TestHandleAddressAddedOnNonUplinkIsIgnored(t *testing.T) {
	c := validConfig()
	c.Uplinks = []Uplink{
		{Interface: "lan1", Address: "192.168.2.15"},
		{Interface: "br-private", Address: "10.63.101.1"},
	}
	ap := &fakeApplier{}
	r := NewReconciler(c, ap.apply)

	if err := r.HandleAddressAdded("br-lan", "192.168.1.1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ap.calls != 0 {
		t.Errorf("an address on a non-uplink must not reconcile, got %d applies", ap.calls)
	}
}

func TestNilConfigReconcilerIsInert(t *testing.T) {
	// Defensive: a caller that has not loaded config yet must not panic when
	// the monitor starts firing events.
	ap := &fakeApplier{}
	r := NewReconciler(nil, ap.apply)

	if err := r.HandleInterfaceUp("lan1", "192.168.2.15"); err != nil {
		t.Fatalf("nil config must not error, got: %v", err)
	}
	if err := r.HandleInterfaceDown("lan1"); err != nil {
		t.Fatalf("nil config must not error on down, got: %v", err)
	}
	if ap.calls != 0 {
		t.Errorf("nil config must apply nothing, got %d", ap.calls)
	}
	if got := r.RegisteredUplinks(); len(got) != 0 {
		t.Errorf("nil config has no registered uplinks, got %v", got)
	}
}

func TestReconcileWithoutApplierIsInert(t *testing.T) {
	// A caller that wants events recorded before execution is wired must not
	// panic on a nil Applier.
	c := validConfig()
	c.Uplinks = []Uplink{
		{Interface: "lan1", Address: "192.168.2.15"},
		{Interface: "phy0-sta0"},
	}
	r := NewReconciler(c, nil)
	if err := r.HandleInterfaceUp("phy0-sta0", "192.168.2.44"); err != nil {
		t.Fatalf("nil applier must not error, got: %v", err)
	}
}

func TestLimitsSetCommandStringEndsWithNewline(t *testing.T) {
	c := validConfig()
	c.Uplinks = []Uplink{
		{Interface: "lan1", Address: "192.168.2.15"},
		{Interface: "br-private", Address: "10.63.101.1"},
	}
	s := c.LimitsSetCommandString()
	if s == "" {
		t.Fatal("enabled bond must render a limits command string")
	}
	if s[len(s)-1] != '\n' {
		t.Errorf("the string form must end with a newline for safe concatenation, got %q", s)
	}

	// Disabled renders nothing at all.
	if got := (&Config{}).LimitsSetCommandString(); got != "" {
		t.Errorf("disabled bond must render no limits string, got %q", got)
	}
}

func containsUplink(list []string, name string) bool {
	for _, s := range list {
		if s == name {
			return true
		}
	}
	return false
}

func TestReconcileCurrentRegistersStartupUplinks(t *testing.T) {
	// The detector only reports CHANGES. An uplink that came up before the
	// daemon started would otherwise never be registered, so startup must
	// explicitly reconcile what is already configured.
	c := validConfig()
	c.Uplinks = []Uplink{
		{Interface: "lan1", Address: "192.168.2.15"},
		{Interface: "br-private", Address: "10.63.101.1"},
	}
	ap := &fakeApplier{}
	r := NewReconciler(c, ap.apply)

	if err := r.ReconcileCurrent(); err != nil {
		t.Fatalf("ReconcileCurrent returned error: %v", err)
	}
	if ap.calls != 1 {
		t.Fatalf("startup must apply exactly once, got %d", ap.calls)
	}
	if !strings.Contains(ap.last, "192.168.2.15") || !strings.Contains(ap.last, "10.63.101.1") {
		t.Errorf("both configured uplinks must be registered at startup, got:\n%s", ap.last)
	}
	// A second call must be a no-op: startup may run more than once.
	if err := r.ReconcileCurrent(); err != nil {
		t.Fatal(err)
	}
	if ap.calls != 1 {
		t.Errorf("a repeated startup reconcile must not re-apply, got %d", ap.calls)
	}
}

func TestReconcileCurrentDisabledIsInert(t *testing.T) {
	ap := &fakeApplier{}
	r := NewReconciler(&Config{}, ap.apply)
	if err := r.ReconcileCurrent(); err != nil {
		t.Fatalf("disabled bond must not error: %v", err)
	}
	if ap.calls != 0 {
		t.Errorf("disabled bond must apply nothing, got %d", ap.calls)
	}
}
