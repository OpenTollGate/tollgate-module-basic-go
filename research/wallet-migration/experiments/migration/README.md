# Migration rehearsal (T6)

`migration_rehearsal.py` moves funds **gonuts → CDK** and back, on the local
fakewallet mint, and asserts no value is lost. It is the executable proof behind
`03-baseline/migration-path.md`.

```
mint 100 into gonuts
  -> gonuts send(100)      -> token -> CDK receive  (gonuts 0, cdk 100)   # migrate
  -> CDK send(100)         -> token -> gonuts receive (cdk 0, gonuts 100) # rollback
```

## Run

```
# requires the gonuts driver built at experiments/parity/gonutsinterop/gonutsinterop
python3 experiments/migration/migration_rehearsal.py \
    --workdir /home/c03rad0r/r2-work/migrate-final
```

Raw result: `output.txt`. Verdict last checked 2026-09-16: **PASS**,
`no_value_lost: true`.

## Why token transfer and not same-seed restore

Both `send`/`drain`/`receive` are `WalletPort` operations already proven
interchangeable in T2e, so the migration does not depend on the two libraries
sharing a NUT-13 derivation. Same-seed NUT-09 restore is documented as the
preferred *alternative* (config-flip rollback, no fund movement) but is **not**
exercised — it needs a gonuts seed-export tool and a derivation-compatibility
check. See `03-baseline/migration-path.md` §"Mechanism B".
