#!/bin/bash

# Store start time
START_TIME=$(date +%s)

# Check if jq is installed
if ! command -v jq &> /dev/null; then
  echo "jq is not installed. Installing jq..."
  sudo apt-get update
  sudo apt-get install -y jq
fi

SECRETS_FILE="../secrets.json"

# Check if secrets file exists
if [ ! -f "$SECRETS_FILE" ]; then
  echo "Error: $SECRETS_FILE not found"
  exit 1
fi

# Get absolute path of secrets file
SECRETS_FILE_ABS=$(readlink -f "$SECRETS_FILE")

# Check if Docker is installed
if ! command -v docker &> /dev/null; then
  echo "Docker is not installed. Installing Docker..."
  sudo apt-get update
  sudo apt-get install -y docker.io
  sudo systemctl start docker
  sudo systemctl enable docker
fi

# Add current user to Docker group if not already
if ! groups $USER | grep -q "docker"; then
  sudo usermod -aG docker $USER
  echo "Added $USER to Docker group. Please log out and log back in to apply changes."
  newgrp docker
fi

# Check if openwrt/sdk image exists locally
if ! docker image inspect openwrt/sdk:mediatek-filogic-23.05.3 > /dev/null 2>&1; then
  echo "Pulling openwrt/sdk:mediatek-filogic-23.05.3 Docker image..."
  sudo docker pull openwrt/sdk:mediatek-filogic-23.05.3
else
  echo "openwrt/sdk:mediatek-filogic-23.05.3 Docker image already exists locally."
fi

# Build the Docker image for act
docker build -f Dockerfile-act -t act-image .

# Get the number of available CPUs
NUM_CPUS=$(nproc)

# Create artifacts directory if it doesn't exist
mkdir -p artifacts

# Run the act-image container with Docker socket mounted, artifacts volume, and secrets.json volume
echo "Medium" | docker run -i --cpus=$NUM_CPUS \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v $(pwd)/artifacts:/github/workspace/artifacts \
  -e NSEC_HEX=$(jq -r '.NSEC_HEX' "$SECRETS_FILE") \
  -e REPO_ACCESS_TOKEN=$(jq -r '.REPO_ACCESS_TOKEN' "$SECRETS_FILE") \
  --rm act-image