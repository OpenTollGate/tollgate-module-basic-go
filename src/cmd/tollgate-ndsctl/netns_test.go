//go:build netns

// netns_test.go — the kernel-facing evidence tier for LINUX-HOST-6.
//
// It is compiled only under `-tags netns` and is a no-op unless run as root
// with TOLLGATE_NETNS=1, because it needs `ip netns`, `nft` and a real forward
// path. It builds a throwaway topology that mirrors the host-mode shape:
//
//	cli(10.66.0.2, MAC 02:11:22:33:44:55) --vc-- [br-tg] --host-- vs0 -- srv(192.168.9.2)
//
// `br-tg` is the guest bridge ($AP). The host forwards cli -> srv, so forwarded
// frames cross the inet forward hook where the per-client counter rules live.
//
// The legs are the card's evidence list:
//
//  1. auth twice            -> both succeed, exactly ONE element in authed_v4
//  2. two concurrent auths  -> ruleset still parses, exactly one up/dl rule
//  3. transfer 4 MiB        -> json KiB > 0
//  4. deauth                -> json reads zeroed counters
//
// Leg 2 is the negative control's target: built with `-tags "netns nft_noflock"`
// (flock compiled out) it leaves duplicate rules and FAILS.
//
// This test drives the package API directly rather than a CLI binary, because
// the CLI (main.go) is LINUX-HOST-5 SHIM-CLI's deliverable and is not part of
// this card.
package main

import (
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	nsCli     = "tgn-cli"
	nsSrv     = "tgn-srv"
	brTG      = "br-tg"
	guestMac  = "02:11:22:33:44:55"
	guestIP   = "10.66.0.2"
	srvIP     = "192.168.9.2"
	hostWanIP = "192.168.9.1"
)

func nsrunCmd(t *testing.T, name string, args ...string) string {
	t.Helper()
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
	return string(out)
}

func nsrun(t *testing.T, ns string, args ...string) string {
	t.Helper()
	return nsrunCmd(t, "ip", append([]string{"netns", "exec", ns}, args...)...)
}

func nft(t *testing.T, args ...string) string {
	t.Helper()
	return nsrunCmd(t, "nft", args...)
}

func TestNetnsNftBackend(t *testing.T) {
	if os.Getenv("TOLLGATE_NETNS") != "1" {
		t.Skip("kernel evidence tier: set TOLLGATE_NETNS=1 and run as root")
	}
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	setupTopology(t)

	cfg := Config{Runner: ExecRunner{Path: "nft"}, Lock: LockPath}

	// --- leg 1: auth twice -> exit 0 both, exactly ONE element -------------
	if err := Auth(cfg, guestMac, guestIP); err != nil {
		t.Fatalf("first auth: %v", err)
	}
	if err := Auth(cfg, guestMac, guestIP); err != nil {
		t.Fatalf("second auth: %v", err)
	}
	if n := strings.Count(nft(t, "list", "set", "inet", "tollgate", "authed_v4"), guestIP); n != 1 {
		t.Fatalf("authed_v4 holds %d elements, want exactly 1\n%s", n,
			nft(t, "list", "set", "inet", "tollgate", "authed_v4"))
	}
	t.Logf("leg1 ok: two auths, one element")

	// --- leg 2: two concurrent auths -> ruleset parses, one rule each -------
	// Start from a clean slate so both racers see the rules as absent; without
	// the lock they both insert and the count below is 4, not 2.
	if err := Deauth(cfg, guestMac, guestIP); err != nil {
		t.Fatalf("deauth before concurrency leg: %v", err)
	}
	// Widen the read-do-write window so the race is observable without a lock.
	os.Setenv(stallEnv, "400")
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) { defer wg.Done(); errs[i] = Auth(cfg, guestMac, guestIP) }(i)
	}
	wg.Wait()
	os.Unsetenv(stallEnv)
	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent auth %d: %v", i, err)
		}
	}
	if _, err := exec.Command("nft", "-j", "list", "ruleset").Output(); err != nil {
		t.Fatalf("ruleset does not parse after concurrent auths: %v", err)
	}
	if got := countCounterRules(t); got != 2 {
		t.Fatalf("counter rules for %s = %d, want exactly 2 (up + dl): the flock is not serialising (expected failure under -tags nft_noflock)", guestMac, got)
	}
	t.Logf("leg2 ok: concurrent auths left exactly one up + one dl rule")

	// --- leg 3: transfer 4 MiB -> KiB > 0 ----------------------------------
	transfer(t, 4)
	up, dl, err := CountersKiB(cfg, guestMac)
	if err != nil {
		t.Fatal(err)
	}
	if up <= 0 && dl <= 0 {
		t.Fatalf("after a 4 MiB transfer counters read up=%d dl=%d KiB, want > 0", up, dl)
	}
	t.Logf("leg3 ok: up=%d KiB dl=%d KiB after 4 MiB", up, dl)

	// --- leg 4: deauth -> zeroed counters ----------------------------------
	if err := Deauth(cfg, guestMac, guestIP); err != nil {
		t.Fatalf("deauth: %v", err)
	}
	up, dl, err = CountersKiB(cfg, guestMac)
	if err != nil {
		t.Fatal(err)
	}
	if up != 0 || dl != 0 {
		t.Fatalf("after deauth counters read up=%d dl=%d KiB, want 0/0", up, dl)
	}
	if n := strings.Count(nft(t, "list", "set", "inet", "tollgate", "authed_v4"), guestIP); n != 0 {
		t.Fatalf("element survived deauth")
	}
	if got := countCounterRules(t); got != 0 {
		t.Fatalf("counter rules survived deauth: %d", got)
	}
	t.Logf("leg4 ok: deauth removed element, rules and counters; json reads 0/0")
}

// countCounterRules counts the up/dl rules we own for guestMac.
func countCounterRules(t *testing.T) int {
	t.Helper()
	raw, err := exec.Command("nft", "-j", "list", "ruleset").Output()
	if err != nil {
		t.Fatalf("list ruleset: %v", err)
	}
	rules, err := ParseRules(raw)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, r := range rules {
		if commentKey(r.Comment) == CounterKey(guestMac) {
			n++
		}
	}
	return n
}

func transfer(t *testing.T, mib int) {
	t.Helper()
	// 4 MiB of UDP from the guest toward the server across the forward path.
	script := "dd if=/dev/zero bs=65536 count=" + itoa(mib*16) + " 2>/dev/null | nc -u -w1 " + srvIP + " 9999"
	nsrun(t, nsCli, "sh", "-c", script)
	time.Sleep(200 * time.Millisecond)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// ---------------------------------------------------------------------------
// topology
// ---------------------------------------------------------------------------

func setupTopology(t *testing.T) {
	t.Helper()
	cleanupTopology()
	t.Cleanup(cleanupTopology)

	nsrunCmd(t, "ip", "netns", "add", nsCli)
	nsrunCmd(t, "ip", "netns", "add", nsSrv)

	// guest bridge
	nsrunCmd(t, "ip", "link", "add", brTG, "type", "bridge")
	nsrunCmd(t, "ip", "link", "set", brTG, "up")
	nsrunCmd(t, "ip", "addr", "add", "10.66.0.1/24", "dev", brTG)

	// client veth
	nsrunCmd(t, "ip", "link", "add", "vc0", "type", "veth", "peer", "name", "vc1")
	nsrunCmd(t, "ip", "link", "set", "vc1", "master", brTG)
	nsrunCmd(t, "ip", "link", "set", "vc1", "up")
	nsrunCmd(t, "ip", "link", "set", "vc0", "netns", nsCli)
	nsrun(t, nsCli, "ip", "link", "set", "vc0", "address", guestMac)
	nsrun(t, nsCli, "ip", "link", "set", "vc0", "up")
	nsrun(t, nsCli, "ip", "addr", "add", guestIP+"/24", "dev", "vc0")
	nsrun(t, nsCli, "ip", "route", "add", "default", "via", "10.66.0.1")

	// WAN veth to the server namespace
	nsrunCmd(t, "ip", "link", "add", "vs0", "type", "veth", "peer", "name", "vs1")
	nsrunCmd(t, "ip", "link", "set", "vs0", "up")
	nsrunCmd(t, "ip", "addr", "add", hostWanIP+"/24", "dev", "vs0")
	nsrunCmd(t, "ip", "link", "set", "vs1", "netns", nsSrv)
	nsrun(t, nsSrv, "ip", "link", "set", "vs1", "up")
	nsrun(t, nsSrv, "ip", "addr", "add", srvIP+"/24", "dev", "vs1")

	// route + forward
	nsrunCmd(t, "sysctl", "-w", "net.ipv4.ip_forward=1")

	// the valve table the shim drives (NETUP-RENDER's shape, minimal)
	script := `table inet tollgate {
	set authed_v4 { type ipv4_addr ; }
	chain forward_gate { type filter hook forward priority filter - 10; policy accept; }
}`
	cmd := exec.Command("nft", "-f", "-")
	cmd.Stdin = strings.NewReader(script)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create valve table: %v\n%s", err, out)
	}
}

func cleanupTopology() {
	_ = exec.Command("ip", "netns", "del", nsCli).Run()
	_ = exec.Command("ip", "netns", "del", nsSrv).Run()
	for _, l := range []string{"vc0", "vs0", brTG} {
		_ = exec.Command("ip", "link", "del", l).Run()
	}
	_ = exec.Command("nft", "delete", "table", "inet", "tollgate").Run()
}
