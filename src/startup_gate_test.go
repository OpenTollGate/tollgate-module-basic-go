package main

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/merchant"
)

// constructedMerchant is what the fake construction below returns: a
// startingMerchant with recognisable answers, so a test can tell "this request
// reached the constructed merchant" from "this request was refused by the
// startup gate" and from "this request hit the placeholder".
type constructedMerchant struct{ startingMerchant }

func (constructedMerchant) GetAdvertisement() string {
	return `{"status":1,"advertisement":"constructed"}`
}

func (constructedMerchant) GetUsage(string) (string, error) { return "3/10", nil }

// TestMoneyPathAcceptsWhileMintDependentInitIsInFlight is the regression test
// for the cold-boot blind money path (t_54359164: with 7 accepted mints and the
// mint fronts accepting-and-never-answering, :2121 did not accept a connection
// for 347.1 s, measured on a host with the shipped binary).
//
// It drives the real boot sequence — bootSequence(), the same function init()
// runs, on an ephemeral port — and substitutes the one thing a test cannot let
// run: the mint-dependent construction. That substitution is a function that
// blocks until the test releases it, which is exactly the cold-boot shape
// (mint probes and a wallet load that take minutes).
//
// The sequence it asserts is the fix, in order:
//
//  1. while that construction is in flight, the API has already accepted a
//     connection (this is what used to take 347.1 s);
//  2. a mint-dependent request is answered explicitly ("starting", 503, with a
//     Retry-After) instead of dropped or hung;
//  3. the CLI Unix socket exists — `tollgate wallet balance` used to fail with
//     ENOENT for the same minutes the API was missing;
//  4. once the construction returns, the gate opens on the merchant it
//     produced, and the same endpoint stops refusing.
//
// A mutant that constructs before binding (the pre-fix order) fails at (1).
func TestMoneyPathAcceptsWhileMintDependentInitIsInFlight(t *testing.T) {
	// This test stands in for a process that is booting: a fresh, closed gate
	// and no listener. Everything it overwrites is put back afterwards, so the
	// rest of the package's tests see the process as init() left it.
	//
	// The CLI socket the boot below starts goes in a throwaway directory: a
	// second server on the test build's own socket path would leave the
	// process's original one bound to an unlinked path, and CLIServer.Stop() is
	// NOT called here on purpose — `Stop` writes `s.running` while
	// `acceptConnections` reads it, an unsynchronized pair in src/cli that has
	// no caller in the repository today (this test would be the first) and is
	// therefore reported to the board rather than exercised from here. The
	// socket this test starts is left to the test binary's exit, like the one
	// init() leaves.
	t.Setenv("TOLLGATE_TEST_CONFIG_DIR", t.TempDir())

	prevProvider, prevListener, prevServer, prevGate, prevCLI, prevUpstream :=
		merchantProvider, apiListener, apiHTTPServer, apiStartup, cliServer, upstreamManager
	defer func() {
		if apiHTTPServer != nil && apiHTTPServer != prevServer {
			_ = apiHTTPServer.Close()
		}
		merchantProvider, apiListener, apiHTTPServer, apiStartup, cliServer, upstreamManager =
			prevProvider, prevListener, prevServer, prevGate, prevCLI, prevUpstream
	}()

	apiStartup = newStartupGate()
	apiListener = nil
	apiHTTPServer = nil

	constructing := make(chan struct{})
	releaseConstruction := make(chan struct{})

	bootDone := make(chan error, 1)
	go func() {
		bootDone <- bootSequence("127.0.0.1:0", func(*config_manager.ConfigManager) (merchant.MerchantInterface, error) {
			close(constructing)
			<-releaseConstruction
			return constructedMerchant{}, nil
		})
	}()

	select {
	case <-constructing:
	case <-time.After(30 * time.Second):
		t.Fatal("the boot sequence never reached the mint-dependent construction")
	}

	// ---- the mint-dependent construction is in flight right now ----

	if apiListener == nil {
		t.Fatal("the payment API was not bound before the mint-dependent construction: the money path would be blind for as long as the construction lasts")
	}
	addr := apiListener.Addr().String()

	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatalf("the payment API did not accept a connection while the mint-dependent init was in flight: %v", err)
	}
	_ = conn.Close()

	if dir := os.Getenv("TOLLGATE_TEST_CONFIG_DIR"); dir != "" {
		if _, err := os.Stat(filepath.Join(dir, "tollgate.sock")); err != nil {
			t.Errorf("the CLI socket did not exist while the mint-dependent init was in flight: %v", err)
		}
	}

	client := &http.Client{Timeout: 5 * time.Second}

	var refusal lightningInvoiceResponse
	resp := getJSON(t, client, "http://"+addr+"/balance", &refusal)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("/balance answered %d while the merchant was still being constructed, want %d", resp.StatusCode, http.StatusServiceUnavailable)
	}
	if refusal.Code != codeStarting {
		t.Errorf("/balance answered code %q while starting, want %q", refusal.Code, codeStarting)
	}
	if refusal.Status != 0 {
		t.Errorf("/balance answered status %d while starting, want 0", refusal.Status)
	}
	if resp.Header.Get("Retry-After") == "" {
		t.Error("the starting refusal carries no Retry-After, so a caller cannot know when to come back")
	}

	// /whoami needs no merchant: it must answer for real during startup instead
	// of being refused with everything else.
	if whoami := getJSON(t, client, "http://"+addr+"/whoami", nil); whoami.StatusCode != http.StatusOK {
		t.Errorf("/whoami answered %d during startup, want %d — it does not depend on the merchant", whoami.StatusCode, http.StatusOK)
	}

	// ---- construction finishes ----

	close(releaseConstruction)
	select {
	case err := <-bootDone:
		if err != nil {
			t.Fatalf("boot sequence: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the boot sequence did not finish after the construction was released")
	}

	if !apiStartup.isReady() {
		t.Fatal("the startup gate did not open after the merchant was installed")
	}
	if _, ok := merchantProvider.inner.GetMerchant().(constructedMerchant); !ok {
		t.Fatalf("the constructed merchant was not installed behind the provider, found %T", merchantProvider.inner.GetMerchant())
	}

	// The same endpoint now reaches the merchant. /balance answers 200 either
	// way — with the constructed merchant's usage, or with the handler's own
	// no-session body when the caller resolves to no MAC — but never the gate's
	// 503 anymore.
	var after balanceResponse
	resp = getJSON(t, client, "http://"+addr+"/balance", &after)
	if resp.StatusCode == http.StatusServiceUnavailable {
		t.Fatalf("/balance still refuses with %d after the merchant was installed", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK || after.Status != 1 {
		t.Fatalf("/balance answered %d/status %d after the merchant was installed, want 200/status 1", resp.StatusCode, after.Status)
	}

	// The advertisement is the strongest proof that this is the CONSTRUCTED
	// merchant and not the placeholder: the placeholder advertises nothing.
	advertResp := mustGet(t, client, "http://"+addr+"/")
	defer advertResp.Body.Close()
	advertisement, err := io.ReadAll(advertResp.Body)
	if err != nil {
		t.Fatalf("reading the advertisement: %v", err)
	}
	if !strings.Contains(string(advertisement), "constructed") {
		t.Fatalf("/ advertised %q after construction, want the constructed merchant's advertisement", string(advertisement))
	}
}

// mustGet performs a GET and returns the response for the caller to read; it
// fails the test on a transport error, because a refused connection is exactly
// what this test exists to rule out.
func mustGet(t *testing.T, client *http.Client, url string) *http.Response {
	t.Helper()

	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	return resp
}

// getJSON performs a GET and decodes the body into v (when v is non-nil). The
// body is closed before returning; the response's status and headers stay
// readable.
func getJSON(t *testing.T, client *http.Client, url string, v any) *http.Response {
	t.Helper()

	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()

	if v != nil {
		if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
			t.Fatalf("GET %s: body is not the JSON a portal parses: %v", url, err)
		}
	}
	return resp
}
