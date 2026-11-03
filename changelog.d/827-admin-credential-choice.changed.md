- **A generated admin credential is marked provisional.** A router whose root
  password was unset once got a generated credential printed once — easy to
  miss, and its only other copy went to `/tmp/tollgate-setup.log`, which is
  tmpfs and is truncated at the start of every full-setup run, so an install or
  upgrade erased the record. The package now generates nothing at all (see the
  0.6.0 no-generated-credentials entry), but the
  `/etc/tollgate/admin-credential-provisional` marker is still written by older
  installs and still read: it is the signal for the config UI to force a
  password change at first login and the reason a later run re-states the
  recovery path (`passwd root` over SSH). A locked root account and an
  unreadable shadow are deliberately exempt
  ([#708](https://github.com/OpenTollGate/tollgate-module-basic-go/issues/708)).
 ([#827](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/827)).
