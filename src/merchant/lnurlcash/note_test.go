package lnurlcash

import (
	"context"
	"strings"
	"testing"

	"github.com/btcsuite/btcd/btcutil/bech32"
)

// encodeLNURL builds an `lnurl1…` string for a URL, the way an issuer does:
// UTF-8 bytes, 8->5 bit regrouping, bech32 with the "lnurl" HRP.
func encodeLNURL(t *testing.T, rawURL string) string {
	t.Helper()
	five, err := bech32.ConvertBits([]byte(rawURL), 8, 5, true)
	if err != nil {
		t.Fatalf("ConvertBits: %v", err)
	}
	enc, err := bech32.Encode("lnurl", five)
	if err != nil {
		t.Fatalf("bech32.Encode: %v", err)
	}
	return enc
}

// A note has two spellings in the wild: the `lnurl1…` bech32 string a wallet
// shows and the https URL it decodes to. The journal is keyed by fingerprint, so
// if the two spellings hashed differently the SAME note would get two journal
// records — and could be redeemed, and granted, twice. This is the double-grant
// invariant, tested at the level it actually breaks.
func TestOneNoteHasOneJournalKeyWhicheverwayItIsPresented(t *testing.T) {
	issuer := newFakeIssuer(t, "k1secret", 10_000_000)
	h := newHarness(t, Config{})

	urlForm := issuer.note()
	bechForm := encodeLNURL(t, urlForm)

	fpURL := h.svc.FingerprintOfPresented(urlForm)
	fpBech := h.svc.FingerprintOfPresented(bechForm)
	if fpURL != fpBech {
		t.Fatalf("one note produced two journal keys (%s vs %s): the same note could be redeemed twice",
			fpURL, fpBech)
	}

	// And the behaviour, not just the key: redeem it once in one spelling, then
	// present it in the other. The second attempt must be answered from the
	// journal, with no second callback to the issuer.
	if _, err := h.svc.Redeem(context.Background(), urlForm, "aa:bb:cc:dd:ee:ff"); err != nil {
		t.Fatalf("first Redeem (url form): %v", err)
	}
	afterFirst := issuer.callbackCount()

	if _, err := h.svc.Redeem(context.Background(), bechForm, "aa:bb:cc:dd:ee:ff"); err != nil {
		t.Fatalf("second Redeem (bech32 form): %v", err)
	}
	if got := issuer.callbackCount(); got != afterFirst {
		t.Fatalf("the issuer saw %d callbacks after the bech32 re-presentation, want %d: "+
			"the same note was presented twice", got, afterFirst)
	}
}

// A presented note is either an `lnurl1…` bech32 string or an https URL. Both
// decode to the issuer's withdrawRequest endpoint; anything else is refused
// before any network call, because the note is an untrusted input that decides
// which host the gate will talk to.
func TestDecodeNoteAcceptsBothSpellings(t *testing.T) {
	tests := []struct {
		name      string
		presented string
		wantURL   string
	}{
		{
			name:      "https URL",
			presented: "https://issuer.example/withdraw?x=1",
			wantURL:   "https://issuer.example/withdraw?x=1",
		},
		{
			name:      "bech32 lnurl",
			presented: encodeLNURL(t, "https://issuer.example/withdraw?k1=secret"),
			wantURL:   "https://issuer.example/withdraw?k1=secret",
		},
		{
			name:      "bech32 in upper case with surrounding whitespace",
			presented: "  " + strings.ToUpper(encodeLNURL(t, "https://issuer.example/w")) + "\n",
			wantURL:   "https://issuer.example/w",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			note, err := DecodeNote(tt.presented)
			if err != nil {
				t.Fatalf("DecodeNote(%q): %v", tt.presented, err)
			}
			if note.URL != tt.wantURL {
				t.Fatalf("DecodeNote URL = %q, want %q", note.URL, tt.wantURL)
			}
			if note.Raw == "" {
				t.Fatal("DecodeNote kept no raw note; the fingerprint needs it")
			}
		})
	}
}

func TestDecodeNoteRefusesUntrustedShapes(t *testing.T) {
	httpNote := "http://issuer.example/withdraw"
	tests := []struct {
		name      string
		presented string
		wantInErr string
	}{
		{name: "empty", presented: "   ", wantInErr: "empty"},
		{name: "plain http URL", presented: httpNote, wantInErr: "https"},
		{name: "relative path", presented: "/withdraw", wantInErr: "https"},
		{name: "not a note at all", presented: "cashuBnotanote", wantInErr: "note"},
		{name: "wrong bech32 hrp", presented: "lnbc1qqqqqqqq", wantInErr: "note"},
		{
			name:      "bech32 payload that is not a URL",
			presented: encodeLNURL(t, "not a url at all"),
			wantInErr: "https",
		},
		{
			name:      "bech32 payload that is plain http",
			presented: encodeLNURL(t, httpNote),
			wantInErr: "https",
		},
		{name: "URL with userinfo (credential smuggling)", presented: "https://user:pw@issuer.example/w", wantInErr: "userinfo"},
		{name: "fragment is not sent, refuse rather than silently drop", presented: "https://issuer.example/w#frag", wantInErr: "fragment"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := DecodeNote(tt.presented); err == nil {
				t.Fatalf("DecodeNote(%q) accepted an untrusted note", tt.presented)
			} else if !strings.Contains(err.Error(), tt.wantInErr) {
				t.Fatalf("DecodeNote error = %q, want it to mention %q", err, tt.wantInErr)
			}
		})
	}
}

// The note is a bearer secret. Its error strings and its String() must never
// carry it: these values end up in logs and in notice responses.
func TestNoteDoesNotExposeItsSecret(t *testing.T) {
	const secret = "https://issuer.example/withdraw?k1=SUPERSECRETK1"
	note, err := DecodeNote(secret)
	if err != nil {
		t.Fatalf("DecodeNote: %v", err)
	}
	if strings.Contains(note.String(), "SUPERSECRETK1") {
		t.Fatalf("Note.String() leaks the k1 secret: %q", note.String())
	}
	// Deliberately not asserted: note.URL does carry the secret, because the
	// gate must GET it. The rule is about the surfaces written to a log or a
	// response, and those are String() and every error below.
	if _, err := DecodeNote("http://issuer.example/withdraw?k1=SUPERSECRETK1"); err == nil ||
		strings.Contains(err.Error(), "SUPERSECRETK1") {
		t.Fatalf("refusal error leaks the k1 secret: %v", err)
	}
}
