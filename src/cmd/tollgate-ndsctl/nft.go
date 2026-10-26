// nft.go — LINUX-HOST-6 SHIM-NFT: the nftables backend of the host-mode
// `ndsctl` shim.
//
// The shim exposes exactly three verbs (LINUX-HOST-5 SHIM-CLI):
//
//	ndsctl auth   <mac>
//	ndsctl deauth <mac>
//	ndsctl json   <mac>
//
// This file implements the kernel-state half of those verbs. Nothing here
// renders a ruleset: LINUX-HOST-2 NETUP-RENDER owns the static ruleset, and
// LINUX-HOST-4 INTEGRATE mirrors `authed_v4` (empty) into every firewall table
// it tags. The shim's job is to *drive* the live set and the metering counters:
//
//   - auth   adds the client's IPv4 address to every `authed_v4` set that
//     exists, treating an "already there" nft error as success, and
//     installs one up + one down per-client counter rule.
//   - deauth removes the address element, removes the two per-client counter
//     rules AND their named counter objects (a removed named counter
//     reads back as zero, which is what makes the module's in-memory
//     ClearDataBaseline assumption hold), and is safe to run on a
//     client that is not authorised.
//   - json   reads `nft -j list counters` and reports KiB, dividing by 1024
//     with TRUNCATION (never rounding up — a byte count is an
//     operator-facing lower bound, and rounding up would over-credit a
//     still-metreable session).
//
// Concurrency. The shim is invoked as a *separate process* by the module
// (src/valve invokes the binary), so the module's in-process mutex does not
// serialise two shim runs. Every read-modify-write of kernel state therefore
// runs under an exclusive flock(2) on /run/tollgate/ndsctl.lock. Without it,
// two concurrent `auth` runs both observe "the counter rule is absent" and
// both insert it, leaving duplicate metering rules — the concurrency leg of
// the card's evidence fails exactly when this lock is stripped.
//
// Scope note / honest limits. This module (like its sibling) does not render
// the ruleset and does not know the real valve chain name beyond the default
// below; the table, chain and guest interface are configuration. The physical
// 4 MiB-transfer leg of the evidence runs in a network namespace (see
// netns_test.go) and is the only part that needs root + a live nft; the rest
// is a pure unit tier.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"sort"
	"strings"
)

const (
	// LockPath is the inter-process lock every mutating verb takes.
	LockPath = "/run/tollgate/ndsctl.lock"

	// AuthedSet is the authorization set the valve (LINUX-HOST-2) declares and
	// the integrator (LINUX-HOST-4) mirrors. auth adds to every instance of
	// this name that exists; deauth removes from the same.
	AuthedSet = "authed_v4"

	// DefaultTable is the family+name of the valve's own table. The named
	// metering counters and their rules live here.
	DefaultTableFamily = "inet"
	DefaultTableName   = "tollgate"

	// DefaultChain is the valve's forward chain (LINUX-HOST-2 NETUP-RENDER).
	DefaultChain = "forward_gate"

	// DefaultAP is $AP: the guest-facing device the per-client rules match on.
	DefaultAP = "br-tg"

	// CounterTag prefixes the per-client rule comment so auth can find and
	// dedupe its own rules and deauth can delete them by handle. The full
	// comment is "tollgate:counter:<key>".
	CounterTag = "tollgate:counter"

	// KiB is the report divisor. Truncating division is deliberate.
	KiB = 1024
)

// ---------------------------------------------------------------------------
// nft seam
// ---------------------------------------------------------------------------

// Runner is the seam to the nft binary. Production uses ExecRunner; tests
// inject a fake that returns canned dumps and records the scripts it was asked
// to run.
type Runner interface {
	// Run applies an nft script via `nft -f -`.
	Run(script string) error
	// JSON runs `nft -j <args...>` and returns raw stdout.
	JSON(args ...string) ([]byte, error)
}

// ExecRunner shells out to a real nft binary.
type ExecRunner struct{ Path string }

func (e ExecRunner) Run(script string) error {
	path := e.Path
	if path == "" {
		path = "nft"
	}
	cmd := exec.Command(path, "-f", "-")
	cmd.Stdin = strings.NewReader(script)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("nft -f -: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func (e ExecRunner) JSON(args ...string) ([]byte, error) {
	path := e.Path
	if path == "" {
		path = "nft"
	}
	full := append([]string{"-j"}, args...)
	out, err := exec.Command(path, full...).Output()
	if err != nil {
		return nil, fmt.Errorf("nft %s: %w", strings.Join(full, " "), err)
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// config
// ---------------------------------------------------------------------------

// Config locates the kernel state the shim drives. Zero values take defaults.
type Config struct {
	Runner Runner
	// AP is $AP, the guest interface the per-client rules match on.
	AP string
	// TableFamily/TableName name the valve's table (default inet/tollgate).
	TableFamily string
	TableName   string
	// Chain is the forward chain the per-client rules are added to.
	Chain string
	// Lock is the flock path (default LockPath).
	Lock string
}

func (c Config) withDefaults() Config {
	if c.Runner == nil {
		c.Runner = ExecRunner{}
	}
	if c.AP == "" {
		c.AP = DefaultAP
	}
	if c.TableFamily == "" {
		c.TableFamily = DefaultTableFamily
	}
	if c.TableName == "" {
		c.TableName = DefaultTableName
	}
	if c.Chain == "" {
		c.Chain = DefaultChain
	}
	if c.Lock == "" {
		c.Lock = LockPath
	}
	return c
}

// ---------------------------------------------------------------------------
// ruleset model (minimal decode of `nft -j list <kind>`)
// ---------------------------------------------------------------------------

// SetObj is a named nft set.
type SetObj struct {
	Family string `json:"family"`
	Table  string `json:"table"`
	Name   string `json:"name"`
}

// CounterObj is a named nft counter.
type CounterObj struct {
	Family  string `json:"family"`
	Table   string `json:"table"`
	Name    string `json:"name"`
	Packets int64  `json:"packets"`
	Bytes   int64  `json:"bytes"`
}

// RuleObj is a subset of an nft rule.
type RuleObj struct {
	Family  string `json:"family"`
	Table   string `json:"table"`
	Chain   string `json:"chain"`
	Handle  int    `json:"handle"`
	Comment string `json:"comment"`
}

type envelope struct {
	Nftables []map[string]json.RawMessage `json:"nftables"`
}

func decodeKind(b []byte, key string) ([]json.RawMessage, error) {
	var env envelope
	if err := json.Unmarshal(b, &env); err != nil {
		return nil, fmt.Errorf("decode nft json: %w", err)
	}
	var out []json.RawMessage
	for _, obj := range env.Nftables {
		if raw, ok := obj[key]; ok {
			out = append(out, raw)
		}
	}
	return out, nil
}

// ParseSets decodes `nft -j list sets`.
func ParseSets(b []byte) ([]SetObj, error) {
	raws, err := decodeKind(b, "set")
	if err != nil {
		return nil, err
	}
	var out []SetObj
	for _, raw := range raws {
		var s SetObj
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, fmt.Errorf("decode set: %w", err)
		}
		out = append(out, s)
	}
	return out, nil
}

// ParseCounters decodes `nft -j list counters`.
func ParseCounters(b []byte) ([]CounterObj, error) {
	raws, err := decodeKind(b, "counter")
	if err != nil {
		return nil, err
	}
	var out []CounterObj
	for _, raw := range raws {
		var c CounterObj
		if err := json.Unmarshal(raw, &c); err != nil {
			return nil, fmt.Errorf("decode counter: %w", err)
		}
		out = append(out, c)
	}
	return out, nil
}

// ParseRules decodes `nft -j list rules`.
func ParseRules(b []byte) ([]RuleObj, error) {
	raws, err := decodeKind(b, "rule")
	if err != nil {
		return nil, err
	}
	var out []RuleObj
	for _, raw := range raws {
		var r RuleObj
		if err := json.Unmarshal(raw, &r); err != nil {
			return nil, fmt.Errorf("decode rule: %w", err)
		}
		out = append(out, r)
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// identity
// ---------------------------------------------------------------------------

var nonHex = regexp.MustCompile(`[^0-9a-f]`)

// CounterKey maps a MAC to the identifier used in counter and comment names.
// nft identifiers may not contain ":", so the separators are dropped and the
// result lowercased: "AA:BB:CC:DD:EE:FF" -> "aabbccddeeff".
func CounterKey(mac string) string {
	return nonHex.ReplaceAllString(strings.ToLower(mac), "")
}

func upName(key string) string { return "up_" + key }
func dlName(key string) string { return "dl_" + key }

// counterComment is the per-direction rule tag, "tollgate:counter:<key>:<dir>",
// so auth can find and dedupe its own rule for one direction and deauth can
// match every rule it owns for the client.
func counterComment(key, dir string) string { return CounterTag + ":" + key + ":" + dir }

// commentKey returns the client key embedded in a counter comment, or "".
func commentKey(comment string) string {
	parts := strings.Split(comment, ":")
	if len(parts) < 4 || parts[0] != "tollgate" || parts[1] != "counter" {
		return ""
	}
	return parts[2]
}

// commentDir returns "up", "dl", or "both" for a counter comment (the last is
// the conservative answer for a comment written before direction was encoded).
func commentDir(comment string) string {
	parts := strings.Split(comment, ":")
	if len(parts) < 4 {
		return "both"
	}
	switch parts[3] {
	case "up", "dl":
		return parts[3]
	default:
		return "both"
	}
}

// ---------------------------------------------------------------------------
// error classification
// ---------------------------------------------------------------------------

// nft reports additive-verb conflicts as "File exists" (EEXIST) and removals of
// absent objects as "No such file or directory" (ENOENT). The exact strings
// come from libnftables and are stable across 1.x.
var (
	reEEXIST = regexp.MustCompile(`(?i)\b(file exists|already exists)\b`)
	reENOENT = regexp.MustCompile(`(?i)\b(no such file or directory|does not exist|could not process rule: No such)\b`)
)

// IsEEXIST reports whether an nft error means "the object already exists". The
// shim treats that as success: auth is idempotent by design.
func IsEEXIST(err error) bool { return err != nil && reEEXIST.MatchString(err.Error()) }

// IsENOENT reports whether an nft error means "the object is not there". The
// shim treats that as success for deletions.
func IsENOENT(err error) bool { return err != nil && reENOENT.MatchString(err.Error()) }

// ---------------------------------------------------------------------------
// locking (see lock.go / lock_noflock.go)
// ---------------------------------------------------------------------------

// withLock runs fn while holding an exclusive flock on path, creating the
// parent directory and the lock file if needed. Under the `nft_noflock` build
// tag it degrades to a plain call — that build is the card's negative control
// and only exists to prove the lock is load-bearing.
func withLock(path string, fn func() error) error {
	return lockGate(path, fn)
}

// ---------------------------------------------------------------------------
// auth
// ---------------------------------------------------------------------------

// ErrNoAuthedSet means no `authed_v4` set exists anywhere: the valve has not
// been rendered/applied, so there is nothing to authorise into.
var ErrNoAuthedSet = errors.New("no authed_v4 set found: valve ruleset not applied")

// Auth authorises ip for the client identified by mac. It is idempotent:
//
//  1. add ip as an element of every existing `authed_v4` set, tolerating EEXIST;
//  2. ensure the up_<key>/dl_<key> named counters exist;
//  3. ensure exactly one up rule and one dl rule referencing them exist.
//
// Steps 2 and 3 are check-then-act; the whole call holds the flock so two
// concurrent shim processes cannot both decide a rule is absent and both add it.
func Auth(cfg Config, mac, ip string) error {
	cfg = cfg.withDefaults()
	if err := validateIP(ip); err != nil {
		return err
	}
	key := CounterKey(mac)
	if key == "" {
		return fmt.Errorf("auth: empty MAC")
	}
	return withLock(cfg.Lock, func() error { return authLocked(cfg, key, mac, ip) })
}

func authLocked(cfg Config, key, mac, ip string) error {
	sets, err := listAuthedSets(cfg)
	if err != nil {
		return err
	}
	if len(sets) == 0 {
		return ErrNoAuthedSet
	}
	// 1. element, one script per set so an EEXIST on one set cannot abort the
	//    element add for another (nft -f is a single transaction).
	for _, s := range sets {
		stmt := fmt.Sprintf("add element %s %s %s { %s }\n", s.Family, s.Table, s.Name, ip)
		if err := cfg.Runner.Run(stmt); err != nil && !IsEEXIST(err) {
			return fmt.Errorf("auth: add %s to %s %s: %w", ip, s.Family, s.Table, err)
		}
	}
	// testHookStall widens the read-do-write window for the concurrency
	// evidence; it is a no-op unless the environment opts in.
	testHookStall()
	// 2. counters
	existing, err := listCounters(cfg)
	if err != nil {
		return err
	}
	have := map[string]bool{}
	for _, c := range existing {
		have[c.Name] = true
	}
	for _, name := range []string{upName(key), dlName(key)} {
		if have[name] {
			continue
		}
		stmt := fmt.Sprintf("add counter %s %s %s\n", cfg.TableFamily, cfg.TableName, name)
		if err := cfg.Runner.Run(stmt); err != nil && !IsEEXIST(err) {
			return fmt.Errorf("auth: add counter %s: %w", name, err)
		}
	}
	// 3. rules
	rules, err := listRules(cfg)
	if err != nil {
		return err
	}
	haveRule := map[string]bool{}
	for _, r := range rules {
		if commentKey(r.Comment) == key {
			haveRule[commentDir(r.Comment)] = true
		}
	}
	for _, r := range []struct{ dir, name, expr string }{
		{"up", upName(key), upExpr(cfg.AP, mac, upName(key))},
		{"dl", dlName(key), dlExpr(cfg.AP, mac, dlName(key))},
	} {
		if haveRule[r.dir] || haveRule["both"] {
			continue
		}
		stmt := fmt.Sprintf("add rule %s %s %s %s comment %s\n",
			cfg.TableFamily, cfg.TableName, cfg.Chain, r.expr, nftQuote(counterComment(key, r.dir)))
		if err := cfg.Runner.Run(stmt); err != nil && !IsEEXIST(err) {
			return fmt.Errorf("auth: add %s rule: %w", r.dir, err)
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// deauth
// ---------------------------------------------------------------------------

// Deauth removes ip from every `authed_v4` set, deletes the per-client counter
// rules (matched on their comment), and deletes the named counters so a
// subsequent json reads zero. Every step tolerates "not present", so deauth is
// safe to run on an unknown or already-deauthorised client.
func Deauth(cfg Config, mac, ip string) error {
	cfg = cfg.withDefaults()
	key := CounterKey(mac)
	if key == "" {
		return fmt.Errorf("deauth: empty MAC")
	}
	if ip != "" {
		if err := validateIP(ip); err != nil {
			return err
		}
	}
	return withLock(cfg.Lock, func() error { return deauthLocked(cfg, key, ip) })
}

func deauthLocked(cfg Config, key, ip string) error {
	sets, err := listAuthedSets(cfg)
	if err != nil {
		return err
	}
	for _, s := range sets {
		if ip == "" {
			continue
		}
		stmt := fmt.Sprintf("delete element %s %s %s { %s }\n", s.Family, s.Table, s.Name, ip)
		if err := cfg.Runner.Run(stmt); err != nil && !IsENOENT(err) {
			return fmt.Errorf("deauth: delete %s from %s %s: %w", ip, s.Family, s.Table, err)
		}
	}
	// rules first (they reference the counters), then the counters themselves.
	rules, err := listRules(cfg)
	if err != nil {
		return err
	}
	for _, r := range rules {
		if commentKey(r.Comment) != key {
			continue
		}
		stmt := fmt.Sprintf("delete rule %s %s %s handle %d\n", r.Family, r.Table, r.Chain, r.Handle)
		if err := cfg.Runner.Run(stmt); err != nil && !IsENOENT(err) {
			return fmt.Errorf("deauth: delete rule handle %d: %w", r.Handle, err)
		}
	}
	counters, err := listCounters(cfg)
	if err != nil {
		return err
	}
	for _, c := range counters {
		if c.Name != upName(key) && c.Name != dlName(key) {
			continue
		}
		stmt := fmt.Sprintf("delete counter %s %s %s\n", c.Family, c.Table, c.Name)
		if err := cfg.Runner.Run(stmt); err != nil && !IsENOENT(err) {
			return fmt.Errorf("deauth: delete counter %s: %w", c.Name, err)
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// counters -> json
// ---------------------------------------------------------------------------

// CountersKiB returns (up, down) in KiB for the client, read from
// `nft -j list counters` and divided by 1024 with truncation. Missing counters
// read as (0, 0), which is exactly what a deauthorised client looks like.
func CountersKiB(cfg Config, mac string) (up, down int64, err error) {
	cfg = cfg.withDefaults()
	key := CounterKey(mac)
	counters, err := listCounters(cfg)
	if err != nil {
		return 0, 0, err
	}
	for _, c := range counters {
		switch c.Name {
		case upName(key):
			up = c.Bytes / KiB
		case dlName(key):
			down = c.Bytes / KiB
		}
	}
	return up, down, nil
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func listAuthedSets(cfg Config) ([]SetObj, error) {
	raw, err := cfg.Runner.JSON("list", "sets")
	if err != nil {
		return nil, fmt.Errorf("list sets: %w", err)
	}
	all, err := ParseSets(raw)
	if err != nil {
		return nil, err
	}
	var out []SetObj
	for _, s := range all {
		if s.Name == AuthedSet {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Family != out[j].Family {
			return out[i].Family < out[j].Family
		}
		return out[i].Table < out[j].Table
	})
	return out, nil
}

func listCounters(cfg Config) ([]CounterObj, error) {
	raw, err := cfg.Runner.JSON("list", "counters")
	if err != nil {
		return nil, fmt.Errorf("list counters: %w", err)
	}
	return ParseCounters(raw)
}

func listRules(cfg Config) ([]RuleObj, error) {
	// nft has no `list rules` verb; the whole-ruleset dump is the correct
	// source. ParseRules ignores everything that is not a rule.
	raw, err := cfg.Runner.JSON("list", "ruleset")
	if err != nil {
		return nil, fmt.Errorf("list ruleset: %w", err)
	}
	return ParseRules(raw)
}

// expr builders. The literal shape is fixed by the card:
//
//	iifname $AP ether saddr <mac> counter name up_<key>
//	oifname $AP ether daddr <mac> counter name dl_<key>
//
// Values are nft-quoted, not merely shell-quoted; ":" in a MAC must be quoted
// at the nft level or nft rejects the script.
func upExpr(ap, mac, name string) string {
	return fmt.Sprintf("iifname %s ether saddr %s counter name %s", nftQuote(ap), nftQuote(mac), name)
}

func dlExpr(ap, mac, name string) string {
	return fmt.Sprintf("oifname %s ether daddr %s counter name %s", nftQuote(ap), nftQuote(mac), name)
}

func nftQuote(s string) string { return `"` + s + `"` }

func validateIP(ip string) error {
	if strings.TrimSpace(ip) == "" {
		return errors.New("empty address")
	}
	// The set is ipv4_addr; reject anything that is not dotted-quad so a bad
	// lease cannot inject an nft token.
	if !regexp.MustCompile(`^\d{1,3}(\.\d{1,3}){3}$`).MatchString(ip) {
		return fmt.Errorf("not an IPv4 address: %q", ip)
	}
	return nil
}
