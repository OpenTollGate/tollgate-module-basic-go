package wireless_gateway_manager

import (
	"os"
	"regexp"
	"strings"
)

// Captive-SSID recognition.
//
// The prefix is a ZERO-DEPENDENCY FALLBACK: it decides which AP is worth the
// signed kind-10021 probe on :2121, and nothing more. The probe remains the
// authority; a spoofed prefix only costs one failed probe.
//
// The set is no longer compiled in. A router always recognises `TollGate-`
// (the protocol default), its own brand from `/etc/tollgate/brand`, and any
// extra prefixes an operator or downstream skin puts in
// `/etc/tollgate/ssid-prefixes` (one per line). A custom skin therefore adds
// its name by writing a file, without a PR to this repository.
const (
	// defaultCaptivePrefix is always recognised, even with no brand file and
	// no extra-prefix file. It is the name every existing TollGate router
	// carries and every third-party matcher knows.
	defaultCaptivePrefix = "TollGate"

	// ssidDecoration is the optional leading '!' the captive-SSID writer may
	// emit (#706: 0x21 sorts before digits and letters). It is PRESENTATION
	// ONLY: recognition strips at most ONE, so `!!TollGate-…` is not a
	// TollGate name.
	ssidDecoration = "!"

	// brandFile is written by the package/installer. TOLLGATE_BRAND_FILE is
	// the test/portable override.
	brandFile = "/etc/tollgate/brand"

	// prefixFile carries extra prefixes (one per line, `#` comments). It is
	// deliberately plain text: the shell side (99-tollgate-setup) reads the
	// same file, and a router cannot be assumed to have a JSON parser.
	prefixFile = "/etc/tollgate/ssid-prefixes"
)

// brandToken is the local brand, lowercased, or "" for the default. The file
// is a single alphanumeric token; an unreadable/invalid file means "default",
// never an error.
func brandToken() string {
	path := strings.TrimSpace(os.Getenv("TOLLGATE_BRAND_FILE"))
	if path == "" {
		path = brandFile
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	token := strings.ToLower(strings.TrimSpace(string(data)))
	if !brandTokenRe.MatchString(token) || token == "tollgate" {
		return ""
	}
	return token
}

var brandTokenRe = regexp.MustCompile(`^[a-z0-9]+$`)

// ssidPrefixRe bounds an extra prefix. Hyphens are excluded because the
// device code is separated by a hyphen; a prefix with one would make the
// `<prefix>-<code>` split ambiguous. 3..32 chars keeps a typo from matching
// half the neighbourhood.
var ssidPrefixRe = regexp.MustCompile(`^[A-Za-z0-9_]{3,32}$`)

// ssidPrefixes returns the effective recognition set, in precedence order:
// the default, the local brand, then the operator's extra file.
func ssidPrefixes() []string {
	out := []string{defaultCaptivePrefix}
	if brand := brandToken(); brand != "" {
		out = append(out, brand)
	}
	out = append(out, readSSIDPrefixFile()...)
	return dedupeFold(out)
}

// readSSIDPrefixFile parses the optional extra-prefix file. Invalid lines are
// ignored: a bad file must never break discovery.
func readSSIDPrefixFile() []string {
	path := strings.TrimSpace(os.Getenv("TOLLGATE_SSID_PREFIXES_FILE"))
	if path == "" {
		path = prefixFile
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []string
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, ssidDecoration))
		line = strings.TrimSuffix(line, "-")
		if !ssidPrefixRe.MatchString(line) {
			continue
		}
		out = append(out, line)
	}
	return out
}

// dedupeFold removes case-insensitive duplicates while preserving order.
func dedupeFold(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, v := range in {
		key := strings.ToLower(v)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, v)
	}
	return out
}

// hasTollGateSSID reports whether ssid starts with a recognised captive
// prefix, case-insensitively, with at most one leading '!' stripped. It is
// the single Go reader of the selection contract (reseller-mode upstream
// selection and the vendor-element score heuristic); the shell side's
// code_from_name reads the same files.
func hasTollGateSSID(ssid string) bool {
	name := strings.TrimPrefix(ssid, ssidDecoration)
	for _, prefix := range ssidPrefixes() {
		candidate := prefix + "-"
		if len(name) >= len(candidate) && strings.EqualFold(name[:len(candidate)], candidate) {
			return true
		}
	}
	return false
}
