package merchant

import (
	"fmt"
	"strings"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/merchant/lnurlcash"
)

// LNURLCashGate is the merchant half of the opt-in LNURLcash payment method.
//
// It exists so the redeemer package owns the issuer protocol and this package
// owns everything that is already true about money here: which mint invoices
// come from, how a quote is polled, and how access is granted. Duplicating any
// of that in the redeemer would fork the fund-safety rules.
//
// The one thing this adapter must never do is decide that value arrived. It
// reports what the mint says; the redeemer grants only on that.
type LNURLCashGate struct {
	m *Merchant

	// Test seams. Nil in production, where the real merchant paths are used.
	settlementMintForTest func() (string, error)
	statusForTest         func(quoteID, macAddress string) (*LightningQuoteStatus, error)
	requestInvoiceForTest func(macAddress, mintURL string, amountSats uint64) (string, string, error)
}

// NewLNURLCashGate wraps a merchant as the redeemer's settlement gate.
func NewLNURLCashGate(m *Merchant) *LNURLCashGate { return &LNURLCashGate{m: m} }

// RequestRedeemInvoice asks the settlement mint for a NUT-04 quote and returns
// the quote id plus the BOLT11 the issuer is asked to pay.
//
// It reuses RequestLightningInvoice deliberately: that path already
// canonicalises the mint URL (#375), persists the quote so a restart can
// reconcile rather than re-invoice, and applies the same MAC attribution the
// rest of the money path uses. The portal's existing `GET /ln-invoice?quote=`
// poll then works unchanged.
func (g *LNURLCashGate) RequestRedeemInvoice(macAddress, mintURL string, amountSats uint64) (string, string, error) {
	if g.requestInvoiceForTest != nil {
		return g.requestInvoiceForTest(macAddress, mintURL, amountSats)
	}
	if g.m == nil {
		return "", "", fmt.Errorf("lnurlcash: no merchant")
	}
	inv, err := g.m.RequestLightningInvoice(macAddress, mintURL, amountSats)
	if err != nil {
		return "", "", err
	}
	if inv == nil || inv.QuoteID == "" || inv.Invoice == "" {
		return "", "", fmt.Errorf("lnurlcash: settlement mint returned no usable invoice")
	}
	return inv.QuoteID, inv.Invoice, nil
}

// RedemptionSettled reports whether the mint has been paid for this quote and
// whether the access grant has been applied.
//
// GetLightningInvoiceStatus is the authoritative source: it polls the mint's
// quote state and, on PAID/ISSUED, runs the existing grant path
// (ensureLightningAccessGranted) and re-reads the record. So `paid` and
// `granted` are two separate reads of one consistent answer, not two guesses.
//
// An error means the mint did not answer. The redeemer treats that as
// ambiguity; it must never be flattened into "not paid", because the issuer may
// already have melted the note.
func (g *LNURLCashGate) RedemptionSettled(quoteID, macAddress string) (bool, bool, error) {
	var status *LightningQuoteStatus
	var err error
	if g.statusForTest != nil {
		status, err = g.statusForTest(quoteID, macAddress)
	} else {
		if g.m == nil {
			return false, false, fmt.Errorf("lnurlcash: no merchant")
		}
		status, err = g.m.GetLightningInvoiceStatus(quoteID, macAddress)
	}
	if err != nil {
		return false, false, err
	}
	if status == nil {
		return false, false, fmt.Errorf("lnurlcash: no status for quote %s", quoteID)
	}
	paid := status.State == "PAID" || status.State == "ISSUED"
	return paid, paid && status.AccessGranted, nil
}

// SettlementMint names the mint invoices are requested from.
func (g *LNURLCashGate) SettlementMint() (string, error) {
	if g.settlementMintForTest != nil {
		return g.settlementMintForTest()
	}
	if g.m == nil || g.m.configManager == nil {
		return "", fmt.Errorf("lnurlcash: no configuration")
	}
	return firstAcceptedMint(g.m.configManager.GetConfig().AcceptedMints)
}

// firstAcceptedMint applies the settlement-target rule: the first entry in
// `accepted_mints`. That is the same rule the upstream settlement path uses
// (#1011), so an operator's config order expresses one preference everywhere
// rather than two that can disagree.
//
// A blank URL is refused rather than returned: an empty settlement target would
// produce an invoice request against nothing, and the caller would read the
// failure as an issuer problem.
func firstAcceptedMint(mints []config_manager.MintConfig) (string, error) {
	for _, m := range mints {
		if url := strings.TrimSpace(m.URL); url != "" {
			return url, nil
		}
	}
	return "", fmt.Errorf("lnurlcash: no accepted mints configured: nowhere to settle a note")
}

// lnurlcashConfigFrom translates the operator's config section into the
// redeemer's own Config.
//
// Missing bounds fall back to the shipped defaults for the same reason the
// loader does: a section written by hand with only `enabled` must not run with a
// zero timeout and a zero note cap. Enabled is passed through untouched — only
// the file can turn this on.
func lnurlcashConfigFrom(c config_manager.LNURLCashConfig) lnurlcash.Config {
	out := lnurlcash.Config{
		Enabled:                  c.Enabled,
		MaxNoteSats:              c.MaxNoteSats,
		RedemptionTimeoutSeconds: c.RedemptionTimeoutSeconds,
		RetentionSeconds:         c.RetentionSeconds,
	}
	d := lnurlcash.DefaultConfig()
	if out.MaxNoteSats == 0 {
		out.MaxNoteSats = d.MaxNoteSats
	}
	if out.RedemptionTimeoutSeconds <= 0 {
		out.RedemptionTimeoutSeconds = d.RedemptionTimeoutSeconds
	}
	if out.RetentionSeconds <= 0 {
		out.RetentionSeconds = d.RetentionSeconds
	}
	return out
}
