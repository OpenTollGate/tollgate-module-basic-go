- Refuse a sidecar frame the peer cannot correlate **with its code on the wire**
  instead of closing silently. `Outcome::Fatal` was matched and discarded, so
  `frame_too_large` and `invalid_request` never reached a client: a 1 MB flood
  and a one-byte typo were both indistinguishable from a crashed daemon. A
  partial frame that stalls mid-read is now refused as `invalid_request` rather
  than pinning the connection until the read timeout, and an oversize frame is
  answered with exactly one uncorrelated refusal before the close.
