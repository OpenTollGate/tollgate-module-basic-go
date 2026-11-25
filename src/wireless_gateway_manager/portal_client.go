// Uplink-scoped captive-portal login.
//
// The engine in portal_auto.go predates multi-uplink bonding: it inherited the
// default route, the system resolver and the global connectivity probe. With
// two STAs that sends the venue's portal request out of the wrong interface.
// This file exposes the same parsing/submission logic with an explicit binding
// so a caller can say "log in THIS uplink" and mean it.
package wireless_gateway_manager

import (
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"time"
)

// PortalRecipe is the operator-configured login recipe. It is deliberately
// transport-free: the binding says where to send it.
type PortalRecipe struct {
	// Mode is "click_through" (submit the page's own fields and accept button)
	// or "form" (also fill Username/Password).
	Mode string
	// PageURL is the portal page. Empty means discover it from the captive
	// probe's redirect.
	PageURL string
	// UserField/PassField are the form field names for form mode.
	UserField string
	PassField string
	Username  string
	Password  string
	// Extra is urlencoded form data merged into the submission, for example
	// room=101&lastname=Smith. It is how a guest form that wants a name but no
	// password is driven without a special mode.
	Extra string
	// Timeout bounds one attempt.
	Timeout time.Duration
	// AllowHosts lists external hosts a redirect or form action may use. The
	// page host is always allowed. Credentials are never sent to any other host.
	AllowHosts []string
}

// PortalBinding pins one attempt to an uplink. Interface is a netdev name
// (for example phy1-sta0). SourceIP is optional and, when set, also becomes
// the socket's local address.
type PortalBinding struct {
	Interface string
	SourceIP  string
	Mark      int
	DNS       string
}

// PortalOutcome is the machine-friendly result of one attempt.
type PortalOutcome string

const (
	// PortalOK means the portal accepted the submission and the scoped probe
	// reached the internet.
	PortalOK PortalOutcome = "ok"
	// PortalNoPortal means there was no portal URL to act on.
	PortalNoPortal PortalOutcome = "no-portal"
	// PortalNoForm means a page was fetched but no usable login form was found.
	PortalNoForm PortalOutcome = "no-form"
	// PortalSubmitFailed means the request could not be built or delivered, or
	// the portal rejected it.
	PortalSubmitFailed PortalOutcome = "submit-failed"
	// PortalStillCaptive means the submission returned but the probe still saw
	// a captive state.
	PortalStillCaptive PortalOutcome = "still-captive"
)

// PortalResult carries the outcome without credentials or response bodies.
type PortalResult struct {
	Outcome    PortalOutcome
	Reason     string
	PortalHost string
}

// AttemptPortalLogin performs exactly one portal login through binding.
//
// probe, when non-nil, must check connectivity through the same binding. A
// nil probe is for callers that only want the submission (tests); production
// callers should always pass one, because a portal can answer 200 and still
// leave the uplink captive.
func AttemptPortalLogin(ctx context.Context, binding PortalBinding, recipe PortalRecipe, probe func(context.Context) bool) PortalResult {
	if recipe.Timeout <= 0 {
		recipe.Timeout = 10 * time.Second
	}
	mode := strings.ToLower(strings.TrimSpace(recipe.Mode))
	if mode != portalLoginModeForm {
		mode = portalLoginModeClickThrough
	}
	userField := strings.TrimSpace(recipe.UserField)
	if userField == "" {
		userField = "username"
	}
	passField := strings.TrimSpace(recipe.PassField)
	if passField == "" {
		passField = "password"
	}

	internal := portalRecipe{
		mode:       mode,
		pageURL:    strings.TrimSpace(recipe.PageURL),
		userField:  userField,
		passField:  passField,
		username:   recipe.Username,
		password:   recipe.Password,
		extra:      recipe.Extra,
		timeout:    recipe.Timeout,
		mark:       binding.Mark,
		dns:        strings.TrimSpace(binding.DNS),
		allowHosts: append([]string(nil), recipe.AllowHosts...),
	}
	bound := portalBinding{
		iface:    strings.TrimSpace(binding.Interface),
		sourceIP: strings.TrimSpace(binding.SourceIP),
	}
	jar, _ := cookiejar.New(nil)

	pageURL := internal.pageURL
	if pageURL == "" {
		pageURL = discoverPortalURL(ctx, internal, bound, jar)
	}
	if pageURL == "" {
		return PortalResult{Outcome: PortalNoPortal, Reason: "no portal URL discovered"}
	}

	page, base, err := fetchPortalPage(ctx, pageURL, internal, bound, jar)
	if err != nil {
		return PortalResult{Outcome: PortalNoPortal, Reason: err.Error(), PortalHost: portalHost(pageURL)}
	}
	form, err := parsePortalForm(page, base, internal.allowHosts)
	if err != nil {
		return PortalResult{Outcome: PortalNoForm, Reason: err.Error(), PortalHost: base.Host}
	}
	if internal.mode == portalLoginModeForm {
		applyPortalCredentials(form, internal)
	}
	if err := mergePortalExtra(form, internal.extra); err != nil {
		return PortalResult{Outcome: PortalSubmitFailed, Reason: "malformed extra fields", PortalHost: form.action.Host}
	}
	if err := submitPortalForm(ctx, form, internal, bound, jar); err != nil {
		return PortalResult{Outcome: PortalSubmitFailed, Reason: err.Error(), PortalHost: form.action.Host}
	}
	if probe != nil && !probe(ctx) {
		return PortalResult{Outcome: PortalStillCaptive, Reason: "internet still unavailable", PortalHost: form.action.Host}
	}
	return PortalResult{Outcome: PortalOK, PortalHost: form.action.Host}
}

// ProbeUplink checks for real internet through one binding. It deliberately
// does not follow redirects: a captive portal answers with a 3xx or a 200
// page, and only the 204 sentinel means the uplink is usable.
func ProbeUplink(ctx context.Context, binding PortalBinding) bool {
	recipe := portalRecipe{timeout: 6 * time.Second, mark: binding.Mark, dns: strings.TrimSpace(binding.DNS)}
	bound := portalBinding{
		iface:    strings.TrimSpace(binding.Interface),
		sourceIP: strings.TrimSpace(binding.SourceIP),
	}
	client := portalClient(recipe, bound, false, nil)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, portalProbeURL, nil)
	if err != nil {
		return false
	}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
	return resp.StatusCode == http.StatusNoContent
}
