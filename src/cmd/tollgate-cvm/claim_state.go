package main

import (
	"strings"
)

// MaintainerFromConfigPayload extracts the maintainer npub from a `config get`
// CLIResponse payload.
//
// It accepts both the flat shape and the {"config": {...}} wrapper, because the
// server's config_get returns `{"config": <redacted config>, "identities": …,
// "secret_set": …}` (src/cli/config.go:92) — the flat form is accepted too so a
// future flattening does not silently stop the daemon from finding its owner.
//
// A non-string value yields "" rather than a coerced string: a number or object
// where an npub belongs is a config error, and coercing it would produce a
// maintainer identity that matches nothing while looking configured.
func MaintainerFromConfigPayload(data interface{}) string {
	m, ok := data.(map[string]interface{})
	if !ok {
		return ""
	}
	if v, ok := m["maintainer_npub"].(string); ok {
		return strings.TrimSpace(v)
	}
	if inner, ok := m["config"].(map[string]interface{}); ok {
		if v, ok := inner["maintainer_npub"].(string); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
