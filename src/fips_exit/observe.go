package fips_exit

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Probe is a read-only argv request. The injected runner must honor Context
// cancellation and stop capture at MaxOutputBytes (returning an error).
// It must resolve ip from a trusted path in the intended network namespace,
// execute directly without a shell, and never return truncated output as success.
type Probe struct {
	Program        string
	Args           []string
	MaxOutputBytes int
}
type ProbeRunner func(context.Context, Probe) ([]byte, error)

// LinkRouteEvidence is deliberately NOT Environment or GuardReadiness.
// It cannot authorize activation or attest daemon identity/discovery or guards.
type LinkRouteEvidence struct {
	Config                    Config
	Interfaces                map[string]int
	LocalAddress, ExitAddress string
}

// CollectLinkRoutes collects a bounded snapshot, not an atomic readiness gate.
func CollectLinkRoutes(ctx context.Context, c Config, run ProbeRunner, timeout time.Duration) (LinkRouteEvidence, error) {
	var zero LinkRouteEvidence
	if !c.Enabled {
		return zero, nil
	}
	if err := c.Validate(); err != nil {
		return zero, err
	}
	if run == nil || timeout <= 0 || timeout > 30*time.Second {
		return zero, fmt.Errorf("runner and timeout in (0,30s] required")
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	local, inner := Derive(c.LocalPublicKey)
	exit, _ := Derive(c.ExitPublicKey)
	args := [][]string{{"-j", "link", "show"}, {"-j", "address", "show"}, {"-j", "-4", "rule", "show"}, {"-j", "-4", "route", "show", "table", "all"}, {"-j", "-6", "route", "get", exit.String(), "from", local.String()}}
	data := make([][]observation, len(args))
	for i, a := range args {
		if err := ctx.Err(); err != nil {
			return zero, err
		}
		b, err := run(ctx, Probe{"ip", a, 1 << 20})
		if err != nil {
			return zero, fmt.Errorf("probe %d: %w", i, err)
		}
		if err = ctx.Err(); err != nil {
			return zero, err
		}
		if len(b) > 1<<20 {
			return zero, fmt.Errorf("probe %d exceeds output limit", i)
		}
		if err = json.Unmarshal(b, &data[i]); err != nil || data[i] == nil {
			return zero, fmt.Errorf("probe %d: invalid JSON array", i)
		}
	}
	for i, rows := range data {
		schema := []string{linkSchema, linkSchema + " addr_info:a", "priority:n src:s table:t protocol:t", routeSchema, routeSchema}[i]
		for _, row := range rows {
			if !validObservation(row, schema) {
				return zero, fmt.Errorf("probe %d: unknown/malformed fields", i)
			}
		}
	}
	e := LinkRouteEvidence{Config: c, Interfaces: map[string]int{}, LocalAddress: local.String(), ExitAddress: exit.String()}
	indexes := map[int]string{}
	for _, o := range data[0] {
		name := o.text("ifname")
		mtu := o.number("mtu")
		idx := o.number("ifindex")
		if name == "" || mtu <= 0 || idx <= 0 || e.Interfaces[name] != 0 || indexes[idx] != "" {
			return zero, fmt.Errorf("incomplete/duplicate link")
		}
		e.Interfaces[name] = mtu
		indexes[idx] = name
		if name == c.GuestInterface || name == c.FIPSInterface {
			var flags []string
			if json.Unmarshal(o["flags"], &flags) != nil || !slices.Contains(flags, "UP") {
				return zero, fmt.Errorf("interface %s not administratively up", name)
			}
		}
	}
	if e.Interfaces[c.GuestInterface] == 0 || e.Interfaces[c.FIPSInterface] < c.MTU+40 || e.Interfaces[c.TunnelInterface] != 0 {
		return zero, fmt.Errorf("missing interface, insufficient MTU or occupied tunnel")
	}
	seen := map[string]bool{}
	found := false
	for _, o := range data[1] {
		name := o.text("ifname")
		if seen[name] || e.Interfaces[name] == 0 || indexes[o.number("ifindex")] != name {
			return zero, fmt.Errorf("inconsistent address inventory")
		}
		seen[name] = true
		var addresses []observation
		if json.Unmarshal(o["addr_info"], &addresses) != nil || addresses == nil {
			return zero, fmt.Errorf("missing address inventory")
		}
		for _, a := range addresses {
			ip, err := netip.ParseAddr(a.text("local"))
			family := a.text("family")
			prefix := a.number("prefixlen")
			if err != nil || (family != "inet" && family != "inet6") || (family == "inet") != ip.Is4() || prefix < 0 || prefix > ip.BitLen() {
				return zero, fmt.Errorf("malformed address")
			}
			if ip == inner || ip == exit {
				return zero, fmt.Errorf("derived address occupied")
			}
			if ip == local {
				if name != c.FIPSInterface || a.text("scope") != "global" {
					return zero, fmt.Errorf("local FIPS address on wrong interface/scope")
				}
				for _, k := range []string{"tentative", "dadfailed", "deprecated"} {
					if v, ok := a[k]; ok && string(v) != "false" {
						return zero, fmt.Errorf("unusable local address: %s", k)
					}
				}
				if v, ok := a["preferred_life_time"]; ok && string(v) == "0" {
					return zero, fmt.Errorf("expired local address")
				}
				found = true
			}
		}
	}
	if len(seen) != len(e.Interfaces) || !found {
		return zero, fmt.Errorf("incomplete inventory or missing derived local address")
	}
	// Deliberately conservative: only the standard IPv4 RPDB is accepted. This
	// does not attest IPv6 policy ordering or future guest/mark-specific lookups.
	if len(data[2]) != 3 {
		return zero, fmt.Errorf("nonstandard IPv4 rules require review")
	}
	priorities := map[int]bool{}
	for _, r := range data[2] {
		priority := r.number("priority")
		table, ok := tableID(r["table"], false)
		allowed := map[int]uint32{0: 255, 32766: 254, 32767: 253}
		if !ok || priorities[priority] || allowed[priority] != table || table == 0 || r.text("src") != "all" || priority == int(c.RulePriority) || table == c.RoutingTable {
			return zero, fmt.Errorf("conflicting/unknown IPv4 rule")
		}
		for k := range r {
			if k != "priority" && k != "src" && k != "table" && k != "protocol" {
				return zero, fmt.Errorf("unsupported rule attribute %s", k)
			}
		}
		priorities[priority] = true
	}
	for _, r := range data[3] {
		table, ok := tableID(r["table"], true)
		if !ok || table == c.RoutingTable || r.text("dst") == "" {
			return zero, fmt.Errorf("conflicting/unknown IPv4 table route")
		}
		dst := r.text("dst")
		if dst != "default" {
			prefix, err := netip.ParsePrefix(dst)
			if err != nil {
				ip, e := netip.ParseAddr(dst)
				if e != nil || !ip.Is4() {
					return zero, fmt.Errorf("malformed IPv4 route")
				}
				prefix = netip.PrefixFrom(ip, 32)
			}
			if !prefix.Addr().Is4() || prefix.Contains(inner) {
				return zero, fmt.Errorf("derived inner route conflict")
			}
		}
	}
	if len(data[4]) != 1 {
		return zero, fmt.Errorf("ambiguous/missing exit route")
	}
	r := data[4][0]
	if r.text("dst") != exit.String() || r.text("from") != local.String() || r.text("dev") != c.FIPSInterface {
		return zero, fmt.Errorf("wrong source-specific exit route")
	}
	if typ := r.text("type"); typ != "" && typ != "unicast" {
		return zero, fmt.Errorf("non-unicast exit route")
	}
	for _, k := range []string{"error", "gateway", "via", "multipath", "nhid", "encap", "expires"} {
		if _, ok := r[k]; ok {
			return zero, fmt.Errorf("unsupported exit route attribute %s", k)
		}
	}
	if src := r.text("src"); src != "" && src != local.String() {
		return zero, fmt.Errorf("wrong exit source")
	}
	var flags []string
	if json.Unmarshal(r["flags"], &flags) != nil || len(flags) != 0 {
		return zero, fmt.Errorf("unknown exit route flags")
	}
	return e, nil
}

// Unknown table aliases cannot safely be compared with a numeric dedicated ID.
func tableID(raw json.RawMessage, defaultMain bool) (uint32, bool) {
	if raw == nil {
		return 254, defaultMain
	}
	var n uint32
	if json.Unmarshal(raw, &n) == nil {
		return n, n != 0
	}
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return 0, false
	}
	switch s {
	case "local":
		return 255, true
	case "main":
		return 254, true
	case "default":
		return 253, true
	}
	v, err := strconv.ParseUint(s, 10, 32)
	return uint32(v), err == nil && v != 0
}

type observation map[string]json.RawMessage

func (o observation) text(k string) string { var s string; _ = json.Unmarshal(o[k], &s); return s }
func (o observation) number(k string) int {
	var n int
	if json.Unmarshal(o[k], &n) != nil {
		return -1
	}
	return n
}

const linkSchema = "ifindex:n ifname:s flags:l mtu:n qdisc:s operstate:s group:s txqlen:n link_type:s address:s broadcast:s link_index:n link_netnsid:n master:s altnames:l"
const routeSchema = "dst:s from:s dev:s table:t protocol:t scope:s prefsrc:s src:s metric:n flags:l pref:s type:s gateway:s uid:n cache:l"
const addressSchema = "family:s local:s prefixlen:n scope:s label:s broadcast:s valid_life_time:t preferred_life_time:t tentative:b dadfailed:b deprecated:b noprefixroute:b mngtmpaddr:b temporary:b dynamic:b"

// Explicit supported iproute2 schema: new fields require review, not optimistic
// interpretation. Harmless variation may refuse; this collector never activates.
func validObservation(o observation, schema string) bool {
	if len(o) == 0 {
		return false
	}
	fields := map[string]string{}
	for _, pair := range strings.Fields(schema) {
		p := strings.Split(pair, ":")
		fields[p[0]] = p[1]
	}
	for k, v := range o {
		if string(v) == "null" {
			return false
		}
		var err error
		switch fields[k] {
		case "s":
			var x string
			err = json.Unmarshal(v, &x)
		case "n":
			var x uint64
			err = json.Unmarshal(v, &x)
		case "b":
			var x bool
			err = json.Unmarshal(v, &x)
		case "l":
			var x []string
			err = json.Unmarshal(v, &x)
		case "t":
			var n uint32
			err = json.Unmarshal(v, &n)
			if err != nil {
				var s string
				err = json.Unmarshal(v, &s)
			}
		case "a":
			var x []observation
			err = json.Unmarshal(v, &x)
			for _, a := range x {
				if !validObservation(a, addressSchema) {
					return false
				}
			}
		default:
			return false
		}
		if err != nil {
			return false
		}
	}
	return true
}
