- **The package never generates an admin credential the operator cannot see.**
  When root had no password and `TOLLGATE_ADMIN_PASSWORD` was unset, the
  package generated a provisional credential and served a first-run page. That
  generated value is no longer created (the `generate_admin_password` helper is
  removed): the package now generates **nothing** and refuses to serve the
  admin board until the operator provides a password — `TOLLGATE_ADMIN_PASSWORD`
  at install time, or `passwd root` on the router, then the gate is re-run. A
  generated-but-withheld password is exactly how routers got locked out when
  the installer deferred one credential while the package generated another and
  the operator ended up holding neither. A provisional marker carried over from
  an older install is still honoured, so an upgraded router keeps telling the
  operator its root password was generated rather than chosen
  ([#884](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/884)).
