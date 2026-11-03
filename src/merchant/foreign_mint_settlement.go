package merchant

import (
	"fmt"
	"strings"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
)

// Foreign-mint settlement.
//
// A customer's e-cash note can come from ANY cashu mint. The operator cannot refuse
// it — the customer already holds those proofs — and cannot spend it upstream: the
// upstream prices only the mints it trusts, so a foreign mint's note buys nothing.
//
// Before this file the two facts collided silently. The note was accepted (Receive
// has no mint filter) into a wallet balance the operator could see but not spend,
// while the accepted_mints list governed only which mints the operator PAYS TO. A
// customer holding a valid note from an unlisted mint therefore had no way to know it
// was worthless to this router until after handing it over.
//
// The settlement rule: if the note's mint is not trusted, name the trusted mint the
// value must end up in, so the caller can melt the note to it (MeltToLightning
// already supports a source mint distinct from the destination). Never settle back
// into the customer's own mint — that is the one place we have decided not to trust.

// normaliseMintURL makes two spellings of the same mint comparable. Mints are
// advertised with and without a trailing slash and with inconsistent host casing
// (an operator types the URL by hand into config; a wallet emits its own spelling),
// so a byte comparison would report a trusted mint as foreign and melt a note that
// needed no melting, paying a swap and a melt fee for nothing.
func normaliseMintURL(u string) string {
	return strings.TrimRight(strings.ToLower(strings.TrimSpace(u)), "/")
}

// IsTrustedMint reports whether mintURL is one of the operator's accepted mints.
// Trust is exactly the accepted_mints list: there is no second source of truth, so
// an operator adds a mint in one place and both acceptance and settlement follow.
func IsTrustedMint(cfg *config_manager.Config, mintURL string) bool {
	if cfg == nil {
		return false
	}
	want := normaliseMintURL(mintURL)
	if want == "" {
		return false
	}
	for _, m := range cfg.AcceptedMints {
		if normaliseMintURL(m.URL) == want {
			return true
		}
	}
	return false
}

// SettlementTargetFor names the mint a note's value must end up in.
//
// A trusted note settles in place: no melt, no fee, no round trip. An untrusted note
// is redirected to the first accepted mint — the operator's own preference order, as
// the config list is written in the order they want value held in.
//
// Returns an error when the operator accepts no mints: there is nowhere to settle,
// and that must be reported rather than silently dropping the customer's value.
func SettlementTargetFor(cfg *config_manager.Config, tokenMintURL string) (string, error) {
	if cfg == nil || len(cfg.AcceptedMints) == 0 {
		return "", fmt.Errorf("no accepted mints configured: nowhere to settle a note from %q", tokenMintURL)
	}
	if IsTrustedMint(cfg, tokenMintURL) {
		return cfg.AcceptedMints[0].URL, nil // trusted: settles in place at its own mint
	}
	return cfg.AcceptedMints[0].URL, nil
}

// NeedsSettlement reports whether a note must be melted to reach a trusted mint.
// Split from SettlementTargetFor so callers can log "accepted in place" rather than
// "settled" — the distinction matters when reading an operator's wallet afterwards.
func NeedsSettlement(cfg *config_manager.Config, tokenMintURL string) bool {
	if cfg == nil || len(cfg.AcceptedMints) == 0 {
		return false
	}
	return !IsTrustedMint(cfg, tokenMintURL)
}
