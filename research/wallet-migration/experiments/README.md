# Experiments

One directory per experiment, each with a `run.sh` and its raw output committed:

- `experiments/baseline-gonuts/` — footprint and behaviour of the current wallet
- `experiments/cdk-build/` — cross-compile CDK for router arches; record patches
- `experiments/cdk-footprint/` — RSS/threads/storage of a CDK wallet process
- `experiments/nucula-build/` — ESP-IDF build of nucula; measure firmware footprint
- `experiments/integration/` — cgo+FFI vs sidecar prototypes

Rules: no secrets committed; no absolute home paths in scripts; each script
prints its environment facts (arch, OS, revisions) into `env.txt` alongside its
output, so the results are self-describing.
