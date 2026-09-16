TollGate turns an OpenWrt router into a Bitcoin/Lightning paywall for internet access. Its Cashu wallet is load-bearing: it receives e-cash tokens from captive-portal clients and redeems them at a mint. Getting it wrong means a router that gives away service — or loses funds. I just finished a programme to decide whether to keep it or replace it. Here's what we learned.

## The trap we were in

- The router wallet is `gonuts-tollgate`, a **fork** of `elnosh/gonuts`, a Go Cashu library whose upstream has had **no commits since September 2025**. We own the fork; so we own every future bug.
- The fork is not cosmetic. It carries two **funds-safety fixes** upstream will never absorb — an HTLC signature-enforcement bypass (`296c7bf`) and a proof-loss-on-swap fix (`7dc430b`) — plus V2/short-keyset-ID support and reseller overpayment handling.
- So the defect was never "Go" or "in-process". It is that we depend on a dead upstream with security-critical local deltas — and un-forking is impossible: there is no counterparty, and the alternative drops the security fixes.

## What we learned

- **Ownership is the axis, not language.** We graded every option A/B/C by how much fork we would own. Diversification turned out to be an observation; fork-ownership cost is the real number.
- **The interface is small; the risk is behavioural.** One `WalletPort` seam means every candidate can be wrapped. The danger is silently reverting a security fix, so *parity* is the actual gate.
- **Timing-based fault injection lies.** A fast mint settles a swap in milliseconds, so a timer can never land mid-swap. We proved "no funds lost across a crash" by delivering the swap to the mint and dropping the response at the network layer — plus a **negative control** (a fresh-seed wallet must fail with "Token Already Spent") to prove the fault was real.
- **Emulation can stand in for hardware:** qemu-user plus a musl sysroot let us actually *run* a mipsel OpenWrt binary, with every self-test green.

## The three candidates

| | gonuts (forked) | CDK (sidecar) | nucula |
|---|---|---|---|
| Ownership | **C** — we own a dead fork | **A** — consume upstream, own only the build recipe | **C** — unlicensed |
| Licence | GPL-3.0 | MIT OR Apache-2.0 | none |
| Coverage | complete (the reference) | complete via the daemon | missing NUT-07/09 (recovery) |
| Security fixes | has both | **reproduces both (verified)** | n/a |
| Persistence | sqlite | sqlite + saga recovery | whole-proof-set rewrite (**flash wear**) |
| Footprint | ~11.5 MiB in-process service, ~26 MiB RSS | **5.5 MiB aarch64 / 7.5 MiB mipsel** daemon | core ~1.8 MiB (not shippable) |
| Effort to adopt | 0 (but pay forever) | **~3–6 eng-days** | weeks–months (C++→Go rewrite) |
| Biggest risk | security drift on a dead fork | ALPHA churn; flash budget | licence + rewrite |

**gonuts** wins on completeness, mipsel, and being in production today.

**CDK** wins on ownership, licensing, and active maintenance — and, as of this work, it **runs on the actual router** and passes every security-parity case, including a conclusive no-loss-on-crash-swap.

**nucula** is a lovely ESP32 tap-to-pay device wallet with a genuinely reusable protocol core — but it is not a router wallet, it is unlicensed, and its storage rewrites the entire proof set on every payment.

## Where we landed

CDK via a **process-isolated sidecar** is the destination. Keeping the fork is a deliberate, time-boxed stay. nucula is out as a router substitute: its value is ideas, not code, until it is licensed.

The exit condition to leave gonuts — sidecar client, security parity, mipsel — is now met. What remains is a flash-budget decision (does the daemon fit on the small mipsel tier alongside the service?) and a policy for ALPHA API churn.

Everything is reproducible: the research, the raw evidence, and the code-PR diffs are consolidated on one branch — including a mipsel nucula core at `SELFTEST_RESULT suites=4 failures=0` and an end-to-end no-loss-swap proof for CDK.

#cashu #nostr #openwrt #tollgate #bitcoin
