#!/usr/bin/env bash
# update-tools.sh — In-place upgrade for LLM serving tools
#
# Updates a tool's binary without losing daemon configuration or models.
# Stops the daemon, runs the upstream installer, re-removes any conflicting
# login items, then re-bootstraps the daemon.
#
# Usage:
#   sudo ./update-tools.sh ollama
#   sudo ./update-tools.sh all
#
# Idempotent: safe to run multiple times.

set -euo pipefail

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
LOG_FILE="$LOG_DIR/update-tools-$(date +%Y%m%d-%H%M%S).log"
exec > >(tee -a "$LOG_FILE") 2>&1
echo "=== update-tools.sh started at $(date) ==="

# ---------------------------------------------------------------------------
# Config
# ---------------------------------------------------------------------------
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CONFIG_FILE="$SCRIPT_DIR/config.json"

if [[ ! -f "$CONFIG_FILE" ]]; then
  echo "[WARN] config.json not found at $CONFIG_FILE"
  read -rp "       Path to config.json: " CONFIG_FILE
  [[ -f "$CONFIG_FILE" ]] || { echo "ERROR: Not found: $CONFIG_FILE"; exit 1; }
fi
if ! command -v jq &>/dev/null; then
  echo "ERROR: jq required — brew install jq"
  exit 1
fi

CONFIG=$(cat "$CONFIG_FILE")

# ---------------------------------------------------------------------------
# Sudo keepalive
# ---------------------------------------------------------------------------
sudo -v
while true; do sudo -n true; sleep 60; kill -0 "$$" || exit; done 2>/dev/null &
SUDO_PID=$!
trap 'kill $SUDO_PID 2>/dev/null' EXIT

# ---------------------------------------------------------------------------
# Update: Ollama
# ---------------------------------------------------------------------------
update_ollama() {
  echo ""
  echo "========================================"
  echo "Updating Ollama"
  echo "========================================"

  if [[ "$(echo "$CONFIG" | jq -r '.tools.ollama.enabled')" != "true" ]]; then
    echo "[SKIP] Ollama not enabled in config.json"
    return
  fi

  OLLAMA_PLIST="/Library/LaunchDaemons/com.ollama.server.plist"

  # Step 1: Stop daemon
  if sudo launchctl print "system/com.ollama.server" 2>/dev/null | grep -q "state = running"; then
    echo "[INFO] Stopping com.ollama.server daemon..."
    sudo launchctl bootout system "$OLLAMA_PLIST" 2>/dev/null || true
    sleep 2
    echo "[SET]  Daemon stopped"
  else
    echo "[INFO] com.ollama.server not running — continuing"
  fi

  # Step 2: Run upstream installer (idempotent — upgrades if newer version available)
  echo "[INFO] Running Ollama installer..."
  curl -fsSL https://ollama.com/install.sh | sh
  echo "[SET]  Ollama installer complete"

  # Step 3: Re-remove login item — the installer may re-add it
  osascript -e 'tell application "System Events" to delete login item "Ollama"' \
    2>/dev/null || true
  pkill -f "Ollama.app" 2>/dev/null || true
  echo "[SET]  Login item removed (daemon manages startup)"

  # Step 4: Re-bootstrap daemon
  if [[ -f "$OLLAMA_PLIST" ]]; then
    sudo launchctl bootstrap system "$OLLAMA_PLIST"
    sleep 3
    echo "[SET]  com.ollama.server re-bootstrapped"

    # Step 5: Verify
    if curl -sf --max-time 10 http://localhost:11434/api/tags 2>/dev/null | grep -q "models"; then
      NEW_VERSION=$(ollama --version 2>/dev/null || echo "unknown")
      echo "[OK]   Ollama API responding — version: $NEW_VERSION"
    else
      echo "[WARN] Ollama API not yet responding — may still be starting"
      echo "       Check: sudo launchctl print system/com.ollama.server"
      echo "       Logs:  tail -f /var/log/ollama/stderr.log"
    fi
  else
    echo "[WARN] Plist not found at $OLLAMA_PLIST — run sudo ./install-tools.sh first"
  fi
}

# ---------------------------------------------------------------------------
# Dispatcher
# ---------------------------------------------------------------------------
COMPONENT="${1:-}"

case "$COMPONENT" in
  ollama)
    update_ollama
    ;;
  all)
    update_ollama
    ;;
  "")
    echo "Usage: sudo ./update-tools.sh <component>"
    echo ""
    echo "Components:"
    echo "  ollama   Update Ollama binary and restart daemon"
    echo "  all      Update all enabled tools"
    exit 1
    ;;
  *)
    echo "ERROR: Unknown component: $COMPONENT"
    echo "Usage: sudo ./update-tools.sh ollama|all"
    exit 1
    ;;
esac

echo ""
echo "========================================"
echo "update-tools.sh complete"
echo "========================================"
echo "Log written to: $LOG_FILE"
