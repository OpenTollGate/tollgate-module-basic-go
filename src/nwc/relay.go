package nwc

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/coder/websocket"
	"github.com/nbd-wtf/go-nostr"
)

// RelayTransport uses bounded Nostr frames over WebSockets. It does not use the
// SDK's relay lifecycle helper; crypto/event handling still uses go-nostr.
// Only pre-publish dial failures can try another relay. There is no send retry.
// NIP-42 authenticated relays require a separate transport implementation.
type RelayTransport struct{ HTTPClient *http.Client }

func (t RelayTransport) dial(ctx context.Context, u string) (*websocket.Conn, error) {
	h := t.HTTPClient
	if h == nil {
		tr := http.DefaultTransport.(*http.Transport).Clone()
		tr.Proxy = nil
		h = &http.Client{Transport: tr}
	}
	copyClient := *h
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return errors.New("relay redirect refused") }
	c, _, e := websocket.Dial(ctx, u, &websocket.DialOptions{HTTPClient: &copyClient})
	if e != nil {
		return nil, errors.New("relay unavailable")
	}
	c.SetReadLimit(128 * 1024)
	return c, nil
}
func writeFrame(ctx context.Context, c *websocket.Conn, v any) error {
	b, e := json.Marshal(v)
	if e != nil {
		return errors.New("invalid relay frame")
	}
	return c.Write(ctx, websocket.MessageText, b)
}
func readFrame(ctx context.Context, c *websocket.Conn) (string, []json.RawMessage, error) {
	typ, b, e := c.Read(ctx)
	if e != nil {
		return "", nil, errors.New("relay read failed")
	}
	if typ != websocket.MessageText {
		return "", nil, errors.New("nontext relay frame")
	}
	var v []json.RawMessage
	if json.Unmarshal(b, &v) != nil || len(v) < 2 {
		return "", nil, errors.New("invalid relay frame")
	}
	var kind string
	if json.Unmarshal(v[0], &kind) != nil {
		return "", nil, errors.New("invalid relay frame")
	}
	return kind, v, nil
}
func (t RelayTransport) Info(ctx context.Context, urls []string, wallet string) (nostr.Event, error) {
	for _, u := range urls {
		c, e := t.dial(ctx, u)
		if e != nil {
			continue
		}
		event, e := func() (nostr.Event, error) {
			defer c.CloseNow()
			if e := writeFrame(ctx, c, []any{"REQ", "nwc-info", nostr.Filter{Kinds: []int{13194}, Authors: []string{wallet}, Limit: 1}}); e != nil {
				return nostr.Event{}, e
			}
			for range 64 {
				kind, v, e := readFrame(ctx, c)
				if e != nil {
					return nostr.Event{}, e
				}
				switch kind {
				case "EVENT":
					var sub string
					var event nostr.Event
					if len(v) != 3 || json.Unmarshal(v[1], &sub) != nil || sub != "nwc-info" || json.Unmarshal(v[2], &event) != nil {
						return nostr.Event{}, errors.New("invalid info event")
					}
					return event, nil
				case "EOSE", "CLOSED", "AUTH":
					return nostr.Event{}, errors.New("wallet info unavailable")
				}
			}
			return nostr.Event{}, errors.New("relay frame limit")
		}()
		if e == nil {
			return event, nil
		}
	}
	return nostr.Event{}, errors.New("wallet info unavailable")
}
func (t RelayTransport) Exchange(ctx context.Context, urls []string, request nostr.Event) (nostr.Event, error) {
	wallet, ok := singleTag(request, "p")
	if !ok {
		return nostr.Event{}, errors.New("invalid request")
	}
	for _, u := range urls {
		c, e := t.dial(ctx, u)
		if e != nil {
			continue
		}
		defer c.CloseNow()
		filter := nostr.Filter{Kinds: []int{23195}, Authors: []string{wallet}, Tags: nostr.TagMap{"e": []string{request.ID}, "p": []string{request.PubKey}}, Limit: 1}
		if e = writeFrame(ctx, c, []any{"REQ", "nwc-response", filter}); e != nil {
			return nostr.Event{}, errors.New("subscription failed")
		}
		if e = writeFrame(ctx, c, []any{"EVENT", request}); e != nil {
			return nostr.Event{}, errors.New("publish outcome unknown")
		}
		for range 64 {
			kind, v, e := readFrame(ctx, c)
			if e != nil {
				return nostr.Event{}, e
			}
			switch kind {
			case "EVENT":
				var sub string
				var response nostr.Event
				if len(v) != 3 || json.Unmarshal(v[1], &sub) != nil || sub != "nwc-response" || json.Unmarshal(v[2], &response) != nil {
					return nostr.Event{}, errors.New("invalid response event")
				}
				return response, nil
			case "OK":
				var id string
				var accepted bool
				if len(v) < 3 || json.Unmarshal(v[1], &id) != nil || json.Unmarshal(v[2], &accepted) != nil {
					return nostr.Event{}, errors.New("invalid relay acknowledgment")
				}
				if id == request.ID && !accepted {
					return nostr.Event{}, errors.New("publish outcome unknown")
				}
			case "CLOSED", "AUTH":
				return nostr.Event{}, errors.New("relay authorization unavailable")
			}
		}
		return nostr.Event{}, errors.New("relay frame limit")
	}
	return nostr.Event{}, errors.New("no reachable relay")
}
