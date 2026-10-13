#!/usr/bin/env bash
#
# Installer-contract lane for a built TollGate package (apk era).
#
#   tests/installer-contract/run.sh --artifact <pkg.apk> --host <ssh-spec> [options]
#
# Boots a PRISTINE OpenWrt 25.12 (apk-era) VM that already answers key-based
# root ssh, and asserts the rc3 installer contract against the live device:
# the preinst REFUSES a passwordless root (#907), the package itself sets no
# credential at all (#902), the operator's own root password survives the
# install byte-identically, and services/listeners/mapping converge and
# persist across a reboot. Everything this lane checks was first verified by
# hand on the vlab lane on 2026-10-11; this script exists so the next person
# does not have to redo the plumbing (see #888's evidence).
#
# Every check prints one line:
#
#   ICCHECK <id> <PASS|FAIL|SKIP|NOT_VERIFIED> <detail...>
#   ICRESULT total=N pass=N fail=N skip=N not_verified=N
#   ICFAILED <ids...>   (FAIL and NOT_VERIFIED ids)
#   ICEXIT <0|1>
#
# NOT_VERIFIED is fail-closed: a required check whose input could not be
# obtained (no VM, phase skipped without --dev, provenance not recorded) is
# never a PASS and never a silent SKIP — it fails the run (requirement of
# the installation acceptance gate: unexecuted means NOT VERIFIED).
# verdict.json in the evidence dir carries the machine-readable form:
# per-check statuses, artifact sha256, source SHA, image identity, lane
# environment.
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
# KNOWN-RED (kept faithfully red, never loosened):
#   - `contract:preinst-refuses-empty-root`: #911 proved apk-tools 25.12
#     COMPLETES the transaction (package registered, service started,
#     exit 1, guidance suppressed) despite the failing preinst. The check
#     reports that observed shape verbatim; it goes green only when apk
#     actually refuses.
#   - `listeners:uhttpd-sections` / `mapping:port-ownership` /
#     `listeners:8090` / `listeners:8443`: #890/#899 — uhttpd.main ends
#     the fresh-image install listenerless. Port liveness alone cannot
#     catch the tear when another instance claims the ports; the section
#     and ownership checks read `uci show uhttpd` itself.
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
#   --source-sha SHA  the commit the artifact was built from — recorded in
#                     verdict.json; without it provenance is NOT_VERIFIED
#   --dev             allow --skip-pristine/--skip-reboot as plain SKIPs
#                     (dev iterations only; without --dev they are
#                     NOT_VERIFIED and fail the run)
#   --skip-pristine   do not `firstboot -y` before installing (dev only —
#                     makes credential-state assertions unreliable)
#   --skip-reboot     skip the persistence phase (dev only)
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
SOURCE_SHA=""
DEV=0
SKIP_PRISTINE=0
SKIP_REBOOT=0
KEEP=0
OUT=""

usage() {
    sed -n '2,60p' "$0" | sed 's/^# \{0,1\}//'
    exit 2
}

while [ $# -gt 0 ]; do
    case "$1" in
        --artifact) ARTIFACT="${2:?}"; shift 2 ;;
        --host) HOST="${2:?}"; shift 2 ;;
        --port) PORT="${2:?}"; shift 2 ;;
        --ssh-key) SSH_KEY="${2:?}"; shift 2 ;;
        --serial) SERIAL="${2:?}"; shift 2 ;;
        --source-sha) SOURCE_SHA="${2:?}"; shift 2 ;;
        --dev) DEV=1; shift ;;
        --skip-pristine) SKIP_PRISTINE=1; shift ;;
        --skip-reboot) SKIP_REBOOT=1; shift ;;
        --keep) KEEP=1; shift ;;
        --out) OUT="${2:?}"; shift 2 ;;
        *) usage ;;
    esac
done

[ -n "$ARTIFACT" ] && [ -n "$HOST" ] || usage
[ -f "$ARTIFACT" ] || { echo "ICE XIT 1"; echo "artifact not found: $ARTIFACT" >&2; exit 1; }

# shellcheck source=checks.sh
. "$SELF_DIR/checks.sh"

TMPDIR_RUN="$(mktemp -d "${TMPDIR:-/tmp}/installer-contract.XXXXXX")"
OUT="${OUT:-$TMPDIR_RUN/out}"
mkdir -p "$OUT"
# The lane-chosen root password is generated per-run and written ONLY here,
# root-only readable — the lane plays the operator who sets it before install.
CHOSEN_ADMIN="$OUT/admin-password"
umask 077
tr -dc 'A-Za-z0-9' < /dev/urandom | head -c 20 > "$CHOSEN_ADMIN"

PASS=0; FAIL=0; SKIP=0; NV=0
FAILED_IDS=""
# checks.tsv: the raw per-check table verdict.json is built from.
CHECKS_TSV="$OUT/checks.tsv"
: > "$CHECKS_TSV"
# run facts accumulated for the verdict metadata.
META_IMAGE="$OUT/image-identity.txt"
: > "$META_IMAGE"

record() { # record <id> <PASS|FAIL|SKIP|NOT_VERIFIED> <detail>
    printf 'ICCHECK %s %s %s\n' "$1" "$2" "$3"
    printf '%s\t%s\t%s\n' "$1" "$2" "$(printf '%s' "$3" | tr -d '\t\r\n')" >> "$CHECKS_TSV"
    case "$2" in
        PASS) PASS=$((PASS+1)) ;;
        FAIL) FAIL=$((FAIL+1)); FAILED_IDS="$FAILED_IDS $1" ;;
        SKIP) SKIP=$((SKIP+1)) ;;
        NOT_VERIFIED) NV=$((NV+1)); FAILED_IDS="$FAILED_IDS $1" ;;
    esac
}

record_verdict() { # record_verdict <id> <verdict-line from checks.sh>
    local id="$1" line="$2" status detail
    status="$(printf '%s' "$line" | awk '{print $1}')"
    detail="$(printf '%s' "$line" | cut -s -d' ' -f2-)"
    [ -n "$detail" ] || detail="(no detail)"
    record "$id" "$status" "$detail"
}

finish() {
    printf 'ICRESULT total=%d pass=%d fail=%d skip=%d not_verified=%d\n' \
        "$((PASS+FAIL+SKIP+NV))" "$PASS" "$FAIL" "$SKIP" "$NV"
    [ -n "$FAILED_IDS" ] && printf 'ICFAILED%s\n' "$FAILED_IDS"
    python3 - "$CHECKS_TSV" "$OUT/verdict.json" \
        "$ARTIFACT_NAME" "$ARTIFACT_SHA" "$SOURCE_SHA" "$META_IMAGE" \
        "$PASS" "$FAIL" "$SKIP" "$NV" "$*" <<'PYEOF'
import datetime, json, platform, sys
(tsv, out, art_name, art_sha, source_sha, image_path,
 pass_, fail_, skip_, nv, extra) = sys.argv[1:12]
checks = []
for line in open(tsv, encoding="utf-8", errors="replace"):
    parts = line.rstrip("\n").split("\t")
    if len(parts) >= 3:
        checks.append({"id": parts[0], "status": parts[1], "detail": parts[2]})
image = {}
try:
    for line in open(image_path, encoding="utf-8", errors="replace"):
        if ": " in line:
            k, v = line.split(": ", 1)
            image[k.strip()] = v.strip()
except OSError:
    pass
doc = {
    "schema_version": 1,
    "generated_utc": datetime.datetime.now(datetime.timezone.utc).isoformat(),
    "lane_host": platform.platform(),
    "artifact": {"name": art_name, "sha256": art_sha},
    "source_sha": source_sha or None,
    "image": image,
    "summary": {
        "pass": int(pass_), "fail": int(fail_), "skip": int(skip_),
        "not_verified": int(nv),
        "exit": 0 if int(fail_) == 0 and int(nv) == 0 else 1,
    },
    "checks": checks,
}
with open(out, "w", encoding="utf-8") as fh:
    json.dump(doc, fh, indent=2)
PYEOF
    printf 'ICEXIT %d\n' "$([ "$FAIL" -eq 0 ] && [ "$NV" -eq 0 ] && echo 0 || echo 1)"
    exit "$([ "$FAIL" -eq 0 ] && [ "$NV" -eq 0 ] && echo 0 || echo 1)"
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
# The waits are deliberately generous: a network restart keeps the shell busy
# well past 4s, and a marker sent into that window used to be drained before
# it was echoed — reading as a reprovision failure on a healthy device.
serial_reprovision() {
    [ -n "$SERIAL" ] || return 1
    [ -n "$SSH_KEY" ] || return 1
    local pub
    pub="$(cat "${SSH_KEY}.pub")" || return 1
    serial_cmd "$SERIAL" "uci set network.lan.proto='dhcp'; uci commit network; /etc/init.d/network restart" 15 >/dev/null 2>&1
    serial_cmd "$SERIAL" "mkdir -p /etc/dropbear && echo '$pub' > /etc/dropbear/authorized_keys && chmod 600 /etc/dropbear/authorized_keys && echo REPROVISIONED" 10 2>/dev/null | grep -q REPROVISIONED
}

ARTIFACT_SHA="$(sha256sum "$ARTIFACT" | awk '{print $1}')"
ARTIFACT_NAME="$(basename "$ARTIFACT")"

# --- 0. preflight -----------------------------------------------------------
if ! vm 'echo up' | grep -q '^up$'; then
    if [ -n "$SERIAL" ] && serial_reprovision && vm_wait_ssh 60; then
        record preflight:ssh PASS "$HOST:$PORT reachable after serial bootstrap (fresh-boot VM)"
    else
        record preflight:ssh NOT_VERIFIED "no ssh: $HOST:$PORT — the VM is unavailable, so no required invariant below was executed (see README — vlab VMs need the slirp dhcp fix and a key in /etc/dropbear/authorized_keys; pass --serial to bootstrap automatically)"
        finish
    fi
else
    record preflight:ssh PASS "$HOST:$PORT answers key-based root ssh"
fi

REL="$(vm ". /etc/openwrt_release 2>/dev/null && echo \$DISTRIB_RELEASE")"
{
    printf 'release: %s\n' "$REL"
    vm 'cat /etc/openwrt_release 2>/dev/null'
    printf 'kernel: %s\n' "$(vm 'uname -a 2>/dev/null')"
    printf 'apk_version: %s\n' "$(vm 'apk --version 2>/dev/null')"
    printf 'board: %s\n' "$(vm 'ubus call system board 2>/dev/null | grep -o "\"board_name\"[^,]*"' || true)"
} > "$META_IMAGE" 2>/dev/null || true
case "$REL" in
    25.*) record preflight:apk-era PASS "OpenWrt $REL (apk era)" ;;
    *) record preflight:apk-era FAIL "OpenWrt '${REL:-unknown}' — this lane targets the 25.12 apk era" ;;
esac

command -v openssl >/dev/null 2>&1 \
    && record preflight:openssl PASS "lane host has openssl (credential comparison)" \
    || record preflight:openssl FAIL "openssl missing on the lane host (sha256crypt comparison)"

# --- 0b. provenance: bind this run to exact bytes and a commit ---------------
record provenance:artifact-sha256 PASS "$ARTIFACT_NAME sha256 $ARTIFACT_SHA"
if [ -n "$SOURCE_SHA" ]; then
    record provenance:source-sha PASS "artifact recorded as built from $SOURCE_SHA"
else
    record provenance:source-sha NOT_VERIFIED "no --source-sha recorded — the artifact is not bound to a commit for this run"
fi

# --- 1. pristine device -----------------------------------------------------
if [ "$SKIP_PRISTINE" -eq 1 ]; then
    if [ "$DEV" -eq 1 ]; then
        record pristine:factory-reset SKIP "--skip-pristine (--dev)"
    else
        record pristine:factory-reset NOT_VERIFIED "--skip-pristine without --dev: the pristine phase is required — an unverified-clean device cannot back the credential assertions below"
        finish
    fi
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

# --- 2. the preinst must refuse a passwordless root (#907) -------------------
vm 'mkdir -p /tmp/ic-lane' >/dev/null 2>&1
cat "$ARTIFACT" | vm "cat > /tmp/ic-lane/$ARTIFACT_NAME" 2>/dev/null \
    && record install:transfer PASS "$ARTIFACT_NAME sha256 $(vm "sha256sum /tmp/ic-lane/$ARTIFACT_NAME" | awk '{print $1}')" \
    || { record install:transfer FAIL "could not push the artifact over ssh"; finish; }

vm "apk add --allow-untrusted /tmp/ic-lane/$ARTIFACT_NAME" > "$OUT/refuse.log" 2>&1
REFUSE_EC=$?
REFUSE_REGISTERED=0; vm 'apk info 2>/dev/null | grep -q "^tollgate-wrt"' && REFUSE_REGISTERED=1
REFUSE_RUNNING=0; vm '/etc/init.d/tollgate-wrt status 2>/dev/null' | grep -q running && REFUSE_RUNNING=1
# The observed shape (exit code, guidance visibility, registration, service
# state) is recorded verbatim; the verdict stays the strict #907 contract.
# On apk 25.12 builds up to rc3 this is the #911 shape and stays RED.
record_verdict "contract:preinst-refuses-empty-root" \
    "$(ic_preinst_refusal "$REFUSE_EC" "$(cat "$OUT/refuse.log" 2>/dev/null)" "$REFUSE_REGISTERED" "$REFUSE_RUNNING")"

# --- 3. the operator flow: set a root password, then install ------------------
ADMIN_PW="$(cat "$CHOSEN_ADMIN")"
if vm "printf '%s\n%s\n' '$ADMIN_PW' '$ADMIN_PW' | passwd root >/dev/null 2>&1"; then
    record flow:operator-sets-password PASS "root password set by the operator before install"
else
    record flow:operator-sets-password FAIL "could not set the root password over ssh"
    finish
fi
HASH_BEFORE="$(vm 'grep "^root:" /etc/shadow' | cut -d: -f2)"

vm "apk add --allow-untrusted /tmp/ic-lane/$ARTIFACT_NAME" > "$OUT/install.log" 2>&1
INSTALL_EC=$?
if [ "$INSTALL_EC" -eq 0 ] && vm 'apk info 2>/dev/null | grep -q "^tollgate-wrt"'; then
    record install:apk-add PASS "apk add exit 0 after the operator set the credential, tollgate-wrt registered"
else
    record install:apk-add FAIL "apk add exit $INSTALL_EC after password set (see $OUT/install.log)"
    finish
fi

# --- 4. contract: the package sets no credential of its own (#902) ------------
HASH_AFTER="$(vm 'grep "^root:" /etc/shadow' | cut -d: -f2)"
if [ -n "$HASH_BEFORE" ] && [ "$HASH_BEFORE" = "$HASH_AFTER" ]; then
    record contract:package-sets-no-credential PASS "root's hash is byte-identical across the install — nothing was applied, replaced or generated"
else
    record contract:package-sets-no-credential FAIL "root's hash changed during install (before=${HASH_BEFORE:0:8}… after=${HASH_AFTER:0:8}…)"
fi
if vm 'test ! -e /etc/tollgate/admin-credential-provisional'; then
    record contract:no-provisional-marker PASS "no provisional marker on a pristine install (nothing was generated)"
else
    record contract:no-provisional-marker FAIL "provisional marker exists on a pristine install — a credential was generated"
fi
HITS=$(grep -c -F -e "$ADMIN_PW" "$OUT/install.log" || true)
[ "$HITS" -eq 0 ] \
    && record contract:no-plaintext PASS "the operator's root password never appears in the install output" \
    || record contract:no-plaintext FAIL "$HITS plaintext hit(s) of the root password in install output"

# --- 6. services -------------------------------------------------------------
vm '/etc/init.d/tollgate-wrt status 2>/dev/null | head -1' | grep -q running \
    && record services:tollgate-wrt PASS "$(vm '/etc/init.d/tollgate-wrt status 2>/dev/null | head -1')" \
    || record services:tollgate-wrt FAIL "tollgate-wrt not running"
vm '/etc/init.d/nodogsplash status 2>/dev/null | head -1' | grep -q running \
    && record services:nodogsplash PASS "nodogsplash running" \
    || record services:nodogsplash FAIL "nodogsplash not running"
vm '/etc/init.d/uhttpd status 2>/dev/null | head -1' | grep -q running \
    && record services:uhttpd PASS "uhttpd running" \
    || record services:uhttpd FAIL "uhttpd not running"

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

# --- 8. entry-port mapping, section ownership, crash logs ---------------------
MAPPING="$(vm 'cat /etc/tollgate/entry-ui-mapping 2>/dev/null')"
case "$MAPPING" in
    board|luci) record mapping:entry-ui PASS "$MAPPING" ;;
    *) record mapping:entry-ui FAIL "no readable entry-ui mapping ('$MAPPING')" ;;
esac
UHTTPD_UCI="$(vm 'uci show uhttpd 2>/dev/null')"
record_verdict "listeners:uhttpd-sections" "$(ic_uhttpd_sections "$UHTTPD_UCI")"
record_verdict "mapping:port-ownership" "$(ic_port_ownership "${MAPPING:-unknown}" "$UHTTPD_UCI")"
record_verdict "logs:crash-signatures" "$(ic_crash_logs "$(vm 'logread 2>/dev/null')")"

# --- 9. persistence across a reboot ------------------------------------------
if [ "$SKIP_REBOOT" -eq 1 ]; then
    if [ "$DEV" -eq 1 ]; then
        record persistence:reboot SKIP "--skip-reboot (--dev)"
    else
        record persistence:reboot NOT_VERIFIED "--skip-reboot without --dev: reboot persistence is a required invariant of this gate"
        finish
    fi
else
    SALT_BEFORE="$(printf '%s' "$HASH_AFTER" | cut -d'$' -f3)"
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
        # The #899/#890 tear can appear only after the boot-time uci-defaults
        # pass, so the listener plane is re-read from the rebooted device,
        # not inferred from the pre-reboot state.
        LISTEN2="$(vm 'netstat -ltn 2>/dev/null' | awk '{print $4}')"
        for p in 80 443 2050 2051 8080 8090 8443; do
            echo "$LISTEN2" | grep -q ":$p\b" \
                && record "persistence:listeners:$p" PASS "listening after reboot" \
                || record "persistence:listeners:$p" FAIL "nothing listening on :$p after reboot"
        done
        UHTTPD_UCI2="$(vm 'uci show uhttpd 2>/dev/null')"
        record_verdict "persistence:uhttpd-sections" "$(ic_uhttpd_sections "$UHTTPD_UCI2")"
        record_verdict "persistence:port-ownership" "$(ic_port_ownership "${MAP2:-unknown}" "$UHTTPD_UCI2")"
        record_verdict "persistence:crash-signatures" "$(ic_crash_logs "$(vm 'logread 2>/dev/null')")"
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
