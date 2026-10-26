package merchant

import (
	"testing"
)

// The session event's `p` tag is a Nostr pubkey by contract; the raw-token
// flow has no customer Nostr identity, so the event must carry NO `p` tag at
// all — never the MAC, which `device-identifier` already records (#949, spec
// PR tollgate#26). These tests pin the tag set of the wire response every
// successful payment returns.
func TestSessionEventOmitsPTagAndCarriesTheContractTags(t *testing.T) {
	ndsctl := installRenewalNdsctl(t)
	ndsctl.setRegistered(t, true)

	w := &intentWallet{}
	m := newIntentMerchant(t, w, t.TempDir())

	event, err := m.PurchaseSession("cashuBreceive-intent-fixture", intentMAC)
	if err != nil {
		t.Fatalf("PurchaseSession: %v", err)
	}
	if event.Kind != 1022 {
		t.Fatalf("fresh payment must grant a session (kind 1022), got kind %d (code=%q)", event.Kind, noticeErrorCode(t, event))
	}

	var hasDevice, hasAllotment, hasMetric, hasStartTime bool
	for _, tag := range event.Tags {
		if tag[0] == "p" {
			t.Fatalf("session event must carry no p tag (raw-token flow has no customer pubkey); got %q", tag)
		}
		switch {
		case tag[0] == "device-identifier" && len(tag) >= 3 && tag[1] == "mac":
			if tag[2] != intentMAC {
				t.Fatalf("device-identifier must be the customer MAC %s, got %q", intentMAC, tag[2])
			}
			hasDevice = true
		case tag[0] == "allotment":
			hasAllotment = true
		case tag[0] == "metric":
			hasMetric = true
		case tag[0] == "start-time":
			hasStartTime = true
		}
	}
	if !hasDevice || !hasAllotment || !hasMetric || !hasStartTime {
		t.Fatalf("session event must keep its contract tags (device-identifier=%v allotment=%v metric=%v start-time=%v)",
			hasDevice, hasAllotment, hasMetric, hasStartTime)
	}

	if ok, err := event.CheckSignature(); !ok || err != nil {
		t.Fatalf("session event signature must verify after the tag change (ok=%v err=%v)", ok, err)
	}
	if got := w.receiveCalls.Load(); got != 1 {
		t.Fatalf("exactly one Receive must have run, got %d", got)
	}
}
