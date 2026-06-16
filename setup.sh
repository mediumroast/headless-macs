#!/usr/bin/env bash
# setup.sh — System Baseline for Mac LLM Optimizer
#
# Configures the OS for sustained LLM inference: power management, network
# stack tuning, service suppression, SSH hardening, and Xcode CLT install.
#
# Run order: precheck.sh → [storage-volume.sh] → setup.sh → install-tools.sh
#
# Requires: sudo (prompted once at start)
# Idempotent: safe to run multiple times — skips already-applied settings
#
# Flags:
#   --power-only   Run Section 1 (pmset + caffeinate) only and exit.
#                  Used by the com.llm-server.pmset-heal daily launchd timer.

set -euo pipefail

POWER_ONLY=false
for arg in "$@"; do
  [[ "$arg" == "--power-only" ]] && POWER_ONLY=true
done

# ---------------------------------------------------------------------------
# Guard: Apple Silicon only
# ---------------------------------------------------------------------------
ARCH=$(uname -m)
if [[ "$ARCH" != "arm64" ]]; then
  echo "ERROR: This toolset requires Apple Silicon (arm64). Detected: $ARCH"
  exit 1
fi

if [[ "$(uname -s)" != "Darwin" ]]; then
  echo "ERROR: macOS required."
  exit 1
fi

# ---------------------------------------------------------------------------
# Detect macOS version and hardware
# ---------------------------------------------------------------------------
OS_VERSION=$(sw_vers -productVersion)
OS_MAJOR=$(echo "$OS_VERSION" | cut -d. -f1)
HW_MODEL=$(sysctl -n hw.model 2>/dev/null || echo "unknown")
RAM_GB=$(( $(sysctl -n hw.memsize) / 1024 / 1024 / 1024 ))

# ---------------------------------------------------------------------------
# Logging — tee all output to a timestamped log file
# ---------------------------------------------------------------------------
LOG_DIR="/var/log/mac-llm-setup"
sudo mkdir -p "$LOG_DIR"
LOG_FILE="$LOG_DIR/setup-$(date +%Y%m%d-%H%M%S).log"
exec > >(tee -a "$LOG_FILE") 2>&1
echo "=== setup.sh started at $(date) ==="
echo "Hardware: $HW_MODEL | RAM: ${RAM_GB}GB | macOS: $OS_VERSION"
echo ""

# ---------------------------------------------------------------------------
# Config loading
# ---------------------------------------------------------------------------
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CONFIG_FILE="$SCRIPT_DIR/config.json"

if [[ ! -f "$CONFIG_FILE" ]]; then
  echo "[WARN] config.json not found at default location: $CONFIG_FILE"
  read -rp "       Path to config.json: " CONFIG_FILE
  [[ -f "$CONFIG_FILE" ]] || { echo "ERROR: Not found: $CONFIG_FILE"; exit 1; }
fi

if ! command -v jq &>/dev/null; then
  echo "ERROR: jq is required. Install with: brew install jq"
  exit 1
fi

CONFIG=$(cat "$CONFIG_FILE")
echo "[CONFIG] Loaded $CONFIG_FILE"

# ---------------------------------------------------------------------------
# SIP detection
# ---------------------------------------------------------------------------
SIP_RAW=$(csrutil status 2>/dev/null || echo "unknown")
SIP_ENABLED=true
if echo "$SIP_RAW" | grep -q "disabled"; then
  SIP_ENABLED=false
fi

if [[ "$SIP_ENABLED" == true ]]; then
  echo "[WARN] SIP is enabled — some service suppression will not persist across reboots"
  echo "       To disable SIP: boot Recovery Mode → Terminal → 'csrutil disable'"
  echo "       Continuing with non-SIP-required changes only..."
else
  echo "[INFO] SIP is disabled — full service suppression available"
fi
echo ""

# ---------------------------------------------------------------------------
# Sudo keepalive — request once, refresh in background
# ---------------------------------------------------------------------------
echo "[SUDO] This script requires administrator privileges."
sudo -v
while true; do sudo -n true; sleep 60; kill -0 "$$" || exit; done 2>/dev/null &
SUDO_KEEPALIVE_PID=$!
trap 'kill $SUDO_KEEPALIVE_PID 2>/dev/null' EXIT

echo ""

# ---------------------------------------------------------------------------
# Idempotency helpers
# ---------------------------------------------------------------------------

# Apply a pmset setting only if the current value differs
pmset_apply() {
  local key="$1" value="$2"
  local current
  current=$(pmset -g | awk -v k="$key" '$1==k{print $2}' | head -1)
  if [[ "$current" == "$value" ]]; then
    echo "[SKIP] pmset $key already $value"
  else
    sudo pmset -a "$key" "$value"
    echo "[SET]  pmset $key $value  (was: ${current:-unset})"
  fi
}


# Disable a launchd service (SIP-gated for system services)
disable_service() {
  local domain="$1"
  if [[ "$SIP_ENABLED" == false ]]; then
    sudo launchctl disable "$domain" 2>/dev/null || true
    echo "[SET]  disabled $domain"
  else
    echo "[SKIP-SIP] $domain (requires SIP off for persistence)"
  fi
}

echo "========================================"
echo "Section 1: Power Management"
echo "========================================"
echo ""

# ---------------------------------------------------------------------------
# 1.1 Power Management
# ---------------------------------------------------------------------------

# Core sleep prevention
pmset_apply sleep         0
pmset_apply disablesleep  1    # UPS sleep prevention; may not apply on all hardware
pmset_apply disksleep     0
pmset_apply standby       0
pmset_apply autopoweroff  0
pmset_apply powernap      0
pmset_apply networkoversleep 0
pmset_apply autorestart   1    # Restart after power failure — essential for 24/7 headless operation

# Remote access
pmset_apply womp          1    # Wake on magic packet (Wake-on-LAN)
pmset_apply tcpkeepalive  1    # Keep SSH alive during long inference

# Display — can sleep on a headless machine
pmset_apply displaysleep  10

# High Performance Mode on Apple Silicon is not settable via pmset on macOS 26 Tahoe.
# Enable it manually: System Settings → Battery → Options → High Power Mode

# MacBook-specific: battery + AC sleep prevention, clamshell warning
if echo "$HW_MODEL" | grep -qiE "MacBook"; then
  echo ""
  echo "[NOTICE] MacBook detected — applying battery and AC sleep settings"
  sudo pmset -b sleep 0
  sudo pmset -c sleep 0
  echo "[WARN]   Lid-close (clamshell) behavior requires a physical HDMI/USB-C dummy plug"
  echo "         OR run: sudo pmset -a lidwake 0  (thermal risk if vents are blocked)"
  echo "         Recommended: purchase an HDMI dummy plug before going headless."
fi

# Caffeinate LaunchDaemon — belt-and-suspenders for Sequoia sleep regression
CAFFEINATE_PLIST="/Library/LaunchDaemons/com.llm-server.caffeinate.plist"
if [[ ! -f "$CAFFEINATE_PLIST" ]]; then
  sudo tee "$CAFFEINATE_PLIST" > /dev/null <<'PLIST_EOF'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>com.llm-server.caffeinate</string>
  <key>ProgramArguments</key>
  <array>
    <string>/usr/bin/caffeinate</string>
    <string>-dimsu</string>
  </array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
</dict>
</plist>
PLIST_EOF
  sudo chown root:wheel "$CAFFEINATE_PLIST"
  sudo chmod 644 "$CAFFEINATE_PLIST"
  sudo launchctl bootstrap system "$CAFFEINATE_PLIST"
  echo "[SET]  caffeinate LaunchDaemon installed and started"
else
  echo "[SKIP] caffeinate LaunchDaemon already installed"
fi

# pmset self-heal timer — re-applies power settings daily at 03:00.
# macOS updates silently reset pmset values; this closes that gap automatically.
PMSET_HEAL_PLIST="/Library/LaunchDaemons/com.llm-server.pmset-heal.plist"
if [[ ! -f "$PMSET_HEAL_PLIST" ]]; then
  SETUP_PATH="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/setup.sh"
  sudo tee "$PMSET_HEAL_PLIST" > /dev/null <<PLIST_EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>com.llm-server.pmset-heal</string>
  <key>ProgramArguments</key>
  <array>
    <string>/bin/bash</string>
    <string>${SETUP_PATH}</string>
    <string>--power-only</string>
  </array>
  <key>StartCalendarInterval</key>
  <dict>
    <key>Hour</key><integer>3</integer>
    <key>Minute</key><integer>0</integer>
  </dict>
  <key>StandardOutPath</key><string>/var/log/mac-llm-setup/pmset-heal.log</string>
  <key>StandardErrorPath</key><string>/var/log/mac-llm-setup/pmset-heal.log</string>
</dict>
</plist>
PLIST_EOF
  sudo chown root:wheel "$PMSET_HEAL_PLIST"
  sudo chmod 644 "$PMSET_HEAL_PLIST"
  sudo launchctl bootstrap system "$PMSET_HEAL_PLIST"
  echo "[SET]  pmset-heal daily timer installed (runs at 03:00)"
else
  echo "[SKIP] pmset-heal timer already installed"
fi

# --power-only: exit here, skipping all other sections
if [[ "$POWER_ONLY" == true ]]; then
  echo ""
  echo "=== setup.sh --power-only complete at $(date) ==="
  exit 0
fi

echo ""
echo "========================================"
echo "Section 2: Network Stack Tuning"
echo "========================================"
echo ""

# ---------------------------------------------------------------------------
# 1.2 Network Stack Tuning (only if enabled in config)
#
# macOS stopped reading /etc/sysctl.conf at boot since Catalina.
# Persistence requires a LaunchDaemon with RunAtLoad that re-applies
# each sysctl key on every boot.
# ---------------------------------------------------------------------------
NETWORK_TUNING=$(echo "$CONFIG" | jq -r '.system.network_tuning // true')

SYSCTL_PLIST="/Library/LaunchDaemons/com.llm-server.sysctl-tuning.plist"

if [[ "$NETWORK_TUNING" == "true" ]]; then
  if [[ ! -f "$SYSCTL_PLIST" ]]; then
    sudo tee "$SYSCTL_PLIST" > /dev/null <<'PLIST_EOF'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>com.llm-server.sysctl-tuning</string>
  <key>ProgramArguments</key>
  <array>
    <string>/bin/sh</string>
    <string>-c</string>
    <string>/usr/sbin/sysctl -w net.inet.tcp.sendspace=1048576; /usr/sbin/sysctl -w net.inet.tcp.recvspace=1048576; /usr/sbin/sysctl -w kern.ipc.maxsockbuf=8388608; /usr/sbin/sysctl -w net.inet.tcp.autorcvbufmax=8388608; /usr/sbin/sysctl -w net.inet.tcp.autosndbufmax=8388608; /usr/sbin/sysctl -w kern.ipc.somaxconn=2048</string>
  </array>
  <key>RunAtLoad</key><true/>
</dict>
</plist>
PLIST_EOF
    sudo chown root:wheel "$SYSCTL_PLIST"
    sudo chmod 644 "$SYSCTL_PLIST"
    sudo launchctl bootstrap system "$SYSCTL_PLIST"
    echo "[SET]  sysctl-tuning LaunchDaemon installed and started"
  else
    echo "[SKIP] sysctl-tuning LaunchDaemon already installed"
  fi

  # Apply live for the current session
  # NOTE: net.inet.tcp.rfc1323 removed in El Capitan — do not add
  # NOTE: serverperfmode is Intel-only — breaks on Apple Silicon — do not add
  sudo sysctl -w net.inet.tcp.sendspace=1048576     2>/dev/null || true
  sudo sysctl -w net.inet.tcp.recvspace=1048576     2>/dev/null || true
  sudo sysctl -w kern.ipc.maxsockbuf=8388608        2>/dev/null || true
  sudo sysctl -w net.inet.tcp.autorcvbufmax=8388608 2>/dev/null || true
  sudo sysctl -w net.inet.tcp.autosndbufmax=8388608 2>/dev/null || true
  sudo sysctl -w kern.ipc.somaxconn=2048            2>/dev/null || true
  echo "[SET]  Network sysctl tuning applied (live + persistent via LaunchDaemon)"
else
  echo "[SKIP] Network tuning disabled in config.json"
fi

echo ""
echo "========================================"
echo "Section 3: Service Suppression"
echo "========================================"
echo ""

# ---------------------------------------------------------------------------
# 1.3 Service Suppression
# ---------------------------------------------------------------------------

# Pre-change service state snapshot (required for restore.sh)
SNAPSHOT_DIR="/var/log/mac-llm-setup/snapshots"
sudo mkdir -p "$SNAPSHOT_DIR"
SNAPSHOT="$SNAPSHOT_DIR/services-$(date +%Y%m%d).txt"
if [[ ! -f "$SNAPSHOT" ]]; then
  launchctl print-disabled system > "$SNAPSHOT" 2>/dev/null || true
  launchctl print-disabled "gui/$(id -u)" >> "$SNAPSHOT" 2>/dev/null || true
  echo "[SNAPSHOT] Pre-change service state saved to $SNAPSHOT"
else
  echo "[SKIP] Service snapshot already exists for today: $SNAPSHOT"
fi

# Spotlight — biggest inference competitor; suppress regardless of SIP
sudo mdutil -a -i off 2>/dev/null || true
sudo mdutil -i off /Library/Ollama 2>/dev/null || true
echo "[SET]  Spotlight indexing disabled"

if [[ "$SIP_ENABLED" == false ]]; then
  sudo launchctl bootout system \
    /System/Library/LaunchDaemons/com.apple.metadata.mds.plist 2>/dev/null || true
  sudo launchctl disable system/com.apple.metadata.mds 2>/dev/null || true
  echo "[SET]  Spotlight daemon disabled (SIP off)"
fi

# Telemetry
DISABLE_TELEMETRY=$(echo "$CONFIG" | jq -r '.system.disable_telemetry // true')
if [[ "$DISABLE_TELEMETRY" == "true" ]]; then
  disable_service "system/com.apple.analyticsd"
  disable_service "system/com.apple.diagnosticd"
  disable_service "system/com.apple.spindump"
  disable_service "system/com.apple.tailspind"
  disable_service "system/com.apple.triald"
  disable_service "gui/$(id -u)/com.apple.UsageTrackingAgent"
fi

# Siri
DISABLE_SIRI=$(echo "$CONFIG" | jq -r '.system.disable_siri // true')
if [[ "$DISABLE_SIRI" == "true" ]]; then
  disable_service "gui/$(id -u)/com.apple.Siri"
  disable_service "gui/$(id -u)/com.apple.siriknowledged"
  disable_service "gui/$(id -u)/com.apple.assistant_service"
  disable_service "system/com.apple.siriinferenced"
fi

# iCloud
DISABLE_ICLOUD=$(echo "$CONFIG" | jq -r '.system.disable_icloud // true')
if [[ "$DISABLE_ICLOUD" == "true" ]]; then
  disable_service "gui/$(id -u)/com.apple.cloudd"
  disable_service "gui/$(id -u)/com.apple.cloudpaird"
  disable_service "gui/$(id -u)/com.apple.iCloudNotificationAgent"
fi

# Media services
DISABLE_MEDIA=$(echo "$CONFIG" | jq -r '.system.disable_media_services // true')
if [[ "$DISABLE_MEDIA" == "true" ]]; then
  disable_service "gui/$(id -u)/com.apple.AMPArtworkAgent"
  disable_service "gui/$(id -u)/com.apple.AMPLibraryAgent"
  disable_service "gui/$(id -u)/com.apple.music.d"
fi

# Biome / knowledge graph — heavy background ML that competes for ANE bandwidth
disable_service "system/com.apple.biomed"
disable_service "gui/$(id -u)/com.apple.biomesyncd"
disable_service "gui/$(id -u)/com.apple.contextstored"
disable_service "gui/$(id -u)/com.apple.knowledge-agent"
disable_service "gui/$(id -u)/com.apple.LiveLookup"
disable_service "gui/$(id -u)/com.apple.parsecd"
disable_service "gui/$(id -u)/com.apple.tipsd"

echo ""
echo "--- defaults write changes ---"
echo ""

# AirDrop / Handoff
DISABLE_AIRDROP=$(echo "$CONFIG" | jq -r '.system.disable_airdrop_handoff // true')
if [[ "$DISABLE_AIRDROP" == "true" ]]; then
  defaults write com.apple.NetworkBrowser DisableAirDrop -bool true
  defaults write com.apple.coreservices.useractivityd ActivityAdvertisingAllowed -bool false
  defaults write com.apple.coreservices.useractivityd ActivityReceivingAllowed -bool false
  echo "[SET]  AirDrop and Handoff disabled"
fi

# App Nap — throttles background processes; disable for always-on inference
defaults write NSGlobalDomain NSAppSleepDisabled -bool YES
echo "[SET]  App Nap disabled"

# Notification Center — DND all day (0 = midnight, 1440 = 23:59)
DISABLE_NOTIF=$(echo "$CONFIG" | jq -r '.system.disable_notifications // true')
if [[ "$DISABLE_NOTIF" == "true" ]]; then
  defaults write com.apple.notificationcenterui dndStart -int 0
  defaults write com.apple.notificationcenterui dndEnd -int 1440
  echo "[SET]  Notification Center DND enabled (all day)"
fi

# Dock/Finder animations — reduce WindowServer load on headless machine
defaults write com.apple.dock launchanim -bool false
defaults write com.apple.dock expose-animation-duration -float 0
defaults write com.apple.finder DisableAllAnimations -bool true
killall Dock   2>/dev/null || true
killall Finder 2>/dev/null || true
echo "[SET]  Dock and Finder animations disabled"

# Screen saver off
defaults -currentHost write com.apple.screensaver idleTime 0
echo "[SET]  Screen saver disabled"

# Software Update
DISABLE_SWU=$(echo "$CONFIG" | jq -r '.system.disable_software_update // true')
if [[ "$DISABLE_SWU" == "true" ]]; then
  sudo softwareupdate --schedule off 2>/dev/null || true
  sudo defaults write /Library/Preferences/com.apple.SoftwareUpdate \
    AutomaticCheckEnabled -bool false
  sudo defaults write /Library/Preferences/com.apple.SoftwareUpdate \
    AutomaticDownload -bool false
  sudo defaults write /Library/Preferences/com.apple.SoftwareUpdate \
    AutomaticallyInstallMacOSUpdates -bool false
  echo "[SET]  Automatic software updates disabled"
fi

# Time Machine
DISABLE_TM=$(echo "$CONFIG" | jq -r '.system.disable_time_machine // true')
if [[ "$DISABLE_TM" == "true" ]]; then
  sudo tmutil disable 2>/dev/null || true
  sudo tmutil addexclusion /Library/Ollama 2>/dev/null || true
  echo "[SET]  Time Machine disabled; /Library/Ollama excluded"
fi

echo ""
echo "========================================"
echo "Section 4: SSH Hardening"
echo "========================================"
echo ""

# ---------------------------------------------------------------------------
# 1.4 SSH Hardening
#
# Uses a drop-in file under /etc/ssh/sshd_config.d/ rather than editing
# /etc/ssh/sshd_config directly. The drop-in survives macOS OS updates;
# direct edits to sshd_config are silently overwritten.
#
# macOS sshd is launchd socket-activated — new connections automatically
# pick up the drop-in without a daemon restart.
#
# PasswordAuthentication is set to no only when an authorized_keys file
# already exists. Setting it without confirmed key access on a headless box
# is a lockout risk.
# ---------------------------------------------------------------------------
SSHD_DROPIN_DIR="/etc/ssh/sshd_config.d"
SSHD_DROPIN="$SSHD_DROPIN_DIR/100-headless.conf"

# Enable SSH — systemsetup is deprecated on macOS 26+; use launchctl as primary
if sudo launchctl enable system/com.openssh.sshd 2>/dev/null && \
   sudo launchctl kickstart -k system/com.openssh.sshd 2>/dev/null; then
  echo "[SET]  Remote Login (SSH) enabled via launchctl"
else
  sudo systemsetup -setremotelogin on 2>/dev/null || true
  echo "[SET]  Remote Login (SSH) enabled via systemsetup"
fi

# Check for an authorized_keys file before disabling password auth
AUTHORIZED_KEYS_FOUND=false
if [[ -f "$HOME/.ssh/authorized_keys" ]] || [[ -f "/etc/ssh/authorized_keys" ]]; then
  AUTHORIZED_KEYS_FOUND=true
fi

sudo mkdir -p "$SSHD_DROPIN_DIR"

# Build the drop-in content; only write if different from what's on disk
DROPIN_CONTENT="# Managed by headless-macs setup.sh — do not edit manually.
# To change settings, edit config and re-run sudo ./setup.sh
PermitRootLogin no
PubkeyAuthentication yes
MaxAuthTries 3
ClientAliveInterval 120
ClientAliveCountMax 10"

if [[ "$AUTHORIZED_KEYS_FOUND" == true ]]; then
  DROPIN_CONTENT="${DROPIN_CONTENT}
PasswordAuthentication no"
else
  echo "[WARN] No authorized_keys found — skipping PasswordAuthentication no to avoid lockout"
  echo "       Copy your public key to ~/.ssh/authorized_keys, then re-run setup.sh"
fi

EXISTING=""
[[ -f "$SSHD_DROPIN" ]] && EXISTING=$(sudo cat "$SSHD_DROPIN" 2>/dev/null || true)

if [[ "$EXISTING" == "$DROPIN_CONTENT" ]]; then
  echo "[SKIP] sshd drop-in already up to date: $SSHD_DROPIN"
else
  echo "$DROPIN_CONTENT" | sudo tee "$SSHD_DROPIN" > /dev/null
  sudo chmod 644 "$SSHD_DROPIN"
  echo "[SET]  sshd drop-in written: $SSHD_DROPIN"
  echo "       New connections will pick up the config automatically (socket-activated sshd)."
fi

echo ""
echo "========================================"
echo "Section 5: Xcode Command Line Tools"
echo "========================================"
echo ""

# ---------------------------------------------------------------------------
# 1.5 Xcode CLT — headless-safe install via softwareupdate
# ---------------------------------------------------------------------------
if xcode-select -p &>/dev/null; then
  echo "[SKIP] Xcode Command Line Tools already installed: $(xcode-select -p)"
else
  echo "[INFO] Installing Xcode Command Line Tools (headless method)..."
  touch /tmp/.com.apple.dt.CommandLineTools.installondemand.in-progress
  CLT_PROD=$(softwareupdate --list 2>&1 \
    | grep -B1 "Command Line Tools" \
    | head -1 \
    | awk -F'*' '{print $2}' \
    | sed 's/^ *//' \
    | tr -d '\n')
  if [[ -n "$CLT_PROD" ]]; then
    sudo softwareupdate --install "$CLT_PROD" --verbose
    echo "[SET]  Xcode Command Line Tools installed"
  else
    echo "[WARN] Could not find CLT in softwareupdate list — may require GUI install"
    echo "       Run: xcode-select --install  (if a display is attached)"
  fi
  rm -f /tmp/.com.apple.dt.CommandLineTools.installondemand.in-progress
fi

echo ""
echo "========================================"
echo "Section 6: Application Firewall"
echo "========================================"
echo ""

# ---------------------------------------------------------------------------
# 1.6 Application Firewall
#
# Default: left ENABLED. Services bind to localhost by default (localhost_only: true).
# Clients on other LAN hosts must set network.localhost_only: false in config.json
# AND decide whether to disable the firewall.
#
# Set network.disable_firewall: true only if you run unsigned Python inference
# services (Rapid-MLX, mlx-lm, Infinity) on an isolated trusted LAN and cannot
# manage per-app firewall rules — those services would silently block with no
# one present to click "Allow".
# ---------------------------------------------------------------------------
DISABLE_FIREWALL=$(echo "$CONFIG" | jq -r '.network.disable_firewall // false')
FIREWALL_CMD="/usr/libexec/ApplicationFirewall/socketfilterfw"

if [[ "$DISABLE_FIREWALL" == "true" ]]; then
  CURRENT_FW_STATE=$(sudo "$FIREWALL_CMD" --getglobalstate 2>/dev/null || echo "unknown")
  if echo "$CURRENT_FW_STATE" | grep -qi "disabled"; then
    echo "[SKIP] Application Firewall already disabled"
  else
    sudo "$FIREWALL_CMD" --setglobalstate off
    echo "[SET]  Application Firewall disabled (network.disable_firewall: true in config)"
    echo "       Ensure inference nodes are on a trusted isolated network."
  fi
else
  echo "[SKIP] Firewall left enabled (default — network.disable_firewall: false)"
  echo "       If inference clients are on other LAN hosts, set network.localhost_only: false"
  echo "       and ensure inbound ports are open: 11434 (Ollama) 8000 (Rapid-MLX)"
  echo "       8080 (mlx-lm) 7997 (Infinity) 52415 (Exo)"
fi

echo ""
echo "========================================"
echo "Section 7: File Descriptor Limits"
echo "========================================"
echo ""

# ---------------------------------------------------------------------------
# 1.7 File Descriptor Limits
#
# Concurrent model loading and parallel inference connections can exhaust
# the default fd limit. A LaunchDaemon raises the system-wide maxfiles on
# every boot.
# ---------------------------------------------------------------------------
MAXFILES_PLIST="/Library/LaunchDaemons/com.llm-server.maxfiles.plist"
if [[ ! -f "$MAXFILES_PLIST" ]]; then
  sudo tee "$MAXFILES_PLIST" > /dev/null <<'PLIST_EOF'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>com.llm-server.maxfiles</string>
  <key>ProgramArguments</key>
  <array>
    <string>/bin/launchctl</string>
    <string>limit</string>
    <string>maxfiles</string>
    <string>524288</string>
    <string>1048576</string>
  </array>
  <key>RunAtLoad</key><true/>
</dict>
</plist>
PLIST_EOF
  sudo chown root:wheel "$MAXFILES_PLIST"
  sudo chmod 644 "$MAXFILES_PLIST"
  sudo launchctl bootstrap system "$MAXFILES_PLIST"
  echo "[SET]  maxfiles LaunchDaemon installed and started"
else
  echo "[SKIP] maxfiles LaunchDaemon already installed"
fi

# Apply live for the current session
sudo launchctl limit maxfiles 524288 1048576 2>/dev/null || true
echo "[SET]  File descriptor limits raised (soft: 524288 / hard: 1048576)"

echo ""
echo "========================================"
echo "setup.sh complete"
echo "========================================"
echo ""
echo "Next step: sudo ./install-tools.sh"
echo "To verify:  ./verify.sh"
echo "To undo:    sudo ./restore.sh"
echo ""
echo "Log written to: $LOG_FILE"
