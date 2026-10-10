- **Desk runs get a fixed, sanitized record format.** User-shaped hardware
  tests (real router, real install/upgrade, real payment) are now recorded
  under `tests/desk-runs/`: every run declares its device and environment
  (never an SSID, passphrase, key, or token string), reports PASS/FAIL/NIT
  with evidence, and links each nit to a filed issue; `new-run.sh`
  scaffolds the record, `file-nit.sh` opens issues carrying the run's
  environment declaration, and `scripts/desk/router-ssh.py`/`router-scp.py`
  give desk operators non-interactive device access. Entry #1 records the
  GL.iNet MT3000 alpha4→rc1 run: the happy path passes end to end, with
  two defects filed from it (#817, #818)
  ([#823](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/823)).
