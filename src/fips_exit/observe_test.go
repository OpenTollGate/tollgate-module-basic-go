package fips_exit

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

// iproute2 JSON-format fixtures, with synthetic identities/interfaces. Not
// recordings from a deployed router and not daemon/dataplane evidence.
func probeFixtures(c Config) []string {
	local, _ := Derive(c.LocalPublicKey)
	exit, _ := Derive(c.ExitPublicKey)
	return []string{
		`[{"ifindex":1,"ifname":"lo","flags":["LOOPBACK","UP","LOWER_UP"],"mtu":65536,"operstate":"UNKNOWN"},{"ifindex":2,"ifname":"guest-test","flags":["BROADCAST","MULTICAST","UP","LOWER_UP"],"mtu":1500,"operstate":"UP"},{"ifindex":3,"ifname":"mesh-test","flags":["POINTOPOINT","NOARP","UP","LOWER_UP"],"mtu":1280,"operstate":"UNKNOWN"}]`,
		fmt.Sprintf(`[{"ifindex":1,"ifname":"lo","addr_info":[{"family":"inet","local":"127.0.0.1","prefixlen":8,"scope":"host"}]},{"ifindex":2,"ifname":"guest-test","addr_info":[{"family":"inet","local":"192.168.10.1","prefixlen":24,"scope":"global"}]},{"ifindex":3,"ifname":"mesh-test","addr_info":[{"family":"inet6","local":%q,"prefixlen":128,"scope":"global","valid_life_time":4294967295,"preferred_life_time":4294967295}]}]`, local.String()),
		`[{"priority":0,"src":"all","table":"local"},{"priority":32766,"src":"all","table":"main"},{"priority":32767,"src":"all","table":"default"}]`,
		`[{"dst":"default","gateway":"192.0.2.1","dev":"wan","protocol":"dhcp","metric":10,"flags":[]},{"type":"local","dst":"127.0.0.1","dev":"lo","table":"local","protocol":"kernel","scope":"host","prefsrc":"127.0.0.1","flags":[]}]`,
		fmt.Sprintf(`[{"dst":%q,"from":%q,"dev":"mesh-test","table":"main","protocol":"kernel","src":%q,"metric":256,"flags":[],"pref":"medium"}]`, exit.String(), local.String(), local.String()),
	}
}
func TestCollectLinkRoutes(t *testing.T) {
	c := validConfig()
	local, _ := Derive(c.LocalPublicKey)
	exit, _ := Derive(c.ExitPublicKey)
	want := [][]string{{"-j", "link", "show"}, {"-j", "address", "show"}, {"-j", "-4", "rule", "show"}, {"-j", "-4", "route", "show", "table", "all"}, {"-j", "-6", "route", "get", exit.String(), "from", local.String()}}
	fixtures := probeFixtures(c)
	calls := 0
	e, err := CollectLinkRoutes(context.Background(), c, func(ctx context.Context, p Probe) ([]byte, error) {
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("unbounded context")
		}
		if p.Program != "ip" || p.MaxOutputBytes != 1<<20 || !reflect.DeepEqual(p.Args, want[calls]) {
			t.Fatalf("unexpected probe: %+v", p)
		}
		out := fixtures[calls]
		calls++
		return []byte(out), nil
	}, time.Second)
	if err != nil || calls != 5 || e.Config != c || e.Interfaces[c.FIPSInterface] != 1280 || e.LocalAddress != local.String() {
		t.Fatalf("e=%+v calls=%d err=%v", e, calls, err)
	}
	if _, err := BuildPlan(c, Environment{}, GuardReadiness{}); err == nil {
		t.Fatal("observer must not enable planner")
	}
}
func TestCollectDisabled(t *testing.T) {
	e, err := CollectLinkRoutes(context.Background(), Config{}, nil, 0)
	if err != nil || !reflect.DeepEqual(e, LinkRouteEvidence{}) {
		t.Fatalf("disabled: %+v %v", e, err)
	}
}
func TestCollectRefusals(t *testing.T) {
	cases := []struct {
		name     string
		index    int
		old, new string
	}{
		{"missing guest", 0, "guest-test", "other"}, {"missing fips", 0, "mesh-test", "other"},
		{"occupied tunnel", 0, "lo", "exit-test"}, {"mtu", 0, "1280", "1200"},
		{"down", 0, `"POINTOPOINT","NOARP","UP","LOWER_UP"`, `"POINTOPOINT","NOARP"`},
		{"wrong address", 1, "mesh-test", "other"}, {"tentative", 1, `"scope":"global","valid_life_time"`, `"tentative":true,"scope":"global","valid_life_time"`},
		{"priority occupied", 2, "32766", "10000"}, {"table rule", 2, `"main"`, `200`}, {"unknown table", 2, `"main"`, `"custom"`},
		{"table route", 3, `"local"`, `200`}, {"wrong route", 4, "mesh-test", "wan"},
		{"stale source", 4, `"from":`, `"unused":`}, {"route error", 4, `"metric":256`, `"error":101,"metric":256`},
		{"unknown route property", 4, `"metric":256`, `"surprise":true,"metric":256`},
		{"malformed route type", 4, `"metric":256`, `"type":99,"metric":256`},
		{"unknown link property", 0, `"mtu":1280`, `"surprise":true,"mtu":1280`},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			f := probeFixtures(validConfig())
			f[tt.index] = strings.ReplaceAll(f[tt.index], tt.old, tt.new)
			assertProbeRefused(t, f, -1)
		})
	}
	for i := 0; i < 5; i++ {
		for _, bad := range []string{"", "null", "{}", "[{}]", "[", "[] trailing"} {
			f := probeFixtures(validConfig())
			f[i] = bad
			assertProbeRefused(t, f, -1)
		}
		assertProbeRefused(t, probeFixtures(validConfig()), i)
	}
}
func assertProbeRefused(t *testing.T, f []string, fail int) {
	t.Helper()
	n := 0
	e, err := CollectLinkRoutes(context.Background(), validConfig(), func(context.Context, Probe) ([]byte, error) {
		i := n
		n++
		if i == fail {
			return []byte(f[i]), errors.New("partial output/failed command")
		}
		return []byte(f[i]), nil
	}, time.Second)
	if err == nil || !reflect.DeepEqual(e, LinkRouteEvidence{}) {
		t.Fatalf("accepted incomplete evidence: %+v %v fixtures=%v", e, err, f)
	}
}
func TestCollectBounds(t *testing.T) {
	for _, mode := range []string{"timeout", "cancel", "oversize", "nil", "duration", "config"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			c := validConfig()
			timeout := time.Millisecond
			run := ProbeRunner(func(ctx context.Context, p Probe) ([]byte, error) {
				if mode == "oversize" {
					return make([]byte, p.MaxOutputBytes+1), nil
				}
				<-ctx.Done()
				return nil, ctx.Err()
			})
			switch mode {
			case "cancel":
				cancel()
			case "nil":
				run = nil
			case "duration":
				timeout = 0
			case "config":
				c.MTU = 0
			}
			if _, err := CollectLinkRoutes(ctx, c, run, timeout); err == nil {
				t.Fatal("expected refusal")
			}
		})
	}
}
