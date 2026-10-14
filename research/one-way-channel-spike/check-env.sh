#!/usr/bin/env bash
# check-env.sh -- report, from the live machine, everything the memo claims
# about available infrastructure. No guesses, no assumed paths.
#
# This is the B1 evidence. Run it before trusting any statement in the memo
# about "what IS on this host" as opposed to "what we installed for the rig".
#
# Convention: lines marked
#   [PRESENT] tool is usable
#   [ABSENT]  tool is not installed on this host as found
#   [FETCHED] present only because the rig downloaded it -> a NETWORK dependency
set -uo pipefail

echo "host        : $(hostname)"
echo "kernel      : $(uname -sr)"
if [ -r /etc/os-release ]; then . /etc/os-release; echo "distro      : ${PRETTY_NAME:-?}"; fi
echo "cpus        : $(nproc)"
echo "mem         : $(free -m 2>/dev/null | awk '/^Mem:/{print $2" MiB total, "$7" MiB available"}')"
echo "disk(/)     : $(df -h / 2>/dev/null | awk 'NR==2{print $2" total, "$4" avail, "$5" used"}')"
echo "user/groups : $(id -un) / $(id -Gn | tr ' ' ',')"
if sudo -n true 2>/dev/null; then echo "sudo        : NOPASSWD available"
else echo "sudo        : not passwordless"; fi
echo

echo "== binaries relevant to this spike =="
for b in bitcoind bitcoin-cli lightningd lnd lncli cln lightning-cli elementsd \
         docker podman python3 go rustc gcc jq gh; do
  p="$(command -v "$b" 2>/dev/null || true)"
  if [ -n "$p" ]; then printf '  [PRESENT] %-14s %s\n' "$b" "$p"
  else printf '  [ABSENT]  %-14s -\n' "$b"; fi
done
echo

echo "== anything the RIG installed (not the host's own) =="
for b in "$HOME/.local/toolchain/btc/bin/bitcoind" \
         "$HOME/.local/toolchain/go/bin/go"; do
  if [ -x "$b" ]; then printf '  [FETCHED] %s\n' "$b"; else printf '  [ABSENT]  %s\n' "$b"; fi
done
echo

echo "== is a bitcoin/LN daemon already listening anywhere? =="
found=0
for port in 8332 8333 18443 19444 9735 9736 28332; do
  line="$(ss -ltn 2>/dev/null | awk -v p=":$port" '$4 ~ p {print $4}')"
  if [ -n "$line" ]; then printf '  [LISTEN] port %-6s %s\n' "$port" "$line"; found=1; fi
done
[ "$found" = 0 ] && echo "  (no bitcoin/Lightning listener found on this host)"
echo

echo "== network reachability (the rig's ONLY network dependency) =="
for u in https://bitcoincore.org/bin/ https://github.com https://pypi.org; do
  code="$(curl -sS -o /dev/null -w '%{http_code}' --max-time 8 "$u" 2>/dev/null || echo 'FAIL')"
  printf '  %-32s %s\n' "$u" "$code"
done
echo

echo "== the module's own advertised price (the sim's SOURCED constant) =="
if [ -n "${TMBG:-}" ] && [ -d "$TMBG" ]; then
  grep -rn 'PricePerStep:' "$TMBG/src/config_manager/config_manager_config.go" 2>/dev/null | head -3
  grep -rn 'step_size' "$TMBG/src/tollgate_protocol/validator_test.go" 2>/dev/null | head -2
else
  echo "  set TMBG=/path/to/tollgate-module-basic-go to echo the sourced constants"
fi
