package cli

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
)

func setupIntegrationServer(t *testing.T) (*CLIServer, *mockMerchant, string, func()) {
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
		balance: 10000,
		mintBalances: map[string]uint64{
			"https://mint.example.com": 10000,
		},
		acceptedMints: []config_manager.MintConfig{
			{URL: "https://mint.example.com", PricePerStep: 1},
		},
		fundAmount: 500,
	}

	socketPath := filepath.Join(tempDir, "tollgate-test.sock")

	s := &CLIServer{
		configManager: cm,
		merchant:      mm,
		startTime:     time.Now(),
		running:       true,
	}

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("Failed to listen on socket: %v", err)
	}
	s.listener = listener

	go func() {
		for s.running {
			conn, err := listener.Accept()
			if err != nil {
				if s.running {
					cliLogger.WithError(err).Error("Failed to accept connection")
				}
				return
			}
			go s.handleConnection(conn)
		}
	}()

	time.Sleep(50 * time.Millisecond)

	return s, mm, socketPath, func() {
		s.running = false
		listener.Close()
		os.Remove(socketPath)
	}
}

func sendSocketMessage(t *testing.T, socketPath string, msg CLIMessage) *CLIResponse {
	t.Helper()
	conn, err := net.DialTimeout("unix", socketPath, 2*time.Second)
	if err != nil {
		t.Fatalf("Failed to connect to socket: %v", err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))

	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("Failed to marshal message: %v", err)
	}
	conn.Write(data)
	conn.Write([]byte("\n"))

	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 65536), 65536)
	if !scanner.Scan() {
		t.Fatalf("No response from server: %v", scanner.Err())
	}

	var resp CLIResponse
	if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to unmarshal response: %v\nraw: %s", err, scanner.Bytes())
	}
	return &resp
}

func TestIntegrationVersionRoundTrip(t *testing.T) {
	_, _, socketPath, cleanup := setupIntegrationServer(t)
	defer cleanup()

	resp := sendSocketMessage(t, socketPath, CLIMessage{Command: "version"})
	if !resp.Success {
		t.Errorf("version failed: %s", resp.Error)
	}
	if resp.Data == nil {
		t.Error("version response has no data")
	}
}

func TestIntegrationStatusRoundTrip(t *testing.T) {
	_, _, socketPath, cleanup := setupIntegrationServer(t)
	defer cleanup()

	resp := sendSocketMessage(t, socketPath, CLIMessage{Command: "status"})
	if !resp.Success {
		t.Errorf("status failed: %s", resp.Error)
	}
}

func TestIntegrationHealthRoundTrip(t *testing.T) {
	_, _, socketPath, cleanup := setupIntegrationServer(t)
	defer cleanup()

	resp := sendSocketMessage(t, socketPath, CLIMessage{Command: "health"})
	if !resp.Success {
		t.Errorf("health failed: %s", resp.Error)
	}
	data, ok := resp.Data.(map[string]interface{})
	if !ok {
		t.Fatal("health data is not a map")
	}
	if data["wallet_balance_sats"] == nil {
		t.Error("health missing wallet_balance_sats")
	}
}

func TestIntegrationConfigGetRoundTrip(t *testing.T) {
	_, _, socketPath, cleanup := setupIntegrationServer(t)
	defer cleanup()

	resp := sendSocketMessage(t, socketPath, CLIMessage{Command: "config", Args: []string{"get"}})
	if !resp.Success {
		t.Errorf("config get failed: %s", resp.Error)
	}
	data, ok := resp.Data.(map[string]interface{})
	if !ok {
		t.Fatal("config get data is not a map")
	}
	if data["config"] == nil {
		t.Error("config get missing 'config'")
	}
	if data["identities"] == nil {
		t.Error("config get missing 'identities'")
	}
}

func TestIntegrationConfigSchemaRoundTrip(t *testing.T) {
	_, _, socketPath, cleanup := setupIntegrationServer(t)
	defer cleanup()

	resp := sendSocketMessage(t, socketPath, CLIMessage{Command: "config", Args: []string{"schema"}})
	if !resp.Success {
		t.Errorf("config schema failed: %s", resp.Error)
	}
}

func TestIntegrationConfigSetRoundTrip(t *testing.T) {
	s, _, socketPath, cleanup := setupIntegrationServer(t)
	defer cleanup()

	resp := sendSocketMessage(t, socketPath, CLIMessage{
		Command: "config",
		Args:    []string{"set", "metric", "milliseconds"},
	})
	if !resp.Success {
		t.Errorf("config set failed: %s", resp.Error)
	}

	cfg := s.configManager.GetConfig()
	if cfg.Metric != "milliseconds" {
		t.Errorf("metric not updated: got %s", cfg.Metric)
	}
}

func TestIntegrationConfigSetInvalidField(t *testing.T) {
	_, _, socketPath, cleanup := setupIntegrationServer(t)
	defer cleanup()

	resp := sendSocketMessage(t, socketPath, CLIMessage{
		Command: "config",
		Args:    []string{"set", "nonexistent_xyz", "value"},
	})
	if resp.Success {
		t.Error("expected failure for nonexistent field")
	}
}

func TestIntegrationWalletBalanceRoundTrip(t *testing.T) {
	_, _, socketPath, cleanup := setupIntegrationServer(t)
	defer cleanup()

	resp := sendSocketMessage(t, socketPath, CLIMessage{Command: "wallet", Args: []string{"balance"}})
	if !resp.Success {
		t.Errorf("wallet balance failed: %s", resp.Error)
	}
}

func TestIntegrationWalletFundRoundTrip(t *testing.T) {
	_, _, socketPath, cleanup := setupIntegrationServer(t)
	defer cleanup()

	resp := sendSocketMessage(t, socketPath, CLIMessage{
		Command: "wallet",
		Args:    []string{"fund", "cashuTestToken123"},
	})
	if !resp.Success {
		t.Errorf("wallet fund failed: %s", resp.Error)
	}
}

func TestIntegrationUnknownCommand(t *testing.T) {
	_, _, socketPath, cleanup := setupIntegrationServer(t)
	defer cleanup()

	resp := sendSocketMessage(t, socketPath, CLIMessage{Command: "dance"})
	if resp.Success {
		t.Error("expected failure for unknown command")
	}
	if resp.Error == "" {
		t.Error("expected error message")
	}
}

func TestIntegrationMalformedJSON(t *testing.T) {
	_, _, socketPath, cleanup := setupIntegrationServer(t)
	defer cleanup()

	conn, err := net.DialTimeout("unix", socketPath, 2*time.Second)
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))

	conn.Write([]byte("not valid json\n"))

	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 65536), 65536)
	if !scanner.Scan() {
		t.Fatalf("No response for malformed JSON: %v", scanner.Err())
	}

	var resp CLIResponse
	if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
		t.Fatalf("Response is not valid JSON: %v", err)
	}
	if resp.Success {
		t.Error("expected failure for malformed JSON")
	}
}

func TestIntegrationMultipleConnections(t *testing.T) {
	_, _, socketPath, cleanup := setupIntegrationServer(t)
	defer cleanup()

	for i := 0; i < 5; i++ {
		resp := sendSocketMessage(t, socketPath, CLIMessage{Command: "version"})
		if !resp.Success {
			t.Errorf("connection %d: version failed: %s", i, resp.Error)
		}
	}
}

func TestIntegrationSocketFileCleaned(t *testing.T) {
	_, _, socketPath, cleanup := setupIntegrationServer(t)

	if _, err := os.Stat(socketPath); err != nil {
		t.Fatalf("socket file should exist while server running: %v", err)
	}

	cleanup()

	if _, err := os.Stat(socketPath); !os.IsNotExist(err) {
		t.Error("socket file should be removed after stop")
	}
}

func TestIntegrationConfigSaveRoundTrip(t *testing.T) {
	s, _, socketPath, cleanup := setupIntegrationServer(t)
	defer cleanup()

	cfg := s.configManager.GetConfig()
	cfgJSON, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("Failed to marshal config: %v", err)
	}

	resp := sendSocketMessage(t, socketPath, CLIMessage{
		Command: "config",
		Args:    []string{"save", string(cfgJSON)},
	})
	if !resp.Success {
		t.Errorf("config save failed: %s", resp.Error)
	}
}

func TestIntegrationWalletDrainRoundTrip(t *testing.T) {
	_, mm, socketPath, cleanup := setupIntegrationServer(t)
	defer cleanup()

	mm.drainToken = "drainTokenABC"
	mm.drainAmount = 10000

	resp := sendSocketMessage(t, socketPath, CLIMessage{
		Command: "wallet",
		Args:    []string{"drain", "cashu"},
	})
	if !resp.Success {
		t.Errorf("wallet drain failed: %s", resp.Error)
	}
}
