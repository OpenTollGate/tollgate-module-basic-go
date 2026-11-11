# Durable Lightning operation journal

A bounded single-writer journal for payment intents, payout reservations and purchase-grant progress. It does not send funds or grant network access and does not alter the existing Cashu store.

`Open` requires a dedicated owner-only directory and exclusively locks its writer file. Snapshots are protected0600 files, atomically replaced with file and parent-directory fsync. Linked, foreign, malformed, duplicate-key, unsupported-format or oversized data is refused. Required I/O failure poisons the open writer; close, reopen and reconcile before further work. Existing source state is retained rather than overwritten after an outside edit.

Recovery, transitions and invoice binding keep each operation's update timestamp at least as recent as its stored timestamps. A backward clock step or pre-NTP reboot cannot make the next reopen reject an otherwise valid operation. Version/CAS controls ordering; timestamps are audit metadata.

`Prepare` binds a stable operation ID to wallet, amount, direction, recipient and opaque noncredential request data. It idempotently returns the same operation but refuses rebinding. Outgoing principal/fee reservations are serialized against the selected wallet/period budget. Unknown outcomes remain reserved across period boundaries. Completed successes use actual fees; definitive failure releases only that operation's reservation. Keep different aliases of the same treasury in the same wallet budget domain.

For outgoing payments, use CAS `SubmitOutgoing` with the **current** period budget to persist `submitted` before the external monetary request. It atomically rechecks all reservations and in-period actual costs, counting the prepared operation once. An idempotent `Prepare` return records intent; it does not authorize a send under an old budget. Callers must also recheck current consent, per-payment limits, invoice expiry and provider policy before submission. `Advance` remains a lower-level CAS transition API and does not perform a current-budget check.

After restart any unreconciled submitted record becomes `unknown` and cannot be resubmitted through the state machine. Resolve unknown outcomes against the authoritative wallet; never infer failure from a timeout. An incoming invoice is bound once before exposure; paid/grant-pending/completed progress cannot be rewound to replay a credit. Exact idempotent application of the grant is the caller's responsibility, as is validating provider settlement and deciding that a failure is definitive.

Operation bytes are private: use prepared encrypted NWC events or nonsecret invoice parameters, never API keys or connection URIs. Caller-controlled time/pricing/policy/grant snapshots must be recorded before irreversible work. This journal is a local transaction primitive, not an atomic transaction with a remote wallet or NDS, and does not make legacy session metering durable.

From `src`: `go test -race -count=1 ./paymentjournal`, `go vet ./paymentjournal`. Tests exercise concurrent reservations/CAS, restart, partial recipient success, malformed/foreign snapshots and real child-process exits before/after atomic rename and after submitted state. Test fault injection replaces the store's rename operation; there is no production crash environment variable.
