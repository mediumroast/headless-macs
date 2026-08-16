#!/bin/bash

# Homebrew Setup Script
# Installs and configures Homebrew for macOS (Apple Silicon or Intel)
# Supports: setup, enable, disable, remove, status

set -e

# Get the directory where this script is located
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# Source common utilities
source "$SCRIPT_DIR/../lib/common.sh"

# Homebrew paths
ARM64_BREW_PATH="/opt/homebrew/bin/brew"
INTEL_BREW_PATH="/usr/local/bin/brew"
SHELL_CONFIG="$HOME/.zprofile"

# Get expected brew path for current architecture
get_expected_brew_path() {
    if check_apple_silicon; then
        echo "$ARM64_BREW_PATH"
    else
        echo "$INTEL_BREW_PATH"
    fi
}

# Get expected brew prefix for current architecture
get_expected_brew_prefix() {
    if check_apple_silicon; then
        echo "/opt/homebrew"
    else
        echo "/usr/local"
    fi
}

# Check if Homebrew is installed
is_brew_installed() {
    command_exists brew
}

# Check if correct architecture Homebrew is installed
is_correct_brew_installed() {
    local expected_path=$(get_expected_brew_path)
    
    if [ -f "$expected_path" ]; then
        return 0
    fi
    return 1
}

# Install Homebrew
install_homebrew() {
    print_info "Installing Homebrew..."
    
    # Download and run the official installer
    /bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"
    
    # Add Homebrew to PATH
    local brew_prefix=$(get_expected_brew_prefix)
    local shellenv_line="eval \"\$($brew_prefix/bin/brew shellenv)\""
    
    if ! grep -q "brew shellenv" "$SHELL_CONFIG" 2>/dev/null; then
        echo "" >> "$SHELL_CONFIG"
        echo "# Homebrew" >> "$SHELL_CONFIG"
        echo "$shellenv_line" >> "$SHELL_CONFIG"
        print_status "Added Homebrew to $SHELL_CONFIG"
    fi
    
    # Evaluate for current session
    eval "$($brew_prefix/bin/brew shellenv)"
    
    print_status "Homebrew installed successfully"
}

# Update Homebrew
update_homebrew() {
    if ! is_brew_installed; then
        print_error "Homebrew is not installed"
        return 1
    fi
    
    print_info "Updating Homebrew..."
    brew update
    print_status "Homebrew updated"
}

# Show Homebrew status
show_status() {
    print_header "Homebrew Status"
    
    if is_brew_installed; then
        local brew_path=$(which brew)
        local brew_version=$(brew --version | head -1)
        local brew_prefix=$(brew --prefix)
        
        print_status "Homebrew is installed"
        print_info "Path: $brew_path"
        print_info "Prefix: $brew_prefix"
        print_info "Version: $brew_version"
        
        print_separator
        
        # Check architecture
        if check_apple_silicon; then
            if [[ "$brew_path" == "$ARM64_BREW_PATH" ]]; then
                print_status "Correct architecture: ARM64 Homebrew on Apple Silicon"
            elif [[ "$brew_path" == "$INTEL_BREW_PATH" ]]; then
                print_warning "Architecture mismatch: Intel Homebrew on Apple Silicon"
                print_info "ARM64 Homebrew should be at $ARM64_BREW_PATH"
                print_info "You may want to install ARM64 version for better performance"
            fi
        else
            if [[ "$brew_path" == "$INTEL_BREW_PATH" ]]; then
                print_status "Correct architecture: Intel Homebrew on Intel Mac"
            fi
        fi
        
        print_separator
        
        # Check if in PATH
        if grep -q "brew shellenv" "$SHELL_CONFIG" 2>/dev/null; then
            print_status "Homebrew is configured in $SHELL_CONFIG"
        else
            print_warning "Homebrew may not be in your shell PATH"
            print_info "Run 'enable' to add it to $SHELL_CONFIG"
        fi
        
    else
        print_warning "Homebrew is not installed"
        print_info "Run '$0 setup' to install Homebrew"
        
        # Check if binary exists but not in PATH
        local expected_path=$(get_expected_brew_path)
        if [ -f "$expected_path" ]; then
            print_info "Found Homebrew at $expected_path (not in PATH)"
            print_info "Run '$0 enable' to add to PATH"
        fi
    fi
}

# Setup (install/configure)
setup_homebrew() {
    print_header "Homebrew Setup"
    
    # Check macOS
    if ! check_macos; then
        exit 1
    fi
    
    # Check architecture
    check_apple_silicon_warn
    
    print_info "This will install Homebrew package manager"
    echo ""
    
    if ! confirm_action "Install Homebrew?"; then
        print_warning "Setup cancelled"
        exit 0
    fi
    
    # Check if already installed
    if is_brew_installed; then
        local brew_path=$(which brew)
        print_status "Homebrew already installed at $brew_path"
        
        # Check if correct architecture
        local expected_path=$(get_expected_brew_path)
        if [[ "$brew_path" != "$expected_path" ]]; then
            print_warning "Found Homebrew at $brew_path, but expected $expected_path"
            
            if check_apple_silicon && [[ "$brew_path" == "$INTEL_BREW_PATH" ]]; then
                print_warning "You have Intel Homebrew on Apple Silicon"
                
                if confirm_action "Install ARM64 Homebrew alongside?"; then
                    install_homebrew
                fi
            fi
        fi
        
        # Update existing installation
        if confirm_action "Update Homebrew?"; then
            update_homebrew
        fi
    else
        # Check if binary exists but not in PATH
        local expected_path=$(get_expected_brew_path)
        if [ -f "$expected_path" ]; then
            print_info "Found Homebrew at $expected_path (not in PATH)"
            print_info "Adding to PATH..."
            
            local brew_prefix=$(get_expected_brew_prefix)
            eval "$($brew_prefix/bin/brew shellenv)"
            
            # Add to shell config
            local shellenv_line="eval \"\$($brew_prefix/bin/brew shellenv)\""
            if ! grep -q "brew shellenv" "$SHELL_CONFIG" 2>/dev/null; then
                echo "" >> "$SHELL_CONFIG"
                echo "# Homebrew" >> "$SHELL_CONFIG"
                echo "$shellenv_line" >> "$SHELL_CONFIG"
                print_status "Added Homebrew to $SHELL_CONFIG"
            fi
            
            print_status "Homebrew enabled"
        else
            # Fresh installation
            install_homebrew
        fi
    fi
    
    print_separator
    print_status "Homebrew setup complete"
    
    if is_brew_installed; then
        brew --version
    fi
}

# Enable (add to PATH)
enable_homebrew() {
    print_header "Enable Homebrew"
    
    if ! check_macos; then
        exit 1
    fi
    
    local expected_path=$(get_expected_brew_path)
    
    if [ ! -f "$expected_path" ]; then
        print_error "Homebrew is not installed at $expected_path"
        print_info "Run '$0 setup' to install Homebrew"
        exit 1
    fi
    
    local brew_prefix=$(get_expected_brew_prefix)
    local shellenv_line="eval \"\$($brew_prefix/bin/brew shellenv)\""
    
    # Check if already in PATH
    if grep -q "brew shellenv" "$SHELL_CONFIG" 2>/dev/null; then
        print_status "Homebrew is already enabled in $SHELL_CONFIG"
    else
        print_info "Adding Homebrew to $SHELL_CONFIG..."
        echo "" >> "$SHELL_CONFIG"
        echo "# Homebrew" >> "$SHELL_CONFIG"
        echo "$shellenv_line" >> "$SHELL_CONFIG"
        print_status "Homebrew added to $SHELL_CONFIG"
    fi
    
    # Evaluate for current session
    eval "$($brew_prefix/bin/brew shellenv)"
    
    print_status "Homebrew enabled"
    print_info "Restart your shell or run: source $SHELL_CONFIG"
}

# Disable (remove from PATH)
disable_homebrew() {
    print_header "Disable Homebrew"
    
    if ! check_macos; then
        exit 1
    fi
    
    print_info "This will remove Homebrew from your shell PATH"
    print_warning "Homebrew will remain installed but not accessible"
    echo ""
    
    if ! confirm_action "Disable Homebrew?"; then
        print_warning "Operation cancelled"
        exit 0
    fi
    
    if [ -f "$SHELL_CONFIG" ]; then
        # Backup
        backup_file "$SHELL_CONFIG"
        
        # Comment out brew shellenv lines
        sed -i.bak '/brew shellenv/s/^/# /' "$SHELL_CONFIG"
        rm -f "$SHELL_CONFIG.bak"
        
        print_status "Homebrew disabled in $SHELL_CONFIG"
        print_info "Restart your shell for changes to take effect"
    else
        print_warning "$SHELL_CONFIG not found"
    fi
}

# Remove (uninstall)
remove_homebrew() {
    print_header "Remove Homebrew"
    
    if ! check_macos; then
        exit 1
    fi
    
    print_info "This will completely uninstall Homebrew:"
    echo "  • Remove all Homebrew files"
    echo "  • Remove from shell PATH"
    echo "  • Uninstall all Homebrew packages"
    echo ""
    print_warning "This is a destructive operation!"
    echo ""
    
    if ! confirm_action "Completely remove Homebrew?"; then
        print_warning "Operation cancelled"
        exit 0
    fi
    
    # Double confirm
    if ! confirm_action "Are you absolutely sure? This will remove ALL Homebrew packages"; then
        print_warning "Operation cancelled"
        exit 0
    fi
    
    # Disable first (remove from PATH)
    if [ -f "$SHELL_CONFIG" ]; then
        backup_file "$SHELL_CONFIG"
        sed -i.bak '/brew shellenv/d' "$SHELL_CONFIG"
        sed -i.bak '/# Homebrew/d' "$SHELL_CONFIG"
        rm -f "$SHELL_CONFIG.bak"
        print_status "Removed from $SHELL_CONFIG"
    fi
    
    # Run official uninstaller
    if is_brew_installed; then
        print_info "Running Homebrew uninstaller..."
        /bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/uninstall.sh)"
        print_status "Homebrew uninstalled"
    else
        print_warning "Homebrew command not found, attempting manual cleanup..."
        
        # Manual cleanup
        local brew_prefix=$(get_expected_brew_prefix)
        if [ -d "$brew_prefix" ]; then
            if confirm_action "Remove $brew_prefix directory?"; then
                sudo rm -rf "$brew_prefix"
                print_status "Removed $brew_prefix"
            fi
        fi
    fi
    
    print_separator
    print_status "Homebrew removed"
    print_info "Restart your shell for changes to take effect"
}

# Main command dispatcher
main() {
    local command="${1:-}"
    
    case "$command" in
        setup)
            setup_homebrew
            ;;
        enable)
            enable_homebrew
            ;;
        disable)
            disable_homebrew
            ;;
        remove)
            remove_homebrew
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
