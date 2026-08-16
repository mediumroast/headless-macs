#!/bin/bash

# Common Utility Functions for Headless Mac Setup Scripts
# Shared functions used across all setup scripts

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Print colored status messages
print_status() {
    echo -e "${GREEN}[✓]${NC} $1"
}

print_error() {
    echo -e "${RED}[✗]${NC} $1"
}

print_warning() {
    echo -e "${YELLOW}[!]${NC} $1"
}

print_info() {
    echo -e "${BLUE}[ℹ]${NC} $1"
}

# Check if running on macOS
check_macos() {
    if [[ "$OSTYPE" != "darwin"* ]]; then
        print_error "This script must be run on macOS"
        return 1
    fi
    return 0
}

# Check if running on Apple Silicon
check_apple_silicon() {
    if [[ $(uname -m) != "arm64" ]]; then
        return 1
    fi
    return 0
}

# Check if running on Apple Silicon with warning option
check_apple_silicon_warn() {
    if ! check_apple_silicon; then
        print_warning "This script is optimized for Apple Silicon (ARM64)"
        print_info "Detected architecture: $(uname -m)"
        return 1
    fi
    return 0
}

# Check if running on Apple Silicon (strict)
check_apple_silicon_required() {
    if ! check_apple_silicon; then
        print_error "This script requires Apple Silicon (ARM64)"
        print_info "Detected architecture: $(uname -m)"
        return 1
    fi
    return 0
}

# Check if binary supports ARM64
check_binary_arm64() {
    local binary_path=$1
    
    if [ ! -f "$binary_path" ]; then
        print_error "Binary not found: $binary_path"
        return 1
    fi
    
    if file "$binary_path" | grep -q "universal binary"; then
        if file "$binary_path" | grep -q "arm64"; then
            return 0
        else
            print_error "Universal binary but no ARM64 support found"
            return 1
        fi
    elif file "$binary_path" | grep -q "arm64"; then
        return 0
    elif file "$binary_path" | grep -q "x86_64"; then
        print_error "Binary is x86_64 only (no ARM64 support)"
        return 1
    else
        print_warning "Could not determine architecture of $binary_path"
        file "$binary_path"
        return 1
    fi
}

# Confirm action with user (y/n prompt)
confirm_action() {
    local prompt="${1:-Continue?}"
    local default="${2:-n}"
    
    if [[ "$default" == "y" ]]; then
        read -p "$prompt (Y/n): " -n 1 -r
    else
        read -p "$prompt (y/n): " -n 1 -r
    fi
    echo
    
    # If default is y, accept empty response as yes
    if [[ "$default" == "y" ]]; then
        [[ -z "$REPLY" || $REPLY =~ ^[Yy]$ ]]
    else
        [[ $REPLY =~ ^[Yy]$ ]]
    fi
}

# Backup a file with timestamp
backup_file() {
    local file_path=$1
    
    if [ ! -f "$file_path" ]; then
        print_warning "File not found, no backup needed: $file_path"
        return 1
    fi
    
    local backup_path="${file_path}.backup.$(date +%Y%m%d_%H%M%S)"
    
    if cp "$file_path" "$backup_path" 2>/dev/null || sudo cp "$file_path" "$backup_path" 2>/dev/null; then
        print_status "Backup created: $backup_path"
        return 0
    else
        print_error "Failed to create backup of $file_path"
        return 1
    fi
}

# Check if command exists
command_exists() {
    command -v "$1" &> /dev/null
}

# Check if a launchd service is loaded
launchd_service_loaded() {
    local service_name=$1
    
    if launchctl list | grep -q "$service_name"; then
        return 0
    fi
    
    # Also check with sudo for system-level daemons
    if sudo launchctl list 2>/dev/null | grep -q "$service_name"; then
        return 0
    fi
    
    return 1
}

# Check if a process is running by name
process_running() {
    local process_name=$1
    pgrep -x "$process_name" > /dev/null 2>&1
}

# Get total system RAM in GB
get_system_ram_gb() {
    sysctl hw.memsize | awk '{print int($2/1024/1024/1024)}'
}

# Get number of CPU cores
get_cpu_cores() {
    sysctl -n hw.ncpu
}

# Print section header
print_header() {
    local title=$1
    echo ""
    echo "=================================================="
    echo "$title"
    echo "=================================================="
    echo ""
}

# Print section separator
print_separator() {
    echo ""
    echo "--------------------------------------------------"
    echo ""
}

# Ask for sudo password upfront if needed
request_sudo() {
    if [ "$EUID" -ne 0 ]; then
        print_info "This operation requires administrator privileges"
        sudo -v
        # Keep sudo alive in background
        while true; do sudo -n true; sleep 60; kill -0 "$$" || exit; done 2>/dev/null &
    fi
}

# Validate script is being run directly (not sourced)
ensure_not_sourced() {
    if [ "${BASH_SOURCE[0]}" = "${0}" ]; then
        return 0
    else
        print_error "This script should be executed, not sourced"
        return 1
    fi
}

# Show usage information
show_usage() {
    local script_name=$(basename "$0")
    cat << EOF
Usage: $script_name <command>

Commands:
    setup      Initial setup/installation
    enable     Enable/start service
    disable    Disable/stop service
    remove     Complete removal/uninstall
    status     Show current status
    help       Show this help message

Examples:
    $script_name setup
    $script_name status
    $script_name enable

EOF
}
