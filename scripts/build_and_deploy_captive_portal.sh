#!/bin/bash

# Exit immediately if a command exits with a non-zero status.
set -e

# Define paths relative to the script's directory
SCRIPT_DIR=$(dirname "$0")
MAIN_PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)" # This will be /home/c03rad0r/TG/tollgate-module-basic-go
CAPTIVE_PORTAL_REPO_PATH="$(cd "$MAIN_PROJECT_ROOT/../tollgate-captive-portal-site" && pwd)" # This will be /home/c03rad0r/TG/tollgate-captive-portal-site
DESTINATION_PATH="$MAIN_PROJECT_ROOT/files/tollgate-captive-portal-site"

echo "Starting build and deployment of Tollgate Captive Portal..."

# Step 1: Navigate to the Captive Portal Project Directory
echo "Navigating to ${CAPTIVE_PORTAL_REPO_PATH}..."
cd "${CAPTIVE_PORTAL_REPO_PATH}"

# Step 2: Install Dependencies
echo "Installing npm dependencies..."
npm install --legacy-peer-deps

# Step 3: Install compatible ajv version to fix build issues
echo "Installing compatible ajv version..."
npm install ajv@6.12.6 --force

# Step 4: Build the Project
echo "Building the project..."
npm run build

# Navigate back to the original directory (project root) to use relative paths for rm and cp
echo "Navigating back to original directory: ${MAIN_PROJECT_ROOT}..."
cd "${MAIN_PROJECT_ROOT}"

# Step 5: Remove Existing Captive Portal Files
echo "Removing existing captive portal files from ${DESTINATION_PATH}..."
rm -rf "${DESTINATION_PATH}"/*

# Step 6: Copy Build Output to Destination
echo "Copying new build output to ${DESTINATION_PATH}..."
cp -r "${CAPTIVE_PORTAL_REPO_PATH}/build/"* "${DESTINATION_PATH}/"

# Step 7: Verify Deployment
echo "Verifying deployment in ${DESTINATION_PATH}..."
ls -l "${DESTINATION_PATH}/"

echo "Tollgate Captive Portal build and deployment complete!"