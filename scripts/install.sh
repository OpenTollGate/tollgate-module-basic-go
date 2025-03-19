#!/bin/bash

# ANSI color codes for attractive output
GREEN='\033[0;32m'
BLUE='\033[0;34m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m' # No Color

# Function to display a nice header
print_header() {
    local text="$1"
    local width=60
    local line=$(printf "%${width}s" | tr ' ' '-')
    
    echo -e "\n${BLUE}$line${NC}"
    echo -e "${BLUE}$(printf "%-${width}s" "   $text")${NC}"
    echo -e "${BLUE}$line${NC}\n"
}

# Function to display success/error messages
print_status() {
    local status="$1"
    local message="$2"
    
    if [ "$status" -eq 0 ]; then
        echo -e "${GREEN}✓ SUCCESS${NC}: $message"
    else
        echo -e "${RED}✗ FAILED${NC}: $message"
    fi
}

# Function to run a command with visual feedback
run_step() {
    local step_name="$1"
    local command="$2"
    
    echo -e "${YELLOW}► Running:${NC} $step_name..."
    eval "$command"
    local status=$?
    
    if [ $status -eq 0 ]; then
        print_status 0 "$step_name completed successfully"
    else
        print_status 1 "$step_name failed with exit code $status"
        exit $status
    fi
    
    echo ""
    return $status
}

# Check if running with sudo/root privileges
if [ "$(id -u)" -ne 0 ]; then
    echo -e "${RED}Error:${NC} This script must be run with sudo or as root."
    echo -e "Please run: ${YELLOW}sudo $0${NC}"
    exit 1
fi

# Main installation process
print_header "Tollgate Installation Script"

echo -e "This script will install and configure the following components:"
echo -e "  ${BLUE}•${NC} SFTP Server"
echo -e "  ${BLUE}•${NC} Tollgate Service\n"

# Check if the required scripts exist
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SFTP_SCRIPT="${SCRIPT_DIR}/install-sftp-server.sh"
TOLLGATE_SCRIPT="${SCRIPT_DIR}/install-tollgate-service.sh"

if [ ! -f "$SFTP_SCRIPT" ]; then
    echo -e "${RED}Error:${NC} SFTP installation script not found at: $SFTP_SCRIPT"
    exit 1
fi

if [ ! -f "$TOLLGATE_SCRIPT" ]; then
    echo -e "${RED}Error:${NC} Tollgate service installation script not found at: $TOLLGATE_SCRIPT"
    exit 1
fi

# Make scripts executable
chmod +x "$SFTP_SCRIPT" "$TOLLGATE_SCRIPT"

# Run the installation scripts
run_step "SFTP Server Installation" "$SFTP_SCRIPT"
run_step "Tollgate Service Installation" "$TOLLGATE_SCRIPT"

# Final success message
print_header "Installation Complete"
echo -e "${GREEN}Tollgate and its dependencies have been successfully installed!${NC}"
echo -e "\nYou can now:"
echo -e "  ${BLUE}•${NC} Access the SFTP server on port 22"
echo -e "  ${BLUE}•${NC} Control the tollgate service with: ${YELLOW}sudo service tollgate {start|stop|restart|status}${NC}"
echo -e "\nThank you for installing Tollgate.\n"

exit 0
