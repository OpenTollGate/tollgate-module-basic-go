- Record the Spilman sidecar decision: the Go daemon stays `CGO_ENABLED=0` and
  owns all durable channel state, protocol cryptography runs in a separately
  packaged Rust process behind an authenticated AF_UNIX socket, and the first
  milestone lands only a capabilities-only v1 contract (`sidecar/spilman`,
  `src/spilmansidecar`) whose tests refuse mismatched ids, unsupported
  versions, oversize frames and unsupported capabilities. No signing, mint
  call, settlement or payment path exists in this change.
