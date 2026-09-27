package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A router's TLS identity has to cover the names the router is actually reached
// by, and OpenWrt's image ships a placeholder certificate that covers neither.
// Measured on the bench GL-MT3000 (OpenWrt 25.12.5, 2026-09-26):
//
//	/etc/uhttpd.crt -> subject C=ZZ, ST=Somewhere, O=OpenWrta500f645, CN=OpenWrt
//	                   SAN: DNS:OpenWrt
//	hostname        -> tollgate-OQ3Q
//	network.lan.ipaddr -> 192.168.1.1/24   (note the CIDR suffix)
//
// The tests below pin the two halves of the fix: the placeholder must NOT count
// as an identity, and the generator must build an identity that does.

const (
	testRouterHostname = "tollgate-OQ3Q"
	testRouterLANIP    = "192.168.1.1"
	// The value a real OpenWrt 25.12 install stores: the prefix is part of the
	// option, and net.ParseIP refuses it.
	testRouterLANIPWithPrefix = "192.168.1.1/24"
)

func testHosts() []string { return []string{testRouterHostname, testRouterHostname + ".lan"} }
func testIPs() []string   { return []string{testRouterLANIP} }

// writeTestCertPEM writes a self-signed certificate built from tmpl and returns
// its path.
func writeTestCertPEM(t *testing.T, tmpl *x509.Certificate) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	if tmpl.SerialNumber == nil {
		tmpl.SerialNumber = big.NewInt(1)
	}
	if tmpl.NotBefore.IsZero() {
		tmpl.NotBefore = time.Now().Add(-time.Hour)
	}
	if tmpl.NotAfter.IsZero() {
		tmpl.NotAfter = time.Now().Add(time.Hour)
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	path := filepath.Join(t.TempDir(), "cert.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0644); err != nil {
		t.Fatalf("write certificate: %v", err)
	}
	return path
}

// parseTestCertPEM is the read side: it must round-trip what the generator wrote.
func parseTestCertPEM(t *testing.T, path string) *x509.Certificate {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		t.Fatalf("no PEM block in %s", path)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}
	return cert
}

// ---------------------------------------------------------------- LAN IP parsing

func TestParseLanIP(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want string
	}{
		{"192.168.1.1", "192.168.1.1"},
		{"192.168.1.1/24", "192.168.1.1"},
		{"10.0.0.1/8", "10.0.0.1"},
		{" 192.168.1.1/24 ", "192.168.1.1"},
		{"fd00::1/64", "fd00::1"},
		{"", ""},
		{"   ", ""},
		{"not-an-ip", ""},
		{"192.168.1.1/not-a-prefix", "192.168.1.1"},
		{"/24", ""},
	} {
		if got := parseLanIP(tc.in); got != tc.want {
			t.Errorf("parseLanIP(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// ------------------------------------------------- certificate subject names

// TestRouterSANsCoverHostnameAndLANIP pins the identity the generator has to
// produce: <hostname>, <hostname>.lan and the LAN IP. It is driven with the RAW
// uci value, prefix included, because that is what the option holds on a real
// OpenWrt 25.12 install — passing it straight to net.ParseIP yields nil, and a
// template with a nil address makes x509.CreateCertificate fail outright, so the
// router ended up with no identity at all.
func TestRouterSANsCoverHostnameAndLANIP(t *testing.T) {
	dnsNames, ipAddrs := routerSANs(testRouterHostname, testRouterLANIPWithPrefix)

	wantDNS := []string{testRouterHostname + ".lan", testRouterHostname}
	if len(dnsNames) != len(wantDNS) {
		t.Fatalf("DNSNames = %v, want %v", dnsNames, wantDNS)
	}
	for i, want := range wantDNS {
		if dnsNames[i] != want {
			t.Errorf("DNSNames[%d] = %q, want %q", i, dnsNames[i], want)
		}
	}

	if len(ipAddrs) != 1 {
		t.Fatalf("IPAddresses = %v, want exactly the LAN IP", ipAddrs)
	}
	if ipAddrs[0] == nil {
		t.Fatal("IPAddresses[0] is nil — the CIDR suffix was not stripped")
	}
	if !ipAddrs[0].Equal(net.ParseIP(testRouterLANIP)) {
		t.Errorf("IPAddresses[0] = %v, want %v", ipAddrs[0], testRouterLANIP)
	}

	// An unparseable LAN IP must not smuggle a nil address into the template.
	if _, ips := routerSANs(testRouterHostname, "garbage"); len(ips) != 0 {
		t.Errorf("IPAddresses = %v, want none for an unparseable LAN IP", ips)
	}
}

// TestSelfSignedIdentityCoversTheRouter builds the certificate the way
// sslApplySelfSigned does and asks the question a browser asks: does this
// identity validate the address it was reached on?
func TestSelfSignedIdentityCoversTheRouter(t *testing.T) {
	cert := parseTestCertPEM(t, writeTestCertPEM(t, selfSignedTemplate(testRouterHostname, testRouterLANIPWithPrefix)))

	for _, name := range []string{testRouterHostname, testRouterHostname + ".lan", testRouterLANIP} {
		if err := cert.VerifyHostname(name); err != nil {
			t.Errorf("generated identity does not cover %q: %v", name, err)
		}
	}
	if err := cert.VerifyHostname("OpenWrt"); err == nil {
		t.Error("generated identity must not claim to be the OpenWrt placeholder host")
	}
}

// ------------------------------------------------------------------- coverage

// TestCertCoverageRejectsTheVendorPlaceholder is the regression test for the
// reported defect: the OpenWrt image's own certificate satisfies every
// file-existence guard there is, and yet enables the :8080 -> https:// hop onto
// a certificate that cannot validate the address the browser used.
func TestCertCoverageRejectsTheVendorPlaceholder(t *testing.T) {
	cert := parseTestCertPEM(t, writeTestCertPEM(t, &x509.Certificate{
		Subject:  pkix.Name{CommonName: "OpenWrt", Organization: []string{"OpenWrta500f645"}},
		DNSNames: []string{"OpenWrt"},
	}))

	ok, reason := certCoverage(cert, testHosts(), testIPs())
	if ok {
		t.Fatalf("the OpenWrt placeholder was accepted as this router's identity (%s)", reason)
	}
	if reason == "" {
		t.Error("a refusal must carry a reason")
	}
}

func TestCertCoverageAcceptsIdentityThatCoversTheRouter(t *testing.T) {
	for _, tc := range []struct {
		name string
		tmpl *x509.Certificate
	}{
		{"hostname and lan alias and IP", &x509.Certificate{
			DNSNames:    []string{testRouterHostname + ".lan", testRouterHostname},
			IPAddresses: []net.IP{net.ParseIP(testRouterLANIP)},
		}},
		{"hostname only", &x509.Certificate{DNSNames: []string{testRouterHostname}}},
		{"lan alias only", &x509.Certificate{DNSNames: []string{testRouterHostname + ".lan"}}},
		{"LAN IP only", &x509.Certificate{IPAddresses: []net.IP{net.ParseIP(testRouterLANIP)}}},
		{"wildcard lan zone", &x509.Certificate{DNSNames: []string{"*.lan"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cert := parseTestCertPEM(t, writeTestCertPEM(t, tc.tmpl))
			ok, reason := certCoverage(cert, testHosts(), testIPs())
			if !ok {
				t.Fatalf("identity covering this router was refused: %s", reason)
			}
		})
	}
}

// TestCertCoverageCommonNameAloneIsNotCoverage: modern browsers ignore a CN when
// the certificate carries no SAN extension at all, so it must not enable the
// redirect.
func TestCertCoverageCommonNameAloneIsNotCoverage(t *testing.T) {
	cert := parseTestCertPEM(t, writeTestCertPEM(t, &x509.Certificate{
		Subject: pkix.Name{CommonName: testRouterHostname},
	}))
	if ok, reason := certCoverage(cert, testHosts(), testIPs()); ok {
		t.Fatalf("a CN-only certificate was accepted as coverage (%s)", reason)
	}
}

func TestCertCoverageExpiredIsNotCoverage(t *testing.T) {
	cert := parseTestCertPEM(t, writeTestCertPEM(t, &x509.Certificate{
		DNSNames:    []string{testRouterHostname},
		NotBefore:   time.Now().Add(-48 * time.Hour),
		NotAfter:    time.Now().Add(-24 * time.Hour),
		IPAddresses: []net.IP{net.ParseIP(testRouterLANIP)},
	}))
	ok, reason := certCoverage(cert, testHosts(), testIPs())
	if ok {
		t.Fatalf("an expired certificate was accepted as coverage (%s)", reason)
	}
}

func TestCertCoverageWithoutRouterNames(t *testing.T) {
	cert := parseTestCertPEM(t, writeTestCertPEM(t, &x509.Certificate{DNSNames: []string{testRouterHostname}}))
	if ok, _ := certCoverage(cert, nil, nil); ok {
		t.Error("coverage cannot be claimed when the router's own names are unknown")
	}
}

// --------------------------------------------------------------- file reading

func TestCertCoversRouterReadsTheCertificateFile(t *testing.T) {
	good := writeTestCertPEM(t, selfSignedTemplate(testRouterHostname, testRouterLANIP))

	t.Run("covering certificate", func(t *testing.T) {
		stubUCI(t, map[string]string{
			"system.@system[0].hostname": testRouterHostname,
			"network.lan.ipaddr":         testRouterLANIPWithPrefix,
		})
		ok, reason := certCoversRouter(good)
		if !ok {
			t.Fatalf("covering certificate refused: %s", reason)
		}
	})

	t.Run("placeholder certificate", func(t *testing.T) {
		placeholder := writeTestCertPEM(t, &x509.Certificate{
			Subject:  pkix.Name{CommonName: "OpenWrt"},
			DNSNames: []string{"OpenWrt"},
		})
		stubUCI(t, map[string]string{
			"system.@system[0].hostname": testRouterHostname,
			"network.lan.ipaddr":         testRouterLANIPWithPrefix,
		})
		if ok, _ := certCoversRouter(placeholder); ok {
			t.Error("the vendor placeholder must never count as coverage")
		}
	})

	t.Run("missing, empty and non-PEM files", func(t *testing.T) {
		stubUCI(t, map[string]string{"system.@system[0].hostname": testRouterHostname})
		dir := t.TempDir()
		empty := filepath.Join(dir, "empty.pem")
		if err := os.WriteFile(empty, nil, 0644); err != nil {
			t.Fatal(err)
		}
		garbage := filepath.Join(dir, "garbage.pem")
		if err := os.WriteFile(garbage, []byte("not a certificate\n"), 0644); err != nil {
			t.Fatal(err)
		}
		for _, path := range []string{filepath.Join(dir, "absent.pem"), empty, garbage} {
			if ok, reason := certCoversRouter(path); ok {
				t.Errorf("%s counted as coverage (%s)", path, reason)
			}
		}
	})
}

// ------------------------------------------------------- derived-value guard

// TestApplyRedirectHTTPSTracksCoverage pins the derived value the uci-defaults
// script also computes: redirect_https is '1' only while the certificate
// uhttpd.main presents covers this router.
func TestApplyRedirectHTTPSTracksCoverage(t *testing.T) {
	good := writeTestCertPEM(t, selfSignedTemplate(testRouterHostname, testRouterLANIP))
	placeholder := writeTestCertPEM(t, &x509.Certificate{
		Subject:  pkix.Name{CommonName: "OpenWrt"},
		DNSNames: []string{"OpenWrt"},
	})

	for _, tc := range []struct {
		name     string
		certPath string
		want     string
	}{
		{"covering identity enables the redirect", good, "1"},
		{"vendor placeholder keeps it off", placeholder, "0"},
		{"absent certificate keeps it off", "/nonexistent/uhttpd.crt", "0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stubUCI(t, map[string]string{
				"system.@system[0].hostname": testRouterHostname,
				"network.lan.ipaddr":         testRouterLANIPWithPrefix,
				"uhttpd.main.cert":           tc.certPath,
			})
			if err := applyRedirectHTTPS(); err != nil {
				t.Fatalf("applyRedirectHTTPS: %v", err)
			}
			got, err := stubUCIValue(t, "uhttpd.main.redirect_https")
			if err != nil {
				t.Fatalf("read back redirect_https: %v", err)
			}
			if got != tc.want {
				t.Errorf("uhttpd.main.redirect_https = %q, want %q", got, tc.want)
			}
		})
	}
}

// uhttpdCertPath has to look at the certificate uhttpd.main will actually
// present, because that is the one a browser will be offered.
func TestUhttpdCertPathFollowsTheUCIOption(t *testing.T) {
	stubUCI(t, map[string]string{"uhttpd.main.cert": "/etc/tollgate/ssl/server.crt"})
	if got := uhttpdCertPath(); got != "/etc/tollgate/ssl/server.crt" {
		t.Errorf("uhttpdCertPath() = %q, want the configured option", got)
	}
	stubUCI(t, map[string]string{})
	if got := uhttpdCertPath(); got != "/etc/uhttpd.crt" {
		t.Errorf("uhttpdCertPath() = %q, want the image default when the option is unset", got)
	}
}
