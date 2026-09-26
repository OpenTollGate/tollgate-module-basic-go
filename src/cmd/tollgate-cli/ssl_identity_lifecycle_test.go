package main

import (
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The identity lifecycle, end to end through the CLI's own commands, on a
// redirected filesystem:
//
//	install path (uci-defaults 99) --provisions--> `ssl apply` -> covering identity
//	operator reverts                              -> `ssl remove` -> opt-out marker
//	next install                                  -> honours the marker, does not re-key
//
// The uci-defaults half of that contract is pinned by
// tests/uci-defaults-admin-tls-identity_test.sh (which drives the shipped shell
// script against a fixture). These tests pin the CLI half, which the shell
// harness can only stub: that `ssl apply` really installs an identity whose SANs
// cover this router, that `ssl remove` really records the operator's decision,
// and that the marker really ends when the operator asks for an identity again.
// Without the marker, the setup path this change adds would silently re-provision
// a router whose owner ran `ssl remove` — the operator's decision, overwritten.

// redirectSSLPaths points every absolute path the ssl commands touch at a
// throw-away root, and restores them afterwards. Package-level variables, so
// these tests must not run in parallel with each other.
func redirectSSLPaths(t *testing.T) (root string) {
	t.Helper()
	root = t.TempDir()

	oldDir, oldBackup, oldCert, oldKey := sslDir, backupDir, certDest, keyDest
	oldOptOut := sslOptOutFile
	oldUhttpd, oldDnsmasq, oldNds := uhttpdInitPath, dnsmasqInitPath, nodogsplashInitPath
	oldYes, oldNoRestart := sslYesFlag, sslNoRestartFlag

	sslDir = filepath.Join(root, "ssl")
	backupDir = filepath.Join(sslDir, "backup")
	certDest = filepath.Join(sslDir, "server.crt")
	keyDest = filepath.Join(sslDir, "server.key")
	sslOptOutFile = filepath.Join(sslDir, "tls-identity-removed")

	initDir := filepath.Join(root, "init.d")
	if err := os.MkdirAll(initDir, 0755); err != nil {
		t.Fatalf("create init dir: %v", err)
	}
	for name, target := range map[string]*string{
		"uhttpd": &uhttpdInitPath, "dnsmasq": &dnsmasqInitPath, "nodogsplash": &nodogsplashInitPath,
	} {
		path := filepath.Join(initDir, name)
		// A stub that records nothing and succeeds: the reload is not what is
		// under test here, and on a non-router host the real path does not exist.
		if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
			t.Fatalf("write %s stub: %v", name, err)
		}
		*target = path
	}

	// The CLI is driven non-interactively, exactly as the setup path drives it.
	sslYesFlag = true
	sslNoRestartFlag = true

	t.Cleanup(func() {
		sslDir, backupDir, certDest, keyDest = oldDir, oldBackup, oldCert, oldKey
		sslOptOutFile = oldOptOut
		uhttpdInitPath, dnsmasqInitPath, nodogsplashInitPath = oldUhttpd, oldDnsmasq, oldNds
		sslYesFlag, sslNoRestartFlag = oldYes, oldNoRestart
	})
	return root
}

// routerUCI is the config the ssl commands read: the router's own identity,
// including the CIDR spelling OpenWrt 25.12 actually stores.
func routerUCI() map[string]string {
	return map[string]string{
		"system.@system[0].hostname": testRouterHostname,
		"network.lan.ipaddr":         testRouterLANIPWithPrefix,
	}
}

// TestSSLApplyInstallsACoveringIdentityAndEndsTheOptOut drives `ssl apply` the
// way the install path does and reads the result back the way a browser would:
// the installed certificate must validate the hostname, its .lan alias and the
// LAN IP, and the derived redirect must follow it.
func TestSSLApplyInstallsACoveringIdentityAndEndsTheOptOut(t *testing.T) {
	redirectSSLPaths(t)
	state := stubUCI(t, routerUCI())
	t.Setenv("TMPDIR", t.TempDir())

	// The operator had removed the identity, so the marker is present and the
	// setup path is currently holding that decision.
	if err := os.MkdirAll(sslDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sslOptOutFile, []byte("removed by the operator\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if !sslOptedOut() {
		t.Fatal("precondition: the opt-out marker was not in place")
	}

	if err := sslApply(nil); err != nil {
		t.Fatalf("ssl apply: %v", err)
	}

	if sslOptedOut() {
		t.Error("ssl apply left the opt-out marker behind: the next install would remove the identity it just installed")
	}
	cert := parseTestCertPEM(t, certDest)
	for _, name := range []string{testRouterHostname, testRouterHostname + ".lan"} {
		if err := cert.VerifyHostname(name); err != nil {
			t.Errorf("the installed certificate does not cover %s: %v (SANs: DNS=%v IP=%v)",
				name, err, cert.DNSNames, cert.IPAddresses)
		}
	}
	if err := cert.VerifyHostname(testRouterLANIP); err != nil {
		t.Errorf("the installed certificate does not cover the LAN IP %s: %v (SANs: DNS=%v IP=%v)",
			testRouterLANIP, err, cert.DNSNames, cert.IPAddresses)
	}

	gotCert, _ := stubUCIValue(t, "uhttpd.main.cert")
	if gotCert != certDest {
		t.Errorf("uhttpd.main.cert = %q, want the installed identity %q", gotCert, certDest)
	}
	gotRedirect, _ := stubUCIValue(t, "uhttpd.main.redirect_https")
	if gotRedirect != "1" {
		t.Errorf("uhttpd.main.redirect_https = %q, want 1 — a covering identity may redirect :8080", gotRedirect)
	}
	if _, err := os.Stat(state); err != nil {
		t.Fatalf("the uci stub state disappeared: %v", err)
	}
}

// TestSSLRemoveRecordsTheOptOut is the other half: a removal that left no trace
// would be undone by the next install, so the CLI has to write the marker — and
// only after the removal actually completed.
func TestSSLRemoveRecordsTheOptOut(t *testing.T) {
	redirectSSLPaths(t)
	stubUCI(t, routerUCI())
	t.Setenv("TMPDIR", t.TempDir())

	// A router with an applied self-signed identity: cert+key installed and a
	// backup directory recording what uhttpd held before (the image's own
	// placeholder, which the removal restores).
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sslDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(certDest, []byte("cert\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyDest, []byte("key\n"), 0600); err != nil {
		t.Fatal(err)
	}
	prevCert := filepath.Join(t.TempDir(), "uhttpd.crt")
	prevKey := filepath.Join(t.TempDir(), "uhttpd.key")
	for _, f := range []string{prevCert, prevKey} {
		if err := os.WriteFile(f, []byte("placeholder\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	for name, body := range map[string]string{
		"ssl.mode":        "self-signed",
		"ssl.domain":      testRouterHostname + ".lan",
		"uhttpd.cert":     prevCert,
		"uhttpd.key":      prevKey,
		"ssl.lan_ip":      testRouterLANIP,
		"dnsmasq.domains": "",
	} {
		if err := os.WriteFile(filepath.Join(backupDir, name), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}

	if err := sslRemove(); err != nil {
		t.Fatalf("ssl remove: %v", err)
	}

	if !sslOptedOut() {
		t.Fatal("ssl remove did not record the opt-out: the next install would provision the identity the operator just removed")
	}
	if _, err := os.Stat(certDest); !os.IsNotExist(err) {
		t.Errorf("the identity was not removed (stat %s: %v)", certDest, err)
	}
	body, err := os.ReadFile(sslOptOutFile)
	if err != nil {
		t.Fatalf("read the marker: %v", err)
	}
	if !strings.Contains(string(body), "tollgate ssl apply") {
		t.Errorf("the marker does not tell an operator how to end the opt-out:\n%s", body)
	}
	// The derived value follows the restored certificate: the image's
	// placeholder does not cover this router, so :8080 must stay on HTTP.
	gotRedirect, _ := stubUCIValue(t, "uhttpd.main.redirect_https")
	if gotRedirect != "0" {
		t.Errorf("uhttpd.main.redirect_https = %q after a removal, want 0 — a restored placeholder must not redirect", gotRedirect)
	}
}

// TestSSLCoversJSONContract pins the machine-readable contract of `ssl covers`:
// one object on stdout, and `success` mirrors the exit status so a caller that
// parses stdout cannot read a "no" as a green result (#375).
func TestSSLCoversJSONContract(t *testing.T) {
	redirectSSLPaths(t)
	stubUCI(t, routerUCI())

	cases := []struct {
		name       string
		certPath   string
		wantCovers bool
		wantErr    bool
	}{
		{
			name:       "covering identity",
			certPath:   writeTestCertPEM(t, selfSignedTemplate(testRouterHostname, testRouterLANIP)),
			wantCovers: true,
		},
		{
			name: "vendor placeholder",
			certPath: writeTestCertPEM(t, &x509.Certificate{
				Subject:     pkix.Name{CommonName: "OpenWrt"},
				DNSNames:    []string{"OpenWrt"},
				IPAddresses: []net.IP{},
			}),
			wantCovers: false,
			wantErr:    true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := captureStdout(t, func() error {
				old := jsonOutput
				jsonOutput = true
				defer func() { jsonOutput = old }()
				return sslCoversCmd.RunE(sslCoversCmd, []string{tc.certPath})
			})
			if tc.wantErr && err == nil {
				t.Error("a non-covering certificate must fail the command, not just print 'no'")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("covers on a covering certificate: %v", err)
			}

			var payload struct {
				Success bool   `json:"success"`
				Command string `json:"command"`
				Cert    string `json:"cert"`
				Covers  bool   `json:"covers"`
				Reason  string `json:"reason"`
				Error   string `json:"error"`
			}
			if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &payload); err != nil {
				t.Fatalf("stdout is not one JSON object (%v):\n%s", err, out)
			}
			if payload.Success != tc.wantCovers || payload.Covers != tc.wantCovers {
				t.Errorf("success=%v covers=%v, want both %v", payload.Success, payload.Covers, tc.wantCovers)
			}
			if payload.Cert != tc.certPath {
				t.Errorf("cert = %q, want %q", payload.Cert, tc.certPath)
			}
			if strings.TrimSpace(payload.Reason) == "" {
				t.Error("the reason is empty: a caller cannot tell WHY it does not cover")
			}
			if tc.wantCovers && payload.Error != "" {
				t.Errorf("a covering certificate reported an error: %q", payload.Error)
			}
			if !tc.wantCovers && payload.Error == "" {
				t.Error("a non-covering certificate reported success:true-style output with no error field")
			}
		})
	}
}

// TestSSLCoversPlainOutputStaysHumanReadable: the setup script runs `covers`
// without --json and branches on the exit status, so the plain form must keep
// printing a one-line verdict and must not print JSON.
func TestSSLCoversPlainOutputStaysHumanReadable(t *testing.T) {
	redirectSSLPaths(t)
	stubUCI(t, routerUCI())
	placeholder := writeTestCertPEM(t, &x509.Certificate{
		Subject:  pkix.Name{CommonName: "OpenWrt"},
		DNSNames: []string{"OpenWrt"},
	})

	out, err := captureStdout(t, func() error {
		old := jsonOutput
		jsonOutput = false
		defer func() { jsonOutput = old }()
		return sslCoversCmd.RunE(sslCoversCmd, []string{placeholder})
	})
	if err == nil {
		t.Error("expected a non-zero exit for the placeholder")
	}
	if !strings.HasPrefix(strings.TrimSpace(out), "covers: no") {
		t.Errorf("plain output = %q, want a 'covers: no' verdict", out)
	}
	if strings.Contains(out, "{") {
		t.Errorf("plain output looks like JSON: %q", out)
	}
}

// captureStdout runs fn with os.Stdout redirected to a file and returns what it
// printed. A file, not a pipe: the writers here are synchronous and a pipe
// buffer could deadlock a longer payload.
func captureStdout(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "stdout-")
	if err != nil {
		t.Fatalf("create stdout capture: %v", err)
	}
	old := os.Stdout
	os.Stdout = f
	runErr := fn()
	os.Stdout = old
	if err := f.Close(); err != nil {
		t.Fatalf("close stdout capture: %v", err)
	}
	data, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatalf("read stdout capture: %v", err)
	}
	return string(data), runErr
}
