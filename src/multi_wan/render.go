package multi_wan

import (
	"fmt"
	"sort"
	"strings"

	cm "github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
)

// renderObjective binds each outcome to the objective at the same index in
// cfg.Objectives BEFORE sorting. Outcome deliberately has no objective ID.
type renderObjective struct {
	id      string
	outcome Outcome
	mark    uint64
}

func renderInputs(cfg *cm.MultiWANConfig, outcomes []Outcome) ([]cm.MultiWANUplink, []renderObjective, error) {
	// The section has no MPTCP fwmark. The enclosing caller must first call
	// Validate with its effective MPTCP mark; do not invent one here.
	if err := Validate(cfg, ""); err != nil {
		return nil, nil, err
	}
	if cfg == nil || !cfg.Enabled {
		return nil, nil, fmt.Errorf("multi_wan: rendering requires enabled configuration")
	}
	if cfg.Engine != "mwan3" {
		return nil, nil, fmt.Errorf("multi_wan: renderer requires mwan3 engine")
	}
	if len(outcomes) == 0 || len(outcomes) != len(cfg.Objectives) {
		return nil, nil, fmt.Errorf("multi_wan: need one outcome per configured objective")
	}
	uplinks := append([]cm.MultiWANUplink(nil), cfg.Uplinks...)
	sort.Slice(uplinks, func(i, j int) bool { return uplinks[i].ID < uplinks[j].ID })
	ids := map[string]bool{}
	for _, u := range uplinks {
		if !uciID(u.ID) {
			return nil, nil, fmt.Errorf("unsafe UCI uplink ID %q", u.ID)
		}
		ids[u.ID] = true
	}
	objectives := make([]renderObjective, len(outcomes))
	for i, o := range cfg.Objectives {
		if !uciID(o.ID) {
			return nil, nil, fmt.Errorf("unsafe UCI objective ID %q", o.ID)
		}
		for id, w := range outcomes[i].Weights {
			if !ids[id] || w < 0 || w > cfg.Eligibility.WeightMax {
				return nil, nil, fmt.Errorf("invalid weight for %q", id)
			}
		}
		for id := range ids {
			if _, ok := outcomes[i].Weights[id]; !ok {
				return nil, nil, fmt.Errorf("missing weight for %q", id)
			}
		}
		objectives[i] = renderObjective{id: o.ID, outcome: outcomes[i]}
	}
	sort.Slice(objectives, func(i, j int) bool { return objectives[i].id < objectives[j].id })
	base, _ := hex(cfg.MarkBase)
	for i := range objectives {
		mark := base + uint64(i)
		if mark > 0xffffffff || mark&0x3f00 != 0 {
			return nil, nil, fmt.Errorf("multi_wan: objective mark %#x overlaps mwan3 or overflows", mark)
		}
		objectives[i].mark = mark
	}
	return uplinks, objectives, nil
}

func uciID(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_') {
			return false
		}
	}
	return true
}

// RenderMWAN3 returns UCI text with commented rule templates only: stock
// mwan3 does not support a UCI mark selector. Do not activate these templates.
// Uplink IDs must be existing logical
// network interface names, never guessed device names. Outcomes correspond to
// cfg.Objectives in input order. Metrics differ BETWEEN objectives; members
// within one objective share a metric so mwan3 actually uses their weights.
func RenderMWAN3(cfg *cm.MultiWANConfig, objectives []Outcome) (string, error) {
	uplinks, objs, err := renderInputs(cfg, objectives)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("# Generated multi-WAN policy; review before applying.\n")
	for _, u := range uplinks {
		fmt.Fprintf(&b, "\nconfig interface '%s'\n\toption enabled '1'\n\toption family 'ipv4'\n\toption track_method 'ping'\n", u.ID)
		for _, ip := range cfg.HealthCheck.TrackIPs {
			fmt.Fprintf(&b, "\tlist track_ip '%s'\n", ip)
		}
		fmt.Fprintf(&b, "\toption up '%d'\n\toption down '%d'\n\toption interval '%d'\n", cfg.HealthCheck.Up, cfg.HealthCheck.Down, cfg.HealthCheck.IntervalS)
	}
	for i, o := range objs {
		for j, u := range uplinks {
			fmt.Fprintf(&b, "\nconfig member 'mw_%d_%d'\n\toption interface '%s'\n\toption metric '%d'\n\toption weight '%d'\n", i, j, u.ID, i+1, o.outcome.Weights[u.ID])
		}
		fmt.Fprintf(&b, "\nconfig policy 'mw_policy_%d'\n\toption last_resort 'unreachable'\n", i)
		for j, u := range uplinks {
			if o.outcome.Weights[u.ID] > 0 {
				fmt.Fprintf(&b, "\tlist use_member 'mw_%d_%d'\n", i, j)
			}
		}
		fmt.Fprintf(&b, "\n# BLOCKED: stock mwan3 has no UCI mark selector; template only.\n# config rule 'mw_rule_%d'\n# \toption family 'ipv4'\n# \toption proto 'all'\n# \toption mark '0x%x/0xffffc0ff'\n# \toption use_policy 'mw_policy_%d'\n", i, o.mark, i)
	}
	return b.String(), nil
}

// RenderMarks emits a table-body nftables fragment. The immutable #1124 config
// has no client assignments: empty IPv4 sets are explicit integration points
// for a later caller, not fictional clients or a blanket default assignment.
// Populate each set with client addresses before use. No interface is assumed.
func RenderMarks(cfg *cm.MultiWANConfig, objectives []Outcome) (string, error) {
	_, objs, err := renderInputs(cfg, objectives)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("# Multi-WAN client marks; insert inside an IPv4/inet table.\n# Client sets are intentionally empty: populate before use.\n")
	for i, o := range objs {
		fmt.Fprintf(&b, "# objective %s: mark_base + %d = 0x%x\n", o.id, i, o.mark)
	}
	for i := range objs {
		fmt.Fprintf(&b, "set mw_clients_%d { type ipv4_addr; }\n", i)
	}
	b.WriteString("chain mw_client_marks {\n\ttype filter hook prerouting priority mangle - 1; policy accept;\n")
	var owned uint64
	for _, o := range objs {
		owned |= o.mark
	}
	for i, o := range objs {
		fmt.Fprintf(&b, "	ip saddr @mw_clients_%d meta mark set (meta mark & 0x%08x) | 0x%x\n", i, uint64(0xffffffff)^owned, o.mark)
	}
	b.WriteString("}\n")
	return b.String(), nil
}
