"""Offline subprocess regressions: a failed parser is never an empty graph."""
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest

CHECK = Path(__file__).with_name("check-deps-sync.py")


class DependencyFenceTests(unittest.TestCase):
    def run_case(self, mode, modules=True, arguments=()):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            script = root / "tests/contract/check-deps-sync.py"
            script.parent.mkdir(parents=True)
            shutil.copyfile(CHECK, script)
            if modules:
                for name in ("one", "two"):
                    path = root / "src" / name / "go.mod"
                    path.parent.mkdir(parents=True)
                    path.write_text(f"module example.invalid/{name}\n\ngo 1.26.0\n")
            tools = root / "bin"
            tools.mkdir()
            if mode != "missing":
                fake = tools / "go"
                fake.write_text(f"#!{sys.executable}\n" + r'''
import json, os, sys
from pathlib import Path
mode = os.environ['DEPS_FENCE_TEST_MODE']
if mode == 'invalid-json':
    print('not-json')
    sys.exit(0)
if mode == 'invalid-shape':
    print('{}')
    sys.exit(0)
version = 'v2.0.0' if mode == 'drift' and Path(sys.argv[-1]).parent.name == 'two' else 'v1.0.0'
requirements = [{'Path':'example.invalid/shared', 'Version':version}]
if mode == 'stdlib': requirements = []
if mode == 'invalid-requirements': requirements = {}
go_version = 0 if mode == 'invalid-directive' else '1.26.0'
print(json.dumps({'Module':{'Path':'example.invalid/module'}, 'Go':go_version,
                  'Require':requirements}))
sys.exit(7 if mode == 'nonzero' else 0)
''')
                fake.chmod(0o700)
            result = subprocess.run([sys.executable, str(script), *arguments],
                                    env={**os.environ, "PATH":str(tools), "DEPS_FENCE_TEST_MODE":mode},
                                    capture_output=True, text=True, timeout=10)
            return result

    def test_real_invocations_require_selftests(self):
        # Exercise the consumer wiring contract without invoking release actions.
        root = Path(__file__).resolve().parents[2]
        found = []
        for home in (root / "scripts", root / ".github" / "workflows"):
            for path in home.rglob("*"):
                if not path.is_file() or path.suffix not in {".sh", ".yml", ".yaml"}:
                    continue
                for number, line in enumerate(path.read_text().splitlines(), 1):
                    if "python3 tests/contract/check-deps-sync.py" in line and not line.lstrip().startswith("#"):
                        found.append((path, number))
                        self.assertIn("--selftest-and-check", line, f"{path.relative_to(root)}:{number}")
        self.assertTrue(found, "no dependency-fence invocations discovered")

    def test_valid_parser(self):
        result = self.run_case("valid")
        self.assertEqual(result.returncode, 0, result.stdout)

    def test_real_drift_refused(self):
        self.assertNotEqual(self.run_case("drift").returncode, 0)

    def test_valid_stdlib_only_graph(self):
        self.assertEqual(self.run_case("stdlib").returncode, 0)

    def test_parser_failures_refused(self):
        for mode in ("missing", "nonzero", "invalid-json", "invalid-shape", "invalid-requirements", "invalid-directive"):
            with self.subTest(mode=mode):
                result = self.run_case(mode)
                self.assertNotEqual(result.returncode, 0, f"{mode}: unverified graph was accepted")

    def test_missing_modules_refused(self):
        self.assertNotEqual(self.run_case("valid", modules=False).returncode, 0)

    def test_required_selftest_missing_refused(self):
        self.assertNotEqual(self.run_case("valid", arguments=("--selftest-and-check",)).returncode, 0)


if __name__ == "__main__":
    unittest.main()
