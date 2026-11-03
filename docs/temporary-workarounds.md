# Temporary workarounds — the registry

Every `TEMPORARY WORKAROUND` marker shipped in `src/` is registered here,
and `tests/contract/temporary-workarounds_test.sh` fails the build when a
marker exists without a registry row (or a row references a marker that no
longer exists). "Temporary" that nobody can see is permanent — the registry
is what makes the debt fail fast instead of compounding silently.

Registry columns:

- **id** — `TW-<n>`, cited by the code comment and the audit.
- **marker** — the file carrying the `TEMPORARY WORKAROUND` marker.
- **issue** — the owning issue (status, removal condition, and discussion).
- **added-in** — the release that first shipped it.
- **invocation contract** — who may call it, when, how often. The audit
  test pins the call-site half of this where it is checkable statically.
- **review-by** — the release (or date) by which the workaround must be
  re-argued or removed; the audit fails a build shipped past it.

| id | marker | issue | added-in | invocation contract | review-by |
|----|--------|-------|----------|---------------------|-----------|
| TW-1 | `src/upstream_session_manager/tollgate_prober.go` (`TriggerCaptivePortalSession`) | [#768](https://github.com/OpenTollGate/tollgate-module-basic-go/issues/768) | pre-0.6.0 (present in v0.6.0-rc1); gated in #768 | only `UpstreamSessionManager.HandleGatewayConnected`, only after `ValidateAdvertisementFromBytes` accepts the gateway's advertisement, at most once per gateway per process | v0.8.0 |

## TW-1 — upstream captive-portal session trigger

The upstream TollGate's ndsctl is supposed to create a client session for a
reseller device after successful payment; some implementations only do so
when the device makes a plain-HTTP request to the gateway's port 80. The
workaround makes that request. It is **not part of the TollGate protocol**
(browser-mimicking User-Agent included) — that is precisely why it is
registered and gated:

- until #768 it fired from inside the probe path — any HTTP 200 on
  gateway `:2121`, before advertisement validation, again on every 30 s
  detector tick — so a deployed router poked `gateway:80` for upstreams it
  had already rejected as non-TollGates;
- since #768 it fires only for a validated advertisement, once per gateway
  per process (in-memory; restart re-arms it — acceptable, see the issue);
- removal condition: upstream TollGate implementations create the session
  from the payment flow alone. Kill-switch candidate: an
  `upstream_detector` config flag, default off, enabled only for
  deployments known to need the poke.
