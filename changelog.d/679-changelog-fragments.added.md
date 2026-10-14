- **Changelog entries no longer conflict on merge: a change's entry is its own
  file.** Every PR used to append its bullet to the same anchor of
  `CHANGELOG.md`, so the second of two open PRs always conflicted there — about a
  dozen hand-resolved hunks on the 0.6.0 train, two of which resurrected an entry
  a branch had already moved past a keep-both-sides resolution (#524, #531). An
  entry is now a fragment, `changelog.d/<pr>-<slug>.<type>.md`, whose body is the
  bullet verbatim: unique filenames cannot conflict, so a merge never touches the
  changelog. `scripts/changelog-assemble.py fold` writes the accumulated
  fragments into the release section in one deterministic step — one wave under
  the release heading, newest PR first, subsections in canonical order — and
  deletes them; `check` validates fragments in the pre-commit hook and in CI
  ([#679](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/679)).
