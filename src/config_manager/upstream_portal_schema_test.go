package config_manager

import "testing"

func TestUpstreamPortalLoginSchema(t *testing.T) {
	upstream := findField(GetConfigSchema(), "upstream_wifi")
	if upstream == nil {
		t.Fatal("upstream_wifi section missing")
	}
	for _, key := range []string{
		"portal_login_enabled",
		"portal_login_mode",
		"portal_login_url",
		"portal_login_user_field",
		"portal_login_pass_field",
		"portal_login_username",
		"portal_login_password",
		"portal_login_extra",
		"portal_login_timeout_s",
		"portal_login_mark",
		"portal_login_dns",
	} {
		if findField(upstream.Children, key) == nil {
			t.Errorf("upstream_wifi must expose %q", key)
		}
	}
	password := findField(upstream.Children, "portal_login_password")
	if password == nil {
		t.Fatal("portal_login_password missing")
	}
	if !password.Secret {
		t.Error("portal_login_password must be marked Secret so it is never rendered back")
	}
	mode := findField(upstream.Children, "portal_login_mode")
	if mode == nil {
		t.Fatal("portal_login_mode missing")
	}
	got := map[string]bool{}
	for _, v := range mode.Enum {
		got[v] = true
	}
	if !got["click_through"] || !got["form"] {
		t.Errorf("portal_login_mode must allow click_through and form, got %v", mode.Enum)
	}
}
