- **Packaging comments no longer restate the DEPENDS contract.** Four
  comments still claimed the runtime `DEPENDS` was `+libc only` or that
  `+ca-bundle` "arrives with FreedomTechFeed/packages#57" — both false since
  the dependency was restored and extended. Each site now points at the
  declaring file and the packaging fence instead of restating the value
  (per the one-declaration-per-fact doctrine), the ngit shard template
  comment was regenerated through the generator in a single run, and a
  duplicated `exit 0` in the SDK preinst define is gone. Comment-only: no
  recipe, artifact or maintainer-script behavior changes.
  ([#901](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/901))
