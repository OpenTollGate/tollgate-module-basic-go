package main

import (
	"encoding/json"
	"strings"
)

// Redaction at the CVM boundary.
//
// WHY A CHOKE POINT AND NOT PER-TOOL CHECKS: the audit that found the
// private_status passphrase leak also showed why a per-tool or per-schema check
// cannot work here. src/cli redacts config.json secrets via
// redactSecretFields, which walks the config SCHEMA (exactly one field is
// marked Secret). But `private_status` does not read config.json at all — it
// reads the live WPA key out of UCI (src/cli/network.go:100) and marshals it as
// "password". The schema-driven contract structurally cannot see it, and the
// tool's own description ("SSID, enabled") did not even mention the third
// field. So every response crossing this boundary is scrubbed here, once, by
// key name — a new tool cannot forget to opt in.

// secretKeys are response keys whose VALUES must never cross the CVM boundary.
// Matched case-insensitively on the key name, at any depth.
//
// Deliberately a denylist of NAMES rather than an allowlist of fields: the
// responses are the CLI's own structs, which change shape over time, and a
// missed field here is a credential leak. The cost of over-redacting (a
// non-secret field sharing a name) is a less useful response, which is the
// right way to fail.
var secretKeys = map[string]bool{
	"password":          true, // private network WPA passphrase (UCI wireless.*.key)
	"privatekey":        true, // owned identity secret
	"private_key":       true, // config.json private network passphrase
	"nsec":              true,
	"mnemonic":          true,
	"seed":              true,
	"root_password":     true,
	"wifi_password":     true,
	"token":             true, // Cashu token — a bearer instrument
	"cashu_token":       true,
	"lightning_address": false, // a payout address is not a secret; keep it
	"authorization":     true,
	"api_key":           true,
	"secret":            true,
}

// RedactResponse blanks every secret-keyed value in a CLIResponse's Data,
// reporting that a value was withheld rather than silently dropping the field
// (the "secret_set" idiom src/cli already uses): a caller must be able to tell
// "there is a credential here" from "there is no credential".
//
// The response is round-tripped through JSON rather than walked with
// reflection: the Data payload is `interface{}` and may be a struct, a map, or
// a slice of either, and JSON is the shape that actually crosses the wire, so
// scrubbing it is scrubbing what the caller receives.
func RedactResponse(resp CLIResponse) CLIResponse {
	if resp.Data == nil {
		return resp
	}
	raw, err := json.Marshal(resp.Data)
	if err != nil {
		// Cannot inspect it ⇒ cannot vouch for it. Withhold rather than pass
		// through something unexamined.
		resp.Data = map[string]interface{}{
			"redacted": true,
			"reason":   "response could not be inspected for secrets and was withheld",
		}
		return resp
	}
	var tree interface{}
	if err := json.Unmarshal(raw, &tree); err != nil {
		resp.Data = map[string]interface{}{
			"redacted": true,
			"reason":   "response could not be inspected for secrets and was withheld",
		}
		return resp
	}
	resp.Data = redactValue(tree)
	return resp
}

func redactValue(v interface{}) interface{} {
	switch t := v.(type) {
	case map[string]interface{}:
		for k, val := range t {
			if secretKeys[strings.ToLower(k)] {
				t[k] = ""
				// Make the withholding visible. A bare "" is indistinguishable
				// from "not configured", which is how a leak gets replaced by
				// a bug report nobody can act on.
				if _, exists := t[k+"_set"]; !exists {
					t[k+"_set"] = true
				}
				continue
			}
			t[k] = redactValue(val)
		}
		return t
	case []interface{}:
		for i := range t {
			t[i] = redactValue(t[i])
		}
		return t
	default:
		return v
	}
}
