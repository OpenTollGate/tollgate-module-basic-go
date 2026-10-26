package multi_wan

import (
	"encoding/json"
	"errors"
	cm "github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"math"
	"strings"
	"testing"
)

func fixture() *cm.MultiWANConfig {
	return &cm.MultiWANConfig{Enabled: true, Engine: "mwan3", AllowChange: "next_session", DefaultObjective: "o", MarkBase: "0x100", HealthCheck: cm.MultiWANHealthCheck{IntervalS: 5, Down: 3, Up: 2}, Sampler: cm.MultiWANSampler{WindowS: 60, MinSamples: 2}, Eligibility: cm.MultiWANEligibility{MinSamples: 2, MaxLossPct: 10, WeightMax: 100}, Uplinks: []cm.MultiWANUplink{{ID: "a", Source: "plain", Cost: &cm.MultiWANCost{Value: 10, Unit: "sats_gib"}}, {ID: "b", Source: "plain", Cost: &cm.MultiWANCost{Value: 20, Unit: "sats_gib"}}}, Objectives: []cm.MultiWANObjective{{ID: "o", Mode: "balanced"}}}
}
func TestNormalize(t *testing.T) {
	for _, tc := range []struct {
		unit              string
		value, rate, want float64
		flat              bool
	}{{"sats_gib", 40, 92, 40, false}, {"sats_mib", 2, 92, 2048, false}, {"sats_min", 40, 92, 59.4, false}, {"flat_month", 100, 92, 0, true}} {
		t.Run(tc.unit, func(t *testing.T) {
			got, err := Normalize(cm.MultiWANCost{Value: tc.value, Unit: tc.unit}, tc.rate)
			if tc.flat {
				if !errors.Is(err, ErrNotComparable) {
					t.Fatal(err)
				}
				return
			}
			if err != nil || math.Abs(got-tc.want) > .05 {
				t.Fatalf("%v %v", got, err)
			}
		})
	}
	for _, c := range []cm.MultiWANCost{{Value: -1, Unit: "sats_gib"}, {Value: 1, Unit: "invalid"}, {Value: math.NaN(), Unit: "sats_gib"}} {
		if _, err := Normalize(c, 10); err == nil {
			t.Fatal("accepted invalid cost")
		}
	}
	if _, err := Normalize(cm.MultiWANCost{Value: 1, Unit: "sats_min"}, 0); err == nil {
		t.Fatal("accepted zero rate")
	}
}
func TestValidation(t *testing.T) {
	cases := []struct {
		name, part string
		change     func(*cm.MultiWANConfig)
	}{
		{"mode", "mode", func(c *cm.MultiWANConfig) { c.Objectives[0].Mode = "oops" }},
		{"duplicate uplink", "duplicate", func(c *cm.MultiWANConfig) { c.Uplinks[1].ID = "a" }},
		{"duplicate objective", "duplicate", func(c *cm.MultiWANConfig) { c.Objectives = append(c.Objectives, c.Objectives[0]) }},
		{"empty uplinks", "uplinks", func(c *cm.MultiWANConfig) { c.Uplinks = nil }},
		{"default", "default_objective", func(c *cm.MultiWANConfig) { c.DefaultObjective = "absent" }},
		{"mark collision", "overlap", func(c *cm.MultiWANConfig) { c.MarkBase = "0x10000" }},
		{"mark invalid", "mark_base", func(c *cm.MultiWANConfig) { c.MarkBase = "xyz" }},
		{"tollgate cost", "locally", func(c *cm.MultiWANConfig) { c.Uplinks[0].Source = "tollgate" }},
		{"override key", "weight_override", func(c *cm.MultiWANConfig) { c.Objectives[0].WeightOverride = map[string]int{"z": 1} }},
		{"override negative", "weight_override", func(c *cm.MultiWANConfig) { c.Objectives[0].WeightOverride = map[string]int{"a": -1} }},
		{"override high", "weight_override", func(c *cm.MultiWANConfig) { c.Objectives[0].WeightOverride = map[string]int{"a": 101} }},
		{"engine", "engine", func(c *cm.MultiWANConfig) { c.Engine = "x" }},
		{"change", "allow_change", func(c *cm.MultiWANConfig) { c.AllowChange = "x" }},
		{"source", "source", func(c *cm.MultiWANConfig) { c.Uplinks[0].Source = "x" }},
		{"cost unit", "unit", func(c *cm.MultiWANConfig) { c.Uplinks[0].Cost.Unit = "x" }},
		{"cost negative", "cost", func(c *cm.MultiWANConfig) { c.Uplinks[0].Cost.Value = -1 }},
		{"weight max", "weight_max", func(c *cm.MultiWANConfig) { c.Eligibility.WeightMax = 0 }},
		{"loss", "max_loss_pct", func(c *cm.MultiWANConfig) { c.Eligibility.MaxLossPct = 101 }},
		{"samples", "min_samples", func(c *cm.MultiWANConfig) { c.Eligibility.MinSamples = 0 }},
		{"sampler", "sampler", func(c *cm.MultiWANConfig) { c.Sampler.WindowS = 0 }},
		{"health", "health_check", func(c *cm.MultiWANConfig) { c.HealthCheck.Down = 0 }},
		{"ip", "track_ips", func(c *cm.MultiWANConfig) { c.HealthCheck.TrackIPs = []string{"bad"} }},
		{"empty id", "id", func(c *cm.MultiWANConfig) { c.Uplinks[0].ID = "" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := fixture()
			tc.change(c)
			err := Validate(c, "0x10000")
			if err == nil || !strings.Contains(err.Error(), tc.part) {
				t.Fatalf("got %v want %s", err, tc.part)
			}
		})
	}
	c := fixture()
	c.Uplinks[0].Cost = nil
	if err := Validate(c, "0x10000"); err != nil {
		t.Fatal(err)
	}
	if err := Validate(nil, ""); err != nil {
		t.Fatal(err)
	}
	if err := Validate(fixture(), "bad-mark"); err == nil {
		t.Fatal("invalid fwmark")
	}
	raw, _ := json.Marshal(fixture())
	if _, err := Load(raw, "0x10000"); err != nil {
		t.Fatal(err)
	}
	if _, err := Load([]byte(`{"unknown":1}`), ""); err == nil {
		t.Fatal("unknown field")
	}
}
func observations() map[string]Measurement {
	return map[string]Measurement{"a": {Up: true, RecentSamples: 3, DownMbps: 100, RTTMs: 10}, "b": {Up: true, RecentSamples: 3, DownMbps: 200, RTTMs: 20}}
}
func TestDerive(t *testing.T) {
	for _, tc := range []struct {
		mode string
		a, b int
	}{{"cost", 100, 0}, {"speed", 0, 100}, {"balanced", 50, 50}} {
		t.Run(tc.mode, func(t *testing.T) {
			c := fixture()
			c.Objectives[0].Mode = tc.mode
			o, err := Derive(c, "o", observations(), "0x10000")
			if err != nil || o.Weights["a"] != tc.a || o.Weights["b"] != tc.b {
				t.Fatalf("%+v %v", o, err)
			}
			if tc.mode == "balanced" && (o.PerConnectionMbps != 150 || o.AggregateMbps != 300 || o.RTTMs != 15 || o.SatsPerGiB == nil || *o.SatsPerGiB != 15 || o.Survivors != 2) {
				t.Fatalf("metrics %+v", o)
			}
		})
	}
	for _, mode := range []string{"cost", "speed"} {
		c := fixture()
		c.Objectives[0].Mode = mode
		c.Uplinks[1].Cost.Value = 10
		m := observations()
		b := m["b"]
		b.DownMbps = 100
		m["b"] = b
		o, _ := Derive(c, "o", m, "0x10000")
		if o.Weights["a"] != 100 {
			t.Fatal("tie not broken by id")
		}
	}
	c := fixture()
	c.Eligibility.WeightMax = 1
	c.Uplinks = append(c.Uplinks, cm.MultiWANUplink{ID: "c", Source: "plain", Cost: &cm.MultiWANCost{Value: 10, Unit: "sats_gib"}})
	m := observations()
	m["c"] = m["a"]
	o, err := Derive(c, "o", m, "0x10000")
	if err != nil || o.Weights["a"] != 1 || o.Survivors != 1 {
		t.Fatalf("fallback %+v %v", o, err)
	}
}
func TestOverridesAndUnknownPrice(t *testing.T) {
	c := fixture()
	c.Objectives[0].Mode = "speed"
	c.Objectives[0].WeightOverride = map[string]int{"a": 25, "b": 0}
	c.Uplinks[0].Cost = nil
	o, err := Derive(c, "", observations(), "0x10000")
	if err != nil || o.Weights["a"] != 25 || o.Survivors != 1 || o.SatsPerGiB != nil {
		t.Fatalf("%+v %v", o, err)
	}
	m := observations()
	a := m["a"]
	a.Up = false
	m["a"] = a
	o, err = Derive(c, "", m, "0x10000")
	if err != nil || o.Survivors != 0 {
		t.Fatal("override bypasses health")
	}
	c = fixture()
	c.Uplinks[0].Cost.Value = 0
	o, err = Derive(c, "", observations(), "0x10000")
	if err != nil || o.Weights["a"] != 100 || o.Weights["b"] != 0 {
		t.Fatal("free-line handling")
	}
	if _, err := Derive(c, "missing", m, "0x10000"); err == nil {
		t.Fatal("unknown objective accepted")
	}
}

func TestEligibilityAndProvenance(t *testing.T) {
	c := fixture()
	m := observations()
	for _, bad := range []Measurement{{Up: false, RecentSamples: 3, DownMbps: 100}, {Up: true, RecentSamples: 1, DownMbps: 100}, {Up: true, RecentSamples: 3, DownMbps: 100, LossPct: 11}, {Up: true, RecentSamples: 3, DownMbps: math.NaN()}} {
		m["a"] = bad
		o, err := Derive(c, "o", m, "0x10000")
		if err != nil || o.Weights["a"] != 0 {
			t.Fatalf("ineligible %+v %v", o, err)
		}
	}
	c = fixture()
	c.Objectives[0].Mode = "cost"
	c.Uplinks[0].Cost = nil
	o, _ := Derive(c, "o", observations(), "0x10000")
	if o.Weights["a"] != 0 {
		t.Fatal("unknown cost selected")
	}
	c.Uplinks[0].Cost = &cm.MultiWANCost{Value: 1, Unit: "flat_month"}
	o, _ = Derive(c, "o", observations(), "0x10000")
	if o.Weights["a"] != 0 || len(o.FlatUplinks) != 1 {
		t.Fatal("flat selected or not reported")
	}
	c.Uplinks[0].Source = "tollgate"
	c.Uplinks[0].Cost = nil
	m = observations()
	a := m["a"]
	a.SessionCost = &cm.MultiWANCost{Value: 1, Unit: "sats_min"}
	m["a"] = a
	o, _ = Derive(c, "o", m, "0x10000")
	if o.Weights["a"] != 100 {
		t.Fatal("upstream price not used")
	}
	c.Enabled = false
	o, err := Derive(c, "o", m, "0x10000")
	if err != nil || o.Survivors != 0 {
		t.Fatal("disabled policy active")
	}
}
