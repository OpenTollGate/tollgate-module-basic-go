- **Lanes add files now, not hunks: the four conflict-hotspot anchors are
  partitioned (#920).** During the 2026-10-11→13 queue campaign ~20 PR
  rebases fought the same four files, and the shared pre-commit hook on main
  had been silently broken by a merge that ate an entire `fi` between two
  fences — every hook-driven commit was refused from that point on. Three
  organs are now fragment-backed, generator-written and CI-fenced:
  `hooks/pre-commit` is a stable dispatcher over `fences.d/NN-<slug>.sh`
  (one complete `if/fi` block per fence, parsed standalone, so the eaten-`fi`
  shape is refused at the commit that adds it); the Test workflow twins
  (`.github/workflows/test.yml` and its `.ngit/act` copy) are rendered from
  `ci.d/{both,github,ngit}/` job fragments by `scripts/assemble-ci.py`,
  whose `--check` (CI, both lanes) fails any hand-edit or
  fragment-changed-without-render, and which refuses per-lane variants of
  one job id whose matrix diverges; and `AGENTS.md` is a stable core plus
  `docs/agents/<topic>.md` files behind a fenced index (every topic listed,
  every listing real, every entry an explicit load directive). Each organ
  ships planted-drift verdicts — `tests/contract/fences-dispatch_test.sh`
  plants the exact missing-`fi` shape that broke main and requires the
  refusal
  ([#920](https://github.com/OpenTollGate/tollgate-module-basic-go/issues/920)).
