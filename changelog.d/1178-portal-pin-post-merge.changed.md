- **The portal pin now follows the merged Cobrador portal main.** The module
  re-pins the captive portal from the old feature-branch SHA `5fd4cb32` to the
  squash-merged portal main commit `38206d78`, regenerates the guest/admin
  bundle, rpcd plugin/ACL and admin setup from that commit, and refreshes the
  build contract so shipped artifacts match the merged portal tree.
  ([#1178](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/1178)).
