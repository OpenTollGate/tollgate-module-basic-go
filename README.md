# research/issue-briefs — research branch, never merge

This branch is a side channel for AI-agent context, not part of the
product history. It has no ancestry in common with any product branch
(orphan root) and is never intended to be merged.

Contents: `issue-briefs/` — one research brief per open GitHub issue
(102 as of 2026-10-11/12), each with a summary, verified file:line
anchors (checkout `fdeff2d1`), ranked testable hypotheses,
verification commands, prior-art links (zread + web), an actionable
checklist with acceptance criteria, and applicable fund-safety
invariants. Start at `issue-briefs/INDEX.md`.

Consume read-only:
    git fetch origin research/issue-briefs
    git show research/issue-briefs:issue-briefs/INDEX.md

Anchors were verified against `release/rc1-gate-verdict` @ `fdeff2d1`;
re-verify line numbers after further commits.
