package main

import (
	"testing"
	"time"
)

func recoverResponseCanned(success bool) CLIResponse {
	data := map[string]interface{}{
		"journal_path": "/etc/tollgate/wallet-drain-journal.jsonl",
		"checked":      float64(3),
		"live":         float64(1),
		"spent":        float64(1),
		"unknown":      float64(1),
		"invalid":      float64(0),
		"live_sats":    float64(50),
		"tokens": []interface{}{
			map[string]interface{}{
				"mint_url":     "https://mint.example/Bitcoin",
				"balance_sats": float64(50),
				"token":        "cashu-test-recover-token",
			},
		},
		"entries": []interface{}{
			map[string]interface{}{"mint_url": "https://mint.example/Bitcoin", "amount_sats": float64(50), "state": "live"},
			map[string]interface{}{"mint_url": "https://mint-b.test/Bitcoin", "amount_sats": float64(30), "state": "spent"},
			map[string]interface{}{"mint_url": "https://mint-c.test/Bitcoin", "amount_sats": float64(20), "state": "unknown", "error": "mint unreachable"},
		},
	}
	var errMsg string
	if !success {
		errMsg = "Could not determine the state of 1 journal token(s)"
	}
	return CLIResponse{
		Success:   success,
		Message:   "recovery summary",
		Data:      data,
		Error:     errMsg,
		Timestamp: time.Now(),
	}
}

func TestWalletRecoverCmd_PlainMode_ReportsTokens(t *testing.T) {
	resetDrainCmdState(t)
	t.Setenv("TOLLGATE_TEST_CONFIG_DIR", t.TempDir())
	startFakeService(t, recoverResponseCanned(true))

	// Live tokens are funds: the command must succeed and surface them.
	if err := recoverCmd.RunE(recoverCmd, []string{}); err != nil {
		t.Fatalf("fully classified recovery must exit 0, got: %v", err)
	}
}

func TestWalletRecoverCmd_PlainMode_UnknownState_ExitsNonZero(t *testing.T) {
	resetDrainCmdState(t)
	t.Setenv("TOLLGATE_TEST_CONFIG_DIR", t.TempDir())
	startFakeService(t, recoverResponseCanned(false))

	if err := recoverCmd.RunE(recoverCmd, []string{}); err == nil {
		t.Fatal("undeterminable token state must exit non-zero so operators cannot mistake it for a clean sweep")
	}
}

func TestWalletRecoverCmd_JSONMode_PropagatesFailure(t *testing.T) {
	resetDrainCmdState(t)
	t.Setenv("TOLLGATE_TEST_CONFIG_DIR", t.TempDir())
	startFakeService(t, recoverResponseCanned(false))

	jsonOutput = true
	if err := recoverCmd.RunE(recoverCmd, []string{}); err == nil {
		t.Fatal("JSON mode must exit non-zero when recovery status is incomplete")
	}
}
