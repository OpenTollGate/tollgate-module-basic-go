#!/usr/bin/env bash
#
# Installer-contract lane for a built TollGate package (apk era).
#
#   tests/installer-contract/run.sh --artifact <pkg.apk> --host <ssh-spec> [options]
#
# Boots a PRISTINE OpenWrt 25.12 (apk-era) VM that already answers key-based
# root ssh, installs the package the way the docs tell an operator to —
# including TOLLGATE_ADMIN_PASSWORD / TOLLGATE_PRIVATE_WIFI_PASSWORD in the
# environment — and asserts the installer's credential and convergence
# contracts against the live device. Everything this lane checks was first
# verified by hand on the vlab lane on 2026-10-11; this script exists so the
# next person does not have to redo the plumbing (see #888's evidence).
#
# Every check prints one line:
#
#   ICCHECK <id> <PASS|FAIL|SKIP> <detail...>
#   ICRESULT total=N pass=N fail=N skip=N
#   ICEXIT <0|1>
#
# KNOWN-RED: `credential-applied` is the acceptance criterion of #888 —
# apk-tools on 25.12 does not deliver the install-time environment to the
# uci-defaults script, so the operator-chosen password is NOT applied and
# this check is expected to FAIL until #888 lands a delivery mechanism.
# The lane is shipped red on purpose: a fence that cannot fail is
# decoration. `provisional-lifecycle` pins the safe degraded behavior that
# ships meanwhile (random provisional credential, never printed, marker
# present, first-run page offered).
#
# What this lane does NOT cover (needs the physical lane): radios and SSID
# derivation, br-private flows, band steering, real NoDogSplash captivity.
#
# Options:
#   --artifact FILE   built package (.apk), required
#   --host SPEC       ssh spec with root key access to the VM, required
#                     (e.g. root@127.0.0.1 with --port 2222 --ssh-key ~/.ssh/k)
#   --port N          ssh port (default 22)
#   --ssh-key FILE    identity file for the ssh spec
#   --serial SPEC     serial console as HOST:PORT (telnet) — REQUIRED for the
#                     pristine phase: `firstboot -y` wipes /etc (including
#                     dropbear's authorized_keys and any network fix), and the
#                     lane re-provisions dhcp + the key over serial before
#                     ssh can return. Without --serial, pass --skip-pristine.
#   --skip-pristine   do not `firstboot -y` before installing (dev only —
#                     makes credential-state assertions unreliable)
#   --skip-reboot     skip the persistence phase
#   --keep            keep the run's remote scratch dir
#   --out DIR         evidence dir (default: <tmpdir>/installer-contract.out)

set -u

SELF_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SELF_DIR/../.." && pwd)"

ARTIFACT=""
HOST=""
PORT="22"
SSH_KEY=""
SERIAL=""
SKIP_PRISTINE=0
SKIP_REBOOT=0
KEEP=0
OUT=""

usage() {
    sed -n '2,45p' "$0" | sed 's/^# \{0,1\}//'
    exit 2
}

while [ $# -gt 0 ]; do
    case "$1" in
        --artifact) ARTIFACT="${2:?}"; shift 2 ;;
        --host) HOST="${2:?}"; shift 2 ;;
        --port) PORT="${2:?}"; shift 2 ;;
        --ssh-key) SSH_KEY="${2:?}"; shift 2 ;;
        --serial) SERIAL="${2:?}"; shift 2 ;;
        --skip-pristine) SKIP_PRISTINE=1; shift ;;
        --skip-reboot) SKIP_REBOOT=1; shift ;;
        --keep) KEEP=1; shift ;;
        --out) OUT="${2:?}"; shift 2 ;;
        *) usage ;;
    esac
done

[ -n "$ARTIFACT" ] && [ -n "$HOST" ] || usage
[ -f "$ARTIFACT" ] || { echo "ICE XIT 1"; echo "artifact not found: $ARTIFACT" >&2; exit 1; }

TMPDIR_RUN="$(mktemp -d /tmp/installer-contract.XXXXXX)"
OUT="${OUT:-$TMPDIR_RUN/out}"
mkdir -p "$OUT"
# The chosen credentials are generated per-run and written ONLY here, root-only
# readable, exactly like the one-time channel the installer itself uses.
CHOSEN_ADMIN="$OUT/admin-password"
CHOSEN_WIFI="$OUT/wifi-password"
umask 077
tr -dc 'A-Za-z0-9' < /dev/urandom | head -c 20 > "$CHOSEN_ADMIN"
tr -dc 'A-Za-z0-9' < /dev/urandom | head -c 20 > "$CHOSEN_WIFI"

PASS=0; FAIL=0; SKIP=0
FAILED_IDS=""

record() { # record <id> <PASS|FAIL|SKIP> <detail>
    printf 'ICCHECK %s %s %s\n' "$1" "$2" "$3"
    case "$2" in
        PASS) PASS=$((PASS+1)) ;;
        FAIL) FAIL=$((FAIL+1)); FAILED_IDS="$FAILED_IDS $1" ;;
        SKIP) SKIP=$((SKIP+1)) ;;
    esac
}

finish() {
    printf 'ICRESULT total=%d pass=%d fail=%d skip=%d\n' \
        "$((PASS+FAIL+SKIP))" "$PASS" "$FAIL" "$SKIP"
    [ -n "$FAILED_IDS" ] && printf 'ICFAILED%s\n' "$FAILED_IDS"
    printf 'ICEXIT %d\n' "$([ "$FAIL" -eq 0 ] && echo 0 || echo 1)"
    exit "$([ "$FAIL" -eq 0 ] && echo 0 || echo 1)"
}

SSH_OPTS="-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ConnectTimeout=8 -p $PORT"
[ -n "$SSH_KEY" ] && SSH_OPTS="$SSH_OPTS -i $SSH_KEY"

vm() { # vm <command...> — run on the VM, stdout only
    ssh $SSH_OPTS "root@$HOST" "$@" 2>/dev/null
}

vm_wait_ssh() { # vm_wait_ssh <max-seconds>
    local i
    for i in $(seq 1 $((($1 + 4) / 5))); do
        sleep 5
        vm 'echo up' | grep -q '^up$' && return 0
    done
    return 1
}

# serial_cmd <SPEC host:port> <command> [wait-seconds]
# Drives the device's serial console (telnet) — the only channel that
# survives `firstboot -y`. A pristine OpenWrt root with no password gives
# the console an unconditional root shell, which is exactly what the
# re-provisioning below needs.
serial_cmd() {
    local spec="$1" cmd="$2" wait="${3:-3}"
    local shost sport
    shost="${spec%%:*}"; sport="${spec##*:}"
    python3 - "$shost" "$sport" "$cmd" "$wait" <<'PYEOF'
import socket, sys, time
host, port, cmd, wait = sys.argv[1], int(sys.argv[2]), sys.argv[3], float(sys.argv[4])
s = socket.create_connection((host, port), timeout=8)
s.settimeout(1.0)
def drain():
    out = b""
    try:
        while True:
            d = s.recv(4096)
            if not d:
                break
            out += d
    except socket.timeout:
        pass
    return out.decode(errors="replace")
s.send(b"\n"); time.sleep(0.6); drain()
s.send(cmd.encode() + b"\n"); time.sleep(wait)
print(drain())
s.close()
PYEOF
}

# serial_reprovision — after firstboot, /etc is gone: restore the two things
# ssh needs (an address on the qemu user-net, the lane's dropbear key) over
# the serial console. Idempotent; safe to call on an already-provisioned box.
serial_reprovision() {
    [ -n "$SERIAL" ] || return 1
    [ -n "$SSH_KEY" ] || return 1
    local pub
    pub="$(cat "${SSH_KEY}.pub")" || return 1
    serial_cmd "$SERIAL" "uci set network.lan.proto='dhcp'; uci commit network; /etc/init.d/network restart; sleep 3" 8 >/dev/null 2>&1
    serial_cmd "$SERIAL" "mkdir -p /etc/dropbear && echo '$pub' > /etc/dropbear/authorized_keys && chmod 600 /etc/dropbear/authorized_keys && echo REPROVISIONED" 4 2>/dev/null | grep -q REPROVISIONED
}

ARTIFACT_SHA="$(sha256sum "$ARTIFACT" | awk '{print $1}')"
ARTIFACT_NAME="$(basename "$ARTIFACT")"

# --- 0. preflight -----------------------------------------------------------
if ! vm 'echo up' | grep -q '^up$'; then
    if [ -n "$SERIAL" ] && serial_reprovision && vm_wait_ssh 60; then
        record preflight:ssh PASS "$HOST:$PORT reachable after serial bootstrap (fresh-boot VM)"
    else
        record preflight:ssh FAIL "no ssh: $HOST:$PORT (see README — vlab VMs need the slirp dhcp fix and a key in /etc/dropbear/authorized_keys; pass --serial to bootstrap automatically)"
        finish
    fi
else
    record preflight:ssh PASS "$HOST:$PORT answers key-based root ssh"
fi

REL="$(vm ". /etc/openwrt_release 2>/dev/null && echo \$DISTRIB_RELEASE")"
case "$REL" in
    25.*) record preflight:apk-era PASS "OpenWrt $REL (apk era)" ;;
    *) record preflight:apk-era FAIL "OpenWrt '${REL:-unknown}' — this lane targets the 25.12 apk era" ;;
esac

command -v openssl >/dev/null 2>&1 \
    && record preflight:openssl PASS "lane host has openssl (credential comparison)" \
    || record preflight:openssl FAIL "openssl missing on the lane host (sha256crypt comparison)"

# --- 1. pristine device -----------------------------------------------------
if [ "$SKIP_PRISTINE" -eq 1 ]; then
    record pristine:factory-reset SKIP "--skip-pristine"
else
    if [ -z "$SERIAL" ]; then
        record pristine:factory-reset FAIL "the pristine phase needs --serial (firstboot wipes dropbear keys; the lane must re-provision over the console) — or pass --skip-pristine on an already-clean device"
        finish
    fi
    vm 'firstboot -y; sync; reboot -f' >/dev/null 2>&1 &
    sleep 30
    if vm_wait_ssh 120; then
        record pristine:factory-reset PASS "firstboot + forced reboot, ssh back"
    elif serial_reprovision && vm_wait_ssh 60; then
        record pristine:factory-reset PASS "firstboot + forced reboot; dhcp/key re-provisioned over serial"
    else
        record pristine:factory-reset FAIL "device did not come back after firstboot (serial reprovision failed — is the console at $SERIAL?)"
        finish
    fi
    # Trust nothing about the reset: on at least one 25.12.5 x86 image
    # `firstboot -y && reboot` silently kept /etc (dropbear keys, an installed
    # tollgate-wrt, even root's old hash), which turns every downstream
    # credential assertion into a lie. Verify the clean state explicitly.
    CLEAN_PKGS="$(vm 'apk info 2>/dev/null | grep -c "^tollgate-wrt" || true')"
    CLEAN_SHADOW="$(vm 'grep "^root:" /etc/shadow' | cut -d: -f2)"
    if [ "$CLEAN_PKGS" = "0" ] && [ -z "$CLEAN_SHADOW" ]; then
        record pristine:verified PASS "no tollgate-wrt registered and root credential empty"
    else
        record pristine:verified FAIL "device is not pristine (tollgate-wrt=$CLEAN_PKGS, root hash='${CLEAN_SHADOW:0:8}…') — firstboot did not reset this image; boot the VM fresh (snapshot) or fix its reset path"
        finish
    fi
fi

# --- 2. install with operator-chosen credentials ----------------------------
vm 'mkdir -p /tmp/ic-lane' >/dev/null 2>&1
if cat "$ARTIFACT" | vm "cat > /tmp/ic-lane/$ARTIFACT_NAME" 2>/dev/null; then
    REMOTE_SHA="$(vm "sha256sum /tmp/ic-lane/$ARTIFACT_NAME" | awk '{print $1}')"
    [ "$REMOTE_SHA" = "$ARTIFACT_SHA" ] \
        && record install:transfer PASS "$ARTIFACT_NAME sha256 $REMOTE_SHA" \
        || record install:transfer FAIL "sha256 mismatch after transfer ($REMOTE_SHA)"
else
    record install:transfer FAIL "could not push the artifact over ssh"
    finish
fi

ADMIN_PW="$(cat "$CHOSEN_ADMIN")"
WIFI_PW="$(cat "$CHOSEN_WIFI")"
vm "TOLLGATE_ADMIN_PASSWORD='$ADMIN_PW' TOLLGATE_PRIVATE_WIFI_PASSWORD='$WIFI_PW' apk add --allow-untrusted /tmp/ic-lane/$ARTIFACT_NAME" \
    > "$OUT/install.log" 2>&1
INSTALL_EC=$?
if [ "$INSTALL_EC" -eq 0 ] && vm 'apk info 2>/dev/null | grep -q "^tollgate-wrt"'; then
    record install:apk-add PASS "apk add exit 0, tollgate-wrt registered"
else
    record install:apk-add FAIL "apk add exit $INSTALL_EC (see $OUT/install.log)"
    finish
fi

# --- 3. contract: no credential material in the install output --------------
HITS=$(grep -c -F -e "$ADMIN_PW" -e "$WIFI_PW" "$OUT/install.log" || true)
[ "$HITS" -eq 0 ] \
    && record contract:no-plaintext PASS "neither chosen value appears in the install output" \
    || record contract:no-plaintext FAIL "$HITS plaintext hit(s) of the chosen credentials in install output"

# --- 4. contract: the operator-chosen password was applied (#888) -----------
HASH="$(vm 'grep "^root:" /etc/shadow' | cut -d: -f2)"
case "$HASH" in
    '$5$'*|'$6$'*)
        SALT="$(printf '%s' "$HASH" | cut -d'$' -f3)"
        COMPUTED="$(printf '%s' "$ADMIN_PW" | openssl passwd -5 -salt "$SALT" -stdin)"
        if [ "$COMPUTED" = "$HASH" ]; then
            record contract:credential-applied PASS "root hash matches the operator-chosen password"
        else
            record contract:credential-applied FAIL "root hash does NOT match the operator-chosen password — the env did not reach the uci-defaults script (#888; acceptance criterion for its fix)"
        fi
        ;;
    '')
        record contract:credential-applied FAIL "root credential is EMPTY after install — fail-closed path did not engage either"
        ;;
    *)
        record contract:credential-applied SKIP "root credential state not comparable ($HASH)"
        ;;
esac

# --- 5. contract: the safe degraded path while #888 stands ------------------
if vm 'test -f /etc/tollgate/admin-credential-provisional'; then
    if grep -q "FIRST-RUN" "$OUT/install.log"; then
        record contract:provisional-lifecycle PASS "provisional credential generated silently + first-run page offered (the safe degraded path of #888)"
    else
        record contract:provisional-lifecycle FAIL "marker present but no FIRST-RUN guidance in the install output"
    fi
else
    record contract:provisional-lifecycle SKIP "no provisional marker (operator-chosen path — expected once #888 is fixed)"
fi

# --- 6. services -------------------------------------------------------------
vm '/etc/init.d/tollgate-wrt status 2>/dev/null | head -1' | grep -q running \
    && record services:tollgate-wrt PASS "$(vm '/etc/init.d/tollgate-wrt status 2>/dev/null | head -1')" \
    || record services:tollgate-wrt FAIL "tollgate-wrt not running"
vm '/etc/init.d/nodogsplash status 2>/dev/null | head -1' | grep -q running \
    && record services:nodogsplash PASS "nodogsplash running" \
    || record services:nodogsplash FAIL "nodogsplash not running"

# --- 7. listeners and the HTTP plane -----------------------------------------
LISTEN="$(vm 'netstat -ltn 2>/dev/null' | awk '{print $4}')"
for p in 80 443 2050 2051 8080 8090 8443; do
    echo "$LISTEN" | grep -q ":$p\b" \
        && record "listeners:$p" PASS "listening" \
        || record "listeners:$p" FAIL "nothing listening on :$p"
done
vm 'wget -q -T 4 -O /dev/null http://127.0.0.1:80/' \
    && record http:guest-entry PASS "GET / on :80 answered" \
    || record http:guest-entry FAIL "GET / on :80 failed"
vm 'wget -q --no-check-certificate -T 4 -O /dev/null https://127.0.0.1:443/' \
    && record http:admin-tls PASS "admin pair answers TLS on :443" \
    || record http:admin-tls FAIL "no TLS answer on :443"

# --- 8. entry-port mapping ----------------------------------------------------
MAPPING="$(vm 'cat /etc/tollgate/entry-ui-mapping 2>/dev/null')"
case "$MAPPING" in
    board|luci) record mapping:entry-ui PASS "$MAPPING" ;;
    *) record mapping:entry-ui FAIL "no readable entry-ui mapping ('$MAPPING')" ;;
esac

# --- 9. persistence across a reboot ------------------------------------------
if [ "$SKIP_REBOOT" -eq 1 ]; then
    record persistence:reboot SKIP "--skip-reboot"
else
    SALT_BEFORE="$SALT"
    MAP_BEFORE="$MAPPING"
    vm 'sync; reboot' >/dev/null 2>&1 &
    if vm_wait_ssh 180; then
        HASH2="$(vm 'grep "^root:" /etc/shadow' | cut -d: -f2)"
        SALT2="$(printf '%s' "$HASH2" | cut -d'$' -f3)"
        [ "$SALT2" = "$SALT_BEFORE" ] \
            && record persistence:credential PASS "root credential survived the reboot" \
            || record persistence:credential FAIL "root credential changed across reboot"
        MAP2="$(vm 'cat /etc/tollgate/entry-ui-mapping 2>/dev/null')"
        [ "$MAP2" = "$MAP_BEFORE" ] \
            && record persistence:mapping PASS "entry-ui mapping stable ($MAP2)" \
            || record persistence:mapping FAIL "mapping changed across reboot ($MAP_BEFORE -> $MAP2)"
        vm '/etc/init.d/tollgate-wrt status 2>/dev/null | head -1' | grep -q running \
            && record persistence:services PASS "tollgate-wrt auto-started after reboot" \
            || record persistence:services FAIL "tollgate-wrt did not come back after reboot"
    else
        record persistence:reboot FAIL "device did not return after reboot"
    fi
fi

# --- cleanup ------------------------------------------------------------------
if [ "$KEEP" -ne 1 ]; then
    vm 'rm -rf /tmp/ic-lane' >/dev/null 2>&1
fi
printf 'evidence: %s (admin/wifi values: root-only %s)\n' "$OUT" "$CHOSEN_ADMIN"
finish
