// Package nwc implements NIP-47 with journalable, one-shot signed requests.
// Callers must durably record a Prepared request before Execute and reconcile
// ambiguous monetary outcomes with lookup_invoice, including across restarts.
package nwc

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/nbd-wtf/go-nostr"
	"github.com/nbd-wtf/go-nostr/nip04"
	"github.com/nbd-wtf/go-nostr/nip44"
	decodepay "github.com/nbd-wtf/ln-decodepay"
)

type Transport interface {
	Info(context.Context, []string, string) (nostr.Event, error)
	Exchange(context.Context, []string, nostr.Event) (nostr.Event, error)
}
type Config struct {
	ConnectionURI      string `json:"-"`
	Timeout            time.Duration
	AllowLegacyNIP04   bool
	AllowInsecureLocal bool
	Transport          Transport `json:"-"`
}

func (Config) String() string   { return "NWC configuration (connection redacted)" }
func (Config) GoString() string { return "NWC configuration (connection redacted)" }

type Client struct {
	mu                     sync.Mutex
	wallet, public, secret string
	relays                 []string
	timeout                time.Duration
	legacy                 bool
	transport              Transport
	info                   Info
	encryption             string
	used                   map[string]int64
	active                 chan struct{}
}
type Info struct {
	Network string   `json:"network"`
	Methods []string `json:"methods"`
}

func (i Info) Supports(m string) bool {
	for _, s := range i.Methods {
		if s == m {
			return true
		}
	}
	return false
}

type Prepared struct {
	Request nostr.Event `json:"request"`
	Method  string      `json:"method"`
}
type Error struct {
	Code    string
	Unknown bool
}

func (e *Error) Error() string {
	if e.Unknown {
		return "NWC outcome unknown: " + e.Code
	}
	return "NWC refused: " + e.Code
}
func IsUnknown(err error) bool { var e *Error; return errors.As(err, &e) && e.Unknown }
func refused(s string) error   { return &Error{Code: s} }
func hash(s string) bool {
	b, e := hex.DecodeString(s)
	return e == nil && len(b) == 32 && s == strings.ToLower(s)
}
func signed(e nostr.Event) bool {
	if e.ID != e.GetID() {
		return false
	}
	ok, err := e.CheckSignature()
	return err == nil && ok
}
func singleTag(e nostr.Event, name string) (string, bool) {
	var value string
	count := 0
	for _, tag := range e.Tags {
		if len(tag) > 0 && tag[0] == name {
			if len(tag) != 2 {
				return "", false
			}
			count++
			value = tag[1]
		}
	}
	return value, count == 1
}
func New(cfg Config) (*Client, error) {
	u, e := url.Parse(cfg.ConnectionURI)
	if e != nil || u.Scheme != "nostr+walletconnect" || u.User != nil || u.Fragment != "" || u.Path != "" || !hash(u.Host) {
		return nil, refused("invalid-connection")
	}
	q, e := url.ParseQuery(u.RawQuery)
	if e != nil || len(q["secret"]) != 1 || !hash(q.Get("secret")) {
		return nil, refused("invalid-connection")
	}
	secret := q.Get("secret")
	raw, _ := hex.DecodeString(secret)
	var scalar btcec.ModNScalar
	if scalar.SetByteSlice(raw) || scalar.IsZero() {
		return nil, refused("invalid-connection")
	}
	walletRaw, _ := hex.DecodeString(u.Host)
	if _, e = schnorr.ParsePubKey(walletRaw); e != nil {
		return nil, refused("invalid-connection")
	}
	pub, e := nostr.GetPublicKey(secret)
	if e != nil || !hash(pub) {
		return nil, refused("invalid-connection")
	}
	relays := q["relay"]
	if len(relays) == 0 || len(relays) > 3 {
		return nil, refused("invalid-relays")
	}
	seen := map[string]bool{}
	for _, s := range relays {
		r, e := url.Parse(s)
		if e != nil || r.Hostname() == "" || r.User != nil || r.Fragment != "" || r.RawQuery != "" || seen[s] {
			return nil, refused("invalid-relays")
		}
		seen[s] = true
		if r.Scheme != "wss" {
			ip := net.ParseIP(r.Hostname())
			if r.Scheme != "ws" || !cfg.AllowInsecureLocal || ip == nil || !ip.IsLoopback() {
				return nil, refused("secure-relay-required")
			}
		}
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	if timeout > 30*time.Second {
		return nil, refused("timeout-too-large")
	}
	tr := cfg.Transport
	if tr == nil {
		tr = RelayTransport{}
	}
	return &Client{wallet: u.Host, public: pub, secret: secret, relays: append([]string(nil), relays...), timeout: timeout, legacy: cfg.AllowLegacyNIP04, transport: tr, used: make(map[string]int64), active: make(chan struct{}, 4)}, nil
}
func (c *Client) crypt(s, enc string, decrypt bool) (string, error) {
	if enc == "nip44_v2" {
		key, e := nip44.GenerateConversationKey(c.wallet, c.secret)
		if e != nil {
			return "", refused("encryption")
		}
		if decrypt {
			return nip44.Decrypt(s, key)
		}
		return nip44.Encrypt(s, key)
	}
	if enc == "nip04" && c.legacy {
		key, e := nip04.ComputeSharedSecret(c.wallet, c.secret)
		if e != nil {
			return "", refused("encryption")
		}
		if decrypt {
			return nip04.Decrypt(s, key)
		}
		return nip04.Encrypt(s, key)
	}
	return "", refused("unsupported-encryption")
}
func (c *Client) Connect(ctx context.Context) (Info, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	event, e := c.transport.Info(ctx, c.relays, c.wallet)
	if e != nil {
		return Info{}, refused("wallet-info-unavailable")
	}
	if event.Kind != 13194 || event.PubKey != c.wallet || !signed(event) || int64(event.CreatedAt) > time.Now().Add(5*time.Minute).Unix() || len(event.Content) > 4096 {
		return Info{}, refused("invalid-wallet-info")
	}
	enc, ok := singleTag(event, "encryption")
	mode := ""
	if ok {
		for _, m := range strings.Fields(enc) {
			if m == "nip44_v2" {
				mode = m
				break
			}
		}
		if mode == "" && c.legacy {
			for _, m := range strings.Fields(enc) {
				if m == "nip04" {
					mode = m
				}
			}
		}
	} else {
		has := false
		for _, t := range event.Tags {
			if len(t) > 0 && t[0] == "encryption" {
				has = true
			}
		}
		if !has && c.legacy {
			mode = "nip04"
		}
	}
	if mode == "" {
		return Info{}, refused("modern-encryption-required")
	}
	advertised := strings.Fields(event.Content)
	supportsInfo := false
	for _, m := range advertised {
		if m == "get_info" {
			supportsInfo = true
		}
	}
	if !supportsInfo {
		return Info{}, refused("get-info-required")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	request, e := c.prepareLocked("get_info", map[string]any{}, mode)
	if e != nil {
		return Info{}, e
	}
	data, e := c.exchange(ctx, request, mode)
	if e != nil {
		return Info{}, e
	}
	var info Info
	if json.Unmarshal(data, &info) != nil || len(info.Methods) == 0 || len(info.Methods) > 32 {
		return Info{}, refused("invalid-effective-capabilities")
	}
	switch info.Network {
	case "mainnet", "testnet", "regtest", "signet":
	default:
		return Info{}, refused("unsupported-network")
	}
	methods := []string{}
	for _, m := range info.Methods {
		for _, a := range advertised {
			if m == a {
				methods = append(methods, m)
				break
			}
		}
	}
	info.Methods = methods
	c.info = info
	c.encryption = mode
	result := info
	result.Methods = append([]string(nil), info.Methods...)
	return result, nil
}
func (c *Client) validateParams(method string, b []byte) error {
	var p struct {
		Amount  *int64  `json:"amount"`
		Expiry  *uint64 `json:"expiry"`
		Invoice string  `json:"invoice"`
		Hash    string  `json:"payment_hash"`
	}
	if json.Unmarshal(b, &p) != nil {
		return refused("invalid-params")
	}
	switch method {
	case "get_info", "get_balance":
		return nil
	case "make_invoice":
		if p.Amount == nil || *p.Amount <= 0 || p.Expiry == nil || *p.Expiry == 0 || *p.Expiry > 86400 {
			return refused("invalid-invoice-params")
		}
	case "lookup_invoice":
		if !hash(p.Hash) {
			return refused("invalid-lookup")
		}
	case "pay_invoice":
		inv, e := decodepay.Decodepay(p.Invoice)
		network := map[string]string{"mainnet": "bc", "testnet": "tb", "regtest": "bcrt", "signet": "tbs"}[c.info.Network]
		if e != nil || inv.MSatoshi <= 0 || inv.Currency != network || int64(inv.CreatedAt)+int64(inv.Expiry) <= time.Now().Unix() || (p.Amount != nil && *p.Amount != inv.MSatoshi) {
			return refused("invalid-payment-invoice")
		}
	default:
		return refused("unsupported-method")
	}
	return nil
}
func (c *Client) prepareLocked(method string, params any, enc string) (Prepared, error) {
	body, e := json.Marshal(struct {
		Method string `json:"method"`
		Params any    `json:"params"`
	}{method, params})
	if e != nil || len(body) > 60000 {
		return Prepared{}, refused("invalid-params")
	}
	encrypted, e := c.crypt(string(body), enc, false)
	if e != nil {
		return Prepared{}, refused("encryption")
	}
	expiry := time.Now().Add(c.timeout).Unix() + 1
	event := nostr.Event{Kind: 23194, CreatedAt: nostr.Now(), Content: encrypted, Tags: nostr.Tags{{"p", c.wallet}, {"encryption", enc}, {"expiration", strconv.FormatInt(expiry, 10)}}}
	if e = event.Sign(c.secret); e != nil {
		return Prepared{}, refused("signing")
	}
	return Prepared{event, method}, nil
}
func (c *Client) Prepare(method string, params any) (Prepared, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.encryption == "" || !c.info.Supports(method) {
		return Prepared{}, refused("missing-permission")
	}
	b, e := json.Marshal(params)
	if e != nil || len(b) > 60000 {
		return Prepared{}, refused("invalid-params")
	}
	if e = c.validateParams(method, b); e != nil {
		return Prepared{}, e
	}
	return c.prepareLocked(method, params, c.encryption)
}
func (c *Client) exchange(ctx context.Context, p Prepared, enc string) (json.RawMessage, error) {
	monetary := p.Method == "pay_invoice" || p.Method == "make_invoice"
	response, e := c.transport.Exchange(ctx, c.relays, p.Request)
	if e != nil {
		return nil, &Error{Code: "transport", Unknown: monetary}
	}
	invalid := func() (json.RawMessage, error) { return nil, &Error{Code: "invalid-response", Unknown: monetary} }
	peer, pok := singleTag(response, "p")
	id, eok := singleTag(response, "e")
	encryption, encok := singleTag(response, "encryption")
	if !encok && enc == "nip04" {
		has := false
		for _, t := range response.Tags {
			if len(t) > 0 && t[0] == "encryption" {
				has = true
			}
		}
		if !has {
			encryption = "nip04"
			encok = true
		}
	}
	if response.Kind != 23195 || response.PubKey != c.wallet || !signed(response) || !pok || peer != c.public || !eok || id != p.Request.ID || !encok || encryption != enc || int64(response.CreatedAt) > time.Now().Add(5*time.Minute).Unix() || int64(response.CreatedAt) < int64(p.Request.CreatedAt)-300 || len(response.Content) > 90000 {
		return invalid()
	}
	plain, e := c.crypt(response.Content, enc, true)
	if e != nil || len(plain) > 65535 {
		return invalid()
	}
	var body struct {
		Type   string          `json:"result_type"`
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal([]byte(plain), &body) != nil || body.Type != p.Method {
		return invalid()
	}
	hasResult := len(body.Result) > 0 && string(body.Result) != "null"
	if body.Error != nil {
		if hasResult {
			return invalid()
		}
		code := "remote-error"
		unknown := monetary
		switch body.Error.Code {
		case "UNAUTHORIZED", "NOT_IMPLEMENTED", "RESTRICTED", "QUOTA_EXCEEDED":
			code = "permission-or-budget"
			unknown = false
		}
		return nil, &Error{Code: code, Unknown: unknown}
	}
	if !hasResult || body.Result[0] != '{' {
		return invalid()
	}
	return body.Result, nil
}
func (c *Client) Execute(ctx context.Context, p Prepared) (json.RawMessage, error) {
	c.mu.Lock()
	monetary := p.Method == "pay_invoice" || p.Method == "make_invoice"
	bad := func() (json.RawMessage, error) {
		c.mu.Unlock()
		return nil, &Error{Code: "invalid-or-used-request", Unknown: monetary}
	}
	peer, ok := singleTag(p.Request, "p")
	enc, encok := singleTag(p.Request, "encryption")
	expiration, expok := singleTag(p.Request, "expiration")
	expires, e := strconv.ParseInt(expiration, 10, 64)
	now := time.Now().Unix()
	if !ok || peer != c.wallet || !encok || enc != c.encryption || !expok || e != nil || expires <= now || expires > now+31 || p.Request.Kind != 23194 || p.Request.PubKey != c.public || !signed(p.Request) || !c.info.Supports(p.Method) || len(p.Request.Content) > 90000 {
		return bad()
	}
	plain, e := c.crypt(p.Request.Content, enc, true)
	if e != nil {
		return bad()
	}
	var body struct {
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if json.Unmarshal([]byte(plain), &body) != nil || body.Method != p.Method || c.validateParams(p.Method, body.Params) != nil {
		return bad()
	}
	for id, until := range c.used {
		if until <= now {
			delete(c.used, id)
		}
	}
	if _, used := c.used[p.Request.ID]; used {
		return bad()
	}
	if len(c.used) >= 1024 {
		return bad()
	}
	c.used[p.Request.ID] = expires
	c.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	select {
	case c.active <- struct{}{}:
		defer func() { <-c.active }()
	case <-ctx.Done():
		return nil, &Error{Code: "local-timeout", Unknown: monetary}
	}
	if ctx.Err() != nil || time.Now().Unix() >= expires {
		return nil, refused("expired-before-publish")
	}
	return c.exchange(ctx, p, enc)
}
