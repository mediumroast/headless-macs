#!/bin/bash

# Colima Setup Script with Ollama Awareness
# Installs and configures Colima with Docker CLI for container workloads
# Intelligently allocates resources based on Ollama usage
# Supports: setup, enable, disable, remove, status

set -e

# Get the directory where this script is located
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# Source common utilities
source "$SCRIPT_DIR/../lib/common.sh"

# Colima launchd configuration
PLIST_PATH="$HOME/Library/LaunchAgents/com.colima.plist"
SERVICE_NAME="com.colima"

# Default resources
DEFAULT_CPU=4
DEFAULT_MEMORY=16
DEFAULT_DISK=100

# Configuration file
CONFIG_FILE="$HOME/.headless-mac-colima-config"

# Check if Colima is installed
is_colima_installed() {
    command_exists colima
}

# Check if Colima is running
is_colima_running() {
    colima status &> /dev/null
}

# Check if Ollama is running (for resource awareness)
is_ollama_running() {
    process_running ollama
}

# Get Ollama resource usage estimate
estimate_ollama_resources() {
    # Check if Ollama is actually running
    if ! is_ollama_running; then
        echo "0"
        return
    fi
    
    # Get memory usage of Ollama process (in GB)
    local ollama_mem=$(ps -o rss= -p $(pgrep -x ollama) 2>/dev/null | awk '{print int($1/1024/1024)}')
    
    if [ -z "$ollama_mem" ] || [ "$ollama_mem" -eq 0 ]; then
        # Default estimate: 8GB for Ollama
        echo "8"
    else
        # Add some buffer (actual + 2GB)
        echo $((ollama_mem + 2))
    fi
}

# Calculate recommended Colima resources
calculate_recommended_resources() {
    local total_ram=$(get_system_ram_gb)
    local total_cpu=$(get_cpu_cores)
    
    print_info "System resources:"
    echo "  Total RAM: ${total_ram}GB"
    echo "  Total CPU: ${total_cpu} cores"
    echo ""
    
    # Check if Ollama is running
    if is_ollama_running; then
        local ollama_ram=$(estimate_ollama_resources)
        print_warning "Ollama is running"
        print_info "Estimated Ollama RAM usage: ${ollama_ram}GB"
        echo ""
        
        # Calculate available resources
        local available_ram=$((total_ram - ollama_ram - 4))  # Leave 4GB for system
        local available_cpu=$((total_cpu - 2))  # Leave 2 cores for Ollama/system
        
        # Ensure minimums
        if [ "$available_ram" -lt 4 ]; then
            available_ram=4
            print_warning "Low available RAM - recommend at least 16GB total for Ollama + Colima"
        fi
        
        if [ "$available_cpu" -lt 2 ]; then
            available_cpu=2
        fi
        
        RECOMMENDED_CPU=$available_cpu
        RECOMMENDED_MEMORY=$available_ram
        RECOMMENDED_DISK=$DEFAULT_DISK
        
        print_info "Recommended Colima resources (with Ollama):"
        echo "  CPU: $RECOMMENDED_CPU cores (leaving $(( total_cpu - available_cpu )) for Ollama/system)"
        echo "  RAM: ${RECOMMENDED_MEMORY}GB (leaving ${ollama_ram}GB for Ollama + 4GB for system)"
        echo "  Disk: ${RECOMMENDED_DISK}GB"
        
    else
        print_info "Ollama is not running"
        
        # More generous allocation without Ollama
        local available_ram=$((total_ram - 4))  # Leave 4GB for system
        local available_cpu=$((total_cpu - 2))  # Leave 2 cores for system
        
        # Use defaults if enough resources
        if [ "$available_ram" -ge $DEFAULT_MEMORY ]; then
            RECOMMENDED_MEMORY=$DEFAULT_MEMORY
        else
            RECOMMENDED_MEMORY=$available_ram
        fi
        
        if [ "$available_cpu" -ge $DEFAULT_CPU ]; then
            RECOMMENDED_CPU=$DEFAULT_CPU
        else
            RECOMMENDED_CPU=$available_cpu
        fi
        
        RECOMMENDED_DISK=$DEFAULT_DISK
        
        print_info "Recommended Colima resources:"
        echo "  CPU: $RECOMMENDED_CPU cores"
        echo "  RAM: ${RECOMMENDED_MEMORY}GB"
        echo "  Disk: ${RECOMMENDED_DISK}GB"
    fi
    
    echo ""
}

# Get Colima configuration from user
get_colima_config() {
    calculate_recommended_resources
    
    print_info "Colima configuration:"
    echo ""
    
    read -p "CPU cores for Colima [default: $RECOMMENDED_CPU]: " COLIMA_CPU
    COLIMA_CPU=${COLIMA_CPU:-$RECOMMENDED_CPU}
    
    read -p "Memory in GB [default: $RECOMMENDED_MEMORY]: " COLIMA_MEMORY
    COLIMA_MEMORY=${COLIMA_MEMORY:-$RECOMMENDED_MEMORY}
    
    read -p "Disk size in GB [default: $RECOMMENDED_DISK]: " COLIMA_DISK
    COLIMA_DISK=${COLIMA_DISK:-$RECOMMENDED_DISK}
    
    echo ""
    print_info "Configuration summary:"
    echo "  CPUs: $COLIMA_CPU"
    echo "  Memory: ${COLIMA_MEMORY}GB"
    echo "  Disk: ${COLIMA_DISK}GB"
    echo "  Architecture: $(uname -m)"
    echo ""
    
    if ! confirm_action "Start Colima with these settings?"; then
        print_error "Configuration cancelled"
        exit 1
    fi
}

# Save configuration
save_config() {
    cat > "$CONFIG_FILE" << EOF
# Colima Configuration
# Created: $(date)

COLIMA_CPU=${COLIMA_CPU}
COLIMA_MEMORY=${COLIMA_MEMORY}
COLIMA_DISK=${COLIMA_DISK}
COLIMA_ARCH=$(uname -m)
EOF
    print_status "Configuration saved to $CONFIG_FILE"
}

# Load configuration
load_config() {
    if [ -f "$CONFIG_FILE" ]; then
        source "$CONFIG_FILE"
    fi
}

# Start Colima with configuration
start_colima() {
    local cpu=${1:-$DEFAULT_CPU}
    local memory=${2:-$DEFAULT_MEMORY}
    local disk=${3:-$DEFAULT_DISK}
    
    print_info "Starting Colima..."
    
    if check_apple_silicon; then
        colima start \
            --arch aarch64 \
            --cpu "$cpu" \
            --memory "$memory" \
            --disk "$disk" \
            --vm-type vz \
            --vz-rosetta \
            --mount-type virtiofs \
            --network-address
    else
        colima start \
            --cpu "$cpu" \
            --memory "$memory" \
            --disk "$disk"
    fi
    
    print_status "Colima started successfully"
}

# Create launchd service for auto-start
create_launchd_service() {
    print_info "Creating launchd plist for auto-start..."
    
    mkdir -p "$HOME/Library/LaunchAgents"
    
    cat > "$PLIST_PATH" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>$SERVICE_NAME</string>
    
    <key>ProgramArguments</key>
    <array>
        <string>$(which colima)</string>
        <string>start</string>
        <string>--foreground</string>
    </array>
    
    <key>RunAtLoad</key>
    <true/>
    
    <key>KeepAlive</key>
    <false/>
    
    <key>StandardOutPath</key>
    <string>/tmp/colima.log</string>
    
    <key>StandardErrorPath</key>
    <string>/tmp/colima.err</string>
</dict>
</plist>
EOF
    
    print_status "Launchd plist created at $PLIST_PATH"
}

# Show status
show_status() {
    print_header "Colima Status"
    
    # Check if installed
    if is_colima_installed; then
        print_status "Colima is installed"
        local colima_version=$(colima version | head -1)
        print_info "Version: $colima_version"
    else
        print_warning "Colima is not installed"
        print_info "Run '$0 setup' to install Colima"
        return
    fi
    
    print_separator
    
    # Check if running
    if is_colima_running; then
        print_status "Colima is running"
        echo ""
        colima status
    else
        print_warning "Colima is not running"
        print_info "Run '$0 enable' to start Colima"
    fi
    
    print_separator
    
    # Check Docker connectivity
    if command_exists docker; then
        print_status "Docker CLI is installed"
        if docker info &> /dev/null; then
            print_status "Docker CLI connected to Colima"
        else
            print_warning "Docker CLI cannot connect to Colima"
        fi
    else
        print_warning "Docker CLI is not installed"
    fi
    
    print_separator
    
    # Check if auto-start is configured
    if [ -f "$PLIST_PATH" ]; then
        print_status "Auto-start is configured"
        print_info "Plist: $PLIST_PATH"
    else
        print_warning "Auto-start is not configured"
        print_info "Run '$0 setup' and enable auto-start"
    fi
    
    print_separator
    
    # Show Ollama connectivity info
    print_info "Container to Host Ollama connectivity:"
    if is_ollama_running; then
        print_status "Ollama is running on host"
        echo "  Access from containers: http://host.docker.internal:11434"
        echo "  Environment variable: OLLAMA_BASE_URL=http://host.docker.internal:11434"
    else
        print_warning "Ollama is not running on host"
        print_info "Install Ollama with: $SCRIPT_DIR/ollama_setup.sh setup"
    fi
    
    print_separator
    
    # Show configuration
    if [ -f "$CONFIG_FILE" ]; then
        print_info "Configuration file: $CONFIG_FILE"
        load_config
        echo "  CPUs: $COLIMA_CPU"
        echo "  Memory: ${COLIMA_MEMORY}GB"
        echo "  Disk: ${COLIMA_DISK}GB"
    fi
}

# Setup (install/configure)
setup_colima() {
    print_header "Colima Setup"
    
    # Check macOS
    if ! check_macos; then
        exit 1
    fi
    
    check_apple_silicon_warn
    
    print_info "This will:"
    echo "  1. Install Homebrew (if needed)"
    echo "  2. Install Colima"
    echo "  3. Install Docker CLI and Docker Compose"
    echo "  4. Configure Colima for containers"
    echo "  5. Set up auto-start (optional)"
    echo ""
    
    if is_ollama_running; then
        print_warning "Ollama is running - will optimize resources accordingly"
        echo ""
    fi
    
    if ! confirm_action "Continue with Colima setup?"; then
        print_warning "Setup cancelled"
        exit 0
    fi
    
    # Ensure Homebrew is installed
    if ! command_exists brew; then
        print_info "Homebrew is required for Colima installation"
        if confirm_action "Install Homebrew first?"; then
            "$SCRIPT_DIR/homebrew_setup.sh" setup
        else
            print_error "Homebrew is required - cancelling"
            exit 1
        fi
    fi
    
    print_separator
    print_header "Step 1: Install Colima"
    
    if is_colima_installed; then
        print_status "Colima already installed"
        local colima_version=$(colima version | head -1)
        print_info "Version: $colima_version"
        
        if confirm_action "Upgrade to latest version?"; then
            brew upgrade colima
            print_status "Colima upgraded"
        fi
    else
        print_info "Installing Colima..."
        brew install colima
        print_status "Colima installed"
    fi
    
    print_separator
    print_header "Step 2: Install Docker Tools"
    
    # Install Docker CLI
    if command_exists docker; then
        print_status "Docker CLI already installed"
        docker --version
    else
        print_info "Installing Docker CLI..."
        brew install docker
        print_status "Docker CLI installed"
    fi
    
    # Install Docker Compose
    if command_exists docker-compose; then
        print_status "Docker Compose already installed"
        docker-compose --version
    else
        print_info "Installing Docker Compose..."
        brew install docker-compose
        print_status "Docker Compose installed"
    fi
    
    # Install Docker Buildx
    if docker buildx version &> /dev/null; then
        print_status "Docker Buildx already available"
    else
        print_info "Installing Docker Buildx..."
        brew install docker-buildx
        print_status "Docker Buildx installed"
    fi
    
    print_separator
    print_header "Step 3: Configure Colima"
    
    # Check if already running
    if is_colima_running; then
        print_warning "Colima is already running"
        colima status
        echo ""
        
        if confirm_action "Stop and reconfigure?"; then
            print_info "Stopping Colima..."
            colima stop
            print_status "Colima stopped"
        else
            print_info "Keeping current configuration"
            print_status "Setup complete"
            return
        fi
    fi
    
    # Get configuration from user
    get_colima_config
    
    # Save configuration
    save_config
    
    # Start Colima
    start_colima "$COLIMA_CPU" "$COLIMA_MEMORY" "$COLIMA_DISK"
    
    print_separator
    print_header "Step 4: Verify Installation"
    
    # Verify Colima is running
    if is_colima_running; then
        print_status "Colima is running"
        colima status
    else
        print_error "Colima failed to start"
        exit 1
    fi
    
    print_separator
    
    # Verify Docker CLI
    print_info "Testing Docker CLI..."
    if docker info &> /dev/null; then
        print_status "Docker CLI connected to Colima"
    else
        print_error "Docker CLI cannot connect to Colima"
        exit 1
    fi
    
    # Test with hello-world
    print_info "Running test container..."
    if docker run --rm hello-world &> /dev/null; then
        print_status "Docker containers work correctly"
    else
        print_warning "Test container failed"
    fi
    
    print_separator
    
    # Show Docker info
    print_info "Docker environment:"
    docker info | grep -E "Server Version|Operating System|Architecture|CPUs|Total Memory"
    
    print_separator
    print_header "Step 5: Auto-start Configuration (Optional)"
    
    print_info "Colima can start automatically on login using launchd"
    
    if confirm_action "Set up auto-start?"; then
        create_launchd_service
        
        # Load the plist
        launchctl unload "$PLIST_PATH" 2>/dev/null || true
        launchctl load "$PLIST_PATH"
        print_status "Auto-start configured"
        print_info "Colima will start automatically on login"
    else
        print_info "Skipping auto-start configuration"
        print_warning "You'll need to run 'colima start' manually after reboots"
    fi
    
    print_separator
    print_header "Setup Complete!"
    
    print_status "Colima and Docker are ready to use"
    echo ""
    print_info "Useful commands:"
    echo "  colima status         - Check Colima status"
    echo "  docker ps             - List running containers"
    echo "  docker info           - Show Docker info"
    echo ""
    
    if is_ollama_running; then
        print_info "Connecting to Ollama from containers:"
        echo "  Host: http://host.docker.internal:11434"
        echo "  Example: docker run -e OLLAMA_BASE_URL=http://host.docker.internal:11434 ..."
        echo ""
    fi
    
    print_info "Colima VM details:"
    colima status
}

# Enable (start Colima)
enable_colima() {
    print_header "Enable Colima"
    
    if ! check_macos; then
        exit 1
    fi
    
    if ! is_colima_installed; then
        print_error "Colima is not installed"
        print_info "Run '$0 setup' to install Colima"
        exit 1
    fi
    
    if is_colima_running; then
        print_status "Colima is already running"
        colima status
        return
    fi
    
    # Load config if available
    load_config
    
    # Start with saved config or defaults
    if [ -n "$COLIMA_CPU" ]; then
        start_colima "$COLIMA_CPU" "$COLIMA_MEMORY" "$COLIMA_DISK"
    else
        print_info "No saved configuration found, using defaults"
        start_colima
    fi
    
    # Load launchd if configured
    if [ -f "$PLIST_PATH" ]; then
        launchctl unload "$PLIST_PATH" 2>/dev/null || true
        launchctl load "$PLIST_PATH"
        print_status "Auto-start enabled"
    fi
    
    print_separator
    
    if is_colima_running; then
        print_status "Colima is running"
        colima status
    else
        print_error "Failed to start Colima"
        exit 1
    fi
}

# Disable (stop Colima)
disable_colima() {
    print_header "Disable Colima"
    
    if ! check_macos; then
        exit 1
    fi
    
    if ! is_colima_installed; then
        print_error "Colima is not installed"
        exit 1
    fi
    
    print_info "This will stop Colima and disable auto-start"
    print_warning "Colima will remain installed but not running"
    echo ""
    
    if ! confirm_action "Disable Colima?"; then
        print_warning "Operation cancelled"
        exit 0
    fi
    
    # Stop Colima
    if is_colima_running; then
        print_info "Stopping Colima..."
        colima stop
        print_status "Colima stopped"
    else
        print_status "Colima is not running"
    fi
    
    # Unload launchd
    if [ -f "$PLIST_PATH" ]; then
        launchctl unload "$PLIST_PATH" 2>/dev/null || true
        print_status "Auto-start disabled"
    fi
    
    print_separator
    print_status "Colima disabled"
}

# Remove (uninstall)
remove_colima() {
    print_header "Remove Colima"
    
    if ! check_macos; then
        exit 1
    fi
    
    print_info "This will completely remove Colima:"
    echo "  • Stop Colima"
    echo "  • Delete Colima VM and data"
    echo "  • Remove auto-start configuration"
    echo "  • Optionally uninstall via Homebrew"
    echo ""
    print_warning "This is a destructive operation!"
    print_warning "All containers and images will be deleted"
    echo ""
    
    if ! confirm_action "Completely remove Colima?"; then
        print_warning "Operation cancelled"
        exit 0
    fi
    
    # Stop and delete Colima
    if is_colima_installed; then
        if is_colima_running; then
            print_info "Stopping Colima..."
            colima stop
            print_status "Colima stopped"
        fi
        
        print_info "Deleting Colima VM..."
        colima delete --force
        print_status "Colima VM deleted"
    fi
    
    # Remove launchd plist
    if [ -f "$PLIST_PATH" ]; then
        launchctl unload "$PLIST_PATH" 2>/dev/null || true
        rm -f "$PLIST_PATH"
        print_status "Auto-start configuration removed"
    fi
    
    # Remove config
    if [ -f "$CONFIG_FILE" ]; then
        rm -f "$CONFIG_FILE"
        print_status "Configuration removed"
    fi
    
    # Ask about uninstalling from Homebrew
    if is_colima_installed; then
        echo ""
        if confirm_action "Uninstall Colima via Homebrew?"; then
            brew uninstall colima
            print_status "Colima uninstalled"
        else
            print_info "Colima package left installed"
        fi
    fi
    
    print_separator
    print_status "Colima removed"
}

# Main command dispatcher
main() {
    local command="${1:-}"
    
    case "$command" in
        setup)
            setup_colima
            ;;
        enable)
            enable_colima
            ;;
        disable)
            disable_colima
            ;;
        remove)
            remove_colima
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
