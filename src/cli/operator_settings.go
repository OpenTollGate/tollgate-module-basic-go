package cli

// Operator-settable network settings: declared intent in config.json, converged
// onto the router here.
//
// Two of the module's settings cannot be honoured by anything that reads
// /etc/tollgate/config.json directly, because the components that need them do
// not read that file at all:
//
//   - the private network's SSID, passphrase and encryption live in UCI
//     (/etc/config/wireless), which is what hostapd is started from;
//   - "which network may reach the administration surfaces" is an nftables
//     property (fw4 reads /etc/nftables.d/*.nft), and the shipped guards
//     (31-admin-board-not-guest-reachable.nft, 32-luci-not-guest-reachable.nft)
//     hardcode the captive bridge by name.
//
// So config.json holds the operator's declared value and this file is the ONE
// writer that turns it into runtime state: UCI for the credentials, one
// generated, module-owned nftables fragment for the scope. It is
// compare-and-converge, not write-always: on a router whose runtime already
// matches config.json every step reports `unchanged` and nothing on the wire
// moves — which is what makes it safe to call from the daemon's start path,
// where a procd respawn would otherwise reload the firewall and the wireless
// every few seconds.
//
// Scope of the authority: a field whose value is EMPTY is not an instruction,
// it is "keep what the router has". 99-tollgate-setup mints the first private
// SSID and passphrase, and an upgrade must never revert a router's private
// network to an empty value, so the applier only ever writes a declared,
// non-empty value.
//
// See docs/architecture/lan-port-management-bridge-decision.md (D9-D12) for
// the decision, the rejected alternatives and the assertions.

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"github.com/sirupsen/logrus"
)

// The admin_access values, spelled exactly as the config schema's enum.
const (
	AdminAccessPrivate  = "br-private"
	AdminAccessMgmt     = "br-mgmt"
	AdminAccessBoth     = "both"
	AdminAccessLoopback = "loopback-only"

	// The bridge the wired LAN ports get from the br-mgmt decision (D1). The
	// value is a name, not an existence claim: on a router built before that
	// writer shipped the name matches no interface and every rule naming it is
	// inert.
	mgmtBridge = "br-mgmt"
	// The bridge the private SSID is on (99-tollgate-setup setup_private_network).
	privateBridge = "br-private"

	// adminScopeFragmentName is generated, never shipped: the package owns no
	// file here, so `apk upgrade` cannot restore a stale scope and the file's
	// absence means exactly "the default scope, which adds no rule".
	adminScopeFragmentName = "33-admin-access-scope.nft"

	// The administration surfaces, as one port set. The guards own the split
	// (31-*.nft = board :8090/:8443, 32-*.nft = LuCI :8080/:443) and the
	// captive-bridge drop; this fragment owns only which OTHER network may
	// reach them, so it deliberately does not restate the groupings — a future
	// release that moves a port between uhttpd sections must not also have to
	// edit this file.
	adminPortList = "{ 443, 8080, 8090, 8443 }"

	// privateRadio0Section/privateRadio1Section are the two wifi-ifaces the
	// private network is served from. Both are written when both exist: the
	// board's WiFi page used to edit one section at a time, which is how the
	// two radios drifted apart.
	privateRadio0Section = "wireless.private_radio0"
	privateRadio1Section = "wireless.private_radio1"

	// passphraseMinLen/passphraseMaxLen are WPA2-PSK's bounds (8-63 ASCII
	// characters), the same check `tollgate network private set-password`
	// applies. A passphrase outside them makes hostapd refuse the interface.
	passphraseMinLen = 8
	passphraseMaxLen = 63
	ssidMaxLen       = 32
)

var (
	opLogger = logrus.WithField("module", "cli/operator_settings")

	// nftablesDir is where fw4 reads its includes from. A var so tests can
	// point the writer at a temp dir; production never reassigns it.
	nftablesDir = "/etc/nftables.d"

	// ifacePresent reports whether a network interface exists on this host. It
	// is the check that keeps the br-mgmt scope from locking an operator out of
	// a router where that bridge does not exist (see planAdminScope).
	ifacePresent = func(name string) bool {
		_, err := net.InterfaceByName(name)
		return err == nil
	}

	// privateRadioSections is the write order for the credential convergence.
	privateRadioSections = []string{privateRadio0Section, privateRadio1Section}

	// privateEncryptionAlgorithms mirrors the schema enum. Kept here too because
	// a wholesale `config save` does not run per-key schema validation, and an
	// unknown encryption string is a management interface that will not come up.
	privateEncryptionAlgorithms = []string{"psk2+ccmp", "psk2+tkip+ccmp", "psk-mixed+ccmp"}
)

// SettingResult reports what happened to one declared setting. It is the shape
// every surface (CLI, board) renders, so "the config file says X but the router
// did Y" is never silent.
type SettingResult struct {
	Setting string `json:"setting"`
	Status  string `json:"status"` // applied | unchanged | skipped | refused | failed
	Detail  string `json:"detail,omitempty"`
	Warning string `json:"warning,omitempty"`
}

const (
	statusApplied   = "applied"
	statusUnchanged = "unchanged"
	statusSkipped   = "skipped"
	statusRefused   = "refused"
	statusFailed    = "failed"
)

// AdminScopePlan is the pure decision for one admin_access value: which
// interfaces lose the administration ports. Pure on purpose — the plan is
// testable without a router, and the renderer is testable on top of it.
type AdminScopePlan struct {
	// Scope is the admin_access value this plan came from.
	Scope string `json:"scope"`
	// DropIfaces are interfaces whose administration ports are dropped.
	DropIfaces []string `json:"drop_ifaces,omitempty"`
	// DropAllButLoopback is the one value that tightens globally: every
	// interface that is not `lo` loses the administration ports, including
	// interfaces this module has never heard of. It cannot be expressed as a
	// list of names.
	DropAllButLoopback bool `json:"drop_all_but_loopback,omitempty"`
	// Noop marks a scope that adds no rule at all. The default (both) is a
	// Noop so that shipping this feature changes nothing on the wire until an
	// operator asks for something else.
	Noop bool `json:"noop,omitempty"`
}

// planAdminScope turns an admin_access value into a plan, or refuses it.
//
// Refusals are deliberate and are the whole safety story of this setting:
//
//   - an unknown value is refused rather than defaulted, because a typo in
//     config.json must not silently widen (or narrow) administration;
//   - `br-mgmt` is refused while that bridge does not exist. Naming it drops
//     the private SSID from the administration path, and on a router whose
//     wired ports are still on the captive bridge (the LLM-bridge writer has
//     not run, or a sysupgrade -n regenerated the port list) there would be no
//     network left that reaches the board: the operator's cable would be on the
//     guest bridge, which is dropped by design. Refusing keeps a working
//     management path; the config file still records what he asked for, and the
//     refusal names the prerequisite.
func planAdminScope(scope string, hasIface func(string) bool) (AdminScopePlan, error) {
	switch scope {
	case AdminAccessBoth:
		return AdminScopePlan{Scope: scope, Noop: true}, nil
	case AdminAccessPrivate:
		// Allowing br-private means dropping br-mgmt. Emitting the rule on a
		// router that has no br-mgmt is inert but correct by intent: when the
		// wired bridge appears it is already scoped.
		return AdminScopePlan{Scope: scope, DropIfaces: []string{mgmtBridge}}, nil
	case AdminAccessMgmt:
		if !hasIface(mgmtBridge) {
			return AdminScopePlan{}, fmt.Errorf(
				"admin_access=%s refused: the %s bridge does not exist on this router yet, and this value drops the private SSID (%s) from the administration path — the wired LAN ports must be moved to %s first (lan-port-management-bridge decision D1), otherwise no network would reach the board. The router keeps its previous scope; config.json keeps your value",
				AdminAccessMgmt, mgmtBridge, privateBridge, mgmtBridge)
		}
		return AdminScopePlan{Scope: scope, DropIfaces: []string{privateBridge}}, nil
	case AdminAccessLoopback:
		return AdminScopePlan{Scope: scope, DropAllButLoopback: true}, nil
	default:
		return AdminScopePlan{}, fmt.Errorf(
			"admin_access=%q is not a known value (expected one of %s, %s, %s, %s): leaving the router's current scope in place",
			scope, AdminAccessBoth, AdminAccessPrivate, AdminAccessMgmt, AdminAccessLoopback)
	}
}

// renderAdminScopeFragment renders the nftables include for a plan. Empty is a
// valid rendering: it means "this scope adds no rule".
//
// The rules run at hook input priority -1, the same hook and priority the
// shipped guards use, and they are drops. A drop in an early base chain cannot
// be undone by a later accept, so naming a network here really does remove it
// from the administration path even where another fragment (br-mgmt's own
// allow list, D4) accepts those ports.
func renderAdminScopeFragment(plan AdminScopePlan) string {
	if plan.Noop {
		return ""
	}

	var rules []string
	if plan.DropAllButLoopback {
		rules = append(rules,
			fmt.Sprintf("\tmeta nfproto ipv4 iifname != \"lo\" tcp dport %s counter drop", adminPortList),
			fmt.Sprintf("\tmeta nfproto ipv6 iifname != \"lo\" tcp dport %s counter drop", adminPortList),
		)
	} else if len(plan.DropIfaces) > 0 {
		match := renderIfaceMatch(plan.DropIfaces)
		rules = append(rules,
			fmt.Sprintf("\tmeta nfproto ipv4 %s tcp dport %s counter drop", match, adminPortList),
			fmt.Sprintf("\tmeta nfproto ipv6 %s tcp dport %s counter drop", match, adminPortList),
		)
	}

	header := fmt.Sprintf(`#!/usr/sbin/nft -f

# GENERATED FILE — do not edit. Written by tollgate-wrt from the
# "admin_access" field of /etc/tollgate/config.json, and rewritten by
# `+"`tollgate config apply`"+` and at every daemon start.
#
# admin_access = %s
#
# WHAT THIS FILE DOES. It removes the administration surfaces (the board on
# :8090/:8443 and LuCI on :8080/:443) from every network the configured scope
# does not name. It does not add reach to anything: the accept side stays where
# it always was (fw4's lan-zone input policy, the private zone's input ACCEPT,
# and loopback).
#
# WHY IT LIVES HERE AND NOT IN 31-*.nft / 32-*.nft. Those two fragments keep the
# captive bridge off the admin ports, by name, unconditionally — that is an
# invariant, not a preference (docs/architecture/luci-https-pre-auth-reachability-decision.md).
# This one carries the operator's choice about the *other* networks, so the
# guest bridge is deliberately absent from these rules: its drop is already
# written, once, in 31-*.nft and 32-*.nft, and a second copy would double-count
# the same packet in the operator's diagnostics.
#
# The absence of this file means the default scope (admin_access = both), which
# adds no rule at all.
`, plan.Scope)

	if len(rules) == 0 {
		return ""
	}

	return header + `
chain admin_access_scope {
	type filter hook input priority -1; policy accept

` + strings.Join(rules, "\n") + "\n}\n"
}

// renderIfaceMatch renders one or more interface names as an nftables
// iifname match: a bare name for one, a brace list for several (the same
// shape 30-backend-firewall.nft uses for its br-lan/lo exception).
func renderIfaceMatch(ifaces []string) string {
	if len(ifaces) == 1 {
		return fmt.Sprintf("iifname %q", ifaces[0])
	}
	quoted := make([]string, 0, len(ifaces))
	for _, iface := range ifaces {
		quoted = append(quoted, fmt.Sprintf("%q", iface))
	}
	return "iifname { " + strings.Join(quoted, ", ") + " }"
}

// ApplyOperatorSettings converges every declared operator setting onto this
// router and reports each one. It never returns an error: every outcome is a
// result, because the caller is either the daemon's start path (which must not
// fail to boot over a network setting) or a config write (which must report
// what it could and could not apply).
//
// On a host that is not an OpenWrt router (the module runs on plain Linux for
// development) every step reports `skipped` with the reason.
func ApplyOperatorSettings(cfg *config_manager.Config) []SettingResult {
	if cfg == nil {
		return []SettingResult{{Setting: "operator settings", Status: statusSkipped, Detail: "no configuration available"}}
	}

	results := []SettingResult{applyAdminAccess(cfg.AdminAccess)}
	results = append(results, applyPrivateNetworkCredentials(cfg)...)
	return results
}

// applyAdminAccess converges the admin_access scope.
func applyAdminAccess(scope string) SettingResult {
	result := SettingResult{Setting: "admin_access"}

	plan, err := planAdminScope(scope, ifacePresent)
	if err != nil {
		result.Status = statusRefused
		result.Detail = err.Error()
		result.Warning = err.Error()
		opLogger.Warn(result.Detail)
		return result
	}

	path := filepath.Join(nftablesDir, adminScopeFragmentName)
	desired := renderAdminScopeFragment(plan)

	current, readErr := os.ReadFile(path)
	if readErr != nil && !os.IsNotExist(readErr) {
		result.Status = statusFailed
		result.Detail = fmt.Sprintf("cannot read %s: %v", path, readErr)
		opLogger.Error(result.Detail)
		return result
	}
	exists := readErr == nil

	if exists && string(current) == desired {
		result.Status = statusUnchanged
		result.Detail = fmt.Sprintf("%s already carries admin_access=%s", path, scope)
		return result
	}
	if !exists && desired == "" {
		result.Status = statusUnchanged
		result.Detail = fmt.Sprintf("admin_access=%s adds no rule; no fragment present", scope)
		return result
	}

	if _, err := os.Stat(nftablesDir); err != nil {
		result.Status = statusSkipped
		result.Detail = fmt.Sprintf("this host has no %s (not an fw4 router): the scope is recorded in config.json and will be applied where fw4 is present", nftablesDir)
		return result
	}

	if desired == "" {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			result.Status = statusFailed
			result.Detail = fmt.Sprintf("cannot remove stale %s: %v", path, err)
			opLogger.Error(result.Detail)
			return result
		}
	} else if err := os.WriteFile(path, []byte(desired), 0644); err != nil {
		result.Status = statusFailed
		result.Detail = fmt.Sprintf("cannot write %s: %v", path, err)
		opLogger.Error(result.Detail)
		return result
	}

	result.Status = statusApplied
	if desired == "" {
		result.Detail = fmt.Sprintf("removed %s: admin_access=%s needs no rule", path, scope)
	} else {
		result.Detail = fmt.Sprintf("wrote %s: networks that may reach the admin ports are scoped by admin_access=%s", path, scope)
	}

	if reloadErr := reloadFirewall(); reloadErr != nil {
		// The file is the durable half and fw4 reads it on its next start, so
		// a failed reload is a warning on an applied change, not a failure of
		// the setting.
		result.Warning = fmt.Sprintf("wrote %s but could not reload fw4 (%v): the scope takes effect at the next firewall reload or reboot", path, reloadErr)
		opLogger.Warn(result.Warning)
	}

	opLogger.WithFields(logrus.Fields{"scope": scope, "path": path}).Info(result.Detail)
	return result
}

// reloadFirewall applies a freshly written include. Best-effort by design: a
// host without fw4 (development, host mode) is not an error.
func reloadFirewall() error {
	if _, err := exec.LookPath("fw4"); err != nil {
		return fmt.Errorf("fw4 is not installed on this host")
	}
	out, err := exec.Command("fw4", "reload").CombinedOutput()
	if err != nil {
		return fmt.Errorf("fw4 reload: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// applyPrivateNetworkCredentials converges the declared private-network
// credentials onto UCI. One result per declared field, so an operator can see
// exactly which of the three took.
func applyPrivateNetworkCredentials(cfg *config_manager.Config) []SettingResult {
	declared := []struct {
		setting string
		option  string
		value   string
	}{
		{"private_ssid", "ssid", cfg.PrivateSSID},
		{"private_key", "key", cfg.PrivateKey},
		{"private_encryption", "encryption", cfg.PrivateEncryption},
	}

	var wanted []struct {
		setting string
		option  string
		value   string
	}
	for _, d := range declared {
		// An empty value is not an instruction. For private_encryption the
		// migration fills the schema default in, so empty means a hand-edited
		// file; treat it the same way and leave the radios alone.
		if strings.TrimSpace(d.value) == "" {
			continue
		}
		wanted = append(wanted, d)
	}
	if len(wanted) == 0 {
		return []SettingResult{{
			Setting: "private_ssid/private_key/private_encryption",
			Status:  statusSkipped,
			Detail:  "config.json declares no private-network credentials: the router keeps the values 99-tollgate-setup minted",
		}}
	}

	results := make([]SettingResult, 0, len(wanted))

	if _, err := exec.LookPath("uci"); err != nil {
		for _, d := range wanted {
			results = append(results, SettingResult{
				Setting: d.setting,
				Status:  statusSkipped,
				Detail:  "uci is not available on this host (not an OpenWrt router); the value is recorded in config.json and stays here",
			})
		}
		return results
	}

	sections := existingPrivateRadioSections()
	if len(sections) == 0 {
		for _, d := range wanted {
			results = append(results, SettingResult{
				Setting: d.setting,
				Status:  statusSkipped,
				Detail:  "this router has no wireless.private_radio0 section (the private network is not provisioned); nothing to converge",
			})
		}
		return results
	}

	dirty := false
	for _, d := range wanted {
		if err := validatePrivateCredential(d.setting, d.value); err != nil {
			results = append(results, SettingResult{
				Setting: d.setting,
				Status:  statusRefused,
				Detail:  err.Error(),
				Warning: err.Error(),
			})
			continue
		}

		result := SettingResult{Setting: d.setting}
		var changedSections []string
		var held []string
		for _, section := range sections {
			current, err := getUCIValue(section + "." + d.option)
			if err != nil {
				// The section exists but has no such option yet: unset, not a
				// failure.
				current = ""
			}
			if current == d.value {
				continue
			}
			if err := setUCIValue(section+"."+d.option, d.value); err != nil {
				result.Status = statusFailed
				result.Detail = fmt.Sprintf("cannot set %s.%s: %v", section, d.option, err)
				opLogger.Error(result.Detail)
				break
			}
			changedSections = append(changedSections, section)
		}

		for _, section := range sections {
			if !containsString(changedSections, section) {
				held = append(held, section)
			}
		}

		switch {
		case result.Status == statusFailed:
			// keep the failure as built
		case len(changedSections) == 0:
			result.Status = statusUnchanged
			result.Detail = fmt.Sprintf("every private radio already has this %s", d.setting)
		default:
			result.Status = statusApplied
			result.Detail = fmt.Sprintf("set on %s", strings.Join(changedSections, ", "))
			if len(held) > 0 {
				result.Warning = fmt.Sprintf("%s was already correct", strings.Join(held, ", "))
			}
			if d.setting == "private_key" {
				// Never echo the value, even in a result that is otherwise
				// about where it landed.
				result.Detail = fmt.Sprintf("passphrase set on %s (value withheld)", strings.Join(changedSections, ", "))
			}
			dirty = true
		}

		results = append(results, result)
	}

	if dirty {
		if err := commitUCI("wireless"); err != nil {
			for i := range results {
				if results[i].Status == statusApplied {
					results[i].Status = statusFailed
					results[i].Detail = fmt.Sprintf("UCI written but not committed: %v", err)
				}
			}
			opLogger.WithError(err).Error("Failed to commit the private-network credentials")
			return results
		}
		if err := reloadWireless(); err != nil {
			for i := range results {
				if results[i].Status == statusApplied {
					results[i].Warning = fmt.Sprintf("UCI committed but the wireless did not reload (%v): the change takes effect at the next wifi reload or reboot", err)
				}
			}
			opLogger.WithError(err).Warn("Failed to reload wireless after writing the private-network credentials")
		}
	}

	return results
}

// existingPrivateRadioSections returns the private wifi-ifaces that exist. UCI
// addressing a section that does not exist would CREATE it (`uci set
// wireless.private_radio1.ssid=x` on a single-radio box makes an unusable
// wifi-iface section), so every write is gated on this.
func existingPrivateRadioSections() []string {
	var sections []string
	for _, section := range privateRadioSections {
		if _, err := getUCIValue(section); err == nil {
			sections = append(sections, section)
		}
	}
	return sections
}

// validatePrivateCredential refuses values that would take the management
// network down rather than making hostapd reject the interface.
func validatePrivateCredential(setting, value string) error {
	switch setting {
	case "private_key":
		if len(value) < passphraseMinLen || len(value) > passphraseMaxLen {
			return fmt.Errorf("private_key must be between %d and %d characters (WPA2-PSK bounds); the router keeps its previous passphrase", passphraseMinLen, passphraseMaxLen)
		}
	case "private_ssid":
		if len(value) > ssidMaxLen {
			return fmt.Errorf("private_ssid must be at most %d bytes (SSID bounds); the router keeps its previous SSID", ssidMaxLen)
		}
	case "private_encryption":
		for _, allowed := range privateEncryptionAlgorithms {
			if value == allowed {
				return nil
			}
		}
		return fmt.Errorf("private_encryption %q is not one of %s; the router keeps its previous encryption mode",
			value, strings.Join(privateEncryptionAlgorithms, ", "))
	}
	return nil
}

func containsString(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// redactSecretFields returns a copy of cfg safe to hand to a read caller: every
// schema field marked Secret is blanked.
//
// The copy matters. `config get` is the payload the board's Settings page
// renders and merges into a wholesale `config save`; handing out the private
// passphrase there would put the management network's WPA key in any
// authenticated board session's memory, its DOM, and any log that echoes a
// response. The write path is unaffected (a caller that wants to change it
// sends a new value), and handleConfigSave preserves the stored value when the
// blanked one comes back.
func redactSecretFields(cfg *config_manager.Config) *config_manager.Config {
	if cfg == nil {
		return nil
	}
	redacted := *cfg
	redacted.PrivateKey = ""
	return &redacted
}

// secretFieldValues is the ONE place that names which struct field carries each
// schema secret. Both redactSecretFields' contract and secretFieldState read
// from it, and TestEverySchemaSecretIsRedactedAndReported fails the suite the
// moment a field is marked Secret in the schema and is not handled here — so a
// second secret cannot be added without this list, and therefore the redacting
// read path, being updated in the same commit.
func secretFieldValues(cfg *config_manager.Config) map[string]string {
	if cfg == nil {
		return map[string]string{}
	}
	return map[string]string{"private_key": cfg.PrivateKey}
}

// secretFieldState reports, per secret schema field, whether a value is stored.
// The board needs to know that a passphrase EXISTS without being told what it
// is, so it can label the field "set" and leave the input empty.
func secretFieldState(cfg *config_manager.Config) map[string]bool {
	state := map[string]bool{}
	values := secretFieldValues(cfg)
	for _, key := range secretJSONKeys() {
		state[key] = values[key] != ""
	}
	return state
}

// secretJSONKeys returns the json keys of the schema's secret fields.
func secretJSONKeys() []string {
	var keys []string
	for _, field := range config_manager.GetConfigSchema() {
		if field.Secret {
			keys = append(keys, field.JSONKey)
		}
	}
	return keys
}

// applySummary renders the applier's results as one line for a CLI message.
func applySummary(results []SettingResult) string {
	applied := 0
	refused := 0
	failed := 0
	skipped := 0
	for _, r := range results {
		switch r.Status {
		case statusApplied:
			applied++
		case statusRefused:
			refused++
		case statusFailed:
			failed++
		case statusSkipped:
			skipped++
		}
	}

	var parts []string
	if applied > 0 {
		parts = append(parts, fmt.Sprintf("%d applied", applied))
	}
	if refused > 0 {
		parts = append(parts, fmt.Sprintf("%d refused", refused))
	}
	if failed > 0 {
		parts = append(parts, fmt.Sprintf("%d failed", failed))
	}
	if skipped > 0 {
		parts = append(parts, fmt.Sprintf("%d not applicable here", skipped))
	}
	if len(parts) == 0 {
		return "runtime already matches"
	}
	return "runtime: " + strings.Join(parts, ", ")
}
