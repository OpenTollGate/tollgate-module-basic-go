- **The package never sets or generates a credential the operator did not
  choose.** When root had no password the package once generated a provisional
  credential and served a first-run page to set it; a later revision instead set
  root from `TOLLGATE_ADMIN_PASSWORD`, or — because apk-tools 3 (OpenWrt >=
  25.12) does not forward the install environment to package scriptlets — from a
  root-only file (`/etc/tollgate/admin-password`, and
  `/etc/tollgate/wifi-password` for the private-WiFi passphrase). **Both are
  gone.** The package now sets **nothing**: it refuses to serve the admin board
  until the operator has a password (set with `passwd root`, or the LuCI
  password page) and re-runs the setup, and on the private WiFi it leaves the AP
  **disabled** until a passphrase is set with `tollgate network private
  set-password` (the wired `br-private` management bridge is unaffected). A
  non-interactive "supply a value" mechanism is too easy to mis-fire: an
  operator who pasted the run-book's **example** value verbatim ended up with a
  password they never chose and no link to it. A provisional marker carried over
  from an older install is still honoured, so an upgraded router keeps telling
  the operator its root password was generated rather than chosen, and a
  deliberately **locked** root account is never re-enabled
  ([#884](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/884)).
