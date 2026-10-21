- **Build-from-source manual, package targets, and cold-cache gate.** Adds
  `docs/build-from-source.md` (tag-vs-`main`, installer/runbook and feedback
  links, CI/reproducible-builds status, package instructions), `make
  manual-check`/`cold-battery`, `make package-ipk`/`package-apk`, and the SDK
  `tollgate-cvm` staging fix that makes the `.apk` target work
  ([#1041](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/1042)).
