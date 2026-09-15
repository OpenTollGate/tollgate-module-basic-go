# Draft: licence + upstream Linux target request to nucula's author

**Status:** **SENT 2026-09-14** — filed publicly as
`zeugmaster/nucula#8` (https://github.com/zeugmaster/nucula/issues/8);
awaiting the author's response. T2c/T11 are open until answered.
**Why it is the gating question:** nucula's repo has never contained a licence
file (every licence path checked across the full commit history; the GitHub
licence API returns 404). Without a grant of rights we may reuse *ideas* but not
code, so the entire nucula path is blocked on this single question.

Send as a GitHub issue on `zeugmaster/nucula` (public, on the record, and easier
to point at later) — or as a DM if preferred. Keep it short; two separable asks.

---

**Subject: Licence for nucula, and interest in a Linux/OpenWrt build target**

Hi — I maintain TollGate (github.com/OpenTollGate/tollgate-module-basic-go), an
OpenWrt router that takes Cashu payments. We are reworking our wallet layer and
nucula is the strongest small C++ Cashu implementation we have found: we read the
source (not just the README) and the NUT-00/01/02/03/04 coverage with per-unit
handling and the V3/V4 token codecs is genuinely careful work. Thank you for it.

Two questions, both small and independent:

**1. Licence.** The repository has no licence file, so as things stand nobody can
reuse any of the code — we can only borrow ideas. Would you be willing to add a
licence? Any of MIT, Apache-2.0 or GPL-3.0 would work for us; GPL-3.0 would match
TollGate's own licensing and is the easiest match, but this is entirely your call
and we will respect whichever you pick.

**2. A Linux build target.** We would like to use nucula's wallet *core* as a
plain Linux/musl library on the router — no NFC, no OLED, no keypad, driven by a
small daemon, cross-compiled for aarch64/mipsel. We are currently measuring that
port (build, footprint, behaviour) as a feasibility spike. Would you be open to
one of these, in order of preference:

  (a) we contribute a Linux build target upstream and you maintain it as a
      supported target;
  (b) upstream stays ESP32-only and we maintain a downstream port.

We would strongly prefer (a): we are trying to *stop* maintaining a downstream
fork of a Cashu library (that is the whole reason we are looking at nucula), and
we would rather send you a small, well-tested platform shim than own a permanent
port. If it helps, we can send our port plan and the API-boundary inventory
before writing any code, so you can judge the shape first.

No pressure on either — a "no licence" is a perfectly fine answer, we just need
to know so we can plan. Happy to share our measurement results either way, since
they are nucula's results as much as ours.

Thanks,
<maintainer>

---

**Notes for the maintainer (do not send):** the licence ask should come from you,
not from an agent identity. If the answer to (2) is (b), the port becomes a
grade-C dependency (we own it) and the recommendation has to say so explicitly.
