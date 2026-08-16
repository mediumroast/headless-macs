#!/bin/bash

# Headless Mac Mini M4 Setup Script
# Configures power management and Ollama service for 24/7 LLM inference
# macOS 26 Tahoe compatible
#
# ⚠️  DEPRECATED: This script has been refactored into modular components
# ⚠️  Please use the new modular scripts instead:
# ⚠️    ./setup.sh install all          # Full setup
# ⚠️    ./scripts/power_management.sh   # Power management only
# ⚠️    ./scripts/ollama_setup.sh       # Ollama only
#
# This script will continue to work but is no longer maintained.

set -e  # Exit on error

echo "=================================================="
echo "⚠️  DEPRECATED SCRIPT"
echo "=================================================="
echo ""
echo "This script has been replaced by modular components."
echo ""
echo "New usage:"
echo "  ./setup.sh install all          # Full setup"
echo "  ./scripts/power_management.sh   # Power management"
echo "  ./scripts/ollama_setup.sh       # Ollama only"
echo ""
echo "This script will continue working but is not maintained."
echo ""
read -p "Continue with this deprecated script? (y/n): " -n 1 -r
echo
if [[ ! $REPLY =~ ^[Yy]$ ]]; then
    echo "Cancelled. Please use ./setup.sh instead."
    exit 0
fi
echo ""
echo "=================================================="
echo "Headless Mac Mini M4 Setup Script"
echo "=================================================="
echo ""

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Function to print colored output
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
    echo -e "${NC}[ℹ]${NC} $1"
}

# Function to check if binary supports ARM64
check_binary_arm64() {
    local binary_path=$1
    
    if file "$binary_path" | grep -q "universal binary"; then
        if file "$binary_path" | grep -q "arm64"; then
            print_status "Ollama is a universal binary (includes ARM64)"
            return 0
        else
            print_error "Universal binary but no ARM64 support found"
            return 1
        fi
    elif file "$binary_path" | grep -q "arm64"; then
        print_status "Ollama is native ARM64"
        return 0
    elif file "$binary_path" | grep -q "x86_64"; then
        print_error "Ollama is x86_64 only (no ARM64 support)"
        return 1
    else
        print_warning "Could not determine architecture"
        file "$binary_path"
        return 1
    fi
}

# Check if running on macOS
if [[ "$OSTYPE" != "darwin"* ]]; then
    print_error "This script must be run on macOS"
    exit 1
fi

# Check if running on Apple Silicon
if [[ $(uname -m) != "arm64" ]]; then
    print_error "This script is designed for Apple Silicon Macs"
    exit 1
fi

echo ""
print_info "This script will:"
echo "  1. Install Ollama (if not present)"
echo "  2. Configure power management for headless operation"
echo "  3. Create launchd service for Ollama"
echo "  4. Configure Ollama environment variables"
echo "  5. Set up auto-start on boot"
echo ""

read -p "Continue? (y/n): " -n 1 -r
echo
if [[ ! $REPLY =~ ^[Yy]$ ]]; then
    print_warning "Setup cancelled"
    exit 0
fi

echo ""
echo "=================================================="
echo "Step 1: Ollama Installation"
echo "=================================================="
echo ""

OLLAMA_BINARY=""

# Check for Ollama in different locations
if [ -f "/usr/local/bin/ollama" ]; then
    OLLAMA_BINARY="/usr/local/bin/ollama"
    print_status "Ollama found at /usr/local/bin/ollama"
elif [ -f "/opt/homebrew/bin/ollama" ]; then
    OLLAMA_BINARY="/opt/homebrew/bin/ollama"
    print_status "Ollama found at /opt/homebrew/bin/ollama"
elif [ -f "/Applications/Ollama.app/Contents/Resources/ollama" ]; then
    OLLAMA_BINARY="/Applications/Ollama.app/Contents/Resources/ollama"
    print_status "Ollama found at /Applications/Ollama.app"
fi

# Verify existing installation
if [ -n "$OLLAMA_BINARY" ]; then
    print_info "Verifying Ollama installation..."
    
    if check_binary_arm64 "$OLLAMA_BINARY"; then
        print_status "Ollama installation verified"
        
        # Show version
        OLLAMA_VERSION=$("$OLLAMA_BINARY" --version 2>/dev/null || echo "unknown")
        print_info "Ollama version: $OLLAMA_VERSION"
    else
        print_error "Ollama installation verification failed"
        read -p "Remove and reinstall? (y/n): " -n 1 -r
        echo
        if [[ $REPLY =~ ^[Yy]$ ]]; then
            print_info "Removing existing installation..."
            if [ -f "/usr/local/bin/ollama" ]; then
                sudo rm -f /usr/local/bin/ollama
            fi
            if [ -f "/opt/homebrew/bin/ollama" ]; then
                brew uninstall ollama 2>/dev/null || sudo rm -f /opt/homebrew/bin/ollama
            fi
            if [ -d "/Applications/Ollama.app" ]; then
                sudo rm -rf /Applications/Ollama.app
            fi
            OLLAMA_BINARY=""
            print_status "Removed"
        else
            print_error "Cannot proceed with problematic installation"
            exit 1
        fi
    fi
fi

# Install Ollama if not present
if [ -z "$OLLAMA_BINARY" ]; then
    print_info "Ollama not found. Installing from ollama.com..."
    
    # Create temporary directory
    TMP_DIR=$(mktemp -d)
    cd "$TMP_DIR"
    
    print_info "Downloading Ollama installer..."
    
    # Download the latest Ollama installer
    if ! curl -L https://ollama.com/download/Ollama-darwin.zip -o Ollama.zip; then
        print_error "Failed to download Ollama"
        rm -rf "$TMP_DIR"
        exit 1
    fi
    
    print_status "Download complete"
    
    print_info "Extracting installer..."
    if ! unzip -q Ollama.zip; then
        print_error "Failed to extract Ollama"
        rm -rf "$TMP_DIR"
        exit 1
    fi
    
    print_status "Extraction complete"
    
    # Check what we got
    if [ -d "Ollama.app" ]; then
        # App bundle - install to /Applications and symlink binary
        print_info "Installing Ollama.app to /Applications..."
        sudo mv Ollama.app /Applications/
        
        # Create symlink to /usr/local/bin for CLI access
        sudo mkdir -p /usr/local/bin
        sudo ln -sf /Applications/Ollama.app/Contents/Resources/ollama /usr/local/bin/ollama
        
        OLLAMA_BINARY="/usr/local/bin/ollama"
        print_status "Ollama installed successfully"
    elif [ -f "ollama" ]; then
        # Standalone binary
        print_info "Installing Ollama binary to /usr/local/bin..."
        sudo mkdir -p /usr/local/bin
        sudo mv ollama /usr/local/bin/ollama
        sudo chmod +x /usr/local/bin/ollama
        
        OLLAMA_BINARY="/usr/local/bin/ollama"
        print_status "Ollama installed successfully"
    else
        print_error "Unexpected download contents"
        ls -la
        rm -rf "$TMP_DIR"
        exit 1
    fi
    
    # Clean up
    cd ~
    rm -rf "$TMP_DIR"
    print_status "Cleanup complete"
    
    # Verify installation
    if [ -f "$OLLAMA_BINARY" ]; then
        if check_binary_arm64 "$OLLAMA_BINARY"; then
            OLLAMA_VERSION=$("$OLLAMA_BINARY" --version 2>/dev/null || echo "unknown")
            print_status "Verified: Ollama $OLLAMA_VERSION installed"
        else
            print_error "Installation verification failed"
            exit 1
        fi
    else
        print_error "Installation failed - binary not found"
        exit 1
    fi
else
    print_status "Using existing Ollama installation"
fi

# Kill any running Ollama instances
print_info "Stopping any running Ollama instances..."
pkill ollama 2>/dev/null || true
sleep 2
print_status "Ollama stopped"

echo ""
echo "=================================================="
echo "Step 2: Power Management Configuration"
echo "=================================================="
echo ""

print_info "Configuring pmset for headless operation..."

# Disable sleep entirely
sudo pmset -a sleep 0
print_status "Sleep disabled"

sudo pmset -a disablesleep 1
print_status "Sleep disable flag set"

# Disable disk sleep
sudo pmset -a disksleep 0
print_status "Disk sleep disabled"

# Disable standby
sudo pmset -a standby 0
print_status "Standby mode disabled"

# Disable autopoweroff
sudo pmset -a autopoweroff 0
print_status "Auto power off disabled"

# Disable powernap
sudo pmset -a powernap 0
print_status "Power nap disabled"

# Disable autorestart
sudo pmset -a autorestart 0
print_status "Auto restart on power failure disabled"

# Keep network alive
sudo pmset -a networkoversleep 0
print_status "Network over sleep disabled (not needed since sleep is off)"

# Wake on magic packet
sudo pmset -a womp 1
print_status "Wake on magic packet enabled"

# Display sleep (saves power, doesn't affect headless operation)
sudo pmset -a displaysleep 10
print_status "Display sleep set to 10 minutes"

# TCP keep alive
sudo pmset -a tcpkeepalive 1
print_status "TCP keep alive enabled"

echo ""
print_info "Current power settings:"
pmset -g

echo ""
read -p "Power settings look correct? (y/n): " -n 1 -r
echo
if [[ ! $REPLY =~ ^[Yy]$ ]]; then
    print_error "Please review settings and re-run script"
    exit 1
fi

echo ""
echo "=================================================="
echo "Step 3: Configure Ollama Environment"
echo "=================================================="
echo ""

# Get user input for configuration
read -p "Max loaded models [default: 3]: " MAX_MODELS
MAX_MODELS=${MAX_MODELS:-3}

read -p "Keep alive time in hours [default: 24]: " KEEP_ALIVE_HOURS
KEEP_ALIVE_HOURS=${KEEP_ALIVE_HOURS:-24}
KEEP_ALIVE_SECONDS=$((KEEP_ALIVE_HOURS * 3600))

read -p "Number of parallel requests [default: 4]: " NUM_PARALLEL
NUM_PARALLEL=${NUM_PARALLEL:-4}

read -p "Max context window [default: 32768]: " MAX_CONTEXT
MAX_CONTEXT=${MAX_CONTEXT:-32768}

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

read -p "Confirm configuration? (y/n): " -n 1 -r
echo
if [[ ! $REPLY =~ ^[Yy]$ ]]; then
    print_error "Configuration cancelled"
    exit 1
fi

echo ""
echo "=================================================="
echo "Step 4: Create Launchd Service"
echo "=================================================="
echo ""

PLIST_PATH="/Library/LaunchDaemons/com.ollama.server.plist"

print_info "Creating launchd plist at $PLIST_PATH..."

# Backup existing plist if it exists
if [ -f "$PLIST_PATH" ]; then
    print_warning "Existing plist found, backing up..."
    sudo cp "$PLIST_PATH" "$PLIST_PATH.backup.$(date +%Y%m%d_%H%M%S)"
    print_status "Backup created"
fi

# Create the plist file
sudo tee "$PLIST_PATH" > /dev/null <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.ollama.server</string>
    
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
    <string>/tmp/ollama.log</string>
    
    <key>StandardErrorPath</key>
    <string>/tmp/ollama.err</string>
    
    <key>EnvironmentVariables</key>
    <dict>
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
    </dict>
    
    <key>WorkingDirectory</key>
    <string>/tmp</string>
</dict>
</plist>
EOF

print_status "Launchd plist created"

# Set proper permissions
sudo chown root:wheel "$PLIST_PATH"
sudo chmod 644 "$PLIST_PATH"
print_status "Permissions set"

echo ""
echo "=================================================="
echo "Step 5: Load and Start Service"
echo "=================================================="
echo ""

# Unload if already loaded
sudo launchctl unload "$PLIST_PATH" 2>/dev/null || true
sleep 1

# Load the service
print_info "Loading Ollama service..."
sudo launchctl load "$PLIST_PATH"
print_status "Service loaded"

# Start the service
print_info "Starting Ollama service..."
sudo launchctl start com.ollama.server
sleep 3
print_status "Service started"

echo ""
echo "=================================================="
echo "Step 6: Verification"
echo "=================================================="
echo ""

# Check if process is running
if pgrep -x ollama > /dev/null; then
    print_status "Ollama process is running"
    
    # Get process info
    OLLAMA_PID=$(pgrep -x ollama)
    print_info "Process ID: $OLLAMA_PID"
    
    # Check if running natively
    if ps -p $OLLAMA_PID -o comm= 2>/dev/null | head -1 | xargs file 2>/dev/null | grep -q "arm64"; then
        print_status "Ollama is running natively on ARM64"
    else
        # Try alternative check
        print_info "Checking process architecture..."
    fi
else
    print_error "Ollama process not running!"
    print_info "Check logs:"
    echo "  tail -f /tmp/ollama.log"
    echo "  tail -f /tmp/ollama.err"
    exit 1
fi

# Test API endpoint
print_info "Testing API endpoint..."
sleep 2

API_TEST=$(curl -s http://localhost:11434/api/tags 2>&1)
if [ $? -eq 0 ]; then
    print_status "API endpoint responding"
else
    print_warning "API endpoint not responding yet (may still be starting)"
    print_info "Wait a few seconds and try: curl http://localhost:11434/api/tags"
fi

# Display service status
echo ""
print_info "Service status:"
LAUNCHCTL_STATUS=$(sudo launchctl list | grep ollama || echo "not found")
if [[ "$LAUNCHCTL_STATUS" != "not found" ]]; then
    echo "  $LAUNCHCTL_STATUS"
    print_status "Service registered with launchd"
else
    print_warning "Service not found in launchctl list"
fi

echo ""
echo "=================================================="
echo "Setup Summary"
echo "=================================================="
echo ""
print_status "Ollama installed and verified"
print_status "Power management configured for headless operation"
print_status "Ollama service created and started"
print_status "Service will auto-start on boot"
echo ""
print_info "Installation details:"
echo "  Binary location: $OLLAMA_BINARY"
echo "  Ollama version: $("$OLLAMA_BINARY" --version 2>/dev/null || echo 'unknown')"
echo ""
print_info "Configuration:"
echo "  Max loaded models: $MAX_MODELS"
echo "  Keep alive: $KEEP_ALIVE_HOURS hours"
echo "  Parallel requests: $NUM_PARALLEL"
echo "  Max context: $MAX_CONTEXT tokens"
echo "  Bind address: $OLLAMA_HOST"
echo ""
print_info "Logs:"
echo "  Output: /tmp/ollama.log"
echo "  Errors: /tmp/ollama.err"
echo ""
print_info "Useful commands:"
echo "  Check status: sudo launchctl list | grep ollama"
echo "  Stop service: sudo launchctl stop com.ollama.server"
echo "  Restart: sudo launchctl stop com.ollama.server && sudo launchctl start com.ollama.server"
echo "  View logs: tail -f /tmp/ollama.log"
echo "  Test API: curl http://localhost:11434/api/tags"
echo "  Pull a model: ollama pull qwen2.5-coder:32b"
echo "  List models: ollama list"
echo "  Run a model: ollama run qwen2.5-coder:7b"
echo ""
print_info "Power settings verification:"
echo "  View settings: pmset -g"
echo "  Check after reboot to ensure persistence"
echo ""
print_info "Next steps:"
echo "  1. Test reboot to verify auto-start"
echo "  2. Pull your first model: ollama pull qwen2.5-coder:7b"
echo "  3. Test inference: ollama run qwen2.5-coder:7b 'write a hello world in python'"
echo "  4. Monitor performance: watch -n 1 'ps aux | grep ollama'"
echo ""
print_info "For LangServe/RAG setup (later):"
echo "  - Install Colima: brew install colima docker docker-compose"
echo "  - Pull embedding model: ollama pull nomic-embed-text"
echo ""
print_warning "Testing reboot persistence..."
read -p "Reboot now to verify auto-start? (y/n): " -n 1 -r
echo
if [[ $REPLY =~ ^[Yy]$ ]]; then
    print_info "After reboot, verify with:"
    echo "  - ps aux | grep ollama"
    echo "  - curl http://localhost:11434/api/tags"
    echo "  - pmset -g"
    echo ""
    print_info "Rebooting in 5 seconds..."
    sleep 5
    sudo reboot
else
    print_warning "Remember to test reboot manually!"
    echo ""
    print_status "Setup complete!"
    echo ""
    print_info "Your headless Mac Mini M4 is configured for 24/7 LLM inference."
fi
