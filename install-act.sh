#!/bin/bash

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

# Pull required openwrt/sdk image
sudo docker pull openwrt/sdk:mediatek-filogic-23.05.3

# Build the Docker image for act
docker build -f Dockerfile-act -t act-image .

# Run the act-image container with Docker socket mounted and automatically choose Medium image size
echo "Medium" | docker run -i -v /var/run/docker.sock:/var/run/docker.sock act-image