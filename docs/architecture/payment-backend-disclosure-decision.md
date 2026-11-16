# Payment Backend Disclosure — Architecture Decision

## Status: Decided (2026-11-16)

This decision governs how TollGate presents **bearer Cashu** and
**account-based Lightning** backends to operators and customers. It is a
disclosure and consent contract, not provider enablement. No NIP-47 or LNbits
consumer is enabled by this record.

## The distinction that must not be blurred

| | Cashu ecash | NIP-47 / LNbits Lightning |
|---|---|---|
| Value model | Bearer token; possession controls value | Account-based wallet; the router receives spending authority |
| Router role | Verifier of ecash | Remote client of a Lightning wallet/account |
| Persistent identity | Not required to hold a token | Required for the access session, payment reconciliation and restart recovery |
| Primary privacy tradeoff | The mint sees proofs/redemption; the router sees the payment flow | The wallet provider/relay can see payment metadata; the router keeps a session-to-payment record |
| Offline behavior | A token can be held; verification needs the mint | Relay/wallet availability is required |
| Failure class | Mint refusal, keyset loss, stale proofs | Wallet/relay outage, ambiguous payment outcome, account compromise |

Neither model is anonymous by default. The disclosure must not claim that
Cashu is private and Lightning is not; it must state the different trust and
linkability models.

## Persistent session identity is part of the product

An account-based backend requires a durable session/payment identity so the
router can:

- grant access exactly once;
- recover the session after a reconnect or restart;
- reconcile a Lightning payment by lookup rather than re-sending;
- avoid double-charging or double-granting;
- expire or purge the record under an explicit retention policy.

This is a **temporary session record, not a personal account**. The customer
must still be told that the payment is linked to the access session, and the
operator must be told that the record persists and is visible to them.

Correlation stays local: the session ID, MAC address, customer name and portal
URL must not be written into the Lightning invoice or memo. The wallet sees a
generic payment; the router keeps the mapping.

## Required operator disclosure

NIP-47 and LNbits must not be a silent dropdown choice. Before enablement, the
operator must see and acknowledge:

> **Account-based Lightning backend**
> This gives this router remote spending authority over a Lightning wallet. It
> is not bearer ecash. The router will keep a durable session/payment record so
> it can grant access and recover the session after reconnects or restarts.
> That record links the Lightning payment to the customer's access session and
> is visible to the operator.
>
> The wallet provider and Nostr relay can see payment metadata such as amount,
> timing and counterparty public keys. A compromised router or connection secret
> can spend the wallet. Use a dedicated wallet with a limited balance, and set a
> retention policy.

Required acknowledgements:

- [ ] I understand this is an account-based connection, not bearer ecash.
- [ ] I understand the router will persist a session/payment record.
- [ ] I will use a dedicated wallet with a limited balance and set a retention policy.

The acknowledgement must be stored and must survive upgrades. An upgrade cannot
silently preserve an unacknowledged enablement.

## Required customer disclosure

Before the Lightning payment action, the customer must see:

> **Pay with Lightning**
> This TollGate uses an account-based Lightning wallet connection. To keep your
> access working across reconnects, this router keeps a temporary record linking
> this payment to this access session. The operator and wallet provider may see
> the amount and time.
>
> We do not put your name, MAC address or session ID in the Lightning invoice.
> [What we store and how long we keep it]

Required acknowledgement:

- [ ] I understand this payment is linked to this access session.

The wording must say **persistent session record**, not merely "session". It must
also say that this is not a personal account, but it must not hide the
linkability.

## Session/receipt surface

The customer-facing session/receipt view must show:

- an opaque session ID;
- the backend label: `Lightning via NIP-47 (account-based, persistent session record)`;
- payment status;
- "This record links the payment to this access session";
- retention period and expiry/delete behavior;
- "The wallet provider sees the payment amount/time; it does not receive this
  session ID from us."

## Data minimization

Persist only what reconciliation and access require:

- opaque session ID;
- wallet/payment hash or quote ID;
- amount and expiry;
- payment state and grant state;
- timestamps.

Do not put the session ID, MAC address, customer name or portal URL in the
Lightning invoice or memo. Keep the payment-to-session mapping local. Define and
enforce retention; do not retain account-based session records indefinitely by
accident.

## UI surfaces

1. **Admin setup:** blocking consent card with the three acknowledgements.
2. **Guest portal:** pre-payment badge, persistent-session notice, privacy
   details link, and a consent checkbox.
3. **Status page:** fixed backend label including `account-based` and
   `persistent session record`.
4. **Receipt/session page:** opaque ID, retention and data summary.
5. **Documentation:** a comparison page that explains both models without
   claiming either is anonymous.

## Consumer contract

The consumer-wiring PR must carry this shape (names may differ; fields may not):

```text
provider: nip47 | lnbits
custody: account_based
router_spending_authority: yes
persistent_session_identity: required
session_linkability: payment <-> access session
invoice_contains_session_id: no
wallet_sees: amount, time, payment metadata
operator_sees: session ID, amount, time, payment state
retention: operator-configurable
consent_required_admin: yes
consent_required_guest: yes
```

A consumer must not enable an account-based backend unless:

- both consent surfaces exist;
- the persistent session record is written before the payment is actionable;
- restart/reconnect recovery is exercised;
- invoice/memo contents are covered by a test proving no session identifier or
  customer identifier is sent to the wallet;
- retention/purge behavior is documented and tested.

## Non-goals

- This record does not enable NIP-47, LNbits or any provider.
- It does not claim Cashu is anonymous.
- It does not decide the final portal visual design; the portal repo owns that.
- It does not replace PRTA/router acceptance or real-wallet interoperability.

## References

- `#1211` — NIP-47 transport; dormant until a consumer satisfies this contract.
- `#1210` — LNbits client; same account-based disclosure.
- `#1212` / `#1231` — durable journal and paid/unresolved-fee reconciliation.
- `docs/operator-guide.md` — operator-facing summary.
