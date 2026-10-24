- **A fresh install now sets a fixed default admin and management-WiFi
  password (`TollGate`) instead of refusing.** rc2/rc3 left a credential-less
  router with the admin board switched OFF and required the operator to run
  `passwd root` before the package would install at all — a burden that was
  also lane-inconsistent, because apk-tools 3 treats a failing `pre-install` as
  non-fatal and the `.apk` landed anyway. The uci-defaults now set root's
  password to a well-known constant and the private (management) WiFi key to
  the same value, mark the admin credential PROVISIONAL, and serve the board;
  the admin/config UI forces the operator to choose their own password at first
  login (shipped separately, post-release). The package still accepts NO
  install-time credential — `TOLLGATE_ADMIN_PASSWORD` and a staged
  `/etc/tollgate/admin-password` file stay ignored — and an operator's own
  password is never rewritten. The `preinst` refusal is removed (now a no-op);
  the write still fails CLOSED if the default cannot be set.
