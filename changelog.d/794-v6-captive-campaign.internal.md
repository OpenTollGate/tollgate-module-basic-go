- **The IPv6 captivity axis is pinned on the VM rig.** The #783
  investigation closed with a coverage gap: nothing anywhere tested IPv6
  captivity *behavior* (the rc1 rig's "pre-auth bypass" was the
  hypervisor's own RAs on the captive bridge, not the router).
  `tests/vm-campaign/v6_captive_regression.py` boots a pristine VM,
  first proves the captive bridge carries no uplink (the rig-validity
  lesson — otherwise the test exercises the hypervisor, not the router),
  then pins uci v6-off state (including the post-#815 global axes,
  skipped on pre-#815 artifacts), RA wire silence, RS silence, no
  SLAAC/RA-route escape, pre-auth interception, and the healthy v4
  journey. Serial-console only, no hostfwd; verdicts and artifact
  sha256s land in the evidence dir alongside the console transcript
  ([#794](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/794)).
