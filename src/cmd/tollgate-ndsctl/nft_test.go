package main

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"testing"
)

// ---------------------------------------------------------------------------
// a stateful in-memory nft model, good enough to drive the auth/deauth/counters
// code paths without a kernel.
// ---------------------------------------------------------------------------

type simRule struct {
	family  string
	table   string
	chain   string
	comment string
	handle  int
}

type sim struct {
	mu         sync.Mutex
	sets       map[string]map[string]bool // "family/table" -> element set
	counters   map[string]int64           // name -> bytes
	rules      []simRule
	nextHandle int
	ran        []string
}

func newSim() *sim {
	return &sim{
		sets:     map[string]map[string]bool{},
		counters: map[string]int64{},
	}
}

func (s *sim) addSet(family, table, name string) {
	s.sets[family+"/"+table] = map[string]bool{}
}

var (
	reAddElement = regexp.MustCompile(`^add element (\S+) (\S+) (\S+) \{ (\S+) \}$`)
	reDelElement = regexp.MustCompile(`^delete element (\S+) (\S+) (\S+) \{ (\S+) \}$`)
	reAddCounter = regexp.MustCompile(`^add counter (\S+) (\S+) (\S+)$`)
	reDelCounter = regexp.MustCompile(`^delete counter (\S+) (\S+) (\S+)$`)
	reAddRule    = regexp.MustCompile(`^add rule (\S+) (\S+) (\S+) (.*) comment "([^"]*)"$`)
	reDelRule    = regexp.MustCompile(`^delete rule (\S+) (\S+) (\S+) handle (\d+)$`)
)

func (s *sim) Run(script string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, line := range strings.Split(strings.TrimSpace(script), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		s.ran = append(s.ran, line)
		switch {
		case reAddElement.MatchString(line):
			m := reAddElement.FindStringSubmatch(line)
			key := m[1] + "/" + m[2]
			set, ok := s.sets[key]
			if !ok {
				return fmt.Errorf("add element: no such set %s", key)
			}
			if set[m[4]] {
				// nft answers a duplicate element with EEXIST.
				return fmt.Errorf("add element: File exists")
			}
			set[m[4]] = true
		case reDelElement.MatchString(line):
			m := reDelElement.FindStringSubmatch(line)
			set := s.sets[m[1]+"/"+m[2]]
			if set == nil || !set[m[4]] {
				return fmt.Errorf("delete element: No such file or directory")
			}
			delete(set, m[4])
		case reAddCounter.MatchString(line):
			m := reAddCounter.FindStringSubmatch(line)
			if _, ok := s.counters[m[3]]; ok {
				return fmt.Errorf("add counter: File exists")
			}
			s.counters[m[3]] = 0
		case reDelCounter.MatchString(line):
			m := reDelCounter.FindStringSubmatch(line)
			if _, ok := s.counters[m[3]]; !ok {
				return fmt.Errorf("delete counter: No such file or directory")
			}
			delete(s.counters, m[3])
		case reAddRule.MatchString(line):
			m := reAddRule.FindStringSubmatch(line)
			s.nextHandle++
			s.rules = append(s.rules, simRule{m[1], m[2], m[3], m[5], s.nextHandle})
		case reDelRule.MatchString(line):
			m := reDelRule.FindStringSubmatch(line)
			var h int
			fmt.Sscanf(m[4], "%d", &h)
			kept := s.rules[:0]
			for _, r := range s.rules {
				if r.handle != h {
					kept = append(kept, r)
				}
			}
			s.rules = kept
		default:
			return fmt.Errorf("sim: unhandled script %q", line)
		}
	}
	return nil
}

func (s *sim) JSON(args ...string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	kind := args[len(args)-1]
	var objs []map[string]any
	switch kind {
	case "sets":
		for key := range s.sets {
			parts := strings.SplitN(key, "/", 2)
			objs = append(objs, map[string]any{"set": map[string]any{
				"family": parts[0], "table": parts[1], "name": AuthedSet,
			}})
		}
	case "counters":
		for name, bytes := range s.counters {
			objs = append(objs, map[string]any{"counter": map[string]any{
				"family": DefaultTableFamily, "table": DefaultTableName,
				"name": name, "packets": 1, "bytes": bytes,
			}})
		}
	case "ruleset":
		for _, r := range s.rules {
			objs = append(objs, map[string]any{"rule": map[string]any{
				"family": r.family, "table": r.table, "chain": r.chain,
				"handle": r.handle, "comment": r.comment,
			}})
		}
		// ruleset also carries the sets and counters; the code paths that read
		// it only look at rules, so that is all the fake needs to emit.
	case "rules":
		for _, r := range s.rules {
			objs = append(objs, map[string]any{"rule": map[string]any{
				"family": r.family, "table": r.table, "chain": r.chain,
				"handle": r.handle, "comment": r.comment,
			}})
		}
	default:
		return nil, fmt.Errorf("sim: unknown json kind %q", kind)
	}
	return json.Marshal(map[string]any{"nftables": objs})
}

func (s *sim) elements(family, table string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.sets[family+"/"+table])
}

func (s *sim) ruleCount(key string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, r := range s.rules {
		if commentKey(r.comment) == key {
			n++
		}
	}
	return n
}

func (s *sim) counterCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.counters)
}

// ---------------------------------------------------------------------------
// tests
// ---------------------------------------------------------------------------

const (
	mac = "AA:BB:CC:DD:EE:FF"
	key = "aabbccddeeff"
	ip  = "10.66.0.2"
)

func TestCounterKey(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"AA:BB:CC:DD:EE:FF", "aabbccddeeff"},
		{"aa-bb-cc-dd-ee-ff", "aabbccddeeff"},
		{"aabb.ccdd.eeff", "aabbccddeeff"},
	} {
		if got := CounterKey(tc.in); got != tc.want {
			t.Errorf("CounterKey(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestAuthTwiceExactlyOneElement(t *testing.T) {
	s := newSim()
	s.addSet("inet", "tollgate", AuthedSet)
	cfg := Config{Runner: s, Lock: t.TempDir() + "/ndsctl.lock"}

	if err := Auth(cfg, mac, ip); err != nil {
		t.Fatalf("first auth: %v", err)
	}
	if err := Auth(cfg, mac, ip); err != nil {
		t.Fatalf("second auth (must tolerate EEXIST): %v", err)
	}
	if got := s.elements("inet", "tollgate"); got != 1 {
		t.Fatalf("authed_v4 holds %d elements, want exactly 1", got)
	}
}

func TestAuthInstallsOneUpOneDlRule(t *testing.T) {
	s := newSim()
	s.addSet("inet", "tollgate", AuthedSet)
	cfg := Config{Runner: s, Lock: t.TempDir() + "/ndsctl.lock"}

	if err := Auth(cfg, mac, ip); err != nil {
		t.Fatal(err)
	}
	if err := Auth(cfg, mac, ip); err != nil {
		t.Fatal(err)
	}
	if got := s.ruleCount(key); got != 2 {
		t.Fatalf("counter rules for %s = %d, want exactly 2 (up + dl)", key, got)
	}
	if got := s.counterCount(); got != 2 {
		t.Fatalf("named counters = %d, want exactly 2", got)
	}
	// the literal expression shape the card fixes:
	var sawUp, sawDl bool
	for _, l := range s.ran {
		if strings.Contains(l, `iifname "br-tg" ether saddr "`+mac+`" counter name up_`+key) {
			sawUp = true
		}
		if strings.Contains(l, `oifname "br-tg" ether daddr "`+mac+`" counter name dl_`+key) {
			sawDl = true
		}
	}
	if !sawUp || !sawDl {
		t.Fatalf("expected iifname/ether saddr up rule and oifname/ether daddr dl rule; ran=%v", s.ran)
	}
}

func TestAuthAddsElementToEveryMirror(t *testing.T) {
	s := newSim()
	s.addSet("inet", "tollgate", AuthedSet) // the valve's own table
	s.addSet("ip", "filter", AuthedSet)     // a LINUX-HOST-4 integrated mirror
	cfg := Config{Runner: s, Lock: t.TempDir() + "/ndsctl.lock"}

	if err := Auth(cfg, mac, ip); err != nil {
		t.Fatal(err)
	}
	if s.elements("inet", "tollgate") != 1 || s.elements("ip", "filter") != 1 {
		t.Fatalf("element not mirrored into every authed_v4 set")
	}
}

func TestAuthWithoutSetIsAnError(t *testing.T) {
	s := newSim()
	cfg := Config{Runner: s, Lock: t.TempDir() + "/ndsctl.lock"}
	if err := Auth(cfg, mac, ip); err != ErrNoAuthedSet {
		t.Fatalf("auth with no authed_v4 = %v, want ErrNoAuthedSet", err)
	}
}

func TestAuthRejectsNonIPv4(t *testing.T) {
	s := newSim()
	s.addSet("inet", "tollgate", AuthedSet)
	cfg := Config{Runner: s, Lock: t.TempDir() + "/ndsctl.lock"}
	if err := Auth(cfg, mac, "2001:db8::1"); err == nil {
		t.Fatal("auth accepted an IPv6 address for an ipv4_addr set")
	}
}

func TestCountersTruncateTowardZero(t *testing.T) {
	s := newSim()
	s.addSet("inet", "tollgate", AuthedSet)
	s.counters[upName(key)] = 4*1024*1024 + 1023 // 4096 KiB + 1023 B
	s.counters[dlName(key)] = 1023               // < 1 KiB
	cfg := Config{Runner: s, Lock: t.TempDir() + "/ndsctl.lock"}

	up, dl, err := CountersKiB(cfg, mac)
	if err != nil {
		t.Fatal(err)
	}
	if up != 4096 {
		t.Errorf("up = %d KiB, want 4096 (truncated)", up)
	}
	if dl != 0 {
		t.Errorf("dl = %d KiB, want 0 (truncated)", dl)
	}
}

func TestDeauthRemovesElementRulesAndCounters(t *testing.T) {
	s := newSim()
	s.addSet("inet", "tollgate", AuthedSet)
	cfg := Config{Runner: s, Lock: t.TempDir() + "/ndsctl.lock"}

	if err := Auth(cfg, mac, ip); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.counters[upName(key)] = 8192
	s.counters[dlName(key)] = 4096
	s.mu.Unlock()

	if err := Deauth(cfg, mac, ip); err != nil {
		t.Fatal(err)
	}
	if s.elements("inet", "tollgate") != 0 {
		t.Error("element survived deauth")
	}
	if s.ruleCount(key) != 0 {
		t.Error("counter rules survived deauth")
	}
	if s.counterCount() != 0 {
		t.Error("named counters survived deauth")
	}
	up, dl, err := CountersKiB(cfg, mac)
	if err != nil {
		t.Fatal(err)
	}
	if up != 0 || dl != 0 {
		t.Errorf("counters read back %d/%d KiB after deauth, want 0/0", up, dl)
	}
}

func TestDeauthUnknownClientIsNoop(t *testing.T) {
	s := newSim()
	s.addSet("inet", "tollgate", AuthedSet)
	cfg := Config{Runner: s, Lock: t.TempDir() + "/ndsctl.lock"}
	if err := Deauth(cfg, mac, "10.66.0.9"); err != nil {
		t.Fatalf("deauth of an unauthorised client must be a no-op, got %v", err)
	}
	if len(s.ran) != 1 { // just the failed element delete
		t.Errorf("unexpected nft work for a no-op deauth: %v", s.ran)
	}
}

func TestParseCountersFixture(t *testing.T) {
	fixture := []byte(`{"nftables":[
		{"metainfo":{"version":"1.1.5"}},
		{"counter":{"family":"inet","table":"tollgate","name":"up_aabbccddeeff","handle":5,"packets":10,"bytes":10485760}},
		{"counter":{"family":"inet","table":"tollgate","name":"dl_aabbccddeeff","handle":6,"packets":3,"bytes":2048}}
	]}`)
	cs, err := ParseCounters(fixture)
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 2 {
		t.Fatalf("decoded %d counters, want 2", len(cs))
	}
	for _, c := range cs {
		switch c.Name {
		case "up_aabbccddeeff":
			if c.Bytes/1024 != 10240 {
				t.Errorf("up bytes = %d", c.Bytes)
			}
		case "dl_aabbccddeeff":
			if c.Bytes/1024 != 2 {
				t.Errorf("dl bytes = %d", c.Bytes)
			}
		}
	}
}
