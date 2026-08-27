# Security Policy & Incident Log

## Reporting

Report vulnerabilities privately to the maintainers (see README for
contact). Do **not** open public issues containing secret material.

## 2026-08-26 — Router deployment backup committed to `main` (PURGED)

**Status: purged from history on 2026-08-27. Merchant identity key
rotation required — tracking issue
[#364](https://github.com/OpenTollGate/tollgate-module-basic-go/issues/364).**

### What happened

A deployment backup directory, `deploy-backup-20260730/`, was
accidentally committed to `main` as part of PR
[#358](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/358)
(commit `e7161ab`, 2026-08-26). Found by the Amperstrand security
audit 2026-08-26 (task T7).

### What was exposed (no secret material reproduced here)

- `deploy-backup-20260730/backup-info.txt` — deployment config dump
  including the **merchant private identity key** (line 118) and other
  key/seed material markers.
- `deploy-backup-20260730/tollgate-backup.tar.gz` — router filesystem
  backup containing `etc/tollgate/wallet.db` (ecash wallet) and
  `etc/tollgate/tokens-to-recover.txt` (spendable Cashu token list).
- `deploy-backup-20260730/tollgate-wrt-v0.5.0-backup` — router binary
  image.

### Remediation (2026-08-27)

- `main` history rewritten (`git filter-branch --index-filter` on path
  `deploy-backup-20260730/`) and force-pushed; the legitimate #358/#359
  changes are preserved as rewritten commits `5e904a1` / `1e70bca`.
- No remote branch or tag other than `main` carried the directory
  (verified via `git ls-remote`); `net4sats/tollgate-module-basic-go`
  was hard-reset separately by Amperstrand.

### Residual exposure

- GitHub PR refs `refs/pull/358/*` still reference the original commit
  `e7161ab` until GitHub Support garbage-collects unreachable objects;
  cached/raw views of that commit may persist in the meantime.
- Forks that fetched during the exposure window still carry the files
  until they rewrite the affected branches.
- Clones made during the window: re-clone, or remove stale refs and run
  `git gc --prune=now`.

### Required follow-ups

- Rotate the merchant identity key (it must be treated as BURNED) —
  [#364](https://github.com/OpenTollGate/tollgate-module-basic-go/issues/364).
- Sweep or write off the ~285 sats held on
  `nofee.testnut.cashu.space` (test mint, trivial amount).
- Rotate any API keys or endpoints present in the config dump at
  operator discretion.
