package fips_exit

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func offerBody(t *testing.T, ch Challenge) string {
	t.Helper()
	b, err := json.Marshal(PurchaseOffer{Challenge: ch})
	if err != nil {
		t.Fatalf("marshal offer: %v", err)
	}
	return string(b)
}

// The listener exists to be reachable, so the first thing it must prove is that
// it answers the two requests a buyer makes: what are the terms, and here is my
// payment.
func TestPurchaseHandlerServesTheChallenge(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	svc := NewPurchaseService(PurchaseConfig{
		Now:       func() time.Time { return now },
		PriceSats: 2,
		Mode:      BillingBytes,
		ExitKey:   PeerKey{0x11},
		Random:    bytes.NewReader(bytes.Repeat([]byte{0x42}, 64)),
	})
	rec := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	ch, ok := challengeFromHeader(t, rec)
	if !ok {
		t.Fatal("the advertisement carries no challenge")
	}
	if ch.PriceSats != 2 || ch.Mode != BillingBytes {
		t.Fatalf("challenge does not carry the terms: %+v", ch)
	}
	if ch.Nonce[0] != 0x42 {
		t.Fatal("challenge did not use the injected nonce")
	}
	if ch.ExitKey != (PeerKey{0x11}) {
		t.Fatal("challenge did not carry the exit key")
	}
}

// A purchase from a caller that cannot prove the claim must be refused, must
// not persist anything, and must not disclose why in more detail than needed.
func TestPurchaseHandlerRefusesUnverifiedClaim(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	store := &memStore{}
	svc := NewPurchaseService(PurchaseConfig{
		Now:       func() time.Time { return now },
		PriceSats: 2,
		Mode:      BillingBytes,
		ExitKey:   PeerKey{0x11},
		Store:     store,
		Cache:     NewNonceCache(8, now),
		Random:    bytes.NewReader(bytes.Repeat([]byte{0x42}, 64)),
		// The observed source is whatever the transport says the peer is; here
		// a tunnel address the claim was never bound to.
		ObservedSource: func(*http.Request) (string, error) {
			return "fd11:2233:4455:6677::9", nil
		},
	})

	claim, _, _ := redeemFixture(t, now)
	body, _ := json.Marshal(PurchaseRequest{Claim: claim, TTLSeconds: 60, ByteBudget: 5000})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(body)))
	svc.Handler().ServeHTTP(rec, req)

	if rec.Code == http.StatusOK {
		t.Fatal("an unbound claim was granted")
	}
	if store.writes != 0 {
		t.Fatalf("a refused purchase wrote durable state (%d writes)", store.writes)
	}
}

// The happy path, end to end through the handler: a genuine claim from its own
// tunnel gets an entitlement, and the clients file it renders actually contains
// the buyer's outer address.
func TestPurchaseHandlerGrantsAndRenders(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	store := &memStore{}
	clientsStore := &memStore{}
	claim, source, buyer := redeemFixture(t, now)
	svc := NewPurchaseService(PurchaseConfig{
		Now:          func() time.Time { return now },
		PriceSats:    2,
		Mode:         BillingBytes,
		ExitKey:      claim.Challenge.ExitKey,
		Store:        store,
		ClientsStore: clientsStore,
		Cache:        NewNonceCache(8, now),
		ObservedSource: func(*http.Request) (string, error) {
			return source.String(), nil
		},
	})

	body, _ := json.Marshal(PurchaseRequest{Claim: claim, TTLSeconds: 120, ByteBudget: 5000})
	rec := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(body))))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (%s), want 200", rec.Code, rec.Body.String())
	}
	// The reply must be the module's own contract, not a private one: the app
	// classifies on kind 1022 and reads allotment/metric/device-identifier.
	var reply struct {
		Kind      int    `json:"kind"`
		Allotment int    `json:"allotment"`
		Metric    string `json:"metric"`
		Device    string `json:"device-identifier"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &reply); err != nil {
		t.Fatalf("reply is not JSON: %v", err)
	}
	if reply.Kind != 1022 {
		t.Fatalf("kind = %d, want 1022", reply.Kind)
	}
	if reply.Allotment != 5000 {
		t.Fatalf("allotment = %d, want the granted budget 5000", reply.Allotment)
	}
	if reply.Metric != string(BillingBytes) {
		t.Fatalf("metric = %q, want %q", reply.Metric, BillingBytes)
	}
	if reply.Device != buyer.OuterAddress().String() {
		t.Fatalf("device-identifier = %q, want the outer address", reply.Device)
	}
	if store.writes == 0 {
		t.Fatal("a granted purchase persisted nothing")
	}
	// The clients file the daemon reads must name the buyer, and it must have
	// been written from the same state the purchase persisted.
	if clientsStore.data == nil {
		t.Fatal("no clients file was written")
	}
	if !strings.Contains(string(clientsStore.data), buyer.OuterAddress().String()) {
		t.Fatalf("clients file does not name the buyer: %s", clientsStore.data)
	}
	if _, err := LoadState(store); err != nil {
		t.Fatalf("state not persisted: %v", err)
	}
}

// A malformed body is a bad request, and must not be reported as a spent note:
// the app distinguishes "spent" from "did not understand you".
func TestPurchaseHandlerRejectsMalformedBody(t *testing.T) {
	svc := NewPurchaseService(PurchaseConfig{
		Now:       func() time.Time { return time.Unix(1_800_000_000, 0) },
		ExitKey:   PeerKey{0x11},
		Store:     &memStore{},
		Cache:     NewNonceCache(8, time.Unix(1_800_000_000, 0)),
		Mode:      BillingBytes,
		PriceSats: 2,
		ObservedSource: func(*http.Request) (string, error) {
			return "fd00::1", nil
		},
	})
	rec := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader("{not json")))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "malformed") {
		t.Fatalf("body should name the fault: %s", rec.Body.String())
	}
}

// A replayed purchase must be refused with a distinguishable status, because the
// buyer has to know a retry will never work while a new payment might.
func TestPurchaseHandlerRefusesReplayAsConflict(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	claim, source, _ := redeemFixture(t, now)
	svc := NewPurchaseService(PurchaseConfig{
		Now:       func() time.Time { return now },
		ExitKey:   claim.Challenge.ExitKey,
		Store:     &memStore{},
		Cache:     NewNonceCache(8, now),
		Mode:      BillingBytes,
		PriceSats: 2,
		ObservedSource: func(*http.Request) (string, error) {
			return source.String(), nil
		},
	})
	body, _ := json.Marshal(PurchaseRequest{Claim: claim, TTLSeconds: 60, ByteBudget: 1000})
	for i, want := range []int{http.StatusOK, http.StatusConflict} {
		rec := httptest.NewRecorder()
		svc.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(body))))
		if rec.Code != want {
			t.Fatalf("attempt %d: status = %d (%s), want %d", i+1, rec.Code, rec.Body.String(), want)
		}
	}
}

// The observed source must come from the transport, never from the request
// body or a header: a caller who can set it can steal any npub's tunnel.
func TestPurchaseHandlerIgnoresSpoofedHeaders(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	claim, source, _ := redeemFixture(t, now)
	var gotFromHeader bool
	svc := NewPurchaseService(PurchaseConfig{
		Now:       func() time.Time { return now },
		ExitKey:   claim.Challenge.ExitKey,
		Store:     &memStore{},
		Cache:     NewNonceCache(8, now),
		Mode:      BillingBytes,
		PriceSats: 2,
		ObservedSource: func(r *http.Request) (string, error) {
			// The resolver is given the request, so the test can prove the
			// header was not consulted by the handler itself.
			gotFromHeader = r.Header.Get("X-FIPS-Source") != ""
			return source.String(), nil
		},
	})
	body, _ := json.Marshal(PurchaseRequest{Claim: claim, TTLSeconds: 60, ByteBudget: 1000})
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(body)))
	req.Header.Set("X-FIPS-Source", "fd11:dead:beef::1")
	rec := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !gotFromHeader {
		t.Fatal("test setup did not carry the header")
	}
	// The grant must be bound to the resolver's answer, not the header value.
	state, err := LoadState(svc.Store())
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if _, ok := state.Entitlement(claim.Buyer); !ok {
		t.Fatal("grant missing")
	}
	applied, err := Apply(state, now, nil)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(applied.File.Entries) != 1 || applied.File.Entries[0].Addr != claim.Buyer.OuterAddress() {
		t.Fatalf("grant bound to something other than the observed source: %+v", applied.File.Entries)
	}
}

// A resolver failure is not a silent pass: the purchase must fail closed.
func TestPurchaseHandlerFailsClosedWhenSourceUnknown(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	claim, _, _ := redeemFixture(t, now)
	store := &memStore{}
	svc := NewPurchaseService(PurchaseConfig{
		Now:       func() time.Time { return now },
		ExitKey:   claim.Challenge.ExitKey,
		Store:     store,
		Cache:     NewNonceCache(8, now),
		Mode:      BillingBytes,
		PriceSats: 2,
		ObservedSource: func(*http.Request) (string, error) {
			return "", errors.New("no source binding on this listener")
		},
	})
	body, _ := json.Marshal(PurchaseRequest{Claim: claim, TTLSeconds: 60, ByteBudget: 1000})
	rec := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(body))))
	if rec.Code == http.StatusOK {
		t.Fatal("granted with no source binding")
	}
	if store.writes != 0 {
		t.Fatal("wrote state with no source binding")
	}
}

// The refusal table. Every one of these is a money or identity decision, so each
// gets its own status and each is asserted here rather than trusted to review.
func TestPurchaseHandlerRefusalTable(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	base := func() PurchaseConfig {
		return PurchaseConfig{
			Now:       func() time.Time { return now },
			PriceSats: 2,
			Mode:      BillingBytes,
			Store:     &memStore{},
			Cache:     NewNonceCache(8, now),
			Random:    bytes.NewReader(bytes.Repeat([]byte{0x42}, 64)),
			ObservedSource: func(*http.Request) (string, error) {
				return "fd00::1", nil
			},
		}
	}

	t.Run("method not allowed", func(t *testing.T) {
		rec := httptest.NewRecorder()
		NewPurchaseService(base()).Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/", nil))
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("status = %d, want 405", rec.Code)
		}
		if allow := rec.Header().Get("Allow"); allow == "" {
			t.Fatal("405 without an Allow header")
		}
	})

	t.Run("offer without a clock", func(t *testing.T) {
		cfg := base()
		cfg.Now = nil
		rec := httptest.NewRecorder()
		NewPurchaseService(cfg).Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", rec.Code)
		}
	})

	t.Run("purchase without authority", func(t *testing.T) {
		cfg := base()
		cfg.ObservedSource = nil
		rec := httptest.NewRecorder()
		NewPurchaseService(cfg).Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader("{}")))
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", rec.Code)
		}
	})

	t.Run("unparsable source", func(t *testing.T) {
		cfg := base()
		cfg.ObservedSource = func(*http.Request) (string, error) { return "not-an-address", nil }
		claim, _, _ := redeemFixture(t, now)
		body, _ := json.Marshal(PurchaseRequest{Claim: claim, TTLSeconds: 60, ByteBudget: 1000})
		rec := httptest.NewRecorder()
		NewPurchaseService(cfg).Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(body))))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", rec.Code)
		}
	})

	t.Run("state cannot be read", func(t *testing.T) {
		cfg := base()
		cfg.Store = &memStore{failRead: errors.New("io")}
		claim, _, _ := redeemFixture(t, now)
		body, _ := json.Marshal(PurchaseRequest{Claim: claim, TTLSeconds: 60, ByteBudget: 1000})
		rec := httptest.NewRecorder()
		NewPurchaseService(cfg).Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(body))))
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", rec.Code)
		}
	})

	t.Run("state cannot be written", func(t *testing.T) {
		cfg := base()
		cfg.Store = &memStore{failWrite: errors.New("disk")}
		claim, source, _ := redeemFixture(t, now)
		cfg.ExitKey = claim.Challenge.ExitKey
		cfg.ObservedSource = func(*http.Request) (string, error) { return source.String(), nil }
		body, _ := json.Marshal(PurchaseRequest{Claim: claim, TTLSeconds: 60, ByteBudget: 1000})
		rec := httptest.NewRecorder()
		NewPurchaseService(cfg).Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(body))))
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", rec.Code)
		}
	})

	t.Run("expired terms", func(t *testing.T) {
		cfg := base()
		claim, source, _ := redeemFixture(t, now)
		cfg.ExitKey = claim.Challenge.ExitKey
		cfg.ObservedSource = func(*http.Request) (string, error) { return source.String(), nil }
		later := claim.Challenge.ExpiresAt.Add(time.Second)
		cfg.Now = func() time.Time { return later }
		cfg.Cache = NewNonceCache(8, later)
		body, _ := json.Marshal(PurchaseRequest{Claim: claim, TTLSeconds: 60, ByteBudget: 1000})
		rec := httptest.NewRecorder()
		NewPurchaseService(cfg).Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(body))))
		if rec.Code != http.StatusGone {
			t.Fatalf("status = %d, want 410", rec.Code)
		}
	})

	t.Run("terms beyond the challenge", func(t *testing.T) {
		cfg := base()
		claim, source, _ := redeemFixture(t, now)
		cfg.ExitKey = claim.Challenge.ExitKey
		cfg.ObservedSource = func(*http.Request) (string, error) { return source.String(), nil }
		body, _ := json.Marshal(PurchaseRequest{
			Claim:      claim,
			TTLSeconds: int(claim.Challenge.ExpiresAt.Sub(now).Seconds()) * 2,
			ByteBudget: 1000,
		})
		rec := httptest.NewRecorder()
		NewPurchaseService(cfg).Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(body))))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d (%s), want 400", rec.Code, rec.Body.String())
		}
	})
}

// A buyer who names no window gets the challenge's own window, and a buyer who
// names no budget gets a default rather than an unbounded grant.
func TestPurchaseHandlerDefaultsTerms(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	claim, source, _ := redeemFixture(t, now)
	svc := NewPurchaseService(PurchaseConfig{
		Now:       func() time.Time { return now },
		ExitKey:   claim.Challenge.ExitKey,
		Store:     &memStore{},
		Cache:     NewNonceCache(8, now),
		Mode:      BillingBytes,
		PriceSats: 2,
		ObservedSource: func(*http.Request) (string, error) {
			return source.String(), nil
		},
	})
	body, _ := json.Marshal(PurchaseRequest{Claim: claim})
	rec := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(body))))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (%s), want 200", rec.Code, rec.Body.String())
	}
	state, err := LoadState(svc.Store())
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	ent, ok := state.Entitlement(claim.Buyer)
	if !ok {
		t.Fatal("no entitlement")
	}
	if !ent.ExpiresAt.Equal(claim.Challenge.ExpiresAt) {
		t.Fatalf("expiry = %v, want the challenge window %v", ent.ExpiresAt, claim.Challenge.ExpiresAt)
	}
	if ent.ByteBudget == 0 {
		t.Fatal("a default budget of zero would be an unbounded grant")
	}
}

// A buyer must be able to price this exit from GET / alone. The app parses the
// module's own advertisement grammar (kind 10021 + metric/step_size/price_per_step
// tags), so serving the challenge struct instead would be unreachable to the one
// client that exists. This asserts the wire shape, tag order and values.
func TestPurchaseOfferIsATollGateAdvertisement(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	svc := NewPurchaseService(PurchaseConfig{
		Now:       func() time.Time { return now },
		ExitKey:   PeerKey{0x11},
		PriceSats: 2,
		Mode:      BillingBytes,
		StepBytes: 60000,
		MintURL:   "https://mint.example",
		MinSteps:  1,
		Random:    bytes.NewReader(bytes.Repeat([]byte{0x42}, 64)),
		Store:     &memStore{},
		Cache:     NewNonceCache(8, now),
		ObservedSource: func(*http.Request) (string, error) {
			return "fd00::1", nil
		},
	})
	rec := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}

	var advert struct {
		Kind int        `json:"kind"`
		Tags [][]string `json:"tags"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &advert); err != nil {
		t.Fatalf("offer is not JSON: %v", err)
	}
	if advert.Kind != 10021 {
		t.Fatalf("kind = %d, want 10021 (a TollGate advertisement)", advert.Kind)
	}

	byName := map[string][]string{}
	for _, tag := range advert.Tags {
		if len(tag) > 0 {
			byName[tag[0]] = tag
		}
	}
	if got := byName["metric"]; len(got) != 2 || got[1] != "bytes" {
		t.Fatalf("metric tag = %v, want [metric bytes]", got)
	}
	if got := byName["step_size"]; len(got) != 2 || got[1] != "60000" {
		t.Fatalf("step_size tag = %v, want [step_size 60000]", got)
	}
	price := byName["price_per_step"]
	if len(price) != 6 {
		t.Fatalf("price_per_step tag = %v, want 6 fields", price)
	}
	if price[1] != "cashu" || price[2] != "2" || price[3] != "sat" ||
		price[4] != "https://mint.example" || price[5] != "1" {
		t.Fatalf("price_per_step tag = %v, want [price_per_step cashu 2 sat <mint> 1]", price)
	}

	// The challenge must still be reachable, or the buyer cannot sign anything.
	if rec.Header().Get("X-TollGate-Challenge") == "" {
		t.Fatal("the advertisement does not carry a challenge for the buyer to sign")
	}
}

// challengeFromHeader decodes the challenge the advertisement carries. The
// advertisement body stays the exact tag grammar a TollGate client parses, so
// the challenge travels beside it rather than inside it.
func challengeFromHeader(t *testing.T, r interface{}) (Challenge, bool) {
	t.Helper()
	var raw string
	switch v := r.(type) {
	case http.Header:
		raw = v.Get("X-TollGate-Challenge")
	case interface{ Header() http.Header }:
		raw = v.Header().Get("X-TollGate-Challenge")
	case *http.Response:
		raw = v.Header.Get("X-TollGate-Challenge")
	}
	if raw == "" {
		return Challenge{}, false
	}
	blob, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		t.Fatalf("challenge header is not base64: %v", err)
	}
	var ch Challenge
	if err := json.Unmarshal(blob, &ch); err != nil {
		t.Fatalf("challenge header is not a challenge: %v", err)
	}
	return ch, true
}
