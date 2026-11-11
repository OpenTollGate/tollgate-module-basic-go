- Reproduce fw4's include contract in the backend-firewall parse check: wrap the
  defs fragment (comment-stripped) before the API fragment inside
  `table inet fw4`, so `nft -c` validates the ruleset the router actually
  loads instead of rejecting the `$tg_portal_if`/`$tg_private_if` defines that
  `00-tollgate-defs.nft` provides. Test-only; no shipped rule or packaging
  change. ([#1214](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/1214)).
