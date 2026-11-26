package lnurlcash

import (
	"encoding/hex"
	"errors"
	"strings"
	"testing"
)

// These tests are driven by LUD-25's own published test vectors
// (`25-vectors.json`, sha256 of the file recorded in the PR body). The point is
// that the decoder is checked against the bytes the spec author produced with
// libbitcoinkernel, not against values this package computed for itself — a
// self-consistent codec that is off by one byte passes every test written from
// its own output.

// LUD-25 bearer note vector.
const (
	vecPreimage = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"
	vecHash     = "630dcd2966c4336691125448bbb25b4ff412a49c732db2c8abc1b8581bd710dd"
	vecLeaf     = "a820630dcd2966c4336691125448bbb25b4ff412a49c732db2c8abc1b8581bd710dd87"
	vecTapLeaf  = "edffc9fa683d8844ded0ba5ec4215d5940ae436b0675257c1344bcb082516b50"
	vecTweak    = "5649b0259110c0c11426819ea5a40b53e5a9a2b8c6fb3d6c43217df73b326364"
	vecQ        = "d18b619687343df2fc7a47e1daf25260b909bb563fb4b4b11e59e2bd64880982"
	vecControl  = "c050929b74c1a04954b78b4b6035e97a5e078a5a0f28ec96d547bfee9ace803ac0"
	vecCP1      = "cp16x9kr958xs7l9lr6glsa4ujjvzusnw6k876tfvg7t83t6eygpxpq6we0xc"
	vecCW1      = "cw1qqqqqq8lllll7qpr4qsxxrwd99nvgvmxjyf9gj9mkfd5laqj5jw8xtdjez4urwzcr0t3phv8qqsuq5yjnd6vrgzf2jmckjmqxh5h5hs83fdq728vjm2500lwnt8gqwkqqqsqqqgzqvzq2ps8pqys5zcvp58q7yq3zgf3g9gkzuvpjxsmrsw3u8c6x6a4c"
	vecDomain   = "mint.example"
	vecNoteURL  = "lnurlw://mint.example/w?k1=" + vecPreimage
)

// The certificate vector: signed by `service_sk` for domain mint.example at
// t = 1800000000, one case per amount.
const (
	vecMintPubkey = "035acdbd57663f858be6d61ec4bfcbc99492699010f1451e30a6550f26295e813d"
	vecCS1_1000n  = "cs10n1qtyekghmuysd2eu3qwtgg9v47hxlsa64lcd2l7v2rnrcqagccl8st2cav4as2pewnghel0mm0u88ql2qle03z6gyu34h84vsn6ky67gpddyayqqmjlftw"
	vecCS1_21u    = "cs210u1feedlr3qynxmdehp5qcnqm2gxfa2uz6dec93rj54nglw9nrfjv04u7xs83rznqazywxvhtx5wu303zq7kewgngaslr6de3nucjdp7tgpddyayqqergmft"
	vecCertT      = int64(1800000000)
	// The certificates section signs for the key-path vector's Q, not the bearer
	// note's — the two sections are different notes on purpose, and using one
	// section's Q with the other's certificate is exactly the mistake this
	// constant exists to make impossible.
	vecCertQ = "690ac33892c64aa53874b0066ab1332f0ef45cb7c0e017eae0828916f52aa99f"
	// The bearer note's own certificate, from the bearer_note section.
	vecBearerCS1 = "cs10n1xptnjgf4v6w4ewce89mk9766z63lhc36dp8c62sgfzzcm49uwtl5wf75g8k9pg9fsazs5zg9xvr8m48lsu98j29dnwuks30sdzgqwscpddyayqqc3yf72"
)

func mustHexBytes(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("fixture %q is not hex: %v", s, err)
	}
	return b
}

func mustQ(t *testing.T, s string) [32]byte {
	t.Helper()
	var q [32]byte
	copy(q[:], mustHexBytes(t, s))
	return q
}

// TestBearerPrimitivesMatchTheSpecVectors pins the commitment math byte for
// byte. If any of these drift, every note this gate accepts is the wrong note.
func TestBearerPrimitivesMatchTheSpecVectors(t *testing.T) {
	var h [32]byte
	copy(h[:], mustHexBytes(t, vecHash))

	leaf := bearerLeafScript(h)
	if got := hex.EncodeToString(leaf); got != vecLeaf {
		t.Fatalf("bearer leaf = %s, want %s", got, vecLeaf)
	}

	th, err := tapleafHash(leaf)
	if err != nil {
		t.Fatalf("tapleafHash: %v", err)
	}
	if got := hex.EncodeToString(th[:]); got != vecTapLeaf {
		t.Fatalf("tapleaf hash = %s, want %s", got, vecTapLeaf)
	}

	ik, err := bearerInternalKey()
	if err != nil {
		t.Fatalf("bearerInternalKey: %v", err)
	}
	tweak := taggedHash("TapTweak", ik.SerializeCompressed()[1:], th[:])
	if got := hex.EncodeToString(tweak[:]); got != vecTweak {
		t.Fatalf("taproot tweak = %s, want %s", got, vecTweak)
	}

	q := BearerOutputKey(h)
	if got := hex.EncodeToString(q[:]); got != vecQ {
		t.Fatalf("output key Q = %s, want %s", got, vecQ)
	}
}

func TestDecodeCP1RoundTripsTheVector(t *testing.T) {
	q, err := DecodeCP1(vecCP1)
	if err != nil {
		t.Fatalf("DecodeCP1(%s): %v", vecCP1, err)
	}
	if got := hex.EncodeToString(q[:]); got != vecQ {
		t.Fatalf("cp1 decodes to Q %s, want %s", got, vecQ)
	}
	back, err := EncodeCP1(q)
	if err != nil {
		t.Fatalf("EncodeCP1: %v", err)
	}
	if back != vecCP1 {
		t.Fatalf("EncodeCP1 = %s, want %s (bech32m, no length limit)", back, vecCP1)
	}

	// The hex short form of the same note (its hashlock h) must decode to the
	// same Q — LUD-25 requires one note, two encodings, never two notes.
	qFromHex, err := DecodeCP1(vecHash)
	if err != nil {
		t.Fatalf("DecodeCP1(h): %v", err)
	}
	if qFromHex != q {
		t.Fatalf("cp1 and hex h short form decode to different output keys")
	}
}

// A `k1` short-form note and a full `cw1` of the same spend must produce the
// same note: LUD-25 says a note minted in one form is spendable in the other,
// and comparing raw strings instead of decoded Q is how one note becomes two
// credits.
func TestPreimageAndCW1AreTheSameNote(t *testing.T) {
	a, err := Parse(vecNoteURL, "")
	if err != nil {
		t.Fatalf("Parse(k1 form): %v", err)
	}
	b, err := Parse("lnurlw://mint.example/w?k1="+vecCW1, "")
	if err != nil {
		t.Fatalf("Parse(cw1 form): %v", err)
	}

	for _, n := range []Note{a, b} {
		if got := hex.EncodeToString(n.Q[:]); got != vecQ {
			t.Fatalf("%s: Q = %s, want %s", n.Kind, got, vecQ)
		}
		if n.Domain != vecDomain {
			t.Fatalf("%s: domain = %q, want %q", n.Kind, n.Domain, vecDomain)
		}
		if n.Spend() != vecPreimage {
			t.Fatalf("%s: spend is not the vector preimage", n.Kind)
		}
		if got := hex.EncodeToString(n.Hash[:]); got != vecHash {
			t.Fatalf("%s: hashlock = %s, want %s", n.Kind, got, vecHash)
		}
	}
	if a.Q != b.Q {
		t.Fatal("the k1 and cw1 encodings of one spend decode to different notes")
	}
}

// A Note must never print its spend: it ends up in error messages and logs, and
// the spend is the value.
func TestNoteStringNeverContainsTheSpend(t *testing.T) {
	n, err := Parse(vecNoteURL, "")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	for _, format := range []string{"%v", "%+v", "%s", "%#v"} {
		out := sprintf(format, n)
		if strings.Contains(strings.ToLower(out), vecPreimage) {
			t.Fatalf("%s printed the bearer preimage: %s", format, out)
		}
		if strings.Contains(strings.ToLower(out), vecHash) {
			t.Fatalf("%s printed the note's hashlock: %s", format, out)
		}
	}
	// The cw1 form carries the preimage inside the spend string too.
	c, err := Parse("lnurlw://mint.example/w?k1="+vecCW1, "")
	if err != nil {
		t.Fatalf("Parse(cw1): %v", err)
	}
	if strings.Contains(sprintf("%v", c), vecCW1) {
		t.Fatalf("%s printed the cw1 spend", "%v")
	}
}

func TestParseRefusesWhatItCannotVerify(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		hint string
		want error
	}{
		{
			name: "ck1 key-path spend needs a tapscript interpreter",
			raw:  "ck1dy9vxwyjce922wr5kqrx4vfn9u80gh9hcrsp06hqs2y3daf24x0lcdy378rtefeae4mt8r7xk75z4mc0r7n8yykwkltlvndug8ytlem7pkmqwa3yhughh",
			hint: vecDomain,
			want: ErrUnsupportedSpend,
		},
		{
			name: "http for a clearnet host",
			raw:  "http://mint.example/w?k1=" + vecPreimage,
			want: ErrInsecureScheme,
		},
		{
			name: "no spend at all",
			raw:  "lnurlw://mint.example/w",
			want: ErrMalformedNote,
		},
		{
			name: "not a note",
			raw:  "hello",
			want: ErrMalformedNote,
		},
		{
			name: "empty",
			raw:  "",
			want: ErrMalformedNote,
		},
		{
			name: "64 hex that is not hex",
			raw:  strings.Repeat("z", 64),
			want: ErrMalformedNote,
		},
		{
			name: "bare spend with no issuer",
			raw:  vecPreimage,
			want: ErrMalformedNote,
		},
		{
			name: "hint disagrees with the note's own host",
			raw:  vecNoteURL,
			hint: "evil.example",
			want: ErrIssuerMismatch,
		},
		{
			name: "cp1 is not a spend",
			raw:  "lnurlw://mint.example/w?k1=" + vecCP1,
			want: ErrMalformedNote,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(tc.raw, tc.hint)
			if err == nil {
				t.Fatal("Parse accepted a note it cannot verify")
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("Parse error = %v, want %v", err, tc.want)
			}
			if strings.Contains(err.Error(), vecPreimage) {
				t.Fatalf("the refusal leaked the bearer preimage: %v", err)
			}
		})
	}
}

// A cw1 whose witness does not hash to its leaf's hashlock is provably
// worthless. It must be refused locally, before any issuer is contacted — this
// is the one case where a note can be ruled out with certainty and no network.
func TestCW1WithAWrongWitnessIsRefusedLocally(t *testing.T) {
	var cb CW1
	hrpData := vecCW1[3:]
	_ = hrpData
	// Build a cw1 for the vector leaf but claim a different preimage.
	bad, err := reencodeCW1WithWitness(t, vecCW1, bytesRepeat(0xAA, 32))
	if err != nil {
		t.Fatalf("rebuild cw1: %v", err)
	}
	_, err = Parse("lnurlw://mint.example/w?k1="+bad, "")
	if err == nil {
		t.Fatal("a cw1 whose witness does not open its own leaf was accepted")
	}
	if !errors.Is(err, ErrNotBearer) {
		t.Fatalf("error = %v, want ErrNotBearer", err)
	}
	_ = cb
}

// A locktimed spend needs the gate to assert its own clock, which LUD-25
// explicitly calls custodial policy. This version refuses it rather than
// honouring a time condition it cannot check.
func TestCW1WithATimelockClaimIsRefused(t *testing.T) {
	const timelockCW1 = "cw1ddyayq8llllluqpfqsqdyjttk96jqjr3rs245aqru2csmf9xnqp6pmak4ffjf69zt7nhflk506x69k5g4sqzrs2sj2dhfsdqf92t0z6tvq67j7j7q7995regajtd23ala6dvaqp6cqqyq9gzecqsd4klp7l7jrtmh2lmm42jh9dcazla80s35useymun2q0kz6358qn2ydtsu8yn9jgtyvv9kpuft0ntyucyhsnwt9kgf43vg2nq0k8sdn"
	_, err := Parse("lnurlw://mint.example/w?k1="+timelockCW1, "")
	if err == nil {
		t.Fatal("a timelocked spend was accepted")
	}
	if !errors.Is(err, ErrUnsupportedSpend) {
		t.Fatalf("error = %v, want ErrUnsupportedSpend", err)
	}
}

func TestCanonicalDomainMatchesTheSpecCases(t *testing.T) {
	cases := map[string]string{
		"https://Mint.Example.:443/w":                                            "mint.example",
		"https://mint.example:8443/w":                                            "mint.example",
		"https://user:pass@mint.example/w":                                       "mint.example",
		"lnurlw://mint.example/w":                                                "mint.example",
		"http://MINTMINTMINTMINTMINTMINTMINTMINTMINTMINTMINTMINTMINT4AD.onion/w": "mintmintmintmintmintmintmintmintmintmintmintmintmint4ad.onion",
	}
	for in, want := range cases {
		u := mustURL(t, in)
		got, err := CanonicalDomain(u)
		if err != nil {
			t.Errorf("CanonicalDomain(%s): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("CanonicalDomain(%s) = %q, want %q", in, got, want)
		}
	}

	// A Unicode label is not canonicalised here: the string the issuer signed is
	// the A-label, and silently converting would compare a punycode allowlist
	// against a different name than the customer presented.
	if _, err := CanonicalDomain(mustURL(t, "https://Bücher.example/w")); err == nil {
		t.Error("a raw Unicode label was accepted as canonical")
	}
}

func TestCertificateVectorsVerifyAndTheAmountIsAuthenticated(t *testing.T) {
	q := mustQ(t, vecCertQ)

	cert, err := DecodeCertificate(vecCS1_1000n)
	if err != nil {
		t.Fatalf("DecodeCertificate(1000n): %v", err)
	}
	if cert.AmountMSat != 1000 {
		t.Fatalf("cs10n decodes to %d msat, want 1000", cert.AmountMSat)
	}
	if cert.SignedAt != vecCertT {
		t.Fatalf("certificate t = %d, want %d", cert.SignedAt, vecCertT)
	}
	if err := Verify(cert, vecMintPubkey, vecDomain, q, 1000, vecCertT+60, 300); err != nil {
		t.Fatalf("the spec's own certificate did not verify: %v", err)
	}

	// The same signature against a different amount must fail: amount is inside
	// the signed message, so a note cannot be re-valued by editing its cs1 HRP.
	if err := Verify(cert, vecMintPubkey, vecDomain, q, 2000, vecCertT+60, 300); err == nil {
		t.Fatal("a certificate verified for an amount it does not attest")
	}

	other, err := DecodeCertificate(vecCS1_21u)
	if err != nil {
		t.Fatalf("DecodeCertificate(21u): %v", err)
	}
	if other.AmountMSat != 21_000_000 {
		t.Fatalf("cs21u decodes to %d msat, want 21000000", other.AmountMSat)
	}
	if err := Verify(other, vecMintPubkey, vecDomain, q, 21_000_000, vecCertT+60, 300); err != nil {
		t.Fatalf("the 21u certificate did not verify: %v", err)
	}
}

func TestCertificateIsBoundToDomainKeyAndTime(t *testing.T) {
	q := mustQ(t, vecCertQ)
	cert, err := DecodeCertificate(vecCS1_1000n)
	if err != nil {
		t.Fatalf("DecodeCertificate: %v", err)
	}

	if err := Verify(cert, vecMintPubkey, "other.example", q, 1000, vecCertT+60, 300); err == nil {
		t.Fatal("a certificate verified at a different domain")
	}

	const wrongKey = "02c6047f9441ed7d6d3045406e95c07cd85c778e4b8cef3ca7abac09b95c709ee5"
	if err := Verify(cert, wrongKey, vecDomain, q, 1000, vecCertT+60, 300); err == nil {
		t.Fatal("a certificate verified against a key that did not sign it")
	}

	// A certificate dated after the gate's clock by more than the allowed skew
	// is evidence the answer is not this issuer's (LUD-25 forbids signing ahead
	// of the clock). The vector's t is 1800000000, 2027.
	if err := Verify(cert, vecMintPubkey, vecDomain, q, 1000, vecCertT-600, 300); err == nil {
		t.Fatal("a future-dated certificate was accepted")
	}

	anotherNote := mustQ(t, vecQ) // the bearer note's Q, a different note
	if err := Verify(cert, vecMintPubkey, vecDomain, anotherNote, 1000, vecCertT+60, 300); err == nil {
		t.Fatal("a certificate verified for a different note's Q")
	}
}
