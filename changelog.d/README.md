# changelog.d — one fragment per change

`CHANGELOG.md` is the human record of what shipped. It is written by folding
*these* files in, not by editing the changelog from a pull request.

Every PR used to append its entry to the same anchor of `CHANGELOG.md`, so two
pull requests in flight always conflicted on the same lines and the maintainer
resolved those hunks by hand — about a dozen of them on the 0.6.0 train, and a
`--ours` resolution resurrected an already-moved entry twice (#524, #531). A
fragment removes the shared anchor: filenames are unique, and git merges unique
new files without conflict, so a merge never touches `CHANGELOG.md` at all.

## Adding an entry

Create one file, named

```
changelog.d/<pr-number>-<short-slug>.<type>.md
```

where `<type>` is the section it belongs in:

| type | subsection in the changelog |
| --- | --- |
| `added` | `### Added` |
| `changed` | `### Changed` |
| `internal` | `### Changed / Internal` |
| `deprecated` | `### Deprecated` |
| `fixed` | `### Fixed` |
| `removed` | `### Removed` |
| `security` | `### Security` |

The file body **is** the bullet: it starts with `- `, it is wrapped the way you
want it in the changelog, and it ends with the PR link, exactly as the entry in
`CHANGELOG.md` would have read:

```markdown
- **A wedged mint no longer stalls the payment lane.** The swap-fee precheck
  now runs under a 3-second budget …
  ([#612](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/612)).
```

Do not edit `CHANGELOG.md` in the same PR. Do not put headings in a fragment,
and do not create `changelog.d/README.md`-style files for anything other than a
release: the directory carries only fragments, `README.md` is ignored by the
tool.

## Folding (maintainers)

```bash
python3 scripts/changelog-assemble.py check        # names and bodies
python3 scripts/changelog-assemble.py fold --dry-run   # see the diff
python3 scripts/changelog-assemble.py fold             # write it, delete the fragments
```

`fold` writes one wave of subsections immediately below the target release
heading (default `## [Unreleased]`, or `--target v0.6.0-rc1`), newest PR number
first and subsections in the canonical order above. It touches nothing else in
the file, and it refuses to run on a malformed fragment or on a
`CHANGELOG.md` that still has conflict markers in it.

Fold in batches — before a release (see [docs/release-process.md](../docs/release-process.md)),
or whenever the changelog should read current. The rule the fold follows, and
why the directory exists at all, are recorded in
[docs/architecture/changelog-fragments-decision.md](../docs/architecture/changelog-fragments-decision.md).
