# TollGate `v0.6.0-alpha2` — reporting a test result

**Channel:** `alpha` — a release candidate, not a stable release.
**Audience:** you have an OpenWrt router, you installed (or tried to install)
this alpha on it, and you have something to report.

This document defines **where** a report goes, **what** it must contain,
**what happens to it**, and **what this release actually claims to support**.
It is the intake side of the release. The install, upgrade, removal and
rollback instructions live in [rc-tester-guide.md](rc-tester-guide.md) — read
that one first if you have not installed yet.

## How to read the verification markers

Same convention as the tester guide:

- **VERIFIED (rehearsal)** — the command was executed against a real OpenWrt
  25.12.5 userland (`apk-tools 3.0.5`, `x86_64`) that installed the alpha
  package from a local copy of the channel layout. The quoted output is real
  output from that run.
- **UNTESTED ON A ROUTER** — documented intent, not an executed claim.
  Anything that needs `procd`, `ubus`, `logd`, the captive portal or the
  published feed host is in this class.

Every command in the §3 template was executed in that rehearsal environment,
against a service started by hand (the container has no `procd`). One item —
the log readback — depends on a system logger the container does not have, so
its *router-side* output is **UNTESTED ON A ROUTER**; that item and the
release-line variants are marked below.

---

## 1. The one place to report

**One channel, one place, no exceptions.**

> **Post your report as a comment on the pinned issue titled**
> **“Tester reports — TollGate v0.6.0-alpha2 (alpha channel)”**
> **in the project's public issue tracker:**
> **https://github.com/OpenTollGate/tollgate-module-basic-go/issues**

- **Do not open a new issue for a report.** One issue, many comments.
- **Do not send the report to a second place as well.** Not to a chat, not to
  a private message, not to the person who invited you. A report that exists in
  two versions in two places cannot be triaged, and the version we act on will
  be the wrong one.
- **Why one place:** we triage from that thread and nowhere else. A report we
  cannot find is a report nobody can fix.
- **Reading it costs nothing.** You do not need an account to read the thread.
- **Posting needs a free GitHub account.** If you cannot or will not use
  GitHub, hand your report to the person who invited you to test and ask them
  to relay it into that issue, word for word, with your name on it. That is a
  relay *into* the channel, not a second channel.
- **The issue is created and pinned when this release candidate is announced.**
  If it is not there, the RC has not been announced yet — wait for the
  announcement. Do not report somewhere else in the meantime.

## 2. Before you write: three quick checks

1. **Is your release line supported?** See §6 for the matrix. If you are on
   OpenWrt 24.10 or older, read §6 first: the feed is not for you, and what we
   can do for you there is genuinely limited.
2. **Is it already a known problem?** `RELEASE-NOTES.md`, section *Known
   issues*, and the *Verification status* section below it list what is
   already known and what was never verified. If your symptom matches
   something there, it is still worth reporting when **anything differs** — a
   different router or architecture, an extra step, a different message, a
   different trigger. Say “this looks like the known X issue, but …”.
3. **Can you run the commands in §3?** If the router is unreachable, or you
   removed the package, send what you can and mark the rest
   `cannot run: <reason>`. But accept §4: **a report without a version and an
   architecture is untriaged**, so if you cannot produce them, say that
   plainly and say why.

## 3. The report template

Copy this into your comment on the intake issue and fill it in. Replace
everything in `<…>`. Paste **raw terminal output** — never a screenshot, never
a paraphrase, never a retyped summary.

````text
### Report — <S1|S2|S3> <one line: what happened>

- 1. Router model: <make and model, e.g. GL.iNet MT3000 / Xiaomi AX3000T / custom build>
- What I was doing: <clean install | upgrade from <version> | removal | rollback | normal use>
- Severity I think it is: <S1 funds | S2 broken service | S3 cosmetic — see §4 of docs/tester-intake.md>
- Expected: <what you expected to happen>
- Actual: <what actually happened, with the exact message if there was one>
- Was the service running at the time? <yes | no | unknown>

<details><summary>Facts — raw output</summary>

**2. OpenWrt release and architecture**
$ cat /etc/openwrt_release
$ apk --print-arch
<paste both outputs here>

**3. The feed line I added**
$ cat /etc/apk/repositories.d/tollgate-testing.list
<paste; or write "my file is called <name>, the line is: <line>">

**4. What the package manager says is installed**
$ apk list --installed tollgate-wrt
<paste; or write "not installed">

**5. What the service reports**
$ tollgate version
<paste>

**6. The exact bytes installed**
$ sha256sum /usr/bin/tollgate-wrt /usr/bin/tollgate
<paste>

**7. The log**
$ logread -e tollgate | tail -50
<paste>

</details>
````

### Notes on the items that surprise people

**Item 2 — architecture.** `apk --print-arch` exists on OpenWrt 25.12
(`apk-tools` 3.x). On 24.10 and older there is no `apk`: use
`opkg print-architecture` and say that you did. The architecture is what tells
us whether your report concerns an architecture anybody has run yet, so it is
not optional.

**Item 4 vs item 5.** Item 4 is what the package manager believes is installed;
item 5 is what the running service actually reports. When both are present and
they disagree, that disagreement is itself a finding — include both.
`apk list tollgate-wrt` (without `--installed`) shows the same line and is
equally acceptable; `--installed` is simply the precise question.

**One disagreement is expected and is not a finding.** The same release carries
two spellings on purpose: item 4 prints the apk control spelling
(`0.6.0_alpha2-r0` — no leading `v`, `_` instead of `-`, plus apk's own `-r<N>`
release revision) and item 5 prints the release tag (`v0.6.0-alpha2`), because
`apk` cannot carry a hyphen in a version. A mismatch **of that shape** — the
same version number, differing only in a leading `v`, in `_` versus `-`, or in
a trailing `-r<N>` — is expected and is not a finding. Only a genuinely
different version number is.

**Item 5 — there is no `--version` flag.** `tollgate --version` is not a valid
option in this CLI: it exits non-zero with `unknown flag: --version`. The
command is `tollgate version`.
**VERIFIED (rehearsal)** — the error message and the non-zero exit were both
observed, and the successful command produces:

```
$ tollgate version
TollGate Version
version: v0.6.0_alpha2
commit: dcf8c5d
build_time: 2026-09-13T00:00:00Z
go_version: go1.26.0
openwrt_version: OpenWrt 25.12.5 r33051-f5dae5ece4
```

**What produced that block — and why three of its values are not the
release's.** The output is real, but it is the **rehearsal build**: a build made
by hand in the rehearsal container (OpenWrt 25.12.5 userland, `x86_64`) at
commit `dcf8c5d`, compiled with the build host's own Go toolchain rather than
CI's, and with a version string injected for the rehearsal. It is **not** output
from the package in the feed, and the three values below are artifacts of how
it was built:

| field | this rehearsal build | the published `v0.6.0-alpha2` package |
|---|---|---|
| `version` | `v0.6.0_alpha2` — the apk-safe spelling, injected by hand | `v0.6.0-alpha2` — the release-tag string in the `VERSION` file, which CI injects into the binary |
| `build_time` | `2026-09-13T00:00:00Z` — a fixed placeholder; no build path emits RFC 3339 | `%Y-%m-%d %H:%M:%S UTC`, e.g. `2026-09-13 00:00:00 UTC`, from `date -u` |
| `go_version` | `go1.26.0` — the rehearsal host's toolchain | `go1.25.x` — every published package is compiled by CI, which pins Go 1.25 |

The field names, their order, the `commit` line and `openwrt_version` are what
you will see; only those three values differ, and they are the reason a real
install is not expected to reproduce this block byte for byte. Compare item 5
against item 4 and against the version in the announcement, not against this
block.

`tollgate version` asks the **running service**. If the service is down it
fails with `failed to communicate with TollGate service` — say so instead of
guessing, and make sure item 4 is present, because that is what we fall back to.

**Item 6.** Paste both hashes. We compare them against the artifact the
announcement names; a hash that matches nothing is how we discover a
substituted or truncated package. If one of the two paths does not exist on
your install, `sha256sum` prints an error line for it — **paste that too**, it
tells us the install is incomplete.

**Item 7.** **VERIFIED (rehearsal)** in the sense that the command ran and
returned; its *output* on a router is **UNTESTED ON A ROUTER**. In the
container there is no `logd`, so it prints:

```
$ logread -e tollgate | tail -50
Failed to connect to ubus
```

If that, or nothing at all, is what you get, **paste it anyway** — an empty or
broken log is information. You can also try `tollgate logs --tail 50`, which
reads the same log source through the service. **VERIFIED (rehearsal)**: the
subcommand exists and returns (`tollgate logs --help` lists `-n, --tail int`
and `-f, --follow`); in the rehearsal container it returned
`Error: failed to read logs: exit status 255` for the same reason `logread`
fails there — no `logd`. Its output on a router is **UNTESTED ON A ROUTER**.

**Never** work around a missing fact by inventing it. “Unknown” is a usable
answer; a wrong version number sends the investigation in the wrong direction.

## 4. What happens to your report

### 4.1 Triage rule

**A report without a package version and an architecture is untriaged.**

- We ask **once**, in the thread, for exactly the missing pieces from items 2,
  4 and 5.
- If those do not arrive, the report is **closed as untriaged**.
- This is not a judgement on your report. “It broke on my router” cannot be
  reproduced, matched to a published build, or fixed — the version and the
  architecture are what make the difference between a bug report and a story.

Everything else in the template is expected, but not the gate: a report that
carries version and architecture gets triaged even if you could not produce the
other items, as long as you say which ones you could not produce and why.

### 4.2 Severity

- **S1 — any wallet or funds symptom. This is stop-ship.**
  A balance that changed when nothing was bought; a sale credited but not paid;
  a cancel, drain or funding operation that behaved oddly; a token that
  disappeared; anything at all that could mean money moved the wrong way.
  **What stop-ship means, literally:** before anyone investigates, we **pull
  the release** — the feed index for the affected architecture is withdrawn so
  that nobody else installs those bytes — and then we fix it. Put the label
  first on the first line of your report — `### Report — <S1|S2|S3> <what
  happened>`, exactly as the §3 template's first line shows it — so that nobody
  skims past it.
  If you are **not sure** whether your symptom is S1, report it as S1 and say
  why you are unsure. Over-reporting S1 costs us an hour; under-reporting one
  costs somebody their money.
- **S2 — the service is broken.** It crashes, will not start, will not stop,
  leaves the router unusable after install/upgrade/removal, the portal is
  unreachable, payments fail. The router itself is still usable; the service is
  not.
- **S3 — cosmetic, documentation, or “I am not sure”.** Wrong or confusing
  text, layout problems, a step in the tester guide that does not match what
  your router does, slowness that does not break anything.

Severity is **our** call after reading the report, not yours to get right. If
you are unsure, describe the symptom and let us label it.

### 4.3 What every qualified report becomes

- Every qualified report is turned into exactly **one tracked work item on our
  internal board**, tagged with its severity and your architecture — for
  example `S2 aarch64_cortex-a53` — and worked in severity order, S1 first.
- You will see that echoed back **as a reply in the intake thread** with the tag
  we gave it. That reply is your confirmation that the report landed. If you
  get no reply at all after a few days, re-post the two facts from items 2 and
  4 and ask — silence usually means the report was never seen, not that it was
  ignored.
- We do not promise a fix. We do promise that an S1 report changes what we do
  next.

**Reports from architectures nobody has run yet are the most valuable reports
you can send us.** The build exists for all of them; whether the package
installs and runs is exactly what we do not know.

## 5. Never paste any of this

Reports are public. Nothing here is needed to diagnose a problem:

- `tollgate config get` output, or the contents of `/etc/tollgate/` — that
  directory holds identities and the wallet database;
- a wallet file, a seed phrase, a mnemonic, an `nsec`, an `npub` you consider
  private;
- a Cashu token, or the output of `tollgate wallet drain cashu`;
- router credentials, WireGuard keys, private network passwords, WiFi PSKs;
- anything from `/tmp/tollgate-setup.log` (it contains the generated
  management-WiFi password);
- **the log block you paste for item 7.** Normal operation writes token material
  into the daemon's log: lines containing `token_preview=` (the first 50
  characters of a Cashu token, `src/merchant/merchant.go`) and `preview:` (the
  same, on the wallet-funding path) are ordinary log lines. Read the log block
  **before** you paste it, delete everything after `token_preview=` and after
  `preview:`, and say in the report that you redacted it — a redacted log is
  still a usable log, and the surrounding lines are what we need.

If we need a specific value we will ask for that value and tell you where it is
safe to read it.

**If you think you sent a secret by accident, say so immediately and do not
paste it again.** That is a recoverable situation; a secret sitting in a public
thread is one we would rather hear about than discover later.

## 6. What this release actually supports

| Release line | Architecture | What is claimed |
|---|---|---|
| OpenWrt 25.12.x (apk) | `x86_64` | Install/upgrade/remove/rollback **rehearsed** on a real 25.12.5 userland (a container, not a router). Router-hardware acceptance: **see the announcement**, which names exactly which architectures passed. |
| OpenWrt 25.12.x (apk) | every other architecture the build matrix produces | **Built, untested** — the package exists and is published; nobody has installed it on hardware yet. |
| OpenWrt 24.10.x and earlier (opkg) | any | **Not supported through the feed.** Read “best effort” below. |
| snapshots / master | any | **Not supported for testers.** |

**The announcement decides.** Only the architecture whose acceptance test
actually passed is announced as *tested*. Everything else is published as
*build only, untested* — that is not a support claim, and a report from those
architectures is a first test rather than a regression.

**Why 24.10 and earlier have no feed.** Their OpenWrt SDK toolchains ship Go
1.21/1.23 and cannot build this module, which needs a current Go toolchain, so
no package for those releases is produced by this pipeline — and publishing an
empty or unsigned opkg index would read to a router as a broken feed.

### What “best effort” means, in plain words

If the announcement offers standalone package files for a release line that has
no feed (24.10 and earlier), then for those files:

- they are built by the same pipeline, and **installed and tested by nobody** —
  whether they run on your release line at all is unknown;
- there is **no feed**: no automatic upgrade, no version pinning, nothing that
  tells you a newer build exists;
- there is **no tested rollback path** on that release line — assume you may
  have to restore or reflash your router;
- we will read reports about them and we may fix what a report shows, but we
  will **not** hold the release for them and we cannot promise you a working
  artifact;
- if you need any degree of certainty, do not use this build on that release
  line.

“Best effort” is us being honest about a build nobody tested, not a softer word
for supported.

### Rollback exists — know it before you install

- The tester guide §6 has the rollback sequence, and it is executed in the
  rehearsal. On 25.12 a rollback leaves a **version pin** in `/etc/apk/world`;
  §6 also tells you how to remove that pin so the router follows the channel
  again. Read it before you install, not after.
- Rollback returns the **software**, not necessarily your **configuration**.
  Back up `/etc/config/tollgate` and the wallet directory first. The two are
  not the same thing and only one of them is reversible.
- Unsubscribing completely is removing the one repository line you added
  (tester guide §3.2) — a production router never sees this release in
  `apk upgrade`, and reverting a tester is one line.

### About funds

This is an alpha. **Do not keep funds in a wallet on a router running it.** The
stop-ship rule in §4.2 is a promise about how fast we react to a funds symptom,
not a promise that funds cannot be lost.

## 7. Where to read more

- [rc-tester-guide.md](rc-tester-guide.md) — the feed key, the repository line,
  install / upgrade / remove / rollback, what a successful install looks like,
  the failure modes seen in practice, and the list of things a real-router test
  still has to confirm.
- `RELEASE-NOTES.md` — what changed in this release, its known issues, and its
  verification status. Read the known issues before concluding that what you
  found is new.
- **The intake thread itself** — other testers' reports and our replies. If you
  are about to report something, search the thread first; you may find your
  answer, or find that you can add a detail that helps.

---

## Appendix A — the intake issue (maintainer: paste this verbatim)

**Title**

```text
Tester reports — TollGate v0.6.0-alpha2 (alpha channel)
```

**Body**

````markdown
**This is the single intake channel for TollGate `v0.6.0-alpha2` alpha
testers. Post your report as a comment on this issue.**

- **Do not open a new issue for a report**, and do not send the same report
  somewhere else as well. A report in two places in two versions cannot be
  triaged.
- **Use the report template**: [docs/tester-intake.md](https://github.com/OpenTollGate/tollgate-module-basic-go/blob/main/docs/tester-intake.md)
  §3. It asks for your router model, `cat /etc/openwrt_release` +
  `apk --print-arch`, the feed line you added,
  `apk list --installed tollgate-wrt`, `tollgate version`,
  `sha256sum /usr/bin/tollgate-wrt /usr/bin/tollgate`,
  `logread -e tollgate | tail -50`, and expected vs actual.
- **A report without a package version and an architecture is untriaged.** We
  ask once for the missing pieces and then close it.
- **Severity.** `S1` = any wallet or funds symptom, and it is **stop-ship**:
  the feed index is pulled before anyone investigates. `S2` = the service is
  broken. `S3` = cosmetic or documentation. Put the label first on the first
  line of your report — `### Report — <S1|S2|S3> <what happened>`, exactly as
  the §3 template's first line shows it — so that nobody skims past it.
- **Every qualified report becomes one tracked work item** tagged with its
  severity and your architecture, and you will see that tag echoed back in this
  thread.
- **Never paste** `tollgate config get` output, anything from `/etc/tollgate/`,
  a wallet file, a seed phrase, an `nsec`, a Cashu token, router credentials or
  WiFi PSKs. If you think you sent a secret by accident, say so immediately and
  do not paste it again.
- **Read the honest support matrix before you report**: [docs/tester-intake.md](https://github.com/OpenTollGate/tollgate-module-basic-go/blob/main/docs/tester-intake.md)
  §6 — which release line is supported, which architecture was actually
  tested, what "best effort" means, and that a rollback exists.
````

The pinned issue and `docs/tester-intake.md` must say the same thing. If they
ever disagree, the document wins.

## Appendix B — maintainer go-live (not part of a tester's instructions)

The intake document is published with the release, but the channel itself does
not exist until someone with write access to the upstream repository creates it.
The release automation cannot do it — the account it uses has read-only access
to upstream (`403` on any write) — so **this is an operator step in the
announcement sequence**:

1. **Create the issue** on `OpenTollGate/tollgate-module-basic-go` → *Issues* →
   *New issue*, using the title and body in Appendix A. Create it when the feed
   is live — not earlier, or the channel points at a release nobody can install
   yet.
2. **Pin it.** *Issues* → the issue → *Pin issue*. The document names the issue
   by title rather than URL, so nothing needs editing once the issue exists;
   the pin is what makes it findable.
3. **Link it in the announcement** next to the tester-guide link, and make sure
   `RELEASE-NOTES.md` still links both documents.
4. **Keep the tags in the thread.** Severity and architecture are recorded as
   replies and as tracked work items; do not open a second tracker for the same
   reports.
5. If the channel ever moves (for example, a differently named issue), update
   §1 of this document and the `RELEASE-NOTES.md` link in the same change, so
   the two never disagree.
