#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
if ! command -v greatspectate &>/dev/null; then
    echo "Installing greatspectations..."
    pip install git+https://github.com/rustyrussell/greatspectations.git
fi
if [ ! -d nuts ]; then
    echo "Cloning NUT specs..."
    git clone --depth=1 https://github.com/cashubtc/nuts.git nuts
fi
echo "Checking spec quote drift..."
greatspectate check --config specquotes.toml --comment-start '// ' --comment-continue '//' $(find src -name '*.go' -not -name '*_test.go')
echo ""
echo "Spec coverage report:"
greatspectate coverage --config specquotes.toml
