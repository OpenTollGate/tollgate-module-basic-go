package main

import (
	"context"
	"time"

	"github.com/nbd-wtf/go-nostr"
)

// Serve is the daemon's whole loop: subscribe broadly to gift wraps, filter
// the p-tag client-side, unwrap, dispatch, respond.
//
// The broad subscription is deliberate: `#p` tag filtering does NOT work for
// kind 1059 on some relays (measured, see ADR D2), so the filter carries only
// the kinds and every event's p-tag is checked here before any key material
// is touched. An event not addressed to us is dropped, not decrypted.
func Serve(ctx context.Context, cfg *ServerConfig, bridge Bridge) error {
	sk := cfg.ServerKeyHex
	serverPK, err := nostr.GetPublicKey(sk)
	if err != nil {
		return err
	}

	// Maintainer resolution: config.json via the socket is the source of
	// truth; the env var is an override for first-boot claiming.
	maintainer := cfg.MaintainerNpub
	if maintainer == "" && bridge != nil {
		if resp, err := bridge.Call(CLIMessage{Command: "config", Args: []string{"get"}, Timestamp: time.Now()}); err == nil && resp.Success {
			if cfgMap, ok := resp.Data.(map[string]interface{}); ok {
				if v, ok := cfgMap["maintainer_npub"].(string); ok {
					maintainer = v
				}
			}
		}
	}

	authz := NewAuthorizer(maintainer)
	dispatcher := NewDispatcher(authz, bridge, serverPK)

	pool := nostr.NewSimplePool(ctx)
	events := pool.SubMany(ctx, cfg.Relays, nostr.Filters{
		{Kinds: []int{nostr.KindGiftWrap}, Limit: 0},
	})

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case relayEvent, ok := <-events:
			if !ok {
				// All relays gone: reconnect with a pause rather than a hot loop.
				time.Sleep(5 * time.Second)
				events = pool.SubMany(ctx, cfg.Relays, nostr.Filters{
					{Kinds: []int{nostr.KindGiftWrap}, Limit: 0},
				})
				continue
			}
			evt := relayEvent.Event

			// Client-side p-tag filter: the gate before any decryption.
			pTag := evt.Tags.GetFirst([]string{"p"})
			if pTag == nil || pTag.Value() != serverPK {
				continue
			}

			// Unwrap + dispatch; an unwrap failure is someone else's traffic.
			req, caller, err := UnwrapRequest(*evt, sk)
			if err != nil {
				continue
			}
			resp := dispatcher.Dispatch(caller, req)

			// Respond gift-wrapped to the CALLER (seal author), not to the
			// gift wrap's nonce key.
			// The e-tag rides on the gift wrap and must be added BEFORE
			// signing (WrapResponse) — a tag appended after signing
			// invalidates the signature and relays drop the event.
			respGW, err := WrapResponse(resp, caller, sk, nostr.Tag{"e", evt.ID})
			if err != nil {
				continue
			}

			publishCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			for result := range pool.PublishMany(publishCtx, cfg.Relays, respGW) {
				_ = result
			}
			cancel()
		}
	}
}
