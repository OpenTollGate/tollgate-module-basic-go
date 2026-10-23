- **A fence now catches incomplete go.sums across the whole module tree.**
  A merge can change one module's import surface and leave every importer's
  go.sum incomplete — exactly the #975 class that only the night battery
  caught, hours after merge. `tests/contract/check-gosum-completeness.sh`
  builds every module under `src/` against its own go.sum (glob discovery,
  ~2 s warm), fails naming the modules and quoting the missing entries, and
  ships a hermetic `--selftest` negative control; it runs in release-check's
  deps leg and both CI twins' deps-and-imports jobs
  ([#985](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/985)).
