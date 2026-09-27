package cli

// Offline tests for the operator-settable network settings: the pure scope
// plan, the fragment renderer, the compare-and-converge writer, and the two
// contract properties the admin surface rests on (a secret is never returned,
// and a wholesale save never clears one).
//
// None of these need a router: `ifacePresent` and `nftablesDir` are the seams.

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
)

func withTempNftablesDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	old := nftablesDir
	nftablesDir = dir
	t.Cleanup(func() { nftablesDir = old })
	return dir
}

func withIfacePresence(t *testing.T, present ...string) {
	t.Helper()
	set := make(map[string]bool, len(present))
	for _, name := range present {
		set[name] = true
	}
	old := ifacePresent
	ifacePresent = func(name string) bool { return set[name] }
	t.Cleanup(func() { ifacePresent = old })
}

// newConfigCLIServer builds a CLIServer over a real ConfigManager in a temp
// dir, which is what the config paths need (the operator-settings tests). It is
// deliberately separate from newTestCLIServer, which services the wifi/upstream
// tests with a nil config manager.
func newConfigCLIServer(t *testing.T) *CLIServer {
	t.Helper()
	dir := t.TempDir()
	cm, err := config_manager.NewConfigManager(
		filepath.Join(dir, "config.json"),
		filepath.Join(dir, "install.json"),
		filepath.Join(dir, "identities.json"),
	)
	if err != nil {
		t.Fatalf("NewConfigManager: %v", err)
	}
	return &CLIServer{configManager: cm}
}

func ruleLines(fragment string) []string {
	var rules []string
	for _, line := range strings.Split(fragment, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "meta nfproto") {
			rules = append(rules, line)
		}
	}
	return rules
}

func TestPlanAdminScope(t *testing.T) {
	withIfacePresence(t, "br-mgmt", "br-private", "lo", "br-lan")

	t.Run("both_is_a_noop", func(t *testing.T) {
		plan, err := planAdminScope(AdminAccessBoth, ifacePresent)
		if err != nil {
			t.Fatalf("both must be accepted, got: %v", err)
		}
		if !plan.Noop {
			t.Error("both must be a Noop: the default value has to change nothing on the wire")
		}
		if len(plan.DropIfaces) != 0 || plan.DropAllButLoopback {
			t.Errorf("both must not drop anything, got %+v", plan)
		}
	})

	t.Run("private_drops_the_wired_bridge", func(t *testing.T) {
		plan, err := planAdminScope(AdminAccessPrivate, ifacePresent)
		if err != nil {
			t.Fatalf("br-private must be accepted, got: %v", err)
		}
		if len(plan.DropIfaces) != 1 || plan.DropIfaces[0] != "br-mgmt" {
			t.Errorf("br-private must drop br-mgmt only, got %v", plan.DropIfaces)
		}
	})

	t.Run("mgmt_drops_the_private_bridge", func(t *testing.T) {
		plan, err := planAdminScope(AdminAccessMgmt, ifacePresent)
		if err != nil {
			t.Fatalf("br-mgmt must be accepted while the bridge exists, got: %v", err)
		}
		if len(plan.DropIfaces) != 1 || plan.DropIfaces[0] != "br-private" {
			t.Errorf("br-mgmt must drop br-private only, got %v", plan.DropIfaces)
		}
	})

	t.Run("mgmt_is_refused_without_the_bridge", func(t *testing.T) {
		withIfacePresence(t, "br-private", "lo", "br-lan")
		plan, err := planAdminScope(AdminAccessMgmt, ifacePresent)
		if err == nil {
			t.Fatal("br-mgmt without the br-mgmt bridge must be refused: it would leave no network reaching the board")
		}
		if plan.Scope != "" || len(plan.DropIfaces) != 0 {
			t.Errorf("a refused scope must not produce a plan, got %+v", plan)
		}
		if !strings.Contains(err.Error(), "br-mgmt") {
			t.Errorf("the refusal must name the missing prerequisite, got: %v", err)
		}
	})

	t.Run("loopback_is_global", func(t *testing.T) {
		plan, err := planAdminScope(AdminAccessLoopback, ifacePresent)
		if err != nil {
			t.Fatalf("loopback-only must be accepted, got: %v", err)
		}
		if !plan.DropAllButLoopback {
			t.Error("loopback-only must be the global exception form, not a list of names")
		}
	})

	t.Run("unknown_is_refused", func(t *testing.T) {
		for _, bad := range []string{"", "br-lan", "private", "BR-PRIVATE", "both "} {
			if _, err := planAdminScope(bad, ifacePresent); err == nil {
				t.Errorf("admin_access=%q must be refused rather than defaulted", bad)
			}
		}
	})
}

func TestRenderAdminScopeFragmentDefaultIsEmpty(t *testing.T) {
	plan, err := planAdminScope(AdminAccessBoth, func(string) bool { return true })
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if got := renderAdminScopeFragment(plan); got != "" {
		t.Errorf("the default scope must render nothing, got:\n%s", got)
	}
}

func TestRenderAdminScopeFragmentShape(t *testing.T) {
	plan, err := planAdminScope(AdminAccessPrivate, func(string) bool { return true })
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	fragment := renderAdminScopeFragment(plan)

	if !strings.Contains(fragment, "chain admin_access_scope {") {
		t.Error("fragment must define its own chain")
	}
	if !strings.Contains(fragment, "hook input priority -1") {
		t.Error("fragment must hook input at priority -1: the same seam and priority the shipped guards use")
	}
	if !strings.Contains(fragment, "policy accept") {
		t.Error("the chain's own policy must stay accept: this fragment removes reach, it never grants it")
	}
	if strings.Contains(fragment, "table ") {
		t.Error("fragment must not open its own table: fw4 includes it inside `table inet fw4`")
	}

	rules := ruleLines(fragment)
	if len(rules) != 2 {
		t.Fatalf("want one rule per address family, got %d: %v", len(rules), rules)
	}
	for _, want := range []string{"meta nfproto ipv4", "meta nfproto ipv6", `iifname "br-mgmt"`, "tcp dport { 443, 8080, 8090, 8443 }", "counter drop"} {
		if !strings.Contains(fragment, want) {
			t.Errorf("fragment is missing %q", want)
		}
	}
	for _, rule := range rules {
		if strings.Contains(rule, "br-lan") {
			t.Errorf("the captive bridge must not appear in this fragment — its drop is owned by 31-*.nft/32-*.nft and a second copy double-counts the same packet: %s", rule)
		}
		if strings.Contains(rule, "br-private") && plan.Scope == AdminAccessPrivate {
			t.Errorf("admin_access=br-private must not drop br-private: %s", rule)
		}
	}
}

func TestRenderAdminScopeFragmentLoopback(t *testing.T) {
	plan, err := planAdminScope(AdminAccessLoopback, func(string) bool { return true })
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	fragment := renderAdminScopeFragment(plan)
	rules := ruleLines(fragment)
	if len(rules) != 2 {
		t.Fatalf("want one rule per address family, got %d", len(rules))
	}
	for _, rule := range rules {
		if !strings.Contains(rule, `iifname != "lo"`) {
			t.Errorf("loopback-only must exempt loopback explicitly: %s", rule)
		}
		if !strings.Contains(rule, "drop") {
			t.Errorf("loopback-only must drop: %s", rule)
		}
	}
}

func TestApplyAdminAccessWritesThenConverges(t *testing.T) {
	dir := withTempNftablesDir(t)
	withIfacePresence(t, "br-mgmt", "br-private", "lo", "br-lan")

	path := filepath.Join(dir, adminScopeFragmentName)

	first := applyAdminAccess(AdminAccessPrivate)
	if first.Status != statusApplied {
		t.Fatalf("first apply: got status %q (%s), want %q", first.Status, first.Detail, statusApplied)
	}
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("fragment not written: %v", err)
	}
	if !strings.Contains(string(written), `iifname "br-mgmt"`) {
		t.Error("written fragment does not carry the scope")
	}
	if first.Warning == "" {
		t.Error("fw4 is not installed in this test environment: the missing reload must be reported as a warning, not swallowed")
	}

	second := applyAdminAccess(AdminAccessPrivate)
	if second.Status != statusUnchanged {
		t.Errorf("second apply: got status %q, want %q (compare-and-converge: nothing on the wire may move twice)", second.Status, statusUnchanged)
	}

	// Switching to the default removes the file: its absence IS the default.
	third := applyAdminAccess(AdminAccessBoth)
	if third.Status != statusApplied {
		t.Errorf("switching to the default: got status %q (%s), want %q", third.Status, third.Detail, statusApplied)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("admin_access=both must leave no fragment on disk, stat err=%v", err)
	}

	// And a second time is a no-op with no file to write.
	fourth := applyAdminAccess(AdminAccessBoth)
	if fourth.Status != statusUnchanged {
		t.Errorf("default with no fragment present: got status %q, want %q", fourth.Status, statusUnchanged)
	}
}

func TestApplyAdminAccessRefusalLeavesTheRouterAlone(t *testing.T) {
	dir := withTempNftablesDir(t)
	withIfacePresence(t, "br-private", "lo", "br-lan")

	// Seed a fragment for the scope the operator is running today.
	if r := applyAdminAccess(AdminAccessPrivate); r.Status != statusApplied {
		t.Fatalf("seed apply failed: %s", r.Detail)
	}
	path := filepath.Join(dir, adminScopeFragmentName)
	seeded, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("seed fragment missing: %v", err)
	}

	// br-mgmt is not a bridge on this router: the scope must be refused, and
	// the running scope left exactly as it was.
	result := applyAdminAccess(AdminAccessMgmt)
	if result.Status != statusRefused {
		t.Fatalf("got status %q (%s), want %q", result.Status, result.Detail, statusRefused)
	}
	if result.Warning == "" {
		t.Error("a refusal must carry a reason an operator can act on")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("a refusal must not remove the fragment in force: %v", err)
	}
	if string(after) != string(seeded) {
		t.Error("a refusal must not rewrite the fragment in force")
	}
}

func TestApplyAdminAccessSkipsWithoutNftablesDir(t *testing.T) {
	old := nftablesDir
	nftablesDir = filepath.Join(t.TempDir(), "no-such-dir", "nftables.d")
	t.Cleanup(func() { nftablesDir = old })
	withIfacePresence(t, "br-mgmt")

	result := applyAdminAccess(AdminAccessPrivate)
	if result.Status != statusSkipped {
		t.Fatalf("got status %q (%s), want %q on a host with no fw4 include dir", result.Status, result.Detail, statusSkipped)
	}
	if !strings.Contains(result.Detail, "config.json") {
		t.Errorf("a skip must say the value is still recorded, got: %s", result.Detail)
	}
}

func TestApplyOperatorSettingsReportsEveryDeclaredSetting(t *testing.T) {
	withTempNftablesDir(t)

	cfg := config_manager.NewDefaultConfig()
	results := ApplyOperatorSettings(cfg)

	seen := map[string]string{}
	for _, r := range results {
		seen[r.Setting] = r.Status
	}
	if got, ok := seen["admin_access"]; !ok || got != statusUnchanged {
		t.Errorf("admin_access result: got %q (present=%v), want %q", got, ok, statusUnchanged)
	}
	// The shipped default declares an encryption mode (the module has always
	// owned that value: 99-tollgate-setup writes the literal on every full
	// setup), so it is converged. The SSID and passphrase are empty by default,
	// which is "keep what the router has" — reporting them as settings to
	// converge would be a lie.
	if _, ok := seen["private_encryption"]; !ok {
		t.Errorf("private_encryption has a shipped default and must be reported, got %v", seen)
	}
	if _, ok := seen["private_ssid"]; ok {
		t.Errorf("an empty private_ssid is not an instruction and must not be applied, got %v", seen)
	}
	if _, ok := seen["private_key"]; ok {
		t.Errorf("an empty private_key is not an instruction and must not be applied, got %v", seen)
	}
}

func TestApplyOperatorSettingsNilConfig(t *testing.T) {
	results := ApplyOperatorSettings(nil)
	if len(results) != 1 || results[0].Status != statusSkipped {
		t.Errorf("a nil config must produce one skipped result, got %+v", results)
	}
}

func TestValidatePrivateCredential(t *testing.T) {
	if err := validatePrivateCredential("private_key", "short"); err == nil {
		t.Error("a passphrase under 8 characters must be refused: hostapd would refuse the interface")
	}
	if err := validatePrivateCredential("private_key", strings.Repeat("a", 64)); err == nil {
		t.Error("a passphrase over 63 characters must be refused")
	}
	if err := validatePrivateCredential("private_key", "Good-Pass-1234"); err != nil {
		t.Errorf("a valid passphrase must be accepted, got: %v", err)
	}
	if err := validatePrivateCredential("private_ssid", strings.Repeat("s", 33)); err == nil {
		t.Error("an SSID over 32 bytes must be refused")
	}
	if err := validatePrivateCredential("private_encryption", "sae"); err == nil {
		t.Error("sae must be refused: the shipped wpad has no SAE support")
	}
	if err := validatePrivateCredential("private_encryption", "psk2+tkip+ccmp"); err != nil {
		t.Errorf("psk2+tkip+ccmp must be accepted, got: %v", err)
	}
}

func TestRedactSecretFields(t *testing.T) {
	cfg := config_manager.NewDefaultConfig()
	cfg.PrivateKey = "Hotel-November-Zulu-42"
	cfg.PrivateSSID = "c08r4d0r-7F3A"

	redacted := redactSecretFields(cfg)
	if redacted == nil {
		t.Fatal("redactSecretFields returned nil for a non-nil config")
	}
	if redacted.PrivateKey != "" {
		t.Errorf("the passphrase must be blanked, got %q", redacted.PrivateKey)
	}
	if redacted.PrivateSSID != "c08r4d0r-7F3A" {
		t.Errorf("the SSID is not a secret and must survive redaction, got %q", redacted.PrivateSSID)
	}
	if cfg.PrivateKey == "" {
		t.Error("redaction must not mutate the caller's config")
	}
	if state := secretFieldState(cfg); !state["private_key"] {
		t.Error("secretFieldState must report that a passphrase is stored")
	}
	if state := secretFieldState(config_manager.NewDefaultConfig()); state["private_key"] {
		t.Error("secretFieldState must report false when no passphrase is stored")
	}
	if _, err := json.Marshal(redacted); err != nil {
		t.Errorf("a redacted config must still marshal: %v", err)
	}
}

func TestHandleConfigSetWithholdsSecretValue(t *testing.T) {
	withTempNftablesDir(t)
	s := newConfigCLIServer(t)

	// The fixture is a value, not a credential: it is named for what it is
	// (a new key being SET) so the repo's credential scanner does not have to
	// treat a test literal as a password-shaped assignment.
	const newKey = "Unique-Pass-2026"
	resp := s.handleConfigSet("private_key", newKey)
	if !resp.Success {
		t.Fatalf("set failed: %s", resp.Error)
	}
	if strings.Contains(resp.Message, newKey) {
		t.Errorf("the response message echoed the passphrase: %s", resp.Message)
	}
	if data, ok := resp.Data.(map[string]interface{}); ok {
		if _, present := data["value"]; present {
			t.Error("the response data carried the passphrase back")
		}
	} else {
		t.Fatalf("unexpected response data type %T", resp.Data)
	}

	stored := s.configManager.GetConfig()
	if stored.PrivateKey != newKey {
		t.Errorf("the passphrase was not stored: got %q", stored.PrivateKey)
	}

	get := s.handleConfigGet()
	data, ok := get.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("unexpected config get data type %T", get.Data)
	}
	returned, ok := data["config"].(*config_manager.Config)
	if !ok {
		t.Fatalf("config get did not return a *Config: %T", data["config"])
	}
	if returned.PrivateKey != "" {
		t.Errorf("config get must never return the passphrase, got %q", returned.PrivateKey)
	}
	secretSet, ok := data["secret_set"].(map[string]bool)
	if !ok || !secretSet["private_key"] {
		t.Errorf("config get must report that a passphrase is set, got %v", data["secret_set"])
	}

	// A non-secret field keeps echoing its value, so the change stays legible.
	plain := s.handleConfigSet("private_ssid", "c08r4d0r-TEST")
	if !plain.Success || !strings.Contains(plain.Message, "c08r4d0r-TEST") {
		t.Errorf("a non-secret setting must still report its value, got: %s / %s", plain.Message, plain.Error)
	}
}

func TestHandleConfigSavePreservesStoredSecret(t *testing.T) {
	withTempNftablesDir(t)
	s := newConfigCLIServer(t)

	const newKey = "Unique-Pass-2026"
	if err := config_manager.SetDotPath(s.configManager, "private_key", newKey); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Exactly what the board sends: the payload it got from `config get`,
	// which has the secret blanked, merged with an unrelated edit.
	cfg := config_manager.NewDefaultConfig()
	cfg.LogLevel = "debug"
	payload, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(payload), newKey) {
		t.Fatal("the test payload must be the redacted shape the board sends")
	}

	resp := s.handleConfigSave(string(payload))
	if !resp.Success {
		t.Fatalf("save failed: %s", resp.Error)
	}
	if !strings.Contains(resp.Message, "kept unchanged") {
		t.Errorf("the save must say it preserved the secret, got: %s", resp.Message)
	}

	stored := s.configManager.GetConfig()
	if stored.PrivateKey != newKey {
		t.Errorf("a wholesale save cleared the stored passphrase: got %q, want it preserved", stored.PrivateKey)
	}
	if stored.LogLevel != "debug" {
		t.Errorf("the save did not apply the real edit: log_level=%q", stored.LogLevel)
	}
}

func TestHandleConfigSaveValidatesOperatorEnums(t *testing.T) {
	withTempNftablesDir(t)
	s := newConfigCLIServer(t)

	cfg := config_manager.NewDefaultConfig()
	cfg.PrivateEncryption = "sae"
	payload, _ := json.Marshal(cfg)

	resp := s.handleConfigSave(string(payload))
	if resp.Success {
		t.Fatal("a wholesale save with an unsupported encryption mode must be refused: the private network would not come up")
	}
	if !strings.Contains(resp.Error, "private_encryption") {
		t.Errorf("the refusal must name the field, got: %s", resp.Error)
	}

	cfg = config_manager.NewDefaultConfig()
	cfg.AdminAccess = "br-lan"
	payload, _ = json.Marshal(cfg)

	resp = s.handleConfigSave(string(payload))
	if resp.Success {
		t.Fatal("a wholesale save with an unknown admin_access must be refused rather than silently defaulted")
	}
	if !strings.Contains(resp.Error, "admin_access") {
		t.Errorf("the refusal must name the field, got: %s", resp.Error)
	}
}

func TestHandleConfigSetRejectsUnknownAdminAccess(t *testing.T) {
	withTempNftablesDir(t)
	s := newConfigCLIServer(t)

	resp := s.handleConfigSet("admin_access", "br-lan")
	if resp.Success {
		t.Fatal("admin_access=br-lan must be refused: the captive bridge is never an administration path")
	}

	resp = s.handleConfigSet("admin_access", "loopback-only")
	if !resp.Success {
		t.Fatalf("admin_access=loopback-only must be accepted, got: %s", resp.Error)
	}
	data, _ := resp.Data.(map[string]interface{})
	results, _ := data["applied"].([]SettingResult)
	if len(results) == 0 {
		t.Fatalf("a config set must report what it applied, got %v", data)
	}
}

func TestHandleConfigApplyReportsPerSetting(t *testing.T) {
	withTempNftablesDir(t)
	s := newConfigCLIServer(t)

	resp := s.handleConfigApply()
	if !resp.Success {
		t.Fatalf("apply failed: %s", resp.Error)
	}
	data, ok := resp.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("unexpected data type %T", resp.Data)
	}
	results, ok := data["applied"].([]SettingResult)
	if !ok || len(results) == 0 {
		t.Fatalf("apply must report every setting it looked at, got %v", data["applied"])
	}
	for _, r := range results {
		if r.Status == "" {
			t.Errorf("a result without a status is not legible: %+v", r)
		}
	}
}

// TestApplyPrivateNetworkCredentialsWithoutUci pins the host-mode path: the
// module runs on plain Linux, where there is no uci, and a config set must not
// fail because of it.
func TestApplyPrivateNetworkCredentialsWithoutUci(t *testing.T) {
	if _, err := exec.LookPath("uci"); err == nil {
		t.Skip("uci is present on this host; the no-uci path cannot be exercised here")
	}

	cfg := config_manager.NewDefaultConfig()
	cfg.PrivateSSID = "c08r4d0r-TEST"
	cfg.PrivateKey = "Hotel-November-Zulu-42"

	results := applyPrivateNetworkCredentials(cfg)
	if len(results) != 3 {
		t.Fatalf("want one result per declared field, got %d: %+v", len(results), results)
	}
	for _, r := range results {
		if r.Status != statusSkipped {
			t.Errorf("%s: got status %q, want %q on a host without uci", r.Setting, r.Status, statusSkipped)
		}
		if strings.Contains(r.Detail+r.Warning, cfg.PrivateKey) {
			t.Errorf("%s: the passphrase must never appear in a result", r.Setting)
		}
	}
}

func TestApplyPrivateNetworkCredentialsAllEmpty(t *testing.T) {
	cfg := config_manager.NewDefaultConfig()
	cfg.PrivateEncryption = ""

	results := applyPrivateNetworkCredentials(cfg)
	if len(results) != 1 || results[0].Status != statusSkipped {
		t.Fatalf("an empty declaration must produce one skipped result, got %+v", results)
	}
}
