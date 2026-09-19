#!/bin/sh
# phases/06-analyze.sh — recompute the headline numbers in 03-baseline/gonuts.md
# from the raw output, so a third party does not have to trust the prose.
#
#   ./run.sh analyze        # writes raw/analysis-<stamp>.txt
#
# Every statistic is printed WITH its input file and n. Nothing here is typed in
# by hand: if a number in the report is not printed by this script, the report
# labels it UNMEASURED or ESTIMATE.
#
# Notes on the inputs (all measured 2026-09-18/19 on the GL-MT6000):
#  * payments-cycles-*.csv rows are one `tollgate wallet fund` call each, with
#    the eMMC + loop diskstats deltas around it. Two column layouts exist: the
#    2026-09-18 runs have no CPU columns (the sampler grew utime/stime later),
#    the 2026-09-19 run adds cpu_ticks_delta,cpu_ms. Both are handled.
#  * `amount_sats = na` means the receive was REJECTED — those rows must never be
#    averaged together with successful ones (they differ ~1.9x on flash writes).
. "$(dirname "$0")/../lib/common.sh"

STAMP=$(utc | tr ':' '-')
OUTF="$OUT/analysis-$STAMP.txt"
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT
say() { echo "$*" | tee -a "$OUTF"; }

# nearest-rank percentile of a sorted numeric file: pctl <file> <percent>
pctl() {
    _f="$1"; _p="$2"
    _n=$(wc -l < "$_f" | tr -d ' ')
    if [ "$_n" -eq 0 ]; then echo "n/a"; return; fi
    _k=$(( (_p * _n + 99) / 100 ))
    if [ "$_k" -lt 1 ]; then _k=1; fi
    sort -n "$_f" | sed -n "${_k}p"
}
# min/p50/p95/max/mean for a value file, label + unit
row() {
    _label="$1"; _f="$2"; _unit="$3"
    _n=$(wc -l < "$_f" | tr -d ' ')
    if [ "$_n" -eq 0 ]; then say "$(printf '%-34s n=0' "$_label")"; return; fi
    _min=$(sort -n "$_f" | head -1)
    _max=$(sort -n "$_f" | tail -1)
    _mean=$(awk '{s+=$1} END{printf "%.1f", s/NR}' "$_f")
    say "$(printf '%-34s n=%-4s min=%-8s p50=%-8s p95=%-8s max=%-8s mean=%-8s %s' \
        "$_label" "$_n" "$_min" "$(pctl "$_f" 50)" "$(pctl "$_f" 95)" "$_max" "$_mean" "$_unit")"
}

say "# analysis — derived from raw/*.csv, raw/*.txt   (generated $(utc))"
say "# provenance: on-device measurements on GL-MT6000 / OpenWrt 25.12.5 (ENV-ROUTER-A64)"

# --------------------------------------------------------------- P2/P3 + S2/P4
say ""
say "## P3 latency, S2 flash writes, P4 CPU — per receive (raw/payments-cycles-*.csv)"
for f in "$OUT"/payments-cycles-*.csv; do
    [ -f "$f" ] || continue
    b=$(basename "$f" .csv)
    say ""
    say "-- $b"
    : > "$TMP/lat.ok"; : > "$TMP/lat.fail"
    : > "$TMP/bytes.ok"; : > "$TMP/bytes.fail"
    : > "$TMP/ops.ok"; : > "$TMP/ops.fail"
    : > "$TMP/loop.ok"
    : > "$TMP/cpu.ok"; : > "$TMP/cpu.fail"
    awk -F',' -v t="$TMP" '
        NR==1 { for (i=1;i<=NF;i++) c[$i]=i; next }
        {
            # Outcome is taken from the CLI JSON in the row, NOT from the
            # amount_sats column: the 2026-09-18 run wrote amount_sats="na" for
            # all 20 rows because of a bug in that revision of the phase script,
            # while the very same row carries
            #   "message":"Successfully funded wallet with 20 sats",
            #   "data":{"amount_received":20}
            # (and the run ended by draining 400 sats = 20 x 20, independently
            # confirming every row credited). Row text is the reliable signal.
            ok = (index($0, "Successfully funded wallet") > 0)
            suf = ok ? "ok" : "fail"
            print $(c["latency_ms"])          > (t "/lat." suf)
            print $(c["mmc_bytes_written"])   > (t "/bytes." suf)
            print $(c["mmc_wc_delta"])        > (t "/ops." suf)
            if (ok) print $(c["loop_bytes_written"]) > (t "/loop.ok")
            if (c["cpu_ms"] != "" && $(c["cpu_ms"]) != "") print $(c["cpu_ms"]) > (t "/cpu." suf)
            if (c["amount_sats"]) print $(c["amount_sats"]) > (t "/amt.all")
        }' "$f" | tee -a "$OUTF"
    nok=$(wc -l < "$TMP/lat.ok" | tr -d ' '); nf=$(wc -l < "$TMP/lat.fail" | tr -d ' ')
    say "   successful receives: $nok    rejected receives: $nf   (total rows: $((nok+nf)))"
    if [ -s "$TMP/amt.all" ]; then
        say "   amount_sats column as written: $(sort "$TMP/amt.all" | uniq -c | tr '\n' ' ')"
    fi
    row "   latency (success)" "$TMP/lat.ok" "ms"
    row "   latency (rejected)" "$TMP/lat.fail" "ms"
    row "   mmc bytes written (success)" "$TMP/bytes.ok" "B"
    row "   mmc bytes written (rejected)" "$TMP/bytes.fail" "B"
    row "   mmc write ops (success)" "$TMP/ops.ok" "ops"
    row "   mmc write ops (rejected)" "$TMP/ops.fail" "ops"
    row "   loop0 bytes written (success)" "$TMP/loop.ok" "B"
    row "   CPU in daemon (success)" "$TMP/cpu.ok" "ms"
    # loop vs mmc equivalence check (loop layer adds no amplification? )
    if [ -s "$TMP/bytes.ok" ] && [ -s "$TMP/loop.ok" ]; then
        say "   loop0 == mmcblk0 for every successful row: $(cmp -s "$TMP/bytes.ok" "$TMP/loop.ok" && echo yes || echo NO)"
    fi
done

# ------------------------------------------------------------------ F5/F7/F8
say ""
say "## F5/F7/F8 — daemon footprint (VmRSS / VmHWM / threads / fds)"
for f in "$OUT"/idle-samples-*.csv "$OUT"/payments-samples-*.csv; do
    [ -f "$f" ] || continue
    say ""
    say "-- $(basename "$f")"
    awk -F',' '
        NR==1 { for (i=1;i<=NF;i++) c[$i]=i; next }
        NF>1 {
            if (c["vmrss_kb"]) { if ($(c["vmrss_kb"])+0 > rssmax) rssmax=$(c["vmrss_kb"])+0;
                                 if (rssmin==0 || $(c["vmrss_kb"])+0 < rssmin) rssmin=$(c["vmrss_kb"])+0 }
            if (c["vmhwm_kb"] && $(c["vmhwm_kb"])+0 > hwm) hwm=$(c["vmhwm_kb"])+0
            if (c["threads"])  { t[$(c["threads"])]++ }
            if (c["fds"])      { if (fdmin==0 || $(c["fds"])+0 < fdmin) fdmin=$(c["fds"])+0;
                                 if ($(c["fds"])+0 > fdmax) fdmax=$(c["fds"])+0 }
            n++
        }
        END {
            printf "   samples=%d  VmRSS min=%d max=%d KiB  VmHWM max=%d KiB  fds min=%d max=%d\n", n, rssmin, rssmax, hwm, fdmin, fdmax
            printf "   thread-count distribution:"
            for (v in t) printf " %s x%d", v, t[v]
            printf "\n"
        }' "$f" | tee -a "$OUTF"
done

# ---------------------------------------------------------------------- S1/S3
say ""
say "## S1/S3 — store contents and growth (boltdump output, logical DB)"
for f in "$OUT"/store-analysis-*.txt "$OUT"/payments-store-timeline-*.txt; do
    [ -f "$f" ] || continue
    say ""
    say "-- $(basename "$f")"
    grep -E '^(file_bytes|logical_bytes|proofs:|proof_record_bytes|tx_count)' "$f" | sed 's/^/   /' | tee -a "$OUTF" || true
    grep -A3 '"path": "proofs"\|"path": "pending_proofs"\|"path": "seed"' "$f" | sed 's/^/   /' | tee -a "$OUTF" || true
done

# --------------------------------------------------------------------- faults
say ""
say "## INJ-1/2/3 — fault injections (raw/faults-*.csv)"
for f in "$OUT"/faults-*.csv; do
    [ -f "$f" ] || continue
    say ""
    say "-- $(basename "$f")"
    awk -F',' '
        NR==1 { for (i=1;i<=NF;i++) c[$i]=i
                printf "   %-8s %-11s %-9s %-8s %-9s %-6s %-7s %-7s %s\n", "inj","mode","elapsed_s","store_ok","bal_ok","alive","cli_rc","err_rep","verdict"
                next }
        { bok = "n/a"
          if (c["balance_sats_before"] && c["balance_sats_after"])
              bok = ($(c["balance_sats_before"]) == $(c["balance_sats_after"])) ? "yes" : "no"
          printf "   %-8s %-11s %-9s %-8s %-9s %-6s %-7s %-7s %s\n",
              $1, $2, $(c["elapsed_s"]), $(c["store_ok"]), bok,
              $(c["service_alive"]), (c["cli_rc"] ? $(c["cli_rc"]) : "n/a"),
              (c["err_reported"] ? $(c["err_reported"]) : "n/a"), $(c["verdict"]) }' "$f" | tee -a "$OUTF"
done

# ------------------------------------------------------- injected-mint reality
say ""
say "## Environment reality checks"
for f in "$OUT"/user-hz-*.txt; do
    [ -f "$f" ] || continue
    say "-- $(basename "$f")"
    sed 's/^/   /' "$f" | tee -a "$OUTF"
done
say "-- reachable-mint gate: only mints that answer the startup probe become"
say "   acceptedMints (src/merchant/merchant.go:111-126 GetReachableMintConfigs ->"
say "   tollwallet.NewWalletPort), so a configured-but-unreachable mint is absent"
say "   from the trust list and its tokens are rejected without a dial (INJ-1a)."

say ""
say "# done: $OUTF"
