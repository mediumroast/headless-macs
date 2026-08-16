#!/bin/bash

# Colima + Docker Setup Script for Headless Mac
# Installs Homebrew (if needed), Colima, Docker CLI, and Docker Compose
# Configures Colima for headless operation with auto-start
#
# ⚠️  DEPRECATED: This script has been refactored into modular components
# ⚠️  Please use the new modular scripts instead:
# ⚠️    ./setup.sh install colima       # Colima setup
# ⚠️    ./scripts/colima_setup.sh       # Direct Colima script
#
# The new version is Ollama-aware and optimizes resources!
# This script will continue to work but is no longer maintained.

set -e  # Exit on error

echo "=================================================="
echo "⚠️  DEPRECATED SCRIPT"
echo "=================================================="
echo ""
echo "This script has been replaced by modular components."
echo ""
echo "New usage (with Ollama awareness):"
echo "  ./setup.sh install colima       # Full Colima setup"
echo "  ./scripts/colima_setup.sh       # Direct script"
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
echo "Colima + Docker Setup Script"
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

# Check if running on macOS
if [[ "$OSTYPE" != "darwin"* ]]; then
    print_error "This script must be run on macOS"
    exit 1
fi

# Check if running on Apple Silicon
if [[ $(uname -m) != "arm64" ]]; then
    print_warning "This script is optimized for Apple Silicon"
    print_info "Detected architecture: $(uname -m)"
fi

echo ""
print_info "This script will:"
echo "  1. Install Homebrew (if not present)"
echo "  2. Install Colima"
echo "  3. Install Docker CLI and Docker Compose"
echo "  4. Configure Colima for headless operation"
echo "  5. Set up auto-start on boot (optional)"
echo ""

read -p "Continue? (y/n): " -n 1 -r
echo
if [[ ! $REPLY =~ ^[Yy]$ ]]; then
    print_warning "Setup cancelled"
    exit 0
fi

echo ""
echo "=================================================="
echo "Step 1: Check/Install Homebrew"
echo "=================================================="
echo ""

# Check if Homebrew is installed
if command -v brew &> /dev/null; then
    BREW_PATH=$(which brew)
    print_status "Homebrew already installed at $BREW_PATH"
    
    # Check if it's the right Homebrew for the architecture
    if [[ $(uname -m) == "arm64" ]] && [[ "$BREW_PATH" == "/usr/local/bin/brew" ]]; then
        print_warning "You have x86_64 Homebrew on Apple Silicon"
        print_info "ARM64 Homebrew should be at /opt/homebrew/bin/brew"
        
        read -p "Install ARM64 Homebrew alongside? (y/n): " -n 1 -r
        echo
        if [[ $REPLY =~ ^[Yy]$ ]]; then
            print_info "Installing ARM64 Homebrew..."
            /bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"
            
            # Add to PATH
            echo 'eval "$(/opt/homebrew/bin/brew shellenv)"' >> ~/.zprofile
            eval "$(/opt/homebrew/bin/brew shellenv)"
            
            print_status "ARM64 Homebrew installed"
        fi
    fi
    
    # Update Homebrew
    print_info "Updating Homebrew..."
    brew update
    print_status "Homebrew updated"
    
else
    print_info "Homebrew not found. Installing..."
    
    # Install Homebrew
    /bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"
    
    # Add Homebrew to PATH based on architecture
    if [[ $(uname -m) == "arm64" ]]; then
        echo 'eval "$(/opt/homebrew/bin/brew shellenv)"' >> ~/.zprofile
        eval "$(/opt/homebrew/bin/brew shellenv)"
    else
        echo 'eval "$(/usr/local/bin/brew shellenv)"' >> ~/.zprofile
        eval "$(/usr/local/bin/brew shellenv)"
    fi
    
    print_status "Homebrew installed successfully"
fi

echo ""
echo "=================================================="
echo "Step 2: Install Colima"
echo "=================================================="
echo ""

if command -v colima &> /dev/null; then
    print_status "Colima already installed"
    COLIMA_VERSION=$(colima version | head -1)
    print_info "Version: $COLIMA_VERSION"
    
    read -p "Upgrade to latest version? (y/n): " -n 1 -r
    echo
    if [[ $REPLY =~ ^[Yy]$ ]]; then
        brew upgrade colima
        print_status "Colima upgraded"
    fi
else
    print_info "Installing Colima..."
    brew install colima
    print_status "Colima installed"
fi

echo ""
echo "=================================================="
echo "Step 3: Install Docker CLI and Docker Compose"
echo "=================================================="
echo ""

# Install Docker CLI
if command -v docker &> /dev/null; then
    print_status "Docker CLI already installed"
    docker --version
else
    print_info "Installing Docker CLI..."
    brew install docker
    print_status "Docker CLI installed"
fi

# Install Docker Compose
if command -v docker-compose &> /dev/null; then
    print_status "Docker Compose already installed"
    docker-compose --version
else
    print_info "Installing Docker Compose..."
    brew install docker-compose
    print_status "Docker Compose installed"
fi

# Also install docker-buildx for multi-platform builds (useful for ARM64)
if docker buildx version &> /dev/null; then
    print_status "Docker Buildx already available"
else
    print_info "Installing Docker Buildx..."
    brew install docker-buildx
    print_status "Docker Buildx installed"
fi

echo ""
echo "=================================================="
echo "Step 4: Configure Colima"
echo "=================================================="
echo ""

# Check if Colima is already running
if colima status &> /dev/null; then
    print_warning "Colima is already running"
    colima status
    
    read -p "Stop and reconfigure? (y/n): " -n 1 -r
    echo
    if [[ $REPLY =~ ^[Yy]$ ]]; then
        print_info "Stopping Colima..."
        colima stop
        print_status "Colima stopped"
    else
        print_info "Keeping current configuration"
        SKIP_CONFIG=true
    fi
fi

if [[ "$SKIP_CONFIG" != "true" ]]; then
    echo ""
    print_info "Colima configuration:"
    echo ""
    
    # Get configuration from user
    read -p "CPU cores for Colima [default: 4]: " COLIMA_CPU
    COLIMA_CPU=${COLIMA_CPU:-4}
    
    read -p "Memory in GB [default: 16]: " COLIMA_MEMORY
    COLIMA_MEMORY=${COLIMA_MEMORY:-16}
    
    read -p "Disk size in GB [default: 100]: " COLIMA_DISK
    COLIMA_DISK=${COLIMA_DISK:-100}
    
    echo ""
    print_info "Configuration summary:"
    echo "  CPUs: $COLIMA_CPU"
    echo "  Memory: ${COLIMA_MEMORY}GB"
    echo "  Disk: ${COLIMA_DISK}GB"
    echo "  Architecture: $(uname -m)"
    echo ""
    
    read -p "Start Colima with these settings? (y/n): " -n 1 -r
    echo
    if [[ ! $REPLY =~ ^[Yy]$ ]]; then
        print_error "Configuration cancelled"
        exit 1
    fi
    
    print_info "Starting Colima..."
    
    # Start Colima with configuration
    if [[ $(uname -m) == "arm64" ]]; then
        colima start \
            --arch aarch64 \
            --cpu $COLIMA_CPU \
            --memory $COLIMA_MEMORY \
            --disk $COLIMA_DISK \
            --vm-type vz \
            --vz-rosetta \
            --mount-type virtiofs \
            --network-address
    else
        colima start \
            --cpu $COLIMA_CPU \
            --memory $COLIMA_MEMORY \
            --disk $COLIMA_DISK
    fi
    
    print_status "Colima started successfully"
fi

echo ""
echo "=================================================="
echo "Step 5: Verify Installation"
echo "=================================================="
echo ""

# Verify Colima is running
print_info "Checking Colima status..."
if colima status &> /dev/null; then
    print_status "Colima is running"
    colima status
else
    print_error "Colima is not running"
    exit 1
fi

# Verify Docker CLI can connect
print_info "Testing Docker CLI..."
if docker info &> /dev/null; then
    print_status "Docker CLI connected to Colima"
else
    print_error "Docker CLI cannot connect to Colima"
    print_info "You may need to set DOCKER_HOST"
    exit 1
fi

# Test with hello-world
print_info "Running test container..."
if docker run --rm hello-world &> /dev/null; then
    print_status "Docker containers work correctly"
else
    print_warning "Test container failed"
fi

# Show Docker info
echo ""
print_info "Docker environment:"
docker info | grep -E "Server Version|Operating System|Architecture|CPUs|Total Memory"

echo ""
echo "=================================================="
echo "Step 6: Auto-start Configuration (Optional)"
echo "=================================================="
echo ""

print_info "Colima can start automatically on boot using launchd"
read -p "Set up auto-start? (y/n): " -n 1 -r
echo

if [[ $REPLY =~ ^[Yy]$ ]]; then
    PLIST_PATH="$HOME/Library/LaunchAgents/com.colima.plist"
    
    print_info "Creating launchd plist..."
    
    mkdir -p "$HOME/Library/LaunchAgents"
    
    cat > "$PLIST_PATH" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.colima</string>
    
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
    
    print_status "Launchd plist created"
    
    # Load the plist
    launchctl load "$PLIST_PATH"
    print_status "Auto-start configured"
    
    print_info "Colima will now start automatically on login"
else
    print_info "Skipping auto-start configuration"
    print_warning "You'll need to run 'colima start' manually after reboots"
fi

echo ""
echo "=================================================="
echo "Setup Complete!"
echo "=================================================="
echo ""

print_status "Colima and Docker are ready to use"
echo ""
print_info "Installation summary:"
echo "  Homebrew: $(which brew)"
echo "  Colima: $(which colima) - $(colima version | head -1)"
echo "  Docker: $(which docker) - $(docker --version)"
echo "  Docker Compose: $(which docker-compose) - $(docker-compose --version)"
echo ""
print_info "Useful commands:"
echo "  colima status              - Check Colima status"
echo "  colima stop                - Stop Colima"
echo "  colima start               - Start Colima"
echo "  colima delete              - Delete Colima VM"
echo "  docker ps                  - List running containers"
echo "  docker-compose up -d       - Start a compose stack"
echo "  docker info                - Show Docker info"
echo ""
print_info "Next steps for LangServe/RAG:"
echo "  1. Create your docker-compose.yml"
echo "  2. Pull Ollama embedding model: ollama pull nomic-embed-text"
echo "  3. Start your stack: docker-compose up -d"
echo ""
print_info "Colima VM details:"
colima status

echo ""
print_status "Setup complete! Docker/Colima ready for use."
