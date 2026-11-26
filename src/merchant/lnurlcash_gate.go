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
	// provider returns the CURRENT merchant. The merchant value is swapped at
	// runtime (a reachable-mint change rebuilds it, see swapMerchant), so a
	// redeemer holding only a concrete pointer would keep settling against a
	// merchant that has been replaced. When both are set, provider wins.
	provider func() *Merchant
	// accepted reads the accepted-mint list per call, so a config reload is
	// honoured without rebuilding the redeemer.
	accepted func() []config_manager.MintConfig

	// Test seams. Nil in production, where the real merchant paths are used.
	settlementMintForTest func() (string, error)
	statusForTest         func(quoteID, macAddress string) (*LightningQuoteStatus, error)
	requestInvoiceForTest func(macAddress, mintURL string, amountSats uint64) (string, string, error)
}

// NewLNURLCashGate wraps a merchant as the redeemer's settlement gate.
func NewLNURLCashGate(m *Merchant) *LNURLCashGate { return &LNURLCashGate{m: m} }

// NewLNURLCashGateLive is the production constructor: it resolves the merchant
// at each call, and reads the accepted mints from the live configuration.
func NewLNURLCashGateLive(provider func() *Merchant, accepted func() []config_manager.MintConfig) *LNURLCashGate {
	return &LNURLCashGate{provider: provider, accepted: accepted}
}

// merchant resolves the merchant this call must settle through.
func (g *LNURLCashGate) merchant() *Merchant {
	if g.provider != nil {
		return g.provider()
	}
	return g.m
}

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
	mm := g.merchant()
	if mm == nil {
		return "", "", fmt.Errorf("lnurlcash: no merchant")
	}
	inv, err := mm.RequestLightningInvoice(macAddress, mintURL, amountSats)
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
		mm := g.merchant()
		if mm == nil {
			return false, false, fmt.Errorf("lnurlcash: no merchant")
		}
		status, err = mm.GetLightningInvoiceStatus(quoteID, macAddress)
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
	if g.accepted != nil {
		return firstAcceptedMint(g.accepted())
	}
	mm := g.merchant()
	if mm == nil || mm.configManager == nil {
		return "", fmt.Errorf("lnurlcash: no configuration")
	}
	return firstAcceptedMint(mm.configManager.GetConfig().AcceptedMints)
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

// WalletDataDir is the directory the wallet's durable files live in: next to the
// config, or in its `wallet/` subdirectory when that is a mountpoint. It is
// exported so the LNURLcash journal lands on the same filesystem as quotes.json
// and wallet.db — same durability assumptions, same backup surface — without a
// second copy of the mountpoint rule (the one-declaration-per-fact rule).
func WalletDataDir(configManager *config_manager.ConfigManager) string {
	if configManager == nil {
		return ""
	}
	return walletDirFor(configManager)
}

// LNURLCashConfigFrom translates the operator's config section into the
// redeemer's own Config.
//
// Missing bounds fall back to the shipped defaults for the same reason the
// loader does: a section written by hand with only `enabled` must not run with a
// zero timeout and a zero note cap. Enabled is passed through untouched — only
// the file can turn this on.
func LNURLCashConfigFrom(c config_manager.LNURLCashConfig) lnurlcash.Config {
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
