# Dockerfile.mint — Builds cdk-mintd with FakeWallet backend for cloud-lab testing.
#
# Uses cargo install from crates.io. The FakeWallet backend automatically
# settles Lightning quotes without a real Lightning node, making it ideal
# for integration testing.
#
# If cdk-mintd is not available on crates.io, build from the cashubtc/cdk
# repo instead:
#   RUN git clone https://github.com/cashubtc/cdk.git /cdk && \
#       cd /cdk && cargo build --release --bin cdk-mintd --features fakewallet,sqlite

FROM rust:1.88-bookworm AS builder

RUN apt-get update && \
    apt-get install -y --no-install-recommends pkg-config libssl-dev protobuf-compiler && \
    rm -rf /var/lib/apt/lists/*

# Build cdk-mintd with only the features we need
# --locked ensures reproducible builds from the published Cargo.lock
RUN cargo install cdk-mintd --no-default-features --features fakewallet,sqlite --locked

# --- Runtime stage ---
FROM debian:bookworm-slim

RUN apt-get update && \
    apt-get install -y --no-install-recommends libssl3 ca-certificates curl && \
    rm -rf /var/lib/apt/lists/*

COPY --from=builder /usr/local/cargo/bin/cdk-mintd /usr/local/bin/cdk-mintd

EXPOSE 8085

CMD ["cdk-mintd"]
