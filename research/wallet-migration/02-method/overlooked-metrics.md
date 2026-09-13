# Metrics we might be overlooking

Placeholder for the consultant findings (T4). Seeded with the obvious candidates
so the consultants can confirm, reject, or extend them:

- **Licence compatibility** — comes before performance. nucula has *no licence
  file at all*; CDK's terms need reading. Nothing else matters if we cannot
  legally ship it.
- **Architecture cost, not benchmark cost.** Both candidates force an
  integration design (cgo/FFI vs sidecar process vs embedded interpreter). The
  ongoing cost of that seam — build complexity, CI, debuggability on a router,
  failure modes — may dwarf any footprint difference.
- **Flash wear.** A router writes to flash with limited erase cycles; a wallet
  that updates a sqlite/redb file on every operation can wear the device out
  over years of small payments. Bytes-written-per-payment is a first-class metric.
- **Failure-mode symmetry.** What happens when the mint is down, the clock is
  wrong, or power drops mid-swap? A wallet that loses proofs is worse than one
  that is 2 MB larger.
- **Upgrade path for existing users.** Real routers already hold gonuts proofs.
  Migration of stored tokens/keys (and rollback) is a hard requirement, not a
  footnote.
- **Debuggability on the device** — can we attach, log, and reproduce a failure
  on an OpenWrt router with a 64–128 MB RAM class device?
- **Build reproducibility + supply chain** — can we rebuild the exact wallet
  binary ourselves, and how many transitive dependencies are we trusting?
- **Test story** — does the candidate have a fake-mint/regtest harness we can
  run in our CI, or would we own that too?
- **Bus factor honesty** — CDK is ALPHA with API churn; nucula is one person
  with no licence. "Maintained by a proper Cashu developer" needs to mean
  sustained, not merely present.
