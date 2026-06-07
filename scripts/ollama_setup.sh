#!/bin/bash

# Ollama Setup Script
# Installs Ollama, configures environment, creates launchd service
# Supports: setup, enable, disable, remove, status

set -e

# Get the directory where this script is located
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# Source common utilities
source "$SCRIPT_DIR/../lib/common.sh"

# Ollama binary locations
OLLAMA_LOCATIONS=(
    "/usr/local/bin/ollama"
    "/opt/homebrew/bin/ollama"
    "/Applications/Ollama.app/Contents/Resources/ollama"
)

# Launchd configuration
PLIST_PATH="/Library/LaunchDaemons/com.ollama.server.plist"
SERVICE_NAME="com.ollama.server"

# Default Ollama configuration
DEFAULT_MAX_MODELS=3
DEFAULT_KEEP_ALIVE_HOURS=24
DEFAULT_NUM_PARALLEL=4
DEFAULT_MAX_CONTEXT=32768
DEFAULT_HOST="0.0.0.0:11434"

# Configuration file
CONFIG_FILE="$HOME/.headless-mac-ollama-config"

# Find Ollama binary
find_ollama_binary() {
    for location in "${OLLAMA_LOCATIONS[@]}"; do
        if [ -f "$location" ]; then
            echo "$location"
            return 0
        fi
    done
    return 1
}

# Check if Ollama is installed
is_ollama_installed() {
    find_ollama_binary > /dev/null 2>&1
}

# Check if Ollama service is running
is_ollama_running() {
    process_running ollama
}

# Check if launchd service is loaded
is_service_loaded() {
    launchd_service_loaded "$SERVICE_NAME"
}

# Test Ollama API
test_ollama_api() {
    local timeout=5
    if curl -s --max-time "$timeout" http://localhost:11434/api/tags > /dev/null 2>&1; then
        return 0
    fi
    return 1
}

# Load configuration from file
load_config() {
    if [ -f "$CONFIG_FILE" ]; then
        source "$CONFIG_FILE"
    fi
}

# Save configuration to file
save_config() {
    cat > "$CONFIG_FILE" << EOF
# Ollama Configuration
# Created: $(date)

MAX_MODELS=${MAX_MODELS}
KEEP_ALIVE_HOURS=${KEEP_ALIVE_HOURS}
KEEP_ALIVE_SECONDS=${KEEP_ALIVE_SECONDS}
NUM_PARALLEL=${NUM_PARALLEL}
MAX_CONTEXT=${MAX_CONTEXT}
OLLAMA_HOST=${OLLAMA_HOST}
OLLAMA_BINARY=${OLLAMA_BINARY}
EOF
    print_status "Configuration saved to $CONFIG_FILE"
}

# Get configuration from user
get_ollama_config() {
    print_info "Ollama configuration:"
    echo ""
    
    read -p "Max loaded models [default: $DEFAULT_MAX_MODELS]: " MAX_MODELS
    MAX_MODELS=${MAX_MODELS:-$DEFAULT_MAX_MODELS}
    
    read -p "Keep alive time in hours [default: $DEFAULT_KEEP_ALIVE_HOURS]: " KEEP_ALIVE_HOURS
    KEEP_ALIVE_HOURS=${KEEP_ALIVE_HOURS:-$DEFAULT_KEEP_ALIVE_HOURS}
    KEEP_ALIVE_SECONDS=$((KEEP_ALIVE_HOURS * 3600))
    
    read -p "Number of parallel requests [default: $DEFAULT_NUM_PARALLEL]: " NUM_PARALLEL
    NUM_PARALLEL=${NUM_PARALLEL:-$DEFAULT_NUM_PARALLEL}
    
    read -p "Max context window [default: $DEFAULT_MAX_CONTEXT]: " MAX_CONTEXT
    MAX_CONTEXT=${MAX_CONTEXT:-$DEFAULT_MAX_CONTEXT}
    
    read -p "Bind to all network interfaces? (y/n) [default: y]: " -n 1 -r BIND_ALL
    echo
    BIND_ALL=${BIND_ALL:-y}
    if [[ $BIND_ALL =~ ^[Yy]$ ]]; then
        OLLAMA_HOST="0.0.0.0:11434"
        print_warning "Binding to all interfaces - ensure firewall is configured!"
    else
        OLLAMA_HOST="127.0.0.1:11434"
    fi
    
    echo ""
    print_info "Configuration:"
    echo "  Max loaded models: $MAX_MODELS"
    echo "  Keep alive: $KEEP_ALIVE_HOURS hours ($KEEP_ALIVE_SECONDS seconds)"
    echo "  Parallel requests: $NUM_PARALLEL"
    echo "  Max context: $MAX_CONTEXT tokens"
    echo "  Bind address: $OLLAMA_HOST"
    echo ""
    
    if ! confirm_action "Confirm configuration?"; then
        print_error "Configuration cancelled"
        exit 1
    fi
}

# Install Ollama
install_ollama() {
    print_info "Installing Ollama via official CLI installer..."

    # Use the CLI installer — places binary at /usr/local/bin/ollama directly,
    # which is what the LaunchDaemon plist expects. Avoids the .app bundle which
    # conflicts with the daemon approach and installs a login item we don't want.
    if ! curl -fsSL https://ollama.com/install.sh | sh; then
        print_error "Ollama installer failed"
        exit 1
    fi

    OLLAMA_BINARY="/usr/local/bin/ollama"

    # Remove any Ollama login item — it conflicts with the LaunchDaemon
    osascript -e 'tell application "System Events" to delete login item "Ollama"' \
        2>/dev/null || true
    # Also kill any app-based instance that may have auto-launched
    pkill -f "Ollama.app" 2>/dev/null || true
    print_status "Ollama login item removed (daemon will manage startup)"

    # Verify installation
    if [ -f "$OLLAMA_BINARY" ]; then
        if check_binary_arm64 "$OLLAMA_BINARY"; then
            local version
            version=$("$OLLAMA_BINARY" --version 2>/dev/null || echo "unknown")
            print_status "Verified: Ollama $version at $OLLAMA_BINARY"
        else
            print_error "Installation verification failed — binary may not be ARM64"
            exit 1
        fi
    else
        print_error "Installation failed — binary not found at $OLLAMA_BINARY"
        exit 1
    fi
}

# Create launchd service
create_launchd_service() {
    print_info "Creating launchd service at $PLIST_PATH..."
    
    # Backup existing plist if it exists
    if [ -f "$PLIST_PATH" ]; then
        backup_file "$PLIST_PATH"
    fi
    
    # Create log directory with correct ownership
    sudo mkdir -p /var/log/ollama
    sudo chown root:wheel /var/log/ollama

    # Create the plist file
    sudo tee "$PLIST_PATH" > /dev/null <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>$SERVICE_NAME</string>

    <key>ProgramArguments</key>
    <array>
        <string>$OLLAMA_BINARY</string>
        <string>serve</string>
    </array>

    <key>RunAtLoad</key>
    <true/>

    <key>KeepAlive</key>
    <true/>

    <key>StandardOutPath</key>
    <string>/var/log/ollama/stdout.log</string>

    <key>StandardErrorPath</key>
    <string>/var/log/ollama/stderr.log</string>

    <key>EnvironmentVariables</key>
    <dict>
        <!-- HOME is required: Ollama panics with 'panic: \$HOME is not defined' without it -->
        <key>HOME</key>
        <string>/var/root</string>
        <key>PATH</key>
        <string>/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin</string>
        <key>OLLAMA_MAX_LOADED_MODELS</key>
        <string>$MAX_MODELS</string>
        <key>OLLAMA_KEEP_ALIVE</key>
        <string>$KEEP_ALIVE_SECONDS</string>
        <key>OLLAMA_NUM_PARALLEL</key>
        <string>$NUM_PARALLEL</string>
        <key>OLLAMA_MAX_CONTEXT</key>
        <string>$MAX_CONTEXT</string>
        <key>OLLAMA_FLASH_ATTENTION</key>
        <string>1</string>
        <key>OLLAMA_NUM_GPU</key>
        <string>1</string>
        <key>OLLAMA_HOST</key>
        <string>$OLLAMA_HOST</string>
        <key>OLLAMA_ORIGINS</key>
        <string>*</string>
    </dict>

    <key>WorkingDirectory</key>
    <string>/tmp</string>

    <key>UserName</key>
    <string>root</string>
</dict>
</plist>
EOF

    print_status "Launchd plist created"

    # Set proper permissions
    sudo chown root:wheel "$PLIST_PATH"
    sudo chmod 644 "$PLIST_PATH"
    print_status "Permissions set"
}

# Show status
show_status() {
    print_header "Ollama Status"
    
    # Check if installed
    if is_ollama_installed; then
        local ollama_path=$(find_ollama_binary)
        print_status "Ollama is installed"
        print_info "Binary: $ollama_path"
        
        if [ -x "$ollama_path" ]; then
            local version=$("$ollama_path" --version 2>/dev/null || echo "unknown")
            print_info "Version: $version"
        fi
    else
        print_warning "Ollama is not installed"
        print_info "Run '$0 setup' to install Ollama"
        return
    fi
    
    print_separator
    
    # Check if service is configured
    if [ -f "$PLIST_PATH" ]; then
        print_status "Launchd service is configured"
        print_info "Plist: $PLIST_PATH"
    else
        print_warning "Launchd service is not configured"
        print_info "Run '$0 setup' to configure service"
    fi
    
    print_separator
    
    # Check if service is loaded
    if is_service_loaded; then
        print_status "Service is loaded"
    else
        print_warning "Service is not loaded"
    fi
    
    # Check if process is running
    if is_ollama_running; then
        print_status "Ollama process is running"
        local pid=$(pgrep -x ollama)
        print_info "PID: $pid"
    else
        print_warning "Ollama process is not running"
    fi
    
    print_separator
    
    # Check API
    if test_ollama_api; then
        print_status "API is responding"
        print_info "Endpoint: http://localhost:11434"
    else
        print_warning "API is not responding"
        if is_ollama_running; then
            print_info "Process is running but API not ready yet"
        fi
    fi
    
    print_separator
    
    # Show configuration
    if [ -f "$CONFIG_FILE" ]; then
        print_info "Configuration file: $CONFIG_FILE"
        load_config
        echo "  Max loaded models: $MAX_MODELS"
        echo "  Keep alive: $KEEP_ALIVE_HOURS hours"
        echo "  Parallel requests: $NUM_PARALLEL"
        echo "  Max context: $MAX_CONTEXT tokens"
        echo "  Bind address: $OLLAMA_HOST"
    fi
    
    print_separator
    
    # Show logs
    print_info "Logs:"
    echo "  Output: /var/log/ollama/stdout.log"
    echo "  Errors: /var/log/ollama/stderr.log"
    if [ -f "/var/log/ollama/stderr.log" ] && [ -s "/var/log/ollama/stderr.log" ]; then
        print_warning "Error log has content - check for issues"
    fi
}

# Setup (install/configure)
setup_ollama() {
    print_header "Ollama Setup"
    
    # Check macOS and architecture
    if ! check_macos; then
        exit 1
    fi
    
    if ! check_apple_silicon_required; then
        exit 1
    fi
    
    print_info "This will:"
    echo "  1. Install Ollama (if not present)"
    echo "  2. Configure Ollama environment"
    echo "  3. Create launchd service for auto-start"
    echo "  4. Start Ollama service"
    echo ""
    
    if ! confirm_action "Continue with Ollama setup?"; then
        print_warning "Setup cancelled"
        exit 0
    fi
    
    # Check if already installed
    if is_ollama_installed; then
        OLLAMA_BINARY=$(find_ollama_binary)
        print_status "Ollama found at $OLLAMA_BINARY"
        
        # Verify ARM64
        if ! check_binary_arm64 "$OLLAMA_BINARY"; then
            if confirm_action "Remove and reinstall?"; then
                print_info "Removing existing installation..."
                for location in "${OLLAMA_LOCATIONS[@]}"; do
                    if [ -f "$location" ]; then
                        sudo rm -f "$location"
                    fi
                done
                if [ -d "/Applications/Ollama.app" ]; then
                    sudo rm -rf "/Applications/Ollama.app"
                fi
                install_ollama
            else
                print_error "Cannot proceed with problematic installation"
                exit 1
            fi
        fi
    else
        install_ollama
    fi
    
    # Stop any running instances
    print_info "Stopping any running Ollama instances..."
    pkill ollama 2>/dev/null || true
    sleep 2
    print_status "Ollama stopped"
    
    print_separator
    
    # Get configuration
    get_ollama_config
    
    # Save configuration
    save_config
    
    print_separator
    
    # Request sudo for launchd
    request_sudo
    
    # Create launchd service
    create_launchd_service
    
    print_separator
    
    # Load and start service (bootstrap/bootout — load/unload deprecated in macOS 15+)
    print_info "Loading Ollama service..."
    sudo launchctl bootout system "$PLIST_PATH" 2>/dev/null || true
    sleep 1
    sudo launchctl bootstrap system "$PLIST_PATH"
    print_status "Service loaded"

    sleep 3
    print_status "Service started"
    
    print_separator
    
    # Verify
    if is_ollama_running; then
        print_status "Ollama is running"
        
        # Test API
        print_info "Testing API endpoint..."
        sleep 2
        if test_ollama_api; then
            print_status "API endpoint responding"
        else
            print_warning "API not responding yet (may still be starting)"
        fi
    else
        print_error "Ollama failed to start"
        print_info "Check logs: tail -f /var/log/ollama/stderr.log"
        exit 1
    fi
    
    print_separator
    print_status "Ollama setup complete"
    print_info "Next steps:"
    echo "  Pull a model: ollama pull qwen2.5-coder:7b"
    echo "  List models: ollama list"
    echo "  Run a model: ollama run qwen2.5-coder:7b"
}

# Enable (start service)
enable_ollama() {
    print_header "Enable Ollama Service"
    
    if ! check_macos; then
        exit 1
    fi
    
    if ! is_ollama_installed; then
        print_error "Ollama is not installed"
        print_info "Run '$0 setup' to install Ollama"
        exit 1
    fi
    
    if [ ! -f "$PLIST_PATH" ]; then
        print_error "Launchd service not configured"
        print_info "Run '$0 setup' to configure service"
        exit 1
    fi
    
    request_sudo
    
    # Load service if not loaded
    if ! is_service_loaded; then
        print_info "Loading service..."
        sudo launchctl bootstrap system "$PLIST_PATH"
        print_status "Service loaded"
    else
        print_status "Service already loaded"
    fi
    
    # Start if not running
    if ! is_ollama_running; then
        print_info "Starting service..."
        sudo launchctl start "$SERVICE_NAME"
        sleep 3
        
        if is_ollama_running; then
            print_status "Service started"
        else
            print_error "Failed to start service"
            exit 1
        fi
    else
        print_status "Service already running"
    fi
    
    # Verify API
    if test_ollama_api; then
        print_status "API is responding"
    else
        print_warning "API not responding yet"
    fi
}

# Disable (stop service)
disable_ollama() {
    print_header "Disable Ollama Service"
    
    if ! check_macos; then
        exit 1
    fi
    
    print_info "This will stop the Ollama service"
    print_warning "Ollama will remain installed but not running"
    echo ""
    
    if ! confirm_action "Disable Ollama service?"; then
        print_warning "Operation cancelled"
        exit 0
    fi
    
    request_sudo
    
    # Stop service
    if is_ollama_running; then
        print_info "Stopping service..."
        sudo launchctl stop "$SERVICE_NAME" 2>/dev/null || true
        pkill ollama 2>/dev/null || true
        sleep 2
        print_status "Service stopped"
    else
        print_status "Service not running"
    fi
    
    # Unload service
    if is_service_loaded; then
        print_info "Unloading service..."
        sudo launchctl bootout system "$PLIST_PATH" 2>/dev/null || true
        print_status "Service unloaded"
    else
        print_status "Service not loaded"
    fi
    
    print_separator
    print_status "Ollama service disabled"
}

# Remove (uninstall)
remove_ollama() {
    print_header "Remove Ollama"
    
    if ! check_macos; then
        exit 1
    fi
    
    print_info "This will completely remove Ollama:"
    echo "  • Stop and remove service"
    echo "  • Remove Ollama binary"
    echo "  • Remove launchd plist"
    echo "  • Clean up logs"
    echo ""
    print_warning "This is a destructive operation!"
    print_info "Model data (~/.ollama) will NOT be removed"
    echo ""
    
    if ! confirm_action "Completely remove Ollama?"; then
        print_warning "Operation cancelled"
        exit 0
    fi
    
    request_sudo
    
    # Stop and unload service
    if is_ollama_running || is_service_loaded; then
        print_info "Stopping service..."
        sudo launchctl bootout system "$PLIST_PATH" 2>/dev/null || true
        pkill ollama 2>/dev/null || true
        sleep 2
        print_status "Service stopped"
    fi
    
    # Remove plist
    if [ -f "$PLIST_PATH" ]; then
        sudo rm -f "$PLIST_PATH"
        print_status "Launchd plist removed"
    fi
    
    # Remove binaries
    for location in "${OLLAMA_LOCATIONS[@]}"; do
        if [ -f "$location" ]; then
            sudo rm -f "$location"
            print_status "Removed $location"
        fi
    done
    
    # Remove app bundle
    if [ -d "/Applications/Ollama.app" ]; then
        sudo rm -rf "/Applications/Ollama.app"
        print_status "Removed /Applications/Ollama.app"
    fi
    
    # Clean up logs
    sudo rm -f /var/log/ollama/stdout.log /var/log/ollama/stderr.log
    sudo rmdir /var/log/ollama 2>/dev/null || true
    print_status "Logs removed"
    
    # Remove config
    if [ -f "$CONFIG_FILE" ]; then
        rm -f "$CONFIG_FILE"
        print_status "Configuration removed"
    fi
    
    # Ask about model data
    if [ -d "$HOME/.ollama" ]; then
        echo ""
        if confirm_action "Remove model data (~/.ollama)?"; then
            rm -rf "$HOME/.ollama"
            print_status "Model data removed"
        else
            print_info "Model data preserved at ~/.ollama"
        fi
    fi
    
    print_separator
    print_status "Ollama removed"
}

# Main command dispatcher
main() {
    local command="${1:-}"
    
    case "$command" in
        setup)
            setup_ollama
            ;;
        enable)
            enable_ollama
            ;;
        disable)
            disable_ollama
            ;;
        remove)
            remove_ollama
            ;;
        status)
            show_status
            ;;
        help|--help|-h)
            show_usage
            ;;
        "")
            print_error "No command specified"
            echo ""
            show_usage
            exit 1
            ;;
        *)
            print_error "Unknown command: $command"
            echo ""
            show_usage
            exit 1
            ;;
    esac
}

# Run main function if script is executed (not sourced)
if [ "${BASH_SOURCE[0]}" = "${0}" ]; then
    main "$@"
fi
