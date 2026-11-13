package nwc

import (
	"context"
	"encoding/json"
	"github.com/coder/websocket"
	"github.com/nbd-wtf/go-nostr"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRealWebSocketRelayResponseBeforePublishAck(t *testing.T) {
	sk := nostr.GeneratePrivateKey()
	wsk := nostr.GeneratePrivateKey()
	wpk, _ := nostr.GetPublicKey(wsk)
	cpk, _ := nostr.GetPublicKey(sk)
	wallet := &walletTransport{walletSK: wsk, clientPK: cpk}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		subscription := ""
		send := func(v any) error { return conn.Write(ctx, websocket.MessageText, mustJSON(v)) }
		for {
			_, data, err := conn.Read(ctx)
			if err != nil {
				return
			}
			var frame []json.RawMessage
			if json.Unmarshal(data, &frame) != nil || len(frame) < 2 {
				return
			}
			var kind string
			json.Unmarshal(frame[0], &kind)
			switch kind {
			case "REQ":
				json.Unmarshal(frame[1], &subscription)
				var filter nostr.Filter
				if len(frame) != 3 || json.Unmarshal(frame[2], &filter) != nil {
					return
				}
				if len(filter.Kinds) == 1 && filter.Kinds[0] == 13194 {
					info, _ := wallet.Info(ctx, nil, wpk)
					if send([]any{"EVENT", subscription, info}) != nil {
						return
					}
					if send([]any{"EOSE", subscription}) != nil {
						return
					}
				}
			case "EVENT":
				var request nostr.Event
				if json.Unmarshal(frame[1], &request) != nil {
					return
				}
				response, err := wallet.Exchange(ctx, nil, request)
				if err != nil {
					return
				}
				if send([]any{"EVENT", subscription, response}) != nil {
					return
				}
				if send([]any{"OK", request.ID, true, ""}) != nil {
					return
				}
			case "CLOSE":
				return
			}
		}
	}))
	defer srv.Close()
	relay := "ws" + strings.TrimPrefix(srv.URL, "http")
	q := url.Values{"secret": {sk}, "relay": {relay}}
	c, err := New(Config{ConnectionURI: "nostr+walletconnect://" + wpk + "?" + q.Encode(), AllowInsecureLocal: true, Timeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	p, err := c.Prepare("get_balance", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.Execute(context.Background(), p); err != nil {
		t.Fatal(err)
	}
}
func TestRelayRedirectNeverFollowed(t *testing.T) {
	var hit atomic.Bool
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hit.Store(true); w.WriteHeader(400) }))
	defer destination.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, destination.URL, 302) }))
	defer redirect.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, _ = (RelayTransport{}).Info(ctx, []string{"ws" + strings.TrimPrefix(redirect.URL, "http")}, strings.Repeat("a", 64))
	if hit.Load() {
		t.Fatal("relay handshake followed redirect")
	}
}
