- **The package refuses to install on a router whose root has no password.** The
  `:8090` admin board's login is checked against root's `/etc/shadow` hash, and
  rpcd's `session.login` accepts **any** password while that hash is empty — so a
  password-less install would leave an unauthenticatable board. Rather than
  install and then withhold the board, the package now refuses **before**
  unpacking: the `preinst` exits non-zero on an empty root hash, so nothing is
  half-installed and the operator is told to `passwd root` and install again. A
  deliberately **locked** root is allowed (a real "password login refused"
  state), and the check is dependency-free (no `jq`, honouring the jq-less
  upgrade lesson). Offline/image builds (`IPKG_INSTROOT`) bypass it, as does the
  documented CI hatch `TOLLGATE_ALLOW_EMPTY_ROOT=1`.

([#907](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/907)).
