- Add `validate_registration.v1` to the Spilman sidecar: a bounded, versioned
  request that parses the payer's nested `ParamsJSON` with the pinned protocol
  crate and refuses a receiver pubkey that is not the one derived from the
  payee's own secret, a nested mint/unit/capacity/expiry/keyset mismatch against
  the outer record and against the supplied trusted mint URL and keyset, an
  unsupported unit, zero capacity, an expired registration, and a channel id
  that cannot be derived. It returns only values the sidecar derived, carries an
  always-false `mint_funds_asserted` marker, and performs no signing, swap,
  restore or mint call — it does not make settlement work.
