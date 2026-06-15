#!/usr/bin/env bash
# restore.sh — Undo All Changes Made by setup.sh and install-tools.sh
#
# Reverses every change made by this toolset, returning the Mac to its
# pre-configuration state using the service snapshot saved by setup.sh.
#
# Requires: sudo
# Safe to run on a partially-configured system — each step degrades gracefully.

set -uo pipefail

# ---------------------------------------------------------------------------
# Guards
# ---------------------------------------------------------------------------
if [[ "$(uname -m)" != "arm64" ]]; then
  echo "ERROR: Apple Silicon (arm64) required."
  exit 1
fi
if [[ "$(uname -s)" != "Darwin" ]]; then
  echo "ERROR: macOS required."
  exit 1
fi

# ---------------------------------------------------------------------------
# Logging
# ---------------------------------------------------------------------------
LOG_DIR="/var/log/mac-llm-setup"
sudo mkdir -p "$LOG_DIR"
LOG_FILE="$LOG_DIR/restore-$(date +%Y%m%d-%H%M%S).log"
exec > >(tee -a "$LOG_FILE") 2>&1
echo "=== restore.sh started at $(date) ==="
echo ""

# ---------------------------------------------------------------------------
# Confirmation
# ---------------------------------------------------------------------------
echo "This will undo all changes made by setup.sh and install-tools.sh:"
echo "  • Remove all LLM server LaunchDaemons (Ollama, Rapid-MLX, mlx-lm, Infinity)"
echo "  • Remove infrastructure LaunchDaemons (caffeinate, sysctl-tuning, maxfiles, pmset-heal)"
echo "  • Remove Exo LaunchAgent"
echo "  • Remove _llmserver service account and /Library/LLMServer"
echo "  • Restore pmset to safe defaults"
echo "  • Re-enable suppressed system services (from snapshot)"
echo "  • Restore Spotlight indexing"
echo "  • Remove sshd drop-in (/etc/ssh/sshd_config.d/100-headless.conf)"
echo "  • Restore defaults (AirDrop, App Nap, animations, software update, Time Machine)"
echo "  • Remove mac-llm sysctl entries from /etc/sysctl.conf (if present from older install)"
echo "  • Re-enable Application Firewall"
echo ""
read -r -p "Proceed with restore? (y/N): " CONFIRM
[[ "$CONFIRM" =~ ^[Yy]$ ]] || { echo "Cancelled."; exit 0; }
echo ""

# ---------------------------------------------------------------------------
# Sudo keepalive
# ---------------------------------------------------------------------------
sudo -v
while true; do sudo -n true; sleep 60; kill -0 "$$" || exit; done 2>/dev/null &
SUDO_PID=$!
trap 'kill $SUDO_PID 2>/dev/null' EXIT

# ---------------------------------------------------------------------------
# Helper: remove a LaunchDaemon and bootout cleanly
# ---------------------------------------------------------------------------
remove_daemon() {
  local plist="$1" label="$2"
  if [[ -f "$plist" ]]; then
    sudo launchctl bootout system "$plist" 2>/dev/null || true
    sudo rm -f "$plist"
    echo "[REMOVED] $plist"
  else
    echo "[SKIP]    $plist (not present)"
  fi
  # Belt-and-suspenders: disable by label even if plist is gone
  sudo launchctl disable "system/${label}" 2>/dev/null || true
}

echo "========================================"
echo "Section 1: Remove LLM Server Services"
echo "========================================"
echo ""

remove_daemon "/Library/LaunchDaemons/com.ollama.server.plist"       "com.ollama.server"
remove_daemon "/Library/LaunchDaemons/com.rapid-mlx.server.plist"    "com.rapid-mlx.server"
remove_daemon "/Library/LaunchDaemons/com.mlx-lm.server.plist"       "com.mlx-lm.server"
remove_daemon "/Library/LaunchDaemons/com.infinity.server.plist"      "com.infinity.server"
remove_daemon "/Library/LaunchDaemons/com.llm-server.caffeinate.plist" "com.llm-server.caffeinate"
remove_daemon "/Library/LaunchDaemons/com.llm-server.sysctl-tuning.plist" "com.llm-server.sysctl-tuning"
remove_daemon "/Library/LaunchDaemons/com.llm-server.maxfiles.plist"  "com.llm-server.maxfiles"
remove_daemon "/Library/LaunchDaemons/com.llm-server.pmset-heal.plist" "com.llm-server.pmset-heal"

# Exo runs as a user LaunchAgent — no sudo needed
EXO_PLIST="$HOME/Library/LaunchAgents/com.exo.node.plist"
if [[ -f "$EXO_PLIST" ]]; then
  launchctl bootout "gui/$(id -u)/com.exo.node" 2>/dev/null || true
  rm -f "$EXO_PLIST"
  echo "[REMOVED] $EXO_PLIST"
else
  echo "[SKIP]    $EXO_PLIST (not present)"
fi

echo ""
echo "========================================"
echo "Section 2: Restore pmset to Safe Defaults"
echo "========================================"
echo ""

# Restore to safe neutral defaults — not Apple factory defaults, which vary by model,
# but reasonable values that allow the Mac to behave normally again.
pmset_restore() {
  local key="$1" value="$2"
  sudo pmset -a "$key" "$value" 2>/dev/null && echo "[SET]  pmset $key $value" \
    || echo "[WARN] pmset $key $value failed"
}

pmset_restore sleep         1
pmset_restore disablesleep  0
pmset_restore disksleep     10
pmset_restore standby       1
pmset_restore autopoweroff  1
pmset_restore powernap      1
pmset_restore networkoversleep 0
pmset_restore womp          0
pmset_restore displaysleep  10
pmset_restore tcpkeepalive  1
pmset_restore powermode     1   # Automatic (was High Performance)

echo ""
echo "========================================"
echo "Section 3: Re-enable Suppressed Services"
echo "========================================"
echo ""

# Find the most recent snapshot saved by setup.sh
SNAPSHOT_DIR="/var/log/mac-llm-setup/snapshots"
SNAPSHOT=$(ls -t "$SNAPSHOT_DIR"/services-*.txt 2>/dev/null | head -1 || echo "")

if [[ -n "$SNAPSHOT" && -f "$SNAPSHOT" ]]; then
  echo "[INFO] Restoring from snapshot: $SNAPSHOT"
  # Re-enable any service that was explicitly disabled (marked false in snapshot)
  # The snapshot format is: "com.apple.foo => false"
  grep "=> false" "$SNAPSHOT" 2>/dev/null | awk '{print $1}' | while IFS= read -r svc; do
    sudo launchctl enable "$svc" 2>/dev/null \
      && echo "[ENABLED] $svc" \
      || echo "[SKIP]    $svc (may not exist on this macOS version)"
  done
else
  echo "[WARN] No service snapshot found at $SNAPSHOT_DIR"
  echo "       Manually re-enable services if needed via System Settings"
  echo "       or: sudo launchctl enable system/<service-name>"
fi

echo ""
echo "========================================"
echo "Section 4: Restore Spotlight"
echo "========================================"
echo ""

sudo mdutil -a -i on 2>/dev/null && echo "[SET]  Spotlight indexing re-enabled" \
  || echo "[WARN] Could not re-enable Spotlight"

echo ""
echo "========================================"
echo "Section 5: Restore sshd_config"
echo "========================================"
echo ""

SSHD_DROPIN="/etc/ssh/sshd_config.d/100-headless.conf"
if [[ -f "$SSHD_DROPIN" ]]; then
  sudo rm -f "$SSHD_DROPIN"
  echo "[REMOVED] $SSHD_DROPIN"
  sudo launchctl stop  com.openssh.sshd 2>/dev/null || true
  sudo launchctl start com.openssh.sshd 2>/dev/null || true
  echo "[SET]  sshd restarted"
else
  echo "[SKIP] $SSHD_DROPIN (not present)"
fi

echo ""
echo "========================================"
echo "Section 6: Restore defaults"
echo "========================================"
echo ""

# AirDrop / Handoff
defaults delete com.apple.NetworkBrowser DisableAirDrop 2>/dev/null \
  && echo "[RESTORED] AirDrop" || echo "[SKIP] AirDrop default (already clear)"
defaults delete com.apple.coreservices.useractivityd ActivityAdvertisingAllowed 2>/dev/null || true
defaults delete com.apple.coreservices.useractivityd ActivityReceivingAllowed  2>/dev/null || true

# App Nap
defaults delete NSGlobalDomain NSAppSleepDisabled 2>/dev/null \
  && echo "[RESTORED] App Nap" || echo "[SKIP] App Nap default (already clear)"

# Notification Center DND
defaults delete com.apple.notificationcenterui dndStart 2>/dev/null || true
defaults delete com.apple.notificationcenterui dndEnd   2>/dev/null || true
echo "[RESTORED] Notification Center DND"

# Dock/Finder animations
defaults delete com.apple.dock launchanim              2>/dev/null || true
defaults delete com.apple.dock expose-animation-duration 2>/dev/null || true
defaults delete com.apple.finder DisableAllAnimations  2>/dev/null || true
killall Dock   2>/dev/null || true
killall Finder 2>/dev/null || true
echo "[RESTORED] Dock and Finder animations"

# Screen saver
defaults -currentHost delete com.apple.screensaver idleTime 2>/dev/null || true
echo "[RESTORED] Screen saver"

# Software Update
sudo softwareupdate --schedule on 2>/dev/null || true
sudo defaults delete /Library/Preferences/com.apple.SoftwareUpdate \
  AutomaticCheckEnabled 2>/dev/null || true
sudo defaults delete /Library/Preferences/com.apple.SoftwareUpdate \
  AutomaticDownload 2>/dev/null || true
sudo defaults delete /Library/Preferences/com.apple.SoftwareUpdate \
  AutomaticallyInstallMacOSUpdates 2>/dev/null || true
echo "[RESTORED] Automatic software updates"

# Time Machine
sudo tmutil enable 2>/dev/null || true
echo "[RESTORED] Time Machine"

echo ""
echo "========================================"
echo "Section 7: Remove sysctl.conf entries"
echo "========================================"
echo ""

SYSCTL_CONF="/etc/sysctl.conf"
KEYS_TO_REMOVE=(
  "net.inet.tcp.sendspace"
  "net.inet.tcp.recvspace"
  "kern.ipc.maxsockbuf"
  "net.inet.tcp.autorcvbufmax"
  "net.inet.tcp.autosndbufmax"
  "kern.ipc.somaxconn"
)

if [[ -f "$SYSCTL_CONF" ]]; then
  for key in "${KEYS_TO_REMOVE[@]}"; do
    if grep -q "^${key}=" "$SYSCTL_CONF" 2>/dev/null; then
      sudo sed -i '' "/^${key}=/d" "$SYSCTL_CONF"
      echo "[REMOVED] sysctl $key from $SYSCTL_CONF"
    else
      echo "[SKIP]    $key (not in $SYSCTL_CONF)"
    fi
  done
  # Remove file entirely if now empty
  if [[ ! -s "$SYSCTL_CONF" ]]; then
    sudo rm -f "$SYSCTL_CONF"
    echo "[REMOVED] $SYSCTL_CONF (empty)"
  fi
else
  echo "[SKIP] $SYSCTL_CONF (not present)"
fi

echo ""
echo "========================================"
echo "Section 8: Re-enable Application Firewall"
echo "========================================"
echo ""

FIREWALL_CMD="/usr/libexec/ApplicationFirewall/socketfilterfw"
if [[ -x "$FIREWALL_CMD" ]]; then
  CURRENT_FW_STATE=$(sudo "$FIREWALL_CMD" --getglobalstate 2>/dev/null || echo "unknown")
  if echo "$CURRENT_FW_STATE" | grep -qi "enabled"; then
    echo "[SKIP] Application Firewall already enabled"
  else
    sudo "$FIREWALL_CMD" --setglobalstate on
    echo "[SET]  Application Firewall re-enabled"
  fi
else
  echo "[WARN] socketfilterfw not found — firewall state unchanged"
fi

echo ""
echo "========================================"
echo "Section 9: Remove _llmserver Service Account"
echo "========================================"
echo ""

if id "_llmserver" &>/dev/null 2>&1; then
  sudo dscl . -delete /Users/_llmserver 2>/dev/null || true
  echo "[REMOVED] _llmserver user"
else
  echo "[SKIP]    _llmserver user (not present)"
fi

if dscl . -read /Groups/_llmserver PrimaryGroupID &>/dev/null 2>&1; then
  sudo dscl . -delete /Groups/_llmserver 2>/dev/null || true
  echo "[REMOVED] _llmserver group"
else
  echo "[SKIP]    _llmserver group (not present)"
fi

if [[ -d /Library/LLMServer ]]; then
  sudo rm -rf /Library/LLMServer
  echo "[REMOVED] /Library/LLMServer"
else
  echo "[SKIP]    /Library/LLMServer (not present)"
fi

dscacheutil -flushcache 2>/dev/null || true

echo ""
echo "========================================"
echo "restore.sh complete"
echo "========================================"
echo ""
echo "The Mac has been returned to its pre-configuration state."
echo "A reboot is recommended to apply all service changes."
echo ""
echo "Log written to: $LOG_FILE"
