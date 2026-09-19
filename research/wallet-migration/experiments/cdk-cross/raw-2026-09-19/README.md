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
and, in the two `.gz` logs, a single final blank line. No content, no line of
error text and no number in `FINDINGS-2026-09-19.md` is affected;
`../verify-findings.sh` re-derives every headline number from these committed
files and prints that per-file diff with trailing space stripped.

`21-produced-binary-hashes.txt` records sizes and sha256 of the binaries the runs
produced; the binaries themselves (16-18 MB each, plus a 35 MB `.so`) are *not*
committed — they are build outputs, regenerable from the scripts.
