#!/bin/bash

# Exit on any error (including SSH timeouts)
set -e

# compile-cli-to-router.sh - Build and deploy tollgate CLI binary to OpenWrt router
#
# PURPOSE:
#   Development/debugging tool for quickly testing CLI changes on a router.
#   NOT intended for official deployments or production use.
#
# DESCRIPTION:
#   This script cross-compiles the tollgate CLI Go application for the target
#   router architecture and deploys it via SSH/SCP to /usr/bin for system-wide access.
#   Designed for rapid iteration during CLI development and debugging.
#
# USAGE:
#   ./compile-cli-to-router.sh [ROUTER_IP] [OPTIONS]
#
# ARGUMENTS:
#   ROUTER_IP (optional)    - IP address of the target router
#                            Format: X.X.X.X (e.g., 192.168.1.1)
#                            Default: 192.168.1.1
#                            Must be the first argument if provided
#
# OPTIONS:
#   --device=DEVICE        - Target device model for architecture selection
#                           Supported values:
#                           - gl-mt3000 (ARM64 architecture) [default]
#                           - gl-ar300 (MIPS with soft float)
#
# EXAMPLES:
#   ./compile-cli-to-router.sh                    # Deploy to 192.168.1.1 for gl-mt3000
#   ./compile-cli-to-router.sh 192.168.1.100     # Deploy to custom IP for gl-mt3000
#   ./compile-cli-to-router.sh --device=gl-ar300 # Deploy to 192.168.1.1 for gl-ar300
#   ./compile-cli-to-router.sh 192.168.1.100 --device=gl-ar300  # Custom IP and device
#
# REQUIREMENTS:
#   - Go compiler installed and configured
#   - SSH access to the router (uses root user)
#   - TollGate service running on router for CLI to connect to

echo "Compiling CLI to router"

# Default settings
ROUTER_USERNAME=root
ROUTER_IP=192.168.1.1
DEVICE="gl-mt3000"

# Check for router IP as first argument
if [[ $1 =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  ROUTER_IP="$1"
  shift
fi

# Parse remaining command line arguments for device
for i in "$@"; do
  case $i in
    --device=*)
      DEVICE="${i#*=}"
      shift
      ;;
    *)
      ;;
  esac
done

EXECUTABLE_NAME=tollgate
EXECUTABLE_PATH="/usr/bin/$EXECUTABLE_NAME"

cd src/cmd/tollgate-cli

# Build for appropriate architecture based on device
if [[ $DEVICE == "gl-mt3000" ]]; then
  env GOOS=linux GOARCH=arm64 go build -o $EXECUTABLE_NAME -trimpath -ldflags="-s -w"
elif [[ $DEVICE == "gl-ar300" ]]; then
  env GOOS=linux GOARCH=mips GOMIPS=softfloat go build -o $EXECUTABLE_NAME -trimpath -ldflags="-s -w"
else
  echo "Unknown device: $DEVICE"
  exit 1
fi

echo "Copying CLI binary to router system PATH..."
scp -o ConnectTimeout=3 -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -O $EXECUTABLE_NAME $ROUTER_USERNAME@$ROUTER_IP:$EXECUTABLE_PATH
echo "CLI binary installed to system PATH at $EXECUTABLE_PATH"

echo "Setting executable permissions..."
ssh -o ConnectTimeout=3 -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null $ROUTER_USERNAME@$ROUTER_IP "chmod +x $EXECUTABLE_PATH"

echo "Testing CLI connection..."
echo "Running: tollgate status (from system PATH)"
ssh -o ConnectTimeout=3 -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null $ROUTER_USERNAME@$ROUTER_IP "tollgate status" || echo "CLI test failed - this is expected if TollGate service is not running"

echo ""
echo "CLI deployment complete!"
echo "CLI is now available system-wide. SSH to router and test with:"
echo "  tollgate status"
echo "  tollgate wallet drain cashu"
echo "  tollgate --help"

echo "Done"