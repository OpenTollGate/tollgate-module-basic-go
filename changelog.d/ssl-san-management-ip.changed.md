- **The generated TLS identity now covers the router's management address.** The
  admin board and LuCI are served on `br-private` (`network.private.ipaddr`), but
  the self-signed identity only carried the hostname, its `.lan` alias and the
  **LAN** IP — so reaching the UI by the **management** IP produced a hostname
  mismatch on top of the expected self-signed warning. `routerSANs` (and the
  coverage check) now include the management IP (de-duplicated), so
  `https://<management-ip>` validates. The identity stays **self-signed** — no
  public CA issues certs for `.lan` — so the browser warning is still expected;
  a managed deployment should install its own CA-signed cert with
  `tollgate ssl apply <cert> <key>`.
