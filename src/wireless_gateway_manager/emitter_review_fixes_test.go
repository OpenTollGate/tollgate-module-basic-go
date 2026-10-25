package wireless_gateway_manager

import (
	"strings"
	"testing"
)

// Tests for the defects a cold cross-family review found in the FIRST version of
// this emitter (2026-10-16). Each one failed in production terms while the
// original tests passed, which is the point: the tests asserted the mechanism
// but never that the mechanism had any input.

// CRITICAL: the original "preserves foreign elements" claim was theatre — the
// map it merged from was populated ONLY by tests, so in production the merge
// loop iterated nothing and the first write replaced whatever was on the AP.
// This test drives the preservation ONLY through the production path (uci), with
// no test ever touching foreignElements directly.
func TestSetLocalAPVendorElements_PreservesElementsFromUCI(t *testing.T) {
	f := &fakeUbus{listOut: "hostapd.phy0-ap0\n"}
	c := &Connector{runUbus: f.run}
	// A pre-existing element configured in uci (e.g. an 11k/11v or mesh element).
	const foreign = "dd0e0050f204104a0001101040"
	c.runUCI = func(args ...string) (string, error) {
		return "wireless.default_radio0.device='radio0'\n" +
			"wireless.default_radio0.vendor_elements='" + foreign + "'\n", nil
	}

	// Production path only: construct, then write. Nothing fills foreignElements.
	p := &VendorElementProcessor{connector: c}
	if err := p.SetLocalAPVendorElements(map[string]string{"tollgate": "dd06212121"}); err != nil {
		t.Fatalf("set: %v", err)
	}

	sent := f.setCall()
	if !strings.Contains(sent, foreign) {
		t.Errorf("pre-existing uci vendor element was NOT preserved:\n  %s\n"+
			"The original implementation only preserved elements that tests injected "+
			"by hand, so in production it clobbered them.", sent)
	}
	if !strings.Contains(sent, "dd06212121") {
		t.Errorf("our own element is missing:\n  %s", sent)
	}
}

// The same, but the element the emitter owns must NOT be re-captured as foreign,
// or it would be sent twice.
func TestLoadConfiguredVendorElements_DoesNotCaptureOurOwn(t *testing.T) {
	c := &Connector{runUbus: func(args ...string) (string, error) { return "hostapd.phy0-ap0\n", nil }}
	c.runUCI = func(args ...string) (string, error) {
		return "wireless.default_radio0.vendor_elements='dd06212121'\n", nil
	}
	p := &VendorElementProcessor{connector: c}
	p.loadConfiguredVendorElements()
	if len(p.foreignElements) != 0 {
		t.Errorf("captured our own TollGate element as foreign: %v — it would be "+
			"duplicated in every payload", p.foreignElements)
	}
}

// MAJOR: a broken ubus must not look like "no hostapd configured".
func TestSetLocalAPVendorElements_UbusListFailureIsAnError(t *testing.T) {
	f := &fakeUbus{listErr: errFake("ubus: no such service")}
	p := &VendorElementProcessor{connector: &Connector{runUbus: f.run}}

	err := p.SetLocalAPVendorElements(map[string]string{"tollgate": "dd06212121"})
	if err == nil {
		t.Errorf("ubus being unreachable returned nil — indistinguishable from a " +
			"healthy router whose radio is off, so a failure to advertise is invisible")
	}
}

// MAJOR: partial failure must be surfaced, not reported as plain success.
func TestSetLocalAPVendorElements_PartialFailureIsReported(t *testing.T) {
	f := &fakeUbus{listOut: "hostapd.phy0-ap0\nhostapd.phy1-ap0\n"}
	// first call succeeds, second fails
	n := 0
	c := &Connector{runUbus: func(args ...string) (string, error) {
		if len(args) > 0 && args[0] == "list" {
			return f.listOut, nil
		}
		n++
		if n > 1 {
			return "", errFake("ubus: timeout")
		}
		return "", nil
	}}
	p := &VendorElementProcessor{connector: c}

	err := p.SetLocalAPVendorElements(map[string]string{"tollgate": "dd06212121"})
	if err == nil {
		t.Errorf("advertised on only 1 of 2 interfaces and returned nil — a router " +
			"that advertises on zero APs could still report success")
	}
}

// MAJOR: the reader must report the MERGED set (what is actually on the air),
// not merely our own elements.
func TestGetLocalAPVendorElements_ReportsMergedSet(t *testing.T) {
	const foreign = "dd0e0050f204104a0001101040"
	f := &fakeUbus{listOut: "hostapd.phy0-ap0\n"}
	c := &Connector{runUbus: f.run}
	c.runUCI = func(args ...string) (string, error) {
		return "wireless.default_radio0.vendor_elements='" + foreign + "'\n", nil
	}
	p := &VendorElementProcessor{connector: c}

	if err := p.SetLocalAPVendorElements(map[string]string{"tollgate": "dd06212121"}); err != nil {
		t.Fatalf("set: %v", err)
	}
	got, err := p.GetLocalAPVendorElements()
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	found := false
	for _, v := range got {
		if v == foreign {
			found = true
		}
	}
	if !found {
		t.Errorf("reader omitted the preserved foreign element that IS on the AP: %v", got)
	}
}

// uci being unreadable must degrade to "no foreign elements known", not crash or
// block the write.
func TestLoadConfiguredVendorElements_UCIFailureIsTolerated(t *testing.T) {
	c := &Connector{runUbus: func(args ...string) (string, error) { return "hostapd.phy0-ap0\n", nil }}
	c.runUCI = func(args ...string) (string, error) { return "", errFake("uci: not found") }
	p := &VendorElementProcessor{connector: c}
	if err := p.SetLocalAPVendorElements(map[string]string{"tollgate": "dd06212121"}); err != nil {
		t.Errorf("an unreadable uci must not block the write: %v", err)
	}
}
