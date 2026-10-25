package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The tool surface is only real if the SERVICE actually serves the command it
// names. This test reads the server's own dispatch source and asserts every
// tool maps to a command the socket answers — the bug it exists for was found
// by audit: four announced tools (logs, service_start/stop/restart) named
// commands that `src/cli/server.go`'s processCommand does not implement, because
// the CLI runs those LOCALLY (logread / /etc/init.d) rather than over the
// socket. An announced tool that always errors is worse than an absent one.
//
// It parses source rather than calling the server because the server needs a
// live config manager, connector and scanner; the dispatch table is the
// contract, and a regex over it fails loudly when someone renames a command.

// serverDispatchDir is the sibling module holding the socket server.
const serverDispatchDir = "../../cli"

func readServerSource(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(serverDispatchDir)
	if err != nil {
		t.Fatalf("resolve server dir: %v", err)
	}
	var sb strings.Builder
	for _, name := range []string{"server.go", "network.go", "config.go"} {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("read %s: %v (the guard must run from the module dir)", name, err)
		}
		sb.Write(b)
	}
	return sb.String()
}

// topLevelCommands returns the `case "x":` labels of processCommand's switch.
func topLevelCommands(src string) map[string]bool {
	out := map[string]bool{}
	start := strings.Index(src, "func (s *CLIServer) processCommand(")
	if start < 0 {
		return out
	}
	body := src[start:]
	if end := strings.Index(body, "\n}\n"); end > 0 {
		body = body[:end]
	}
	re := regexp.MustCompile(`case "([a-z0-9-]+)":`)
	for _, m := range re.FindAllStringSubmatch(body, -1) {
		out[m[1]] = true
	}
	return out
}

func TestEveryToolMapsToAServedCommand(t *testing.T) {
	src := readServerSource(t)
	served := topLevelCommands(src)
	if len(served) == 0 {
		t.Fatal("parsed no commands from processCommand — the guard is vacuous")
	}

	for _, tool := range ToolSurface() {
		if !served[tool.CLICommand] {
			t.Errorf("tool %q maps to command %q, which processCommand does NOT serve — the tool would always error. "+
				"Served: %v", tool.Name, tool.CLICommand, sortedKeys(served))
		}
	}
}

// The subcommand spelling matters as much as the top-level verb: the server
// answers `upstream list-upstream`, not `upstream list`. Naming the wrong
// subcommand produces the same always-errors tool.
func TestSubcommandSpellingsMatchTheServer(t *testing.T) {
	src := readServerSource(t)

	want := map[string]string{
		"upstream_list":  `case "list-upstream":`,
		"upstream_known": `case "known":`,
		"upstream_scan":  `case "scan":`,
	}
	for _, tool := range ToolSurface() {
		needle, ok := want[tool.Name]
		if !ok {
			continue
		}
		if !strings.Contains(src, needle) {
			t.Errorf("tool %q expects %s in the server, which is absent", tool.Name, needle)
		}
		// And the tool's own args must be the spelling that switch matches.
		if len(tool.CLIArgs) == 0 {
			t.Errorf("tool %q has no args but the server needs a subcommand", tool.Name)
			continue
		}
		sub := tool.CLIArgs[0]
		if !strings.Contains(src, `case "`+sub+`":`) {
			t.Errorf("tool %q sends subcommand %q which the server does not match", tool.Name, sub)
		}
	}
}

// Commands the CLI executes LOCALLY must never appear as tools: the socket
// server does not implement them, so a tool naming one can only ever error.
// This is the regression guard for the exact defect found by audit.
func TestLocalOnlyCommandsAreNotTools(t *testing.T) {
	localOnly := []string{"logs", "start", "stop", "restart", "drain", "fund", "ssl"}
	byName := map[string]Tool{}
	for _, tool := range ToolSurface() {
		byName[tool.Name] = tool
	}
	for _, cmd := range localOnly {
		for _, tool := range ToolSurface() {
			if tool.CLICommand == cmd {
				t.Errorf("tool %q names locally-executed command %q — the socket server does not serve it", tool.Name, cmd)
			}
		}
	}
	for _, banned := range []string{"logs", "service_start", "service_stop", "service_restart", "drain", "fund"} {
		if _, ok := byName[banned]; ok {
			t.Errorf("tool %q is announced but cannot work over the socket", banned)
		}
	}
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
