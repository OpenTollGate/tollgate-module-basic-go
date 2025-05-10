#!/bin/bash

# Check if the system is Linux
if [ "$(uname -s)" != "Linux" ]; then
    echo "Error: This script can only be run on Linux systems."
    echo "Current system: $(uname -s)"
    exit 1
fi

# Check if running with sudo/root privileges
if [ "$(id -u)" -ne 0 ]; then
    echo "Error: This script must be run with sudo or as root."
    echo "Please run: sudo $0"
    exit 1
fi

# Set source and destination paths
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SOURCE_PATH="${SCRIPT_DIR}/init.d/tollgate"
DEST_PATH="/etc/init.d/tollgate"

# Check if source file exists
if [ ! -f "$SOURCE_PATH" ]; then
    echo "Error: Source file not found: $SOURCE_PATH"
    exit 1
fi

# Install the service script
echo "Installing tollgate service..."
cp "$SOURCE_PATH" "$DEST_PATH"

# Make it executable
chmod +x "$DEST_PATH"

echo "Success! Tollgate service has been installed to $DEST_PATH"
echo "You can now manage the service with:"
echo "  sudo service tollgate start|stop|restart|status"
echo "To enable the service to start at boot:"
echo "  sudo update-rc.d tollgate defaults"

exit 0
