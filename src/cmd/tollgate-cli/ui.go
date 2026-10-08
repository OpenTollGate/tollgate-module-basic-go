package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

// The entry_ui mapping's two sources, mirroring 99-tollgate-setup's ENTRY_UI_*
// variables. Package-level paths (the same seam as sslDir) so the tests can
// point them at a fixture instead of a live router.
var (
	entryUIConfigPath = "/etc/tollgate/config.json"
	entryUIMarkerPath = "/etc/tollgate/entry-ui-mapping"
)

// uiLink is one UI's cross-link, byte-for-byte the shape the board SPA
// consumes (portal admin/src/lib/ui-links.ts → interface UiLink): url is ""
// — not null — when this UI has no usable HTTPS link, and reason then says
// why. The SPA renders no anchor for an empty url and surfaces the reason.
type uiLink struct {
	URL     string `json:"url"`
	Port    string `json:"port"`
	TLSPort string `json:"tls_port"`
	Reason  string `json:"reason"`
}

// uiLinksResult is the JSON contract of `tollgate ui links --json` and of the
// rpcd plugin's ui_links method, mirroring UiLinks in ui-links.ts. The SPA
// parses it tolerantly (parseUiLinks) and never builds a URL of its own.
type uiLinksResult struct {
	EntryUI string `json:"entry_ui"`
	Links   struct {
		Board uiLink `json:"board"`
		Luci  uiLink `json:"luci"`
	} `json:"links"`
}

var uiCmd = &cobra.Command{
	Use:   "ui",
	Short: "Admin UI mapping and cross-links",
	Long:  `Report which admin UI (board or LuCI) owns the entry pair, and the HTTPS cross-links between them.`,
}

var uiLinksCmd = &cobra.Command{
	Use:   "links",
	Short: "Show admin UI cross-links",
	Long: `Report the router's admin-UI mapping and the HTTPS cross-links it supports.

Which UI answers :8080/:443 and which answers :8090/:8443 depends on the live
entry_ui mapping, so a link to the OTHER admin UI is a router answer, never a
guess: the board SPA renders exactly what this prints and renders nothing when
a UI has no usable link, showing the reason instead (D5 of
docs/architecture/default-ui-and-entry-port-decision.md).

A link is present only when that UI has a live TLS listener whose certificate
covers this router — the same fail-closed predicate as 'ssl covers'. Admin
cross-links are HTTPS-only (D6).`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		result := computeUILinks()
		if jsonOutput {
			return printJSON(result)
		}
		printUILinksHuman(result)
		return nil
	},
}

func init() {
	uiCmd.AddCommand(uiLinksCmd)
	rootCmd.AddCommand(uiCmd)
}

// readEntryUIConfigured reads config.json's .entry_ui with the fail-closed
// default D1 mandates: a missing file, a missing key, an unparseable file and
// a non-string value ALL resolve to "board" — the switch fails toward the
// operator's decision, never toward "bind no admin listener at all". This is
// the same rule 99-tollgate-setup's entry_ui_configured applies.
func readEntryUIConfigured() string {
	data, err := os.ReadFile(entryUIConfigPath)
	if err != nil {
		return "board"
	}
	var cfg map[string]interface{}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return "board"
	}
	raw, ok := cfg["entry_ui"]
	if !ok {
		return "board"
	}
	value, ok := raw.(string)
	if !ok {
		return "board"
	}
	switch value {
	case "board", "luci":
		return value
	default:
		return "board"
	}
}

// entryUIMode resolves the LIVE mapping, the same rule 99-tollgate-setup's
// ensure_entry_ui applies: config.json declares the intent, and the marker
// file records whether the mode-aware portal writer (92) has actually applied
// the board mapping. Honour board only when the marker says so; otherwise
// repair to luci so no admin UI is left unbound (D4).
func entryUIMode() string {
	configured := readEntryUIConfigured()
	if configured != "board" {
		return "luci"
	}
	if strings.TrimSpace(fileRead(entryUIMarkerPath)) != "board" {
		return "luci"
	}
	return "board"
}

// uiPortPair returns the HTTP and TLS port of the named UI under the given
// mapping, matching the port helpers in 99-tollgate-setup (uhttpd_main_* /
// board_*): the ports do not change, only which UI answers them.
func uiPortPair(ui, mode string) (httpPort, tlsPort string) {
	entryIsBoard := mode == "board"
	uiIsBoard := ui == "board"
	if entryIsBoard == uiIsBoard {
		// board mode + board, or luci mode + luci: this UI owns the entry pair.
		return "8080", "443"
	}
	// The other UI owns the secondary pair.
	return "8090", "8443"
}

// uiLinkHost returns the host the admin UIs are reached by, so the CLI can
// build the URL the SPA is forbidden from guessing: the configured hostname's
// .lan alias (the name dnsmasq serves on the LAN), falling back to the LAN IP.
func uiLinkHost() string {
	hostname := strings.TrimSpace(uciGetOrEmpty("system.@system[0].hostname"))
	if hostname != "" {
		return hostname + ".lan"
	}
	if ip := parseLanIP(uciGetOrEmpty("network.lan.ipaddr")); ip != "" {
		return ip
	}
	return ""
}

// sectionListensHTTPS reports whether the named uhttpd section really lists
// tlsPort in its listen_https — tlsListenPortOwner's "uhttpd.main" fallback
// is not evidence of a listener.
func sectionListensHTTPS(section, tlsPort string) bool {
	for _, listen := range uciGetList(section + ".listen_https") {
		if strings.HasSuffix(strings.TrimSpace(listen), ":"+tlsPort) {
			return true
		}
	}
	return false
}

// uiURL builds the HTTPS URL for an admin UI on tlsPort. Port 443 is the
// scheme default and is left implicit (https://<host>/); any other port is
// explicit (https://<host>:<port>/).
func uiURL(host, tlsPort string) string {
	if tlsPort == "443" {
		return fmt.Sprintf("https://%s/", host)
	}
	return fmt.Sprintf("https://%s:%s/", host, tlsPort)
}

// computeUILink answers for ONE UI: an https:// URL when that UI has a live
// TLS listener whose identity covers this router (D5 condition i), or "" with
// a non-empty reason when it does not. Every input is read from live state —
// the section that owns the port, the certificate it is configured to serve,
// and the router's own names.
func computeUILink(tlsPort, host string) (url, reason string) {
	owner := tlsListenPortOwner(tlsPort)
	if !sectionListensHTTPS(owner, tlsPort) {
		return "", fmt.Sprintf("no HTTPS listener on %s", tlsPort)
	}
	certPath := strings.TrimSpace(uciGetOrEmpty(owner + ".cert"))
	if certPath == "" {
		return "", fmt.Sprintf("no certificate configured on %s", owner)
	}
	covers, coverReason := certCoversRouter(certPath)
	if !covers {
		return "", coverReason
	}
	if host == "" {
		return "", "cannot determine the router's hostname or LAN IP"
	}
	return uiURL(host, tlsPort), ""
}

// computeUILinks derives the whole cross-link answer from the router's live
// state. Nothing here is hardcoded: the mapping comes from config.json plus
// the marker, the ports from the mapping, and each link from the listener and
// certificate that actually answer the port.
func computeUILinks() uiLinksResult {
	mode := entryUIMode()
	host := uiLinkHost()

	var result uiLinksResult
	result.EntryUI = mode
	result.Links.Board.Port, result.Links.Board.TLSPort = uiPortPair("board", mode)
	result.Links.Luci.Port, result.Links.Luci.TLSPort = uiPortPair("luci", mode)
	result.Links.Board.URL, result.Links.Board.Reason = computeUILink(result.Links.Board.TLSPort, host)
	result.Links.Luci.URL, result.Links.Luci.Reason = computeUILink(result.Links.Luci.TLSPort, host)
	return result
}

// printUILinksHuman renders the answer for an operator at a shell. The JSON
// shape is the contract; this is a convenience view of the same facts.
func printUILinksHuman(r uiLinksResult) {
	fmt.Printf("entry_ui: %s\n", r.EntryUI)
	for _, ui := range []struct {
		name string
		link uiLink
	}{
		{"board", r.Links.Board},
		{"luci", r.Links.Luci},
	} {
		fmt.Printf("\n%s:\n", ui.name)
		fmt.Printf("  ports: http %s / https %s\n", ui.link.Port, ui.link.TLSPort)
		if ui.link.URL != "" {
			fmt.Printf("  url:   %s\n", ui.link.URL)
		} else {
			fmt.Printf("  url:   (none) — %s\n", ui.link.Reason)
		}
	}
}
