- **The cloud-lab client image can build from scratch again.** `Dockerfile.client`
  declared `GO_VERSION` inside the rust-builder stage, where the go-builder
  stage's `FROM` could not see it — every fresh build of the client/killer image
  died at parse (`golang:-bookworm`, #724). The declaration is now a global ARG
  above the first `FROM`, matching `Dockerfile.tollgate`'s placement
  ([#844](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/844)).
