- **The gonuts-tollgate pin is one version, everywhere, by fence.** The
+  wallet fork's pin drifted three ways in one release week (#791): a
+  carrier outside the documented four (scripts/token-recovery, pinned three
+  minors behind at v0.10.0) was missed by a hand bump, two open PRs pinned
+  the same lines to a tag and a pseudo-version, and a newer fork tag
+  carrying an open-P2 fix went unnoticed. `packaging/build-inputs.json` now
+  names the pin as `.gonuts.version`; `tests/contract/check-gonuts-pin.py`
+  discovers carriers by glob and refuses any disagreement (pre-commit,
+  offline), `scripts/bump-gonuts.sh` rewrites manifest and every carrier
+  together so a half-update cannot happen, and `release-check` refuses to
+  cut a release while the fork has a newer stable tag than the one pinned.
+  The stale token-recovery pin is raised to the current v0.13.0 in the same
+  change
+  ([#791](https://github.com/OpenTollGate/tollgate-module-basic-go/issues/791)).
