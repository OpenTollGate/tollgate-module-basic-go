- **Credentials doctrine landed with the existing scanner findings honestly
  triaged.** A full-history `gitleaks git --redact` baseline (89 findings) was
  classified group by group — 1 real (a history-only Let's Encrypt TLS key for
  `tollgate.dns4sats.xyz`, flagged for confirm-no-reuse/reissue) and 88 benign
  fixtures/placeholders/test vectors, each silenced by the narrowest scope
  that kills it: commit-scoped where the verdict is history-only (the path
  stays armed), value-scoped `paths`+`regexes` where a live file carries a
  known-benign value, in a `.gitleaks.toml` that EXTENDS the default ruleset.
  `hooks/pre-commit`
  now runs `scripts/secret-scan.sh` (gitleaks over the staged diff, fail-closed,
  loud-skip when the tool is absent), `.gitignore` covers the whole `.env.*` /
  `*env-backup` class with `.example`/`.template` carve-outs, the unenforced
  Yelp detect-secrets `.secrets.baseline` is retired in favor of gitleaks in
  both hook lanes, and AGENTS.md gains the three-legal-homes credentials
  doctrine (companion: OpenTollGate/physical-router-test-automation#199)
  ([#764](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/764)).
