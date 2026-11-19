- **`hooks/pre-commit` scans staged changes with gitleaks** before the
  contract checks run — optional unless `GITLEAKS_REQUIRED=1` fails closed,
  pinned to CI's `v8.30.1`, `--staged --redact`; a secret is cheap to remove
  before the commit and expensive after push (port of the archived fork's
  #107 intent)
  ([#1248](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/1248))
