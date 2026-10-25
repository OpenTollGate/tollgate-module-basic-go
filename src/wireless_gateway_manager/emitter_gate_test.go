package wireless_gateway_manager

import "testing"

// The emitter must be inert unless the operator opts in, so that merging it
// changes nothing on an existing deployment. These tests pin the constructor
// contract that carries that gate.

func TestNewVendorElementProcessor_DefaultsToDisabled(t *testing.T) {
	p := NewVendorElementProcessor(nil)
	if !p.disabled {
		t.Errorf("NewVendorElementProcessor() must default to disabled — merging the " +
			"emitter has to be a no-op on an existing deployment")
	}
}

func TestNewVendorElementProcessor_EnabledWhenAsked(t *testing.T) {
	p := NewVendorElementProcessor(nil, true)
	if p.disabled {
		t.Errorf("NewVendorElementProcessor(_, true) must be enabled")
	}
}

func TestVendorElementProcessor_DisabledWritesNothing(t *testing.T) {
	f := &fakeUbus{listOut: "hostapd.phy0-ap0\n"}
	p := &VendorElementProcessor{connector: &Connector{runUbus: f.run}, disabled: true}

	if err := p.SetLocalAPVendorElements(map[string]string{"tollgate": "dd06212121"}); err != nil {
		t.Fatalf("set: %v", err)
	}
	if len(f.calls) != 0 {
		t.Errorf("disabled processor made %d ubus call(s); it must make none: %v", len(f.calls), f.calls)
	}
}

// An enabled processor writes, so the gate is not simply "never writes".
func TestVendorElementProcessor_EnabledWrites(t *testing.T) {
	f := &fakeUbus{listOut: "hostapd.phy0-ap0\n"}
	p := &VendorElementProcessor{connector: &Connector{runUbus: f.run}}

	if err := p.SetLocalAPVendorElements(map[string]string{"tollgate": "dd06212121"}); err != nil {
		t.Fatalf("set: %v", err)
	}
	if f.setCall() == "" {
		t.Errorf("enabled processor wrote nothing; the gate is inverted")
	}
}

// A stable payload: the same element set must serialise identically every run,
// or every emit looks like a change to whatever diffs the hostapd state.
func TestSetLocalAPVendorElements_PayloadIsDeterministic(t *testing.T) {
	mk := func() string {
		f := &fakeUbus{listOut: "hostapd.phy0-ap0\n"}
		p := &VendorElementProcessor{
			connector: &Connector{runUbus: f.run},
			foreignElements: map[string]string{
				"zzz": "dd01ff", "aaa": "dd0100", "mmm": "dd0180",
			},
		}
		if err := p.SetLocalAPVendorElements(map[string]string{"tollgate": "dd06212121"}); err != nil {
			t.Fatalf("set: %v", err)
		}
		return f.setCall()
	}
	first := mk()
	for i := 0; i < 5; i++ {
		if got := mk(); got != first {
			t.Fatalf("payload differs between runs:\n first: %s\n run %d: %s", first, i, got)
		}
	}
}
