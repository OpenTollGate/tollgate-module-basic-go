package wireless_gateway_manager

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUCIFailureDoesNotLogOrReturnCredentialValues(t *testing.T) {
	directory := t.TempDir()
	if e := os.WriteFile(filepath.Join(directory, "uci"), []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" >&2\nexit 1\n"), 0700); e != nil {
		t.Fatal(e)
	}
	t.Setenv("PATH", directory)
	var log bytes.Buffer
	output := logger.Logger.Out
	logger.Logger.SetOutput(&log)
	defer logger.Logger.SetOutput(output)
	const marker = "synthetic-unit-only-credential-marker"
	_, e := (&Connector{}).ExecuteUCI("set", "wireless.synthetic.key="+marker)
	if e == nil {
		t.Fatal("failed UCI was accepted")
	}
	if bytes.Contains(log.Bytes(), []byte(marker)) || strings.Contains(e.Error(), marker) {
		t.Fatal("credential value escaped the UCI failure boundary")
	}
}
