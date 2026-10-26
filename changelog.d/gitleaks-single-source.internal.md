- **Secret scanning now runs on the build of record, from one config.**
  The gitleaks rules/allowlists live in this repo's `.gitleaks.toml`
  (single source of truth), the GitHub lane delegates to the new
  org-level `OpenTollGate/.github` reusable scanner (ported from
  `Amperstrand/.github` as part of the fork consolidation, #758), and a
  new additive ngit workflow scans every mirror push with the same
  pinned binary and the same config — previously the build-of-record
  lane ran no secret scan at all (the #520 silent-job class).
