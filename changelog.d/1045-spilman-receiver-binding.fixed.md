- **Spilman channels are now bound to the receiver key the payee advertises.**
  A channel names the key its receiver signs the close with, and nothing checked
  that the payee owned it: `GET /spilman/params` advertised no key at all, so a payer
  could only open against one of its own choosing, and `ReceiverKeyIsAcceptable`
  returned a hardcoded `true`. The result was silent: the channel funded at the mint,
  the payee could not sign a close for it, and the payer's money sat in a channel
  nobody could settle — surfacing only at `POST /spilman/close`, long after the value
  moved.

  The payee now derives a stable receiver key from its persisted merchant identity
  (domain-separated, so the advertised key cannot be linked to the router's Nostr
  npub), advertises it as `receiver_pubkey` in `/spilman/params`, and **refuses a
  registration addressed to any other key** before anything is written.
  `ReceiverKeyIsAcceptable` is a real comparison instead of a constant.

  No new file, key, or backup obligation: the key is a pure function of
  `identities.json`, which operators already persist.
  ([#N](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/N))
