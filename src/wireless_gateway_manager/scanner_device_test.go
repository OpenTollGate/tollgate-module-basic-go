package wireless_gateway_manager

import (
	"reflect"
	"testing"
)

func TestRadioIndex(t *testing.T) {
	cases := []struct {
		in   string
		want int
		ok   bool
	}{
		{"radio0", 0, true},
		{"radio1", 1, true},
		{"radio12", 12, true},
		{"phy0-ap1", 0, false},
		{"wlan0", 0, false},
		{"radio", 0, false},
		{"radiox", 0, false},
	}
	for _, tc := range cases {
		got, ok := radioIndex(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Errorf("radioIndex(%q) = (%d,%v), want (%d,%v)", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

// Fixture: real `iw dev` output on a two-radio GL-MT6000 (interfaces are
// phy<idx>-ap<k>, NOT wlan0/radio0 — the reason `iwinfo radio0 scan` is a
// usage error on modern OpenWrt).
const iwDevFixture = `phy#1
	Interface phy1-ap0
		ifindex 163
		wdev 0x100000037
		addr 94:83:c4:d1:57:5a
		ssid tollgate-VIL4
		type AP
	Interface phy1-ap1
		ifindex 150
		wdev 0x100000032
		addr 96:83:c4:d1:57:5a
		type AP
phy#0
	Interface phy0-ap0
		ifindex 12
		type AP
	Interface phy0-ap1
		ifindex 13
		type AP
`

func TestParsePhyInterfaces(t *testing.T) {
	if got := parsePhyInterfaces(iwDevFixture, 0); !reflect.DeepEqual(got, []string{"phy0-ap0", "phy0-ap1"}) {
		t.Errorf("phy0 = %v, want [phy0-ap0 phy0-ap1]", got)
	}
	if got := parsePhyInterfaces(iwDevFixture, 1); !reflect.DeepEqual(got, []string{"phy1-ap0", "phy1-ap1"}) {
		t.Errorf("phy1 = %v, want [phy1-ap0 phy1-ap1]", got)
	}
	if got := parsePhyInterfaces(iwDevFixture, 2); got != nil {
		t.Errorf("phy2 = %v, want nil", got)
	}
	if got := parsePhyInterfaces("", 0); got != nil {
		t.Errorf("empty = %v, want nil", got)
	}
}
