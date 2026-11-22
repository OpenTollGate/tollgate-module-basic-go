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
	mode       string
	pageURL    string
	userField  string
	passField  string
	username   string
	password   string
	extra      string
	timeout    time.Duration
	mark       int
	dns        string
	allowHosts []string
}

// portalBinding pins one attempt to an uplink. The portal request must leave
// through the interface that owns the session: with two STAs the default route
// belongs to the other uplink, so an unbound request reaches the wrong venue
// (or no venue at all).
type portalBinding struct {
	iface    string
	sourceIP string
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
		mode:       mode,
		pageURL:    strings.TrimSpace(um.config.PortalLoginURL),
		userField:  userField,
		passField:  passField,
		username:   um.config.PortalLoginUsername,
		password:   um.config.PortalLoginPassword,
		extra:      um.config.PortalLoginExtra,
		timeout:    timeout,
		mark:       um.config.PortalLoginMark,
		dns:        strings.TrimSpace(um.config.PortalLoginDNS),
		allowHosts: append([]string(nil), um.config.PortalLoginAllowHosts...),
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

	netdev := ""
	if um.connector != nil {
		if n, err := um.connector.GetSTANetdev(id.iface); err == nil {
			netdev = n
		}
	}
	recipe := um.portalRecipe()
	binding := PortalBinding{
		Interface: netdev,
		Mark:      um.config.PortalLoginMark,
		DNS:       um.config.PortalLoginDNS,
	}
	result := AttemptPortalLogin(context.Background(), binding, PortalRecipe{
		Mode:       recipe.mode,
		PageURL:    recipe.pageURL,
		UserField:  recipe.userField,
		PassField:  recipe.passField,
		Username:   recipe.username,
		Password:   recipe.password,
		Extra:      recipe.extra,
		Timeout:    recipe.timeout,
		AllowHosts: recipe.allowHosts,
	}, func(context.Context) bool {
		return um.probeConnectivity(netdev).OK
	})

	switch result.Outcome {
	case PortalOK:
		logger.WithFields(map[string]interface{}{
			"portal_host": result.PortalHost,
			"mode":        recipe.mode,
			"interface":   netdev,
		}).Info("Captive portal login succeeded")
		return true
	case PortalNoPortal:
		logger.WithField("interface", netdev).Warn("Captive portal login: no portal URL discovered; leaving the manual window open")
	default:
		logger.WithFields(map[string]interface{}{
			"portal_host": result.PortalHost,
			"mode":        recipe.mode,
			"interface":   netdev,
			"reason":      result.Reason,
		}).Warn("Captive portal login did not reach the internet")
	}
	return false
}

// portalResolver returns a resolver pointed at the venue's DNS. Without it,
// the router's dnsmasq may still be using the other uplink's resolver and
// return NXDOMAIN for the portal host (measured with ASK4: the portal name
// resolved only on 10.32.64.1). The resolver socket uses the same uplink
// binding as the HTTP request.
func portalResolver(dns string, binding portalBinding) *net.Resolver {
	dns = strings.TrimSpace(dns)
	if dns == "" {
		return nil
	}
	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
			d := net.Dialer{Timeout: 5 * time.Second, Control: portalControl(0, binding)}
			return d.DialContext(ctx, "udp", net.JoinHostPort(dns, "53"))
		},
	}
}

// portalControl stamps SO_MARK on the login socket and, when an uplink is
// named, binds the socket to that device. The bind is the part that makes a
// per-uplink login possible: the default route belongs to the other STA.
//
// TollGate's NDS chain DNATs router-originated TCP/80 to 192.168.99.1:2050,
// but returns for the trusted/authenticated marks; marking the socket 0x20000
// lets the venue portal request leave instead of being swallowed by our own
// portal.
func portalControl(mark int, binding portalBinding) func(string, string, syscall.RawConn) error {
	if mark <= 0 && binding.iface == "" && binding.sourceIP == "" {
		return nil
	}
	return func(_, _ string, c syscall.RawConn) error {
		var serr error
		if err := c.Control(func(fd uintptr) {
			if binding.iface != "" {
				if e := syscall.SetsockoptString(int(fd), syscall.SOL_SOCKET, syscall.SO_BINDTODEVICE, binding.iface); e != nil {
					serr = e
					return
				}
			}
			if mark > 0 {
				serr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, soMark, mark)
			}
		}); err != nil {
			return err
		}
		return serr
	}
}

func portalTransport(recipe portalRecipe, binding portalBinding) *http.Transport {
	dialer := &net.Dialer{Timeout: recipe.timeout, Control: portalControl(recipe.mark, binding)}
	if binding.sourceIP != "" {
		if ip := net.ParseIP(binding.sourceIP); ip != nil {
			dialer.LocalAddr = &net.TCPAddr{IP: ip}
		}
	}
	resolver := portalResolver(recipe.dns, binding)
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

func portalClient(recipe portalRecipe, binding portalBinding, followSameHost bool, jar http.CookieJar) *http.Client {
	return &http.Client{
		Timeout:   recipe.timeout,
		Transport: portalTransport(recipe, binding),
		Jar:       jar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if !followSameHost || len(via) == 0 {
				return http.ErrUseLastResponse
			}
			if samePortalHost(req.URL, via[0].URL) || portalHostAllowed(req.URL, recipe.allowHosts) {
				return nil
			}
			return http.ErrUseLastResponse
		},
	}
}

// portalHostAllowed reports whether u is in the operator's explicit allow-list.
// An entry without a port matches any port on that host; an entry with a port
// must match host and port. The allow-list is for redirects and form actions,
// never for arbitrary credential destinations.
func portalHostAllowed(u *url.URL, allow []string) bool {
	if u == nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	hostPort := strings.ToLower(u.Host)
	for _, raw := range allow {
		entry := strings.ToLower(strings.TrimSpace(raw))
		if entry == "" {
			continue
		}
		if strings.Contains(entry, ":") {
			if entry == hostPort {
				return true
			}
			continue
		}
		if entry == host {
			return true
		}
	}
	return false
}

// discoverPortalURL asks the captive probe where the portal lives. A 3xx
// Location or a meta refresh is the portal; a 204 is no portal at all.
func discoverPortalURL(ctx context.Context, recipe portalRecipe, binding portalBinding, jar http.CookieJar) string {
	client := portalClient(recipe, binding, false, jar)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, portalProbeURL, nil)
	if err != nil {
		return ""
	}
	resp, err := client.Do(req)
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

func fetchPortalPage(ctx context.Context, pageURL string, recipe portalRecipe, binding portalBinding, jar http.CookieJar) (string, *url.URL, error) {
	start, err := url.Parse(pageURL)
	if err != nil || (start.Scheme != "http" && start.Scheme != "https") || start.Host == "" {
		return "", nil, fmt.Errorf("portal URL must be http(s) with a host")
	}
	client := portalClient(recipe, binding, true, jar)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, start.String(), nil)
	if err != nil {
		return "", nil, err
	}
	resp, err := client.Do(req)
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

func parsePortalForm(page string, base *url.URL, allowHosts []string) (*portalForm, error) {
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
	if !samePortalHost(action, base) && !portalHostAllowed(action, allowHosts) {
		return nil, fmt.Errorf("portal form action points at a host that is neither the page host nor allow-listed (%s); refusing to send credentials there", action.Host)
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

func submitPortalForm(ctx context.Context, form *portalForm, recipe portalRecipe, binding portalBinding, jar http.CookieJar) error {
	client := portalClient(recipe, binding, false, jar)
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
		req, err = http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	} else {
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, form.action.String(), strings.NewReader(form.fields.Encode()))
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
