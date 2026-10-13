#!/usr/bin/env bash
# checks.sh — pure installer-contract check logic, sourced by run.sh and
# selftest.sh. No VM access here: every function takes captured text
# (uci show / logread / apk output) so the offline selftest can drive the
# same code paths with planted fixtures (#899, #911 controls).
#
# A check function emits exactly one verdict line on stdout:
#   <PASS|FAIL> <detail...>
# The caller maps it onto a check id via record(). NOT_VERIFIED/SKIP are
# lane-level statuses (environment could not provide input); they are
# decided by run.sh, never by these functions.

# ic_section_ports <uci-show-uhttpd-text> <section> — ports that section
# listens on, space-separated. Handles multi-value uci list lines such as
#   uhttpd.main.listen_http='0.0.0.0:8090' '[::]:8090'
ic_section_ports() {
    printf '%s\n' "$1" | awk -v sec="$2" '
        $0 ~ ("^uhttpd\\." sec "\\.listen_(http|https)=") {
            line = $0
            sub(/^[^=]*=/, "", line)
            n = split(line, tok, / /)
            for (i = 1; i <= n; i++) {
                t = tok[i]
                gsub(/\x27/, "", t)
                if (t ~ /:[0-9]+$/) { sub(/.*:/, "", t); print t }
            }
        }'
}

# ic_uhttpd_sections <uci-show-uhttpd-text>
# The #899 invariant: no shipped uhttpd section may end the install
# listenerless (uhttpd exits with "No sockets bound" and procd crash-loops
# a bare instance). Port liveness alone cannot detect a torn section whose
# ports are claimed — or dropped — by another instance; this reads the
# config itself.
ic_uhttpd_sections() {
    local text="$1" listenerless="" secs sec ports detail
    secs="$(printf '%s\n' "$text" | sed -n 's/^uhttpd\.\([^.]*\)\..*/\1/p' | sort -u)"
    [ -n "$secs" ] || { echo "FAIL 'uci show uhttpd' produced no sections — cannot verify the #899 listenerless-section invariant"; return; }
    for sec in $secs; do
        ports="$(ic_section_ports "$text" "$sec" | tr '\n' ' ' | sed 's/ *$//')"
        [ -n "$ports" ] || listenerless="$listenerless $sec"
    done
    if [ -n "$listenerless" ]; then
        echo "FAIL listenerless uhttpd section(s):$listenerless — the #899 tear (an instance with no listen_http/listen_https crashes with 'No sockets bound')"
    else
        detail="$(for sec in $secs; do printf '%s=%s ' "$sec" "$(ic_section_ports "$text" "$sec" | sort -n | tr '\n' ',' | sed 's/,$//')"; done | sed 's/ *$//')"
        echo "PASS every uhttpd section holds listeners ($detail)"
    fi
}

# ic_port_ownership <mapping:board|luci> <uci-show-uhttpd-text>
# The entry-port mapping: board mode — uhttpd.main on 8090/8443 with the
# admin instance on the entry pair 8080/443; luci mode — the mirror image.
ic_port_ownership() {
    local mapping="$1" text="$2" main_ports admin_ports missing="" pair
    case "$mapping" in
        board) pair="main:8090 main:8443 admin:8080 admin:443" ;;
        luci)  pair="main:8080 main:443 admin:8090 admin:8443" ;;
        *) { echo "FAIL unknown mapping '$mapping' — ownership cannot be verified"; return; } ;;
    esac
    main_ports="$(ic_section_ports "$text" main | tr '\n' ' ')"
    admin_ports="$(ic_section_ports "$text" admin | tr '\n' ' ')"
    local sp sec port
    for sp in $pair; do
        sec="${sp%%:*}"; port="${sp##*:}"
        case " $(if [ "$sec" = main ]; then printf '%s' "$main_ports"; else printf '%s' "$admin_ports"; fi) " in
            *" $port "*) : ;;
            *) missing="$missing $sec:$port" ;;
        esac
    done
    if [ -n "$missing" ]; then
        echo "FAIL mapping=$mapping but the expected owners are absent:$missing (main='[$main_ports]' admin='[$admin_ports]')"
    else
        echo "PASS mapping=$mapping: main=[$main_ports] admin=[$admin_ports]"
    fi
}

# ic_crash_logs <logread-text>
# Crash-loop signatures the lane has actually seen: uhttpd's zero-listener
# exit (#899) and kernel-level process deaths.
ic_crash_logs() {
    local text="$1" n_sockets n_seg
    n_sockets="$(printf '%s\n' "$text" | grep -c 'No sockets bound' || true)"
    n_seg="$(printf '%s\n' "$text" | grep -c -E 'Segmentation fault|core dump' || true)"
    if [ "$n_sockets" -gt 0 ] || [ "$n_seg" -gt 0 ]; then
        echo "FAIL crash signatures in logread: 'No sockets bound'=$n_sockets, segfault/core=$n_seg"
    else
        echo "PASS no crash signatures ('No sockets bound'=0, segfault/core=0)"
    fi
}

# ic_preinst_refusal <apk-exit-code> <refuse-log-text> <registered:0|1> <running:0|1>
# The #907 invariant as #911 found it in the field: the REQUIRED outcome is
# a clean refusal (nonzero exit, guidance visible, nothing registered). The
# OBSERVED apk-era reality may instead be the #911 shape — apk-tools 25.12
# executes pre-install at commit time, records a scriptlet error, suppresses
# the scriptlet's stderr, and COMPLETES the transaction (package registered,
# service started) while still exiting 1. That shape is reported faithfully
# here as a FAIL with the observed facts — never massaged into a pass, and
# never hidden behind the exit code alone.
ic_preinst_refusal() {
    local ec="$1" log="$2" registered="$3" running="$4"
    local guidance=0
    printf '%s' "$log" | grep -q "root has no password — refusing to install" && guidance=1
    if [ "$ec" -ne 0 ] && [ "$guidance" = 1 ] && [ "$registered" = 0 ]; then
        echo "PASS clean refusal on the empty-root device (exit $ec, guidance printed, nothing registered)"
        return
    fi
    local shape="exit=$ec guidance=$([ "$guidance" = 1 ] && echo visible || echo suppressed) registered=$([ "$registered" = 1 ] && echo yes || echo no) service=$([ "$running" = 1 ] && echo running || echo stopped)"
    if [ "$ec" -ne 0 ] && [ "$registered" = 1 ]; then
        echo "FAIL required refusal did not happen — apk-tools completed the transaction despite the failing preinst (#911): $shape"
    elif [ "$ec" -eq 0 ]; then
        echo "FAIL apk add exited 0 on an empty-root device — the preinst gate did not even fire: $shape"
    else
        echo "FAIL refusal incomplete: $shape"
    fi
}
