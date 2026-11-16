## Rebased onto current `main` — the branch was unlandable, not wrong

This branch had drifted 86 commits behind `main` and reported `mergeable: false /
state: dirty`, so none of it could land. It is now rebased onto `main` @ `aa677c00`
(head `75657006`, 17 commits, 44 files, `mergeable: MERGEABLE`).

### Conflicts: four files, all additive

- `.gitignore` — ours ignores the machine-local `go.work`, `main` ignores
  `.portal-tree/`. Kept both.
- `src/config_manager/config_manager_config.go` — ours adds the unit/step_size
  mismatch warning, `main` adds the opt-out Cobrador defaults. Kept both, and
  restored the closing brace the conflict region had swallowed (the merge
  produced a syntax error until fixed).
- `src/cli/server.go` — our side of the conflict was empty; `main` added imports
  that were already in the block. Dropped the duplicates.
- `src/cmd/tollgate-cli/main.go` — union: `wallet` keeps `issue` (ours),
  `private` keeps `privateSetAddress` (main).

### One real drift breakage, fixed

`notice_boundary_test.go`'s mock predates `MerchantInterface` growing
`IssueOnRequest`/`DefaultIssuanceMint`, so the root module no longer compiled at
all. Added the two methods to the mock.

### Verification

`scripts/go-test.sh` (gofmt + vet + build + race, every module): **RC=0, 20
modules ok, 0 failures.**

One transient worth recording honestly: `TestTokenFingerprintIsStableAndDistinctInTheLog`
reported a data race on one full-package run. I classified it rather than assuming
it was mine — it passes on pristine `main` under the same full-package conditions,
passes standalone on this branch, and passes on two consecutive full-package
re-runs here. The test file is untouched by this branch; the race is between the
test's `bytes.Buffer` log sink and the upstream-detector goroutines that logrus
writes from. This branch's added tests shift scheduling enough to expose it. That
is a pre-existing harness flake, and it deserves its own issue rather than being
buried in this PR.

### Where this sits against the two goals

**B (custom units) — this PR is the vehicle.** Already carries `Token.Unit()`,
unit-aware `calculateAllotmentForUnit` (unit mismatch refuses rather than
guesses), `cdk_wallet` `CurrencyUnitCustom`, `Merchant.IssueOnRequest`, and the
`wallet issue` command on both CLI and daemon socket.

**C (Pecan teller flow) — not yet.** `IssueOnRequest` requests a mint quote,
**waits for it to be paid**, then mints. There is no ticket, no quote matching and
no console, so a Pecan-style teller cannot drive it:

- Pecan's contract is a **NUT-20 locked** quote that the *wallet* creates and the
  teller matches by short code. `IssueOnRequest` inverts that: the router creates
  the quote and blocks on payment.
- Pecan needs `POST /v1/mint/quote/branch` to reach the mint's payment processor.
  Nothing here implements that method.

So C is a follow-up: either implement the `branch` payment method in Go so a real
Pecan console can attach, or build the minimal Go teller the earlier trade study
recommended. I have not started either.

### Still unproven (unchanged from the original body)

Wallet redemption of a non-sat unit **on a real router**, and hardening of the
settle endpoint. The bench evidence in the original description stands; it predates
this rebase and has not been re-measured.
