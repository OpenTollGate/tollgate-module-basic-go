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

// Fixture: `ubus call network.wireless status` shape on a tri-band router,
// following captured outputs from openwrt/packages#23875 and the netifd
// wireless status docs. radio1 deliberately precedes radio0 to prove the
// section-name ordering; radio2 is disabled and its interface carries no
// ifname (netifd omits it while down).
const ubusStatusFixture = `{
	"radio1": {
		"up": true,
		"pending": false,
		"autostart": true,
		"disabled": false,
		"retry_setup_failed": false,
		"config": {
			"path": "pci0000:00/0000:00:00.0",
			"channel": "36",
			"band": "5g",
			"htmode": "HE80",
			"cell_density": 0
		},
		"interfaces": [
			{
				"section": "wifinet1",
				"ifname": "phy1-ap0",
				"config": {
					"mode": "ap",
					"ssid": "tollgate-VIL4",
					"encryption": "none",
					"network": ["lan"]
				}
			},
			{
				"section": "tollgate_sta_5g",
				"ifname": "phy1-sta0",
				"config": {
					"mode": "sta"
				}
			}
		]
	},
	"radio0": {
		"up": true,
		"pending": false,
		"autostart": true,
		"disabled": false,
		"retry_setup_failed": false,
		"config": {
			"path": "platform/soc/a000000.wifi",
			"channel": "1",
			"band": "2g",
			"htmode": "HT20",
			"cell_density": 0,
			"txpower": 27
		},
		"interfaces": [
			{
				"section": "wifinet0",
				"ifname": "phy0-ap0",
				"config": {
					"mode": "ap",
					"ssid": "tollgate-VIL4",
					"encryption": "none",
					"network": ["lan"]
				}
			}
		]
	},
	"radio2": {
		"up": false,
		"pending": false,
		"autostart": true,
		"disabled": true,
		"retry_setup_failed": false,
		"config": {
			"path": "platform/soc/b000000.wifi",
			"channel": "auto",
			"band": "6g",
			"htmode": "EHT80"
		},
		"interfaces": [
			{
				"section": "wifinet2",
				"config": {
					"mode": "ap"
				}
			}
		]
	}
}`

func TestParseWirelessStatus(t *testing.T) {
	status, err := parseWirelessStatus([]byte(ubusStatusFixture))
	if err != nil {
		t.Fatalf("parseWirelessStatus: %v", err)
	}
	if len(status) != 3 {
		t.Fatalf("parsed %d radios, want 3", len(status))
	}
	if got := status["radio0"].Interfaces[0].Ifname; got != "phy0-ap0" {
		t.Errorf("radio0 first ifname = %q, want phy0-ap0", got)
	}
	if got := status["radio1"].Interfaces[1].Section; got != "tollgate_sta_5g" {
		t.Errorf("radio1 second section = %q, want tollgate_sta_5g", got)
	}
	if !status["radio2"].Disabled {
		t.Errorf("radio2 disabled = false, want true")
	}
}

func TestParseWirelessStatus_Invalid(t *testing.T) {
	if _, err := parseWirelessStatus([]byte("not json")); err == nil {
		t.Error("parseWirelessStatus(invalid) = nil error, want error")
	}
}

func TestScanTargetsFromStatus(t *testing.T) {
	status, err := parseWirelessStatus([]byte(ubusStatusFixture))
	if err != nil {
		t.Fatalf("parseWirelessStatus: %v", err)
	}
	want := []scanTarget{
		{Radio: "radio0", Device: "phy0-ap0"},
		{Radio: "radio1", Device: "phy1-ap0"},
	}
	if got := scanTargetsFromStatus(status); !reflect.DeepEqual(got, want) {
		t.Errorf("scanTargetsFromStatus = %v, want %v", got, want)
	}
}

func TestScanTargetsFromStatus_Empty(t *testing.T) {
	if got := scanTargetsFromStatus(map[string]ubusRadioStatus{}); len(got) != 0 {
		t.Errorf("empty status produced %d targets, want 0", len(got))
	}
}
