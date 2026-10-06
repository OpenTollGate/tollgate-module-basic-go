package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"
)

// writeTestJournal writes raw JSONL lines to the journal path implied by
// TOLLGATE_TEST_CONFIG_DIR (set by newDrainTestServer).
func writeTestJournal(t *testing.T, lines ...string) {
	t.Helper()
	path := drainJournalPath()
	if err := os.MkdirAll(dirOf(path), 0o700); err != nil {
		t.Fatalf("mkdir journal dir: %v", err)
	}
	if err := os.WriteFile(path, []byte(joinLines(lines)), 0o600); err != nil {
		t.Fatalf("write journal: %v", err)
	}
}

func dirOf(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			return path[:i]
		}
	}
	return "."
}

func joinLines(lines []string) string {
	out := ""
	for _, line := range lines {
		out += line + "\n"
	}
	return out
}

func journalLine(t *testing.T, mintURL string, amount uint64, token string) string {
	t.Helper()
	data, err := json.Marshal(drainJournalEntry{
		Timestamp:  time.Now().UTC(),
		MintURL:    mintURL,
		AmountSats: amount,
		Token:      token,
	})
	if err != nil {
		t.Fatalf("marshal journal entry: %v", err)
	}
	return string(data)
}

// recoverResultJSON runs handleWalletRecover and unmarshals its Data into
// the WalletRecoverResult wire shape.
func recoverResultJSON(t *testing.T, s *CLIServer) (CLIResponse, map[string]interface{}) {
	t.Helper()
	resp := s.handleWalletRecover()
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	return resp, parsed
}

func recoverData(t *testing.T, parsed map[string]interface{}) map[string]interface{} {
	t.Helper()
	data, ok := parsed["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("response has no data object: %v", parsed)
	}
	return data
}

func TestHandleWalletRecover_LiveSpentUnknown(t *testing.T) {
	const (
		liveToken    = "token-live"
		spentToken   = "token-spent"
		unknownToken = "token-unknown"
	)
	m := &scriptedDrainMerchant{
		spendable: map[string]bool{liveToken: true, spentToken: false},
		checkErr:  map[string]error{unknownToken: fmt.Errorf("mint unreachable")},
	}
	s := newDrainTestServer(t, m)
	writeTestJournal(t,
		journalLine(t, "https://mint-a.test/Bitcoin", 50, liveToken),
		journalLine(t, "https://mint-b.test/Bitcoin", 30, spentToken),
		journalLine(t, "https://mint-c.test/Bitcoin", 20, unknownToken),
	)

	resp, parsed := recoverResultJSON(t, s)
	data := recoverData(t, parsed)

	if resp.Success {
		t.Fatalf("undeterminable token state must fail the command: %v", parsed)
	}
	for field, want := range map[string]float64{"checked": 3, "live": 1, "spent": 1, "unknown": 1, "live_sats": 50} {
		if got := data[field].(float64); got != want {
			t.Errorf("%s = %v, want %v", field, got, want)
		}
	}
	tokens, _ := data["tokens"].([]interface{})
	if len(tokens) != 1 {
		t.Fatalf("exactly the live token must be recoverable, got: %v", tokens)
	}
	live := tokens[0].(map[string]interface{})
	if live["token"] != liveToken || live["mint_url"] != "https://mint-a.test/Bitcoin" || live["balance_sats"] != float64(50) {
		t.Errorf("live token record wrong: %v", live)
	}
}

func TestHandleWalletRecover_AllClassified_Succeeds(t *testing.T) {
	m := &scriptedDrainMerchant{
		spendable: map[string]bool{"token-a": true, "token-b": false},
	}
	s := newDrainTestServer(t, m)
	writeTestJournal(t,
		journalLine(t, "https://mint-a.test/Bitcoin", 50, "token-a"),
		journalLine(t, "https://mint-b.test/Bitcoin", 30, "token-b"),
	)

	resp, parsed := recoverResultJSON(t, s)
	if !resp.Success {
		t.Fatalf("fully classified journal must succeed: %v", parsed)
	}
	data := recoverData(t, parsed)
	if data["live"].(float64) != 1 || data["spent"].(float64) != 1 {
		t.Errorf("counts wrong: %v", data)
	}
}

func TestHandleWalletRecover_NoJournal(t *testing.T) {
	m := &scriptedDrainMerchant{}
	s := newDrainTestServer(t, m)

	resp, parsed := recoverResultJSON(t, s)
	if !resp.Success {
		t.Fatalf("missing journal is not an error: %v", parsed)
	}
	data := recoverData(t, parsed)
	if data["checked"].(float64) != 0 {
		t.Errorf("checked = %v, want 0", data["checked"])
	}
}

func TestHandleWalletRecover_InvalidLine_FailsLoudly(t *testing.T) {
	m := &scriptedDrainMerchant{
		spendable: map[string]bool{"token-a": true},
	}
	s := newDrainTestServer(t, m)
	writeTestJournal(t,
		journalLine(t, "https://mint-a.test/Bitcoin", 50, "token-a"),
		`{"timestamp": "not-a-real-entry"`,
	)

	resp, parsed := recoverResultJSON(t, s)
	if resp.Success {
		t.Fatalf("corrupt journal line must fail the command: %v", parsed)
	}
	data := recoverData(t, parsed)
	if data["invalid"].(float64) != 1 {
		t.Errorf("invalid = %v, want 1", data["invalid"])
	}
	if data["live"].(float64) != 1 {
		t.Errorf("parseable entries must still be classified, live = %v", data["live"])
	}
}

func TestHandleWalletRecover_NilMerchant(t *testing.T) {
	s := NewCLIServer(nil, nil, nil, nil, nil)
	resp := s.handleWalletRecover()
	if resp.Success {
		t.Fatalf("nil merchant must be an error, got: %v", resp)
	}
}
