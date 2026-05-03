package cli

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
)

func newTestServer(t *testing.T) (*CLIServer, func()) {
	t.Helper()
	tempDir := t.TempDir()
	cm, err := config_manager.NewConfigManager(
		tempDir+"/config.json",
		tempDir+"/install.json",
		tempDir+"/identities.json",
	)
	if err != nil {
		t.Fatalf("Failed to create ConfigManager: %v", err)
	}
	s := NewCLIServer(cm, nil)
	return s, func() {}
}

func newTestServerWithMerchant(t *testing.T) (*CLIServer, *mockMerchant, func()) {
	t.Helper()
	tempDir := t.TempDir()
	cm, err := config_manager.NewConfigManager(
		tempDir+"/config.json",
		tempDir+"/install.json",
		tempDir+"/identities.json",
	)
	if err != nil {
		t.Fatalf("Failed to create ConfigManager: %v", err)
	}
	mm := &mockMerchant{
		balance: 5000,
		mintBalances: map[string]uint64{
			"https://mint.example.com": 5000,
		},
		acceptedMints: []config_manager.MintConfig{
			{URL: "https://mint.example.com", PricePerStep: 1},
		},
	}
	s := NewCLIServer(cm, mm)
	return s, mm, func() {}
}

func TestNewCLIServer(t *testing.T) {
	s := NewCLIServer(nil, nil)
	if s == nil {
		t.Fatal("NewCLIServer returned nil")
	}
	if s.running {
		t.Error("new server should not be running")
	}
	if s.startTime.IsZero() {
		t.Error("startTime should be initialized")
	}
}

func TestProcessCommandUnknown(t *testing.T) {
	s, cleanup := newTestServer(t)
	defer cleanup()
	resp := s.processCommand(CLIMessage{Command: "foobar"})
	if resp.Success {
		t.Error("expected failure for unknown command")
	}
	if !strings.Contains(resp.Error, "Unknown command") {
		t.Errorf("unexpected error: %s", resp.Error)
	}
}

func TestProcessCommandConfigRouting(t *testing.T) {
	s, cleanup := newTestServer(t)
	defer cleanup()
	resp := s.processCommand(CLIMessage{Command: "config", Args: []string{"schema"}})
	if !resp.Success {
		t.Errorf("config schema should succeed: %s", resp.Error)
	}
}

func TestProcessCommandHealthRouting(t *testing.T) {
	s, cleanup := newTestServer(t)
	defer cleanup()
	resp := s.processCommand(CLIMessage{Command: "health"})
	if resp.Success {
		t.Error("health should report degraded when merchant is nil")
	}
	data, ok := resp.Data.(map[string]interface{})
	if !ok {
		t.Fatal("health response data is not a map")
	}
	if data["healthy"] != false {
		t.Error("expected healthy=false with nil merchant and nil config")
	}
}

func TestProcessCommandVersionRouting(t *testing.T) {
	s, cleanup := newTestServer(t)
	defer cleanup()
	resp := s.processCommand(CLIMessage{Command: "version"})
	if !resp.Success {
		t.Errorf("version should succeed: %s", resp.Error)
	}
	if resp.Data == nil {
		t.Error("version response should have data")
	}
}

func TestHandleHealthCommandDegraded(t *testing.T) {
	s, cleanup := newTestServer(t)
	defer cleanup()
	resp := s.handleHealthCommand()
	if resp.Success {
		t.Error("health should fail when merchant is nil (not fully healthy)")
	}
	if !strings.Contains(resp.Message, "degraded") {
		t.Errorf("expected degraded message, got: %s", resp.Message)
	}
	data := resp.Data.(map[string]interface{})
	if data["healthy"] != false {
		t.Error("healthy should be false (running=false)")
	}
	if data["config_ok"] != true {
		t.Error("config_ok should be true (config manager exists)")
	}
	if data["wallet_ok"] != false {
		t.Error("wallet_ok should be false (merchant is nil)")
	}
}

func TestHandleHealthCommandDegradedNilConfig(t *testing.T) {
	s := &CLIServer{startTime: time.Now()}
	resp := s.handleHealthCommand()
	if resp.Success {
		t.Error("health should fail with nil dependencies")
	}
	data := resp.Data.(map[string]interface{})
	if data["healthy"] != false {
		t.Error("healthy should be false")
	}
	if data["config_ok"] != false {
		t.Error("config_ok should be false")
	}
	if data["wallet_ok"] != false {
		t.Error("wallet_ok should be false")
	}
}

func TestHandleHealthCommandHealthy(t *testing.T) {
	s, _, cleanup := newTestServerWithMerchant(t)
	defer cleanup()
	s.running = true
	resp := s.handleHealthCommand()
	if !resp.Success {
		t.Errorf("health should succeed with all deps: %s", resp.Error)
	}
	if !strings.Contains(resp.Message, "healthy") {
		t.Errorf("expected healthy message, got: %s", resp.Message)
	}
	data := resp.Data.(map[string]interface{})
	if data["healthy"] != true {
		t.Error("healthy should be true")
	}
	if data["config_ok"] != true {
		t.Error("config_ok should be true")
	}
	if data["wallet_ok"] != true {
		t.Error("wallet_ok should be true")
	}
	if data["wallet_balance_sats"] != uint64(5000) {
		t.Errorf("wallet_balance_sats: got %v, want 5000", data["wallet_balance_sats"])
	}
	if data["mint_count"] != 1 {
		t.Errorf("mint_count: got %v, want 1", data["mint_count"])
	}
	if data["metric"] == nil {
		t.Error("metric should be present")
	}
	if data["step_size"] == nil {
		t.Error("step_size should be present")
	}
	if data["running"] != true {
		t.Error("running should be true")
	}
}

func TestHandleVersionCommand(t *testing.T) {
	s, cleanup := newTestServer(t)
	defer cleanup()
	resp := s.handleVersionCommand()
	if !resp.Success {
		t.Errorf("version should succeed: %s", resp.Error)
	}
	data, ok := resp.Data.(map[string]string)
	if !ok {
		t.Fatal("version data is not a map[string]string")
	}
	for _, key := range []string{"version", "commit", "build_time", "go_version", "openwrt_version"} {
		if _, ok := data[key]; !ok {
			t.Errorf("version data missing key: %s", key)
		}
	}
}

func TestHandleStatusCommand(t *testing.T) {
	s, cleanup := newTestServer(t)
	defer cleanup()
	resp := s.handleStatusCommand([]string{}, nil)
	if !resp.Success {
		t.Errorf("status should succeed: %s", resp.Error)
	}
	status, ok := resp.Data.(ServiceStatus)
	if !ok {
		t.Fatal("status data is not ServiceStatus")
	}
	if status.ConfigOK != true {
		t.Error("ConfigOK should be true (config manager exists)")
	}
	if status.WalletOK != false {
		t.Error("WalletOK should be false (merchant is nil)")
	}
	if status.NetworkOK != true {
		t.Error("NetworkOK should be true")
	}
}

func TestHandleWalletCommandNoArgs(t *testing.T) {
	s, cleanup := newTestServer(t)
	defer cleanup()
	resp := s.handleWalletCommand([]string{}, nil)
	if resp.Success {
		t.Error("expected failure with no args")
	}
	if !strings.Contains(resp.Error, "requires an action") {
		t.Errorf("unexpected error: %s", resp.Error)
	}
}

func TestHandleWalletCommandUnknownAction(t *testing.T) {
	s, cleanup := newTestServer(t)
	defer cleanup()
	resp := s.handleWalletCommand([]string{"burn"}, nil)
	if resp.Success {
		t.Error("expected failure for unknown action")
	}
}

func TestHandleWalletBalanceNilMerchant(t *testing.T) {
	s, cleanup := newTestServer(t)
	defer cleanup()
	resp := s.handleWalletBalance()
	if resp.Success {
		t.Error("expected failure with nil merchant")
	}
	if !strings.Contains(resp.Error, "Merchant not available") {
		t.Errorf("unexpected error: %s", resp.Error)
	}
}

func TestHandleWalletBalanceWithMerchant(t *testing.T) {
	s, _, cleanup := newTestServerWithMerchant(t)
	defer cleanup()
	resp := s.handleWalletBalance()
	if !resp.Success {
		t.Errorf("expected success: %s", resp.Error)
	}
	data, ok := resp.Data.(WalletInfo)
	if !ok {
		t.Fatal("data is not WalletInfo")
	}
	if data.Balance != 5000 {
		t.Errorf("balance: got %d, want 5000", data.Balance)
	}
}

func TestHandleWalletInfoNilMerchant(t *testing.T) {
	s, cleanup := newTestServer(t)
	defer cleanup()
	resp := s.handleWalletInfo()
	if resp.Success {
		t.Error("expected failure with nil merchant")
	}
}

func TestHandleWalletFundNoArgs(t *testing.T) {
	s, cleanup := newTestServer(t)
	defer cleanup()
	resp := s.handleWalletFund([]string{}, nil)
	if resp.Success {
		t.Error("expected failure with no args")
	}
}

func TestHandleWalletFundEmptyToken(t *testing.T) {
	s, cleanup := newTestServer(t)
	defer cleanup()
	resp := s.handleWalletFund([]string{""}, nil)
	if resp.Success {
		t.Error("expected failure with empty token")
	}
}

func TestHandleWalletFundNilMerchant(t *testing.T) {
	s, cleanup := newTestServer(t)
	defer cleanup()
	resp := s.handleWalletFund([]string{"cashuToken123"}, nil)
	if resp.Success {
		t.Error("expected failure with nil merchant")
	}
}

func TestHandleWalletFundSuccess(t *testing.T) {
	s, mm, cleanup := newTestServerWithMerchant(t)
	defer cleanup()
	mm.fundAmount = 1000
	resp := s.handleWalletFund([]string{"cashuToken123"}, nil)
	if !resp.Success {
		t.Errorf("expected success: %s", resp.Error)
	}
	data, ok := resp.Data.(map[string]interface{})
	if !ok {
		t.Fatal("data is not a map")
	}
	if data["amount_received"] != uint64(1000) {
		t.Errorf("amount_received: got %v, want 1000", data["amount_received"])
	}
}

func TestHandleWalletFundError(t *testing.T) {
	s, mm, cleanup := newTestServerWithMerchant(t)
	defer cleanup()
	mm.fundErr = fmt.Errorf("token expired")
	resp := s.handleWalletFund([]string{"cashuToken123"}, nil)
	if resp.Success {
		t.Error("expected failure")
	}
	if !strings.Contains(resp.Error, "token expired") {
		t.Errorf("unexpected error: %s", resp.Error)
	}
}

func TestHandleWalletDrainNoArgs(t *testing.T) {
	s, cleanup := newTestServer(t)
	defer cleanup()
	resp := s.handleWalletDrain([]string{}, nil)
	if resp.Success {
		t.Error("expected failure with no drain type")
	}
}

func TestHandleWalletDrainNilMerchant(t *testing.T) {
	s, cleanup := newTestServer(t)
	defer cleanup()
	resp := s.handleWalletDrain([]string{"cashu"}, nil)
	if resp.Success {
		t.Error("expected failure with nil merchant")
	}
}

func TestHandleWalletDrainLightningNotSupported(t *testing.T) {
	s, cleanup := newTestServer(t)
	defer cleanup()
	resp := s.handleWalletDrain([]string{"lightning"}, nil)
	if resp.Success {
		t.Error("expected failure for lightning drain")
	}
	if !strings.Contains(resp.Error, "not yet implemented") {
		t.Errorf("unexpected error: %s", resp.Error)
	}
}

func TestHandleWalletDrainCashuNoMints(t *testing.T) {
	s, mm, cleanup := newTestServerWithMerchant(t)
	defer cleanup()
	mm.mintBalances = map[string]uint64{}
	resp := s.handleWalletDrain([]string{"cashu"}, nil)
	if resp.Success {
		t.Error("expected failure with no mints")
	}
	if !strings.Contains(resp.Error, "No mints found") {
		t.Errorf("unexpected error: %s", resp.Error)
	}
}

func TestHandleWalletDrainCashuSuccess(t *testing.T) {
	s, mm, cleanup := newTestServerWithMerchant(t)
	defer cleanup()
	mm.drainToken = "cashuDrainToken123"
	mm.drainAmount = 3000
	resp := s.handleWalletDrain([]string{"cashu"}, nil)
	if !resp.Success {
		t.Errorf("expected success: %s", resp.Error)
	}
}
