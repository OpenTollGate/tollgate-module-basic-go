// Package multi_wan validates policy intent and derives weights without I/O.
// Callers supply measurements already restricted to sampler.window_s and the
// upstream session price; this package never probes links or changes routing.
package multi_wan

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/netip"
	"sort"
	"strconv"
	"strings"

	cm "github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
)

var ErrNotComparable = errors.New("flat monthly cost is not comparable per GiB")

// Load decodes a multi_wan section, not a complete router configuration.
func Load(data []byte, fwmark string) (*cm.MultiWANConfig, error) {
	var c *cm.MultiWANConfig
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(&c); err != nil {
		return nil, err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("multi_wan: trailing JSON")
	}
	if err := Validate(c, fwmark); err != nil {
		return nil, err
	}
	return c, nil
}
func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }
func hex(s string) (uint64, error) {
	return strconv.ParseUint(strings.TrimPrefix(strings.TrimPrefix(s, "0x"), "0X"), 16, 32)
}

// Validate checks even disabled, explicitly supplied sections. An absent section
// is valid. fwmark is the effective mptcp_bonding.fwmark; empty means unreserved.
// mwan3 reserves the six member bits 0x3f00. All member encodings of mark_base
// must be disjoint from the MPTCP mark, not just the literal base value.
func Validate(c *cm.MultiWANConfig, fwmark string) error {
	if c == nil {
		return nil
	}
	if c.Engine != "mwan3" && c.Engine != "mptcp_bond" {
		return fmt.Errorf("multi_wan.engine: unknown %q", c.Engine)
	}
	if c.AllowChange != "next_session" && c.AllowChange != "never" {
		return fmt.Errorf("multi_wan.allow_change: unknown %q", c.AllowChange)
	}
	base, err := hex(c.MarkBase)
	if err != nil {
		return fmt.Errorf("multi_wan.mark_base: %w", err)
	}
	const mask uint64 = 0x3f00
	// mwan3 only ever tests and sets bits INSIDE its member mask: it matches
	// `-m mark --mark 0x0/$MMX_MASK` and writes `--set-xmark v/$MMX_MASK`
	// (net/mwan3 files/lib/mwan3/mwan3.sh). So a multi_wan base must leave
	// those bits clear, or mwan3 will overwrite the member index on it.
	if base == 0 {
		return fmt.Errorf("multi_wan.mark_base: must be non-zero")
	}
	if base&mask != 0 {
		return fmt.Errorf("multi_wan.mark_base: must not use the mwan3 member bits (mask 0x3f00)")
	}
	if fwmark != "" {
		mark, err := hex(fwmark)
		if err != nil {
			return fmt.Errorf("mptcp_bonding.fwmark: %w", err)
		}
		if base&mark != 0 {
			return fmt.Errorf("multi_wan.mark_base overlaps mptcp_bonding.fwmark (both use the same bits)")
		}
	}
	if c.HealthCheck.IntervalS < 1 || c.HealthCheck.Down < 1 || c.HealthCheck.Up < 1 {
		return fmt.Errorf("multi_wan.health_check: interval_s, down and up must be positive")
	}
	for _, ip := range c.HealthCheck.TrackIPs {
		if _, err := netip.ParseAddr(ip); err != nil {
			return fmt.Errorf("multi_wan.health_check.track_ips: invalid IP %q", ip)
		}
	}
	if c.Sampler.WindowS < 1 || c.Sampler.MinSamples < 1 {
		return fmt.Errorf("multi_wan.sampler: window_s and min_samples must be positive")
	}
	e := c.Eligibility
	if e.MinSamples < 1 {
		return fmt.Errorf("multi_wan.eligibility.min_samples must be positive")
	}
	if !finite(e.MaxLossPct) || e.MaxLossPct < 0 || e.MaxLossPct > 100 {
		return fmt.Errorf("multi_wan.eligibility.max_loss_pct must be in 0..100")
	}
	if e.WeightMax < 1 || e.WeightMax > 256 {
		return fmt.Errorf("multi_wan.eligibility.weight_max must be in 1..256")
	}
	if len(c.Uplinks) == 0 {
		return fmt.Errorf("multi_wan.uplinks must not be empty")
	}
	ids := map[string]bool{}
	for _, u := range c.Uplinks {
		if strings.TrimSpace(u.ID) == "" {
			return fmt.Errorf("multi_wan.uplinks: empty id")
		}
		if ids[u.ID] {
			return fmt.Errorf("duplicate uplink id %q", u.ID)
		}
		ids[u.ID] = true
		if u.Source != "tollgate" && u.Source != "plain" {
			return fmt.Errorf("uplink %s: unknown source", u.ID)
		}
		if u.Source == "tollgate" && u.Cost != nil {
			return fmt.Errorf("uplink %s: tollgate price cannot be locally set", u.ID)
		}
		if u.Cost != nil {
			if _, err := Normalize(*u.Cost, 1); err != nil && !errors.Is(err, ErrNotComparable) {
				return fmt.Errorf("uplink %s cost: %w", u.ID, err)
			}
		}
	}
	objectives := map[string]bool{}
	for _, o := range c.Objectives {
		if strings.TrimSpace(o.ID) == "" {
			return fmt.Errorf("objective: empty id")
		}
		if objectives[o.ID] {
			return fmt.Errorf("duplicate objective id %q", o.ID)
		}
		objectives[o.ID] = true
		if o.Mode != "cost" && o.Mode != "speed" && o.Mode != "balanced" {
			return fmt.Errorf("objective %s: unknown mode %q", o.ID, o.Mode)
		}
		for id, v := range o.WeightOverride {
			if !ids[id] || v < 0 || v > e.WeightMax {
				return fmt.Errorf("objective %s weight_override[%s]: unknown uplink or value outside 0..weight_max", o.ID, id)
			}
		}
	}
	if !objectives[c.DefaultObjective] {
		return fmt.Errorf("multi_wan.default_objective not present in objectives")
	}
	return nil
}

// Normalize returns sats/GiB using the measured Mbit/s rate for time pricing.
func Normalize(c cm.MultiWANCost, downMbps float64) (float64, error) {
	if !finite(c.Value) || c.Value < 0 {
		return 0, fmt.Errorf("cost value must be finite and non-negative")
	}
	var v float64
	switch c.Unit {
	case "sats_gib":
		v = c.Value
	case "sats_mib":
		v = c.Value * 1024
	case "sats_min":
		if !finite(downMbps) || downMbps <= 0 {
			return 0, fmt.Errorf("sats_min requires a positive measured rate")
		}
		v = c.Value * (8192 / (60 * downMbps))
	case "flat_month":
		return 0, ErrNotComparable
	default:
		return 0, fmt.Errorf("unknown cost unit %q", c.Unit)
	}
	if !finite(v) {
		return 0, fmt.Errorf("normalized cost overflow")
	}
	return v, nil
}

// Measurement is a caller-provided window aggregate. SessionCost is only read
// for tollgate sources; a plain uplink cannot borrow a session's price.
type Measurement struct {
	Up                       bool
	RecentSamples            int
	LossPct, DownMbps, RTTMs float64
	SessionCost              *cm.MultiWANCost
}

func Eligible(m Measurement, e cm.MultiWANEligibility) bool {
	return m.Up && m.RecentSamples >= e.MinSamples && finite(m.LossPct) && m.LossPct >= 0 && m.LossPct <= e.MaxLossPct && finite(m.DownMbps) && m.DownMbps > 0 && finite(m.RTTMs) && m.RTTMs >= 0
}

type Outcome struct {
	Weights           map[string]int `json:"weights"`
	PerConnectionMbps float64        `json:"per_connection_mbps"`
	AggregateMbps     float64        `json:"aggregate_mbps"`
	RTTMs             float64        `json:"rtt_ms"`
	// Nil when any selected member has unknown or flat cost: never imply free.
	SatsPerGiB  *float64 `json:"sats_per_gib"`
	Survivors   int      `json:"survivors"`
	FlatUplinks []string `json:"flat_uplinks,omitempty"`
}
type member struct {
	id    string
	m     Measurement
	price *float64
	score float64
}

// Derive returns zero weights when disabled. Balanced allocates a weight_max
// budget proportional to rate/price, rounding halves up; all-zero rounding
// selects the best member at weight 1. Unknown/flat costs cannot be scored for
// balanced (but remain selectable by speed). Free lines dominate paid ones,
// sharing by rate. Overrides replace individual derived weights, never bypass
// eligibility, and explicit all-zero overrides intentionally disable all members.
func Derive(c *cm.MultiWANConfig, objective string, measurements map[string]Measurement, fwmark string) (Outcome, error) {
	out := Outcome{Weights: map[string]int{}}
	if err := Validate(c, fwmark); err != nil {
		return out, err
	}
	if c == nil {
		return out, nil
	}
	var obj *cm.MultiWANObjective
	if objective == "" {
		objective = c.DefaultObjective
	}
	for i := range c.Objectives {
		if c.Objectives[i].ID == objective {
			obj = &c.Objectives[i]
			break
		}
	}
	if obj == nil {
		return out, fmt.Errorf("unknown objective %q", objective)
	}
	for _, u := range c.Uplinks {
		out.Weights[u.ID] = 0
	}
	if !c.Enabled {
		return out, nil
	}
	e := c.Eligibility
	if c.Sampler.MinSamples > e.MinSamples {
		e.MinSamples = c.Sampler.MinSamples
	}
	var members []member
	for _, u := range c.Uplinks {
		m := measurements[u.ID]
		if !Eligible(m, e) {
			continue
		}
		price := u.Cost
		if u.Source == "tollgate" {
			price = m.SessionCost
		}
		x := member{id: u.ID, m: m}
		if price != nil {
			p, err := Normalize(*price, m.DownMbps)
			if errors.Is(err, ErrNotComparable) {
				out.FlatUplinks = append(out.FlatUplinks, u.ID)
			} else if err != nil {
				return out, fmt.Errorf("uplink %s: %w", u.ID, err)
			} else {
				x.price = &p
			}
		}
		if obj.Mode != "speed" && x.price == nil {
			continue
		}
		members = append(members, x)
	}
	sort.Strings(out.FlatUplinks)
	sort.Slice(members, func(i, j int) bool { return members[i].id < members[j].id })
	if len(members) == 0 {
		return out, nil
	}
	const priceFloor = 1.0 // sats/GiB: keeps the rate/price ratio continuous at price 0
	best := 0
	if obj.Mode == "balanced" {
		// Log scores avoid overflow/underflow in rate/price and their sum. A zero
		// price is floored, not treated as exclusive: a free line dominates in
		// practice, but a much faster paid line still earns a share and one flaky
		// free line cannot zero out every alternative.
		maxLog := math.Inf(-1)
		for i := range members {
			x := &members[i]
			x.score = math.Log(x.m.DownMbps) - math.Log(math.Max(*x.price, priceFloor))
			if x.score > maxLog {
				maxLog = x.score
				best = i
			}
		}
		sum := 0.0
		for i := range members {
			members[i].score = math.Exp(members[i].score - maxLog)
			sum += members[i].score
		}
		total := 0
		for _, x := range members {
			v := int(math.Round(float64(e.WeightMax) * x.score / sum))
			out.Weights[x.id] = v
			total += v
		}
		if total == 0 {
			out.Weights[members[best].id] = 1
		}
	} else {
		for i := 1; i < len(members); i++ {
			if (obj.Mode == "speed" && members[i].m.DownMbps > members[best].m.DownMbps) || (obj.Mode == "cost" && *members[i].price < *members[best].price) {
				best = i
			}
		}
		out.Weights[members[best].id] = e.WeightMax
	}
	for _, x := range members {
		if v, ok := obj.WeightOverride[x.id]; ok {
			out.Weights[x.id] = v
		}
	}
	total := 0
	for _, x := range members {
		total += out.Weights[x.id]
	}
	if total == 0 {
		return out, nil
	}
	known := true
	cost := 0.0
	for _, x := range members {
		v := out.Weights[x.id]
		if v == 0 {
			continue
		}
		fraction := float64(v) / float64(total)
		out.Survivors++
		out.AggregateMbps += x.m.DownMbps
		out.PerConnectionMbps += fraction * x.m.DownMbps
		out.RTTMs += fraction * x.m.RTTMs
		if x.price == nil {
			known = false
		} else {
			cost += fraction * *x.price
		}
	}
	if known {
		out.SatsPerGiB = &cost
	}
	return out, nil
}
