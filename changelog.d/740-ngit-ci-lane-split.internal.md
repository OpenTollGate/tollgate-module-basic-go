- **The ngit CI test lane is split and its two silent jobs are fixed.** The
  coordinator-executed `.ngit/act/workflows/test.yml` was the heaviest push
  invocation on the runner (16 jobs vs 1–6 per sibling, 2-concurrent-job cap,
  1800 s ceiling) — the lane #520 documents being dropped or concluded
  `failure` with every published job green — and two of its jobs never
  published a result at all: `release-check-fast` was skipped by a job-level
  `if:` (16 declared vs 14 published on every run), and `hygiene`'s `git grep`
  was vacuously green on a checkout with no git metadata. The gate legs
  (`packaging-suites`, `hygiene`, `release-check-fast`) now live in
  `test-gates.yml` on the same triggers — same checks, two invocations that
  fit the budget — with the ref gate moved inside the step and the marker scan
  rewritten to `grep -rEn` with explicit exit-code handling;
  `tests/ngit-act-lanes_test.sh` pins the split and the two failure classes,
  and `.ngit/README.md` replaces its stale "byte-identical twins" claim with
  the real port contract
  ([#740](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/740)).
