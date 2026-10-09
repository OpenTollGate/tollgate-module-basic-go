- **Both package formats now ship the GPL license text.** The SDK-free
  `.ipk` lanes installed the GPL-3.0 text while the SDK `.apk` lane was
  metadata-only — the same version shipped different contents per format,
  and the ~35 KB license file was the lone content delta between the lanes.
  The `.apk` recipe now installs it at the same payload path
  (`/usr/share/doc/tollgate-wrt/LICENSE`), requires it at build time (a GPL
  build that cannot find its license fails loudly), and the artifact checks
  hard-fail any package missing it
  ([#787](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/787)).
