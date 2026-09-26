package valve

import (
	"errors"
	"strings"
	"testing"

	"github.com/sirupsen/logrus"
)

// The client-list seam (`ndsctl json`, no argument) is the only read in this
// package that asks about the ENFORCEMENT layer as a whole rather than about one
// MAC, and the startup reconciliation decides from it. So the two things worth
// pinning are: the payload the router actually prints is parsed the way it is
// meant (including the state that means "this client holds access"), and an
// answer the module could not read is NEVER reported as "NoDogSplash holds
// nobody" — the caller must not act on a fact it does not have.

// benchClientList is the shape measured on the bench MT3000 (pre17,
// 2026-09-26), abbreviated to the fields this package reads. The map key and the
// record's own `mac` agree, as they do on the router.
const benchClientList = `{
"client_length": 3,
"clients":{
"a8:a0:92:a5:39:7a":{
"id":2,
"ip":"192.168.1.124",
"mac":"a8:a0:92:a5:39:7a",
"added":0,
"active":1790419507,
"duration":0,
"token":"d4fceff0",
"state":"Authenticated",
"downloaded":2367,
"avg_down_speed":0.00,
"uploaded":64,
"avg_up_speed":0.00
},
"8c:16:45:0d:6f:c5":{
"id":3,
"ip":"192.168.1.200",
"mac":"8c:16:45:0d:6f:c5",
"added":0,
"active":1790421311,
"duration":0,
"token":"0f1d2a3b",
"state":"Preauthenticated",
"downloaded":0,
"avg_down_speed":0.00,
"uploaded":0,
"avg_up_speed":0.00
},
"02:11:22:33:44:55":{
"id":4,
"ip":"192.168.1.201",
"mac":"02:11:22:33:44:55",
"added":0,
"active":1790421320,
"duration":0,
"token":"9a8b7c6d",
"state":"Authenticated",
"downloaded":8192,
"avg_down_speed":0.00,
"uploaded":128,
"avg_up_speed":0.00
}
}
}`

func TestParseNdsctlClientListReadsTheBenchPayload(t *testing.T) {
	records, err := parseNdsctlClientList(benchClientList)
	if err != nil {
		t.Fatalf("the payload the router prints must parse: %v", err)
	}

	if len(records) != 3 {
		t.Fatalf("parsed %d records, want 3: %+v", len(records), records)
	}

	// Stable, MAC-sorted order: an operator report and a test read the same way.
	want := []string{"02:11:22:33:44:55", "8c:16:45:0d:6f:c5", "a8:a0:92:a5:39:7a"}
	for i, mac := range want {
		if records[i].MAC != mac {
			t.Fatalf("records[%d].MAC = %q, want %q (the list must be MAC-sorted)", i, records[i].MAC, mac)
		}
	}

	// The state decides access, and only "Authenticated" carries it.
	byMAC := map[string]ClientRecord{}
	for _, record := range records {
		byMAC[record.MAC] = record
	}

	for _, mac := range []string{"02:11:22:33:44:55", "a8:a0:92:a5:39:7a"} {
		if !byMAC[mac].Authorised() {
			t.Fatalf("%s is Authenticated in the payload and must count as holding access", mac)
		}
	}
	if byMAC["8c:16:45:0d:6f:c5"].Authorised() {
		t.Fatal("a Preauthenticated client cannot pass traffic past the captive portal: it must NOT count as holding access")
	}
	if !strings.EqualFold(byMAC["a8:a0:92:a5:39:7a"].State, "authenticated") {
		t.Fatalf("the record must carry the state the payload reported, got %q", byMAC["a8:a0:92:a5:39:7a"].State)
	}
	if byMAC["a8:a0:92:a5:39:7a"].IP != "192.168.1.124" {
		t.Fatalf("the record must carry the client's address, got %q", byMAC["a8:a0:92:a5:39:7a"].IP)
	}
}

// TestParseNdsctlClientListTreatsAnUnknownStateAsNotAuthorised pins the
// direction of the guess: an unrecognised state must never be read as access.
func TestParseNdsctlClientListTreatsAnUnknownStateAsNotAuthorised(t *testing.T) {
	records, err := parseNdsctlClientList(`{"client_length":1,"clients":{"02:11:22:33:44:55":{"mac":"02:11:22:33:44:55","state":"Authenticated-ish"}}}`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(records) != 1 || records[0].Authorised() {
		t.Fatalf("an unrecognised state must not be read as authorisation: %+v", records)
	}
}

// TestParseNdsctlClientListHandlesTheEmptyAnswers: NoDogSplash holding no client
// is a real answer, and it is not an error.
func TestParseNdsctlClientListHandlesTheEmptyAnswers(t *testing.T) {
	for _, payload := range []string{"", "{}", "\n{} \n", `{"client_length":0,"clients":{}}`} {
		records, err := parseNdsctlClientList(payload)
		if err != nil {
			t.Fatalf("payload %q: an empty client list is not an error: %v", payload, err)
		}
		if len(records) != 0 {
			t.Fatalf("payload %q: parsed %d records, want 0", payload, len(records))
		}
	}
}

// TestParseNdsctlClientListRejectsAMalformedPayload: a payload this package
// cannot read must surface as an error, never as "no clients".
func TestParseNdsctlClientListRejectsAMalformedPayload(t *testing.T) {
	if _, err := parseNdsctlClientList("Failed to send request: Operation not permitted"); err == nil {
		t.Fatal("a non-JSON answer (which is what a wedged socket or a permission problem prints) must not read as an empty client list")
	}
}

// TestListClientsReportsAnUnreadableListAsAnError: the seam must never turn a
// failure to read into a fact about the enforcement layer, and an invocation the
// MODULE ended must not be escalated as an ndsctl failure (the attribution
// contract of this card, applied to the new read).
func TestListClientsReportsAnUnreadableListAsAnError(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{"ndsctl refusing", errors.New("exit status 1: Failed to send request: Operation not permitted")},
		{"the module's own deadline", newNdsctlInterruption(ndsctlOutcomeTimedOut, []string{"json"}, "", ErrNdsctlTimeout)},
		{"a shutdown", newNdsctlInterruption(ndsctlOutcomeStopping, []string{"json"}, "", ErrNdsctlStopped)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			swapNdsctlRunner(t, func(args ...string) (string, error) {
				if len(args) != 1 || args[0] != "json" {
					t.Fatalf("ListClients must read the CLIENT LIST (`ndsctl json` with no argument), got %v", args)
				}
				return "", tc.err
			})

			buffer := captureValveLogAtLevel(t, logrus.ErrorLevel)

			if _, err := ListClients(); err == nil {
				t.Fatal("a client list the module could not read must be an error, never an empty list")
			}

			if tc.name != "ndsctl refusing" && strings.Contains(buffer.String(), "Error executing ndsctl json for the client list") {
				t.Fatalf("%s: an invocation the MODULE ended must not be escalated as an ndsctl failure\nlog:\n%s", tc.name, buffer.String())
			}
		})
	}
}

// TestAuthorisedClientsReturnsOnlyTheClientsThatHoldAccess is the question the
// startup reconciliation actually asks.
func TestAuthorisedClientsReturnsOnlyTheClientsThatHoldAccess(t *testing.T) {
	swapNdsctlRunner(t, func(args ...string) (string, error) { return benchClientList, nil })

	authorised, err := AuthorisedClients()
	if err != nil {
		t.Fatalf("AuthorisedClients: %v", err)
	}

	want := []string{"02:11:22:33:44:55", "a8:a0:92:a5:39:7a"}
	if len(authorised) != len(want) {
		t.Fatalf("authorised clients = %v, want %v (the Preauthenticated client must not be in it)", authorised, want)
	}
	for i := range want {
		if authorised[i] != want[i] {
			t.Fatalf("authorised clients = %v, want %v", authorised, want)
		}
	}
}

// swapNdsctlRunner replaces the invocation seam for one test and restores it
// afterwards, stopping any close retry the test's calls may have armed so a
// leaked timer cannot fire into the next test.
func swapNdsctlRunner(t *testing.T, runner func(args ...string) (string, error)) {
	t.Helper()

	previousRunner := runNdsctl
	previousInterval := ndsctlTimeoutReportInterval
	runNdsctl = runner

	t.Cleanup(func() {
		gatesMutex.Lock()
		for macAddress, timer := range pendingCloseRetries {
			timer.Stop()
			delete(pendingCloseRetries, macAddress)
		}
		gatesMutex.Unlock()

		runNdsctl = previousRunner
		ndsctlTimeoutReportInterval = previousInterval
		resetNdsctlReportState()
	})
}
