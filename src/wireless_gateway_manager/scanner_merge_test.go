package wireless_gateway_manager

import "testing"

// mergeScanResults folds the two scan views (iwinfo, then iw) into one list.
// It is the join that lets the element-derived fields come from iw while
// entries only iwinfo saw are preserved. Both halves are load-bearing, so both
// are pinned here.

func TestMergeScanResults_EnrichesExistingByBSSID(t *testing.T) {
	// iwinfo saw the AP but has no elements; iw saw the same AP with the
	// vendor element. The merged record must carry the iw-derived fields.
	iwinfo := []NetworkInfo{{
		BSSID: "aa:bb:cc:dd:ee:01", SSID: "!TollGate-AB12",
		Signal: -60, Encryption: "none", Radio: "radio0",
	}}
	iw := []NetworkInfo{{
		BSSID: "aa:bb:cc:dd:ee:01", SSID: "!TollGate-AB12",
		Signal: -55, Radio: "radio0",
		IsTollGate: true, RawIEs: []byte{0xDD, 0x03, 0x21, 0x21, 0x21},
		PricePerStep: 7, StepSize: 1000,
	}}

	got := mergeScanResults(iwinfo, iw)
	if len(got) != 1 {
		t.Fatalf("want 1 merged record, got %d", len(got))
	}
	n := got[0]
	if !n.IsTollGate {
		t.Errorf("IsTollGate = false, want true from the iw view")
	}
	if len(n.RawIEs) == 0 {
		t.Errorf("RawIEs empty, want the iw element bytes")
	}
	if n.PricePerStep != 7 || n.StepSize != 1000 {
		t.Errorf("pricing = %d/%d, want 7/1000 from the iw view", n.PricePerStep, n.StepSize)
	}
	// iwinfo's encryption must survive: iw has no such line.
	if n.Encryption != "none" {
		t.Errorf("Encryption = %q, want %q preserved from the iwinfo view", n.Encryption, "none")
	}
	// Signal was already set by iwinfo, so iw must not overwrite it.
	if n.Signal != -60 {
		t.Errorf("Signal = %d, want -60 (iwinfo's value kept when already set)", n.Signal)
	}
}

func TestMergeScanResults_KeepsEntriesOnlyOneViewSaw(t *testing.T) {
	// The hidden AP case: iwinfo reports it, iw may omit it entirely.
	iwinfo := []NetworkInfo{
		{BSSID: "aa:bb:cc:dd:ee:01", SSID: "OnlyIwinfo", Signal: -70},
	}
	iw := []NetworkInfo{
		{BSSID: "aa:bb:cc:dd:ee:02", SSID: "OnlyIW", Signal: -50, IsTollGate: true},
	}

	got := mergeScanResults(iwinfo, iw)
	if len(got) != 2 {
		t.Fatalf("want 2 records (one from each view), got %d", len(got))
	}
	seen := map[string]bool{}
	for _, n := range got {
		seen[n.BSSID] = true
	}
	if !seen["aa:bb:cc:dd:ee:01"] {
		t.Errorf("iwinfo-only entry was dropped")
	}
	if !seen["aa:bb:cc:dd:ee:02"] {
		t.Errorf("iw-only entry was dropped")
	}
}

func TestMergeScanResults_FillsGapsFromSecondView(t *testing.T) {
	// iwinfo could not give a signal (0 = unknown); the iw view can.
	iwinfo := []NetworkInfo{{BSSID: "aa:bb:cc:dd:ee:03", SSID: "Gappy", Signal: 0, Encryption: ""}}
	iw := []NetworkInfo{{BSSID: "aa:bb:cc:dd:ee:03", SSID: "Gappy", Signal: -44, Encryption: "WPA2"}}

	got := mergeScanResults(iwinfo, iw)
	if len(got) != 1 {
		t.Fatalf("want 1 record, got %d", len(got))
	}
	if got[0].Signal != -44 {
		t.Errorf("Signal = %d, want -44 (gap filled from the iw view)", got[0].Signal)
	}
	if got[0].Encryption != "WPA2" {
		t.Errorf("Encryption = %q, want WPA2 (gap filled)", got[0].Encryption)
	}
}

func TestMergeScanResults_EmptySecondViewIsIdentity(t *testing.T) {
	base := []NetworkInfo{{BSSID: "aa:bb:cc:dd:ee:04", SSID: "Alone", Signal: -60}}
	got := mergeScanResults(base, nil)
	if len(got) != 1 || got[0].SSID != "Alone" {
		t.Fatalf("merging nothing changed the base list: %+v", got)
	}
}

// scanRadioWithIW must be best-effort: a failed or empty iw invocation returns
// nil so the caller keeps the iwinfo view, rather than propagating an error.
// The device name here cannot exist, so the exec fails deterministically.
func TestScanRadioWithIW_FailureReturnsNil(t *testing.T) {
	s := NewScanner(nil, true)
	if got := s.scanRadioWithIW("radio0", "definitely-not-a-real-device-xyz"); got != nil {
		t.Errorf("scanRadioWithIW returned %v, want nil on a failed iw invocation", got)
	}
}

func TestNewScanner_WiresVendorIEDiscovery(t *testing.T) {
	on := NewScanner(nil, true)
	if !on.vendorIEDiscovery {
		t.Errorf("NewScanner(nil, true).vendorIEDiscovery = false, want true")
	}
	off := NewScanner(nil, false)
	if off.vendorIEDiscovery {
		t.Errorf("NewScanner(nil, false).vendorIEDiscovery = true, want false")
	}
	// The setter exists for callers that mutate config at runtime.
	off.SetVendorIEDiscovery(true)
	if !off.vendorIEDiscovery {
		t.Errorf("SetVendorIEDiscovery(true) did not take effect")
	}
}
