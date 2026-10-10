- **Cashu audit hardening: NUT-07 answers are correlated by Y and the
  journals' renames are directory-fsynced.** A checkstate answer naming a
  foreign or duplicated Y — one the wallet never asked about — is now
  refused instead of counting as evidence for the pending-intent
  decisions, and the receive-intent/owed-grant/quote journals fsync their
  directory after the atomic rename so a power cut cannot drop the latest
  write on ext4. Companion wallet-side fixes for the same audit are
  gonuts-tollgate #38/#39 (tracked as #831/#832/#833)
  ([#843](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/843)).
