package cli

import (
	"strings"
	"testing"
)

func TestSetUCIValue_RejectsNewlineInKey(t *testing.T) {
	err := setUCIValue("wireless\nrm -rf /", "TollGate")
	if err == nil {
		t.Error("setUCIValue should reject newline in key")
	}
}

func TestSetUCIValue_RejectsNewlineInValue(t *testing.T) {
	err := setUCIValue("wireless.ssid", "TollGate\nrm -rf /")
	if err == nil {
		t.Error("setUCIValue should reject newline in value")
	}
}

func TestSetUCIValue_RejectsCarriageReturn(t *testing.T) {
	err := setUCIValue("wireless\r.ssid", "value")
	if err == nil {
		t.Error("setUCIValue should reject carriage return in key")
	}
}

func TestSetUCIValue_RejectsNullByte(t *testing.T) {
	err := setUCIValue("wireless\x00.ssid", "value")
	if err == nil {
		t.Error("setUCIValue should reject null byte in key")
	}
}

func TestSetUCIValue_AcceptsValidKey(t *testing.T) {
	// In test env, uci binary doesn't exist, so exec.Command will fail.
	// The test passes if the error is NOT a validation error.
	err := setUCIValue("wireless.@wifi-iface[0].ssid", "TollGate-ABCD")
	if err != nil {
		errMsg := err.Error()
		for _, substr := range []string{"control characters", "invalid UCI key", "invalid UCI value"} {
			if strings.Contains(errMsg, substr) {
				t.Errorf("valid key/value rejected by validation: %v", err)
			}
		}
	}
}
