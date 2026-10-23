- **The ngit trigger's identity check is now a declared trust set.**
  `scripts/ngit-ci-trigger.sh` no longer ships a built-in maintainer pubkey:
  `NGIT_TRUST` declares the identities a run may sign with (64-hex pubkeys,
  npub1… or nsec1… entries; public halves derived, never printed), `MAINT_HEX`
  remains as the legacy single-entry form, and with neither declared it fails
  closed. Identity rotation becomes a declaration change instead of a code
  edit. The pubkey derivation is also portable across nak versions (v0.16.2's
  `key public` rejects nsec-string args, which had broken the check outright),
  the guard suite grows from 9 to 18 subtests, and the local PR gate
  (`scripts/pr-verify.sh`) runs the ngit suites again
  ([#956](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/956)).
