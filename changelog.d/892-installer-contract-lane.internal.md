- **The installer has its own contract lane.** A built `.apk` can now be
  validated end-to-end on a pristine OpenWrt 25.12 VM exactly the way an
  operator installs it — including the `TOLLGATE_ADMIN_PASSWORD` /
  `TOLLGATE_PRIVATE_WIFI_PASSWORD` environment the docs promise — with
  assertions for the credential lifecycle (sha256crypt comparison, no
  plaintext in output, the provisional/first-run fallback), services,
  listeners, the entry-port mapping and reboot persistence, plus serial
  bootstrap and distrusted pristine verification (a false `firstboot`
  "reset" on one 25.12.5 image kept `/etc` and would have silently
  invalidated every credential check). Two checks ship red on purpose as
  the acceptance criteria of #888 (the env never reaching the uci-defaults
  script under `apk add`) and #890 (the entry-port flip's LuCI pair not
  converging on fresh-image installs) — both found by this lane's first
  fully-automated run on the vlab
  ([#892](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/892)).
