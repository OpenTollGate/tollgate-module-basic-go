- **The cloud-lab client image builds again on buildkit 29.** `Dockerfile.client`
  declared `ARG GO_VERSION` after the first `FROM`, so the go-builder stage's
  `FROM golang:${GO_VERSION}-bookworm` interpolated an empty variable and the
  build died at parse (`failed to parse stage name "golang:-bookworm"`), also
  breaking `run-crash-injection.sh` whose killer image builds from the same
  file. The ARG is now declared before the first `FROM`, matching
  `Dockerfile.tollgate`
  ([#728](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/728)).
