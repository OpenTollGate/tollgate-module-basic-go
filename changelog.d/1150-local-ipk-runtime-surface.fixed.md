- **The local `.ipk` lane now ships the full management surface.** The
  reproducible local builder staged the admin SPA, rpcd plugin, ACL and
  `999-tollgate-admin-setup` only as preflight checks; it now installs them,
  along with the CVM daemon and wallet-storage helper. The artifact-content
  gate hard-fails any package missing the admin board, admin setup script,
  rpcd/ACL, CVM files, or guest locale catalogues, so the local and SDK lanes
  cannot silently diverge again.
