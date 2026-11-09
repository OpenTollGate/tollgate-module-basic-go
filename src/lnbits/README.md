# LNbits wallet client

A wallet-bound LNbits REST client, tested against the v1.6.2 wire contract. This package is independent of NWC and does not change TollGate configuration, wallet pins or the Cashu payment flow.

`New(Config)` requires an explicit wallet ID, BOLT11 network and receiving credential. The optional separate admin credential enables `PayInvoice`; receiving does not require it. Retrieve credentials from protected storage, never command arguments or general configuration. `Wallet` checks wallet identity; `CreateInvoice`, `Lookup` and `PayInvoice` validate the wallet, amount, direction, signed invoice and payment identity. Amounts in returned payment records and balances are signed millisatoshis. Outgoing successful results require a matching payment preimage. Base paths are retained; HTTPS is required except explicitly selected loopback development. Redirects are refused, bodies/deadlines are bounded and errors exclude response bodies and credential values.

A public `paid: true` without matching authenticated wallet details is refused. Pending and failed results remain distinct; a valid JSON response is not necessarily a settled payment. No claim is made about the provider's honesty or real routing/liquidity.

## Transaction contract

Persist an operation intent before `CreateInvoice` or `PayInvoice`, and persist a verified incoming invoice before showing it to a customer. The client never retries a POST. `IsUnknown(err)` means the provider may already have acted; reconcile the recorded wallet/payment identity before any new send or invoice attempt. `external_id` is a correlation field, not a promised idempotency guarantee. A caller must also journal grants, payout reservations, fees and partial outcomes. This package does not grant internet access, enforce payout budgets, migrate funds or fix session persistence.

Run from `src`: `go test -race -count=1 ./lnbits` and `go vet ./lnbits`. Tests use local TLS HTTP servers and generated invoice-signing keys, including wallet mismatch, bare status, malformed/oversized data, redirect and ambiguous-send controls. No real wallet or money is used. Existing `ln-decodepay` dependencies provide BOLT11 verification; no version changes are introduced.

API source: https://github.com/lnbits/lnbits/blob/v1.6.2/lnbits/core/views/payment_api.py and https://github.com/lnbits/lnbits/blob/v1.6.2/lnbits/core/models/payments.py . Physical/router, backend/portal integration and release acceptance are separate.
