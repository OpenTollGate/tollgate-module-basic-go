- **A first payment from a present-but-unlisted client is no longer refused.**
  Wired LAN-side hosts NoDogSplash never intercepts, direct API payments that
  skip the portal page, and NDS entries lapsed between portal load and payment
  were all refused before Receive with `client-not-registered` and told to
  "reconnect to the TollGate Wi-Fi" — a dead end for that class. The purchase
  now proceeds on the same presence proof renewals already accept (the
  socket-resolved MAC), with the valve's bounded auth retry performing the
  just-in-time registration and the durable owed-grant store covering a gate
  that genuinely cannot open
  ([#737](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/737)).
