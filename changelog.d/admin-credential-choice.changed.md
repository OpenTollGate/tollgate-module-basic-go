- **The admin credential can be chosen at install time, and a generated one
  is now marked provisional.** A router whose root password was unset got a
  generated credential printed once — easy to miss, and its only other copy
  went to `/tmp/tollgate-setup.log`, which is tmpfs and is truncated at the
  start of every full-setup run, so an install or upgrade erased the record.
  Two changes: the install honours `TOLLGATE_ADMIN_PASSWORD`, so the operator
  (or a fleet/CI provisioning script) chooses the credential instead of
  receiving one — it is applied and never echoed, and it replaces an existing
  credential when supplied; and when a credential IS generated, the package
  writes `/etc/tollgate/admin-credential-provisional` (no secret in it), which
  is both the signal for the config UI to force a password change at first
  login and the reason a later run re-states the recovery path
  (`passwd root` over SSH, or reinstall with the variable set). A locked root
  account and an unreadable shadow are deliberately exempt; a supplied
  password that does not take still fails closed and drops the board
  ([#708](https://github.com/OpenTollGate/tollgate-module-basic-go/issues/708)).
