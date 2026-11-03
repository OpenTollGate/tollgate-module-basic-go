#!/usr/bin/env python3
"""v6 captive-portal regression campaign — the #783 / lane rel-769 test debt.

What this pins dynamically on a pristine install of a tollgate-wrt artifact
(default: the v0.6.0-rc1 closure), over the serial console of a snapshot-mode
QEMU VM (no hostfwd, no ssh — the console socket is the only control path, so
the rig can never collide with sibling lanes' ports):

  RIG   the captive bridge carries no uplink NIC. The stock x86 image bridges
        eth0 into br-lan; in a QEMU rig eth0 is a slirp uplink and slirp is
        ITSELF an IPv6 router (RA source 52:56:00:00:00:02 / fe80::2, fec0::/64
        PIO). Left in, every captive test on this rig shape tests the
        hypervisor, not the router — exactly how lane rel-769 produced the
        #783 false bypass (full forensics: rel-769 transcript addendum).
        The campaign de-bridges such ports (live + uci) and FAILS if any
        survive, naming them.
  T1    the package's IPv6-off state (#148/#160): dhcp.lan.ra='disabled',
        dhcp.lan.dhcpv6='disabled', network.lan.ip6assign='0'.
  T2    wire silence: zero Router Advertisements (icmp6 type 134) on br-lan
        over the capture window.
  T3    RS -> silence: a fresh client's Router Solicitation gets no RA answer.
  T4    no SLAAC: the client never gains a global/site IPv6 address and no
        `proto ra` default route. (With no RA source and no prefix there is
        no v6 path at all — this IS the escape axis.)
  T5    the pre-auth external :80 fetch is captive-intercepted (splash-sized
        reply) or blocked — never the real internet.
  T6    no over-block: the v4 customer journey still works — UDP DNS answers
        from the router (#749/#769 regression guard) and the portal on
        tcp/2050 fetches pre-auth.

Usage (ai-legion): see README.md. Defaults point at the shared image and the
rc1 package closure; every knob is an env var.
"""
import hashlib
import http.server
import os
import re
import socket
import subprocess
import sys
import threading
import time

IMAGE = os.environ.get("V6CR_IMAGE", "/root/tollgate-vm/openwrt-25.12.0-x86-64-combined.img")
PKGDIR = os.environ.get("V6CR_PKGDIR", "/root/tollgate-vm/pkgs")
WORK = os.environ.get("V6CR_WORK", "/tmp/opencode/v6cr-work")
MEM = os.environ.get("V6CR_MEM", "1024")
APKS = os.environ.get(
    "V6CR_APKS",
    "tollgate-wrt-0.6.0_rc1-r0.apk nodogsplash-5.0.2-r2.apk "
    "libmicrohttpd-no-ssl-1.0.2-r1.apk jq-1.8.1-r2.apk iptables-nft-1.8.10-r2.apk "
    "iptables-mod-conntrack-extra-1.8.10-r2.apk iptables-mod-ipopt-1.8.10-r2.apk "
    "iptables-mod-nat-extra-1.8.10-r2.apk",
).split()
FEED_PKGS = os.environ.get("V6CR_FEED_PKGS", "ip-full kmod-veth bind-dig tcpdump").split()
RA_WINDOW_S = int(os.environ.get("V6CR_RA_WINDOW_S", "25"))

B, E = "__V6B__", "__V6E__"
CONSOLE_LOG = None
HTTPD = None
HTTPD_PORT = 0

PASS = FAIL = 0
VERDICTS = None  # evidence-side verdict log, opened in main()


def vlog(line):
    # Verdicts must live in the EVIDENCE dir, not only on the driver's
    # stdout: a committed console.log without the PASS/FAIL lines is not
    # verifiable evidence (the round-1 review's finding).
    print(line, flush=True)
    if VERDICTS:
        VERDICTS.write(line + "\n")


def check(name, ok, detail=""):
    global PASS, FAIL
    vlog(f"{'PASS' if ok else 'FAIL'} {name} {detail}".rstrip())
    PASS, FAIL = PASS + (1 if ok else 0), FAIL + (0 if ok else 1)
    return ok


def die(msg):
    vlog(f"FATAL {msg}")
    sys.exit(2)


def sha256(path, _buf=1 << 20):
    h = hashlib.sha256()
    with open(path, "rb") as f:
        while chunk := f.read(_buf):
            h.update(chunk)
    return h.hexdigest()


# ---------------------------------------------------------------- console
class Console:
    """Serial-console driver with sentinel-delimited output capture.

    The pty prints the shell prompt right after command output with no
    separating newline ("8root@host:~#") and echoes the command itself, so
    naive line parsing is wrong twice over. fetch() wraps commands in unique
    sentinels and returns strictly between them."""

    def __init__(self, path):
        self.s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        self.s.connect(path)
        self.s.settimeout(2.0)
        self.buf = b""

    def pump(self, t=1.0):
        end = time.time() + t
        while time.time() < end:
            try:
                c = self.s.recv(65536)
                if c:
                    self.buf += c
                    if CONSOLE_LOG:
                        CONSOLE_LOG.write(c.decode(errors="replace"))
            except socket.timeout:
                pass

    def sync(self, timeout=240):
        end = time.time() + timeout
        while time.time() < end:
            self.pump(3)
            self.s.sendall(b"\n")
            if re.search(rb"root@[\w().-]+:[^ ]+# ", self.buf):
                time.sleep(0.5)
                return True
        return False

    def run(self, cmd, timeout=120):
        self.buf = b""
        self.s.sendall(cmd.encode() + b"\n")
        probe = cmd.split(";")[0].strip()[:16]
        deadline = time.time() + timeout
        while time.time() < deadline:
            self.pump(2)
            txt = self.buf.decode(errors="replace")
            m = re.search(rb"root@[\w().-]+:[^ ]+# \s*$", self.buf)
            if m and probe and probe in txt:
                echo = txt.find(probe)
                if m.start() > echo:
                    self.pump(1.2)  # let trailing output land
                    return self.buf.decode(errors="replace")
        return self.buf.decode(errors="replace")

    def fetch(self, cmd, timeout=120):
        """Run cmd; return ONLY its output (between sentinels), de-wrapped.

        The console echoes the whole command line — including both sentinels
        — BEFORE the real `echo` outputs land, so anchor on the LAST marker
        occurrences (the echo pair precedes the real pair on the wire)."""
        o = self.run(f"echo {B}; {cmd}; echo {E}", timeout)
        last_b, last_e = o.rfind(B), o.rfind(E)
        if last_b == -1 or last_e == -1 or last_e < last_b:
            return ""
        return re.sub(r"\s+", " ", o[last_b + len(B):last_e])


# ---------------------------------------------------------------- rig
class QuietHandler(http.server.SimpleHTTPRequestHandler):
    def log_message(self, *a):
        pass


def start_httpd():
    global HTTPD, HTTPD_PORT
    handler = lambda *a, **kw: QuietHandler(*a, directory=PKGDIR, **kw)
    HTTPD = http.server.ThreadingHTTPServer(("127.0.0.1", 0), handler)
    HTTPD_PORT = HTTPD.server_address[1]
    threading.Thread(target=HTTPD.serve_forever, daemon=True).start()


def boot_qemu():
    sock = f"{WORK}/console.sock"
    if os.path.exists(sock):
        os.unlink(sock)
    cmd = [
        "qemu-system-x86_64", "-machine", "pc", "-cpu", "max", "-m", MEM,
        "-drive", f"file={IMAGE},format=raw,if=virtio,snapshot=on",
        "-netdev", "user,id=net0", "-device", "virtio-net-pci,netdev=net0",
        "-netdev", "user,id=net1", "-device", "virtio-net-pci,netdev=net1",
        "-display", "none", f"-serial", f"unix:{sock},server,nowait",
        "-monitor", "none", "-daemonize", "-pidfile", f"{WORK}/vm.pid",
        "-name", "v6cr.qemu",
    ]
    rc = subprocess.run(cmd, capture_output=True, text=True)
    if rc.returncode != 0:
        die(f"qemu failed: {rc.stderr.strip()[:300]}")
    time.sleep(2)


def teardown():
    try:
        with open(f"{WORK}/vm.pid") as f:
            os.kill(int(f.read().strip()), 15)
    except Exception:
        subprocess.run(["pkill", "-f", "v6cr.qem[u]"], capture_output=True)
    time.sleep(1)
    if HTTPD:
        HTTPD.shutdown()


def ra_count(con, cap_file):
    con.run("pkill tcpdump 2>/dev/null", 20)
    o = con.fetch(f"grep -c 'router advertisement' {cap_file} 2>/dev/null || echo 0", 30)
    n = re.search(r"(\d+)", o)
    return int(n.group(1)) if n else -1


def main():
    global CONSOLE_LOG, VERDICTS
    os.makedirs(WORK, exist_ok=True)
    for p in (IMAGE, PKGDIR):
        if not os.path.exists(p):
            die(f"missing {p}")
    for a in APKS:
        if not os.path.exists(os.path.join(PKGDIR, a)):
            die(f"missing apk {a} in {PKGDIR}")
    with open("/proc/meminfo") as f:
        avail = int(next(l for l in f if l.startswith("MemAvailable")).split()[1]) // 1024
    if avail < int(MEM) // 2 + 512:
        die(f"only {avail}MiB available — refusing to start a {MEM}M VM on a loaded host")

    VERDICTS = open(f"{WORK}/verdicts.log", "a", buffering=1)
    VERDICTS.write(
        f"v6-captive campaign {time.strftime('%Y-%m-%dT%H:%M:%S%z')} "
        f"ra_window={RA_WINDOW_S}s mem={MEM}\n"
        f"image {IMAGE} sha256={sha256(IMAGE)}\n"
    )
    for a in APKS:
        VERDICTS.write(f"apk {a} sha256={sha256(os.path.join(PKGDIR, a))}\n")

    start_httpd()
    boot_qemu()
    CONSOLE_LOG = open(f"{WORK}/console.log", "a", buffering=1)
    con = Console(f"{WORK}/console.sock")
    if not con.sync():
        die("no boot prompt")

    rel = con.fetch("grep DISTRIB_RELEASE /etc/openwrt_release")
    check("vm-25.12-boot", "25.12" in rel, rel.strip()[:40])

    con.run("stty cols 400")
    con.run("printf 'tg\\ntg\\n' | passwd root >/dev/null 2>&1 && echo PASSWD_OK")

    # tools from the release feeds (25.12 is apk, not opkg)
    o = con.fetch(
        f"apk update >/dev/null 2>&1; apk add {' '.join(FEED_PKGS)} >/dev/null 2>&1; "
        "command -v dig >/dev/null && command -v tcpdump >/dev/null && command -v ip >/dev/null && echo OK || echo MISSING",
        420,
    )
    check("tools-installed", "OK" in o, o.strip()[-40:])

    # Stage one file per short command — the pty's line editor drops input
    # past ~1024 chars per line, so a single joined one-liner is silently
    # truncated (seen as a mid-command cut in the console echo).
    con.fetch("mkdir -p /tmp/staged && rm -f /tmp/staged/* && echo MKDIR_OK")
    staged = 0
    missing = []
    for a in APKS:
        o = con.fetch(f"wget -q http://10.0.2.2:{HTTPD_PORT}/{a} -O /tmp/staged/{a} && echo GOT || echo WGET_FAIL", 300)
        if "GOT" in o:
            staged += 1
        else:
            missing.append(a)
    check("closure-staged", staged == len(APKS), f"got={staged} want={len(APKS)} missing={missing or 'none'}")

    o = con.run("apk add --allow-untrusted /tmp/staged/*.apk 2>&1 | tail -2; echo APK_RC=$?", 600)
    check("rc-install", "APK_RC=0" in o, "")
    for _ in range(40):
        if "SVC_UP" in con.fetch("pgrep -f tollgate-wrt >/dev/null && echo SVC_UP || echo SVC_DOWN", 30):
            break
        time.sleep(3)
    o = con.fetch("pgrep -f tollgate-wrt >/dev/null && echo SVC_UP || echo SVC_DOWN")
    check("service-up", "SVC_UP" in o, "")

    # -- RIG: no uplink inside the captive bridge -------------------------
    brif = con.fetch("ls /sys/class/net/br-lan/brif/ 2>/dev/null | tr '\\n' ' '").split()
    uplinks = [p for p in brif if re.match(r"^eth\d+$", p)]
    detail = f"br-lan ports: {brif or 'none'}"
    if uplinks:
        for p in uplinks:
            con.run(f"ip link set dev {p} nomaster 2>/dev/null", 20)
            con.run(
                f"sec=$(uci show network | grep 'ports=' | grep '{p}' | head -1 "
                f"| cut -d. -f2 | cut -d= -f1); "
                f"[ -n \"$sec\" ] && uci del_list network.$sec.ports='{p}' && uci commit network; echo FIXED_{p}",
                30,
            )
            print(f"  rig-fix: de-bridged {p} (live nomaster + uci del_list)", flush=True)
        brif2 = con.fetch("ls /sys/class/net/br-lan/brif/ 2>/dev/null | tr '\\n' ' '").split()
        uplinks = [p for p in brif2 if re.match(r"^eth\d+$", p)]
        detail += f" -> after fix: {brif2 or 'none'}"
    check("rig-no-uplink-in-captive-bridge", not uplinks, detail)

    # -- T1: the package's IPv6-off state ---------------------------------
    vals = [v.strip() for v in con.fetch(
        "uci get dhcp.lan.ra; uci get dhcp.lan.dhcpv6; uci get network.lan.ip6assign"
    ).replace("uci: Entry not found", "MISSING").split() if v.strip()]
    check("t1-uci-ipv6-off", vals == ["disabled", "disabled", "0"], f"ra/dhcpv6/ip6assign = {vals}")

    # -- T1b: the #815 global axes, conditional on artifact era ----------
    # #815 (the #783 mitigation) adds network.lan.ipv6='0',
    # network.wan.ipv6='0', network.wan6.disabled='1'. On a pre-#815
    # artifact (e.g. the rc1 closure) none of those keys exist — skip, not
    # fail. On a post-#815 artifact the first two must hold; wan6 only when
    # the image actually has a wan6 section (#815's writer is
    # existence-checked there — wan6-less is adoption, not error).
    lan6 = con.fetch("uci -q get network.lan.ipv6").strip()
    if not lan6:
        check("t1b-uci-ipv6-global-off", True, "skipped: pre-#815 artifact (network.lan.ipv6 unset)")
    else:
        wan6_has = bool(con.fetch("uci -q show network.wan6 2>/dev/null").strip())
        wan6_ok = (not wan6_has) or con.fetch("uci -q get network.wan6.disabled").strip() == "1"
        check(
            "t1b-uci-ipv6-global-off",
            lan6 == "0"
            and con.fetch("uci -q get network.wan.ipv6").strip() == "0"
            and wan6_ok,
            f"lan.ipv6={lan6} wan.ipv6={con.fetch('uci -q get network.wan.ipv6').strip()!r} "
            f"wan6={'n/a' if not wan6_has else con.fetch('uci -q get network.wan6.disabled').strip()!r}",
        )

    # -- T2: RA wire silence ----------------------------------------------
    con.run("tcpdump -i br-lan -nn 'icmp6 and ip6[40] == 134' -c 4 > /tmp/ra-watch.txt 2>&1 &", 20)
    time.sleep(RA_WINDOW_S)
    n2 = ra_count(con, "/tmp/ra-watch.txt")
    check("t2-no-ra-on-br-lan", n2 == 0, f"RA packets in {RA_WINDOW_S}s = {n2}")

    # -- T3 + T4: fresh client — RS must go unanswered, no SLAAC ----------
    con.run(
        "ip netns add v6ns; ip link add v-host type veth peer name v-cli; ip link set v-cli netns v6ns; "
        "ip link set v-host master br-lan up; "
        "ip -n v6ns link set v-cli address 02:00:00:6b:36:01; "
        "ip -n v6ns addr add 192.168.1.61/24 dev v-cli; "
        "ip -n v6ns link set v-cli up; ip -n v6ns link set lo up; "
        "ip -n v6ns route add default via 192.168.1.1; "
        "mkdir -p /etc/netns/v6ns; echo 'nameserver 192.168.1.1' > /etc/netns/v6ns/resolv.conf; "
        "echo CLIENT_UP",
        40,
    )
    con.run("tcpdump -i br-lan -nn 'icmp6 and ip6[40] == 134' -c 2 > /tmp/ra-cli.txt 2>&1 &", 20)
    time.sleep(12)
    n3 = ra_count(con, "/tmp/ra-cli.txt")
    r6 = con.fetch("ip netns exec v6ns ip -6 route show default")
    addrs = con.fetch("ip netns exec v6ns ip -6 addr show v-cli | grep 'inet6 ' || true")
    check("t3-rs-gets-no-ra", n3 == 0, f"RA replies after client RS = {n3}")
    check("t4-no-slaac-no-ra-route",
          "default via" not in r6 and "proto ra" not in r6
          and "scope global" not in addrs and "scope site" not in addrs,
          f"ra-route={'none' if 'default via' not in r6 else 'PRESENT'}; "
          f"addrs={addrs.strip()[:60] or 'link-local only'}")

    # -- T5: pre-auth external fetch must be intercepted, not the internet -
    # NB: never assert on a piped busybox rc — in `cmd | tail`, $? is tail's.
    # The byte count is the verdict: the real page is tens of KB; the splash
    # is <5 KB; a hard block leaves the -O file empty.
    con.run("ip netns exec v6ns wget -T 6 -O /tmp/esc.html http://openwrt.org/ >/dev/null 2>&1", 60)
    size = con.fetch("wc -c < /tmp/esc.html 2>/dev/null || echo 0")
    m = re.search(r"(\d+)", size)
    esc_n = int(m.group(1)) if m else -1
    check("t5-external-fetch-intercepted", 0 <= esc_n < 5000,
          f"bytes={esc_n} (splash-intercepted or blocked; >=5000 would be the real internet)")

    # -- T6: v4 journey unharmed ------------------------------------------
    ns = con.run("ip netns exec v6ns nslookup openwrt.org 192.168.1.1 2>&1; echo NSDONE", 40)
    check("t6a-v4-udp-dns-answers", "Address" in ns and "timed out" not in ns and "refused" not in ns, "")
    con.run("ip netns exec v6ns wget -T 6 -O /tmp/portal.html http://192.168.1.1:2050/ >/dev/null 2>&1", 40)
    size = con.fetch("wc -c < /tmp/portal.html 2>/dev/null || echo 0")
    m = re.search(r"(\d+)", size)
    portal_n = int(m.group(1)) if m else 0
    check("t6b-v4-portal-fetch-preauth", portal_n > 100, f"bytes={portal_n}")

    vlog(f"SUMMARY PASS={PASS} FAIL={FAIL} evidence={WORK}")
    return 0 if FAIL == 0 else 1


if __name__ == "__main__":
    try:
        rc = main()
    finally:
        teardown()
    sys.exit(rc)
