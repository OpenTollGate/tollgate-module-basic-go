package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func captureOutput(f func()) string {
	oldStdout := os.Stdout
	oldStderr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stdout = w
	os.Stderr = w
	f()
	w.Close()
	os.Stdout = oldStdout
	os.Stderr = oldStderr
	var buf bytes.Buffer
	buf.ReadFrom(r)
	return buf.String()
}

func TestPrintJSON(t *testing.T) {
	output := captureOutput(func() {
		err := printJSON(map[string]string{"key": "value"})
		if err != nil {
			t.Errorf("printJSON returned error: %v", err)
		}
	})
	if !strings.Contains(output, `"key"`) {
		t.Errorf("printJSON output missing key: %s", output)
	}
	if !strings.Contains(output, `"value"`) {
		t.Errorf("printJSON output missing value: %s", output)
	}
}

func TestPrintJSONFormatted(t *testing.T) {
	output := captureOutput(func() {
		printJSON(map[string]interface{}{
			"success": true,
			"data":    map[string]int{"count": 42},
		})
	})
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(output), &parsed); err != nil {
		t.Fatalf("printJSON output is not valid JSON: %v\n%s", err, output)
	}
	if parsed["success"] != true {
		t.Error("success should be true")
	}
}

func TestPrintJSONCLIResponse(t *testing.T) {
	resp := &CLIResponse{
		Success: true,
		Message: "test message",
		Data:    map[string]interface{}{"balance_sats": uint64(5000)},
	}
	output := captureOutput(func() {
		printJSON(resp)
	})
	var parsed CLIResponse
	if err := json.Unmarshal([]byte(output), &parsed); err != nil {
		t.Fatalf("output not valid CLIResponse: %v", err)
	}
	if !parsed.Success {
		t.Error("success should be true")
	}
	if parsed.Message != "test message" {
		t.Errorf("message: got %q, want 'test message'", parsed.Message)
	}
}

func TestDisplayResponseSuccess(t *testing.T) {
	output := captureOutput(func() {
		displayResponse(&CLIResponse{
			Success: true,
			Message: "Operation completed",
			Data:    map[string]interface{}{"count": 42},
		})
	})
	if !strings.Contains(output, "Operation completed") {
		t.Errorf("output missing message: %s", output)
	}
	if !strings.Contains(output, "count") {
		t.Errorf("output missing data: %s", output)
	}
}

func TestDisplayResponseError(t *testing.T) {
	output := captureOutput(func() {
		displayResponse(&CLIResponse{
			Success: false,
			Error:   "something went wrong",
		})
	})
	if !strings.Contains(output, "something went wrong") {
		t.Errorf("output missing error: %s", output)
	}
}

func TestDisplayResponseSuccessNoData(t *testing.T) {
	output := captureOutput(func() {
		displayResponse(&CLIResponse{
			Success: true,
			Message: "Done",
		})
	})
	if !strings.Contains(output, "Done") {
		t.Errorf("output missing message: %s", output)
	}
}

func TestDisplayDataWalletDrain(t *testing.T) {
	output := captureOutput(func() {
		displayData(map[string]interface{}{
			"tokens": []interface{}{
				map[string]interface{}{
					"mint_url":     "https://mint.example.com",
					"balance_sats": float64(5000),
					"token":        "cashuToken123",
				},
			},
			"total_sats": float64(5000),
		})
	})
	if !strings.Contains(output, "Wallet Drain") {
		t.Errorf("output missing drain header: %s", output)
	}
	if !strings.Contains(output, "5000") {
		t.Errorf("output missing total: %s", output)
	}
}

func TestDisplayDataPrivateNetworkInfo(t *testing.T) {
	output := captureOutput(func() {
		displayData(map[string]interface{}{
			"ssid":     "MyNetwork",
			"password": "secret123",
			"enabled":  true,
		})
	})
	if !strings.Contains(output, "Private Network") {
		t.Errorf("output missing network header: %s", output)
	}
	if !strings.Contains(output, "MyNetwork") {
		t.Errorf("output missing SSID: %s", output)
	}
	if !strings.Contains(output, "Enabled") {
		t.Errorf("output missing status: %s", output)
	}
}

func TestDisplayDataGenericMap(t *testing.T) {
	output := captureOutput(func() {
		displayData(map[string]interface{}{
			"running":    true,
			"version":    "v0.0.4",
			"uptime":     "1h23m45s",
			"config_ok":  true,
			"wallet_ok":  true,
			"network_ok": true,
		})
	})
	if !strings.Contains(output, "running") {
		t.Errorf("output missing 'running': %s", output)
	}
	if !strings.Contains(output, "v0.0.4") {
		t.Errorf("output missing version: %s", output)
	}
}

func TestDisplayDataStructuredType(t *testing.T) {
	output := captureOutput(func() {
		displayData(map[string]interface{}{
			"balance_sats": uint64(12345),
			"address":      "bc1qexample",
		})
	})
	if !strings.Contains(output, "12345") {
		t.Errorf("output missing balance: %s", output)
	}
}

func TestDisplayPrivateNetworkInfoDisabled(t *testing.T) {
	output := captureOutput(func() {
		displayPrivateNetworkInfo(map[string]interface{}{
			"ssid":     "TestNet",
			"password": "pass123",
			"enabled":  false,
		})
	})
	if !strings.Contains(output, "Disabled") {
		t.Errorf("output should show Disabled: %s", output)
	}
}

func TestDisplayPrivateNetworkInfoEnabled(t *testing.T) {
	output := captureOutput(func() {
		displayPrivateNetworkInfo(map[string]interface{}{
			"ssid":     "TestNet",
			"password": "pass123",
			"enabled":  true,
		})
	})
	if !strings.Contains(output, "Enabled") {
		t.Errorf("output should show Enabled: %s", output)
	}
}

func TestSaveTokensToFile(t *testing.T) {
	tempDir := t.TempDir()
	filename := filepath.Join(tempDir, "drain_test.txt")

	tokens := []interface{}{
		map[string]interface{}{
			"mint_url":     "https://mint.example.com",
			"balance_sats": float64(3000),
			"token":        "cashuAbc123",
		},
	}
	data := map[string]interface{}{
		"tokens":     tokens,
		"total_sats": float64(3000),
	}

	err := saveTokensToFile(filename, tokens, data)
	if err != nil {
		t.Fatalf("saveTokensToFile failed: %v", err)
	}

	content, err := os.ReadFile(filename)
	if err != nil {
		t.Fatalf("Failed to read output file: %v", err)
	}

	text := string(content)
	if !strings.Contains(text, "TollGate Wallet Drain") {
		t.Error("missing header")
	}
	if !strings.Contains(text, "3000") {
		t.Error("missing total")
	}
	if !strings.Contains(text, "Token 1") {
		t.Error("missing token section")
	}
	if !strings.Contains(text, "https://mint.example.com") {
		t.Error("missing mint URL")
	}
	if !strings.Contains(text, "cashuAbc123") {
		t.Error("missing token string")
	}
}

func TestSaveTokensToFileMultiple(t *testing.T) {
	tempDir := t.TempDir()
	filename := filepath.Join(tempDir, "drain_multi.txt")

	tokens := []interface{}{
		map[string]interface{}{
			"mint_url":     "https://mint1.example.com",
			"balance_sats": float64(1000),
			"token":        "token1",
		},
		map[string]interface{}{
			"mint_url":     "https://mint2.example.com",
			"balance_sats": float64(2000),
			"token":        "token2",
		},
	}
	data := map[string]interface{}{
		"tokens":     tokens,
		"total_sats": float64(3000),
	}

	err := saveTokensToFile(filename, tokens, data)
	if err != nil {
		t.Fatalf("saveTokensToFile failed: %v", err)
	}

	content, err := os.ReadFile(filename)
	if err != nil {
		t.Fatalf("Failed to read file: %v", err)
	}

	text := string(content)
	if !strings.Contains(text, "Token 1") {
		t.Error("missing Token 1")
	}
	if !strings.Contains(text, "Token 2") {
		t.Error("missing Token 2")
	}
	if !strings.Contains(text, "2 tokens") {
		t.Error("missing '2 tokens'")
	}
}

func TestSaveTokensToFileInvalidPath(t *testing.T) {
	err := saveTokensToFile("/nonexistent/dir/file.txt", []interface{}{}, map[string]interface{}{})
	if err == nil {
		t.Error("expected error for invalid path")
	}
}

func TestDisplayWalletDrainResultEmpty(t *testing.T) {
	output := captureOutput(func() {
		displayWalletDrainResult(map[string]interface{}{
			"tokens":     []interface{}{},
			"total_sats": float64(0),
		})
	})
	if !strings.Contains(output, "No tokens created") {
		t.Errorf("output should mention no tokens: %s", output)
	}
}

func TestDisplayMap(t *testing.T) {
	output := captureOutput(func() {
		displayMap(map[string]interface{}{
			"key1": "value1",
			"key2": 42,
			"key3": true,
		}, "")
	})
	if !strings.Contains(output, "key1: value1") {
		t.Errorf("output missing key1: %s", output)
	}
	if !strings.Contains(output, "key2: 42") {
		t.Errorf("output missing key2: %s", output)
	}
	if !strings.Contains(output, "key3: true") {
		t.Errorf("output missing key3: %s", output)
	}
}

func TestDisplayMapNested(t *testing.T) {
	output := captureOutput(func() {
		displayMap(map[string]interface{}{
			"outer": map[string]interface{}{
				"inner": "nested_value",
			},
		}, "")
	})
	if !strings.Contains(output, "outer") {
		t.Errorf("output missing outer key: %s", output)
	}
	if !strings.Contains(output, "nested_value") {
		t.Errorf("output missing nested value: %s", output)
	}
}

func TestExecuteServiceCommandUnknownAction(t *testing.T) {
	err := executeServiceCommand("fly")
	if err == nil {
		t.Error("expected error for unknown action")
	}
	if !strings.Contains(err.Error(), "unknown service action") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestCLIMessageSerialization(t *testing.T) {
	msg := CLIMessage{
		Command:   "wallet",
		Args:      []string{"balance"},
		Flags:     map[string]string{"test": "value"},
		Timestamp: time.Now(),
	}
	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("Failed to marshal: %v", err)
	}
	var parsed CLIMessage
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}
	if parsed.Command != "wallet" {
		t.Errorf("command: got %q, want 'wallet'", parsed.Command)
	}
	if len(parsed.Args) != 1 || parsed.Args[0] != "balance" {
		t.Errorf("args: got %v", parsed.Args)
	}
	if parsed.Flags["test"] != "value" {
		t.Errorf("flags: got %v", parsed.Flags)
	}
}

func TestCLIResponseSerialization(t *testing.T) {
	resp := CLIResponse{
		Success:   false,
		Error:     "test error",
		Timestamp: time.Now(),
	}
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Failed to marshal: %v", err)
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}
	if parsed["success"] != false {
		t.Error("success should be false")
	}
	if parsed["error"] != "test error" {
		t.Errorf("error: got %v", parsed["error"])
	}
	if _, ok := parsed["timestamp"]; !ok {
		t.Error("missing timestamp")
	}
}

func TestSendCommandNoSocket(t *testing.T) {
	_, err := sendCommand(CLIMessage{Command: "version"})
	if err == nil {
		t.Error("expected error when socket doesn't exist")
	}
	if !strings.Contains(err.Error(), "connect") {
		t.Errorf("expected connection error, got: %v", err)
	}
}

func TestJsonOutputFlag(t *testing.T) {
	if jsonOutput {
		t.Error("jsonOutput should default to false")
	}
}

func TestDisplayWalletDrainWithSaveToFile(t *testing.T) {
	tempDir := t.TempDir()
	savePath := filepath.Join(tempDir, "saved_tokens.txt")

	output := captureOutput(func() {
		displayWalletDrainResult(map[string]interface{}{
			"tokens": []interface{}{
				map[string]interface{}{
					"mint_url":     "https://mint.example.com",
					"balance_sats": float64(1000),
					"token":        "testToken",
				},
			},
			"total_sats":  float64(1000),
			"save_to_file": savePath,
		})
	})
	if !strings.Contains(output, "saved to") {
		t.Errorf("output should mention saved file: %s", output)
	}
	if _, err := os.Stat(savePath); os.IsNotExist(err) {
		t.Error("token file was not created")
	}
}

func TestCLIMessageOmitsEmptyFields(t *testing.T) {
	msg := CLIMessage{
		Command: "status",
	}
	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("Failed to marshal: %v", err)
	}
	var parsed map[string]interface{}
	json.Unmarshal(data, &parsed)
	if _, ok := parsed["args"]; ok {
		t.Error("empty args should be omitted")
	}
	if _, ok := parsed["flags"]; ok {
		t.Error("empty flags should be omitted")
	}
}

func TestDisplayResponseWithVersionData(t *testing.T) {
	output := captureOutput(func() {
		displayResponse(&CLIResponse{
			Success: true,
			Message: fmt.Sprintf("TollGate Version\nversion: %s\ncommit: %s", "v0.0.4", "abc123"),
			Data: map[string]string{
				"version":    "v0.0.4",
				"commit":     "abc123",
				"build_time": "2025-01-01",
				"go_version": "go1.24",
			},
		})
	})
	if !strings.Contains(output, "v0.0.4") {
		t.Errorf("output missing version: %s", output)
	}
}
