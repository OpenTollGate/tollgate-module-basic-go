package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// The audit found this leak by reading source: `private_status` was a READ tool
// (callable by any Nostr keypair) whose response carried the private network's
// live WPA passphrase, read out of UCI and marshalled as "password". src/cli's
// own redaction walks the config SCHEMA and never sees it, because the value
// comes from UCI, not config.json.

func TestRedactStripsPrivateNetworkPassword(t *testing.T) {
	// The exact shape src/cli returns for `network private status`
	// (PrivateNetworkInfo: SSID, Password, Enabled).
	resp := CLIResponse{
		Success: true,
		Data: map[string]interface{}{
			"ssid":     "TollGate-ABCD",
			"password": "correct-horse-battery",
			"enabled":  true,
		},
	}
	got := RedactResponse(resp)

	raw, err := json.Marshal(got.Data)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), "correct-horse-battery") {
		t.Fatalf("passphrase survived redaction: %s", raw)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// The withholding must be VISIBLE — a bare "" reads as "not configured".
	if m["password_set"] != true {
		t.Errorf("password_set marker missing: %s", raw)
	}
	// Non-secret fields must survive; over-redacting everything would make the
	// tool useless and hide the real problem.
	if m["ssid"] != "TollGate-ABCD" || m["enabled"] != true {
		t.Errorf("redaction removed non-secret fields: %s", raw)
	}
}

func TestRedactIsDeepAndCaseInsensitive(t *testing.T) {
	resp := CLIResponse{Success: true, Data: map[string]interface{}{
		"mints": []interface{}{
			map[string]interface{}{"url": "https://mint.example", "token": "cashuBxxx"},
		},
		"identities": map[string]interface{}{
			"owned": []interface{}{map[string]interface{}{"Name": "merchant", "PrivateKey": "deadbeef"}},
		},
		"nsec": "nsec1shouldnotleak",
	}}
	got := RedactResponse(resp)
	raw, _ := json.Marshal(got.Data)
	for _, leaked := range []string{"cashuBxxx", "deadbeef", "nsec1shouldnotleak"} {
		if strings.Contains(string(raw), leaked) {
			t.Errorf("secret %q survived: %s", leaked, raw)
		}
	}
	if !strings.Contains(string(raw), "https://mint.example") {
		t.Errorf("non-secret mint url was dropped: %s", raw)
	}
}

// A response that cannot be inspected must be WITHHELD, not passed through.
func TestRedactWithholdsUninspectable(t *testing.T) {
	resp := CLIResponse{Success: true, Data: func() {}} // functions do not marshal
	got := RedactResponse(resp)
	raw, _ := json.Marshal(got.Data)
	if !strings.Contains(string(raw), "redacted") {
		t.Fatalf("uninspectable payload was passed through: %s", raw)
	}
}

// Every tool must go through the choke point. This is the structural half: it
// fails if a future tool is dispatched without redaction, which is the only way
// the leak class can come back.
func TestDispatcherAlwaysRedacts(t *testing.T) {
	bridge := &FakeBridge{fn: func(msg CLIMessage) (CLIResponse, error) {
		return CLIResponse{Success: true, Data: map[string]interface{}{
			"ssid": "TG", "password": "leak-me-if-you-can",
		}}, nil
	}}
	d := NewDispatcher(NewAuthorizer(""), bridge)
	params, _ := json.Marshal(map[string]interface{}{"name": "private_status"})
	resp := d.Dispatch("anyone", JSONRPCRequest{JSONRPC: "2.0", ID: float64(1), Method: "tools/call", Params: params})
	if resp.Error != nil {
		t.Fatalf("call errored: %+v", resp.Error)
	}
	if strings.Contains(string(resp.Result), "leak-me-if-you-can") {
		t.Fatalf("dispatcher returned a secret: %s", resp.Result)
	}
}

// ---- freshness / replay (audit Q1) ----

// A stale event must be refused: without this, a captured gift wrap can be
// re-broadcast later and executed again — and one captured
// `config_set maintainer_npub <old>` would silently revert the router's owner.
func TestFreshnessRejectsStaleEvents(t *testing.T) {
	now := time.Now()
	old := nostrTimestamp(now.Add(-10 * time.Minute))
	if err := CheckFreshness(old, now); err == nil {
		t.Fatal("a 10-minute-old event was accepted — capture-and-replay-later is open")
	}
	fresh := nostrTimestamp(now.Add(-5 * time.Second))
	if err := CheckFreshness(fresh, now); err != nil {
		t.Fatalf("a 5-second-old event was refused: %v", err)
	}
}

// A far-future event is also refused (a relay holding a request to deliver
// "later" is the ordering half of the same attack).
func TestFreshnessRejectsFutureEvents(t *testing.T) {
	now := time.Now()
	future := nostrTimestamp(now.Add(10 * time.Minute))
	if err := CheckFreshness(future, now); err == nil {
		t.Fatal("a far-future event was accepted")
	}
}

// Duplicate delivery of the SAME event id must execute once.
func TestSeenSetDropsDuplicates(t *testing.T) {
	s := NewSeenSet(1024, 5*time.Minute)
	now := time.Now()
	if s.Seen("evt1", now) {
		t.Fatal("first sighting reported as already seen")
	}
	if !s.Seen("evt1", now) {
		t.Fatal("second sighting of the same event was NOT dropped — duplicate execution")
	}
	if s.Seen("evt2", now) {
		t.Fatal("a different event was treated as a duplicate")
	}
}

func TestSeenSetExpires(t *testing.T) {
	s := NewSeenSet(8, 50*time.Millisecond)
	now := time.Now()
	s.Seen("evt1", now)
	// After the TTL the entry must be gone (the set is bounded, not a leak).
	if s.Seen("evt1", now.Add(100*time.Millisecond)) {
		t.Fatal("entry did not expire — the set grows without bound")
	}
}

func TestSeenSetIsBounded(t *testing.T) {
	s := NewSeenSet(4, time.Hour)
	now := time.Now()
	for i := 0; i < 100; i++ {
		s.Seen(string(rune('a'+i%26))+string(rune('0'+i%10)), now)
	}
	if n := s.Len(); n > 4 {
		t.Fatalf("seen set grew to %d entries, cap is 4", n)
	}
}
