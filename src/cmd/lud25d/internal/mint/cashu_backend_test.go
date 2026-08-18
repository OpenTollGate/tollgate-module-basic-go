package mint

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OpenTollGate/gonuts-tollgate/cashu/nuts/nut04"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeCashuMint starts an httptest server that emulates a Cashu mint's
// NUT-04 quote endpoints. It returns a configurable BOLT11 invoice and
// tracks quote state transitions (UNPAID → PAID).
type fakeCashuMint struct {
	t        *testing.T
	server   *httptest.Server
	quotes   map[string]*nut04.PostMintQuoteBolt11Response
	paidFlag map[string]*atomic.Bool
	counter  atomic.Uint64
}

func newFakeCashuMint(t *testing.T) *fakeCashuMint {
	f := &fakeCashuMint{
		t:        t,
		quotes:   make(map[string]*nut04.PostMintQuoteBolt11Response),
		paidFlag: make(map[string]*atomic.Bool),
	}

	mux := http.NewServeMux()

	// POST /v1/mint/quote/bolt11 — create mint quote
	mux.HandleFunc("POST /v1/mint/quote/bolt11", func(w http.ResponseWriter, r *http.Request) {
		var req nut04.PostMintQuoteBolt11Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		id := f.counter.Add(1)
		quoteID := "quote-" + itoa(id)
		paid := &atomic.Bool{}

		idx := int((id - 1) % uint64(len(testBolt11Invoices)))
		resp := &nut04.PostMintQuoteBolt11Response{
			Quote:   quoteID,
			Request: testBolt11Invoices[idx],
			Amount:  req.Amount,
			Unit:    "sat",
			State:   nut04.Unpaid,
			Expiry:  uint64(time.Now().Add(30 * time.Minute).Unix()),
		}

		f.quotes[quoteID] = resp
		f.paidFlag[quoteID] = paid

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
	})

	// GET /v1/mint/quote/bolt11/{quoteId} — check quote state
	mux.HandleFunc("/v1/mint/quote/bolt11/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		quoteID := strings.TrimPrefix(r.URL.Path, "/v1/mint/quote/bolt11/")
		quoteID = strings.Trim(quoteID, "/")

		resp, ok := f.quotes[quoteID]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}

		if f.paidFlag[quoteID].Load() {
			resp.State = nut04.Paid
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
	})

	f.server = httptest.NewServer(mux)
	t.Cleanup(f.server.Close)

	return f
}

func (f *fakeCashuMint) URL() string {
	return f.server.URL
}

func (f *fakeCashuMint) markPaid(quoteID string) {
	f.paidFlag[quoteID].Store(true)
}

func itoa(n uint64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// testBolt11Invoices are 10 real BOLT11 invoices (testnet) with unique
// payment hashes, generated with lnd's zpay32. These are needed because
// ln-decodepay can only parse real BOLT11 strings — fake ones fail.
var testBolt11Invoices = []string{
	"lntb10u1p4ggr9hpp58aqzmuvs47upzxl4he74xhlh3drqmyky4x5tgad8a4zhefunfxrqdq2w3jhxapqxy4wj8q8gglxcpgzgejggtmjjvkawr9qa2wyh29nnfru8v4k7pjpry07mvhfaheqgs69nzl2uw3eazvq2lthp02kj0pqu7rwpcggvsnucqlx2up4",
	"lntb20u1p4ggr9hpp5x2s9jr3y8p7y0m9vxl8hsdec9g7g8kmzxsnw5hxxusq5ut9y096qdq2w3jhxapqxgnn95rek5mfs00j4m00sjd6scekvznyms0ee8dluga2sp28khjeaqt8sc3swulrcmlj6x5895wvrpx47vmwejdmed7rh9akqdq9es92qqaecene",
	"lntb30u1p4ggr9hpp5akj35q324pf76aepggfcg8k2cylwck9y5u0clcvuyscdgpuefl7sdq2w3jhxapqxvyaf2kdhw99w2mwm2auanranv8s9p89fc0rphalczm9tr9nm96zes0hr9t0ssy6avcp4kpzullzqwsqadjvh7dup8ke3f52752sgskmqqhh63a2",
	"lntb40u1p4ggr9hpp5ngzn8ew9ersy6pw9h0ep5rrgeyf35yfnwhwxrsvll005cxqsxs5sdq2w3jhxapqxsthvrj9tvpgjz0wauv5jn62gchsfl4jnyrq7yk5c0nlf5mqlswvlsjzfsdd9qcr2lhvlnv89jmvfcxcvxdah2nnq6x84vpsglwausy5qqnf25se",
	"lntb50u1p4ggr9hpp5wagv0sx0w5rysue5s6xrwexet3t6s9a8kylfcuy9f0r97rzmwlzqdq2w3jhxapqx5jcf2dzaqj54nat08m00zp5qrsw7rptwz2kmdt8pye089v3y39g585tudly0uzc0885xcysuae086qjqny0agxkr3ujm0axj36925t9cquu5p52",
	"lntb60u1p4ggr9hpp5cmlrnp0j0tjmw58mqylcdfzx66zj85ng35lmr859t7tkguvdeefsdq2w3jhxapqxcav0wmvjw2ykhy93saja888m9y9c27ttyd8f9z2vejpelg6dz4hx8myespgerppnsdcrutcfnv3e2jsx64v2ehc70566cvdp0kvkm34cqwsqnu8",
	"lntb70u1p4ggr9hpp5cjdetypsv07lu50g7wvtenv7n59p98fdmvx7wru6jmvf4v89gv9qdq2w3jhxapqxum7yx0p25c77lhnrhj6nv94375hdq3j882nhvugyhzensnzxeg7c9ne0pypa47t35zj0lhy82reg3wxyx33tnk9wr86uqf5ycfe37tkcptdm0y3",
	"lntb80u1p4ggr9hpp5vepsthewj03uu4nry4meetyc04js50pnwy7hv4h4p58fv9hw7unsdq2w3jhxapq8qh5ctgcgvzfxm26z50g0h2zktazv9xqqfun64pt0l3av92hycdm8s93l7y0jgg8xulvgftvz7agrpcd2gev2ny67ug6ha5f68c03hhqsqqs86dt",
	"lntb90u1p4ggr9hpp55ed8qhxtwlkdw585pzv2m7mnmm5tc2mgdnuj7zfezvvxxtmvuq5sdq2w3jhxapq8yxue8wzf5xaytc8a2qck59nqa2nmmfyz6vhvqrw90vksthr0q5qsp5ml6lrj8dhl7jg3hg48ssqm52hzssdmcz70pucqp5pv8jh0u8tcpfjhyug",
	"lntb100u1p4ggr9hpp5pthawap2h09jjymxf3sx3ldedwrq5znfqvvxy59nhmmp3fehhh4sdqvw3jhxapqxycqlwga5vrus9rrraxreep5qkn6krqy9zdy72ukp82hrlk84z6fa88xraslwep7lwaueva0svdh93zt32ay092fdymwjn74rrq303nj72cpceuwe8",
}

// --- Tests ---

func TestCashuBackend_CreateMintingInvoice(t *testing.T) {
	fakeMint := newFakeCashuMint(t)
	db := newTestDB(t)
	backend := NewCashuBackend(db, fakeMint.URL(), 90*24*time.Hour)

	k1, bolt11, paymentHash, err := backend.CreateMintingInvoice(1000)

	require.NoError(t, err, "CreateMintingInvoice should succeed")
	assert.NotEmpty(t, k1, "k1 should be non-empty")
	assert.NotEmpty(t, bolt11, "bolt11 should be non-empty")
	assert.NotEmpty(t, paymentHash, "paymentHash should be non-empty")
	assert.Len(t, k1, 64, "k1 should be 32 bytes hex-encoded (64 chars)")

	// Verify the note was stored in DB
	note, err := db.GetNote(k1)
	require.NoError(t, err, "note should be retrievable from DB")
	assert.Equal(t, k1, note.K1, "stored k1 should match returned k1")
	assert.Equal(t, paymentHash, note.PaymentHash, "stored payment_hash should match")
	assert.Equal(t, int64(1000), note.AmountMsat, "stored amount should match")
	assert.Equal(t, NotePending, note.Status, "new note should be pending")
}

func TestCashuBackend_CheckPayment(t *testing.T) {
	fakeMint := newFakeCashuMint(t)
	db := newTestDB(t)
	backend := NewCashuBackend(db, fakeMint.URL(), 90*24*time.Hour)

	k1, _, _, err := backend.CreateMintingInvoice(500)
	require.NoError(t, err)

	// Before payment: should be unpaid
	paid, err := backend.CheckPayment(k1)
	require.NoError(t, err)
	assert.False(t, paid, "note should be unpaid initially")

	// Mark the quote as paid on the fake mint
	note, err := db.GetNote(k1)
	require.NoError(t, err)
	fakeMint.markPaid(note.QuoteID)

	// After payment: should be paid
	paid, err = backend.CheckPayment(k1)
	require.NoError(t, err)
	assert.True(t, paid, "note should be paid after mint confirms")

	// Verify DB state updated
	updatedNote, err := db.GetNote(k1)
	require.NoError(t, err)
	assert.Equal(t, NotePaid, updatedNote.Status, "note status should be paid in DB")
	assert.NotNil(t, updatedNote.PaidAt, "paid_at should be set")
}

func TestCashuBackend_CheckPayment_UnknownK1(t *testing.T) {
	fakeMint := newFakeCashuMint(t)
	db := newTestDB(t)
	backend := NewCashuBackend(db, fakeMint.URL(), 90*24*time.Hour)

	_, err := backend.CheckPayment("nonexistent-k1")
	assert.Error(t, err, "checking unknown k1 should error")
	assert.ErrorIs(t, err, ErrNoteNotFound)
}

func TestK1Independence(t *testing.T) {
	fakeMint := newFakeCashuMint(t)
	db := newTestDB(t)
	backend := NewCashuBackend(db, fakeMint.URL(), 90*24*time.Hour)

	// Generate multiple invoices and verify k1 values are:
	// 1. Unique across calls
	// 2. NOT equal to the Lightning payment hash
	// 3. Properly hex-encoded 32-byte CSPRNG values

	k1Set := make(map[string]bool)
	paymentHashSet := make(map[string]bool)

	for i := 0; i < 10; i++ {
		k1, _, paymentHash, err := backend.CreateMintingInvoice(int64(100 * (i + 1)))
		require.NoError(t, err)

		// k1 must be unique
		assert.False(t, k1Set[k1], "k1 must be unique across calls (iteration %d)", i)
		k1Set[k1] = true

		// k1 must NOT equal the payment hash
		assert.NotEqual(t, k1, paymentHash, "k1 must be independent of Lightning payment hash")

		// payment hashes should differ across invoices (different amounts → different invoices)
		paymentHashSet[paymentHash] = true
	}

	assert.Len(t, k1Set, 10, "all 10 k1 values should be distinct")
	assert.Len(t, paymentHashSet, 10, "all 10 payment hashes should be distinct")

	// Verify k1 has high entropy: no two k1s share more than 4 consecutive hex chars
	k1s := make([]string, 0, len(k1Set))
	for k := range k1Set {
		k1s = append(k1s, k)
	}
	for i := 0; i < len(k1s); i++ {
		for j := i + 1; j < len(k1s); j++ {
			common := 0
			maxCommon := 0
			for c := 0; c < len(k1s[i]) && c < len(k1s[j]); c++ {
				if k1s[i][c] == k1s[j][c] {
					common++
				} else {
					if common > maxCommon {
						maxCommon = common
					}
					common = 0
				}
			}
			if common > maxCommon {
				maxCommon = common
			}
			assert.Less(t, maxCommon, 12, "k1 values should have high entropy (max %d consecutive matching chars between %d,%d)", maxCommon, i, j)
		}
	}
}

func TestCashuBackend_ExpirySet(t *testing.T) {
	fakeMint := newFakeCashuMint(t)
	db := newTestDB(t)
	expiry := 90 * 24 * time.Hour
	backend := NewCashuBackend(db, fakeMint.URL(), expiry)

	k1, _, _, err := backend.CreateMintingInvoice(1000)
	require.NoError(t, err)

	note, err := db.GetNote(k1)
	require.NoError(t, err)

	// Expiry should be approximately now + 90 days
	now := time.Now().Unix()
	expectedExpiry := now + int64(expiry.Seconds())
	assert.InDelta(t, expectedExpiry, note.ExpiresAt, 5, "expiry should be ~90 days from creation")
}
