//go:build e2e

// Live-relay proof for the discovery mechanism (ADR D4/D5 assertion): the
// announced kind-11316 must carry the maintainer p tag AND be findable by a
// `#p` subscription — which is the only thing that makes "all my tollgates in
// one place" work. Before the audit fix this path was never executed at all.
//
// Run: TOLLGATE_CVM_E2E=1 go test -tags e2e -run TestE2EAnnouncementDiscovery ./...
package main

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/nbd-wtf/go-nostr"
)

func TestE2EAnnouncementDiscovery(t *testing.T) {
	if os.Getenv("TOLLGATE_CVM_E2E") != "1" {
		t.Skip("set TOLLGATE_CVM_E2E=1 to run live relay e2e")
	}

	serverSK := nostr.GeneratePrivateKey()
	serverPK, _ := nostr.GetPublicKey(serverSK)
	maintainerSK := nostr.GeneratePrivateKey()
	maintainerPK, _ := nostr.GetPublicKey(maintainerSK)

	// The maintainer's npub, as config.json would hold it (bech32).
	maintainerNpub, err := encodeNpub(maintainerPK)
	if err != nil {
		t.Fatalf("encode npub: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool := nostr.NewSimplePool(ctx)

	// A dashboard subscribes BEFORE the router announces, exactly as a real
	// one would.
	// Filter built via the map form: "#p" is not a valid Go field name, and
	// the tag key must be spelled exactly for the relay to index it.
	f := nostr.Filter{Kinds: []int{annKindServer}}
	f.Tags = nostr.TagMap{"p": []string{maintainerPK}}
	sub := pool.SubMany(ctx, e2eRelays, nostr.Filters{f})

	if err := PublishAnnouncements(pool, e2eRelays, serverSK, maintainerNpub); err != nil {
		t.Fatalf("PublishAnnouncements: %v", err)
	}

	deadline := time.After(60 * time.Second)
	for {
		select {
		case re, ok := <-sub:
			if !ok {
				continue
			}
			evt := re.Event
			if evt.Kind != annKindServer {
				continue
			}
			if evt.PubKey != serverPK {
				continue // not our router
			}
			// The assertion the ADR makes: d slug AND maintainer p tag.
			d := evt.Tags.GetFirst([]string{"d"})
			p := evt.Tags.GetFirst([]string{"p"})
			tt := evt.Tags.GetFirst([]string{"t"})
			if d == nil || d.Value() != annDSlug {
				t.Fatalf("d tag = %v, want %q", d, annDSlug)
			}
			// The p tag must be the HEX pubkey (NIP-01 indexable form).
			if p == nil || p.Value() != maintainerPK {
				t.Fatalf("p tag = %v, want maintainer hex %q", p, maintainerPK)
			}
			if tt == nil || tt.Value() != annClassTag {
				t.Fatalf("t tag = %v, want %q", tt, annClassTag)
			}
			if ok, _ := evt.CheckSignature(); !ok {
				t.Fatal("announcement signature invalid — relays would have dropped it")
			}
			t.Logf("E2E discovery OK: found router %s under maintainer tag via #p on %v", serverPK, e2eRelays)
			return
		case <-deadline:
			t.Fatal("announcement never found by #p subscription within 60s — the discovery mechanism does not work")
		}
	}
}
