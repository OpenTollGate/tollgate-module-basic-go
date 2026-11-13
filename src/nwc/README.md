# Journalable Nostr Wallet Connect client

NIP-47 transport independent of LNbits, using the existing `go-nostr` event/signing/NIP-44/NIP-04 primitives and `coder/websocket`. It does not change existing wallet pins, configuration or payment defaults.

`New(Config)` parses a protected `nostr+walletconnect` connection without exposing it in errors or formatting. `Connect` validates signed wallet information and retrieves effective `get_info` capabilities for this connection. NIP-44 v2 is preferred; legacy NIP-04 requires an explicit compatibility option. Secure relay URLs are required except explicitly selected loopback tests. Relay redirects are refused. Private endpoints remain an operator-controlled connection, not a guest-supplied target. NIP-42 relay authentication is not implemented by the default transport; an authenticated relay needs a separately reviewed `Transport` implementation.

`Prepare(method, params)` produces a signed, encrypted `Prepared` request for `make_invoice`, `lookup_invoice`, `pay_invoice`, `get_info` or `get_balance`, only when supported. Signed invoices, network, amount, expiry and method parameters are checked before a payment is prepared. **Durably record the prepared request before `Execute`.** Responses require a valid event ID/signature, expected wallet author, client recipient, request reference, method and encryption. Relay acknowledgment is never wallet settlement.

Execution is bounded and one-shot per request in a running client. A monetary error for which `IsUnknown` is true must be reconciled with `lookup_invoice` before any new attempt. The in-memory replay guard does not replace a durable journal: after restart, never execute a request whose submission may already have happened. Raw wallet results are protocol data; the business layer must still validate amount/direction/hash/settlement, budget and grant ownership. No grant, payout scheduler, real-fund operation or browser credential storage is installed here.

The default relay transport uses bounded WebSocket frames and subscribes before publishing. Only failures before publication may try another configured relay; publish/response failure never sends again. The SDK relay lifecycle helper is not used: the local race test exposed a concurrent close/write in its pinned version. Crypto primitives remain unchanged. Subscriptions/connections and contexts are closed on every path.

Run from `src`: `go test -race -count=1 ./nwc` and `go vet ./nwc`. Tests use generated keys, real signed/encrypted responses and a local WebSocket relay, including response-before-ack, spoof/replay/tag/signature/encryption failures and lost-response duplicate refusal. No public relay or real wallet is contacted.

Protocol: https://github.com/nostr-protocol/nips/blob/master/47.md . Operator versus customer connections, the durable payment journal and TollGate/portal integration remain separate responsibilities.
