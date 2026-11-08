#!/usr/bin/env bash
# Exercise the owner-network test's nft invocation without host privileges.
# The real parser is checked separately in an isolated network namespace.
set -eu
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
python3 - "$ROOT" <<'PY'
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile

root = Path(sys.argv[1])
fake_nft = r'''#!/usr/bin/env python3
import os, re, sys
assert sys.argv[1:3] == ['-c', '-f'], 'only check mode is allowed'
text = open(sys.argv[3]).read()
mode = os.environ['NFT_PARSER_CASE']
if mode == 'no-capability':
    print('netlink: Error: cache initialization failed: Operation not permitted', file=sys.stderr)
    sys.exit(1)
if mode == 'broken-prerequisite':
    print('nft: unsupported option', file=sys.stderr)
    sys.exit(1)
if 'backend_input_firewall' in text:
    definitions = set(re.findall(r'^\s*define\s+(\w+)\s*=', text, re.M))
    code = '\n'.join(line for line in text.splitlines() if not line.lstrip().startswith('#'))
    references = set(re.findall(r'\$(\w+)', code))
    if references - definitions:
        print('Error: unknown identifier in fixture', file=sys.stderr)
        sys.exit(1)
    if mode == 'invalid-rule':
        print('Error: syntax error in fragment', file=sys.stderr)
        sys.exit(1)
'''

cases = [('valid', False, True, False),
         ('no-capability', False, True, True),
         ('invalid-rule', False, False, False),
         ('broken-prerequisite', False, False, False),
         ('valid', True, False, False)]
for mode, missing_defs, success, skip in cases:
    with tempfile.TemporaryDirectory(prefix='nft-parser-test-') as temp:
        fixture = Path(temp)
        for name in ['tests/packaging/backend-api-owner-network_test.sh',
                     'packaging/Makefile', 'packaging/local-build-ipk.sh']:
            target = fixture / name
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(root / name, target)
        nft_dir = Path('packaging/files/etc/nftables.d')
        shutil.copytree(root / nft_dir, fixture / nft_dir)
        if missing_defs:
            (fixture / nft_dir / '00-tollgate-defs.nft').unlink()
        bin_dir = fixture / 'bin'
        bin_dir.mkdir()
        for name, script in [('nft', fake_nft), ('id', '#!/bin/sh\necho 0\n')]:
            command = bin_dir / name
            command.write_text(script)
            command.chmod(0o755)
        env = dict(os.environ, PATH=str(bin_dir) + os.pathsep + os.environ['PATH'],
                   NFT_PARSER_CASE=mode)
        result = subprocess.run(['bash', 'tests/packaging/backend-api-owner-network_test.sh'],
                                cwd=fixture, env=env, text=True, capture_output=True)
        assert (result.returncode == 0) == success, (mode, missing_defs, result.stdout, result.stderr)
        assert ('SKIP nft' in result.stdout) == skip, (mode, result.stdout)
        if success and not skip:
            assert 'nft -c accepts' in result.stdout, result.stdout
        if not success:
            assert 'FAIL' in result.stdout, result.stdout
        print('ok: mode=%s missing_definitions=%s' % (mode, missing_defs))
print('5 parser invocation cases passed')
PY
