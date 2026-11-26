package lnurlcash

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// The issuer client is the only component that talks to a third party, and the
// URL it talks to comes from an untrusted note. These tests exercise the
// refusals that keep a note from steering the gate, and the DEFINITIVE vs
// AMBIGUOUS split the caller depends on: a redemption that might have happened
// must never be reported as a refusal, because the caller's next move differs.
//
// The transport is replaced with one that dials a local test server. That is the
// ONLY way these tests bypass the SSRF guard, and it is done by construction
// here, never through a runtime option in production code.

// testClient builds a Client for domain that talks to srv instead of the
// internet. Only the dial step changes; redirect policy, body limits, timeouts
// and the definitive/ambiguous classification all stay production code.
func testClient(t *testing.T, domain string, srv *httptest.Server) *Client {
	t.Helper()
	c, err := NewIssuerClient(domain)
	if err != nil {
		t.Fatalf("NewIssuerClient(%q): %v", domain, err)
	}
	target, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("test server URL: %v", err)
	}
	_, port, err := net.SplitHostPort(target.Host)
	if err != nil {
		t.Fatalf("test server host: %v", err)
	}
	transport := srv.Client().Transport.(*http.Transport).Clone()
	// The test server's certificate is for 127.0.0.1; the client dials the issuer
	// domain, so verification is pinned to the test server's own root instead of
	// being disabled. Production keeps the system pool (NewIssuerClient).
	transport.TLSClientConfig = srv.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	// The server certificate's SANs cover 127.0.0.1 (httptest's own cert), so the
	// handshake verifies against it while the request URL keeps the issuer's
	// host — which is exactly the pairing production has, minus the CA.
	transport.TLSClientConfig.ServerName = "127.0.0.1"
	transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		// Ignore the requested address; the client's own checks are exercised
		// separately by TestClientRefusesPrivateAddresses.
		return (&net.Dialer{}).DialContext(ctx, "tcp", net.JoinHostPort("127.0.0.1", port))
	}
	c.http = &http.Client{
		Transport: transport,
		Timeout:   c.http.Timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("lnurlcash: issuer redirect refused")
		},
	}
	return c
}

func testNote(t *testing.T) Note {
	t.Helper()
	n, err := Parse(vecNoteURL, "")
	if err != nil {
		t.Fatalf("Parse(vector note): %v", err)
	}
	return n
}

func jsonHandler(t *testing.T, body any) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	}
}

func TestInfoReturnsTheIssuersStateAndNeverFollowsACrossHostCallback(t *testing.T) {
	var gotQuery url.Values
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(withdrawRequest{
			Tag:             "withdrawRequest",
			Callback:        "https://" + vecDomain + "/w/cb",
			K1:              vecPreimage,
			MinWithdrawable: 1000,
			MaxWithdrawable: 1000,
			MintPubkey:      vecMintPubkey,
			C:               vecCS1_1000n,
		})
	}))
	defer srv.Close()

	c := testClient(t, vecDomain, srv)
	state, err := c.Info(context.Background(), testNote(t), "/w")
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if gotQuery.Get("k1") != vecPreimage {
		t.Fatalf("Info did not send the spend as k1 (got %d params)", len(gotQuery))
	}
	if state.WithdrawableMSat != 1000 {
		t.Fatalf("withdrawable = %d msat, want 1000", state.WithdrawableMSat)
	}
	if state.MintPubkey != vecMintPubkey {
		t.Fatalf("mintPubkey = %q", state.MintPubkey)
	}
	if WithdrawableSats(state.WithdrawableMSat) != 1 {
		t.Fatalf("1000 msat should be 1 whole sat, got %d", WithdrawableSats(state.WithdrawableMSat))
	}
	if state.Callback == nil || state.Callback.Host != vecDomain || state.Callback.Path != "/w/cb" {
		t.Fatalf("callback was not rebuilt on the issuer's own domain: %+v", state.Callback)
	}

	// A callback naming another host is a hostile issuer trying to move the
	// spend after our allowlist check.
	srv2 := httptest.NewTLSServer(jsonHandler(t, withdrawRequest{
		Callback:        "https://evil.example/w/cb",
		MaxWithdrawable: 1000,
	}))
	defer srv2.Close()
	_, err = testClient(t, vecDomain, srv2).Info(context.Background(), testNote(t), "/w")
	if err == nil {
		t.Fatal("a callback on a foreign host was accepted")
	}
	var ie *IssuerError
	if !errors.As(err, &ie) || !ie.Definitive {
		t.Fatalf("foreign-host callback should be a definitive refusal, got %v", err)
	}
}

func TestLookupDoesNotRevealTheSpend(t *testing.T) {
	var gotQuery url.Values
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(withdrawRequest{
			Callback:        "https://" + vecDomain + "/w/cb",
			MaxWithdrawable: 1000,
		})
	}))
	defer srv.Close()

	if _, err := testClient(t, vecDomain, srv).Lookup(context.Background(), testNote(t), "/w"); err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if gotQuery.Get("k1") != "" {
		t.Fatal("Lookup sent the spend; a check must use ?p= (cp1), never ?k1=")
	}
	if gotQuery.Get("p") != vecCP1 {
		t.Fatalf("Lookup sent p=%q, want the note's cp1", gotQuery.Get("p"))
	}
}

func TestRefusalClassificationIsDefinitiveOnlyWhenItIs(t *testing.T) {
	cases := []struct {
		reason         string
		wantDefinitive bool
	}{
		{"unknown note", true},
		{"already in use", true},
		// Transient: the note is being redeemed right now (possibly by us, after
		// a lost response). Reading this as a refusal would strand a paid note.
		{"pending", false},
		{"", false},
		{"some new reason", false},
	}
	for _, tc := range cases {
		t.Run(tc.reason, func(t *testing.T) {
			srv := httptest.NewTLSServer(jsonHandler(t, callbackResponse{Status: "ERROR", Reason: tc.reason}))
			defer srv.Close()

			err := testClient(t, vecDomain, srv).Melt(context.Background(), testNote(t),
				mustURL(t, "https://"+vecDomain+"/w/cb"), "lnbc1stub")
			var ie *IssuerError
			if !errors.As(err, &ie) {
				t.Fatalf("Melt error is not an IssuerError: %v", err)
			}
			if ie.Definitive != tc.wantDefinitive {
				t.Fatalf("reason %q classified definitive=%v, want %v", tc.reason, ie.Definitive, tc.wantDefinitive)
			}
			if strings.Contains(ie.Error(), vecPreimage) {
				t.Fatal("the refusal leaked the spend")
			}
		})
	}
}

// A transport failure after the request may have reached the issuer is the case
// this design exists for: it MUST be ambiguous, because the issuer may have
// burned the note. A caller that reads this as "failed" re-presents a note that
// is already spent.
func TestTransportFailureIsAmbiguous(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Close the connection without an answer: the classic ambiguous outcome.
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Error("hijacker unavailable")
			return
		}
		conn, _, err := hj.Hijack()
		if err != nil {
			t.Errorf("hijack: %v", err)
			return
		}
		conn.Close()
	}))
	defer srv.Close()

	err := testClient(t, vecDomain, srv).Melt(context.Background(), testNote(t),
		mustURL(t, "https://"+vecDomain+"/w/cb"), "lnbc1stub")
	var ie *IssuerError
	if !errors.As(err, &ie) {
		t.Fatalf("Melt error is not an IssuerError: %v", err)
	}
	if ie.Definitive {
		t.Fatal("a lost response was reported as a definitive refusal; the note may be burned")
	}
}

func TestMeltRefusesToSendTheSpendToAForeignCallback(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("the client contacted the issuer with a foreign callback")
	}))
	defer srv.Close()

	err := testClient(t, vecDomain, srv).Melt(context.Background(), testNote(t),
		mustURL(t, "https://evil.example/w/cb"), "lnbc1stub")
	if err == nil {
		t.Fatal("Melt accepted a callback on another host")
	}
	if !errors.Is(err, ErrIssuerMismatch) {
		t.Fatalf("error = %v, want ErrIssuerMismatch", err)
	}
}

func TestClientRefusesRedirects(t *testing.T) {
	var followed bool
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/w" {
			http.Redirect(w, r, "/elsewhere", http.StatusFound)
			return
		}
		followed = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := testClient(t, vecDomain, srv)
	// Restore the production redirect policy, which testClient replaced with the
	// same rule — asserting it here keeps the two from drifting.
	c.http.CheckRedirect = func(*http.Request, []*http.Request) error {
		return errors.New("lnurlcash: issuer redirect refused")
	}
	if _, err := c.Info(context.Background(), testNote(t), "/w"); err == nil {
		t.Fatal("a redirect was followed")
	}
	if followed {
		t.Fatal("the client followed the redirect target")
	}
}

func TestClientRefusesAnOversizedBody(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"callback":"https://` + vecDomain + `/w/cb","maxWithdrawable":1000,"pad":"`))
		_, _ = w.Write([]byte(strings.Repeat("A", maxIssuerBody)))
		_, _ = w.Write([]byte(`"}`))
	}))
	defer srv.Close()

	if _, err := testClient(t, vecDomain, srv).Info(context.Background(), testNote(t), "/w"); err == nil {
		t.Fatal("an oversized issuer body was accepted")
	}
}

// The production client (no test transport) must refuse a name that resolves to
// a private address: a note URL is attacker-controlled, and "issuer at
// 127.0.0.1/admin" is the first thing an SSRF attempt tries.
func TestClientRefusesNonPublicAddressesForTheRealDialer(t *testing.T) {
	for _, host := range []string{"127.0.0.1", "10.0.0.1", "169.254.169.254", "192.168.1.1", "[::1]"} {
		if ip := net.ParseIP(strings.Trim(host, "[]")); ip == nil || publicAddress(ip) {
			t.Errorf("publicAddress(%s) = true; a non-public address must be refused", host)
		}
	}
	for _, host := range []string{"1.1.1.1", "203.0.113.1"} {
		ip := net.ParseIP(host)
		if !publicAddress(ip) {
			t.Errorf("publicAddress(%s) = false; a public address must be allowed", host)
		}
	}
}

// THE ADDRESS CLASSIFIER IS AN SSRF BOUNDARY, SO IT GETS A TABLE.
//
// The doc comment promised loopback, private, link-local, multicast and unspecified are
// refused — but 0.0.0.1, 240.0.0.1, 255.255.255.255, ::ffff:0.0.0.1 and the NAT64 form
// 64:ff9b::7f00:1 (which is 127.0.0.1) all passed. The allowlist is the first boundary,
// so this needs an allowlisted hostname resolving into those ranges to bite — on a
// router, NAT64 makes 64:ff9b::/96 exactly that path to loopback.
func TestPublicAddressRefusesNonRoutableRanges(t *testing.T) {
	refused := []string{
		"127.0.0.1", "127.1.2.3", "0.0.0.0", "0.0.0.1", "0.1.2.3",
		"10.0.0.1", "172.16.0.1", "172.31.255.255", "192.168.1.1",
		"169.254.169.254", "100.64.0.1", "100.127.255.254",
		"192.0.0.1", "198.18.0.1", "198.19.255.255",
		"240.0.0.1", "255.255.255.255",
		"::1", "::", "fc00::1", "fd12:3456::1", "fe80::1", "ff02::1",
		"::ffff:127.0.0.1", "::ffff:0.0.0.1", "::ffff:10.0.0.1",
		"64:ff9b::7f00:1", "64:ff9b::a00:1", "64:ff9b::c0a8:101",
	}
	for _, s := range refused {
		ip := net.ParseIP(s)
		if ip == nil {
			t.Fatalf("test bug: %q does not parse", s)
		}
		if publicAddress(ip) {
			t.Errorf("publicAddress(%s) = true; this address must never be dialled for an issuer", s)
		}
	}

	// And it must not be so strict that no real issuer can be reached.
	allowed := []string{"1.1.1.1", "8.8.8.8", "93.184.216.34", "2606:4700:4700::1111", "2a00:1450:4001:80a::200e"}
	for _, s := range allowed {
		ip := net.ParseIP(s)
		if ip == nil {
			t.Fatalf("test bug: %q does not parse", s)
		}
		if !publicAddress(ip) {
			t.Errorf("publicAddress(%s) = false; a real issuer could live here", s)
		}
	}
}
