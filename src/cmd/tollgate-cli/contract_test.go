package main

import (
	"encoding/json"
	"testing"
	"time"
)

func TestCLIResponseJSONShape(t *testing.T) {
	resp := CLIResponse{
		Success: true,
		Message: "test message",
		Data: map[string]interface{}{
			"balance_sats": uint64(12345),
		},
		Timestamp: time.Now(),
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Failed to marshal CLIResponse: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("Failed to unmarshal CLIResponse: %v", err)
	}

	requiredFields := []string{"success", "message", "data", "timestamp"}
	for _, f := range requiredFields {
		if _, ok := parsed[f]; !ok {
			t.Errorf("CLIResponse JSON missing field: %s", f)
		}
	}

	if success, ok := parsed["success"].(bool); !ok || !success {
		t.Errorf("CLIResponse.success should be true bool, got %v", parsed["success"])
	}
}

func TestCLIResponseErrorShape(t *testing.T) {
	resp := CLIResponse{
		Success: false,
		Error:   "something went wrong",
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Failed to marshal error response: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	if _, ok := parsed["error"]; !ok {
		t.Error("Error response missing 'error' field")
	}
	if _, ok := parsed["success"]; !ok {
		t.Error("Error response missing 'success' field")
	}
}

func TestCLIMessageJSONShape(t *testing.T) {
	msg := CLIMessage{
		Command: "wallet",
		Args:    []string{"balance"},
		Flags:   map[string]string{"test": "value"},
	}

	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("Failed to marshal CLIMessage: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("Failed to unmarshal CLIMessage: %v", err)
	}

	if _, ok := parsed["command"]; !ok {
		t.Error("CLIMessage missing 'command' field")
	}
	if _, ok := parsed["args"]; !ok {
		t.Error("CLIMessage missing 'args' field")
	}
}

func TestWalletBalanceDataShape(t *testing.T) {
	data := map[string]interface{}{
		"balance_sats": uint64(5000),
		"address":      "",
		"drain_target": "",
	}

	jsonBytes, _ := json.Marshal(data)
	var parsed map[string]interface{}
	json.Unmarshal(jsonBytes, &parsed)

	if _, ok := parsed["balance_sats"]; !ok {
		t.Error("Wallet balance data missing 'balance_sats'")
	}
}

func TestServiceStatusDataShape(t *testing.T) {
	data := map[string]interface{}{
		"running":    true,
		"version":    "v0.0.4",
		"uptime":     "1h23m45s",
		"config_ok":  true,
		"wallet_ok":  true,
		"network_ok": true,
	}

	jsonBytes, _ := json.Marshal(data)
	var parsed map[string]interface{}
	json.Unmarshal(jsonBytes, &parsed)

	requiredFields := []string{"running", "version", "uptime", "config_ok", "wallet_ok", "network_ok"}
	for _, f := range requiredFields {
		if _, ok := parsed[f]; !ok {
			t.Errorf("ServiceStatus data missing field: %s", f)
		}
	}
}

func TestVersionDataShape(t *testing.T) {
	data := map[string]string{
		"version":         "v0.0.4",
		"commit":          "abc1234",
		"build_time":      "2025-01-01",
		"go_version":      "go1.24",
		"openwrt_version": "24.10.1",
	}

	jsonBytes, _ := json.Marshal(data)
	var parsed map[string]interface{}
	json.Unmarshal(jsonBytes, &parsed)

	requiredFields := []string{"version", "commit", "build_time", "go_version", "openwrt_version"}
	for _, f := range requiredFields {
		if _, ok := parsed[f]; !ok {
			t.Errorf("Version data missing field: %s", f)
		}
	}
}

func TestPrivateNetworkInfoShape(t *testing.T) {
	data := map[string]interface{}{
		"ssid":     "MyNetwork",
		"password": "secret123",
		"enabled":  true,
	}

	jsonBytes, _ := json.Marshal(data)
	var parsed map[string]interface{}
	json.Unmarshal(jsonBytes, &parsed)

	requiredFields := []string{"ssid", "password", "enabled"}
	for _, f := range requiredFields {
		if _, ok := parsed[f]; !ok {
			t.Errorf("PrivateNetworkInfo data missing field: %s", f)
		}
	}
}

func TestConfigGetResponseShape(t *testing.T) {
	data := map[string]interface{}{
		"config": map[string]interface{}{
			"config_version": "v0.0.7",
			"metric":         "bytes",
		},
		"identities": map[string]interface{}{
			"config_version": "v0.0.1",
		},
	}

	jsonBytes, _ := json.Marshal(data)
	var parsed map[string]interface{}
	json.Unmarshal(jsonBytes, &parsed)

	if _, ok := parsed["config"]; !ok {
		t.Error("Config get response missing 'config'")
	}
	if _, ok := parsed["identities"]; !ok {
		t.Error("Config get response missing 'identities'")
	}
}

func TestConfigSchemaResponseShape(t *testing.T) {
	data := map[string]interface{}{
		"config": []interface{}{
			map[string]interface{}{
				"name":     "Metric",
				"type":     "string",
				"json_key": "metric",
				"editable": true,
			},
		},
		"identities": []interface{}{
			map[string]interface{}{
				"name":     "ConfigVersion",
				"type":     "string",
				"json_key": "config_version",
				"editable": false,
			},
		},
	}

	jsonBytes, _ := json.Marshal(data)
	var parsed map[string]interface{}
	json.Unmarshal(jsonBytes, &parsed)

	for _, key := range []string{"config", "identities"} {
		arr, ok := parsed[key].([]interface{})
		if !ok || len(arr) == 0 {
			t.Errorf("Schema response %s is missing or empty", key)
		}
		first := arr[0].(map[string]interface{})
		for _, f := range []string{"name", "type", "json_key", "editable"} {
			if _, ok := first[f]; !ok {
				t.Errorf("Schema field missing key: %s", f)
			}
		}
	}
}

func TestHealthResponseShape(t *testing.T) {
	data := map[string]interface{}{
		"running":             true,
		"socket_ok":           true,
		"uptime":              "1h",
		"version":             "v0.0.4",
		"config_ok":           true,
		"wallet_ok":           true,
		"network_ok":          true,
		"healthy":             true,
		"wallet_balance_sats": uint64(500),
		"mint_count":          2,
		"metric":              "bytes",
	}

	jsonBytes, _ := json.Marshal(data)
	var parsed map[string]interface{}
	json.Unmarshal(jsonBytes, &parsed)

	requiredFields := []string{"running", "socket_ok", "healthy", "config_ok", "wallet_ok"}
	for _, f := range requiredFields {
		if _, ok := parsed[f]; !ok {
			t.Errorf("Health response missing field: %s", f)
		}
	}
}

func TestConfigSetResponseShape(t *testing.T) {
	data := map[string]interface{}{
		"key":   "metric",
		"value": "milliseconds",
	}

	jsonBytes, _ := json.Marshal(data)
	var parsed map[string]interface{}
	json.Unmarshal(jsonBytes, &parsed)

	for _, f := range []string{"key", "value"} {
		if _, ok := parsed[f]; !ok {
			t.Errorf("Config set response missing field: %s", f)
		}
	}
}

func TestWalletDrainDataShape(t *testing.T) {
	data := map[string]interface{}{
		"success":     true,
		"tokens":      []interface{}{},
		"total_sats":  uint64(1000),
		"save_to_file": "drain.txt",
	}

	jsonBytes, _ := json.Marshal(data)
	var parsed map[string]interface{}
	json.Unmarshal(jsonBytes, &parsed)

	for _, f := range []string{"success", "tokens", "total_sats"} {
		if _, ok := parsed[f]; !ok {
			t.Errorf("Wallet drain response missing field: %s", f)
		}
	}
}

func TestWalletFundDataShape(t *testing.T) {
	data := map[string]interface{}{
		"amount_received": uint64(500),
	}

	jsonBytes, _ := json.Marshal(data)
	var parsed map[string]interface{}
	json.Unmarshal(jsonBytes, &parsed)

	if _, ok := parsed["amount_received"]; !ok {
		t.Error("Wallet fund response missing 'amount_received'")
	}
}
