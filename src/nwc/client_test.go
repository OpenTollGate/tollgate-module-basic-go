package nwc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nbd-wtf/go-nostr"
	"github.com/nbd-wtf/go-nostr/nip04"
	"github.com/nbd-wtf/go-nostr/nip44"
)

type walletTransport struct {
	walletSK, clientPK string
	mode               string
	calls              atomic.Int32
	last               nostr.Event
}

func fixture(t *testing.T, legacy ...bool) (*Client, *walletTransport) {
	t.Helper()
	sk := nostr.GeneratePrivateKey()
	wsk := nostr.GeneratePrivateKey()
	wpk, _ := nostr.GetPublicKey(wsk)
	cpk, _ := nostr.GetPublicKey(sk)
	tr := &walletTransport{walletSK: wsk, clientPK: cpk}
	q := url.Values{"secret": {sk}, "relay": {"wss://relay.invalid"}}
	c, err := New(Config{ConnectionURI: "nostr+walletconnect://" + wpk + "?" + q.Encode(), Transport: tr, Timeout: time.Second, AllowLegacyNIP04: len(legacy) > 0 && legacy[0]})
	if err != nil {
		t.Fatal(err)
	}
	return c, tr
}
func (w *walletTransport) Info(ctx context.Context, relays []string, pub string) (nostr.Event, error) {
	e := nostr.Event{Kind: 13194, CreatedAt: nostr.Now(), Content: "get_info get_balance make_invoice lookup_invoice pay_invoice", Tags: nostr.Tags{{"encryption", "nip44_v2"}}}
	if w.mode == "legacy" {
		e.Tags = nil
	}
	e.Sign(w.walletSK)
	return e, nil
}
func (w *walletTransport) Exchange(ctx context.Context, relays []string, req nostr.Event) (nostr.Event, error) {
	w.calls.Add(1)
	w.last = req
	if w.mode == "lost" {
		return nostr.Event{}, errors.New("transport failed with private debug content")
	}
	key, _ := nip44.GenerateConversationKey(req.PubKey, w.walletSK)
	plain, err := nip44.Decrypt(req.Content, key)
	if enc, _ := singleTag(req, "encryption"); enc == "nip04" {
		legacyKey, _ := nip04.ComputeSharedSecret(req.PubKey, w.walletSK)
		plain, err = nip04.Decrypt(req.Content, legacyKey)
	}
	if err != nil {
		return nostr.Event{}, err
	}
	var body struct {
		Method string `json:"method"`
	}
	json.Unmarshal([]byte(plain), &body)
	result := any(map[string]any{"balance": 12345})
	if body.Method == "get_info" {
		result = map[string]any{"network": "mainnet", "methods": []string{"get_info", "get_balance", "make_invoice", "lookup_invoice", "pay_invoice"}}
	}
	payload := map[string]any{"result_type": body.Method, "result": result}
	if w.mode == "wrong-method" {
		payload["result_type"] = "pay_invoice"
	}
	if w.mode == "both-result-error" {
		payload["error"] = map[string]any{"code": "UNAUTHORIZED", "message": "private remote detail"}
	}
	encrypted, _ := nip44.Encrypt(string(mustJSON(payload)), key)
	if enc, _ := singleTag(req, "encryption"); enc == "nip04" {
		legacyKey, _ := nip04.ComputeSharedSecret(req.PubKey, w.walletSK)
		encrypted, _ = nip04.Encrypt(string(mustJSON(payload)), legacyKey)
	}
	e := nostr.Event{Kind: 23195, CreatedAt: nostr.Now(), Content: encrypted, Tags: nostr.Tags{{"p", req.PubKey}, {"e", req.ID}, {"encryption", "nip44_v2"}}}
	if w.mode == "legacy" {
		e.Tags = e.Tags[:2]
	}
	switch w.mode {
	case "wrong-request":
		e.Tags[1][1] = strings.Repeat("a", 64)
	case "wrong-recipient":
		e.Tags[0][1] = strings.Repeat("b", 64)
	case "wrong-kind":
		e.Kind = 1
	case "wrong-encryption":
		e.Tags[2][1] = "nip04"
	case "future":
		e.CreatedAt = nostr.Timestamp(time.Now().Add(time.Hour).Unix())
	case "duplicate-tag":
		e.Tags = append(e.Tags, nostr.Tag{"e", req.ID})
	case "tampered":
		e.Content += "x"
	}
	e.Sign(w.walletSK)
	if w.mode == "wrong-author" {
		e.Sign(nostr.GeneratePrivateKey())
	}
	if w.mode == "bad-signature" {
		e.Sig = strings.Repeat("0", 128)
	}
	return e, nil
}
func mustJSON(v any) []byte {
	b, e := json.Marshal(v)
	if e != nil {
		panic(e)
	}
	return b
}
func TestConnectAndSignedEncryptedRequest(t *testing.T) {
	c, tr := fixture(t)
	info, err := c.Connect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.Network != "mainnet" || !info.Supports("pay_invoice") {
		t.Fatal("wrong capabilities")
	}
	p, err := c.Prepare("get_balance", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(p.Request.Content, "get_balance") {
		t.Fatal("plaintext request")
	}
	r, err := c.Execute(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	var b struct {
		Balance int64 `json:"balance"`
	}
	json.Unmarshal(r, &b)
	if b.Balance != 12345 {
		t.Fatal("wrong decrypted result")
	}
	if tr.last.Kind != 23194 {
		t.Fatal("wrong request kind")
	}
	if ok, _ := tr.last.CheckSignature(); !ok {
		t.Fatal("request not signed")
	}
}
func TestSpoofedOrMalformedResponseNeverAccepted(t *testing.T) {
	for _, mode := range []string{"wrong-method", "both-result-error", "wrong-request", "wrong-recipient", "wrong-kind", "wrong-encryption", "future", "duplicate-tag", "tampered", "wrong-author", "bad-signature"} {
		t.Run(mode, func(t *testing.T) {
			c, tr := fixture(t)
			if _, err := c.Connect(context.Background()); err != nil {
				t.Fatal(err)
			}
			p, err := c.Prepare("get_balance", map[string]any{})
			if err != nil {
				t.Fatal(err)
			}
			tr.mode = mode
			if _, err := c.Execute(context.Background(), p); err == nil {
				t.Fatal("invalid response accepted")
			}
		})
	}
}
func TestPreparedEventIsSerializableButOneShot(t *testing.T) {
	c, tr := fixture(t)
	if _, err := c.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	p, err := c.Prepare("make_invoice", map[string]any{"amount": int64(1000), "expiry": 60})
	if err != nil {
		t.Fatal(err)
	}
	serialized := mustJSON(p)
	var restored Prepared
	if err := json.Unmarshal(serialized, &restored); err != nil {
		t.Fatal(err)
	}
	tr.mode = "lost"
	before := tr.calls.Load()
	if _, err := c.Execute(context.Background(), restored); err == nil || !IsUnknown(err) {
		t.Fatal("mutating timeout not unknown")
	}
	if _, err := c.Execute(context.Background(), restored); err == nil {
		t.Fatal("duplicate prepared event accepted")
	}
	if tr.calls.Load() != before+1 {
		t.Fatal("uncertain request replayed")
	}
}
func TestCapabilitiesAndParametersRefuseBeforePublish(t *testing.T) {
	c, tr := fixture(t)
	if _, err := c.Prepare("get_balance", nil); err == nil {
		t.Fatal("unconnected wallet accepted")
	}
	if _, err := c.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	before := tr.calls.Load()
	for _, x := range []struct {
		method string
		p      any
	}{{"arbitrary_admin", nil}, {"make_invoice", map[string]any{"amount": -1}}, {"lookup_invoice", map[string]any{"payment_hash": "bad"}}, {"pay_invoice", map[string]any{"invoice": "invalid"}}} {
		if _, err := c.Prepare(x.method, x.p); err == nil {
			t.Fatalf("unsafe method/params accepted %s", x.method)
		}
	}
	if tr.calls.Load() != before {
		t.Fatal("invalid request reached relay")
	}
}
func TestConnectionSecretNeverInErrorsOrFormatting(t *testing.T) {
	c, _ := fixture(t)
	_ = c
	for _, s := range []string{"nostr+walletconnect://bad?secret=private-marker&relay=wss://relay.invalid", "https://example.com/?secret=private-marker"} {
		_, err := New(Config{ConnectionURI: s})
		if err == nil || strings.Contains(err.Error(), "private-marker") {
			t.Fatal("connection rejected unsafely")
		}
	}
}
func TestRejectZeroAndOverflowConnectionSecrets(t *testing.T) {
	_, tr := fixture(t)
	wallet, _ := nostr.GetPublicKey(tr.walletSK)
	for _, secret := range []string{strings.Repeat("0", 64), strings.Repeat("f", 64)} {
		q := url.Values{"secret": {secret}, "relay": {"wss://relay.invalid"}}
		if _, err := New(Config{ConnectionURI: "nostr+walletconnect://" + wallet + "?" + q.Encode(), Transport: tr}); err == nil {
			t.Fatal("invalid scalar secret accepted")
		}
	}
}
func TestQueuedRequestCannotPublishAfterExpiration(t *testing.T) {
	c, tr := fixture(t)
	c.timeout = 3 * time.Second
	if _, err := c.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	p, err := c.Prepare("make_invoice", map[string]any{"amount": 1000, "expiry": 60})
	if err != nil {
		t.Fatal(err)
	}
	for i, tag := range p.Request.Tags {
		if tag[0] == "expiration" {
			p.Request.Tags[i][1] = strconv.FormatInt(time.Now().Unix()+1, 10)
		}
	}
	p.Request.Sign(*c.secret)
	for range cap(c.active) {
		c.active <- struct{}{}
	}
	before := tr.calls.Load()
	done := make(chan error, 1)
	go func() { _, err := c.Execute(context.Background(), p); done <- err }()
	time.Sleep(1500 * time.Millisecond)
	<-c.active
	if err := <-done; err == nil {
		t.Fatal("expired queued operation succeeded")
	}
	if tr.calls.Load() != before {
		t.Fatal("expired request reached relay")
	}
}
func TestClientFormattingCannotExposeConnectionSecret(t *testing.T) {
	c, _ := fixture(t)
	// The spending secret is held as *string precisely so that a caller who
	// formats the struct VALUE (fmt.Sprintf("%+v", *c), or a wrapper embedding
	// Client by value) prints a pointer and not the secret. A pointer-receiver
	// String/GoString alone does NOT cover that case.
	//
	// The value form cannot be asserted in this test: copying a Client trips
	// vet's copylocks check (it contains sync.Mutex), and that rejection is
	// correct. It was verified manually against the struct value before this
	// guard was added, and the *string field is what makes it hold.
	for _, format := range []string{"%v", "%+v", "%#v"} {
		s := fmt.Sprintf(format, c)
		if strings.Contains(s, *c.secret) {
			t.Fatalf("client formatting %s exposes connection secret", format)
		}
		if strings.Contains(s, "secret=") {
			t.Fatalf("client formatting %s exposes a connection URI", format)
		}
	}
}

func TestLegacyEncryptionRequiresExplicitChoice(t *testing.T) {
	c, tr := fixture(t)
	tr.mode = "legacy"
	if _, err := c.Connect(context.Background()); err == nil {
		t.Fatal("legacy encryption enabled by default")
	}
	c, tr = fixture(t, true)
	tr.mode = "legacy"
	if _, err := c.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	p, err := c.Prepare("get_balance", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if enc, _ := singleTag(p.Request, "encryption"); enc != "nip04" {
		t.Fatal("explicit legacy mode did not negotiate nip04")
	}
	if _, err = c.Execute(context.Background(), p); err != nil {
		t.Fatal(err)
	}
}
