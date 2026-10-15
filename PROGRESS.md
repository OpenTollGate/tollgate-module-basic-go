# PROGRESS — tollgate-bearer-instruments ralph loop

Append one line per iteration: `finding -> status -> files touched`.
This is the crash-recovery map. Read this + REPORT.md first on resume.

Format:
    [NNN] <ISO ts> | <what I did> | <status> | <files touched>

---

[001] 2026-10-15T01:10Z | loop scaffolding created; worktree verified clean at 67e2be2 on pr/one-way-channel-spike; quota gate ALLOW (deliverable=true, not frozen) | start | PROGRESS.md, REPORT.md

[002] 2026-10-15T01:25Z | P0 COMPLETE: landed ROUTER-FEASIBILITY.md + OPTIONS-PAPER.md + research/lnurlcash-tollgate/ (codec POC 141/141 verified in-tree); fixed 4 stale step_size-ms spots; errata bug-count already clean; gitignored __pycache__ | pushed aeeba1f | docs/analysis/*.md, research/lnurlcash-tollgate/, .gitignore

[003] 2026-10-15T01:45Z | P1.3 IN PROGRESS: discovered dq05 is UNREACHABLE (dq05.fips will not resolve, 192.168.2.32 permission denied) - the regtest node I provisioned there is gone. FOUND BETTER: the rig's OWN local regtest node runs on this host (~/.local/toolchain/btc/bin/bitcoind, datadir ~/.local/toolchain/btc/regtest-data, rpcport 18443, wallet 'rig', height 136, Core v31.1.0). Wrote kernel_check.py. LEG 1 MEASURED against real Core: both leaves decodescript type='nonstandard' (110B/111B or 69B/70B variants). LEG 2 PARTIAL: derived real P2TR addresses for both leaves, FUNDED both (0.5 BTC each) and confirmed them as live witness_v1_taproot outputs via gettxout. NOT YET SUBMITTED: the script-path witness serialization + testmempoolaccept ruling is unfinished, so the BIP-342 claim remains REASONED, not MEASURED. | blocked-partial | research/lnurlcash-tollgate/kernel_check.py, out/KERNEL-CHECK.json

[004] 2026-10-15T02:05Z | P1.3 BANKED as PARTIAL: kernel_check.py runs against real Core v31.1.0; 4 claims MEASURED (both leaves parse; our BIP-341 commit math confirmed by Core producing a valid taproot address; both P2TR outputs funded+confirmed via gettxout; Core executes the script path). Headline BIP-342 claim still REASONED - witness has a 'Witness program hash mismatch' bug, so Core never ruled on the opcode. Bug located, infra in place. | pushed dda4bd4 | research/lnurlcash-tollgate/kernel_check.py, KERNEL-CHECK-FINDINGS.md, out/KERNEL-CHECK.json

[005] 2026-10-15T02:05Z | loop checkpoint: P0 complete; P1.3 partial. Remaining: P1.3 witness fix (highest value, precise), P0.1 hedgehog landing (consultant still running), P1.4/P1.5/P2.*. | checkpoint | PROGRESS.md, REPORT.md

[006] 2026-10-15T02:40Z | P1.3 DONE - HEADLINE CLAIM MEASURED. Root-caused the witness bug to OURSELVES: a single-leaf taproot tree has NO merkle sibling, so the control block is 33 bytes not 65 (appending the tapleaf hash made Core recompute a different output key -> the misleading 'Witness program hash mismatch'). Second trap: taproot_tweak_pubkey takes the final TWEAK SCALAR, not the tapleaf hash. With the fix, Core ruled per opcode on real script-path spends: CHECKMULTISIG -> 'OP_CHECKMULTISIG(VERIFY) is not available in tapscript'; CHECKSIGADD -> 'Invalid Schnorr signature' (opcode accepted, execution proceeded). Corroborated by Core refusing multi() in tapscript while accepting multi_a(). kernel_check.py rewritten to reproduce it and self-verify. 3 earlier claims retracted. | pushed | research/lnurlcash-tollgate/kernel_check.py, KERNEL-CHECK-FINDINGS.md, out/KERNEL-CHECK.json

[007] 2026-10-15T03:10Z | P0.1 COMPLETE + P1.4 DONE. Hedgehog consultant TIMED OUT (3600s, 121 calls) but left a validated library on disk: xcheck.py 10/10 independent cross-checks, incl. X3 byte-for-byte agreement with the SHIPPED tapscript.js for the real 3-leaf Hedgehog tree. Last-mile salvage: wrote the missing run_poc.py against the real API; fixed 6 API mismatches the timeout left behind (Node not BTC instance; bitcoind needs literal 'true'; strip round-tripped args; witness items are raw bytes; prevouts carry ints not serialized; testmempoolaccept takes hex). RESULT: D5 IS NOT MET - Hedgehog is non-custodial AND sender-offline AND recipient-offline (first construction to achieve all three) but RELOCATES the time obligation into a CSV window needing watchtower-equivalent observation. 13 checks pass. Honest boundary: no end-to-end [CORE] accept (Core rejects with 'Invalid Schnorr signature' - it executes the script but our sighash plumbing differs); override race not executed. | pushed | docs/analysis/D5-RETEST.md, research/hedgehog-poc/poc/
