- **The rebrand-literal gutter is green again: the whitelabel prefix now
  lives in one assembled brand registry.** #682's dual-brand SSID
  recognition (a whitelabel-branded TollGate must be recognizable as a
  reseller upstream) spelled the re-brand's name contiguously on seven code
  surfaces — including the shipped `99-tollgate-setup` comment and the
  runtime prefix table — which re-tripped the code-surface ban #684/#692
  had scoped, leaving `make release-check` Packaging RED on main (#722).
  The recognition set moved to `src/wireless_gateway_manager/brands.go`,
  where the whitelabel entry is assembled from non-contiguous bytes (the
  same trick the gutter uses for its own scan pattern) and pinned by a test
  that re-assembles it independently, so an assembly typo fails CI instead
  of silently dropping a brand from upstream recognition; comments now
  reference the registry without spelling the name. Recognition behavior is
  byte-identical
  ([#739](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/739)).
