# ci.d — one file per CI job, rendered into both Test workflow twins

`.github/workflows/test.yml` and `.ngit/act/workflows/test.yml` are two
rendered copies of one Test workflow (#920). Until this directory existed
they were maintained by hand, and every CI/test lane appended its jobs and
steps to the same two documents — ~20 PR rebases fought them in the
2026-10-11→13 queue campaign alone, and the twin drifted three times in
one session. The changelog proved the cure earlier (#679): filenames are
unique, git merges unique new files without conflict, so **a lane adds a
file**.

## Layout

| directory | meaning |
| --- | --- |
| `ci.d/both/` | a job byte-identical on both lanes, declared once |
| `ci.d/github/` | a GitHub-lane job, or the GitHub variant of a job that deliberately differs per lane |
| `ci.d/ngit/` | the ngit act lane's counterpart of the above |

A fragment is named `NN-<slug>.yml`. Its body is the job's YAML exactly as
it appears indented under `jobs:` — comments included, `${{ ... }}`
expressions included, nothing else. The numeric prefix fixes the job's
position in the rendered workflow (it is ordering, not identity) and is
unique per lane.

## Working on CI

```bash
python3 scripts/assemble-ci.py            # render both twins
python3 scripts/assemble-ci.py --check    # diff committed copies vs fragments
python3 scripts/assemble-ci.py --list     # what renders where
```

- **Adding a job**: add `ci.d/<lane>/NN-<slug>.yml`, run the assembler,
  commit fragment + rendered twins together.
- **Adding a step to a job**: edit that job's fragment, re-render. If the
  step should run on both lanes, it lands in the same fragment on the
  other lane too (for `both/` jobs there is only one file).
- **Never edit a rendered workflow by hand** — CI runs `--check` on both
  lanes and the pre-commit fence refuses a staged hand-edit.

## Per-lane variants

A job that genuinely differs per lane (`go-test`'s fail-fast policy,
`contract-lint`'s and `deps-and-imports`' step sets — each lane runs the
checks it can reach) is one fragment per lane directory carrying the SAME
job id. The variants are twins of each other: **only lane policy may
differ**. `assemble-ci.py` refuses variants whose `strategy.matrix`
diverges — the module list is one fact — and any other shared content
between variants is expected to stay in lockstep by review, the same way
the hand-maintained halves were, except now each half has exactly one
owner file and the diff is reviewable in one place.

## Fencing

`tests/contract/ci-fragments_test.sh` plants every drift shape — a
hand-edit to a rendered copy, a fragment changed without re-rendering, a
duplicate job id, a typo'd lane directory, a malformed fragment, a
diverging variant matrix — in fixture trees and requires the refusal, by
name, then requires a clean tree to pass. It runs in CI (both lanes,
`deps-and-imports`) and as the `fences.d/96-ci-fragments.sh` fence.
