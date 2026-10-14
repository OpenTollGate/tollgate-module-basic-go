# Changelog Fragments — Architecture Decision

> **Status: Proposed (2026-10-06).** This record proposes that changelog entries
> travel as one file per pull request under `changelog.d/` and are folded into
> `CHANGELOG.md` by `scripts/changelog-assemble.py`. Acceptance is a maintainer
> action; the drafting account may not accept its own proposal. Until then the
> legacy path (a `CHANGELOG.md` hunk in the PR) stays accepted.

## Context

Every pull request that changes operator-visible behaviour appends one bullet to
`CHANGELOG.md` under the current release heading. That is a single shared anchor:
whichever PRs are open at the same time all insert at the same place, so the
merge of the second one is a conflict — always, not occasionally.

The cost is measurable, and it has been paid:

* the 0.6.0 train resolved roughly a dozen `CHANGELOG.md` conflicts by hand;
* in one rebasing session a keep-both-sides (`--ours`/`--theirs`) resolution
  resurrected an entry a branch had already moved, in **#524 and #531**, and a
  post-rebase audit was the only thing that caught it (the duplicate lint in
  **#581** exists because of this);
* on 2026-10-06, **15 open PRs carried a `CHANGELOG.md` hunk at once**, so the
  maintainer's merge order was also a changelog-editing order;
* the workaround in use — rebase each PR on the current anchor, resolve, merge,
  repeat — is correct but is labour that scales with the number of open PRs
  (#581's review thread reached the same conclusion: the adjacency conflicts are
  structural to the single anchor, and rebase-then-merge only mitigates them).

The conflict is not caused by anyone editing badly. It is caused by `CHANGELOG.md`
being both the *record* and the *staging area for the next release*.

## Decision

Split the two roles.

1. **A change's entry is a fragment**: `changelog.d/<pr-number>-<slug>.<type>.md`,
   `<type>` naming the changelog subsection (`added`, `changed`, `internal`,
   `deprecated`, `fixed`, `removed`, `security`). The body *is* the bullet,
   verbatim, starting with `- `.
2. **A merge never touches `CHANGELOG.md`.** PRs add a new file; new files with
   unique names do not conflict. The in-flight PRs keep their inline hunks until
   they land — the fragment path applies to new work, so nothing has to be
   rewritten for this to start working.
3. **`scripts/changelog-assemble.py` folds them.** `fold` writes one wave of
   subsections immediately below the target release heading — newest PR number
   first, subsections in canonical order — deletes the folded fragments, and
   changes nothing else. `check` validates names and bodies; `fold --dry-run`
   prints the wave and writes nothing. Folding is a maintainer action, in
   batches, before a release.
4. **Nothing else about the changelog changes.** `CHANGELOG.md` stays the human
   record, the release step "the accumulated `[Unreleased]` section becomes
   `## [<version>] - <date>`" is unchanged, and `RELEASE-NOTES.md` is still
   written by hand.

## Alternatives considered

* **`.gitattributes`: `CHANGELOG.md merge=union`.** One line, and no contributor
  changes habit. Rejected: union does not remove the shared anchor, it hides the
  conflict. When two branches resolve the *same* bullet differently — one
  reworded it, the other added a neighbour — union emits both versions and the
  merge is silent, which is exactly the failure the 0.6.0 train already produced
  twice by hand. On a prose file with hand-ordered waves, "both sides' lines, in
  some order" is not a resolution; it is a merge that cannot be reviewed. It is
  also not retroactive insurance: an entry that must move between sections is
  still a hand edit on `CHANGELOG.md`.
* **Generate the changelog from conventional-commit subjects.** Conflict-free by
  construction, and rejected on quality: the entries this project writes are not
  commit subjects. They are paragraphs that say what an operator will experience
  and what the evidence for it was, and that prose is the thing worth keeping.
* **Keep the status quo (rebase the anchor, resolve, merge).** This is the
  measured cost above: every merge of a changelog-touching PR is a hand
  resolution, and every hand resolution can resurrect a moved entry.
* **Make the entry optional for "small" PRs.** Rejected: the ambiguity is the
  problem. A fragment is cheaper to write than the argument about whether it was
  needed.

## Consequences

* Contributors add one small file instead of editing a 3,500-line document; the
  only new rule is the filename.
* The maintainer folds in one step, and a fold is text moving in one direction
  under one anchor — it cannot conflict with anything, because fragments are
  deleted as they are folded.
* The changelog lags the merges until the next fold. That is deliberate: what
  must be current is the *record*, and the record is what the fold writes.
* The duplicate lint from **#581** stays useful and gains scope: an entry can now
  appear both in a folded section and in a stale fragment, so the pair to police
  is "same bold lead-in in two places" whether the second copy is a section or a
  file.
* Enforcement is validation, not prohibition: `hooks/pre-commit` and CI run
  `check` on every fragment (names, types, bodies). The presence rule — a
  user-visible change ships with a fragment or a legacy inline hunk — is a review
  rule during the transition; a hard CI gate can be added once the PRs that were
  open before this mechanism landed are all merged.
