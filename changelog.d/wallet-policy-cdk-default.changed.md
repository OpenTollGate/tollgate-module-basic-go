- **Wallet policy now selects CDK on every target; gonuts is the documented fallback.**
  `wallet-policy.json` pointed the 16 MB mipsel tier at the in-process `gonuts`
  backend and only the roomier tiers at `cdk`. That rule was written when the
  fork was still a viable long-term maintenance path. It is not: `elnosh/gonuts`
  was **archived upstream on 2025-08-16**, so `OpenTollGate/gonuts-tollgate`
  (~36 patches, ahead 63 / behind 6) can never merge back and is a permanent
  hard fork. Every rule now selects `cdk`.

  The policy remains a **default, never a lock** — `wallet.backend` in config
  still overrides it, so the gonuts path stays usable while both backends are
  maintained. What does NOT change: the sidecar client is still compiled in for
  all targets and the policy still only takes effect on images that ship the
  `cdk-walletd` package.

  **Known risk on the small tier:** the 16 MB/mipsel rule now depends on the
  sidecar being a separately packaged daemon (measured 7.51 MiB on mipsel,
  6.02 MiB aarch64, plus ~6 MiB RSS). The original decision chose in-process
  there precisely because that marginal cost was a tight fit. This flip accepts
  that fit must be confirmed by on-device `df` before the tier ships; it is
  recorded in the rule's `note` rather than hidden.
