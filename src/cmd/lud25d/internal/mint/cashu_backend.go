package mint

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/OpenTollGate/gonuts-tollgate/cashu/nuts/nut04"
	decodepay "github.com/nbd-wtf/ln-decodepay"
)

// DefaultExpiry is the default note expiry duration (90 days).
const DefaultExpiry = 90 * 24 * time.Hour

// MintQuoteClient is the interface for interacting with a Cashu mint's
// NUT-04 endpoints. The real implementation uses gonuts-tollgate's
// wallet.Wallet; tests provide a fake.
type MintQuoteClient interface {
	// RequestMint creates a NUT-04 mint quote at the given mint URL.
	RequestMint(amount uint64, mintURL string) (*nut04.PostMintQuoteBolt11Response, error)
	// MintQuoteState polls the state of a NUT-04 mint quote.
	MintQuoteState(quoteID string) (*nut04.PostMintQuoteBolt11Response, error)
}

// HTTPMintClient is the default MintQuoteClient implementation that
// talks directly to a Cashu mint's REST API. It does NOT use
// wallet.Wallet because the wallet manages its own BoltDB state and
// seed — we only need the NUT-04 quote lifecycle here.
type HTTPMintClient struct {
	httpClient *http.Client
}

// NewHTTPMintClient creates a new HTTPMintClient with sensible defaults.
func NewHTTPMintClient() *HTTPMintClient {
	return &HTTPMintClient{
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *HTTPMintClient) RequestMint(amount uint64, mintURL string) (*nut04.PostMintQuoteBolt11Response, error) {
	reqBody := nut04.PostMintQuoteBolt11Request{
		Amount: amount,
		Unit:   "sat",
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal mint quote request: %w", err)
	}

	url := normalizeURL(mintURL) + "/v1/mint/quote/bolt11"
	resp, err := c.httpClient.Post(url, "application/json", bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("request mint quote: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read mint quote response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("mint returned HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var quoteResp nut04.PostMintQuoteBolt11Response
	if err := json.Unmarshal(respBody, &quoteResp); err != nil {
		return nil, fmt.Errorf("unmarshal mint quote response: %w", err)
	}

	return &quoteResp, nil
}

func (c *HTTPMintClient) MintQuoteState(quoteID string) (*nut04.PostMintQuoteBolt11Response, error) {
	// This implementation needs the mint URL to poll state. Since the
	// interface signature doesn't carry it, we store it on the struct
	// per-request via a wrapper. For the HTTPMintClient used in production,
	// the CashuBackend will call this with the configured mint URL.
	// See CashuBackend.CheckPayment which uses a direct HTTP call.
	//
	// This method is provided for interface compliance but the
	// CashuBackend uses its own polling logic with the stored mint URL.
	return nil, errors.New("HTTPMintClient.MintQuoteState: use CashuBackend.CheckPayment instead")
}

// CashuBackend wraps a Cashu mint (via NUT-04 quotes) to provide
// Lightning invoice creation and payment checking for the lud25d mint
// SERVICE. k1 is a CSPRNG-generated bearer secret, NOT the Lightning
// preimage — the Lightning payment hash only proves the invoice was
// paid.
type CashuBackend struct {
	db            *DB
	mintURL       string
	mintClient    MintQuoteClient
	httpClient    *http.Client
	expiry        time.Duration
}

// NewCashuBackend creates a CashuBackend that talks to the given Cashu
// mint URL. The expiry duration determines each note's expires_at.
func NewCashuBackend(db *DB, mintURL string, expiry time.Duration) *CashuBackend {
	return &CashuBackend{
		db:         db,
		mintURL:    mintURL,
		mintClient: NewHTTPMintClient(),
		httpClient: &http.Client{Timeout: 30 * time.Second},
		expiry:     expiry,
	}
}

// NewCashuBackendWithClient creates a CashuBackend with a custom
// MintQuoteClient (for testing or alternative backends).
func NewCashuBackendWithClient(db *DB, mintURL string, expiry time.Duration, client MintQuoteClient) *CashuBackend {
	return &CashuBackend{
		db:         db,
		mintURL:    mintURL,
		mintClient: client,
		httpClient: &http.Client{Timeout: 30 * time.Second},
		expiry:     expiry,
	}
}

// CreateMintingInvoice generates a CSPRNG k1 (32-byte bearer secret),
// creates a NUT-04 mint quote at the Cashu mint to get a BOLT11
// invoice, decodes the payment hash from the invoice, and stores the
// k1 → (payment_hash, quote_id) mapping in the database.
//
// k1 is NOT the Lightning preimage — it is an independent bearer
// secret. The Lightning payment hash only proves the invoice was paid.
func (b *CashuBackend) CreateMintingInvoice(amountMsat int64) (k1 string, bolt11 string, paymentHash string, err error) {
	// Generate k1: 32 bytes from crypto/rand (CSPRNG, NOT math/rand)
	k1Bytes := make([]byte, 32)
	if _, err := rand.Read(k1Bytes); err != nil {
		return "", "", "", fmt.Errorf("generate k1: %w", err)
	}
	k1 = hex.EncodeToString(k1Bytes)

	// Create NUT-04 mint quote at the Cashu mint
	amount := uint64(amountMsat)
	quoteResp, err := b.mintClient.RequestMint(amount, b.mintURL)
	if err != nil {
		return "", "", "", fmt.Errorf("request mint quote: %w", err)
	}

	bolt11 = quoteResp.Request

	// Decode the BOLT11 invoice to extract the payment hash
	paymentHash, err = extractPaymentHash(bolt11)
	if err != nil {
		return "", "", "", fmt.Errorf("decode bolt11: %w", err)
	}

	// Store the note in the database
	now := time.Now().Unix()
	note := Note{
		K1:          k1,
		PaymentHash: paymentHash,
		QuoteID:     quoteResp.Quote,
		AmountMsat:  amountMsat,
		Status:      NotePending,
		CreatedAt:   now,
		ExpiresAt:   expiryDurationToUnix(b.expiry),
	}

	if err := b.db.InsertNote(note); err != nil {
		return "", "", "", fmt.Errorf("store note: %w", err)
	}

	return k1, bolt11, paymentHash, nil
}

// CheckPayment polls the Cashu mint for the quote state associated with
// the given k1. If the quote is paid, the note is marked as paid in the
// database. Returns (true, nil) when paid, (false, nil) when still
// pending.
func (b *CashuBackend) CheckPayment(k1 string) (bool, error) {
	note, err := b.db.GetNote(k1)
	if err != nil {
		return false, err
	}

	// If already paid, return immediately
	if note.Status == NotePaid || note.Status == NoteSpent {
		return true, nil
	}

	// If expired, return false
	if note.Status == NoteExpired {
		return false, nil
	}

	// Poll the Cashu mint for quote state
	quoteResp, err := b.pollQuoteState(note.QuoteID)
	if err != nil {
		return false, fmt.Errorf("poll quote state: %w", err)
	}

	if quoteResp.State == nut04.Paid || quoteResp.State == nut04.Issued {
		now := time.Now().Unix()
		if err := b.db.MarkPaid(k1, now); err != nil {
			return false, fmt.Errorf("mark note paid: %w", err)
		}
		return true, nil
	}

	return false, nil
}

// pollQuoteState queries the Cashu mint for the current state of a
// NUT-04 mint quote.
func (b *CashuBackend) pollQuoteState(quoteID string) (*nut04.PostMintQuoteBolt11Response, error) {
	url := normalizeURL(b.mintURL) + "/v1/mint/quote/bolt11/" + quoteID

	resp, err := b.httpClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("get quote state: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read quote state: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("mint returned HTTP %d: %s", resp.StatusCode, string(body))
	}

	var quoteResp nut04.PostMintQuoteBolt11Response
	if err := json.Unmarshal(body, &quoteResp); err != nil {
		return nil, fmt.Errorf("unmarshal quote state: %w", err)
	}

	return &quoteResp, nil
}

// extractPaymentHash decodes a BOLT11 invoice string and returns the
// hex-encoded payment hash.
func extractPaymentHash(bolt11 string) (string, error) {
	decoded, err := decodepay.Decodepay(bolt11)
	if err != nil {
		return "", fmt.Errorf("decode bolt11 invoice: %w", err)
	}
	return decoded.PaymentHash, nil
}

// normalizeURL strips trailing slashes from a mint URL.
func normalizeURL(raw string) string {
	for len(raw) > 0 && raw[len(raw)-1] == '/' {
		raw = raw[:len(raw)-1]
	}
	return raw
}
