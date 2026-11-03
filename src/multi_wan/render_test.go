package multi_wan

import (
	cm "github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"strings"
	"testing"
)

func TestRenderGolden(t *testing.T) {
	c := fixture()
	c.Uplinks = c.Uplinks[:1]
	c.HealthCheck.TrackIPs = []string{"1.1.1.1"}
	out := []Outcome{{Weights: map[string]int{"a": 73}}}
	want := `# Generated multi-WAN policy; review before applying.

config interface 'a'
	option enabled '1'
	option family 'ipv4'
	option track_method 'ping'
	list track_ip '1.1.1.1'
	option up '2'
	option down '3'
	option interval '5'

config member 'mw_0_0'
	option interface 'a'
	option metric '1'
	option weight '73'

config policy 'mw_policy_0'
	option last_resort 'unreachable'
	list use_member 'mw_0_0'

config rule 'mw_default'
	option family 'ipv4'
	option proto 'all'
	option src_ip '0.0.0.0/0'
	option use_policy 'mw_policy_0'
`
	got, err := RenderMWAN3(c, out, nil)
	if err != nil || got != want {
		t.Fatalf("UCI: %v\ngot:\n%s\nwant:\n%s", err, got, want)
	}
	want = `# Multi-WAN client marks; insert inside an IPv4/inet table.
# Client sets are intentionally empty: populate before use.
# objective o: mark_base + 0 = 0x4000
set mw_clients_0 { type ipv4_addr; }
chain mw_client_marks {
	type filter hook prerouting priority mangle - 1; policy accept;
	ip saddr @mw_clients_0 meta mark set (meta mark & 0xffffbfff) | 0x4000
}
`
	got, err = RenderMarks(c, out)
	if err != nil || got != want {
		t.Fatalf("nft: %v\ngot:\n%s\nwant:\n%s", err, got, want)
	}
}

func TestRenderOrderingAndWeights(t *testing.T) {
	c := fixture()
	c.Objectives = append(c.Objectives, cm.MultiWANObjective{ID: "a", Mode: "speed"})
	c.Uplinks[0], c.Uplinks[1] = c.Uplinks[1], c.Uplinks[0]
	out := []Outcome{{Weights: map[string]int{"a": 20, "b": 80}}, {Weights: map[string]int{"a": 0, "b": 100}}}
	got, err := RenderMWAN3(c, out, []ClientRoute{{IP: "10.0.0.7", Objective: "a"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"config member 'mw_0_0'\n\toption interface 'a'\n\toption metric '1'\n\toption weight '0'", "config member 'mw_1_0'\n\toption interface 'a'\n\toption metric '2'\n\toption weight '20'", "config rule 'mw_client_0'\n\toption family 'ipv4'\n\toption proto 'all'\n\toption src_ip '10.0.0.7'\n\toption use_policy 'mw_policy_0'"} {
		if !strings.Contains(got, s) {
			t.Fatalf("missing %s in %s", s, got)
		}
	}
	if strings.Contains(got, "list use_member 'mw_0_0'") {
		t.Fatal("zero member selected")
	}
	before := got
	c.Objectives[0], c.Objectives[1] = c.Objectives[1], c.Objectives[0]
	out[0], out[1] = out[1], out[0]
	got, err = RenderMWAN3(c, out, []ClientRoute{{IP: "10.0.0.7", Objective: "a"}})
	if err != nil || got != before {
		t.Fatal("ordering changed rendering", err)
	}
	marks, err := RenderMarks(c, out)
	if err != nil || !strings.Contains(marks, "# objective a: mark_base + 0 = 0x4000\n# objective o: mark_base + 1 = 0x4001") {
		t.Fatal(marks, err)
	}
}

func TestRenderRefusesMutations(t *testing.T) {
	for _, render := range []func(*cm.MultiWANConfig, []Outcome) (string, error){
		func(c *cm.MultiWANConfig, o []Outcome) (string, error) { return RenderMWAN3(c, o, nil) }, RenderMarks} {
		for _, mutate := range []func(*cm.MultiWANConfig){
			func(c *cm.MultiWANConfig) { c.MarkBase = "0x3f00" },
			func(c *cm.MultiWANConfig) { c.Enabled = false },
			func(c *cm.MultiWANConfig) { c.Objectives = nil },
			func(c *cm.MultiWANConfig) { c.Engine = "mptcp_bond" },
			func(c *cm.MultiWANConfig) { c.Uplinks[0].ID = "a'\nconfig evil" },
		} {
			c := fixture()
			out := []Outcome{{Weights: map[string]int{"a": 50, "b": 50}}}
			if _, err := render(c, out); err != nil {
				t.Fatal("baseline rejected", err)
			}
			mutate(c)
			if got, err := render(c, out); err == nil || got != "" {
				t.Fatalf("mutation accepted: %s %v", got, err)
			}
		}
		c := fixture()
		for _, out := range [][]Outcome{nil, {{Weights: map[string]int{"a": 1}}}, {{Weights: map[string]int{"a": 101, "b": 0}}}, {{Weights: map[string]int{"a": 1, "b": 2, "unknown": 3}}}} {
			if _, err := render(c, out); err == nil {
				t.Fatal("invalid outcomes accepted")
			}
		}
		if _, err := render(nil, nil); err == nil {
			t.Fatal("nil config")
		}
		c.Objectives = append(c.Objectives, cm.MultiWANObjective{ID: "z", Mode: "speed"})
		out := []Outcome{{Weights: map[string]int{"a": 1, "b": 2}}, {Weights: map[string]int{"a": 1, "b": 2}}}
		for _, base := range []string{"0xff", "0xffffc0ff"} {
			c.MarkBase = base
			if _, err := render(c, out); err == nil {
				t.Fatal("offset collision accepted")
			}
		}
	}
}
