// Automatic captive-portal login for upstream WiFi.
//
// The existing portal path only DETECTS a captive portal and opens a two-minute
// window for a human to log in (portal_login.go). That is not enough for a
// router whose upstream is a hotel or venue network: the TollGate is unattended,
// the bond cannot form until the portal admits it, and every failed probe looks
// like a dead uplink to the manager.
//
// This file adds an opt-in, bounded attempt:
//
//   - discover the portal URL from the captive probe's redirect (or use the
//     configured URL);
//   - parse the portal's first form;
//   - either submit its own hidden fields + accept button (`click_through`) or
//     also fill the configured username/password fields (`form`);
//   - re-probe; success is real internet, not a 200 from the portal.
//
// It is deliberately conservative. Credentials only go to the portal page's
// own host; a cross-host form action is refused, redirects are not followed
// after submission, bodies are bounded, and a failure falls back to the
// existing manual window rather than blacklisting the uplink. The password is
// never logged.
package wireless_gateway_manager

import (
	"context"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"syscall"
	"time"
)

// SO_MARK is Linux-specific and absent from the syscall package on some
// architectures; the router is Linux, so the constant is explicit.
const soMark = 36

const (
	portalLoginModeClickThrough = "click_through"
	portalLoginModeForm         = "form"
	portalMaxBody               = 64 << 10
)

// portalProbeURL is a variable so tests can point discovery at a local
// fixture; production keeps the same endpoint the connectivity probe uses.
var portalProbeURL = "http://connectivitycheck.gstatic.com/generate_204"

type portalRecipe struct {
	mode      string
	pageURL   string
	userField string
	passField string
	username  string
	password  string
	extra     string
	timeout   time.Duration
	mark      int
	dns       string
}

type portalForm struct {
	action *url.URL
	method string
	fields url.Values
}

var (
	portalFormRe    = regexp.MustCompile(`(?is)<form\b([^>]*)>(.*?)</form>`)
	portalInputRe   = regexp.MustCompile(`(?is)<(?:input|button)\b([^>]*)>`)
	portalAttrRe    = regexp.MustCompile(`(?is)([a-zA-Z_:][-a-zA-Z0-9_:.]*)\s*=\s*("([^"]*)"|'([^']*)'|([^\s>]+))`)
	portalRefreshRe = regexp.MustCompile(`(?is)http-equiv\s*=\s*["']?refresh["']?[^>]*content\s*=\s*["'][^"']*url\s*=\s*([^"';]+)`)
)

// tryAutoPortalLogin is the seam tests use to prove the observation path calls
// automatic login without performing HTTP.
func (um *UpstreamManager) tryAutoPortalLogin(id upstreamIdentity) bool {
	if !um.config.PortalAutoLogin {
		return false
	}
	if um.portalAutoLoginFn != nil {
		return um.portalAutoLoginFn(id)
	}
	return um.attemptPortalLogin(id)
}

func (um *UpstreamManager) portalRecipe() portalRecipe {
	mode := strings.ToLower(strings.TrimSpace(um.config.PortalLoginMode))
	if mode != portalLoginModeForm {
		mode = portalLoginModeClickThrough
	}
	userField := strings.TrimSpace(um.config.PortalLoginUserField)
	if userField == "" {
		userField = "username"
	}
	passField := strings.TrimSpace(um.config.PortalLoginPassField)
	if passField == "" {
		passField = "password"
	}
	timeout := um.config.PortalLoginTimeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return portalRecipe{
		mode:      mode,
		pageURL:   strings.TrimSpace(um.config.PortalLoginURL),
		userField: userField,
		passField: passField,
		username:  um.config.PortalLoginUsername,
		password:  um.config.PortalLoginPassword,
		extra:     um.config.PortalLoginExtra,
		timeout:   timeout,
		mark:      um.config.PortalLoginMark,
		dns:       strings.TrimSpace(um.config.PortalLoginDNS),
	}
}

// attemptPortalLogin performs at most one attempt per connection generation.
// It runs before the caller takes writerMu.
func (um *UpstreamManager) attemptPortalLogin(id upstreamIdentity) bool {
	um.portalAutoMu.Lock()
	if um.portalAutoAttempted && um.portalAutoGeneration == id.generation {
		um.portalAutoMu.Unlock()
		return false
	}
	um.portalAutoGeneration = id.generation
	um.portalAutoAttempted = true
	um.portalAutoMu.Unlock()

	recipe := um.portalRecipe()
	pageURL := recipe.pageURL
	if pageURL == "" {
		pageURL = discoverPortalURL(recipe)
	}
	if pageURL == "" {
		logger.Warn("Captive portal login: no portal URL discovered; leaving the manual window open")
		return false
	}

	page, base, err := fetchPortalPage(pageURL, recipe)
	if err != nil {
		logger.WithError(err).WithField("portal_host", portalHost(pageURL)).Warn("Captive portal login: could not fetch the portal page")
		return false
	}
	form, err := parsePortalForm(page, base)
	if err != nil {
		logger.WithError(err).WithField("portal_host", base.Host).Warn("Captive portal login: no usable form found")
		return false
	}
	if recipe.mode == portalLoginModeForm {
		applyPortalCredentials(form, recipe)
	}
	if err := mergePortalExtra(form, recipe.extra); err != nil {
		logger.WithError(err).Warn("Captive portal login: malformed extra fields")
		return false
	}
	if err := submitPortalForm(form, recipe); err != nil {
		logger.WithError(err).WithField("portal_host", form.action.Host).Warn("Captive portal login: submission failed")
		return false
	}

	result := um.probeConnectivity("")
	if result.OK {
		logger.WithFields(map[string]interface{}{
			"portal_host": form.action.Host,
			"mode":        recipe.mode,
		}).Info("Captive portal login succeeded")
		return true
	}
	logger.WithFields(map[string]interface{}{
		"portal_host": form.action.Host,
		"mode":        recipe.mode,
		"reason":      result.Reason,
	}).Warn("Captive portal login submitted but internet is still unavailable")
	return false
}

// portalResolver returns a resolver pointed at the venue's DNS. Without it,
// the router's dnsmasq may still be using the other uplink's resolver and
// return NXDOMAIN for the portal host (measured with ASK4: the portal name
// resolved only on 10.32.64.1).
func portalResolver(dns string) *net.Resolver {
	dns = strings.TrimSpace(dns)
	if dns == "" {
		return nil
	}
	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
			d := net.Dialer{Timeout: 5 * time.Second}
			return d.DialContext(ctx, "udp", net.JoinHostPort(dns, "53"))
		},
	}
}

// portalMarkControl stamps SO_MARK on the login socket. TollGate's NDS chain
// DNATs router-originated TCP/80 to 192.168.99.1:2050, but returns for the
// trusted/authenticated marks; marking the socket 0x20000 lets the venue
// portal request leave instead of being swallowed by our own portal.
func portalMarkControl(mark int) func(string, string, syscall.RawConn) error {
	if mark <= 0 {
		return nil
	}
	return func(_, _ string, c syscall.RawConn) error {
		var serr error
		if err := c.Control(func(fd uintptr) {
			serr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, soMark, mark)
		}); err != nil {
			return err
		}
		return serr
	}
}

func portalTransport(recipe portalRecipe) *http.Transport {
	dialer := &net.Dialer{Timeout: recipe.timeout, Control: portalMarkControl(recipe.mark)}
	resolver := portalResolver(recipe.dns)
	return &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			if resolver != nil && net.ParseIP(host) == nil {
				ips, err := resolver.LookupHost(ctx, host)
				if err != nil {
					return nil, err
				}
				if len(ips) == 0 {
					return nil, fmt.Errorf("no addresses for portal host %s", host)
				}
				address = net.JoinHostPort(ips[0], port)
			}
			return dialer.DialContext(ctx, network, address)
		},
	}
}

func portalClient(recipe portalRecipe, followSameHost bool) *http.Client {
	return &http.Client{
		Timeout:   recipe.timeout,
		Transport: portalTransport(recipe),
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if !followSameHost || (len(via) > 0 && !samePortalHost(req.URL, via[0].URL)) {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}
}

// discoverPortalURL asks the captive probe where the portal lives. A 3xx
// Location or a meta refresh is the portal; a 204 is no portal at all.
func discoverPortalURL(recipe portalRecipe) string {
	client := portalClient(recipe, false)
	resp, err := client.Get(portalProbeURL)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		if loc := strings.TrimSpace(resp.Header.Get("Location")); loc != "" {
			base, err := url.Parse(portalProbeURL)
			if err != nil {
				return ""
			}
			ref, err := url.Parse(loc)
			if err != nil {
				return ""
			}
			return base.ResolveReference(ref).String()
		}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, portalMaxBody))
	if err != nil {
		return ""
	}
	if m := portalRefreshRe.FindStringSubmatch(string(body)); len(m) == 2 {
		base, err := url.Parse(resp.Request.URL.String())
		if err != nil {
			return ""
		}
		ref, err := url.Parse(html.UnescapeString(strings.TrimSpace(m[1])))
		if err != nil {
			return ""
		}
		return base.ResolveReference(ref).String()
	}
	return ""
}

func fetchPortalPage(pageURL string, recipe portalRecipe) (string, *url.URL, error) {
	start, err := url.Parse(pageURL)
	if err != nil || (start.Scheme != "http" && start.Scheme != "https") || start.Host == "" {
		return "", nil, fmt.Errorf("portal URL must be http(s) with a host")
	}
	client := portalClient(recipe, true)
	resp, err := client.Get(start.String())
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", nil, fmt.Errorf("portal returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, portalMaxBody))
	if err != nil {
		return "", nil, err
	}
	return string(body), resp.Request.URL, nil
}

func parsePortalForm(page string, base *url.URL) (*portalForm, error) {
	m := portalFormRe.FindStringSubmatch(page)
	if len(m) != 3 {
		return nil, fmt.Errorf("no <form> on the portal page")
	}
	attrs := parsePortalAttrs(m[1])
	action := base
	if raw := strings.TrimSpace(attrs["action"]); raw != "" {
		ref, err := url.Parse(html.UnescapeString(raw))
		if err != nil {
			return nil, fmt.Errorf("portal form action: %w", err)
		}
		action = base.ResolveReference(ref)
	}
	if action.Scheme != "http" && action.Scheme != "https" {
		return nil, fmt.Errorf("portal form action is not http(s)")
	}
	if !samePortalHost(action, base) {
		return nil, fmt.Errorf("portal form action points at a different host (%s); refusing to send credentials there", action.Host)
	}

	method := strings.ToUpper(strings.TrimSpace(attrs["method"]))
	if method != "POST" {
		method = "GET"
	}
	fields := url.Values{}
	for _, input := range portalInputRe.FindAllStringSubmatch(m[2], -1) {
		raw, attrs := input[1], parsePortalAttrs(input[1])
		typ := strings.ToLower(strings.TrimSpace(attrs["type"]))
		if typ == "" {
			typ = "text"
		}
		name := strings.TrimSpace(attrs["name"])
		if name == "" {
			continue
		}
		switch typ {
		case "file", "image", "reset":
			continue
		case "checkbox", "radio":
			if !hasPortalAttr(raw, "checked") {
				continue
			}
		}
		fields.Add(name, html.UnescapeString(attrs["value"]))
	}
	// A GET form with no named fields is a real portal shape: ASK4's
	// "Continue online" button is <form method="get" action="..."><input
	// type="submit" value="Continue online"></form>. Submitting the action
	// with an empty query is the click-through. A POST form with no fields is
	// more likely a parse failure, so keep that loud.
	if len(fields) == 0 && method != "GET" {
		return nil, fmt.Errorf("portal POST form has no submittable fields")
	}
	return &portalForm{action: action, method: method, fields: fields}, nil
}

func parsePortalAttrs(s string) map[string]string {
	out := map[string]string{}
	for _, m := range portalAttrRe.FindAllStringSubmatch(s, -1) {
		if len(m) < 6 {
			continue
		}
		value := m[3]
		if value == "" {
			value = m[4]
		}
		if value == "" {
			value = m[5]
		}
		out[strings.ToLower(m[1])] = value
	}
	return out
}

func hasPortalAttr(raw, name string) bool {
	name = strings.ToLower(name)
	for _, m := range portalAttrRe.FindAllStringSubmatch(raw, -1) {
		if strings.ToLower(m[1]) == name {
			return true
		}
	}
	return false
}

func applyPortalCredentials(form *portalForm, recipe portalRecipe) {
	if recipe.username == "" && recipe.password == "" {
		return
	}
	form.fields.Set(recipe.userField, recipe.username)
	form.fields.Set(recipe.passField, recipe.password)
}

func mergePortalExtra(form *portalForm, extra string) error {
	extra = strings.TrimSpace(extra)
	if extra == "" {
		return nil
	}
	values, err := url.ParseQuery(extra)
	if err != nil {
		return err
	}
	for k, vs := range values {
		for _, v := range vs {
			form.fields.Add(k, v)
		}
	}
	return nil
}

func submitPortalForm(form *portalForm, recipe portalRecipe) error {
	client := portalClient(recipe, false)
	var req *http.Request
	var err error
	if form.method == "GET" {
		u := *form.action
		q := u.Query()
		for k, vs := range form.fields {
			for _, v := range vs {
				q.Add(k, v)
			}
		}
		u.RawQuery = q.Encode()
		req, err = http.NewRequest(http.MethodGet, u.String(), nil)
	} else {
		req, err = http.NewRequest(http.MethodPost, form.action.String(), strings.NewReader(form.fields.Encode()))
		if req != nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
	}
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, portalMaxBody))
	if resp.StatusCode >= 400 {
		return fmt.Errorf("portal rejected the login with HTTP %d", resp.StatusCode)
	}
	return nil
}

func samePortalHost(a, b *url.URL) bool {
	if a == nil || b == nil {
		return false
	}
	if (a.Scheme != "http" && a.Scheme != "https") || (b.Scheme != "http" && b.Scheme != "https") {
		return false
	}
	return strings.EqualFold(a.Host, b.Host)
}

func portalHost(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Host
}
