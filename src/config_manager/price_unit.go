package config_manager

import "strings"

// CanonicalSatoshiUnit is the unit spelling the Cashu protocol uses. NUT-01
// and NUT-04 name the satoshi unit "sat"; keyset ids, mint responses and token
// units are all expressed with that string.
const CanonicalSatoshiUnit = "sat"

// CanonicalPriceUnit normalises a configured price unit to the spelling the
// protocol uses for it.
//
// Why this exists: `price_unit` is operator/consumer-written, and both "sat"
// and "sats" have been written into `/etc/tollgate/config.json`. The module
// used to advertise whatever it found verbatim in the kind:10021
// `price_per_step` tag — `["price_per_step","cashu","10","sats","<mint>","1"]`.
// A consumer compares that advertised unit against the unit carried inside the
// customer's e-cash token, which is always `"sat"`. A literal comparison then
// rejects perfectly good money: the captive portal refuses a freshly-minted
// token and reports it as "the mint has rotated its keys", so the customer is
// told to obtain a new token that will fail in exactly the same way.
//
// Normalising at the boundary keeps the advertisement speaking the protocol's
// vocabulary no matter what the config file says, and leaves other units
// (a custom-unit mint, for example) untouched apart from case and surrounding
// whitespace.
func CanonicalPriceUnit(unit string) string {
	switch strings.ToLower(strings.TrimSpace(unit)) {
	case "", "sat", "sats", "satoshi", "satoshis":
		return CanonicalSatoshiUnit
	default:
		return strings.ToLower(strings.TrimSpace(unit))
	}
}
