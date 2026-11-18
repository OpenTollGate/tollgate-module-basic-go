package merchant

import (
	"fmt"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/tollwallet"
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

// normaliseMintURL makes two spellings of the same mint comparable.
//
// It delegates to tollwallet rather than comparing bytes locally. A hand-typed
// config URL and a wallet's own spelling differ in ways a naive comparison gets
// wrong — and getting it wrong is expensive in both directions: report a trusted
// mint as foreign and you melt a note that needed no melting, paying a swap and a
// melt fee for nothing; report a foreign mint as trusted and you accept value you
// have not vouched for.
//
// The rules live in tollwallet.NormalizeMintURL (see its doc comment for why path
// case is preserved: "/Bitcoin" and "/Liquid" are different units on one mint,
// issue #375). This package used to carry a byte-level copy — TrimSpace, ToLower
// on the WHOLE URL including the path, TrimRight("/") — which disagreed with
// tollwallet in two ways that both show up on real configs:
//
//	accepted "https://mint.example:443/Bitcoin", note "https://mint.example/Bitcoin"
//	    -> false   (explicit default port treated as a different mint)
//	accepted "https://mint.example/Bitcoin", note "https://mint.example/bitcoin"
//	    -> true    (path case folded, so a different unit would have been trusted)
//
// merchant already normalises through tollwallet on the lightning path
// (lightning.go), so the local copy was also an inconsistency inside one package.
func normaliseMintURL(u string) string {
	return tollwallet.NormalizeMintURL(u)
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
		// Settles in place at its own mint, but return the CONFIG spelling rather
		// than the caller's raw string: the returned URL is used as a mint identity
		// downstream, and handing back a trailing-slash or case variant creates a
		// second spelling of one mint — the drift AGENTS.md forbids. The caller's
		// note can arrive spelled any way; the operator's config is the declaration.
		for _, m := range cfg.AcceptedMints {
			if normaliseMintURL(m.URL) == normaliseMintURL(tokenMintURL) {
				return m.URL, nil
			}
		}
		return tokenMintURL, nil
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
