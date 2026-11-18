# Seal vetting — evaluation code

Reproducible evaluation of the **seal** construction proposed for TollGate in
[PR #944](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/944)
(comment `#issuecomment-6185842750`), from
[`lnurlcash/lnurl-wallet`](https://github.com/lnurlcash/lnurl-wallet).

The written analysis is posted on that PR as
[`#issuecomment-6205571043`](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/944#issuecomment-6205571043).
**This directory is the code behind it**, so the claims can be re-run rather than
taken on trust.

---

## Run it

```bash
bash run-seal-vetting.sh            # workdir defaults to ~/seal-vetting-work
```

It clones upstream at a **pinned revision**, installs dependencies, drops the
adversarial test into the project's own test tree, and runs it with the project's
own `vitest`. A fresh clone reproduces the findings; output is written to `out/`.

Nothing is pushed to upstream and nothing is modified there — the script writes
only inside its own scratch directory.

---

## Why this is run against upstream's own harness

We could have reimplemented `sealChainProblem` in a scratch file and shown it
failing. That would prove nothing: it would only show that *our* copy of the
function validates a fabricated chain.

Instead the test is installed into upstream's `src/`, so it exercises **their
modules, their transforms, their test runner**. If someone fixes these findings
upstream, re-running this against a newer pin will disagree — for the right
reason.

---

## What the test demonstrates

`seal_adversarial.test.ts` — 3 tests, all passing against the pinned revision.

### 1. A seal can be fabricated with no mint, no note and no counterparty

`sealChainProblem` consumes no signature, no key and no certificate. So a party
with nothing at all invents a genesis, chains it to any depth, names the victim as
owner — and validation returns `''` (valid).

Demonstrated at 5 states, and again at **500 states** to show depth is no
constraint.

### 2. Every PREFIX of a consignment is also valid

The chain is append-only and validation never asks *"is this state the current
one?"*. So each prefix is a valid consignment with a **distinct** terminal hash,
all naming the same owner — 6 states → 6 distinct valid terminal hashes.

This is why the replay-keying choice matters and is **undocumented** in the
source: keying on **genesis/`assetId`** collapses the prefixes to one identity
(safe); keying on the **terminal state hash** makes each prefix a separate spend.

---

## How it relates to the type declarations

The test also documents the root cause, which is **one field wide**:

- `rotateNoteWithHash` returns `{signature}` (`lib/request.ts:467–479`)
- `seal.transition` receives it (`verbs.ts:443–447`) **and discards it**
- `encodeSealConsignment` casts a type that *declares* `signature` /
  `groupPubkeyHex` (`manifest.ts:49–54`) but reads only two fields
  (`seals.ts:414`)
- `verifyNoteSignatureForKey` already exists (`lib/signature.ts:186`) and is
  already used to verify a mint certificate for `Q` in `note.lockToPubkey`
  (`verbs.ts:214–223`)

So the certificate needed to close the CRITICAL class is **already flowing through
the code and simply being dropped**.

---

## Files

| File | Purpose |
|---|---|
| `run-seal-vetting.sh` | Clone at the pin, install, run the test, capture output |
| `seal_adversarial.test.ts` | The 3 adversarial tests |
| `PRELIMINARY-READING.md` | My reading **before** the consultant results, so my own errors are visible |
| `out/GREEN.txt` | Expected passing output |
| `out/run-*.txt` | Actual output from each run |

---

## Scope and honesty

- **Pinned revision.** Conclusions are about `f1cdb9e5` specifically. The seal is
  under active development; a newer revision may differ.
- **These are design gaps, not concealment.** Several correspond to limitations
  the author states himself in the file's own design comment (seals.ts:48–52,
  64–70; manifest.ts:597). The gap is engineering.
- **`PRELIMINARY-READING.md` contains a partially wrong hypothesis**, retained
  deliberately. We first suspected the redemption-time mint binding was also
  absent; it is present and correctly implemented, and only constrains the wrong
  direction. The correction is in the PR comment.
- **This evaluates fit and soundness for our use case**, not the project's value
  in general. The design does a number of things well and the PR comment says so.
