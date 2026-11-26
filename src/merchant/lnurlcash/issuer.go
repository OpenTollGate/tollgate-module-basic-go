package lnurlcash

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// The issuer client. Every URL it dials came from an untrusted note, so the
// defences live here rather than being borrowed: the shared
// `lightning.ssrfSafeClient` has NO CheckRedirect (a 302 would carry the k1
// bearer secret to whatever host the redirect names), its dial guard relies on
// net.IP.IsPrivate (which does not cover CGNAT 100.64.0.0/10, i.e. the address
// space a carrier-grade or Tailscale-style network hands out), and its
// validateCallbackURL is not called on any path. Those are the three defects this
// client exists to not have.

const (
	// maxIssuerBodyBytes bounds an issuer response. A withdrawRequest is a few
	// hundred bytes; 64 KiB is generous and still small enough for a router.
	maxIssuerBodyBytes = 64 << 10

	// cgnatLow/cgnatHigh delimit 100.64.0.0/10, the shared address space
	// (RFC 6598). Go's net.IP.IsPrivate covers RFC 1918 and the ULA ranges but
	// NOT this one, which is why it is spelled out.
	cgnatLow  = 100<<24 | 64<<16
	cgnatHigh = 100<<24 | 127<<16 | 255<<8 | 255
)

// ClientOptions configures an issuer Client.
type ClientOptions struct {
	// Timeout bounds one issuer request, body included.
	Timeout time.Duration
	// AllowInsecureLoopbackForTests is the ONLY way to dial a plain-http
	// loopback address. It exists so the unit tests can drive a real
	// httptest.Server; production leaves it false, and nothing in the
	// daemon's wiring sets it.
	AllowInsecureLoopbackForTests bool
}

// Client talks to note issuers over LUD-03, with the SSRF and redirect
// defences this package owns.
type Client struct {
	http                          *http.Client
	allowInsecureLoopbackForTests bool
	timeout                       time.Duration
}

// NewClient builds the issuer client. One client is shared by every redemption;
// it holds no per-note state.
func NewClient(opts ClientOptions) *Client {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	c := &Client{
		allowInsecureLoopbackForTests: opts.AllowInsecureLoopbackForTests,
		timeout:                       timeout,
	}
	transport := &http.Transport{
		Proxy: nil, // never obey an environment proxy: it would defeat the dial guard
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, fmt.Errorf("issuer dial: bad address: %w", err)
			}
			ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, fmt.Errorf("issuer dial: resolve %q: %w", host, err)
			}
			if len(ips) == 0 {
				return nil, fmt.Errorf("issuer dial: %q resolved to no address", host)
			}
			// Every resolved address is checked before any is dialled: a name
			// that mixes a public and a private answer must not be reachable
			// through whichever one the resolver happened to return first.
			for _, candidate := range ips {
				if blockedAddress(candidate.IP) && !(c.allowInsecureLoopbackForTests && candidate.IP.IsLoopback()) {
					return nil, fmt.Errorf("issuer dial: %q resolves to the blocked address %s", host, candidate.IP)
				}
			}
			dialer := &net.Dialer{Timeout: 10 * time.Second}
			return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
		},
		TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
		// An issuer that wants a different host answers with a redirect; the
		// redirect is refused, never followed. This is the single most important
		// line in the file: net/http's default follows up to 10 redirects and
		// carries the request — query string, k1 and all — to each one.
	}
	c.http = &http.Client{
		Timeout:   timeout,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return fmt.Errorf("issuer redirect refused: %s is not where the note pointed", req.URL.Host)
		},
	}
	return c
}

// blockedAddress reports whether ip is an address class the gate must never be
// steered at by an untrusted note: loopback (the gate's own API and the
// NoDogSplash control surface), RFC 1918 and IPv6 ULA (the customers' LAN),
// link-local (cloud metadata, and the router's own neighbours), CGNAT (RFC 6598
// — a carrier or overlay network, never an issuer), unspecified, and multicast.
func blockedAddress(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsUnspecified() || ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() {
		return true
	}
	if v4 := ip.To4(); v4 != nil {
		n := uint32(v4[0])<<24 | uint32(v4[1])<<16 | uint32(v4[2])<<8 | uint32(v4[3])
		if n >= cgnatLow && n <= cgnatHigh {
			return true
		}
	}
	return false
}

// WithdrawRequest is the LUD-03 `withdrawRequest` document, reduced to the
// fields this package acts on.
type WithdrawRequest struct {
	Callback            *url.URL
	K1                  string
	MaxWithdrawableMSat uint64
	DefaultDescription  string
}

type withdrawRequestWire struct {
	Tag                string          `json:"tag"`
	Callback           string          `json:"callback"`
	K1                 string          `json:"k1"`
	MaxWithdrawable    json.RawMessage `json:"maxWithdrawable"`
	DefaultDescription string          `json:"defaultDescription"`
}

// FetchWithdrawRequest performs step 1 of the redemption: GET the note's
// target and validate the document it answers with.
//
// The validation that matters is the callback host: it must be the SAME host the
// note itself named. Without that rule a hostile note can serve a withdrawRequest
// whose callback points anywhere, which is precisely how the k1 secret would be
// walked off the box.
func (c *Client) FetchWithdrawRequest(ctx context.Context, noteURL string) (*WithdrawRequest, error) {
	noteTarget, err := url.Parse(noteURL)
	if err != nil {
		return nil, errors.New("note URL is unparseable")
	}
	if err := c.validateURL(noteTarget); err != nil {
		return nil, err
	}

	body, err := c.get(ctx, noteTarget)
	if err != nil {
		return nil, err
	}

	var wire withdrawRequestWire
	if err := json.Unmarshal(body, &wire); err != nil {
		return nil, errors.New("issuer answered a withdrawRequest that is not JSON")
	}
	if wire.Tag != "withdrawRequest" {
		return nil, fmt.Errorf("issuer answered tag %q, not \"withdrawRequest\"", wire.Tag)
	}
	if wire.K1 == "" {
		return nil, errors.New("issuer's withdrawRequest carries no k1")
	}
	if wire.Callback == "" {
		return nil, errors.New("issuer's withdrawRequest carries no callback")
	}

	callback, err := url.Parse(wire.Callback)
	if err != nil {
		return nil, errors.New("issuer's callback is not a URL")
	}
	if err := c.validateURL(callback); err != nil {
		return nil, fmt.Errorf("issuer's callback is unusable: %w", err)
	}
	if !strings.EqualFold(callback.Host, noteTarget.Host) {
		return nil, fmt.Errorf("issuer's callback host %q is not the note's host %q — refusing to send the note secret to a third party",
			callback.Host, noteTarget.Host)
	}

	maxMsat, err := parseMsat(wire.MaxWithdrawable)
	if err != nil {
		return nil, fmt.Errorf("issuer's maxWithdrawable is unusable: %w", err)
	}
	if maxMsat == 0 {
		return nil, errors.New("issuer's maxWithdrawable is zero: the note is worth nothing to price")
	}

	return &WithdrawRequest{
		Callback:            callback,
		K1:                  wire.K1,
		MaxWithdrawableMSat: maxMsat,
		DefaultDescription:  wire.DefaultDescription,
	}, nil
}

// Redemption is the classified outcome of step 3. It is deliberately three-way:
// OK (the issuer says it paid), refused (the issuer says it will not), and
// ambiguous (everything else — a timeout, a 5xx, a body that is neither). An
// ambiguous answer may still have moved money, so it is never collapsed into
// either of the other two.
type Redemption struct {
	OK        bool
	Ambiguous bool
	Reason    string
}

type redemptionWire struct {
	Status string `json:"status"`
	Reason string `json:"reason"`
}

// RequestRedemption performs step 3: ask the issuer to melt the note into OUR
// invoice. pr must be the BOLT11 invoice our own accepted mint issued for this
// redemption; the issuer pays it, and that payment is the only thing that turns
// the note into value.
func (c *Client) RequestRedemption(ctx context.Context, callback *url.URL, k1, pr string) (Redemption, error) {
	if k1 == "" || pr == "" {
		return Redemption{}, errors.New("redemption needs both k1 and an invoice")
	}
	if err := c.validateURL(callback); err != nil {
		return Redemption{}, err
	}

	target := *callback
	q := target.Query()
	q.Set("k1", k1)
	q.Set("pr", pr)
	target.RawQuery = q.Encode()

	status, body, err := c.getRaw(ctx, &target)
	if err != nil {
		// Transport-level failure: the request may or may not have been
		// processed. That is the definition of ambiguous.
		return Redemption{Ambiguous: true, Reason: "the issuer did not answer"}, nil
	}

	var wire redemptionWire
	parsed := json.Unmarshal(body, &wire) == nil

	switch {
	case status == http.StatusOK && parsed && strings.EqualFold(wire.Status, "OK"):
		return Redemption{OK: true}, nil
	case parsed && strings.EqualFold(wire.Status, "ERROR"):
		reason := strings.TrimSpace(wire.Reason)
		if reason == "" {
			reason = "the issuer refused the redemption"
		}
		return Redemption{Reason: reason}, nil
	default:
		return Redemption{Ambiguous: true, Reason: "the issuer's answer could not be classified"}, nil
	}
}

// validateURL is the per-hop check applied to every URL this client is about to
// dial. The dial guard already blocks internal address classes; this one refuses
// the SHAPES (scheme, userinfo, fragment) so a bad URL is rejected before a name
// is even resolved.
func (c *Client) validateURL(u *url.URL) error {
	if u == nil || u.Host == "" {
		return errors.New("issuer URL has no host")
	}
	if u.User != nil {
		return errors.New("issuer URL carries userinfo")
	}
	if u.Fragment != "" {
		return errors.New("issuer URL carries a fragment")
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		// Plain http is allowed only through the explicitly named test seam,
		// and only for a loopback literal — never for a hostname, which could
		// resolve anywhere.
		if c.allowInsecureLoopbackForTests && isLoopbackLiteral(u.Hostname()) {
			return nil
		}
		return errors.New("issuer URL must be https")
	default:
		return fmt.Errorf("issuer URL scheme %q is not allowed", u.Scheme)
	}
}

func isLoopbackLiteral(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (c *Client) get(ctx context.Context, u *url.URL) ([]byte, error) {
	status, body, err := c.getRaw(ctx, u)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("issuer answered HTTP %d", status)
	}
	return body, nil
}

// getRaw performs the request and returns the status and a bounded body. The
// error text never includes the URL: the URL carries the k1 secret.
func (c *Client) getRaw(ctx context.Context, u *url.URL) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return 0, nil, errors.New("could not build the issuer request")
	}
	req.Header.Set("User-Agent", "tollgate-lnurlcash/1")
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return 0, nil, err
		}
		// Wrapped without the URL: net/http's own error text includes it.
		return 0, nil, fmt.Errorf("issuer request failed: %T", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxIssuerBodyBytes+1))
	if err != nil {
		return 0, nil, errors.New("issuer response read failed")
	}
	if len(body) > maxIssuerBodyBytes {
		return 0, nil, fmt.Errorf("issuer response too large (over %d bytes)", maxIssuerBodyBytes)
	}
	return resp.StatusCode, body, nil
}

// parseMsat reads maxWithdrawable, which the LUD-03 spec defines as millisats
// but which issuers in the wild have occasionally sent as a string. A negative
// or malformed value is an error, not a zero.
func parseMsat(raw json.RawMessage) (uint64, error) {
	if len(raw) == 0 {
		return 0, errors.New("absent")
	}
	var n uint64
	if err := json.Unmarshal(raw, &n); err == nil {
		return n, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		s = strings.TrimSpace(s)
		if s == "" {
			return 0, errors.New("empty")
		}
		for _, r := range s {
			if r < '0' || r > '9' {
				return 0, errors.New("not a number of millisatoshis")
			}
		}
		var out uint64
		for _, r := range s {
			digit := uint64(r - '0')
			if out > (^uint64(0)-digit)/10 {
				return 0, errors.New("overflows")
			}
			out = out*10 + digit
		}
		return out, nil
	}
	return 0, errors.New("not a number of millisatoshis")
}
