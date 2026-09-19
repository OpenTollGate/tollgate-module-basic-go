# raw logs — T5c re-verification (2026-09-19)

Unedited output of the commands listed in `../FINDINGS-2026-09-19.md` §9. One file
per measurement; the numbered names follow the order they were produced, and the
`14-`/`17-` pair are the service-level link tests (the deployment artefact) with
their tag-free baselines.

Two exceptions to "unedited", both disclosed rather than silent:

- `14-service-link-mipsel-musl.txt.gz` and `mipsel-musl-link.txt.gz` are the full
  logs, gzipped only because they exceed the repo's 500 KB
  `check-added-large-files` hook limit (609 KB each). `zcat` them; nothing was
  removed. Regenerable with `../run-service-link.sh` and `../run-adapter-cross.sh`.
- the two sha256 digests inside `12-cdkffi-aarch64-musl-build.txt` are listed as
  reviewed false positives in the repo's `.secrets.baseline` (they are hashes of
  build artefacts, not secrets). The log itself is byte-identical to the run.

One formatting caveat worth stating: this repo's pre-commit hooks
(`trailing-whitespace`, `end-of-file-fixer`) rewrite *staged* files, so in five
committed files the bytes differ from the live run output by trailing whitespace
and, in the two `.gz` logs, a single final blank line. That is why the committed
`14-service-link-aarch64-musl.txt` has 145 lines where its own summary log records
146, and the `.gz` decompresses to 2077 where the summary records 2078 — the
missing line is the blank one, so no symbol count, error line or number in
`FINDINGS-2026-09-19.md` is affected.

`../verify-findings.sh` re-derives every headline number from these committed files
and fails closed (non-zero) on any empty derivation. Its output is committed as
`23-verify-findings.txt` (`VERDICT: PASS`). **The first version of that script was
non-runnable** — it grepped bare filenames from the directory above the logs, so
every number printed empty while the script exited 0. The fix (and the fact that
the earlier claim "ran it: all match" was reading a blank page) is recorded in
`FINDINGS-2026-09-19.md` §10.1 and in the commit history.

`24-ci-matrix-rows.txt` is the CI package-matrix row count, derived from
`.github/workflows/build-package.yml` by `../count-ci-matrix.py` (workflow sha256
recorded in the log). It exists because the committed `16-ci-cgo-and-arches.txt`
is a *truncated* excerpt of that matrix: the earlier "17 rows" figure could not be
re-derived from it, and the measurement says 14.

`21-produced-binary-hashes.txt` records sizes and sha256 of the binaries the runs
produced; the binaries themselves (16-18 MB each, plus a 35 MB `.so`) are *not*
committed — they are build outputs, regenerable from the scripts.
