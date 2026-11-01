#!/usr/bin/env bash
# Shim: the driver is backend-parameterized now (run-module-e2e.sh);
# this name is kept for the PR #1155 references and muscle memory.
export BACKEND=cdk
exec "$(dirname "$0")/run-module-e2e.sh" "$@"
