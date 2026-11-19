- **Secret scanning now runs on every surface a commit can reach.** The org
  gitleaks enrollment watches `main` on push and every PR
  ([#657](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/657),
  porting the archived fork's #91/#98), one in-repo `.gitleaks.toml` drives
  both the cheap PR lane and the proof-grade main lane through the
  org-wide reusable scanner
  ([#759](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/759),
  config via
  [#758](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/758)),
  and the weekly deep scan plus tags-release lane keep full-history coverage
  frugal
  ([#820](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/820))
  ([#1248](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/1248))
