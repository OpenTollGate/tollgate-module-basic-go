package wireless_gateway_manager

import (
	"strings"
	"testing"
)

// The emitter: the WRITE path. Before this change the entire emit chain was
// dead code — EmitTollGateVendorIE had zero callers and GetLocalAPVendorElements
// returned an empty map unconditionally — so no router could ever advertise
// itself and the scan-side producer had nothing to find.
//
// These tests pin three things that were missing:
//  1. a real GetLocalAPVendorElements round-trip (was a stub),
//  2. third-party vendor elements are PRESERVED, not wiped (the old
//     implementation sent only its own hex, clobbering co-resident elements
//     such as the mesh/11k/11v ones other tooling relies on),
//  3. an all-false success is not reported as success (SetLocalAPVendorElements
//     used to return nil even when every hostapd call failed).

// fakeUbus records ubus invocations and replies with canned output. It is
// wired in through the same seam pattern the Connector already uses for uci
// (runUCI), so tests exercise the real code path rather than a mock.
type fakeUbus struct {
	calls   [][]string
	listOut string
	callOut string
	callErr error
	listErr error
}

func (f *fakeUbus) run(args ...string) (string, error) {
	f.calls = append(f.calls, args)
	if len(args) > 0 && args[0] == "list" {
		return f.listOut, f.listErr
	}
	return f.callOut, f.callErr
}

// setCall returns the last set_vendor_elements payload sent, or "".
func (f *fakeUbus) setCall() string {
	out := ""
	for _, c := range f.calls {
		if len(c) >= 3 && c[2] == "set_vendor_elements" {
			out = strings.Join(c, " ")
		}
	}
	return out
}

func newTestProcessor(f *fakeUbus) *VendorElementProcessor {
	return &VendorElementProcessor{connector: &Connector{runUbus: f.run}}
}

// TestGetLocalAPVendorElements_RoundTripsWhatWasSet is the core RED test: the
// reader must report the elements that were actually applied, not an empty map.
func TestGetLocalAPVendorElements_RoundTripsWhatWasSet(t *testing.T) {
	f := &fakeUbus{listOut: "hostapd.phy0-ap0\nhostapd.phy1-ap0\n"}
	p := newTestProcessor(f)

	// After a successful set, the reader must see it.
	if err := p.SetLocalAPVendorElements(map[string]string{"tollgate": "dd06212121"}); err != nil {
		t.Fatalf("set: %v", err)
	}

	got, err := p.GetLocalAPVendorElements()
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got["tollgate"] != "dd06212121" {
		t.Errorf("GetLocalAPVendorElements()[tollgate] = %q, want %q — the reader "+
			"reported something other than what was applied", got["tollgate"], "dd06212121")
	}
}

// TestSetLocalAPVendorElements_PreservesForeignElements pins defect 2: the
// implementation must not wipe vendor elements it does not own.
func TestSetLocalAPVendorElements_PreservesForeignElements(t *testing.T) {
	// hostapd reports a pre-existing element (e.g. an 11k/11v or mesh element
	// set by other tooling).
	foreign := "dd0e0050f204104a0001101040"
	f := &fakeUbus{
		listOut: "hostapd.phy0-ap0\n",
		callOut: foreign,
	}
	p := newTestProcessor(f)
	p.foreignElements = map[string]string{"other": foreign}

	if err := p.SetLocalAPVendorElements(map[string]string{"tollgate": "dd06212121"}); err != nil {
		t.Fatalf("set: %v", err)
	}

	// The payload actually sent must contain BOTH elements.
	sent := f.setCall()
	if sent == "" {
		t.Fatal("no set_vendor_elements call was made")
	}
	if !strings.Contains(sent, foreign) {
		t.Errorf("foreign vendor element was dropped from the payload:\n  %s\n"+
			"wiping elements we do not own breaks co-resident tooling", sent)
	}
	if !strings.Contains(sent, "dd06212121") {
		t.Errorf("our own element is missing from the payload:\n  %s", sent)
	}
}

// TestSetLocalAPVendorElements_FailsLoudWhenAllCallsFail pins defect 3.
func TestSetLocalAPVendorElements_FailsLoudWhenAllCallsFail(t *testing.T) {
	f := &fakeUbus{
		listOut: "hostapd.phy0-ap0\nhotspot.1\n",
		callErr: errFake("ubus: not found"),
	}
	p := newTestProcessor(f)

	err := p.SetLocalAPVendorElements(map[string]string{"tollgate": "dd06212121"})
	if err == nil {
		t.Errorf("SetLocalAPVendorElements returned nil although every hostapd call " +
			"failed — a silent success here means the router never advertises and " +
			"nothing reports it")
	}
}

// TestSetLocalAPVendorElements_NoHostapdIsNotAnError — a router with the radio
// down is a legitimate state, not a failure to shout about.
func TestSetLocalAPVendorElements_NoHostapdIsNotAnError(t *testing.T) {
	f := &fakeUbus{listOut: "hotspot.1\nnetwork.interface\n"}
	p := newTestProcessor(f)
	if err := p.SetLocalAPVendorElements(map[string]string{"tollgate": "dd06212121"}); err != nil {
		t.Errorf("no hostapd interface should be a no-op, got error: %v", err)
	}
}

// TestEmitTollGateVendorIE_SendsEncodedElement ties the emitter to the encoder,
// so the two cannot drift.
func TestEmitTollGateVendorIE_SendsEncodedElement(t *testing.T) {
	f := &fakeUbus{listOut: "hostapd.phy0-ap0\n"}
	p := newTestProcessor(f)

	adv := TollGateAdvertisement{
		Version:      1,
		HasInternet:  true,
		MintURL:      "https://mint.example",
		PricePerStep: 7,
		StepSize:     1000,
	}
	if err := EmitTollGateVendorIE(p, adv); err != nil {
		t.Fatalf("emit: %v", err)
	}

	wantHex, _, err := EncodeTollGateVendorIE(adv)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	sent := f.setCall()
	if !strings.Contains(sent, wantHex) {
		t.Errorf("the emitted payload does not contain the encoder's output.\n got: %s\nwant substring: %s",
			sent, wantHex)
	}
}

// TestEmitTollGateVendorIE_GatedOffDoesNothing — the feature must be inert
// unless VendorIEDiscovery is enabled, so merging changes nothing by default.
func TestEmitTollGateVendorIE_GatedOffDoesNothing(t *testing.T) {
	f := &fakeUbus{listOut: "hostapd.phy0-ap0\n"}
	p := newTestProcessor(f)
	p.disabled = true // flag off

	if err := EmitTollGateVendorIE(p, TollGateAdvertisement{Version: 1, PricePerStep: 7}); err != nil {
		t.Fatalf("emit: %v", err)
	}
	for _, c := range f.calls {
		if len(c) >= 3 && c[2] == "set_vendor_elements" {
			t.Errorf("emitter wrote to hostapd while disabled: %v", c)
		}
	}
}

type errFake string

func (e errFake) Error() string { return string(e) }
