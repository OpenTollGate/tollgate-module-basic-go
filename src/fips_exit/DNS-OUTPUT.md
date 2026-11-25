# DNS OUTPUT confinement (renderer only)

`RenderDNSOutput` emits an `inet fw4` table-body include, with an OUTPUT base
chain at priority -2. It changes no files, services, interfaces or rulesets.
The dedicated numeric UID is explicit (1..4294967294), never inferred from a
username. Provision an exclusive nonroot account, with no capabilities or
ability to change UID/network namespace; do not share it with FIPS, system
DNS, portal, or any other process. The helper cannot verify account ownership.
Keys require caller-side identity/curve validation. Verify actual interface
roles, source-address ownership and routing before deployment.

Only the selected UID is confined. Exceptions use `return`, not an unconditional
accept grant: later fw4 OUTPUT policy remains authoritative. Exceptions are UDP
exit IPv6 port 53 on the FIPS interface with the key-derived local IPv6 source;
UDP/TCP to the exact loopback `.fips` resolver endpoint on `lo`; and established
conntrack **reply-direction** UDP/TCP packets on the guest interface with the
exact guest listener IPv4 source and high source port. Everything else for the
UID drops, including other IPv4/IPv6 DNS, TCP exit DNS, and non-DNS output.
Non-loopback guest IPv4 and distinct non-loopback interfaces exclude endpoint
and interface overlap with the loopback resolver. No INPUT, DNS redirect,
deauthorization, account creation or service wiring is implemented here.

## Required future start/stop ordering

Start denied: keep the dedicated resolver stopped while provisioning its account,
listener, addresses and routes. Atomically install and verify the confinement
before starting DNS as that UID with an isolated configuration and no inherited
resolvers. If validation/installation fails, leave it stopped. Stop the resolver
before removing/changing confinement or reusing its UID; firewall reloads must
never create an unguarded live-process interval. This renderer is not readiness
or live packet-path evidence; guest INPUT authorization remains future work.

## Reproduce static verification

From `src`, with Go 1.26.10 selected explicitly:

```
GOTOOLCHAIN=go1.26.10 go test -race -cover ./fips_exit
GOTOOLCHAIN=go1.26.10 go vet ./fips_exit
FIPS_DNS_NFT_CHECK=1 GOTOOLCHAIN=go1.26.10 go test ./fips_exit -run TestRenderDNSOutput -v -count=1
```

The opt-in test feeds the actual renderer output through stdin to
`sudo -n unshare -n nft -c -f -`, wrapped in `table inet fw4`. Each IPv4,
IPv6 and mapped-IPv4 loopback case also requires rejection of deliberately
invalid nft syntax. Privilege/netns failures fail the test; no host fallback.
`-c` validates without installing rules, inside a fresh privileged netns.
Observed: initial RED undefined `RenderDNSOutput`; GREEN all cases passed;
race suite 87.7% statement coverage; vet clean; three real nft checks and their
three syntax-negative controls passed on Linux. No runtime confinement claim.
