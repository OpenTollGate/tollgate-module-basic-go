package main

import (
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The `ui links` contract, pinned at the seam the consumer defines. The board
// SPA's admin/src/lib/ui-links.ts (portal @ 752df98) is the consumer of record
// and its parseUiLinks/UiLink/UiLinks types define the JSON this command must
// emit — url is "" (not null) when a UI has no usable link, ports are strings,
// and reason is set exactly then. D5 of
// docs/architecture/default-ui-and-entry-port-decision.md is the spec; this
// file pins what actually crosses the wire.

// redirectUIPaths points the entry_ui config and marker files at a throw-away
// root, in the shape of redirectSSLPaths.
func redirectUIPaths(t *testing.T) (root string) {
	t.Helper()
	root = t.TempDir()
	oldConfig, oldMarker := entryUIConfigPath, entryUIMarkerPath
	entryUIConfigPath = filepath.Join(root, "config.json")
	entryUIMarkerPath = filepath.Join(root, "entry-ui-mapping")
	t.Cleanup(func() {
		entryUIConfigPath, entryUIMarkerPath = oldConfig, oldMarker
	})
	return root
}

// writeEntryUIFixture writes the config.json and marker files the resolver
// reads. An empty configured or marker means "file absent".
func writeEntryUIFixture(t *testing.T, root, configured, marker string) {
	t.Helper()
	_ = root
	if configured != "" {
		body := `{"entry_ui":` + jsonString(t, configured) + `}`
		if err := os.WriteFile(entryUIConfigPath, []byte(body), 0644); err != nil {
			t.Fatalf("write config fixture: %v", err)
		}
	}
	if marker != "" {
		if err := os.WriteFile(entryUIMarkerPath, []byte(marker+"\n"), 0644); err != nil {
			t.Fatalf("write marker fixture: %v", err)
		}
	}
}

// placeholderCertPEM is the OpenWrt image's placeholder identity (subject
// CN=OpenWrt, SAN DNS:OpenWrt) — readable, valid, and covering no router.
func placeholderCertPEM(t *testing.T) string {
	t.Helper()
	return writeTestCertPEM(t, &x509.Certificate{
		Subject:  pkix.Name{CommonName: "OpenWrt"},
		DNSNames: []string{"OpenWrt"},
	})
}

func jsonString(t *testing.T, s string) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal %q: %v", s, err)
	}
	return string(b)
}

// boardUCI is the live board mapping as the mode-aware writers leave it
// (measured on the bench MT3000, 2026-10-08): uhttpd.admin owns the entry
// pair, uhttpd.main the secondary one, both presenting the provisioned
// identity.
func boardUCI(certPath string) map[string]string {
	return map[string]string{
		"system.@system[0].hostname": testRouterHostname,
		"network.lan.ipaddr":         testRouterLANIPWithPrefix,
		"uhttpd.admin.listen_http":   "0.0.0.0:8080 [::]:8080",
		"uhttpd.admin.listen_https":  "0.0.0.0:443 [::]:443",
		"uhttpd.admin.cert":          certPath,
		"uhttpd.main.listen_http":    "0.0.0.0:8090 [::]:8090",
		"uhttpd.main.listen_https":   "0.0.0.0:8443 [::]:8443",
		"uhttpd.main.cert":           certPath,
	}
}

// luciUCI is the legacy mapping: uhttpd.main owns the entry pair.
func luciUCI(certPath string) map[string]string {
	return map[string]string{
		"system.@system[0].hostname": testRouterHostname,
		"network.lan.ipaddr":         testRouterLANIPWithPrefix,
		"uhttpd.main.listen_http":    "0.0.0.0:8080 [::]:8080",
		"uhttpd.main.listen_https":   "0.0.0.0:443 [::]:443",
		"uhttpd.main.cert":           certPath,
		"uhttpd.admin.listen_http":   "0.0.0.0:8090 [::]:8090",
		"uhttpd.admin.listen_https":  "0.0.0.0:8443 [::]:8443",
		"uhttpd.admin.cert":          certPath,
	}
}

// ------------------------------------------------------------- the mapping

// TestEntryUIModeResolvesLikeTheSetupPath pins the resolver against the rule
// 99-tollgate-setup's ensure_entry_ui applies: config declares intent (D1,
// fail-closed default board), the marker gates board on the mode-aware portal
// writer (D4), and luci always passes through.
func TestEntryUIModeResolvesLikeTheSetupPath(t *testing.T) {
	for _, tc := range []struct {
		name       string
		configured string
		marker     string
		want       string
	}{
		{"board with the marker: applied", "board", "board", "board"},
		{"board without the marker: repairs to luci (D4)", "board", "", "luci"},
		{"board with a stale marker: repairs to luci", "board", "luci", "luci"},
		{"luci passes through without a marker", "luci", "", "luci"},
		{"luci ignores a board marker", "luci", "board", "luci"},
		{"missing config.json: board default, no marker → luci", "", "", "luci"},
		{"missing config.json: board default with the marker → board", "", "board", "board"},
		{"garbage value: board default", "garbage", "board", "board"},
		{"empty value: board default", "", "board", "board"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := redirectUIPaths(t)
			writeEntryUIFixture(t, root, tc.configured, tc.marker)
			if got := entryUIMode(); got != tc.want {
				t.Errorf("entryUIMode() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestReadEntryUIConfiguredFailsClosedToBoard is D1's invariant 6: no value —
// missing file, missing key, unparseable body, wrong type — may resolve to
// "bind no admin listener".
func TestReadEntryUIConfiguredFailsClosedToBoard(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{"missing key", `{"metric":"bytes"}`},
		{"unparseable body", `not json at all`},
		{"empty file", ``},
		{"null value", `{"entry_ui":null}`},
		{"non-string value", `{"entry_ui":1}`},
		{"empty string value", `{"entry_ui":""}`},
		{"unknown value", `{"entry_ui":"admin"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			redirectUIPaths(t)
			if err := os.WriteFile(entryUIConfigPath, []byte(tc.body), 0644); err != nil {
				t.Fatalf("write fixture: %v", err)
			}
			if got := readEntryUIConfigured(); got != "board" {
				t.Errorf("readEntryUIConfigured() = %q, want the board default", got)
			}
		})
	}
}

// --------------------------------------------------------------- the links

// TestUILinksBoardMapping: with entry_ui=board applied and a covering
// identity on both listeners, BOTH UIs carry an HTTPS link, the board's on
// the implicit :443 and LuCI's on the explicit :8443 — the exact pair the
// SPA's example renders.
func TestUILinksBoardMapping(t *testing.T) {
	root := redirectUIPaths(t)
	writeEntryUIFixture(t, root, "board", "board")
	cert := writeTestCertPEM(t, selfSignedTemplate(testRouterHostname, testRouterLANIPWithPrefix))
	stubUCI(t, boardUCI(cert))

	got := computeUILinks()
	if got.EntryUI != "board" {
		t.Fatalf("entry_ui = %q, want board", got.EntryUI)
	}
	board, luci := got.Links.Board, got.Links.Luci

	if board.Port != "8080" || board.TLSPort != "443" {
		t.Errorf("board ports = %s/%s, want 8080/443", board.Port, board.TLSPort)
	}
	if board.URL != "https://"+testRouterHostname+".lan/" {
		t.Errorf("board url = %q, want https://%s.lan/ (:443 implicit)", board.URL, testRouterHostname)
	}
	if board.Reason != "" {
		t.Errorf("board reason = %q, want empty alongside a url", board.Reason)
	}

	if luci.Port != "8090" || luci.TLSPort != "8443" {
		t.Errorf("luci ports = %s/%s, want 8090/8443", luci.Port, luci.TLSPort)
	}
	if luci.URL != "https://"+testRouterHostname+".lan:8443/" {
		t.Errorf("luci url = %q, want https://%s.lan:8443/", luci.URL, testRouterHostname)
	}
	if luci.Reason != "" {
		t.Errorf("luci reason = %q, want empty alongside a url", luci.Reason)
	}
}

// TestUILinksLuciMapping: with entry_ui=luci the SAME state answers with the
// pairs swapped — LuCI on the entry pair, the board on the secondary one.
func TestUILinksLuciMapping(t *testing.T) {
	root := redirectUIPaths(t)
	writeEntryUIFixture(t, root, "luci", "")
	cert := writeTestCertPEM(t, selfSignedTemplate(testRouterHostname, testRouterLANIPWithPrefix))
	stubUCI(t, luciUCI(cert))

	got := computeUILinks()
	if got.EntryUI != "luci" {
		t.Fatalf("entry_ui = %q, want luci", got.EntryUI)
	}
	if got.Links.Luci.Port != "8080" || got.Links.Luci.TLSPort != "443" {
		t.Errorf("luci ports = %s/%s, want 8080/443", got.Links.Luci.Port, got.Links.Luci.TLSPort)
	}
	if got.Links.Luci.URL != "https://"+testRouterHostname+".lan/" {
		t.Errorf("luci url = %q, want the entry URL", got.Links.Luci.URL)
	}
	if got.Links.Board.Port != "8090" || got.Links.Board.TLSPort != "8443" {
		t.Errorf("board ports = %s/%s, want 8090/8443", got.Links.Board.Port, got.Links.Board.TLSPort)
	}
	if got.Links.Board.URL != "https://"+testRouterHostname+".lan:8443/" {
		t.Errorf("board url = %q, want the secondary URL", got.Links.Board.URL)
	}
}

// TestUILinksNoCoveringIdentityIsNoLink: the fail-closed half of D5. The
// OpenWrt image's placeholder certificate does not cover this router, so a
// listener presenting it must yield url:"" plus a reason — never a link that
// dies on a browser's certificate error. This is the exact state the bench
// assertion 13 probes with `tollgate ssl remove`.
func TestUILinksNoCoveringIdentityIsNoLink(t *testing.T) {
	root := redirectUIPaths(t)
	writeEntryUIFixture(t, root, "board", "board")
	stubUCI(t, boardUCI(placeholderCertPEM(t)))

	got := computeUILinks()
	for _, ui := range []struct {
		name string
		link uiLink
	}{
		{"board", got.Links.Board},
		{"luci", got.Links.Luci},
	} {
		if ui.link.URL != "" {
			t.Errorf("%s url = %q, want empty: a placeholder identity must not be linked", ui.name, ui.link.URL)
		}
		if ui.link.Reason == "" {
			t.Errorf("%s reason is empty: a refused link must say why (the SPA surfaces it)", ui.name)
		}
		if !strings.Contains(ui.link.Reason, "cover") {
			t.Errorf("%s reason = %q, want it to name the identity problem", ui.name, ui.link.Reason)
		}
	}
}

// TestUILinksNoTLSListenerIsNoLink: a UI whose HTTPS port nothing lists (the
// pre-provisioning state of the board's :8443 as shipped) is url:"" with the
// listener named in the reason — the SPA's example reason verbatim.
func TestUILinksNoTLSListenerIsNoLink(t *testing.T) {
	root := redirectUIPaths(t)
	writeEntryUIFixture(t, root, "board", "board")
	cert := writeTestCertPEM(t, selfSignedTemplate(testRouterHostname, testRouterLANIPWithPrefix))
	seed := boardUCI(cert)
	// LuCI's section exists but serves HTTP only, as a half-converged or
	// deliberately-cleartext secondary pair would.
	delete(seed, "uhttpd.main.listen_https")
	stubUCI(t, seed)

	got := computeUILinks()
	if got.Links.Board.URL == "" {
		t.Errorf("board url empty (%q): the entry listener is live and covering", got.Links.Board.Reason)
	}
	if got.Links.Luci.URL != "" {
		t.Errorf("luci url = %q, want empty: nothing listens on :8443", got.Links.Luci.URL)
	}
	if got.Links.Luci.Reason != "no HTTPS listener on 8443" {
		t.Errorf("luci reason = %q, want %q", got.Links.Luci.Reason, "no HTTPS listener on 8443")
	}
}

// --------------------------------------------------------------- JSON shape

// TestUILinksJSONShapeMatchesTheSPA is the byte-level contract: the JSON this
// command emits must marshal to exactly the fields parseUiLinks reads, with
// url/reason as ""-able strings, not omitempty-nulls.
func TestUILinksJSONShapeMatchesTheSPA(t *testing.T) {
	root := redirectUIPaths(t)
	writeEntryUIFixture(t, root, "board", "board")
	cert := writeTestCertPEM(t, selfSignedTemplate(testRouterHostname, testRouterLANIPWithPrefix))
	// The board is linked; LuCI is not (placeholder on its listener) — one
	// payload exercising both halves of the contract.
	seed := boardUCI(cert)
	seed["uhttpd.main.cert"] = placeholderCertPEM(t)
	stubUCI(t, seed)

	out, err := captureStdout(t, func() error {
		jsonOutput = true
		defer func() { jsonOutput = false }()
		return uiLinksCmd.RunE(uiLinksCmd, nil)
	})
	if err != nil {
		t.Fatalf("ui links --json: %v", err)
	}

	// Field order and names, as the SPA's tolerant parse sees them.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		t.Fatalf("output is not one JSON object: %v\n%s", err, out)
	}
	for _, key := range []string{"entry_ui", "links"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("payload is missing %q", key)
		}
	}
	var links map[string]json.RawMessage
	if err := json.Unmarshal(raw["links"], &links); err != nil {
		t.Fatalf("links is not an object: %v", err)
	}
	for _, key := range []string{"board", "luci"} {
		if _, ok := links[key]; !ok {
			t.Errorf("links is missing %q", key)
		}
	}
	for _, key := range []string{"board", "luci"} {
		var link map[string]json.RawMessage
		if err := json.Unmarshal(links[key], &link); err != nil {
			t.Fatalf("links.%s is not an object: %v", key, err)
		}
		for _, field := range []string{"url", "port", "tls_port", "reason"} {
			if _, ok := link[field]; !ok {
				t.Errorf("links.%s is missing %q — the SPA reads it with asString, so it must be present even when empty", key, field)
			}
		}
	}

	// And the round-trip: what the SPA would hold after parseUiLinks.
	var parsed uiLinksResult
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("round-trip: %v", err)
	}
	if parsed.Links.Luci.URL != "" || parsed.Links.Luci.Reason == "" {
		t.Errorf("luci = {url:%q reason:%q}: a refused link must be empty url + non-empty reason", parsed.Links.Luci.URL, parsed.Links.Luci.Reason)
	}
	if parsed.Links.Board.URL == "" || parsed.Links.Board.Reason != "" {
		t.Errorf("board = {url:%q reason:%q}: a live link must be a url + empty reason", parsed.Links.Board.URL, parsed.Links.Board.Reason)
	}
}

// TestUILinksPlainOutputIsNotJSON: without --json the answer is for an
// operator; the JSON contract must not leak into it by accident.
func TestUILinksPlainOutputIsNotJSON(t *testing.T) {
	root := redirectUIPaths(t)
	writeEntryUIFixture(t, root, "board", "board")
	cert := writeTestCertPEM(t, selfSignedTemplate(testRouterHostname, testRouterLANIPWithPrefix))
	stubUCI(t, boardUCI(cert))

	out, err := captureStdout(t, func() error {
		return uiLinksCmd.RunE(uiLinksCmd, nil)
	})
	if err != nil {
		t.Fatalf("ui links: %v", err)
	}
	if strings.Contains(out, "{") {
		t.Errorf("plain output looks like JSON: %q", out)
	}
	if !strings.Contains(out, "entry_ui: board") {
		t.Errorf("plain output does not name the mapping: %q", out)
	}
}

// -------------------------------------------------------------- CLI surface

// TestUICmdRegistersLinks pins the surface the rpcd plugin and the docs name:
// `tollgate ui links` must exist as a command, and the plugin invokes it with
// the --json flag spelled exactly this way.
func TestUICmdRegistersLinks(t *testing.T) {
	if uiCmd.Name() != "ui" {
		t.Errorf("ui command name = %q", uiCmd.Name())
	}
	if uiLinksCmd.Name() != "links" {
		t.Fatalf("subcommand name = %q, want links", uiLinksCmd.Name())
	}
	if uiLinksCmd.Parent() != uiCmd {
		t.Errorf("links' parent = %q, want ui", uiLinksCmd.Parent().Name())
	}
	if uiCmd.Root() != rootCmd {
		t.Errorf("ui's root = %q, want tollgate", uiCmd.Root().Name())
	}
	// --json is a persistent root flag; the plugin calls
	// `tollgate ui links --json`, which only works if it parses at this level.
	flag := uiLinksCmd.Flags().Lookup("json")
	if flag == nil {
		flag = uiLinksCmd.InheritedFlags().Lookup("json")
	}
	if flag == nil {
		t.Error("the --json flag the rpcd plugin passes is not visible on `ui links`")
	}
}
