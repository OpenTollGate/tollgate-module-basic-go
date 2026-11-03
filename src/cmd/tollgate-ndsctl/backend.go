package main

// backend.go — where the shim's answers come from.
//
// The CLI is a MODULE-VISIBLE PROCESS: src/valve runs `ndsctl <verb>` as a
// fresh child every time (valve.go runNdsctl), so any state the shim keeps has
// to be durable and shared between processes — an in-process map would forget
// every authorization the moment the child that created it exited. Two pieces
// make that work here:
//
//  1. HostClients — "does the HOST know this MAC, and at which IP?". This is
//     answered from the same two places a host actually learns a client's
//     address: dnsmasq's /tmp/dhcp.leases and the kernel neighbour table at
//     /proc/net/arp. It is the presence gate: a MAC in neither is not on the
//     host yet, and the shim answers with a non-zero exit so authorizeMAC's
//     5x/400 ms retry can converge once the client appears.
//  2. Backend — "which clients does the shim consider AUTHORIZED, and what are
//     their counters?". The portable default is a JSON state file under
//     /run/tollgate; LINUX-HOST-6 replaces it with the nftables backend
//     (authed_v4 + per-MAC counters) behind this same interface. The seam is
//     deliberate: the CLI surface and the JSON contract do not change when the
//     enforcement layer does.
//
// Both paths are overridable for tests and for the live host runs:
//
//	TOLLGATE_DHCP_LEASES   default /tmp/dhcp.leases
//	TOLLGATE_PROC_ARP      default /proc/net/arp
//	TOLLGATE_NDSCTL_STATE  default /run/tollgate/ndsctl-state.json
//
// Nothing here shells out and nothing here needs CGO: the binary must stay
// buildable for the router's mips/mipsel/arm targets the same way src/valve is.

import (
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
)

// ---------------------------------------------------------------------------
// Presence: does the host know this MAC, and at which IP?
// ---------------------------------------------------------------------------

// HostClients answers whether the host knows a MAC at all. It is the presence
// gate the retry loop hangs off.
type HostClients interface {
	Locate(mac string) (ip string, known bool)
}

// hostFiles is the production HostClients: dnsmasq's lease file first (it
// carries the address the host actually handed out), then the neighbour table
// (a statically-addressed client has an ARP entry but no lease).
type hostFiles struct {
	leasesPath string
	arpPath    string
}

func (h hostFiles) Locate(mac string) (string, bool) {
	if data, err := os.ReadFile(h.leasesPath); err == nil {
		if ip, ok := parseDhcpLeases(string(data), mac); ok {
			return ip, true
		}
	}
	if data, err := os.ReadFile(h.arpPath); err == nil {
		if ip, ok := parseProcArp(string(data), mac); ok {
			return ip, true
		}
	}
	return "", false
}

// parseDhcpLeases reads the dnsmasq lease format, one record per line:
//
//	<expiry> <mac> <ip> <hostname> <client-id>
//
// The MAC compares case-insensitively (dnsmasq lower-cases it; the module sends
// whatever the payment carried). When a MAC holds several leases the LONGEST
// expiry wins — the lease that is actually current, not a stale row.
func parseDhcpLeases(data, mac string) (string, bool) {
	want := strings.ToLower(strings.TrimSpace(mac))
	bestExpiry := int64(-1)
	bestIP := ""
	for _, line := range strings.Split(data, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		if strings.ToLower(fields[1]) != want {
			continue
		}
		expiry, err := parseInt64(fields[0])
		if err != nil {
			continue
		}
		if expiry > bestExpiry {
			bestExpiry, bestIP = expiry, fields[2]
		}
	}
	return bestIP, bestIP != ""
}

// parseProcArp reads the kernel neighbour table:
//
//	IP address       HW type     Flags       HW address            Mask     Device
//	192.168.1.124    0x1         0x2         a8:a0:92:a5:39:7a     *        br-lan
//
// An entry with Flags 0x0 is INCOMPLETE (the kernel has not resolved the
// neighbour): it is not evidence the client is present, so it is skipped. The
// all-zero address is skipped for the same reason.
func parseProcArp(data, mac string) (string, bool) {
	want := strings.ToLower(strings.TrimSpace(mac))
	for i, line := range strings.Split(data, "\n") {
		if i == 0 {
			continue // header
		}
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		if strings.ToLower(fields[3]) != want {
			continue
		}
		if fields[2] == "0x0" || fields[3] == "00:00:00:00:00:00" {
			continue
		}
		return fields[0], true
	}
	return "", false
}

// ---------------------------------------------------------------------------
// Sessions: which clients are authorized, and their counters.
// ---------------------------------------------------------------------------

// Session is one authorized client, as the shim holds it. Counters are in
// KILOBYTES, matching the ndsctl contract; the backend that sources them
// (nftables counters in LINUX-HOST-6) is responsible for the KiB quantisation.
type Session struct {
	IP         string `json:"ip"`
	Added      int64  `json:"added"`
	Active     int64  `json:"active"`
	Downloaded uint64 `json:"downloaded"` // KiB
	Uploaded   uint64 `json:"uploaded"`   // KiB
	Token      string `json:"token"`
}

// Backend holds the authorized set and its counters. Every method must be safe
// to call from a fresh process: implementations persist to disk.
type Backend interface {
	// Authorize adds (or refreshes) a client. It is idempotent: authorizing an
	// already-authorized MAC succeeds and leaves its original `added` time and
	// counters alone, because a re-auth of a live session must not reset the
	// metering the customer has already paid for.
	Authorize(mac, ip string, now int64) error
	// Deauthorize removes a client. Removing a client that is not authorized
	// is not an error here; the CLI turns that into the ndsctl "not found"
	// answer that deauthorizeMAC reads as an already-closed gate.
	Deauthorize(mac string) error
	// Lookup returns the authorized session for a MAC. ok is false when the MAC
	// carries no authorized session — the `{}` answer.
	Lookup(mac string) (Session, bool, error)
	// List returns every authorized session.
	List() (map[string]Session, error)
}

// tokenFor derives the opaque 8-hex token opennds prints. It is a function of
// the MAC so the same client yields the same token across processes and runs,
// which is what lets the golden fixture be byte-stable.
func tokenFor(mac string) string {
	return fmt.Sprintf("%08x", crc32.ChecksumIEEE([]byte(strings.ToLower(strings.TrimSpace(mac)))))
}

// newBackend builds the backend the CLI uses. Today that is the portable state
// file; LINUX-HOST-6 installs the nftables backend here (same interface) when
// the host has the authed_v4 set and per-MAC counters to read.
func newBackend(env config) Backend {
	return &fileBackend{path: env.statePath}
}

// fileBackend is the portable Backend: a JSON object keyed by MAC, written
// atomically and guarded by an advisory lock so two concurrent ndsctl children
// (the module drives the CLI from several goroutines) cannot interleave a
// read-modify-write. It is a stand-in for the nftables backend; it exists so
// the CLI surface and the JSON contract are exercisable, and testable, before
// the enforcement layer is wired.
type fileBackend struct {
	path string
}

func (b *fileBackend) load() (map[string]Session, error) {
	data, err := os.ReadFile(b.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]Session{}, nil
		}
		return nil, fmt.Errorf("read ndsctl state %s: %w", b.path, err)
	}
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return map[string]Session{}, nil
	}
	sessions := map[string]Session{}
	if err := json.Unmarshal([]byte(trimmed), &sessions); err != nil {
		return nil, fmt.Errorf("parse ndsctl state %s: %w", b.path, err)
	}
	return sessions, nil
}

func (b *fileBackend) save(sessions map[string]Session) error {
	if err := os.MkdirAll(filepath.Dir(b.path), 0o755); err != nil {
		return fmt.Errorf("create ndsctl state dir: %w", err)
	}
	encoded, err := json.Marshal(sessions)
	if err != nil {
		return fmt.Errorf("encode ndsctl state: %w", err)
	}
	tmp := b.path + ".tmp"
	if err := os.WriteFile(tmp, append(encoded, '\n'), 0o644); err != nil {
		return fmt.Errorf("write ndsctl state: %w", err)
	}
	if err := os.Rename(tmp, b.path); err != nil {
		return fmt.Errorf("replace ndsctl state: %w", err)
	}
	return nil
}

// withLock runs fn while holding an exclusive advisory lock on the state file,
// so a read-modify-write from one child cannot be lost to another.
func (b *fileBackend) withLock(fn func() error) error {
	if err := os.MkdirAll(filepath.Dir(b.path), 0o755); err != nil {
		return fmt.Errorf("create ndsctl state dir: %w", err)
	}
	lock, err := os.OpenFile(b.path+".lock", os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return fmt.Errorf("open ndsctl state lock: %w", err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("lock ndsctl state: %w", err)
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	return fn()
}

func (b *fileBackend) Authorize(mac, ip string, now int64) error {
	key := normalizeMAC(mac)
	return b.withLock(func() error {
		sessions, err := b.load()
		if err != nil {
			return err
		}
		if existing, ok := sessions[key]; ok {
			// Idempotent re-auth: keep the paid-for clock and counters, only
			// refresh the address the host handed out.
			existing.IP = ip
			existing.Active = now
			sessions[key] = existing
			return b.save(sessions)
		}
		sessions[key] = Session{
			IP:     ip,
			Added:  now,
			Active: now,
			Token:  tokenFor(key),
		}
		return b.save(sessions)
	})
}

func (b *fileBackend) Deauthorize(mac string) error {
	key := normalizeMAC(mac)
	return b.withLock(func() error {
		sessions, err := b.load()
		if err != nil {
			return err
		}
		if _, ok := sessions[key]; !ok {
			return nil
		}
		delete(sessions, key)
		return b.save(sessions)
	})
}

func (b *fileBackend) Lookup(mac string) (Session, bool, error) {
	sessions, err := b.load()
	if err != nil {
		return Session{}, false, err
	}
	session, ok := sessions[normalizeMAC(mac)]
	return session, ok, nil
}

func (b *fileBackend) List() (map[string]Session, error) {
	return b.load()
}

// normalizeMAC is the key form of a MAC everywhere the shim stores or looks one
// up: lower-case, trimmed. The wire form echoes the caller's spelling.
func normalizeMAC(mac string) string {
	return strings.ToLower(strings.TrimSpace(mac))
}

// sortedMACs orders a session map's keys so the client list and the per-client
// ids are stable run to run.
func sortedMACs(sessions map[string]Session) []string {
	macs := make([]string, 0, len(sessions))
	for mac := range sessions {
		macs = append(macs, mac)
	}
	sort.Strings(macs)
	return macs
}

func parseInt64(s string) (int64, error) {
	var v int64
	_, err := fmt.Sscanf(strings.TrimSpace(s), "%d", &v)
	return v, err
}
