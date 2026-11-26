- **A paid FIPS exit now checks who is buying, not just which npub was pasted.**
  `fips_exit.Claim` carries a BIP340 schnorr signature over the exit's own
  `Challenge` terms, so a caller must prove possession of the buyer key
  (`ErrClaimSignature`) *and* arrive from the tunnel whose observed source that
  key derives to (`ErrClaimUnbound`); a proof cannot be replayable
  (`NonceCache`, `ErrNonceReused`) or moved to cheaper terms, because every
  term is inside the domain-separated canonical bytes. Pure Go, no cgo, no I/O:
  nothing is wired to payment, nftables or the admission ledger yet, and the
  nonce cache is in-memory only, so the 15-minute challenge TTL is the real
  replay bound. ([#1266](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/1266))
