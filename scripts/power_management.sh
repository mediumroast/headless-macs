#!/bin/bash

# Power Management Configuration Script
# Configures macOS power settings for headless 24/7 operation
# Supports: setup, enable, disable, remove, status

set -e

# Get the directory where this script is located
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# Source common utilities
source "$SCRIPT_DIR/../lib/common.sh"

# Default power settings for headless operation
HEADLESS_SLEEP=0
HEADLESS_DISABLESLEEP=1
HEADLESS_DISKSLEEP=0
HEADLESS_STANDBY=0
HEADLESS_AUTOPOWEROFF=0
HEADLESS_POWERNAP=0
HEADLESS_AUTORESTART=0
HEADLESS_NETWORKOVERSLEEP=0
HEADLESS_WOMP=1
HEADLESS_DISPLAYSLEEP=10
HEADLESS_TCPKEEPALIVE=1

# Store original settings (we'll capture these on first run)
SETTINGS_FILE="$HOME/.headless-mac-pmset-backup.txt"

# Function to get current pmset value
get_pmset_value() {
    local setting=$1
    pmset -g | awk -v s="$setting" '$1==s{print $2}' | head -1
}

# Apply a pmset setting only if the current value differs (idempotent)
pmset_apply() {
    local key=$1 value=$2
    local current
    current=$(get_pmset_value "$key")
    if [ "$current" = "$value" ]; then
        print_info "pmset $key already $value — skipping"
    else
        sudo pmset -a "$key" "$value"
        print_status "pmset $key set to $value (was: ${current:-unset})"
    fi
}

# Function to store current settings as backup
backup_current_settings() {
    if [ -f "$SETTINGS_FILE" ]; then
        print_info "Existing backup found at $SETTINGS_FILE"
        return 0
    fi
    
    print_info "Backing up current power management settings..."
    
    cat > "$SETTINGS_FILE" << EOF
# Power Management Settings Backup
# Created: $(date)
# Restore with: source this file and run disable_power_management

sleep=$(get_pmset_value "sleep")
disablesleep=$(get_pmset_value "disablesleep")
disksleep=$(get_pmset_value "disksleep")
standby=$(get_pmset_value "standby")
autopoweroff=$(get_pmset_value "autopoweroff")
powernap=$(get_pmset_value "powernap")
autorestart=$(get_pmset_value "autorestart")
networkoversleep=$(get_pmset_value "networkoversleep")
womp=$(get_pmset_value "womp")
displaysleep=$(get_pmset_value "displaysleep")
tcpkeepalive=$(get_pmset_value "tcpkeepalive")
EOF
    
    print_status "Settings backed up to $SETTINGS_FILE"
}

# Apply headless power management settings
apply_headless_settings() {
    print_info "Applying headless power management settings..."

    pmset_apply sleep            $HEADLESS_SLEEP
    pmset_apply disablesleep     $HEADLESS_DISABLESLEEP
    pmset_apply disksleep        $HEADLESS_DISKSLEEP
    pmset_apply standby          $HEADLESS_STANDBY
    pmset_apply autopoweroff     $HEADLESS_AUTOPOWEROFF
    pmset_apply powernap         $HEADLESS_POWERNAP
    pmset_apply autorestart      $HEADLESS_AUTORESTART
    pmset_apply networkoversleep $HEADLESS_NETWORKOVERSLEEP
    pmset_apply womp             $HEADLESS_WOMP
    pmset_apply displaysleep     $HEADLESS_DISPLAYSLEEP
    pmset_apply tcpkeepalive     $HEADLESS_TCPKEEPALIVE
}

# Restore default/conservative power settings
apply_default_settings() {
    print_info "Restoring default power management settings..."
    
    # Check if we have a backup
    if [ -f "$SETTINGS_FILE" ]; then
        print_info "Found backup settings, restoring original values..."
        source "$SETTINGS_FILE"

        pmset_apply sleep            ${sleep:-10}
        pmset_apply disablesleep     0
        pmset_apply disksleep        ${disksleep:-10}
        pmset_apply standby          ${standby:-1}
        pmset_apply autopoweroff     ${autopoweroff:-1}
        pmset_apply powernap         ${powernap:-0}
        pmset_apply autorestart      ${autorestart:-0}
        pmset_apply networkoversleep ${networkoversleep:-0}
        pmset_apply womp             ${womp:-0}
        pmset_apply displaysleep     ${displaysleep:-10}
        pmset_apply tcpkeepalive     ${tcpkeepalive:-1}

        print_status "Original settings restored"
    else
        print_warning "No backup found, applying conservative defaults..."

        pmset_apply sleep            10
        pmset_apply disablesleep     0
        pmset_apply disksleep        10
        pmset_apply standby          1
        pmset_apply autopoweroff     1
        pmset_apply powernap         0
        pmset_apply autorestart      0
        pmset_apply networkoversleep 0
        pmset_apply womp             0
        pmset_apply displaysleep     10
        pmset_apply tcpkeepalive     1

        print_status "Default settings applied"
    fi
}

# Show current power management settings
show_status() {
    print_header "Power Management Status"
    
    print_info "Current power management settings:"
    pmset -g
    
    print_separator
    
    # Check if in headless mode
    local sleep_val=$(get_pmset_value "sleep")
    local disablesleep_val=$(get_pmset_value "disablesleep")
    local womp_val=$(get_pmset_value "womp")
    
    if [ "$sleep_val" = "0" ] && [ "$disablesleep_val" = "1" ] && [ "$womp_val" = "1" ]; then
        print_status "Power management appears to be in HEADLESS mode"
        print_info "System will not sleep and supports Wake-on-LAN"
    else
        print_warning "Power management appears to be in NORMAL mode"
        print_info "System may sleep when idle"
    fi
    
    print_separator
    
    # Check for backup
    if [ -f "$SETTINGS_FILE" ]; then
        print_info "Settings backup exists: $SETTINGS_FILE"
        print_info "Original settings can be restored with: $0 disable"
    else
        print_warning "No settings backup found"
        print_info "Run 'setup' to create a backup before enabling headless mode"
    fi
}

# Setup (install/configure)
setup_power_management() {
    print_header "Power Management Setup - Headless Mode"
    
    # Check macOS
    if ! check_macos; then
        exit 1
    fi
    
    print_info "This will configure your Mac for 24/7 headless operation:"
    echo "  • Disable all sleep modes"
    echo "  • Keep network alive"
    echo "  • Enable Wake-on-LAN"
    echo "  • Allow display sleep (saves power)"
    echo ""
    
    if ! confirm_action "Configure power management for headless operation?"; then
        print_warning "Setup cancelled"
        exit 0
    fi
    
    # Backup current settings
    backup_current_settings
    
    # Request sudo
    request_sudo
    
    # Apply settings
    apply_headless_settings
    
    print_separator
    print_info "Current settings after configuration:"
    pmset -g
    
    print_separator
    
    if confirm_action "Do these settings look correct?"; then
        print_status "Power management configured for headless operation"
        print_info "Your Mac will not sleep and is ready for 24/7 operation"
        print_warning "Test after reboot to ensure settings persist"
    else
        print_error "Settings not confirmed"
        print_info "You can restore original settings with: $0 disable"
        exit 1
    fi
}

# Enable (apply headless settings)
enable_power_management() {
    print_header "Enable Headless Power Management"
    
    if ! check_macos; then
        exit 1
    fi
    
    # Backup if not already done
    backup_current_settings
    
    # Request sudo
    request_sudo
    
    # Apply headless settings
    apply_headless_settings
    
    print_separator
    print_status "Headless power management enabled"
    print_info "System will not sleep"
}

# Disable (restore default settings)
disable_power_management() {
    print_header "Disable Headless Power Management"
    
    if ! check_macos; then
        exit 1
    fi
    
    print_info "This will restore normal power management settings"
    print_warning "Your Mac will sleep when idle"
    echo ""
    
    if ! confirm_action "Restore normal power settings?"; then
        print_warning "Operation cancelled"
        exit 0
    fi
    
    # Request sudo
    request_sudo
    
    # Restore settings
    apply_default_settings
    
    print_separator
    print_info "Current settings after restore:"
    pmset -g
    
    print_separator
    print_status "Normal power management restored"
    print_info "Your Mac will now sleep when idle"
}

# Remove (same as disable, plus cleanup backup)
remove_power_management() {
    print_header "Remove Headless Power Management"
    
    if ! check_macos; then
        exit 1
    fi
    
    print_info "This will:"
    echo "  • Restore normal power management settings"
    echo "  • Remove backup file"
    echo ""
    
    if ! confirm_action "Remove headless power configuration?"; then
        print_warning "Operation cancelled"
        exit 0
    fi
    
    # Request sudo
    request_sudo
    
    # Restore settings
    apply_default_settings
    
    # Remove backup
    if [ -f "$SETTINGS_FILE" ]; then
        rm -f "$SETTINGS_FILE"
        print_status "Backup file removed"
    fi
    
    print_separator
    print_status "Headless power management removed"
    print_info "Your Mac is back to normal operation"
}

# Main command dispatcher
main() {
    local command="${1:-}"
    
    case "$command" in
        setup)
            setup_power_management
            ;;
        enable)
            enable_power_management
            ;;
        disable)
            disable_power_management
            ;;
        remove)
            remove_power_management
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
