package valve

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/sirupsen/logrus"
)

// The NoDogSplash CLIENT LIST (`ndsctl json`, no argument) is the only way this
// module can ask the enforcement layer a question its own bookkeeping cannot
// answer: "which clients do YOU hold, and in which state?".
//
// Every other read this package has is per-MAC (`ndsctl json <mac>`) and is
// therefore useless for the one drift a module restart creates: after the
// process is replaced, the module holds no session at all while NoDogSplash
// still holds every client it had authorised, with an open gate and nothing
// metering it.
//
// Measured shape (bench MT3000, pre17, 2026-09-26), abbreviated:
//
//	{
//	  "client_length": 3,
//	  "clients": {
//	    "a8:a0:92:a5:39:7a": {
//	      "id": 2, "ip": "192.168.1.124", "mac": "a8:a0:92:a5:39:7a",
//	      "added": 0, "active": 1790419507, "duration": 0,
//	      "token": "d4fceff0", "state": "Authenticated",
//	      "downloaded": 2367, "avg_down_speed": 0.00,
//	      "uploaded": 64, "avg_up_speed": 0.00
//	    }
//	  }
//	}
//
// Two properties of that payload matter to the caller:
//
//   - the `clients` map is keyed by MAC and each record also carries its own
//     `mac`; the two agree on the router, and the record's own field wins when
//     they do not (the map key is the fallback).
//   - `state` is "Authenticated" for a client that HAS internet access and
//     "Preauthenticated" for one that is merely known to NoDogSplash (the state
//     a client reaches by being physically present, before it authenticates —
//     it cannot pass traffic past the captive portal in that state, so it is not
//     an access leak).

// ndsctlClientEntry is one client record inside `ndsctl json`'s client list.
type ndsctlClientEntry struct {
	IP         string `json:"ip"`
	MAC        string `json:"mac"`
	State      string `json:"state"`
	Downloaded uint64 `json:"downloaded"`
	Uploaded   uint64 `json:"uploaded"`
}

// ndsctlClientList is the wire form of `ndsctl json` with no argument.
type ndsctlClientList struct {
	ClientLength int                          `json:"client_length"`
	Clients      map[string]ndsctlClientEntry `json:"clients"`
}

// ClientRecord is the module's view of one client NoDogSplash holds, as reported
// by the client list. It is read-only information about the ENFORCEMENT layer,
// never about this module's own bookkeeping.
type ClientRecord struct {
	MAC        string
	IP         string
	State      string
	Downloaded uint64
	Uploaded   uint64
}

// Authorised reports whether NoDogSplash holds this client in the state that
// carries internet access. The comparison is case-insensitive because the
// capitalisation of a printed state is not a contract, and a state this package
// does not recognise is deliberately NOT treated as authorisation: guessing
// "authorised" leaks unmetered access, while guessing "not authorised" only
// leaves a client that NoDogSplash says nothing about alone.
func (c ClientRecord) Authorised() bool {
	return strings.EqualFold(strings.TrimSpace(c.State), "Authenticated")
}

// ListClients asks NoDogSplash for its whole client list (`ndsctl json`, read
// only) and returns the records in a stable, MAC-sorted order so an operator
// report and a test both read the same way.
//
// An empty answer is not an error: NoDogSplash holding no client is a real
// answer (a router nobody has joined yet), indistinguishable in the payload
// from `{}`. A list the module could not READ, by contrast, is an error and is
// never reported as "no clients" — the caller must not act on a fact it does not
// have.
func ListClients() ([]ClientRecord, error) {
	ndsctlMutex.Lock()
	output, err := runNdsctl("json")
	ndsctlMutex.Unlock()

	if err != nil {
		if interruption, ended := ndsctlInterruptionOf(err); ended {
			// Ended by the module (its own deadline, or a shutdown), already
			// reported with its outcome: reading the client list is not an
			// ndsctl refusal and must not be escalated as one.
			logger.WithFields(ndsctlInterruptionFields(interruption)).Debug("ndsctl json (client list) was ended by the module; the client list stays UNKNOWN")
			return nil, fmt.Errorf("failed to read the ndsctl client list: %w", err)
		}
		logger.WithFields(logrus.Fields{
			"error": err,
		}).Error("Error executing ndsctl json for the client list")
		return nil, fmt.Errorf("failed to read the ndsctl client list: %w", err)
	}

	return parseNdsctlClientList(output)
}

// parseNdsctlClientList turns one `ndsctl json` payload into records. It is
// separate from the invocation so the wire form can be tested against the
// payload measured on the router, without a child process.
func parseNdsctlClientList(output string) ([]ClientRecord, error) {
	trimmed := strings.TrimSpace(output)
	if trimmed == "" || trimmed == "{}" {
		// NoDogSplash answers an empty object when it holds no client: a real
		// answer, not a failure to read one.
		return nil, nil
	}

	var list ndsctlClientList
	if err := json.Unmarshal([]byte(trimmed), &list); err != nil {
		logger.WithFields(logrus.Fields{
			"error":  err,
			"output": output,
		}).Error("Error parsing the ndsctl client list")
		return nil, fmt.Errorf("failed to parse the ndsctl client list: %w", err)
	}

	records := make([]ClientRecord, 0, len(list.Clients))
	for key, entry := range list.Clients {
		macAddress := strings.TrimSpace(entry.MAC)
		if macAddress == "" {
			macAddress = strings.TrimSpace(key)
		}
		if macAddress == "" {
			logger.WithField("output", output).Warn("The ndsctl client list carries a record with no MAC address at all; it is skipped (there is nothing to act on)")
			continue
		}

		records = append(records, ClientRecord{
			MAC:        macAddress,
			IP:         strings.TrimSpace(entry.IP),
			State:      strings.TrimSpace(entry.State),
			Downloaded: entry.Downloaded,
			Uploaded:   entry.Uploaded,
		})
	}

	// A payload whose own count disagrees with what it carries is worth saying
	// once: it means the record set is not the whole truth, and the caller is
	// about to decide from it.
	if list.ClientLength != 0 && list.ClientLength != len(records) {
		logger.WithFields(logrus.Fields{
			"client_length": list.ClientLength,
			"parsed":        len(records),
		}).Warn("The ndsctl client list reports a different number of clients than it carries")
	}

	sort.Slice(records, func(i, j int) bool { return records[i].MAC < records[j].MAC })
	return records, nil
}

// AuthorisedClients returns the MAC addresses NoDogSplash currently holds in the
// state that carries internet access, MAC-sorted. It is the question the startup
// reconciliation asks; a client in any other state (preauthenticated, or a state
// this package does not know) is not returned, because it is not holding access.
func AuthorisedClients() ([]string, error) {
	records, err := ListClients()
	if err != nil {
		return nil, err
	}

	macs := make([]string, 0, len(records))
	for _, record := range records {
		if record.Authorised() {
			macs = append(macs, record.MAC)
		}
	}
	return macs, nil
}
