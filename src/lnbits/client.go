// Package lnbits implements a bounded, wallet-bound LNbits v1.6 payment client.
// It never retries monetary requests. Callers must journal an intent before
// CreateInvoice or PayInvoice and reconcile uncertain results before retrying.
package lnbits

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	decodepay "github.com/nbd-wtf/ln-decodepay"
)

const maxResponse = 1 << 20

type Config struct {
	BaseURL    string
	WalletID   string
	Network    string       // BOLT11 currency: bc, tb, bcrt, or tbs.
	InvoiceKey string       `json:"-"`
	AdminKey   string       `json:"-"`
	HTTPClient *http.Client `json:"-"`
	// Only for explicitly selected local development endpoints; HTTPS is default.
	AllowInsecureLocal bool
}

func (Config) String() string   { return "LNbits configuration (credentials redacted)" }
func (Config) GoString() string { return "LNbits configuration (credentials redacted)" }

type Client struct {
	base                                    url.URL
	walletID, network, invoiceKey, adminKey string
	http                                    *http.Client
}
type Error struct {
	Code    string
	Status  int
	Unknown bool
}

func (e *Error) Error() string {
	if e.Unknown {
		return "LNbits request outcome unknown: " + e.Code
	}
	return "LNbits request refused: " + e.Code
}
func IsUnknown(err error) bool  { var e *Error; return errors.As(err, &e) && e.Unknown }
func refusal(code string) error { return &Error{Code: code} }

type Direction int

const (
	Incoming Direction = 1
	Outgoing Direction = -1
)

type Wallet struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	BalanceMSat int64  `json:"balance"`
}
type InvoiceRequest struct {
	AmountSat  uint64
	Memo       string
	Expiry     uint64
	ExternalID string
}
type Payment struct {
	PaymentHash    string `json:"payment_hash"`
	CheckingID     string `json:"checking_id"`
	WalletID       string `json:"wallet_id"`
	BOLT11         string `json:"bolt11"`
	PaymentRequest string `json:"payment_request"`
	AmountMSat     int64  `json:"amount"`
	FeeMSat        int64  `json:"fee"`
	Status         string `json:"status"`
	Preimage       string `json:"preimage"`
}

func (p Payment) Settled() bool { return p.Status == "success" }
func New(c Config) (*Client, error) {
	u, err := url.Parse(c.BaseURL)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.TrimSpace(c.BaseURL) != c.BaseURL {
		return nil, refusal("invalid-base-url")
	}
	if u.Scheme != "https" {
		ip := net.ParseIP(u.Hostname())
		if u.Scheme != "http" || !c.AllowInsecureLocal || ip == nil || !ip.IsLoopback() {
			return nil, refusal("https-required")
		}
	}
	if strings.TrimSpace(c.WalletID) == "" || len(c.WalletID) > 256 || !validKey(c.InvoiceKey) || (c.AdminKey != "" && !validKey(c.AdminKey)) {
		return nil, refusal("invalid-wallet-credentials")
	}
	switch c.Network {
	case "bc", "tb", "bcrt", "tbs":
	default:
		return nil, refusal("explicit-network-required")
	}
	h := &http.Client{Timeout: 30 * time.Second}
	if c.HTTPClient != nil {
		*h = *c.HTTPClient
	} else {
		tr := http.DefaultTransport.(*http.Transport).Clone()
		tr.Proxy = nil
		tr.MaxConnsPerHost = 4
		tr.ResponseHeaderTimeout = 10 * time.Second
		h.Transport = tr
	}
	if h.Timeout <= 0 || h.Timeout > 30*time.Second {
		h.Timeout = 30 * time.Second
	}
	h.CheckRedirect = func(*http.Request, []*http.Request) error { return errors.New("redirect refused") }
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawPath = ""
	return &Client{*u, c.WalletID, c.Network, c.InvoiceKey, c.AdminKey, h}, nil
}
func validKey(s string) bool { return s != "" && len(s) <= 4096 && !strings.ContainsAny(s, "\r\n\x00") }
func validID(s string) bool {
	return len(s) > 0 && len(s) <= 256 && !strings.ContainsAny(s, " \t\r\n\x00")
}
func validHash(s string) bool {
	b, e := hex.DecodeString(s)
	return e == nil && len(b) == 32 && s == strings.ToLower(s)
}
func (c *Client) request(ctx context.Context, method, path, key string, input, out any) error {
	var body io.Reader
	if input != nil {
		b, e := json.Marshal(input)
		if e != nil {
			return refusal("invalid-request")
		}
		body = bytes.NewReader(b)
	}
	u := c.base
	u.Path += path
	req, e := http.NewRequestWithContext(ctx, method, u.String(), body)
	if e != nil {
		return refusal("invalid-request")
	}
	req.Header.Set("X-Api-Key", key)
	req.Header.Set("Accept", "application/json")
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	monetary := method == http.MethodPost
	resp, e := c.http.Do(req)
	if e != nil {
		return &Error{Code: "transport", Unknown: monetary}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Only explicit authorization/schema refusals prove the request was refused.
		unknown := monetary && resp.StatusCode != 401 && resp.StatusCode != 403 && resp.StatusCode != 422
		return &Error{Code: "http-status", Status: resp.StatusCode, Unknown: unknown}
	}
	b, e := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if e != nil || len(b) > maxResponse {
		return &Error{Code: "invalid-response", Unknown: monetary}
	}
	decoder := json.NewDecoder(bytes.NewReader(b))
	if e = decoder.Decode(out); e != nil {
		return &Error{Code: "invalid-response", Unknown: monetary}
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return &Error{Code: "invalid-response", Unknown: monetary}
	}
	return nil
}
func (c *Client) wallet(ctx context.Context, key string) (Wallet, error) {
	var w Wallet
	if e := c.request(ctx, "GET", "/api/v1/wallet", key, nil, &w); e != nil {
		return w, e
	}
	if w.ID != c.walletID || w.BalanceMSat < 0 {
		return Wallet{}, refusal("wallet-mismatch")
	}
	return w, nil
}
func (c *Client) Wallet(ctx context.Context) (Wallet, error) { return c.wallet(ctx, c.invoiceKey) }
func (c *Client) decodeInvoice(bolt string, checkExpiry bool) (decodepay.Bolt11, error) {
	if len(bolt) > 16384 {
		return decodepay.Bolt11{}, refusal("invalid-invoice")
	}
	inv, e := decodepay.Decodepay(bolt)
	if e != nil || inv.Currency != c.network || inv.MSatoshi <= 0 || !validHash(inv.PaymentHash) {
		return decodepay.Bolt11{}, refusal("invalid-invoice")
	}
	if checkExpiry && (inv.Expiry <= 0 || int64(inv.CreatedAt)+int64(inv.Expiry) <= time.Now().Unix()) {
		return decodepay.Bolt11{}, refusal("invoice-expired")
	}
	return inv, nil
}
func (c *Client) validate(p *Payment, hash string, d Direction) error {
	if p.WalletID != c.walletID || !validHash(p.PaymentHash) || p.PaymentHash != hash || p.AmountMSat == 0 || p.AmountMSat == math.MinInt64 || ((d == Incoming) != (p.AmountMSat > 0)) {
		return refusal("payment-binding")
	}
	if p.BOLT11 == "" {
		p.BOLT11 = p.PaymentRequest
	}
	if p.PaymentRequest != "" && p.PaymentRequest != p.BOLT11 {
		return refusal("invoice-mismatch")
	}
	inv, e := c.decodeInvoice(p.BOLT11, false)
	if e != nil || inv.PaymentHash != hash {
		return refusal("invoice-mismatch")
	}
	amount := p.AmountMSat
	if amount < 0 {
		amount = -amount
	}
	if inv.MSatoshi != amount {
		return refusal("amount-mismatch")
	}
	switch p.Status {
	case "pending", "success", "failed":
	default:
		return refusal("invalid-state")
	}
	if p.Preimage != "" {
		pre, e := hex.DecodeString(p.Preimage)
		if e != nil || len(pre) != 32 {
			return refusal("invalid-proof")
		}
		h := sha256.Sum256(pre)
		if hex.EncodeToString(h[:]) != hash {
			return refusal("invalid-proof")
		}
	}
	if d == Outgoing && p.Settled() && p.Preimage == "" {
		return refusal("missing-payment-proof")
	}
	return nil
}
func (c *Client) CreateInvoice(ctx context.Context, r InvoiceRequest) (Payment, error) {
	if r.AmountSat == 0 || r.AmountSat > math.MaxInt64/1000 || r.Expiry == 0 || r.Expiry > 86400 || len(r.Memo) > 1024 || !validID(r.ExternalID) {
		return Payment{}, refusal("invalid-invoice-request")
	}
	if _, e := c.Wallet(ctx); e != nil {
		return Payment{}, e
	}
	input := struct {
		Out        bool   `json:"out"`
		Amount     uint64 `json:"amount"`
		Unit       string `json:"unit"`
		Memo       string `json:"memo"`
		Expiry     uint64 `json:"expiry"`
		ExternalID string `json:"external_id"`
	}{false, r.AmountSat, "sat", r.Memo, r.Expiry, r.ExternalID}
	var p Payment
	if e := c.request(ctx, "POST", "/api/v1/payments", c.invoiceKey, input, &p); e != nil {
		return p, e
	}
	if e := c.validate(&p, p.PaymentHash, Incoming); e != nil || p.AmountMSat != int64(r.AmountSat*1000) {
		return Payment{}, &Error{Code: "invoice-binding", Unknown: true}
	}
	if _, e := c.decodeInvoice(p.BOLT11, true); e != nil {
		return Payment{}, &Error{Code: "invoice-expired", Unknown: true}
	}
	return p, nil
}
func (c *Client) Lookup(ctx context.Context, hash string, d Direction) (Payment, error) {
	if !validHash(hash) || (d != Incoming && d != Outgoing) {
		return Payment{}, refusal("invalid-lookup")
	}
	var r struct {
		Paid    *bool    `json:"paid"`
		Details *Payment `json:"details"`
	}
	if e := c.request(ctx, "GET", "/api/v1/payments/"+hash, c.invoiceKey, nil, &r); e != nil {
		return Payment{}, e
	}
	if r.Paid == nil || r.Details == nil {
		return Payment{}, refusal("missing-wallet-bound-status")
	}
	p := r.Details
	if e := c.validate(p, hash, d); e != nil {
		return Payment{}, e
	}
	if *r.Paid != p.Settled() {
		return Payment{}, refusal("inconsistent-status")
	}
	return *p, nil
}
func (c *Client) PayInvoice(ctx context.Context, bolt, externalID string) (Payment, error) {
	if c.adminKey == "" {
		return Payment{}, refusal("spending-not-enabled")
	}
	inv, e := c.decodeInvoice(bolt, true)
	if e != nil {
		return Payment{}, e
	}
	if !validID(externalID) {
		return Payment{}, refusal("invalid-intent-id")
	}
	if _, e = c.wallet(ctx, c.adminKey); e != nil {
		return Payment{}, e
	}
	input := struct {
		Out        bool   `json:"out"`
		BOLT11     string `json:"bolt11"`
		ExternalID string `json:"external_id"`
	}{true, bolt, externalID}
	var p Payment
	if e = c.request(ctx, "POST", "/api/v1/payments", c.adminKey, input, &p); e != nil {
		return p, e
	}
	if e = c.validate(&p, inv.PaymentHash, Outgoing); e != nil {
		return Payment{}, &Error{Code: "payment-binding", Unknown: true}
	}
	return p, nil
}

func (*Client) String() string   { return "LNbits client (credentials redacted)" }
func (*Client) GoString() string { return "LNbits client (credentials redacted)" }
