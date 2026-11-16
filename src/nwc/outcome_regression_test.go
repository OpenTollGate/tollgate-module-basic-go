package nwc

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/ecdsa"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/coder/websocket"
	"github.com/lightningnetwork/lnd/lnwire"
	"github.com/lightningnetwork/lnd/zpay32"
	"github.com/nbd-wtf/go-nostr"
	"github.com/nbd-wtf/go-nostr/nip44"
)

func prepareOutcomeRequest(t *testing.T, c *Client, method string) Prepared {
	t.Helper()
	params := map[string]any{}
	switch method {
	case "make_invoice":
		params = map[string]any{"amount": 1000, "expiry": 60}
	case "pay_invoice":
		hash := sha256.Sum256([]byte("synthetic NWC outcome test"))
		key, err := btcec.NewPrivateKey()
		if err != nil {
			t.Fatal(err)
		}
		invoice, err := zpay32.NewInvoice(&chaincfg.MainNetParams, hash, time.Now(),
			zpay32.Amount(lnwire.MilliSatoshi(1000)), zpay32.Description("synthetic NWC outcome test"))
		if err != nil {
			t.Fatal(err)
		}
		bolt, err := invoice.Encode(zpay32.MessageSigner{SignCompact: func(msg []byte) ([]byte, error) {
			return ecdsa.SignCompact(key, chainhash.HashB(msg), true), nil
		}})
		if err != nil {
			t.Fatal(err)
		}
		params = map[string]any{"invoice": bolt}
	}
	p, err := c.Prepare(method, params)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

type remoteErrorTransport struct {
	*walletTransport
	code  string
	calls atomic.Int32
}

func (w *remoteErrorTransport) Exchange(_ context.Context, _ []string, req nostr.Event) (nostr.Event, error) {
	w.calls.Add(1)
	key, err := nip44.GenerateConversationKey(req.PubKey, w.walletSK)
	if err != nil {
		return nostr.Event{}, err
	}
	plain, err := nip44.Decrypt(req.Content, key)
	if err != nil {
		return nostr.Event{}, err
	}
	var request struct {
		Method string `json:"method"`
	}
	if err := json.Unmarshal([]byte(plain), &request); err != nil {
		return nostr.Event{}, err
	}
	body := map[string]any{"result_type": request.Method, "error": map[string]any{
		"code": w.code, "message": "synthetic private wallet detail",
	}}
	content, err := nip44.Encrypt(string(mustJSON(body)), key)
	if err != nil {
		return nostr.Event{}, err
	}
	response := nostr.Event{Kind: 23195, CreatedAt: nostr.Now(), Content: content,
		Tags: nostr.Tags{{"p", req.PubKey}, {"e", req.ID}, {"encryption", "nip44_v2"}}}
	if err := response.Sign(w.walletSK); err != nil {
		return nostr.Event{}, err
	}
	return response, nil
}

func TestPureRemoteErrorClassificationAndNoReplay(t *testing.T) {
	for _, method := range []string{"pay_invoice", "make_invoice", "get_balance"} {
		for _, code := range []string{"INTERNAL_ERROR", "FUTURE_ERROR", "UNAUTHORIZED", "NOT_IMPLEMENTED", "RESTRICTED", "QUOTA_EXCEEDED"} {
			t.Run(method+"/"+code, func(t *testing.T) {
				c, wallet := fixture(t)
				if _, err := c.Connect(context.Background()); err != nil {
					t.Fatal(err)
				}
				p := prepareOutcomeRequest(t, c, method)
				remote := &remoteErrorTransport{walletTransport: wallet, code: code}
				c.transport = remote
				_, err := c.Execute(context.Background(), p)
				permission := code == "UNAUTHORIZED" || code == "NOT_IMPLEMENTED" || code == "RESTRICTED" || code == "QUOTA_EXCEEDED"
				wantUnknown := method != "get_balance" && !permission
				if err == nil || IsUnknown(err) != wantUnknown {
					t.Fatalf("classification: err=%v wantUnknown=%v", err, wantUnknown)
				}
				if strings.Contains(err.Error(), "synthetic private wallet detail") {
					t.Fatal("raw remote detail escaped")
				}
				if _, err := c.Execute(context.Background(), p); err == nil {
					t.Fatal("used request accepted")
				}
				if remote.calls.Load() != 1 {
					t.Fatal("remote error caused request replay")
				}
			})
		}
	}
}

func TestRelayNAKAndUnreachablePreserveUnknownWithoutReplay(t *testing.T) {
	for _, method := range []string{"pay_invoice", "make_invoice", "get_balance"} {
		for _, mode := range []string{"nak", "unreachable"} {
			t.Run(method+"/"+mode, func(t *testing.T) {
				c, wallet := fixture(t)
				if _, err := c.Connect(context.Background()); err != nil {
					t.Fatal(err)
				}
				p := prepareOutcomeRequest(t, c, method)
				var firstDials, secondDials, publications atomic.Int32
				first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					firstDials.Add(1)
					if mode == "unreachable" {
						w.WriteHeader(http.StatusServiceUnavailable)
						return
					}
					conn, err := websocket.Accept(w, r, nil)
					if err != nil {
						return
					}
					defer conn.CloseNow()
					ctx, cancel := context.WithTimeout(r.Context(), time.Second)
					defer cancel()
					kind, _, err := readFrame(ctx, conn)
					if err != nil || kind != "REQ" {
						return
					}
					kind, frame, err := readFrame(ctx, conn)
					if err != nil || kind != "EVENT" || len(frame) != 2 {
						return
					}
					var request nostr.Event
					if json.Unmarshal(frame[1], &request) != nil {
						return
					}
					publications.Add(1)
					if writeFrame(ctx, conn, []any{"OK", request.ID, false, "synthetic rejection"}) != nil {
						return
					}
					// A valid response queued after NAK must not turn a rejected
					// publication into success if NAK handling is removed.
					response, err := wallet.Exchange(ctx, nil, request)
					if err == nil {
						_ = writeFrame(ctx, conn, []any{"EVENT", "nwc-response", response})
					}
				}))
				defer first.Close()
				second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					secondDials.Add(1)
					w.WriteHeader(http.StatusServiceUnavailable)
				}))
				defer second.Close()
				c.relays = []string{"ws" + strings.TrimPrefix(first.URL, "http"), "ws" + strings.TrimPrefix(second.URL, "http")}
				c.transport = RelayTransport{}
				_, err := c.Execute(context.Background(), p)
				if err == nil || IsUnknown(err) != (method != "get_balance") {
					t.Fatalf("relay failure classification: %v", err)
				}
				if _, err := c.Execute(context.Background(), p); err == nil {
					t.Fatal("used request accepted")
				}
				wantSecond, wantPublications := int32(0), int32(1)
				if mode == "unreachable" {
					wantSecond, wantPublications = 1, 0
				}
				if firstDials.Load() != 1 || secondDials.Load() != wantSecond || publications.Load() != wantPublications {
					t.Fatalf("dials/publications=%d/%d/%d want=1/%d/%d", firstDials.Load(), secondDials.Load(), publications.Load(), wantSecond, wantPublications)
				}
			})
		}
	}
}
