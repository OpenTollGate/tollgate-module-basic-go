- **A Go-native multi-step portal recipe engine** can run `request`,
  `attempts`, `skip_if`/`fail_if`, `form_generic` and `wispr` steps with
  `{{var}}` templating, JSON and header extraction, transport retries and a
  per-host allow-list. It rejects `identity` strategies (no MAC/node-key
  rotation), never auto-runs `paid` recipes, and refuses to auto-fill a page
  that looks like a payment form
  ([#1253](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/1253)).
  The per-uplink watcher can select a built-in recipe with
  `portal_login_recipes[].recipe` (`man-purple-free`, `ask4-guest`); `url` is
  the recipe's `start_url`.
