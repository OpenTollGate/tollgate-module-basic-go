package lightning

import (
	"net/url"
	"testing"
)

func TestValidateCallbackURL_PublicDomain(t *testing.T) {
	u, _ := url.Parse("https://ln.example.com/callback")
	if err := validateCallbackURL(u); err != nil {
		t.Errorf("public domain should pass: %v", err)
	}
}

func TestValidateCallbackURL_PublicIP(t *testing.T) {
	u, _ := url.Parse("https://203.0.113.1/callback")
	if err := validateCallbackURL(u); err != nil {
		t.Errorf("public IP should pass: %v", err)
	}
}

func TestValidateCallbackURL_Loopback(t *testing.T) {
	u, _ := url.Parse("http://127.0.0.1:8080/steal")
	if err := validateCallbackURL(u); err == nil {
		t.Error("loopback should be rejected")
	}
}

func TestValidateCallbackURL_LoopbackIPv6(t *testing.T) {
	u, _ := url.Parse("http://[::1]:8080/steal")
	if err := validateCallbackURL(u); err == nil {
		t.Error("IPv6 loopback should be rejected")
	}
}

func TestValidateCallbackURL_PrivateRFC1918(t *testing.T) {
	ips := []string{"10.0.0.1", "172.16.0.1", "192.168.1.1"}
	for _, ip := range ips {
		u, _ := url.Parse("http://" + ip + "/steal")
		if err := validateCallbackURL(u); err == nil {
			t.Errorf("RFC1918 %s should be rejected", ip)
		}
	}
}

func TestValidateCallbackURL_LinkLocal(t *testing.T) {
	u, _ := url.Parse("http://169.254.169.254/latest/meta-data/")
	if err := validateCallbackURL(u); err == nil {
		t.Error("link-local (cloud metadata) should be rejected")
	}
}

func TestValidateCallbackURL_Unspecified(t *testing.T) {
	u, _ := url.Parse("http://0.0.0.0/callback")
	if err := validateCallbackURL(u); err == nil {
		t.Error("unspecified 0.0.0.0 should be rejected")
	}
}

func TestValidateCallbackURL_EmptyHost(t *testing.T) {
	u, _ := url.Parse("/relative-path")
	if err := validateCallbackURL(u); err != nil {
		t.Errorf("empty host should pass: %v", err)
	}
}

func TestValidateCallbackURL_CGNAT_PassesThrough(t *testing.T) {
	u, _ := url.Parse("http://100.64.0.1/callback")
	if err := validateCallbackURL(u); err != nil {
		t.Errorf("CGNAT 100.64.x passes through Go IsPrivate() — acceptable for SSRF prevention since CGNAT is ISP-level: %v", err)
	}
}
