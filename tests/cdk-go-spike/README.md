# cdk-go Evaluation Spike

Spike for evaluating cdk-go (FFI bindings to Rust CDK) as a gonuts replacement on the architectures it supports.

## Build constraints

This spike requires `CGO_ENABLED=1` and only works on Linux amd64 and arm64 because cdk-go ships prebuilt libcdk_ffi.so only for those architectures. MIPS and armv7 are not supported (see issue #176 for prior rejection of CGo/FFI for embedded builds).

## Targets

- `make build`: Build the spike with CGO
- `make test`: Run offline tests
- `make test-network`: Run network-gated tests (requires CDK_SPIKE_NETWORK=1)
- `make tidy`: Clean up dependencies

## Why a separate module

- Keeps cdk-go dependencies out of `src/go.mod` so MIPS and armv7 builds of the main project cannot regress

## Findings

See tracking issue [#271](https://github.com/OpenTollGate/tollgate-module-basic-go/issues/271) for the full evaluation, including the MIPS/armv7 blocker, POC results, options compared, and recommendation.