package cli

import (
	"regexp"
	"strings"
	"testing"
)

func TestRandomWord(t *testing.T) {
	words := []string{"alpha", "bravo", "charlie"}
	word, err := randomWord(words)
	if err != nil {
		t.Fatalf("randomWord returned error: %v", err)
	}
	found := false
	for _, w := range words {
		if word == w {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("randomWord returned %q, not in word list", word)
	}
}

func TestRandomWordEmptyList(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for empty word list")
		}
	}()
	randomWord([]string{})
}

func TestRandomWordSingleElement(t *testing.T) {
	words := []string{"only"}
	word, err := randomWord(words)
	if err != nil {
		t.Fatalf("randomWord returned error: %v", err)
	}
	if word != "only" {
		t.Errorf("expected 'only', got %q", word)
	}
}

func TestGenerateRandomPasswordFormat(t *testing.T) {
	pw, err := generateRandomPassword()
	if err != nil {
		t.Fatalf("generateRandomPassword returned error: %v", err)
	}

	pattern := regexp.MustCompile(`^[A-Z][a-z]+-[A-Z][a-z]+-[A-Z][a-z]+-\d{2}$`)
	if !pattern.MatchString(pw) {
		t.Errorf("password %q does not match expected format Word-Word-Word-NN", pw)
	}
}

func TestGenerateRandomPasswordLength(t *testing.T) {
	pw, err := generateRandomPassword()
	if err != nil {
		t.Fatalf("generateRandomPassword returned error: %v", err)
	}
	if len(pw) < 8 {
		t.Errorf("password %q is too short (%d chars), expected at least 8", pw, len(pw))
	}
	if len(pw) > 63 {
		t.Errorf("password %q is too long (%d chars), expected at most 63 for WPA2", pw, len(pw))
	}
}

func TestGenerateRandomPasswordComponents(t *testing.T) {
	pw, err := generateRandomPassword()
	if err != nil {
		t.Fatalf("generateRandomPassword returned error: %v", err)
	}

	parts := strings.Split(pw, "-")
	if len(parts) != 4 {
		t.Fatalf("expected 4 dash-separated parts, got %d: %q", len(parts), pw)
	}

	for i, part := range parts[:3] {
		if len(part) == 0 {
			t.Errorf("word part %d is empty", i)
			continue
		}
		if part[0] < 'A' || part[0] > 'Z' {
			t.Errorf("word part %d (%q) does not start with uppercase", i, part)
		}
		if len(part) > 1 && part[1:] != strings.ToLower(part[1:]) {
			t.Errorf("word part %d (%q) has uppercase after first char", i, part)
		}
	}

	numPart := parts[3]
	if len(numPart) != 2 {
		t.Errorf("number part %q is not 2 digits", numPart)
	}
	for _, c := range numPart {
		if c < '0' || c > '9' {
			t.Errorf("number part %q contains non-digit", numPart)
			break
		}
	}
}

func TestGenerateRandomPasswordUniqueness(t *testing.T) {
	passwords := make(map[string]bool)
	for i := 0; i < 100; i++ {
		pw, err := generateRandomPassword()
		if err != nil {
			t.Fatalf("generateRandomPassword %d returned error: %v", i, err)
		}
		passwords[pw] = true
	}
	if len(passwords) < 90 {
		t.Errorf("only %d unique passwords out of 100, expected >= 90", len(passwords))
	}
}

func TestGenerateRandomPasswordWPA2Compliance(t *testing.T) {
	for i := 0; i < 50; i++ {
		pw, err := generateRandomPassword()
		if err != nil {
			t.Fatalf("generateRandomPassword %d returned error: %v", i, err)
		}
		if len(pw) < 8 {
			t.Errorf("password %q too short for WPA2 (need 8-63 chars)", pw)
		}
		if len(pw) > 63 {
			t.Errorf("password %q too long for WPA2 (need 8-63 chars)", pw)
		}
	}
}

func TestGenerateRandomPasswordUsesCryptoRand(t *testing.T) {
	histogram := make(map[string]int)
	n := 500
	for i := 0; i < n; i++ {
		pw, err := generateRandomPassword()
		if err != nil {
			t.Fatalf("generateRandomPassword %d returned error: %v", i, err)
		}
		histogram[pw]++
	}
	maxFreq := 0
	for _, count := range histogram {
		if count > maxFreq {
			maxFreq = count
		}
	}
	if maxFreq > 5 {
		t.Errorf("most frequent password appeared %d times out of %d, expected uniform distribution", maxFreq, n)
	}
}

func TestHandleNetworkCommandNoArgs(t *testing.T) {
	s := &CLIServer{}
	resp := s.handleNetworkCommand([]string{}, nil)
	if resp.Success {
		t.Error("expected failure for empty args")
	}
	if resp.Error == "" {
		t.Error("expected error message")
	}
}

func TestHandleNetworkCommandUnknownSubcommand(t *testing.T) {
	s := &CLIServer{}
	resp := s.handleNetworkCommand([]string{"vpn"}, nil)
	if resp.Success {
		t.Error("expected failure for unknown subcommand")
	}
	if !strings.Contains(resp.Error, "Unknown network subcommand") {
		t.Errorf("error message doesn't mention unknown subcommand: %s", resp.Error)
	}
}

func TestHandlePrivateNetworkCommandNoArgs(t *testing.T) {
	s := &CLIServer{}
	resp := s.handlePrivateNetworkCommand([]string{}, nil)
	if resp.Success {
		t.Error("expected failure for empty args")
	}
}

func TestHandlePrivateNetworkCommandUnknownAction(t *testing.T) {
	s := &CLIServer{}
	resp := s.handlePrivateNetworkCommand([]string{"jump"}, nil)
	if resp.Success {
		t.Error("expected failure for unknown action")
	}
}

func TestHandlePrivateNetworkRenameNoSSID(t *testing.T) {
	s := &CLIServer{}
	resp := s.handlePrivateNetworkCommand([]string{"rename"}, nil)
	if resp.Success {
		t.Error("expected failure for rename without SSID")
	}
}

func TestHandlePrivateNetworkSetPasswordAutoGenerate(t *testing.T) {
	s := &CLIServer{}
	resp := s.handlePrivateNetworkCommand([]string{"set-password"}, nil)
	if resp.Success {
		t.Error("expected failure (uci not available), but should attempt auto-generation")
	}
}

func TestHandlePrivateNetworkRenameEmptySSID(t *testing.T) {
	s := &CLIServer{}
	resp := s.handlePrivateNetworkRename("")
	if resp.Success {
		t.Error("expected failure for empty SSID")
	}
	if !strings.Contains(resp.Error, "empty") && !strings.Contains(resp.Error, "cannot be empty") {
		t.Errorf("expected empty SSID error, got: %s", resp.Error)
	}
}

func TestHandlePrivateNetworkSetPasswordTooShort(t *testing.T) {
	s := &CLIServer{}
	resp := s.handlePrivateNetworkSetPassword("short")
	if resp.Success {
		t.Error("expected failure for short password")
	}
	if !strings.Contains(resp.Error, "8") {
		t.Errorf("expected WPA2 length error mentioning 8, got: %s", resp.Error)
	}
}

func TestHandlePrivateNetworkSetPasswordTooLong(t *testing.T) {
	s := &CLIServer{}
	longPw := strings.Repeat("a", 64)
	resp := s.handlePrivateNetworkSetPassword(longPw)
	if resp.Success {
		t.Error("expected failure for too-long password")
	}
	if !strings.Contains(resp.Error, "63") {
		t.Errorf("expected WPA2 length error mentioning 63, got: %s", resp.Error)
	}
}
