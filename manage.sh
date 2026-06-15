#!/bin/bash

# manage.sh — Component Orchestrator for Mac LLM Optimizer
# Installs, enables, disables, removes, and reports status of all components.
# Components: Homebrew, Power Management, Ollama, Colima
#
# NOTE: This is the orchestrator (renamed from setup.sh in Phase 2).
# The system baseline script (pmset, sysctl, service suppression) is setup.sh.

set -e

# Get the directory where this script is located
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Source common utilities
source "$SCRIPT_DIR/lib/common.sh"

# Component scripts
HOMEBREW_SCRIPT="$SCRIPT_DIR/scripts/homebrew_setup.sh"
POWER_SCRIPT="$SCRIPT_DIR/scripts/power_management.sh"
OLLAMA_SCRIPT="$SCRIPT_DIR/scripts/ollama_setup.sh"
COLIMA_SCRIPT="$SCRIPT_DIR/scripts/colima_setup.sh"
UPDATE_SCRIPT="$SCRIPT_DIR/update-tools.sh"

# Show main menu
show_menu() {
    clear
    print_header "Headless Mac Setup"
    
    echo "Select an option:"
    echo ""
    echo "  Installation:"
    echo "    1) Install Homebrew"
    echo "    2) Configure Power Management"
    echo "    3) Install Ollama"
    echo "    4) Install Colima + Docker"
    echo "    5) Full Setup (All of the above)"
    echo ""
    echo "  Management:"
    echo "    6) Enable All Services"
    echo "    7) Disable All Services"
    echo "    8) Show Status (All Components)"
    echo ""
    echo "  Individual Status:"
    echo "    9) Homebrew Status"
    echo "   10) Power Management Status"
    echo "   11) Ollama Status"
    echo "   12) Colima Status"
    echo ""
  echo "  Updates:"
    echo "   16) Update Ollama"
    echo ""
    echo "  Removal:"
    echo "   13) Remove Ollama"
    echo "   14) Remove Colima"
    echo "   15) Remove All Components"
    echo ""
    echo "    0) Exit"
    echo ""
}

# Full setup
full_setup() {
    print_header "Full Headless Mac Setup"
    
    print_info "This will install and configure:"
    echo "  1. Homebrew (package manager)"
    echo "  2. Power Management (24/7 operation)"
    echo "  3. Ollama (LLM inference)"
    echo "  4. Colima + Docker (containers)"
    echo ""
    
    if ! confirm_action "Proceed with full setup?"; then
        print_warning "Setup cancelled"
        return
    fi
    
    # Step 1: Homebrew
    print_separator
    print_header "Step 1/4: Homebrew"
    if command_exists brew; then
        print_status "Homebrew already installed"
    else
        "$HOMEBREW_SCRIPT" setup
    fi
    
    # Step 2: Power Management
    print_separator
    print_header "Step 2/4: Power Management"
    if confirm_action "Configure power management for 24/7 operation?"; then
        "$POWER_SCRIPT" setup
    else
        print_info "Skipping power management"
    fi
    
    # Step 3: Ollama
    print_separator
    print_header "Step 3/4: Ollama"
    if confirm_action "Install and configure Ollama?"; then
        "$OLLAMA_SCRIPT" setup
    else
        print_info "Skipping Ollama"
    fi
    
    # Step 4: Colima
    print_separator
    print_header "Step 4/4: Colima + Docker"
    if confirm_action "Install and configure Colima with Docker?"; then
        "$COLIMA_SCRIPT" setup
    else
        print_info "Skipping Colima"
    fi
    
    # Summary
    print_separator
    print_header "Setup Complete!"
    
    print_status "All components installed and configured"
    echo ""
    print_info "Next steps:"
    echo "  • Test reboot to verify auto-start"
    echo "  • Pull an Ollama model: ollama pull qwen2.5-coder:7b"
    echo "  • Test Ollama: ollama run qwen2.5-coder:7b 'hello'"
    echo "  • Verify Docker: docker run hello-world"
    echo ""
    print_info "Check status anytime with: $0 status"
}

# Enable all services
enable_all() {
    print_header "Enable All Services"
    
    print_info "This will start/enable all services"
    echo ""
    
    # Power management
    if confirm_action "Enable headless power management?"; then
        "$POWER_SCRIPT" enable
    fi
    
    print_separator
    
    # Ollama
    if [ -x "$OLLAMA_SCRIPT" ]; then
        if confirm_action "Enable Ollama service?"; then
            "$OLLAMA_SCRIPT" enable
        fi
    fi
    
    print_separator
    
    # Colima
    if command_exists colima; then
        if confirm_action "Enable Colima?"; then
            "$COLIMA_SCRIPT" enable
        fi
    fi
    
    print_separator
    print_status "Services enabled"
}

# Disable all services
disable_all() {
    print_header "Disable All Services"
    
    print_info "This will stop/disable all services"
    print_warning "Components remain installed"
    echo ""
    
    if ! confirm_action "Disable all services?"; then
        print_warning "Operation cancelled"
        return
    fi
    
    # Colima
    if command_exists colima; then
        print_info "Disabling Colima..."
        "$COLIMA_SCRIPT" disable
    fi
    
    print_separator
    
    # Ollama
    if [ -x "$OLLAMA_SCRIPT" ]; then
        print_info "Disabling Ollama..."
        "$OLLAMA_SCRIPT" disable
    fi
    
    print_separator
    
    # Power management
    print_info "Restoring normal power management..."
    "$POWER_SCRIPT" disable
    
    print_separator
    print_status "All services disabled"
}

# Show status of all components
show_all_status() {
    print_header "System Status"
    
    # System info
    print_info "System Information:"
    echo "  macOS: $(sw_vers -productVersion)"
    echo "  Architecture: $(uname -m)"
    echo "  Hostname: $(hostname)"
    echo "  RAM: $(get_system_ram_gb)GB"
    echo "  CPUs: $(get_cpu_cores)"
    echo ""
    
    print_separator
    
    # Homebrew
    print_info "Homebrew:"
    if command_exists brew; then
        echo "  Status: ✓ Installed"
        echo "  Path: $(which brew)"
        echo "  Version: $(brew --version | head -1)"
    else
        echo "  Status: ✗ Not installed"
    fi
    
    print_separator
    
    # Power Management
    print_info "Power Management:"
    local sleep_val=$(pmset -g | grep "^[ ]*sleep" | awk '{print $2}' | head -1)
    if [ "$sleep_val" = "0" ]; then
        echo "  Status: ✓ Headless mode (sleep disabled)"
    else
        echo "  Status: ⚠ Normal mode (will sleep)"
    fi
    
    print_separator
    
    # Ollama
    print_info "Ollama:"
    if command_exists ollama; then
        echo "  Status: ✓ Installed"
        echo "  Path: $(which ollama)"
        if process_running ollama; then
            echo "  Service: ✓ Running"
            if curl -s --max-time 2 http://localhost:11434/api/tags > /dev/null 2>&1; then
                echo "  API: ✓ Responding"
            else
                echo "  API: ⚠ Not responding"
            fi
        else
            echo "  Service: ✗ Not running"
        fi
    else
        echo "  Status: ✗ Not installed"
    fi
    
    print_separator
    
    # Colima
    print_info "Colima:"
    if command_exists colima; then
        echo "  Status: ✓ Installed"
        if colima status &> /dev/null; then
            echo "  Service: ✓ Running"
            if docker info &> /dev/null; then
                echo "  Docker: ✓ Connected"
            else
                echo "  Docker: ✗ Not connected"
            fi
        else
            echo "  Service: ✗ Not running"
        fi
    else
        echo "  Status: ✗ Not installed"
    fi
    
    print_separator
    
    # Docker
    print_info "Docker:"
    if command_exists docker; then
        echo "  Status: ✓ Installed"
        echo "  Version: $(docker --version)"
    else
        echo "  Status: ✗ Not installed"
    fi
    
    print_separator
    
    print_info "For detailed status, use:"
    echo "  $0 status <component>"
    echo "  Components: homebrew, power, ollama, colima"
}

# Remove all components
remove_all() {
    print_header "Remove All Components"
    
    print_warning "THIS WILL REMOVE ALL COMPONENTS"
    print_warning "This is a destructive operation!"
    echo ""
    print_info "The following will be removed:"
    echo "  • Colima and Docker (VMs, containers, images)"
    echo "  • Ollama (service and binary, optionally models)"
    echo "  • Power management configuration"
    echo "  • Optionally: Homebrew and all packages"
    echo ""
    
    if ! confirm_action "Are you absolutely sure?"; then
        print_warning "Operation cancelled"
        return
    fi
    
    # Double confirm
    print_warning "This cannot be undone!"
    if ! confirm_action "Type 'y' again to confirm"; then
        print_warning "Operation cancelled"
        return
    fi
    
    # Remove in reverse order
    
    # Colima
    if command_exists colima; then
        print_separator
        print_info "Removing Colima..."
        "$COLIMA_SCRIPT" remove
    fi
    
    # Ollama
    if command_exists ollama || [ -f "/usr/local/bin/ollama" ]; then
        print_separator
        print_info "Removing Ollama..."
        "$OLLAMA_SCRIPT" remove
    fi
    
    # Power management
    print_separator
    print_info "Restoring normal power management..."
    "$POWER_SCRIPT" remove
    
    # Homebrew (optional)
    if command_exists brew; then
        print_separator
        if confirm_action "Remove Homebrew? (This will remove ALL Homebrew packages)"; then
            "$HOMEBREW_SCRIPT" remove
        else
            print_info "Homebrew kept installed"
        fi
    fi
    
    print_separator
    print_status "All components removed"
    print_info "Your Mac has been restored to a clean state"
}

# Interactive menu mode
interactive_mode() {
    while true; do
        show_menu
        read -p "Enter choice [0-15]: " choice
        echo ""
        
        case $choice in
            1)
                "$HOMEBREW_SCRIPT" setup
                ;;
            2)
                "$POWER_SCRIPT" setup
                ;;
            3)
                "$OLLAMA_SCRIPT" setup
                ;;
            4)
                "$COLIMA_SCRIPT" setup
                ;;
            5)
                full_setup
                ;;
            6)
                enable_all
                ;;
            7)
                disable_all
                ;;
            8)
                show_all_status
                ;;
            9)
                "$HOMEBREW_SCRIPT" status
                ;;
            10)
                "$POWER_SCRIPT" status
                ;;
            11)
                "$OLLAMA_SCRIPT" status
                ;;
            12)
                "$COLIMA_SCRIPT" status
                ;;
            13)
                "$OLLAMA_SCRIPT" remove
                ;;
            14)
                "$COLIMA_SCRIPT" remove
                ;;
            15)
                remove_all
                ;;
            16)
                sudo "$UPDATE_SCRIPT" ollama
                ;;
            0)
                print_info "Exiting..."
                exit 0
                ;;
            *)
                print_error "Invalid choice: $choice"
                ;;
        esac
        
        echo ""
        read -p "Press Enter to continue..."
    done
}

# Show CLI usage
show_cli_usage() {
    cat << EOF
Mac LLM Optimizer — Component Manager

Usage: $0 <command> [component]

Commands:
    install <component>    Install/setup a component
    enable <component>     Enable/start a component
    disable <component>    Disable/stop a component
    remove <component>     Remove/uninstall a component
    update <component>     Update a tool binary in-place and restart its daemon
    status [component]     Show status
    menu                   Interactive menu mode
    help                   Show this help message

Components:
    all                    All components
    homebrew               Homebrew package manager
    power                  Power management
    ollama                 Ollama LLM service
    colima                 Colima + Docker

Examples:
    $0 install all         # Full setup
    $0 install ollama      # Install only Ollama
    $0 enable ollama       # Start Ollama service
    $0 update ollama       # Update Ollama binary and restart daemon
    $0 status              # Show status of all components
    $0 status ollama       # Show Ollama status only
    $0 disable all         # Disable all services
    $0 remove colima       # Remove Colima
    $0 menu                # Interactive menu

EOF
}

# Main command dispatcher
main() {
    local command="${1:-menu}"
    local component="${2:-}"
    
    # Check macOS
    if ! check_macos; then
        exit 1
    fi
    
    case "$command" in
        install)
            case "$component" in
                all)
                    full_setup
                    ;;
                homebrew)
                    "$HOMEBREW_SCRIPT" setup
                    ;;
                power)
                    "$POWER_SCRIPT" setup
                    ;;
                ollama)
                    "$OLLAMA_SCRIPT" setup
                    ;;
                colima)
                    "$COLIMA_SCRIPT" setup
                    ;;
                "")
                    print_error "No component specified"
                    echo ""
                    show_cli_usage
                    exit 1
                    ;;
                *)
                    print_error "Unknown component: $component"
                    exit 1
                    ;;
            esac
            ;;
            
        enable)
            case "$component" in
                all)
                    enable_all
                    ;;
                homebrew)
                    "$HOMEBREW_SCRIPT" enable
                    ;;
                power)
                    "$POWER_SCRIPT" enable
                    ;;
                ollama)
                    "$OLLAMA_SCRIPT" enable
                    ;;
                colima)
                    "$COLIMA_SCRIPT" enable
                    ;;
                "")
                    print_error "No component specified"
                    exit 1
                    ;;
                *)
                    print_error "Unknown component: $component"
                    exit 1
                    ;;
            esac
            ;;
            
        disable)
            case "$component" in
                all)
                    disable_all
                    ;;
                homebrew)
                    "$HOMEBREW_SCRIPT" disable
                    ;;
                power)
                    "$POWER_SCRIPT" disable
                    ;;
                ollama)
                    "$OLLAMA_SCRIPT" disable
                    ;;
                colima)
                    "$COLIMA_SCRIPT" disable
                    ;;
                "")
                    print_error "No component specified"
                    exit 1
                    ;;
                *)
                    print_error "Unknown component: $component"
                    exit 1
                    ;;
            esac
            ;;
            
        remove)
            case "$component" in
                all)
                    remove_all
                    ;;
                homebrew)
                    "$HOMEBREW_SCRIPT" remove
                    ;;
                power)
                    "$POWER_SCRIPT" remove
                    ;;
                ollama)
                    "$OLLAMA_SCRIPT" remove
                    ;;
                colima)
                    "$COLIMA_SCRIPT" remove
                    ;;
                "")
                    print_error "No component specified"
                    exit 1
                    ;;
                *)
                    print_error "Unknown component: $component"
                    exit 1
                    ;;
            esac
            ;;
            
        update)
            case "$component" in
                ollama)
                    sudo "$UPDATE_SCRIPT" ollama
                    ;;
                all)
                    sudo "$UPDATE_SCRIPT" all
                    ;;
                "")
                    print_error "No component specified"
                    echo ""
                    show_cli_usage
                    exit 1
                    ;;
                *)
                    print_error "Unknown component: $component"
                    exit 1
                    ;;
            esac
            ;;

        status)
            if [ -z "$component" ]; then
                show_all_status
            else
                case "$component" in
                    homebrew)
                        "$HOMEBREW_SCRIPT" status
                        ;;
                    power)
                        "$POWER_SCRIPT" status
                        ;;
                    ollama)
                        "$OLLAMA_SCRIPT" status
                        ;;
                    colima)
                        "$COLIMA_SCRIPT" status
                        ;;
                    *)
                        print_error "Unknown component: $component"
                        exit 1
                        ;;
                esac
            fi
            ;;
            
        menu)
            interactive_mode
            ;;
            
        help|--help|-h)
            show_cli_usage
            ;;
            
        *)
            print_error "Unknown command: $command"
            echo ""
            show_cli_usage
            exit 1
            ;;
    esac
}

# Run main function
main "$@"
