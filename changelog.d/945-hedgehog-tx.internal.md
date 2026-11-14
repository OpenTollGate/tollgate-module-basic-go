- **Add the transaction and signing layer of `src/hedgehog`, with the full
  2-of-2 spend verified in-process and every output key validated against
  Bitcoin Core.** The payer's half-signature is confirmed to be non-spendable
  alone, which is the protocol's core safety property: it is what makes the half
  safe to transmit as plaintext.

  Two consensus-level traps are now pinned by tests. First, the BIP-341
  signature message for a script-path spend needs the BIP-342 extension
  (`ext_flag = 1` plus the tapleaf hash); using btcd's key-path
  `CalcTaprootSignatureHash` yields well-formed signatures that the interpreter
  rejects with "Invalid Schnorr signature". Second, the signature message commits
  to `sha_sequences`, so changing any input's `nSequence` must change the digest —
  omitting it was the most expensive bug of the PR #944 research phase.
