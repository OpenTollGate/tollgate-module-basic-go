# Where each class of verification lives (single source of truth)

Topic doctrine split out of `AGENTS.md` (#920) so rig homes grow in their
own file instead of appending to the shared core. Indexed in AGENTS.md's
"Topic doctrine" section. The support-policy context (25.12/apk feature
target vs 24.10/ipk frozen compat lane) is the two-era rule in
`AGENTS.md`'s environment-truth doctrine.

Complexity has outgrown any single rig — each class has ONE canonical
home, and a change is verified where its failure class is observable:

- **Contract suites** (`tests/contract/`, offline, both CI lanes):
  render-level and structural checks. They CANNOT catch dataplane
  semantics — PR #782's contract suite was green while its ordering was
  dead (see `firewall-engagement.md` in this directory). Every contract
  suite must be mutation-checked before it ships: revert the guarded fix
  and confirm the suite FAILS; a suite that still passes on the broken
  shape has decorative assertions. Equally: a gate cited as protection
  must actually test the thing it is cited for (#778's build-purity
  claim).
- **Bench QEMU lanes** (per OpenWrt era — both 24.10 and 25.12 matter
  when the compat lane is touched; their netifd/nft behavior differs.
  The SUPPORT policy is `AGENTS.md`'s two-era rule: 25.12/apk is the
  only feature target, 24.10/ipk security-and-stop-ship only): dataplane
  ordering and packet counters. The #754/#755/#757 re-verify legs with
  counter asserts are the model. Bench VM images must pass an
  image-doctor check (depmod present, kmods non-zero-byte,
  wpad/veth/ip-full installed) before a campaign — stock minimal images
  ship without them.
- **PRTA** (the `physical-router-test-automation` repo — the deployed-lab
  and campaign harness): `scripts/extensive-test.sh` (tiered campaign),
  the wifi suites (`test_mac80211_hwsim.py`: in-guest radios — scan,
  associate, DHCP, reconnect; `test_virtual_wifi_hwsim_netns.py`: netns
  plane, dual-AP captive journeys; the `--vwifi` cross-VM 802.11 relay
  on the cloud lane), and `scripts/0.6.0-validation/` (phases A–G plus
  the 24h soak). New topology/journey scenarios belong THERE, as cases
  in these suites — not as scratch bench scripts that rot in /tmp.
- **cloud-lab crash lanes** (`tests/cloud-lab/`): the fund-safety
  kill/restart boundaries from `AGENTS.md`'s fund-safety section; runs
  anywhere with docker (ai-legion has it).
- **Labgrid physical tier**: real RF, real client devices, power-pull
  windows, era-specific kernels (e.g. mt7621 bridge-teardown behavior).
  The place protocol and registry rules are `labgrid-testing.md` in this
  directory.
