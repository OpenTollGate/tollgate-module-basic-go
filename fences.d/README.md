# fences.d — one file per pre-commit fence

`hooks/pre-commit` is a stable dispatcher: it runs every `fences.d/*.sh`
in lexical order (numeric prefixes keep that order stable) and refuses to
continue the moment one fails. Adding a fence = adding a file here; two
lanes can never conflict, because no lane edits a file another lane owns
(#920).

Until #920 every fence-adding lane concatenated its block into the one
shared script, and a merge on main ate an entire `fi` between two fences
(`bash -n hooks/pre-commit` failed on main with "unexpected end of file",
found 2026-10-14) — every commit through the hook was refused from that
moment on. One fence per file makes that shape unrepresentable: the
dispatcher parses each fragment standalone with `bash -n` before running
it, so a fence missing its `fi` is refused at the commit that added it.

## The fragment contract

- Name: `NN-<short-slug>.sh` — two digits fix the run order (cheap checks
  first; the prefix is ordering, not identity).
- Body: one COMPLETE `if <staged-file trigger>; then <check>; fi` block —
  it must pass `bash -n` on its own, with no state from other fragments.
- The trigger gates on `git diff --cached --name-only` (the hook runs from
  the repo root); the body runs one check and lets its exit status speak.
- No shebang, no `set` — the dispatcher (`hooks/pre-commit`) owns both.

## Fencing the fence

`tests/contract/fences-dispatch_test.sh` plants the drift shapes — a
fragment that does not parse, a failing fence, a lost fences.d/ — in
fixture repositories and requires the hook to refuse each one, then
commits through a clean fixture to prove the happy path. It runs in CI
(both lanes) and, naturally, as `fences.d/98-fences-selftest.sh`.
