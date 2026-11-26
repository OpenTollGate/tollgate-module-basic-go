package config_manager

import (
	"fmt"
	"net"
	"strings"
)

// LNURLCashDisclosureVersion is the operator-disclosure version the shipped
// binary knows how to present. It is the ONLY value accepted in
// lnurlcash.acknowledgement: an acknowledgement recorded under a different
// version means the operator consented to different words than the ones this
// build shows, so the rail stays off rather than treating consent as a boolean.
//
// Bump this (and rewrite the disclosure in
// docs/architecture/lnurlcash-rail-decision.md) whenever the trust statement
// materially changes: what the operator is told, whose key can spend, what is
// persisted, or where the note is sent.
const LNURLCashDisclosureVersion = "lnurlcash-issuer-redeem-v1"

// CanonicalLNURLCashIssuer canonicalises an issuer identifier to the domain
// form LUD-25 commits to: lowercase, A-label (punycode) handled by the caller's
// URL parser, no scheme, no port, no userinfo, no trailing dot or slash.
//
// The gate reaches the issuer over the network, so the allowlist entry and the
// host it actually dials must be the same string; anything the canonicaliser
// cannot reduce to a bare host is refused instead of guessed at.
func CanonicalLNURLCashIssuer(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", fmt.Errorf("lnurlcash issuer is empty")
	}
	// Reject anything carrying a scheme, path, query or userinfo: an allowlist
	// entry must name a host, not a URL, or "https://evil.example@mint.example"
	// and "mint.example" become indistinguishable to a reader.
	if strings.ContainsAny(s, "/?@#") || strings.Contains(s, "://") {
		return "", fmt.Errorf("lnurlcash issuer %q must be a bare host, not a URL", raw)
	}
	host := strings.ToLower(strings.TrimSuffix(s, "."))
	if host == "" {
		return "", fmt.Errorf("lnurlcash issuer %q is empty after canonicalisation", raw)
	}
	if strings.Contains(host, ":") {
		return "", fmt.Errorf("lnurlcash issuer %q must not carry a port", raw)
	}
	// An IP literal is not a domain: LUD-25 canonicalises `domain` as a host in
	// A-label form, and the gate dials exactly what the allowlist names. Allowing
	// an address literal would let the allowlist double as an SSRF target list,
	// so it is refused rather than silently accepted.
	if net.ParseIP(host) != nil {
		return "", fmt.Errorf("lnurlcash issuer %q must be a domain name, not an IP literal", raw)
	}
	for _, r := range host {
		if r > 127 || !(r == '.' || r == '-' || r == '_' || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')) {
			return "", fmt.Errorf("lnurlcash issuer %q must be lowercase ASCII (A-label) after canonicalisation", raw)
		}
	}
	if !strings.Contains(host, ".") {
		return "", fmt.Errorf("lnurlcash issuer %q must be a fully qualified domain", raw)
	}
	return host, nil
}

// LNURLCashActivation reports whether the LNURLcash redemption rail may serve
// requests, and if not, why. Refusal is the default in every partial state:
//
//   - disabled (the zero value, and every upgraded router) → off;
//   - enabled but with no acknowledgement, or one for different words → off;
//   - enabled and acknowledged but with no allowlisted issuer → off, because
//     the gate must reach the issuer and an empty allowlist means it has no
//     issuer it is willing to reach;
//   - any allowlist entry that is not a bare canonical host → off, rather than
//     silently dropping it (an operator who wrote a URL believes that issuer is
//     allowed).
//
// The returned reason is operator-facing text; it never contains a note, spend
// or bearer secret.
func (c *Config) LNURLCashActivation() (bool, string) {
	if c == nil {
		return false, "no configuration"
	}
	if !c.LNURLCash.Enabled {
		return false, "lnurlcash.enabled is false (the rail is opt-in)"
	}
	if c.LNURLCash.Acknowledgement != LNURLCashDisclosureVersion {
		if c.LNURLCash.Acknowledgement == "" {
			return false, "lnurlcash.acknowledgement is empty — the operator disclosure has not been accepted"
		}
		return false, fmt.Sprintf("lnurlcash.acknowledgement names %q but this build presents %q", c.LNURLCash.Acknowledgement, LNURLCashDisclosureVersion)
	}
	if len(c.LNURLCash.Issuers) == 0 {
		return false, "lnurlcash.issuers is empty — the rail needs at least one allowlisted issuer domain"
	}
	for _, raw := range c.LNURLCash.Issuers {
		if _, err := CanonicalLNURLCashIssuer(raw); err != nil {
			return false, err.Error()
		}
	}
	if c.LNURLCash.MaxNoteMSat == 0 {
		return false, "lnurlcash.max_note_msat is 0 — no note could ever be redeemed"
	}
	return true, ""
}

// LNURLCashIssuerAllowed reports whether domain is an allowlisted issuer, and
// returns the canonical host on success. Both sides go through the same
// canonicaliser so a note cannot smuggle a different host past a textual
// comparison (case, trailing dot, default port).
func (c *Config) LNURLCashIssuerAllowed(domain string) (string, bool) {
	if c == nil {
		return "", false
	}
	want, err := CanonicalLNURLCashIssuer(domain)
	if err != nil {
		return "", false
	}
	for _, raw := range c.LNURLCash.Issuers {
		allowed, err := CanonicalLNURLCashIssuer(raw)
		if err != nil {
			continue
		}
		if allowed == want {
			return want, true
		}
	}
	return "", false
}
