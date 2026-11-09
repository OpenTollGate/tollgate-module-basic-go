# Durable Lightning operation journal

A bounded single-writer journal for payment intents, payout reservations and purchase-grant progress. It does not send funds or grant network access and does not alter the existing Cashu store.

`Open` requires a dedicated owner-only directory and exclusively locks its writer file. Snapshots are protected0600 files, atomically replaced with file and parent-directory fsync. Linked, foreign, malformed, duplicate-key, unsupported-format or oversized data is refused. Required I/O failure poisons the open writer; close, reopen and reconcile before further work. Existing source state is retained rather than overwritten after an outside edit.

`Prepare` binds a stable operation ID to wallet, amount, direction, recipient and opaque noncredential request data. It idempotently returns the same operation but refuses rebinding. Outgoing principal/fee reservations are serialized against the selected wallet/period budget. Unknown outcomes remain reserved across period boundaries. Completed successes use actual fees; definitive failure releases only that operation's reservation. Keep different aliases of the same treasury in the same wallet budget domain.

Use CAS `Advance` to persist `submitted` **before** any external monetary request. After restart any unreconciled submitted record becomes `unknown` and cannot be resubmitted through the state machine. Resolve unknown outcomes against the authoritative wallet; never infer failure from a timeout. An incoming invoice is bound once before exposure; paid/grant-pending/completed progress cannot be rewound to replay a credit. Exact idempotent application of the grant is the caller's responsibility, as is validating provider settlement and deciding that a failure is definitive.

Operation bytes are private: use prepared encrypted NWC events or nonsecret invoice parameters, never API keys or connection URIs. Caller-controlled time/pricing/policy/grant snapshots must be recorded before irreversible work. This journal is a local transaction primitive, not an atomic transaction with a remote wallet or NDS, and does not make legacy session metering durable.

From `src`: `go test -race -count=1 ./paymentjournal`, `go vet ./paymentjournal`. Tests exercise concurrent reservations/CAS, restart, partial recipient success, malformed/foreign snapshots and real child-process exits before/after atomic rename and after submitted state. Test fault injection replaces the store's rename operation; there is no production crash environment variable.
