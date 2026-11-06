package cli

import (
	"bytes"
	"testing"

	"github.com/sirupsen/logrus"
)

func TestCommandLoggingDoesNotRetainCredentialArguments(t *testing.T) {
	var captured bytes.Buffer
	oldOut, oldLevel := cliLogger.Logger.Out, cliLogger.Logger.GetLevel()
	cliLogger.Logger.SetOutput(&captured)
	cliLogger.Logger.SetLevel(logrus.DebugLevel)
	defer func() { cliLogger.Logger.SetOutput(oldOut); cliLogger.Logger.SetLevel(oldLevel) }()
	const marker = "synthetic-private-cli-argument-marker"
	s := newTestCLIServer(nil, nil)
	s.processCommand(CLIMessage{Command: "unsupported", Args: []string{marker}})
	if bytes.Contains(captured.Bytes(), []byte(marker)) {
		t.Fatal("CLI arguments escaped into debug logging")
	}
}
