package lightning

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// LNURLPayResponse represents the response from the LNURL-pay service
type LNURLPayResponse struct {
	Callback    string `json:"callback"`
	MaxSendable int64  `json:"maxSendable"` // millisatoshis
	MinSendable int64  `json:"minSendable"` // millisatoshis
	Metadata    string `json:"metadata"`
}

// LNURLInvoiceResponse is the response containing the invoice
type LNURLInvoiceResponse struct {
	PR            string        `json:"pr"` // Payment request (invoice)
	SuccessAction interface{}   `json:"successAction,omitempty"`
	Routes        []interface{} `json:"routes,omitempty"`
}

// GetInvoiceFromLightningAddress requests an invoice from a Lightning Address for a specific amount
func GetInvoiceFromLightningAddress(lightningAddr string, amountSats uint64) (string, error) {
	// 1. Parse the Lightning Address (user@domain.com)
	parts := strings.Split(lightningAddr, "@")
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid Lightning Address format (expected user@domain.com): %s", lightningAddr)
	}
	username := parts[0]
	domain := parts[1]

	// 2. Form the well-known URL for Lightning Address
	wellKnownURL := fmt.Sprintf("https://%s/.well-known/lnurlp/%s", domain, username)

	// 3. Make initial request to the Lightning Address service
	resp, err := http.Get(wellKnownURL)
	if err != nil {
		return "", fmt.Errorf("failed to make request to Lightning Address service: %w", err)
	}
	defer resp.Body.Close()

	// 4. Parse the LNURL response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read Lightning Address response: %w", err)
	}

	var lnurlPayResp LNURLPayResponse
	if err := json.Unmarshal(body, &lnurlPayResp); err != nil {
		return "", fmt.Errorf("failed to parse Lightning Address response: %w", err)
	}

	// 5. Check if amount is within allowed range
	amountMsat := int64(amountSats * 1000) // Convert to millisatoshis
	if amountMsat > lnurlPayResp.MaxSendable || amountMsat < lnurlPayResp.MinSendable {
		return "", fmt.Errorf("amount %d sats is outside allowed range (%d-%d msats)",
			amountSats, lnurlPayResp.MinSendable, lnurlPayResp.MaxSendable)
	}

	// 6. Request an invoice by calling the callback URL with the amount
	callbackURL, err := url.Parse(lnurlPayResp.Callback)
	if err != nil {
		return "", fmt.Errorf("invalid callback URL: %w", err)
	}

	// Add amount parameter
	q := callbackURL.Query()
	q.Set("amount", strconv.FormatInt(amountMsat, 10))
	callbackURL.RawQuery = q.Encode()

	// Make request to get the invoice
	invoiceResp, err := http.Get(callbackURL.String())
	if err != nil {
		return "", fmt.Errorf("failed to request invoice: %w", err)
	}
	defer invoiceResp.Body.Close()

	// 7. Parse the invoice response
	invoiceBody, err := io.ReadAll(invoiceResp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read invoice response: %w", err)
	}

	var invoice LNURLInvoiceResponse
	if err := json.Unmarshal(invoiceBody, &invoice); err != nil {
		return "", fmt.Errorf("failed to parse invoice response: %w", err)
	}

	// 8. Return the payment request (invoice)
	if invoice.PR == "" {
		return "", fmt.Errorf("received empty invoice from Lightning Address service")
	}

	return invoice.PR, nil
}
