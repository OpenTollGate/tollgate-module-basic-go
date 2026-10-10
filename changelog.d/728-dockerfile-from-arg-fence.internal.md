- **A contract fence now pins FROM-line ARG scope in the cloud-lab Dockerfiles.**
  `check-dockerfile-from-args.py` statically proves every `${VAR}` a
  `Dockerfile*` `FROM` line interpolates is declared before the first `FROM` —
  the mid-file-ARG class that broke the client image (#724) — and refuses to
  pass when it discovers no Dockerfiles at all, so an out-of-tree run can no
  longer green-light nothing
  ([#728](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/728)).
