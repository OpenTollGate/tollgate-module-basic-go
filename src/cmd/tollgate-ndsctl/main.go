package main

// main.go — the `ndsctl` shim's command line (LINUX-HOST-5).
//
// Exactly three verbs are accepted:
//
//	ndsctl auth <mac>
//	ndsctl deauth <mac>
//	ndsctl json <mac>      (and, for the module's client list, `ndsctl json`)
//
// Everything else — an unknown verb, a missing MAC — is a usage error and exits
// non-zero, so a typo can never be read as a quiet success.
//
// The binary is installed at /usr/libexec/tollgate/ndsctl. Nothing in src/valve
// is aware it is a shim: it answers the same three shapes opennds' ndsctl does,
// with the exit-status semantics the module's retry loop and its `{}` handling
// depend on. See contract.go for the exact JSON and exit-status contract.

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// Exit statuses. The module only distinguishes "answered" (0) from "did not
// answer" (non-zero), but two codes make the shim's own failures legible:
const (
	exitOK          = 0 // the verb answered (including the `{}` answer)
	exitClientError = 1 // the host does not know the MAC, or state could not be read
	exitUsage       = 2 // unknown verb, or a missing MAC argument
)

// config is everything about the shim's environment that a test wants to
// control: where the host's client knowledge lives, where the shim's own state
// lives, and what "now" is.
type config struct {
	leasesPath string
	arpPath    string
	statePath  string
	now        func() time.Time
}

// defaultConfig reads the environment overrides and falls back to the real host
// paths. See backend.go for what each path is.
func defaultConfig() config {
	return config{
		leasesPath: envOr("TOLLGATE_DHCP_LEASES", "/tmp/dhcp.leases"),
		arpPath:    envOr("TOLLGATE_PROC_ARP", "/proc/net/arp"),
		statePath:  envOr("TOLLGATE_NDSCTL_STATE", "/run/tollgate/ndsctl-state.json"),
		now:        time.Now,
	}
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func main() {
	os.Exit(run(os.Args[1:], defaultConfig(), os.Stdout, os.Stderr))
}

// run is the testable entry point: it never calls os.Exit and never touches the
// real environment beyond the config it is handed.
func run(args []string, cfg config, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintf(stderr, "usage: ndsctl {%s} <mac>\n", strings.Join(knownVerbs, "|"))
		return exitUsage
	}

	host := hostFiles{leasesPath: cfg.leasesPath, arpPath: cfg.arpPath}
	backend := newBackend(cfg)

	switch args[0] {
	case verbAuth:
		if len(args) < 2 {
			fmt.Fprintf(stderr, "usage: ndsctl %s <mac>\n", verbAuth)
			return exitUsage
		}
		return runAuth(args[1], host, backend, cfg, stdout, stderr)

	case verbDeauth:
		if len(args) < 2 {
			fmt.Fprintf(stderr, "usage: ndsctl %s <mac>\n", verbDeauth)
			return exitUsage
		}
		return runDeauth(args[1], host, backend, cfg, stdout, stderr)

	case verbJSON:
		if len(args) < 2 {
			return runJSONList(backend, cfg, stdout, stderr)
		}
		return runJSONClient(args[1], host, backend, cfg, stdout, stderr)

	default:
		// NoDogSplash's own ndsctl answers generic text for an unknown command;
		// the shim refuses instead, so a caller bug is loud. The module never
		// sends an unknown verb — this is for operators and tests.
		fmt.Fprintf(stderr, "ndsctl: unknown command %q; want one of %s\n", args[0], strings.Join(knownVerbs, ", "))
		return exitUsage
	}
}

// runAuth authorizes a MAC. A MAC the host does not know is a non-zero exit:
// authorizeMAC retries its auth 5x at 400 ms precisely so a client whose
// session is not registered yet converges, and that retry is dead code unless
// an unknown client really fails.
func runAuth(mac string, host hostFiles, backend Backend, cfg config, stdout, stderr io.Writer) int {
	ip, known := host.Locate(mac)
	if !known {
		fmt.Fprintf(stderr, "ndsctl auth %s: no lease in the DHCP lease file and no /proc/net/arp neighbour: the client is not on the host yet\n", mac)
		return exitClientError
	}
	if err := backend.Authorize(mac, ip, cfg.now().Unix()); err != nil {
		fmt.Fprintf(stderr, "ndsctl auth %s: %v\n", mac, err)
		return exitClientError
	}
	fmt.Fprintf(stdout, "Client %s has been authenticated.\n", mac)
	return exitOK
}

// runDeauth deauthorizes a MAC. Two negative answers are distinguished on
// purpose:
//
//   - the host does not know the MAC at all: a hard error (non-zero), the
//     presence gate;
//   - the host knows the MAC but the shim holds no authorized session: the
//     measured ndsctl answer "Client <mac> not found." with exit 1. The module
//     reads that phrase (ndsctlUnknownClient) as "NoDogSplash does not know this
//     client": nothing is left to deauthorize, so deauthorizeMAC retires the
//     gate instead of arming a retry that can never converge.
func runDeauth(mac string, host hostFiles, backend Backend, cfg config, stdout, stderr io.Writer) int {
	if _, known := host.Locate(mac); !known {
		fmt.Fprintf(stderr, "ndsctl deauth %s: no lease in the DHCP lease file and no /proc/net/arp neighbour: the client is not on the host\n", mac)
		return exitClientError
	}
	_, held, err := backend.Lookup(mac)
	if err != nil {
		fmt.Fprintf(stderr, "ndsctl deauth %s: %v\n", mac, err)
		return exitClientError
	}
	if err := backend.Deauthorize(mac); err != nil {
		fmt.Fprintf(stderr, "ndsctl deauth %s: %v\n", mac, err)
		return exitClientError
	}
	if !held {
		fmt.Fprintf(stdout, "Client %s not found.\n", mac)
		return exitClientError
	}
	fmt.Fprintf(stdout, "Client %s has been deauthenticated.\n", mac)
	return exitOK
}

// runJSONClient answers `ndsctl json <mac>`: the single-client record, or `{}`
// exit 0 for a MAC the host knows but the shim has not authorized. A MAC the
// host does not know at all is the presence-gate error.
func runJSONClient(mac string, host hostFiles, backend Backend, cfg config, stdout, stderr io.Writer) int {
	if _, known := host.Locate(mac); !known {
		fmt.Fprintf(stderr, "ndsctl json %s: no lease in the DHCP lease file and no /proc/net/arp neighbour: the client is not on the host\n", mac)
		return exitClientError
	}
	sessions, err := backend.List()
	if err != nil {
		fmt.Fprintf(stderr, "ndsctl json %s: %v\n", mac, err)
		return exitClientError
	}
	session, held := sessions[normalizeMAC(mac)]
	if !held {
		fmt.Fprintln(stdout, emptyObject)
		return exitOK
	}
	rendered, err := formatStats(statsFor(mac, session, idOf(sessions, mac), cfg.now().Unix()))
	if err != nil {
		fmt.Fprintf(stderr, "ndsctl json %s: %v\n", mac, err)
		return exitClientError
	}
	fmt.Fprint(stdout, rendered)
	return exitOK
}

// runJSONList answers `ndsctl json` (no argument): the whole client list, the
// shape nds_clients.go parses. With no authorized client it answers `{}`, which
// ListClients reads as a definitive empty enforcement layer.
func runJSONList(backend Backend, cfg config, stdout, stderr io.Writer) int {
	sessions, err := backend.List()
	if err != nil {
		fmt.Fprintf(stderr, "ndsctl json: %v\n", err)
		return exitClientError
	}
	if len(sessions) == 0 {
		fmt.Fprintln(stdout, emptyObject)
		return exitOK
	}
	macs := sortedMACs(sessions)
	clients := make(map[string]ClientStats, len(macs))
	for i, mac := range macs {
		clients[mac] = statsFor(mac, sessions[mac], i+1, cfg.now().Unix())
	}
	rendered, err := formatList(clientList{ClientLength: len(macs), Clients: clients})
	if err != nil {
		fmt.Fprintf(stderr, "ndsctl json: %v\n", err)
		return exitClientError
	}
	fmt.Fprint(stdout, rendered)
	return exitOK
}

// statsFor turns a stored session into the wire record. `active` is the later
// of the session's own last-activity stamp and the moment of the query, so
// `duration` grows with the session rather than freezing at authorization; it
// is not persisted, so a read never mutates what the customer paid for.
func statsFor(mac string, session Session, id int, now int64) ClientStats {
	active := session.Active
	if now > active {
		active = now
	}
	duration := active - session.Added
	if duration < 0 {
		duration = 0
	}
	token := session.Token
	if token == "" {
		token = tokenFor(mac)
	}
	return ClientStats{
		ID:         id,
		IP:         session.IP,
		MAC:        mac,
		Added:      session.Added,
		Active:     active,
		Duration:   duration,
		Token:      token,
		State:      stateAuthenticated,
		Downloaded: session.Downloaded,
		Uploaded:   session.Uploaded,
	}
}

// idOf is the session's stable id: its 1-based position in MAC-sorted order.
// opennds' ids are opaque; the module never reads them, but a stable value is
// what lets the golden fixture be byte-exact.
func idOf(sessions map[string]Session, mac string) int {
	key := normalizeMAC(mac)
	for i, m := range sortedMACs(sessions) {
		if m == key {
			return i + 1
		}
	}
	return 1
}
