# Token Recovery Tool

Recovers Cashu tokens that were rejected by TollGate (e.g., "failed to
open gate: exit status 1" from NDS #88 bug).

## What it does

1. Parses `/etc/tollgate/tokens-to-recover.txt`
2. For each token, checks proof state at the mint via POST /v1/checkstate (NUT-07)
3. If proofs are UNSPENT: calls Wallet.Receive() to recover value
4. If proofs are SPENT: skips (value already consumed)
5. Reports: X recovered, Y spent, Z failed

## Usage

```bash
# Dry run — check what's recoverable without changing wallet
CGO_ENABLED=0 go run . -file /etc/tollgate/tokens-to-recover.txt -dry-run

# Actually recover
CGO_ENABLED=0 go run . -file /etc/tollgate/tokens-to-recover.txt

# Cross-compile for router
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o recover .
scp recover root@10.99.99.1:/tmp/
ssh root@10.99.99.1 '/tmp/recover -file /etc/tollgate/tokens-to-recover.txt -dry-run'
```

## File format

Each line: `timestamp | mint_url | cashu_token | error_message`

Written by `upstream_session_manager/token_recovery.go` when autopay fails.
