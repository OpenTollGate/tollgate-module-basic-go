// Consent policy for automatic upstream selection.
//
// TollGate can join two very different kinds of upstream:
//
//   - a TollGate peer (`!<brand>-<suffix>`), which is the payment network and
//     is meant to be discovered and joined automatically;
//   - a venue/hotel/airport AP, which is someone else's network. Joining one
//     is a decision the operator should have made for that specific network,
//     not a side effect of a stronger signal.
//
// This file implements the second half as an explicit manifest: open networks
// with no `pre-accepted` entry are observed and never joined. `per-session`
// entries also never self-consent, because the terms are per visit and a
// router cannot accept them for the user.
package wireless_gateway_manager

import (
	"strings"

	"github.com/sirupsen/logrus"
)

const (
	// ConsentPreAccepted means the operator has reviewed the venue terms and
	// allows this AP to be joined automatically.
	ConsentPreAccepted = "pre-accepted"
	// ConsentPerSession means the terms must be accepted for each visit; the
	// manager must not join or submit a portal for it automatically.
	ConsentPerSession = "per-session"
)

// ConsentEntry is one operator-approved network.
type ConsentEntry struct {
	// Match is an exact SSID or a prefix ending in '*'. SSIDs are
	// case-sensitive.
	Match string
	// TOSURL records the terms the operator reviewed. It is advisory and is
	// never fetched automatically.
	TOSURL string
	// Consent is "pre-accepted" or "per-session".
	Consent string
	// Recipe names the portal recipe to use, if any. It is advisory until the
	// recipe engine lands.
	Recipe string
}

// consentEntryFor returns the first manifest entry matching ssid. Exact
// matches win only by position; callers should not rely on overlap.
func consentEntryFor(ssid string, manifest []ConsentEntry) *ConsentEntry {
	ssid = strings.TrimSpace(ssid)
	if ssid == "" {
		return nil
	}
	for i := range manifest {
		match := strings.TrimSpace(manifest[i].Match)
		if match == "" {
			continue
		}
		if strings.HasSuffix(match, "*") {
			if strings.HasPrefix(ssid, strings.TrimSuffix(match, "*")) {
				return &manifest[i]
			}
			continue
		}
		if ssid == match {
			return &manifest[i]
		}
	}
	return nil
}

// consentAllowsNetwork decides whether an automatic scan may join net.
//
// The second return value is a short reason for logs and tests.
func consentAllowsNetwork(ssid, encryption string, manifest []ConsentEntry, allowAnyOpen bool) (bool, string) {
	if hasTollGateSSID(ssid) {
		return true, "tollgate-peer"
	}
	if strings.HasPrefix(strings.TrimSpace(ssid), "Private-") {
		return true, "private-ssid"
	}

	enc := strings.ToLower(strings.TrimSpace(encryption))
	// A saved encrypted network already required the operator to supply the
	// key; that is the consent. An empty/unknown encryption value is left
	// alone for compatibility, because older scanner paths did not populate
	// it. Explicit open values are the ones this policy acts on.
	if enc != "" && enc != "none" && enc != "open" {
		return true, "encrypted-saved"
	}
	if enc == "" {
		return true, "unknown-encryption"
	}

	if allowAnyOpen {
		return true, "allow-any-open"
	}

	entry := consentEntryFor(ssid, manifest)
	if entry == nil {
		return false, "no-consent-entry"
	}
	switch strings.ToLower(strings.TrimSpace(entry.Consent)) {
	case ConsentPreAccepted:
		return true, "pre-accepted"
	case ConsentPerSession:
		return false, "per-session"
	default:
		return false, "unknown-consent-value"
	}
}

// consentAllows logs why a candidate was skipped and returns the decision.
func (um *UpstreamManager) consentAllows(net NetworkInfo) bool {
	ok, reason := consentAllowsNetwork(net.SSID, net.Encryption, um.config.ConsentManifest, um.config.AllowAnyOpen)
	if !ok {
		logger.WithFields(logrus.Fields{
			"ssid":   net.SSID,
			"bssid":  net.BSSID,
			"reason": reason,
		}).Debug("Skipping upstream: not in the consent manifest")
	}
	return ok
}
