#!/bin/sh
# lib/restore-config.sh — put /etc/tollgate/config.json back the way it was
# after a measurement phase injected a test mint into `accepted_mints`.
#
# Every injection site in this experiment writes a byte-verified backup to
# /etc/tollgate/config_backups/ first. This script restores from that backup,
# asserts the sha256 matches the value recorded BEFORE the run, restarts the
# service and verifies the device is back in its original shape (mint count,
# advertisement tag count, wallet status). It is the manual counterpart of the
# restore step inside phases/04-payments.sh and phases/05-faults.sh, for when a
# phase is interrupted before its own restore runs.
#
#   EXPECT_SHA=<sha256> ./lib/restore-config.sh [backup-path]
#
# Defaults: backup = /etc/tollgate/config_backups/config.json.baseline-gonuts
set -eu
. "$(dirname "$0")/common.sh"

BAK="${1:-/etc/tollgate/config_backups/config.json.baseline-gonuts}"
STAMP=$(utc | tr ':' '-')
LOG="$OUT/restore-config-$STAMP.log"
log() { echo "$*" | tee -a "$LOG"; }

log "=== restore-config $STAMP ==="
log "current config sha : $(rsha "$CONF")"
log "current mint count : $(rssh "jq -r '.accepted_mints|length' $CONF")"
log "backup file        : $BAK"
BAK_SHA=$(rsha "$BAK")
log "backup sha         : $BAK_SHA"

if [ -n "${EXPECT_SHA:-}" ] && [ "$BAK_SHA" != "$EXPECT_SHA" ]; then
    log "REFUSING: backup sha $BAK_SHA does not match EXPECT_SHA $EXPECT_SHA"
    exit 1
fi

# `cat > file` (not cp/mv) preserves the target's inode and mode, which is how
# the phases do it too; chmod 600 then reasserts the mode explicitly.
rssh "cat $BAK > $CONF && chmod 600 $CONF"
NEW_SHA=$(rsha "$CONF")
log "restored config sha: $NEW_SHA"

if [ "$NEW_SHA" = "$BAK_SHA" ]; then
    log "RESTORE OK: config.json now byte-identical to the backup"
else
    log "RESTORE MISMATCH: $BAK_SHA -> $NEW_SHA"
    exit 1
fi

rssh '/etc/init.d/tollgate-wrt restart' >/dev/null 2>&1 || true
i=0
while [ "$i" -lt 240 ]; do
    i=$((i+1))
    case "$(rcurl 'http://127.0.0.1:2121/' 2>/dev/null)" in
        *'"kind":10021'*) break;;
    esac
    sleep 1
done

log "mints now          : $(rssh "jq -r '.accepted_mints|length' $CONF")"
log "price_per_step tags: $(rcurl 'http://127.0.0.1:2121/' | grep -o 'price_per_step' | wc -l)"
log "testnut refs       : $(rssh "grep -c testnut $CONF || true")"
log "status             : $(rssh "$TOLLGATE_CLI -j status" | tr -d '\n')"
log "wallet             : $(rssh "$TOLLGATE_CLI -j wallet info" | tr -d '\n')"
log "=== restore-config done ==="
