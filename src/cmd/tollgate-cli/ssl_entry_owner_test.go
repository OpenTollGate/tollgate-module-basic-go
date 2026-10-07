package main

import "testing"

// D3 of docs/architecture/default-ui-and-entry-port-decision.md: the TLS
// identity follows the listener that answers :443, not the section name. With
// entry_ui=board the portal's 92 writes the entry pair onto uhttpd.admin, so a
// path that hardcoded uhttpd.main would keep provisioning the SECONDARY listener
// and the entry would answer with the OpenWrt image's placeholder — the defect
// #593 removed, moved one port to the left.

// TestTLSOwnerIsTheSectionThatLists443 pins the one rule every writer in ssl.go
// now shares, including the fallback for a router with no HTTPS listener yet.
func TestTLSOwnerIsTheSectionThatLists443(t *testing.T) {
	for _, tc := range []struct {
		name string
		seed map[string]string
		want string
	}{
		{
			name: "board mapping: admin owns the entry pair",
			seed: map[string]string{
				"uhttpd.admin.listen_https": "0.0.0.0:443 [::]:443",
				"uhttpd.main.listen_https":  "0.0.0.0:8443 [::]:8443",
			},
			want: "uhttpd.admin",
		},
		{
			name: "legacy mapping: main owns the entry pair",
			seed: map[string]string{
				"uhttpd.main.listen_https": "0.0.0.0:443 [::]:443",
			},
			want: "uhttpd.main",
		},
		{
			name: "no HTTPS listener yet: fail closed to main, as today",
			seed: map[string]string{},
			want: "uhttpd.main",
		},
		{
			name: "a section listing only the secondary port does not own :443",
			seed: map[string]string{
				"uhttpd.main.listen_https":  "0.0.0.0:8443",
				"uhttpd.admin.listen_https": "0.0.0.0:8443",
			},
			want: "uhttpd.main",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stubUCI(t, tc.seed)
			if got := tlsListenPortOwner("443"); got != tc.want {
				t.Errorf("tlsListenPortOwner(443) = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestCertPathFollowsTheEntryListener is the read half: the certificate a
// browser is offered on the entry port must be the one the CLI checks for
// coverage, or `ssl covers` gates the redirect on a file nothing serves.
func TestCertPathFollowsTheEntryListener(t *testing.T) {
	stubUCI(t, map[string]string{
		"uhttpd.admin.listen_https": "0.0.0.0:443 [::]:443",
		"uhttpd.main.listen_https":  "0.0.0.0:8443 [::]:8443",
		"uhttpd.admin.cert":         "/etc/tollgate/ssl/server.crt",
		"uhttpd.main.cert":          "/etc/uhttpd.crt",
	})
	if got := uhttpdCertPath(); got != "/etc/tollgate/ssl/server.crt" {
		t.Errorf("uhttpdCertPath() = %q, want the entry listener's certificate", got)
	}
}

// TestRedirectIsWrittenOnTheEntryListener is the write half, and the reason the
// rule cannot be split: the derived redirect and the certificate it is derived
// from have to live on the same section, or the entry serves HTTPS without a
// redirect (or a redirect towards a listener that is not there).
func TestRedirectIsWrittenOnTheEntryListener(t *testing.T) {
	good := writeTestCertPEM(t, selfSignedTemplate(testRouterHostname, testRouterLANIP))
	stubUCI(t, map[string]string{
		"system.@system[0].hostname": testRouterHostname,
		"network.lan.ipaddr":         testRouterLANIPWithPrefix,
		"uhttpd.admin.listen_https":  "0.0.0.0:443 [::]:443",
		"uhttpd.main.listen_https":   "0.0.0.0:8443 [::]:8443",
		"uhttpd.admin.cert":          good,
		"uhttpd.main.cert":           "/etc/uhttpd.crt",
	})
	if err := applyRedirectHTTPS(); err != nil {
		t.Fatalf("applyRedirectHTTPS: %v", err)
	}
	got, err := stubUCIValue(t, "uhttpd.admin.redirect_https")
	if err != nil {
		t.Fatalf("read back uhttpd.admin.redirect_https: %v", err)
	}
	if got != "1" {
		t.Errorf("uhttpd.admin.redirect_https = %q, want 1 (the entry listener holds the covering identity)", got)
	}
	if v, err := stubUCIValue(t, "uhttpd.main.redirect_https"); err == nil {
		t.Errorf("uhttpd.main.redirect_https = %q, want it left untouched: main is not the entry listener", v)
	}
}

// TestProvisioningFollowsTheEntryListener: `ssl apply` under entry_ui=board must
// install onto the section that answers :443. Writing it to uhttpd.main would
// leave the entry on the placeholder and give the secondary listener an identity
// it does not need.
func TestProvisioningFollowsTheEntryListener(t *testing.T) {
	stubUCI(t, map[string]string{
		"uhttpd.admin.listen_https": "0.0.0.0:443 [::]:443",
		"uhttpd.main.listen_https":  "0.0.0.0:8443 [::]:8443",
	})
	if err := configureUhttpd(); err != nil {
		t.Fatalf("configureUhttpd: %v", err)
	}
	for key, want := range map[string]string{
		"uhttpd.admin.cert": certDest,
		"uhttpd.admin.key":  keyDest,
	} {
		got, err := stubUCIValue(t, key)
		if err != nil || got != want {
			t.Errorf("%s = %q (err %v), want %q", key, got, err, want)
		}
	}
	if v, err := stubUCIValue(t, "uhttpd.main.cert"); err == nil {
		t.Errorf("uhttpd.main.cert = %q, want it untouched: main is not the entry listener", v)
	}
}
