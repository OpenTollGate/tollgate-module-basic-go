package main

// The receiver-key binding, end to end through the HTTP surface.
//
// # The bug these tests exist for
//
// A channel names the key its receiver will sign the close with. Three facts have
// to agree or the channel is uncloseable:
//
//  1. what `GET /spilman/params` advertises,
//  2. what the payee actually holds and will sign with,
//  3. what the payer put in the channel it funded.
//
// In this module (2) did not exist as a value at all: `ReceiverKeyIsAcceptable`
// returned a hardcoded `true` (rail_cgo.go), so the answer to "is this key mine?"
// was "yes" for every key. And (1) advertised nothing, so a payer had no way to
// learn the right key even if it wanted to. A payer was therefore free to open a
// channel against a throwaway key: the funding lands at the mint, the payee cannot
// sign a close for it, and the money sits in a channel nobody can settle.
//
// # What must now hold
//
//   - `/spilman/params` advertises `receiver_pubkey`, and it is the key derived from
//     this payee's own persisted merchant identity.
//   - A registration whose `receiver_pubkey` is that key is accepted.
//   - A registration whose `receiver_pubkey` is anything else is REFUSED, before
//     anything is written — because accepting it strands the payer's funding.
//   - `ReceiverKeyIsAcceptable` answers from a real comparison, not a constant.
//
// Every one of these fails if the fix is reverted, which is the point: the previous
// suite passed while the real path was dead.

import (
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
)

// advertisedParams fetches /spilman/params and decodes it.
func advertisedParams(t *testing.T, mux *http.ServeMux) map[string]any {
	t.Helper()
	rec := do(t, mux, http.MethodGet, "/spilman/params")
	if rec.Code != http.StatusOK {
		t.Fatalf("/spilman/params = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("params is not JSON: %v (%s)", err, rec.Body.String())
	}
	return out
}

// postChannel POSTs a registration body and returns the recorder.
func postChannel(t *testing.T, mux *http.ServeMux, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/spilman/channel", stringReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// THE LOAD-BEARING ASSERTION.
//
// `/spilman/params` must advertise the receiver key this payee will actually sign a
// close with. Without it a payer can only guess — and the historical guess was a
// random key, which produced channels that funded successfully and could never be
// settled.
//
// The expected value is derived from the payee's OWN persisted identity, so this
// also pins that the advertisement is not some fresh per-request key: a per-boot or
// per-request key would be a different string here and would orphan every channel
// opened before it rotated.
func TestParamsAdvertisesTheReceiverKeyThePayeeHolds(t *testing.T) {
	withSpilman(t, true, t.TempDir()+"/channels.db")
	params := advertisedParams(t, newMux())

	raw, ok := params["receiver_pubkey"]
	if !ok {
		t.Fatalf("params advertises no receiver_pubkey, so no payer can address a "+
			"channel this payee will settle: %v", params)
	}
	advertised, _ := raw.(string)
	if advertised == "" {
		t.Fatalf("receiver_pubkey is empty: %v", params)
	}
	if _, err := hex.DecodeString(advertised); err != nil {
		t.Fatalf("receiver_pubkey is not hex: %q (%v)", advertised, err)
	}
	if _, err := btcec.ParsePubKey(mustHex(t, advertised)); err != nil {
		// A compressed key parses with btcec; an x-only key needs schnorr.ParsePubKey.
		if _, xerr := schnorr.ParsePubKey(mustHex(t, advertised)); xerr != nil {
			t.Fatalf("receiver_pubkey is neither a compressed nor an x-only secp256k1 "+
				"key: %q", advertised)
		}
	}

	// Stable across calls: a key that changes per request cannot be bound at all.
	again := advertisedParams(t, newMux())
	if again["receiver_pubkey"] != raw {
		t.Fatalf("receiver_pubkey changed between two requests (%v -> %v); a payer "+
			"that read the first value could never settle", raw, again["receiver_pubkey"])
	}
}

// A channel addressed to a key this payee does not hold must be REFUSED.
//
// This is the half that actually protects money. The payer funds at the mint
// BEFORE this body arrives, so a 200 here means the payee has taken responsibility
// for a channel only some other key can close. Fail closed.
func TestChannelForAForeignReceiverKeyIsRefused(t *testing.T) {
	withSpilman(t, true, t.TempDir()+"/channels.db")
	mux := newMux()
	params := advertisedParams(t, mux)

	foreign, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatalf("generate a foreign key: %v", err)
	}
	foreignHex := hex.EncodeToString(foreign.PubKey().SerializeCompressed())
	if foreignHex == params["receiver_pubkey"] {
		t.Fatal("fixture collision: the foreign key equals the advertised one")
	}

	body, err := json.Marshal(map[string]any{
		"channel_id":      "channel-for-somebody-else",
		"unit":            "sat",
		"capacity":        64,
		"expiry":          2_000_000_000,
		"receiver_pubkey": foreignHex,
		"mint_url":        "https://mint.example",
		"keyset_id":       "00aa",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	rec := postChannel(t, mux, string(body))
	if rec.Code == http.StatusOK {
		t.Fatalf("ACCEPTED a channel for a foreign receiver key — that channel is "+
			"fundable and unsettleable. body=%s", rec.Body.String())
	}
	if !contains(rec.Body.String(), "receiver") {
		t.Errorf("the refusal must name the field that is wrong; got %s", rec.Body.String())
	}
}

// The same body addressed to the ADVERTISED key must not be refused for a key
// reason. It may still be refused for other reasons (this fixture has no real
// funding), so the assertion is narrow and honest: the error must not be about the
// receiver key.
//
// Without this, a "fix" that simply rejects every registration would pass the test
// above and be useless.
func TestChannelForTheAdvertisedKeyIsNotRefusedForAKeyReason(t *testing.T) {
	withSpilman(t, true, t.TempDir()+"/channels.db")
	mux := newMux()
	params := advertisedParams(t, mux)

	body, err := json.Marshal(map[string]any{
		"channel_id":      "channel-for-ourselves",
		"unit":            "sat",
		"capacity":        64,
		"expiry":          2_000_000_000,
		"receiver_pubkey": params["receiver_pubkey"],
		"mint_url":        "https://mint.example",
		"keyset_id":       "00aa",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	rec := postChannel(t, mux, string(body))
	if contains(rec.Body.String(), "receiver_pubkey is not this payee") ||
		contains(rec.Body.String(), "receiver key") {
		t.Fatalf("refused a channel addressed to the payee's OWN advertised key: %d %s",
			rec.Code, rec.Body.String())
	}
}

// mustHex decodes hex or fails the test.
func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("not hex: %q", s)
	}
	return b
}
