- **Session events no longer put the customer's MAC address in the `p`
  tag.** The kind-1022 event every successful payment returns carried the
  MAC in a field whose contract says Nostr pubkey — spec-invalid for every
  conformant consumer, and invisible to relay-side `#p` filtering. The
  raw-token flow has no customer Nostr identity, so the tag is now omitted
  (the spec makes it conditional; the MAC stays where it belongs, in
  `device-identifier`). Wire-visible to any consumer that validated `p`;
  the renewal helper no longer propagates a legacy MAC into the field
  either. The same shape on error notices is tracked separately in #950.
  ([#951](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/951))
