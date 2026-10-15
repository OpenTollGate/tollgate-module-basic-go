- **Add the Hedgehog channel state machine (`src/hedgehog/channel.go`).** Every
  operation is annotated with WHO performs it and WHETHER THE OTHER PARTY MUST BE
  REACHABLE, because conflating "who must act" with "who must be reachable" is
  exactly the error that produced a retracted direction claim during the PR #944
  research.

  The two operations that matter both require **no counterparty involvement at
  all**: the payer produces a cheque while the payee is absent, and the payee
  later completes it with the payer gone. A test asserts the payer can propose
  with the payee object never constructed and never contacted.

  Safety properties pinned by test: an unaccepted proposal does **not** move the
  enforceable state; a stale update can never supersede a newer one; an unsigned
  proposal is refused on both sides (which is why a cheque is safe to send as
  plaintext); and a channel cannot be cooperatively closed on a merely proposed
  state.
