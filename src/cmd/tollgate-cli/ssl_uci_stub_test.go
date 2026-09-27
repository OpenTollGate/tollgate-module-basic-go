package main

import (
	"os"
	"path/filepath"
	"testing"
)

// stubUCIState is the state file the uci stub below reads and writes. The CLI
// shells out to `uci` for every config read and write, so a shim on PATH is the
// seam the coverage/derived-value tests use — the same seam the uci-defaults
// tests use on the shell side (tests/uci-defaults-*_test.sh).
var stubUCIState string

const stubUCIScript = `#!/bin/sh
# Minimal uci stand-in: one "key=value" line per option. Only the verbs this
# package's ssl paths use are implemented; everything else is a no-op that
# succeeds, like the real uci on an unknown-but-harmless call.
state="${TOLLGATE_TEST_UCI_STATE:?}"
q=0
[ "${1:-}" = "-q" ] && { q=1; shift; }
cmd="${1:-}"
shift || true
case "$cmd" in
    get)
        val=$(grep -F -- "$1=" "$state" 2>/dev/null | head -n1 | cut -d= -f2-)
        if [ -z "$val" ]; then
            [ "$q" = 1 ] || echo "uci: Entry not found" >&2
            exit 1
        fi
        printf '%s\n' "$val"
        ;;
    set)
        key="${1%%=*}"
        grep -v -F -- "$key=" "$state" > "$state.tmp" 2>/dev/null
        mv "$state.tmp" "$state"
        printf '%s\n' "$1" >> "$state"
        ;;
    add_list|delete|del_list|add|show|export|revert|commit) : ;;
    *) : ;;
esac
exit 0
`

// stubUCI puts the uci shim first on PATH and seeds it with the given options.
// It returns the state file path.
func stubUCI(t *testing.T, seed map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	state := filepath.Join(dir, "uci.state")
	script := filepath.Join(dir, "uci")
	if err := os.WriteFile(script, []byte(stubUCIScript), 0755); err != nil {
		t.Fatalf("write uci stub: %v", err)
	}
	seedUCIState(t, state, seed)
	stubUCIState = state
	t.Setenv("TOLLGATE_TEST_UCI_STATE", state)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return state
}

// seedUCIState rewrites the stub's config; an empty seed is a router with none
// of the options set.
func seedUCIState(t *testing.T, state string, seed map[string]string) {
	t.Helper()
	if err := os.WriteFile(state, nil, 0644); err != nil {
		t.Fatalf("seed uci state: %v", err)
	}
	for key, value := range seed {
		appendUCIState(t, state, key, value)
	}
}

func appendUCIState(t *testing.T, state, key, value string) {
	t.Helper()
	f, err := os.OpenFile(state, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0644)
	if err != nil {
		t.Fatalf("open uci state: %v", err)
	}
	defer f.Close()
	if _, err := f.WriteString(key + "=" + value + "\n"); err != nil {
		t.Fatalf("write uci state: %v", err)
	}
}

// stubUCIValue reads an option back out of the stub's state — what `uci get`
// would have printed after the code under test wrote it.
func stubUCIValue(t *testing.T, key string) (string, error) {
	t.Helper()
	data, err := os.ReadFile(stubUCIState)
	if err != nil {
		return "", err
	}
	prefix := key + "="
	for _, line := range splitLines(string(data)) {
		if len(line) >= len(prefix) && line[:len(prefix)] == prefix {
			return line[len(prefix):], nil
		}
	}
	return "", os.ErrNotExist
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}
