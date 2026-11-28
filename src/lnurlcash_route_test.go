package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/merchant/lnurlcash"
	log "github.com/sirupsen/logrus"
)

// The whole safety claim of the opt-in starts here: with the flag off, the
// payment method does not exist. A registered-but-refusing route (403) would
// advertise a way to pay that the gate will not honour, and a portal that probes
// for capability cannot tell "off" from "misconfigured".
func TestLNURLCashRouteIsAbsentWhenDisabled(t *testing.T) {
	setLNURLCashRedeemer(nil)
	mux := http.NewServeMux()
	registerLNURLCashRoute(mux)

	req := httptest.NewRequest(http.MethodPost, "/lnurlcash/pay", strings.NewReader("lnurl1whatever"))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("disabled route answered %d, want 404 (absent, not 403)", rec.Code)
	}
}

// A GET must not be treated as a payment attempt: the note arrives in a body and
// a GET has none, so answering 405 keeps a link prefetcher from "paying".
func TestLNURLCashRouteRefusesGet(t *testing.T) {
	redeemer := newTestRedeemer(t, lnurlcash.DefaultConfig())
	setLNURLCashRedeemer(redeemer)
	defer setLNURLCashRedeemer(nil)

	req := httptest.NewRequest(http.MethodGet, "/lnurlcash/pay", nil)
	rec := httptest.NewRecorder()
	HandleLNURLCashPay(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET answered %d, want 405", rec.Code)
	}
}

// The handler must never put the note itself in a log line. It is a bearer
// secret; a fingerprint correlates a payment without being one.
func TestLNURLCashHandlerLogsOnlyAFingerprint(t *testing.T) {
	const secretNote = "lnurl1dp68gurn8ghj7um9wfmxjcm99e3k7mf0v9cxj0m385ekvcenxc6r2c35xvukxefcv5mkvv34x5ekzd3ev56nyd3hxqurzepexejxxepnxscrvwfnv9nxzcn9xq6xyefhvgcxxcmyxymnserxfq5fns"

	var buf strings.Builder
	prevOut := log.StandardLogger().Out
	prevLevel := log.GetLevel()
	log.SetOutput(&buf)
	log.SetLevel(log.DebugLevel)
	defer func() {
		// RESTORE, never SetOutput(nil): nil makes logrus dereference a nil
		// writer on the next log call, which panics in whatever test runs after
		// this one — a failure that looks like someone else's bug.
		log.SetOutput(prevOut)
		log.SetLevel(prevLevel)
	}()

	redeemer := newTestRedeemer(t, lnurlcash.DefaultConfig())
	setLNURLCashRedeemer(redeemer)
	defer setLNURLCashRedeemer(nil)

	req := httptest.NewRequest(http.MethodPost, "/lnurlcash/pay", strings.NewReader(secretNote))
	rec := httptest.NewRecorder()
	HandleLNURLCashPay(rec, req)

	if strings.Contains(buf.String(), secretNote) {
		t.Fatal("the note itself appears in the log: a bearer secret was written where a bug report would copy it")
	}
	if strings.Contains(buf.String(), "dp68gurn8ghj7") {
		t.Fatal("a fragment of the note appears in the log")
	}
}

// The response states must map to codes a portal can act on, and "do not present
// this again" must never look like "you were not charged".
func TestLNURLCashResponseStatusCodes(t *testing.T) {
	cases := []struct {
		state string
		want  int
	}{
		{lnurlcash.StateAbandoned, http.StatusPaymentRequired},
		{lnurlcash.StateUnavailable, http.StatusPaymentRequired},
		{lnurlcash.StateAmbiguous, http.StatusConflict},
		{lnurlcash.StatePending, http.StatusAccepted},
		{lnurlcash.StateRedeemed, http.StatusOK},
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		writeLNURLCashResponse(rec, lnurlCashResponse{State: tc.state, Message: "m"})
		if rec.Code != tc.want {
			t.Errorf("state %q answered %d, want %d", tc.state, rec.Code, tc.want)
		}
		var got lnurlCashResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Errorf("state %q body is not JSON: %v", tc.state, err)
		}
	}
}

// newTestRedeemer builds a redeemer with no network and no mint: enough for the
// handler's own contract (identity, size cap, logging, status mapping).
func newTestRedeemer(t *testing.T, cfg lnurlcash.Config) *lnurlcash.Redeemer {
	t.Helper()
	store, err := lnurlcash.OpenIntentStore(t.TempDir() + "/intents.json")
	if err != nil {
		t.Fatalf("open intent store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return lnurlcash.NewRedeemer(cfg, nil, store, lnurlcash.NewClient(lnurlcash.ClientOptions{}))
}
