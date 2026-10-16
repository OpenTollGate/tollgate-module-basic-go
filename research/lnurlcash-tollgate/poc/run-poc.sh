#!/usr/bin/env bash
# run-poc.sh -- the one command. Produces out/ENV.txt, out/RED.txt, out/GREEN.txt.
#
# Design rule (inherited from reports/one-way-channels/rig/README.md): a rig that
# needs PyPI or the network to check a dependency-free, offline claim is not
# evidence. Nothing here touches the network and nothing imports a third-party
# package. `python3 -S` would also work; we do not rely on it.

set -u
cd "$(dirname "$0")"
mkdir -p out
PY=${PY:-python3}

# ---------------------------------------------------------------- ENV.txt
{
  echo "host            : $(uname -a)"
  echo "date            : $(date -Is)"
  echo "python3         : $($PY -VV 2>&1)"
  echo "python3 path    : $(command -v $PY)"
  echo
  echo "--- third-party python packages importable? (must be none needed) ---"
  $PY - <<'EOF'
import importlib.util
for m in ("ecdsa", "coincurve", "secp256k1", "bech32", "embit", "bitcoinlib", "requests"):
    print("  %-12s %s" % (m, "present" if importlib.util.find_spec(m) else "ABSENT"))
EOF
  echo
  echo "--- bitcoin consensus oracle available? ---"
  for b in bitcoind bitcoin-cli; do
    printf '  %-12s %s\n' "$b" "$(command -v $b || echo ABSENT)"
  done
  echo "  libbitcoinkernel: $( (ls /usr/lib/libbitcoinkernel* /usr/local/lib/libbitcoinkernel* 2>/dev/null || echo ABSENT) | head -1)"
  echo
  echo "  => No consensus oracle on this host. Verification here is validated"
  echo "     against the LUD-25/LUD-26 official vectors and the BIP-341/342 text,"
  echo "     NOT against libbitcoinkernel."
  echo
  echo "--- the vectors, unmodified, as fetched ---"
  for f in vectors/25.md vectors/26.md vectors/25-vectors.json vectors/26-vectors.json; do
    printf '  %-28s %s  sha256=%s\n' "$f" "$(wc -c < "$f") bytes" "$(sha256sum "$f" | cut -c1-16)"
  done
} > out/ENV.txt 2>&1

# ---------------------------------------------------------------- RED.txt
{
  echo "RED -- the implementation is WRONG, and the vector assertions FAIL."
  echo "Run: $(date -Is)   host: $(uname -n)"
  echo "This is the falsification condition for the whole POC: if a defect"
  echo "injected here did NOT produce failures, the suite would be proving nothing."
  echo
  echo "############ R1  test_vectors.py --broken ############"
  echo "# Defects injected: (a) the BIP-341 NUMS point H replaced with 0x00*32;"
  echo "#                   (b) the bearer leaf's OP_EQUAL replaced with OP_EQUALVERIFY."
  echo
  $PY tests/test_vectors.py --broken
  echo
  echo "############ R2  test_bip342.py --unpatched ############"
  echo "# Defect injected: BIP-342's disabled-opcode rule REMOVED."
  echo "# Expected: the OP_CHECKMULTISIG leaf, which is INVALID tapscript, is accepted."
  echo "# That is precisely the bug the finding describes, reproduced on demand."
  echo
  $PY tests/test_bip342.py --unpatched
} > out/RED.txt 2>&1
RED_RC=$?

# ---------------------------------------------------------------- GREEN.txt
{
  echo "GREEN -- the passing run."
  echo "Run: $(date -Is)   host: $(uname -n)"
  echo "No defects injected. Every assertion below is a real vector field or a"
  echo "rule quoted from LUD-25/LUD-26/BIP-341/BIP-342/BIP-68."
  echo
  echo "############ G1  tests/test_vectors.py  (LUD-25 + LUD-26 official vectors) ############"
  echo
  $PY tests/test_vectors.py
  RC1=$?
  echo
  echo "############ G0  stdlib-only proof: python3 -S  (site-packages DISABLED) ############"
  echo "# -S removes sys.path entries for site-packages entirely. If anything here"
  echo "#   needed PyPI, this run could not pass."
  echo
  $PY -S tests/test_vectors.py | tail -5
  echo
  echo "############ G2  tests/test_bip342.py  (the BIP-342 finding) ############"
  echo
  $PY tests/test_bip342.py
  RC2=$?
  echo
  echo "exit codes: test_vectors.py=$RC1  test_bip342.py=$RC2"
} > out/GREEN.txt 2>&1
GREEN_RC=$?

echo "wrote out/ENV.txt out/RED.txt out/GREEN.txt"
echo "RED   contains $(grep -c '\[FAIL\]' out/RED.txt) FAIL lines (expected: many)"
echo "GREEN contains $(grep -c '\[FAIL\]' out/GREEN.txt) FAIL lines (expected: 0)"
grep -h '^SUMMARY' out/GREEN.txt
exit 0
