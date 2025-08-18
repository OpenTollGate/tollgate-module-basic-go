#!/bin/bash

# Exit on any error (including SSH timeouts)
set -e

# compile-to-router.sh - Build and deploy tollgate-wrt binary to OpenWrt router
#
# PURPOSE:
#   Development/debugging tool for quickly testing changes on a router.
#   NOT intended for official deployments or production use.
#
# DESCRIPTION:
#   This script cross-compiles both the TollGate service and CLI client for the target
#   router architecture and deploys them via SSH/SCP. It handles the service
#   lifecycle by stopping the service before deployment and restarting it after.
#   Designed for rapid iteration during development and debugging.
#
# USAGE:
#   ./compile-to-router.sh [ROUTER_IP] [OPTIONS]
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
#   ./compile-to-router.sh                                    # Deploy to 192.168.1.1 for gl-mt3000
#   ./compile-to-router.sh 192.168.1.100                     # Deploy to custom IP for gl-mt3000
#   ./compile-to-router.sh --device=gl-ar300                 # Deploy to 192.168.1.1 for gl-ar300
#   ./compile-to-router.sh 192.168.1.100 --device=gl-ar300  # Custom IP and device
#
# REQUIREMENTS:
#   - Go compiler installed and configured
#   - SSH access to the router (uses root user)
#     * For password-less deployment, set up SSH keys: ssh-copy-id root@router_ip
#   - Router must have the tollgate-basic service configured
echo "Compiling to router"

# Default settings
ROUTER_USERNAME=root
ROUTER_IP=192.168.1.1
DEVICE="gl-mt3000"

# Check for router IP as first argument
if [[ $1 =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  ROUTER_IP="$1"
  shift
fi

# Parse remaining command line arguments
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

# SSH/SCP connection options
SSH_OPTS="-o ConnectTimeout=3 -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null"
EXECUTABLE_NAME=tollgate-wrt
EXECUTABLE_PATH="/usr/bin/$EXECUTABLE_NAME"

cd src

# Build main service
echo "Building TollGate service..."
if [[ $DEVICE == "gl-mt3000" ]]; then
  env GOOS=linux GOARCH=arm64 go build -o $EXECUTABLE_NAME -trimpath -ldflags="-s -w"
elif [[ $DEVICE == "gl-ar300" ]]; then
  env GOOS=linux GOARCH=mips GOMIPS=softfloat go build -o $EXECUTABLE_NAME -trimpath -ldflags="-s -w"
else
  echo "Unknown device: $DEVICE"
  exit 1
fi

# Build CLI client
CLI_NAME=tollgate
CLI_PATH="/usr/bin/$CLI_NAME"
echo "Building CLI client..."
cd cmd/tollgate-cli

# Clean previous CLI builds
rm -f $CLI_NAME
go clean -cache
go mod tidy

if [[ $DEVICE == "gl-mt3000" ]]; then
  env GOOS=linux GOARCH=arm64 go build -o $CLI_NAME -trimpath -ldflags="-s -w"
elif [[ $DEVICE == "gl-ar300" ]]; then
  env GOOS=linux GOARCH=mips GOMIPS=softfloat go build -o $CLI_NAME -trimpath -ldflags="-s -w"
else
  echo "Unknown device: $DEVICE"
  exit 1
fi

# Verify CLI binary was created
if [[ ! -f $CLI_NAME ]]; then
  echo "ERROR: CLI binary was not created!"
  exit 1
fi

echo "CLI binary created: $(ls -la $CLI_NAME)"
cd ../..

# Stop service, deploy both binaries, start service
echo "Stopping service $EXECUTABLE_NAME on router..."
ssh $SSH_OPTS $ROUTER_USERNAME@$ROUTER_IP "service $EXECUTABLE_NAME stop"
echo "Stopped service $EXECUTABLE_NAME on router"

echo "Copying service binary to router..."
scp -O $SSH_OPTS "$EXECUTABLE_NAME" "$ROUTER_USERNAME@$ROUTER_IP:$EXECUTABLE_PATH"
echo "Service binary copied to router"

echo "Copying CLI binary to router..."
scp -O $SSH_OPTS "cmd/tollgate-cli/$CLI_NAME" "$ROUTER_USERNAME@$ROUTER_IP:$CLI_PATH"
echo "CLI binary copied to router at $CLI_PATH"

echo "Setting executable permissions..."
ssh $SSH_OPTS $ROUTER_USERNAME@$ROUTER_IP "chmod +x $CLI_PATH"

echo "Starting service $EXECUTABLE_NAME on router..."
ssh $SSH_OPTS $ROUTER_USERNAME@$ROUTER_IP "service $EXECUTABLE_NAME start"
echo "Started service $EXECUTABLE_NAME on router"

echo "Done"