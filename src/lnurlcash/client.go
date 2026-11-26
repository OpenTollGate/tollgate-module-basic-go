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

// The issuer is reached over the internet with a URL that came from an
// untrusted note, so this client is written as if the network is hostile: no
// redirects, no private addresses, no cleartext, bounded bodies and timeouts.
// It is the only component here that talks to a third party, and every refusal
// it produces is either DEFINITIVE (the issuer said no) or AMBIGUOUS (we do not
// know) — a distinction the caller must not flatten, because re-posting a spend
// after an ambiguous answer is how a note is consumed twice.

const (
	// maxIssuerBody bounds a withdraw/callback response. The shipped issuer
	// responses are a few hundred bytes; 8 KiB leaves room for a certificate and
	// refuses a body that is trying to be a memory primitive.
	maxIssuerBody = 8 << 10
	// issuerRequestTimeout bounds one issuer round trip. A redemption that hangs
	// must become AMBIGUOUS promptly, not hold the customer's request open.
	issuerRequestTimeout = 15 * time.Second
)

// IssuerError is a refusal by, or a failure to reach, the issuer. Definitive
// errors mean the issuer answered and the note is not (or no longer) redeemable;
// ambiguous errors mean the outcome is unknown and MUST be reconciled, never
// retried blindly.
type IssuerError struct {
	// Reason is the issuer's own `reason` string when it sent one. It is free
	// text unless LUD-25 fixed it ("already in use", "pending", "unknown
	// note"), so it is for the operator, not for branching on.
	Reason string
	// Definitive is true only when the issuer answered with a refusal that says
	// the note will never work.
	Definitive bool
	// Err is the transport or protocol failure, if any.
	Err error
}

func (e *IssuerError) Error() string {
	switch {
	case e.Reason != "" && e.Definitive:
		return fmt.Sprintf("lnurlcash: issuer refused the note: %s", e.Reason)
	case e.Reason != "":
		return fmt.Sprintf("lnurlcash: issuer answered %q — outcome unknown", e.Reason)
	case e.Err != nil:
		return fmt.Sprintf("lnurlcash: issuer unreachable — outcome unknown: %v", e.Err)
	default:
		return "lnurlcash: issuer answered unexpectedly — outcome unknown"
	}
}

func (e *IssuerError) Unwrap() error { return e.Err }

// withdrawRequest is LUD-03/LUD-25's informational response for a note.
type withdrawRequest struct {
	Tag             string `json:"tag"`
	Callback        string `json:"callback"`
	K1              string `json:"k1"`
	MinWithdrawable int64  `json:"minWithdrawable"` // millisatoshis
	MaxWithdrawable int64  `json:"maxWithdrawable"` // millisatoshis
	PayLink         string `json:"payLink"`
	MintPubkey      string `json:"mintPubkey"`
	C               string `json:"c"`
	Status          string `json:"status"`
	Reason          string `json:"reason"`
}

// callbackResponse is the `status`-shaped answer a callback returns.
type callbackResponse struct {
	Status   string `json:"status"`
	Reason   string `json:"reason"`
	PR       string `json:"pr"`
	Verify   string `json:"verify"`
	Callback string `json:"callback"`
}

// IssuerState is what the issuer says about a note, from the informational GET.
type IssuerState struct {
	// WithdrawableMSat is the note's value the issuer is prepared to honour now;
	// 0 means burned, pending or unknown to the issuer.
	WithdrawableMSat uint64
	// MintPubkey is the issuer's certificate key, when it publishes one.
	MintPubkey string
	// Certificate is a fresh `cs1` the issuer issued for this query, when it
	// answers with one. It is cross-check evidence only.
	Certificate string
	// Callback is the endpoint a redemption must use. It is validated against
	// the note's own domain before use, so a hostile issuer cannot redirect the
	// spend to another host.
	Callback *url.URL
}

// Client talks to ONE issuer domain. A Client is created per redemption from the
// note's canonical domain, so a request can never be built for a host other than
// the one the allowlist admitted.
type Client struct {
	domain string
	http   *http.Client
	now    func() time.Time
}

// NewIssuerClient builds the production client for domain. The transport refuses
// loopback, private, link-local and unspecified addresses at dial time (after
// DNS resolution, so a name that resolves to 127.0.0.1 is refused too), refuses
// cleartext TLS downgrade, and never follows a redirect.
func NewIssuerClient(domain string) (*Client, error) {
	canonical, err := CanonicalDomain(&url.URL{Scheme: "https", Host: domain})
	if err != nil {
		return nil, err
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, err
			}
			for _, ip := range ips {
				if !publicAddress(ip.IP) {
					return nil, fmt.Errorf("lnurlcash: %s resolves to a non-public address (%s)", host, ip.IP)
				}
			}
			// Dial the address that passed the check, not the name: a second
			// lookup could answer differently (DNS rebinding).
			return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
		},
		TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout: 10 * time.Second,
		DisableKeepAlives:   true,
		MaxIdleConns:        1,
		ForceAttemptHTTP2:   false,
	}
	return &Client{
		domain: canonical,
		http: &http.Client{
			Transport: transport,
			Timeout:   issuerRequestTimeout,
			// A redirect is how a hostile issuer would move the spend to another
			// host after our checks; LUD-25 tells a WALLET not to follow one that
			// carries a spend, and the SERVICE side has no reason to follow any.
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return fmt.Errorf("lnurlcash: issuer redirect refused")
			},
		},
		now: time.Now,
	}, nil
}

// publicAddress reports whether ip is a routable unicast address. Anything
// else — loopback, private, link-local, multicast, unspecified — is refused:
// this client exists to reach a public issuer, and a note that names an internal
// address is an SSRF attempt, not a payment.
func publicAddress(ip net.IP) bool {
	if ip == nil || ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() ||
		ip.IsInterfaceLocalMulticast() {
		return false
	}
	// A v4-mapped address (::ffff:a.b.c.d) and a NAT64 address (64:ff9b::a.b.c.d)
	// both reach an IPv4 host. To4() handles the first; NAT64 must be unwrapped by
	// hand, or 64:ff9b::7f00:1 sneaks past every v4 rule below and dials 127.0.0.1.
	if v4 := ip.To4(); v4 != nil {
		return publicIPv4(v4)
	}
	if len(ip) == net.IPv6len && ip[0] == 0x00 && ip[1] == 0x64 && ip[2] == 0xff && ip[3] == 0x9b {
		// 64:ff9b::/96 — the last four bytes are the embedded IPv4 address.
		allZero := true
		for _, b := range ip[4:12] {
			if b != 0 {
				allZero = false
				break
			}
		}
		if allZero {
			return publicIPv4(net.IP(ip[12:16]))
		}
		return false
	}
	return true
}

// publicIPv4 applies the IPv4 rules to a 4-byte address.
//
// isPrivate/IsLoopback do not cover everything that must not be dialled. Named
// explicitly: 0.0.0.0/8 (only the exact 0.0.0.0 counts as unspecified, but the whole
// /8 is "this network"), 192.0.0.0/24, 198.18.0.0/15 (benchmarking), 240.0.0.0/4 and
// 255.255.255.255 (reserved/broadcast), and 100.64.0.0/10 (carrier NAT, where a
// router's own LAN peers often live).
func publicIPv4(v4 net.IP) bool {
	switch {
	case v4[0] == 0:
		return false
	case v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127:
		return false
	case v4[0] == 127:
		return false
	case v4[0] == 169 && v4[1] == 254:
		return false
	case v4[0] == 192 && v4[1] == 0 && v4[2] == 0:
		return false
	case v4[0] == 198 && (v4[1] == 18 || v4[1] == 19):
		return false
	case v4[0] >= 240:
		return false
	case v4[0] == 10:
		return false
	case v4[0] == 172 && v4[1] >= 16 && v4[1] <= 31:
		return false
	case v4[0] == 192 && v4[1] == 168:
		return false
	}
	return true
}

// issuerURL builds a request URL for this client's domain. The caller's path
// comes from the note or the issuer's own answer, and the query is built here so
// no caller-supplied parameter can reach the issuer unexamined.
// redactTransportError strips the request URL out of a transport failure. Go's
// *url.Error prints the full URL, and for a redemption that URL IS the spend —
// so an unredacted error would put a bearer secret in every log line that
// records the failure. The typed failure is kept; the URL is not.
func redactTransportError(err error) error {
	var uerr *url.Error
	if errors.As(err, &uerr) {
		// Keep only the operation and the wrapped cause; drop the URL entirely
		// rather than trying to trim the query, because a cp1 in the path or an
		// issuer-chosen parameter name would survive trimming.
		return fmt.Errorf("%s failed (URL withheld: it carries the spend): %w", uerr.Op, uerr.Err)
	}
	return err
}

func (c *Client) issuerURL(path string, params map[string]string) (*url.URL, error) {
	if !strings.HasPrefix(path, "/") {
		return nil, fmt.Errorf("%w: issuer path %q is not absolute", ErrMalformedNote, path)
	}
	q := url.Values{}
	for k, v := range params {
		q.Set(k, v)
	}
	return &url.URL{
		Scheme:   "https",
		Host:     c.domain,
		Path:     path,
		RawQuery: q.Encode(),
	}, nil
}

// get performs one bounded, non-redirecting GET and decodes the JSON answer.
func (c *Client) get(ctx context.Context, u *url.URL) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	// LUD-25: spends travel in URLs, so ask intermediaries not to store them.
	req.Header.Set("Cache-Control", "no-store")
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, redactTransportError(err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxIssuerBody+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxIssuerBody {
		return nil, fmt.Errorf("lnurlcash: issuer body exceeds %d bytes", maxIssuerBody)
	}
	if resp.StatusCode != http.StatusOK {
		// A 4xx/5xx is an answer about the REQUEST, not about the note: the spend
		// may still have been accepted, so this is ambiguous, never definitive.
		return nil, fmt.Errorf("lnurlcash: issuer answered HTTP %d", resp.StatusCode)
	}
	return body, nil
}

// Lookup asks the issuer what a note is worth, WITHOUT revealing the spend when
// the caller has no need to: it queries `?p=<cp1>` (LUD-25's "checking a note
// without exposing it"). The answer is the issuer's claim, which is not value
// until a redemption confirms it.
func (c *Client) Lookup(ctx context.Context, n Note, withdrawPath string) (IssuerState, error) {
	cp1, err := n.CP1()
	if err != nil {
		return IssuerState{}, err
	}
	u, err := c.issuerURL(withdrawPath, map[string]string{"p": cp1})
	if err != nil {
		return IssuerState{}, err
	}
	body, err := c.get(ctx, u)
	if err != nil {
		return IssuerState{}, &IssuerError{Err: err}
	}
	return c.parseWithdrawRequest(body, n.Domain)
}

// Info performs the informational GET WITH the spend (`?k1=`), which is the
// request that makes the issuer verify the spend in full before answering. It is
// used at redemption time, when the gate is about to spend the note anyway.
func (c *Client) Info(ctx context.Context, n Note, withdrawPath string) (IssuerState, error) {
	u, err := c.issuerURL(withdrawPath, map[string]string{"k1": n.Spend()})
	if err != nil {
		return IssuerState{}, err
	}
	body, err := c.get(ctx, u)
	if err != nil {
		return IssuerState{}, &IssuerError{Err: err}
	}
	return c.parseWithdrawRequest(body, n.Domain)
}

func (c *Client) parseWithdrawRequest(body []byte, domain string) (IssuerState, error) {
	var wr withdrawRequest
	if err := json.Unmarshal(body, &wr); err != nil {
		return IssuerState{}, &IssuerError{Err: fmt.Errorf("lnurlcash: issuer answer is not JSON")}
	}
	if strings.EqualFold(wr.Status, "ERROR") {
		// `unknown note`, `already in use`, `pending` and free-text script
		// refusals all arrive here. Only the ones LUD-25 fixes as permanent are
		// definitive; `pending` in particular is explicitly transient and must
		// not be read as a refusal to redeem.
		return IssuerState{}, &IssuerError{
			Reason:     wr.Reason,
			Definitive: isPermanentRefusal(wr.Reason),
		}
	}
	if wr.MinWithdrawable < 0 || wr.MaxWithdrawable < 0 {
		return IssuerState{}, &IssuerError{Err: fmt.Errorf("lnurlcash: issuer answered a negative withdrawable amount")}
	}
	if wr.MaxWithdrawable == 0 {
		return IssuerState{}, &IssuerError{Reason: "note is not withdrawable (burned, pending or unknown)", Definitive: false}
	}
	if wr.Callback == "" {
		return IssuerState{}, &IssuerError{Reason: "issuer published no callback", Definitive: true}
	}
	cb, err := url.Parse(wr.Callback)
	if err != nil || cb.Host == "" {
		return IssuerState{}, &IssuerError{Reason: "issuer published an unusable callback", Definitive: true}
	}
	cbDomain, derr := CanonicalDomain(cb)
	if derr != nil || cbDomain != domain {
		// The callback must live on the note's own domain: LUD-25 says one domain
		// hosts exactly one mint, and letting the callback cross hosts would hand
		// the spend to whoever the issuer names after our allowlist check.
		return IssuerState{}, &IssuerError{Reason: "issuer's callback is on a different host", Definitive: true}
	}
	if strings.ToLower(cb.Scheme) != "https" {
		return IssuerState{}, &IssuerError{Reason: "issuer's callback is not https", Definitive: true}
	}
	// Rebuild it on our own domain and path: nothing but the path is taken from
	// the issuer's answer.
	state := IssuerState{
		WithdrawableMSat: uint64(wr.MaxWithdrawable),
		MintPubkey:       strings.TrimSpace(wr.MintPubkey),
		Certificate:      strings.TrimSpace(wr.C),
		Callback:         &url.URL{Scheme: "https", Host: domain, Path: cb.Path},
	}
	return state, nil
}

// isPermanentRefusal reports whether an issuer reason means the note will never
// work. Only strings LUD-25 fixes are treated as permanent; everything else is
// reconciled rather than assumed.
func isPermanentRefusal(reason string) bool {
	switch strings.ToLower(strings.TrimSpace(reason)) {
	case "unknown note", "already in use":
		return true
	}
	return false
}

// Melt redeems a note by asking the issuer to pay invoice — the note is burned
// and the gate is paid, which is what makes the redemption prove itself.
//
// Idempotency: LUD-25 requires a melt retried with the SAME spend and the SAME
// invoice to return the original success, so a caller that reconciles by
// re-sending the identical request is safe. A DIFFERENT invoice for the same
// spend is refused by the issuer as `already in use`, and that refusal is
// definitive — the note went somewhere else.
func (c *Client) Melt(ctx context.Context, n Note, callback *url.URL, invoice string) error {
	if callback == nil || callback.Host == "" {
		return fmt.Errorf("%w: no callback to redeem at", ErrMalformedNote)
	}
	if cbDomain, err := CanonicalDomain(callback); err != nil || cbDomain != c.domain {
		return fmt.Errorf("%w: callback is not on the issuer's domain", ErrIssuerMismatch)
	}
	if !strings.HasPrefix(invoice, "lnbc") && !strings.HasPrefix(invoice, "lntb") {
		return fmt.Errorf("%w: a melt needs a bolt11 invoice", ErrMalformedNote)
	}
	u, err := c.issuerURL(callback.Path, map[string]string{"k1": n.Spend(), "pr": invoice})
	if err != nil {
		return err
	}
	body, err := c.get(ctx, u)
	if err != nil {
		// Transport failure or a non-200: the issuer may or may not have burned
		// the note. Ambiguous by construction.
		return &IssuerError{Err: err}
	}
	var cr callbackResponse
	if err := json.Unmarshal(body, &cr); err != nil {
		return &IssuerError{Err: fmt.Errorf("lnurlcash: callback answer is not JSON")}
	}
	if strings.EqualFold(cr.Status, "OK") {
		return nil
	}
	return &IssuerError{
		Reason:     cr.Reason,
		Definitive: isPermanentRefusal(cr.Reason),
	}
}

// Check asks the issuer for a note's current state by cp1 — the reconciliation
// call after an ambiguous melt. A note that is gone answers maxWithdrawable 0
// (burned or pending); one that is still outstanding answers its value, which
// means the melt did not happen and the money did not move.
func (c *Client) Check(ctx context.Context, n Note, withdrawPath string) (IssuerState, error) {
	return c.Lookup(ctx, n, withdrawPath)
}

// WithdrawableSats converts a msat answer to whole sats, rounding DOWN: a gate
// must never grant for a fraction of a satoshi the issuer would not honour.
func WithdrawableSats(msat uint64) uint64 { return msat / 1000 }

// String keeps a Client's domain printable without its transport.
func (c *Client) String() string { return "lnurlcash client for " + c.domain }
