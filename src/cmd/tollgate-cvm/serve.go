package main

import (
	"context"
	"log"
	"time"

	"github.com/nbd-wtf/go-nostr"
)

// unwrapFresh is the relay loop's per-event gate: unwrap the gift wrap, then
// decide freshness on the request's REAL clock — the rumor's created_at.
//
// It is deliberately NOT the gift wrap's created_at. NIP-59 randomizes the wrap
// (and the seal) up to hours into the past for timing privacy —
// nip59.GiftWrap stamps `nostr.Now() - 60*rand.Int63n(600)`, i.e. up to 10
// hours backdated — so deciding freshness on the wrap rejects legitimate
// requests by construction. That was #980 D1: the relay-delivered control plane
// was silently inert, with no error to observe (#982 fixed it).
//
// Both steps live in one function so the loop cannot pair an unwrap with the
// wrong clock. That pairing is exactly what #982 fixed and what nothing pinned:
// reverting the loop to the wrap clock left the whole suite green, because the
// test re-implemented this sequence instead of calling it. With the gate named,
// TestUnwrapFreshUsesTheRumorClockNotTheWrap fails when the clock is wrong.
//
// One decryption, not two: the rumor timestamp comes from the same unwrap that
// yields the request and caller.
func unwrapFresh(gw nostr.Event, serverSK string, now time.Time) (JSONRPCRequest, string, error) {
	req, caller, rumorTS, err := UnwrapRequest(gw, serverSK)
	if err != nil {
		return req, caller, err
	}
	if err := CheckFreshness(int64(rumorTS), now); err != nil {
		return req, caller, err
	}
	return req, caller, nil
}

// claimPollInterval is how often the daemon re-reads maintainer_npub. It is
// short enough that a handover takes effect while an operator is watching,
// and long enough that polling a local unix socket costs nothing.
const claimPollInterval = 15 * time.Second

// logf keeps the daemon's logging in one place (stdout/stderr are captured by
// procd into /tmp/tollgate-cvm.log). It never logs request payloads or key
// material — only lifecycle facts.
func logf(format string, args ...interface{}) { log.Printf(format, args...) }

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
	readMaintainer := func() string {
		if bridge == nil {
			return ""
		}
		resp, err := bridge.Call(CLIMessage{Command: "config", Args: []string{"get"}, Timestamp: time.Now()})
		if err != nil || !resp.Success {
			return ""
		}
		return MaintainerFromConfigPayload(resp.Data)
	}
	if maintainer == "" {
		maintainer = readMaintainer()
	}

	// Claim gate: no maintainer, no service. See RequireMaintainer for why
	// reads must not be served to the world on an unclaimed router.
	if err := RequireMaintainer(maintainer); err != nil {
		return err
	}

	authz := NewAuthorizer(maintainer)
	dispatcher := NewDispatcher(authz, bridge)

	pool := nostr.NewSimplePool(ctx)
	events := pool.SubMany(ctx, cfg.Relays, nostr.Filters{
		{Kinds: []int{nostr.KindGiftWrap}, Limit: 0},
	})

	// Publish the discovery announcement (audit 6.1). Before this, the
	// announcement was never published at all: BuildAnnouncement's only caller
	// was PublishAnnouncements, which nothing called — so no router could ever
	// appear in a maintainer's dashboard. Announce once now and re-announce
	// periodically, because relay retention of replaceable events is not
	// guaranteed.
	announce := func(maintainer string) {
		if err := PublishAnnouncements(pool, cfg.Relays, sk, maintainer); err != nil {
			logf("announce failed: %v", err)
		} else {
			logf("announced 11316/11317 as %s for maintainer %s", serverPK, maintainer)
		}
	}
	announce(authz.Maintainer())
	go func() {
		t := time.NewTicker(30 * time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				announce(authz.Maintainer())
			}
		}
	}()

	// Re-read the claim periodically (audit 8.1). Authorization was resolved
	// once at boot, so a handover left the OLD npub with full write authority
	// until the process restarted — silently, and including further
	// config_set. Watching the config makes the change take effect, and
	// re-announcing re-points discovery at the new owner.
	go func() {
		t := time.NewTicker(claimPollInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				current := readMaintainer()
				if current == authz.Maintainer() {
					continue
				}
				authz.SetMaintainer(current)
				logf("maintainer changed to %q — authorization and discovery updated", current)
				if current != "" {
					announce(current)
				}
			}
		}
	}()

	// Duplicate-delivery guard (audit Q1). Bounded + TTL'd so a hostile relay
	// cannot exhaust a router's memory.
	seen := NewSeenSet(1024, 5*time.Minute)

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

			// Duplicate delivery of the same event id executes once.
			if seen.Seen(evt.ID, time.Now()) {
				continue
			}

			// Unwrap + dispatch; an unwrap failure is someone else's traffic.
			//
			// Freshness is decided on the RUMOR's created_at — the request's
			// real clock — never on the gift wrap's: NIP-59 randomizes the
			// wrap's timestamp (up to hours into the past) for timing
			// privacy, so a freshness check on the wrap rejects legitimate
			// requests by construction (#980 D1 — the relay-delivered control
			// plane was silently inert for exactly this reason).
			// Unwrap + freshness in one gate, so the loop cannot pair the
			// unwrap with the wrap's clock (#980 D1 / #982).
			req, caller, err := unwrapFresh(*evt, sk, time.Now())
			if err != nil {
				continue
			}

			// The seal's author must never be the server itself: that is the
			// echo-loop class, and refusing it makes the class impossible even
			// if the ValidateKeyPair guard is ever bypassed (audit Q2 residue).
			if caller == serverPK {
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
