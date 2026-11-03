package main

// contract.go — the exact ndsctl surface the module drives (LINUX-HOST-5).
//
// The TollGate module (src/valve) shells out to a binary named `ndsctl` and
// parses its stdout. On a router that binary is NoDogSplash/opennds' own
// ndsctl; on a plain Linux host there is no NoDogSplash, so this shim is
// installed at /usr/libexec/tollgate/ndsctl and answers the SAME three shapes
// the module uses. Nothing in src/valve changes: the shim is a drop-in.
//
// The three shapes (src/valve/valve.go, src/valve/nds_clients.go):
//
//	ndsctl auth <mac>    authorize a client      (valve.go authorizeMAC)
//	ndsctl deauth <mac>  deauthorize a client    (valve.go deauthorizeMAC)
//	ndsctl json <mac>    one client's record     (valve.go GetClientStats,
//	                                              CheckClientState)
//	ndsctl json          the whole client list   (nds_clients.go ListClients)
//
// The single-client record is EXACTLY these twelve members, in this order:
//
//	id, ip, mac, added, active, duration, token, state,
//	downloaded, uploaded, avg_down_speed, avg_up_speed
//
// `downloaded` and `uploaded` are KILOBYTES: GetClientStats multiplies by 1024,
// so a shim that answered bytes would over-charge every metered session by
// 1024x. The two speeds are formatted with two decimals ("0.00") because that
// is what opennds prints; the module unmarshals them into float64 either way.
//
// Two exit-status facts the module depends on:
//
//   - a MAC the host does not know AT ALL (no /tmp/dhcp.leases entry and no
//     /proc/net/arp neighbour) is answered with a NON-ZERO exit. authorizeMAC
//     retries its ndsctl auth 5 times at 400 ms (authMaxAttempts /
//     authRetryDelay in valve.go) exactly so the two-router autopay race —
//     "the client's session is not registered yet" — converges. A shim that
//     exits 0 for a MAC that is not even on the host would spend the retry
//     budget on nothing; a shim that exits 0 for EVERY MAC makes the retry
//     loop dead code.
//   - a MAC the host DOES know but which carries no authorized session is a
//     real answer: `json` prints `{}` (the empty object) and exits 0, which
//     GetClientStats reads as "client not found" and CheckClientState reads as
//     "registered: false" — the definitive not-authenticated answer, not an
//     error to fail open on.
//
// A CLI usage error (an unknown verb, or a missing MAC argument) also exits
// non-zero, so `ndsctl bogus` can never be mistaken for a quiet success.

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// The three verbs the module drives. Anything else is a usage error.
const (
	verbAuth   = "auth"
	verbDeauth = "deauth"
	verbJSON   = "json"
)

// knownVerbs is the closed set of accepted verbs.
var knownVerbs = []string{verbAuth, verbDeauth, verbJSON}

// stateAuthenticated is the one `state` a client the shim has authorized
// carries. CheckClientState and ClientRecord.Authorised compare it
// case-insensitively; the module names it with this capitalisation.
const stateAuthenticated = "Authenticated"

// emptyObject is the answer to `ndsctl json <mac>` for a MAC the host knows but
// which carries no authorized session — and to `ndsctl json` when the host
// holds no client at all. The module treats it as a definitive negative, never
// as a read failure, so it is printed verbatim with a trailing newline and an
// exit of 0.
const emptyObject = "{}"

// ClientStats is one client record on the ndsctl wire form. It is marshalled by
// MarshalJSON rather than by encoding/json's struct order so the member order
// and the two-decimal speed formatting are pinned by the bytes, not by a
// reflection detail. The field set is exactly the twelve members above.
type ClientStats struct {
	ID           int
	IP           string
	MAC          string
	Added        int64
	Active       int64
	Duration     int64
	Token        string
	State        string
	Downloaded   uint64
	Uploaded     uint64
	AvgDownSpeed float64
	AvgUpSpeed   float64
}

// MarshalJSON renders the record in the contract's member order, with the two
// speeds at two decimals. Every string goes through encoding/json so a MAC or
// IP can never break the object.
func (c ClientStats) MarshalJSON() ([]byte, error) {
	var b strings.Builder
	b.WriteByte('{')
	writeMemberInt(&b, "id", int64(c.ID))
	writeMemberString(&b, "ip", c.IP)
	writeMemberString(&b, "mac", c.MAC)
	writeMemberInt(&b, "added", c.Added)
	writeMemberInt(&b, "active", c.Active)
	writeMemberInt(&b, "duration", c.Duration)
	writeMemberString(&b, "token", c.Token)
	writeMemberString(&b, "state", c.State)
	writeMemberInt(&b, "downloaded", int64(c.Downloaded))
	writeMemberInt(&b, "uploaded", int64(c.Uploaded))
	writeMemberFloat2(&b, "avg_down_speed", c.AvgDownSpeed)
	writeMemberFloat2(&b, "avg_up_speed", c.AvgUpSpeed)
	b.WriteByte('}')
	return []byte(b.String()), nil
}

func writeMemberInt(b *strings.Builder, name string, v int64) {
	if b.Len() > 1 {
		b.WriteByte(',')
	}
	encoded, _ := json.Marshal(name)
	b.Write(encoded)
	b.WriteByte(':')
	b.WriteString(strconv.FormatInt(v, 10))
}

func writeMemberString(b *strings.Builder, name, v string) {
	if b.Len() > 1 {
		b.WriteByte(',')
	}
	encodedName, _ := json.Marshal(name)
	encodedValue, _ := json.Marshal(v)
	b.Write(encodedName)
	b.WriteByte(':')
	b.Write(encodedValue)
}

func writeMemberFloat2(b *strings.Builder, name string, v float64) {
	if b.Len() > 1 {
		b.WriteByte(',')
	}
	encoded, _ := json.Marshal(name)
	b.Write(encoded)
	b.WriteByte(':')
	b.WriteString(strconv.FormatFloat(v, 'f', 2, 64))
}

// clientList is the wire form of `ndsctl json` with no argument, the payload
// nds_clients.go parses. `clients` is keyed by MAC and each record repeats its
// own `mac`; the parser trusts the record's field and falls back to the map
// key, so both are emitted.
type clientList struct {
	ClientLength int                    `json:"client_length"`
	Clients      map[string]ClientStats `json:"clients"`
}

// MarshalJSON renders the list deterministically. encoding/json sorts map keys,
// so the list is stable across runs — which is what makes it fixture-checkable.
func (l clientList) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		ClientLength int                    `json:"client_length"`
		Clients      map[string]ClientStats `json:"clients"`
	}{ClientLength: l.ClientLength, Clients: l.Clients})
}

// formatStats renders one record plus the module's trailing newline.
func formatStats(c ClientStats) (string, error) {
	encoded, err := c.MarshalJSON()
	if err != nil {
		return "", fmt.Errorf("render ndsctl record for %s: %w", c.MAC, err)
	}
	return string(encoded) + "\n", nil
}

// formatList renders the whole-client-list payload plus the trailing newline.
func formatList(l clientList) (string, error) {
	encoded, err := l.MarshalJSON()
	if err != nil {
		return "", fmt.Errorf("render ndsctl client list: %w", err)
	}
	return string(encoded) + "\n", nil
}
