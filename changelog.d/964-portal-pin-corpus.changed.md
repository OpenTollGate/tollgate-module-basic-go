- **Portal pin refreshed to portal main @ 22a04f8 (corpus conformance era).**
  The pinned portal tree moves from 6c25834 (2026-10-08) to 22a04f8, shipping
  the admin-board credential-setup fix (#72) and the token-corpus conformance
  suite (#83, #84: vendored canonical fixtures — both keyset-id spellings —
  plus the decode and `POST /` raw-wire regression locks) into built images.
  **This is not a fix for the browser e-cash field incident (portal-site
  #82):** the previous pin already contains the decoder fixes (#38/#55/#61)
  and passes the full token corpus — verified 2026-10-16 by running the
  vendored-corpus suite against the 6c25834 tree (8/8, plus the v1.1
  full-66-hex id spelling). The deployed-build defect observed in #82's
  capture remains root-caused-open: the failing deployment's vintage is
  unidentified (the lab rig's portal port was not serving at capture time),
  and the app-level mint-option flow against a live V2-keyset advertisement
  is the untested surface.
  ([#964](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/964)).
