#!/usr/bin/env bash
# install-tools.sh — Serving Stack Installer for Mac LLM Optimizer
#
# Installs and configures LLM serving tools as system services.
# Each tool is gated by its enabled flag in config.json.
#
# Run order: precheck.sh → setup.sh → [storage-volume.sh] → install-tools.sh
#
# Supported tools:
#   ollama      — General inference, model management (default: enabled)
#   rapid_mlx   — Max-speed inference + tool calling for coding agents (default: disabled)
#   mlx_lm      — Raw MLX server for custom HuggingFace models (default: disabled)
#   infinity    — GPU-accelerated embeddings and reranking (default: disabled)
#   exo         — Multi-Mac distributed inference (default: disabled)
#
# Requires: sudo (prompted once at start), Homebrew

set -euo pipefail

# ---------------------------------------------------------------------------
# Guards
# ---------------------------------------------------------------------------
ARCH=$(uname -m)
if [[ "$ARCH" != "arm64" ]]; then
  echo "ERROR: Apple Silicon (arm64) required. Detected: $ARCH"
  exit 1
fi
if [[ "$(uname -s)" != "Darwin" ]]; then
  echo "ERROR: macOS required."
  exit 1
fi

# ---------------------------------------------------------------------------
# Hardware detection
# ---------------------------------------------------------------------------
OS_VERSION=$(sw_vers -productVersion)
HW_MODEL=$(sysctl -n hw.model 2>/dev/null || echo "unknown")
RAM_GB=$(( $(sysctl -n hw.memsize) / 1024 / 1024 / 1024 ))

# ---------------------------------------------------------------------------
# Logging
# ---------------------------------------------------------------------------
LOG_DIR="/var/log/mac-llm-setup"
sudo mkdir -p "$LOG_DIR"
LOG_FILE="$LOG_DIR/install-tools-$(date +%Y%m%d-%H%M%S).log"
exec > >(tee -a "$LOG_FILE") 2>&1
echo "=== install-tools.sh started at $(date) ==="
echo "Hardware: $HW_MODEL | RAM: ${RAM_GB}GB | macOS: $OS_VERSION"
echo ""

# ---------------------------------------------------------------------------
# Config
# ---------------------------------------------------------------------------
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CONFIG_FILE="$SCRIPT_DIR/config.json"

if [[ ! -f "$CONFIG_FILE" ]]; then
  echo "[WARN] config.json not found at default location: $CONFIG_FILE"
  read -rp "       Path to config.json: " CONFIG_FILE
  [[ -f "$CONFIG_FILE" ]] || { echo "ERROR: Not found: $CONFIG_FILE"; exit 1; }
fi
if ! command -v jq &>/dev/null; then
  echo "ERROR: jq required — brew install jq"
  exit 1
fi

CONFIG=$(cat "$CONFIG_FILE")
echo "[CONFIG] Loaded $CONFIG_FILE"

# Remote access mode — default is 0.0.0.0 (all interfaces).
# Set network.localhost_only: true in config.json to restrict to loopback only.
LOCALHOST_ONLY=$(echo "$CONFIG" | jq -r '.network.localhost_only // false')
if [[ "$LOCALHOST_ONLY" == "true" ]]; then
  echo "[INFO]   localhost_only=true — all services will bind to 127.0.0.1 (loopback only)"
else
  echo "[INFO]   Remote access enabled — all services will bind to 0.0.0.0 (all interfaces)"
fi
echo ""

# ---------------------------------------------------------------------------
# Sudo keepalive
# ---------------------------------------------------------------------------
sudo -v
while true; do sudo -n true; sleep 60; kill -0 "$$" || exit; done 2>/dev/null &
SUDO_PID=$!
trap 'kill $SUDO_PID 2>/dev/null' EXIT

# ---------------------------------------------------------------------------
# Prerequisites
# ---------------------------------------------------------------------------
if ! command -v brew &>/dev/null; then
  echo "ERROR: Homebrew not found. Install from https://brew.sh first."
  exit 1
fi

# ---------------------------------------------------------------------------
# Helper: load/reload a LaunchDaemon plist
# ---------------------------------------------------------------------------
load_daemon() {
  local plist="$1"
  sudo launchctl bootout system "$plist" 2>/dev/null || true
  sleep 1
  sudo launchctl bootstrap system "$plist"
}

# Helper: verify an HTTP endpoint responds
check_endpoint() {
  local name="$1" url="$2" pattern="$3" timeout="${4:-10}"
  if curl -sf --max-time "$timeout" "$url" | grep -q "$pattern" 2>/dev/null; then
    echo "[OK]   $name API responding ($url)"
    return 0
  else
    echo "[WARN] $name API not yet responding — may still be starting"
    echo "       Check: sudo launchctl print system/$(echo "$name" | tr '[:upper:]' '[:lower:]')"
    return 1
  fi
}

# ---------------------------------------------------------------------------
# Service account: _llmserver
# Serving tool daemons run as this unprivileged user instead of root.
# ---------------------------------------------------------------------------
LLMSERVER_USER="_llmserver"
LLMSERVER_HOME="/Library/LLMServer"

ensure_llmserver_user() {
  local uid=""

  # --- Group ---
  # Read existing GID if the group record exists and is valid
  uid=$(dscl . -read "/Groups/$LLMSERVER_USER" PrimaryGroupID 2>/dev/null | grep -oE '[0-9]+' | head -1) || true

  if [[ -n "$uid" ]]; then
    echo "[SKIP] $LLMSERVER_USER group already exists (GID $uid)"
  else
    # Group either doesn't exist or has a broken/empty GID. Remove it if present
    # so we can recreate it cleanly, then pick a free ID.
    if dscl . -list "/Groups/$LLMSERVER_USER" &>/dev/null 2>&1; then
      echo "[SET]  Removing broken $LLMSERVER_USER group (no valid GID)"
      sudo dscl . -delete "/Groups/$LLMSERVER_USER" 2>/dev/null || true
    fi

    local used_ids
    used_ids=$(dscl . -list /Groups PrimaryGroupID 2>/dev/null | awk '{print $2}')
    used_ids+=$'\n'$(dscl . -list /Users UniqueID 2>/dev/null | awk '{print $2}')
    for candidate in $(seq 400 499); do
      if ! printf '%s\n' "$used_ids" | grep -qx "$candidate"; then
        uid="$candidate"; break
      fi
    done
    if [[ -z "$uid" ]]; then
      echo "[FAIL] No available ID in range 400-499 for $LLMSERVER_USER"
      exit 1
    fi
    sudo dscl . -create "/Groups/$LLMSERVER_USER"
    sudo dscl . -create "/Groups/$LLMSERVER_USER" PrimaryGroupID "$uid"
    sudo dscl . -create "/Groups/$LLMSERVER_USER" RealName "LLM Server"
    sudo dscl . -create "/Groups/$LLMSERVER_USER" Password "*"
    echo "[SET]  $LLMSERVER_USER group created (GID $uid)"
  fi

  # --- User ---
  # Check for a valid existing user record
  local existing_uid
  existing_uid=$(dscl . -read "/Users/$LLMSERVER_USER" UniqueID 2>/dev/null | grep -oE '[0-9]+' | head -1) || true

  if [[ -n "$existing_uid" ]]; then
    echo "[SKIP] $LLMSERVER_USER user already exists (UID $existing_uid)"
  else
    # User either doesn't exist or has a broken/empty UID. Remove if present.
    if dscl . -list "/Users/$LLMSERVER_USER" &>/dev/null 2>&1; then
      echo "[SET]  Removing broken $LLMSERVER_USER user (no valid UID)"
      sudo dscl . -delete "/Users/$LLMSERVER_USER" 2>/dev/null || true
    fi
    sudo dscl . -create "/Users/$LLMSERVER_USER"
    sudo dscl . -create "/Users/$LLMSERVER_USER" UserShell /usr/bin/false
    sudo dscl . -create "/Users/$LLMSERVER_USER" RealName "LLM Server"
    sudo dscl . -create "/Users/$LLMSERVER_USER" UniqueID "$uid"
    sudo dscl . -create "/Users/$LLMSERVER_USER" PrimaryGroupID "$uid"
    sudo dscl . -create "/Users/$LLMSERVER_USER" NFSHomeDirectory "$LLMSERVER_HOME"
    sudo dscl . -create "/Users/$LLMSERVER_USER" Password "*"
    sudo dscl . -create "/Users/$LLMSERVER_USER" IsHidden 1
    sudo dscacheutil -flushcache 2>/dev/null || true
    echo "[SET]  $LLMSERVER_USER user created (UID $uid)"
  fi

  # Home directory — always ensure correct ownership
  sudo mkdir -p "$LLMSERVER_HOME"
  sudo chown "${LLMSERVER_USER}:${LLMSERVER_USER}" "$LLMSERVER_HOME"
  sudo chmod 750 "$LLMSERVER_HOME"
  echo "[OK]   $LLMSERVER_HOME owned by $LLMSERVER_USER"
}

echo "========================================"
echo "Tool installation plan (from config.json)"
echo "========================================"
echo "  Ollama:    $(echo "$CONFIG" | jq -r '.tools.ollama.enabled')"
echo "  Rapid-MLX: $(echo "$CONFIG" | jq -r '.tools.rapid_mlx.enabled')"
echo "  mlx-lm:    $(echo "$CONFIG" | jq -r '.tools.mlx_lm.enabled')"
echo "  Infinity:  $(echo "$CONFIG" | jq -r '.tools.infinity.enabled')"
echo "  Exo:       $(echo "$CONFIG" | jq -r '.tools.exo.enabled')"
echo ""

ensure_llmserver_user
echo ""

# ===========================================================================
# §2.2  OLLAMA
# ===========================================================================
if [[ "$(echo "$CONFIG" | jq -r '.tools.ollama.enabled')" == "true" ]]; then
  echo "========================================"
  echo "Installing Ollama"
  echo "========================================"

  # --- Install ---
  if command -v ollama &>/dev/null; then
    echo "[SKIP] Ollama already installed: $(ollama --version 2>/dev/null || echo unknown)"
  else
    echo "[INFO] Downloading and installing Ollama..."
    curl -fsSL https://ollama.com/install.sh | sh
    echo "[OK]   Ollama installed"
  fi

  OLLAMA_BIN=$(command -v ollama || echo "/usr/local/bin/ollama")

  # Verify ARM64
  if file "$OLLAMA_BIN" 2>/dev/null | grep -q "arm64\|universal"; then
    echo "[OK]   $OLLAMA_BIN is ARM64-compatible"
  else
    echo "[WARN] Could not verify ARM64 support for $OLLAMA_BIN"
  fi

  # Stop all user-level Ollama instances before installing the system daemon.
  # The Ollama.app holds port 11434 and will conflict if left running.

  # 1. Remove login item so it doesn't restart on next login
  osascript -e 'tell application "System Events" to delete login item "Ollama"' \
    2>/dev/null || true

  # 2. Bootout the existing system daemon first — KeepAlive means launchd will
  #    immediately restart the process if we kill it without booting out first
  OLLAMA_PLIST_EARLY="/Library/LaunchDaemons/com.ollama.server.plist"
  if [[ -f "$OLLAMA_PLIST_EARLY" ]]; then
    sudo launchctl bootout system "$OLLAMA_PLIST_EARLY" 2>/dev/null || true
    sleep 1
  fi

  # 3. Gracefully quit the menu bar app, then force-kill any remaining process
  osascript -e 'quit app "Ollama"' 2>/dev/null || true
  sleep 1
  killall Ollama 2>/dev/null || true
  killall ollama 2>/dev/null || true

  # 4. Remove any LaunchAgent the Ollama installer dropped in the user's home
  for agent in \
      "$HOME/Library/LaunchAgents/com.ollama.ollama.plist" \
      "$HOME/Library/LaunchAgents/ollama.plist"; do
    if [[ -f "$agent" ]]; then
      launchctl bootout "gui/$(id -u)" "$agent" 2>/dev/null || true
      rm -f "$agent"
      echo "[SET]  Removed user LaunchAgent: $agent"
    fi
  done

  # 5. Stop any Homebrew-managed Ollama service — it also holds port 11434
  if brew services list 2>/dev/null | grep -q "^ollama"; then
    brew services stop ollama 2>/dev/null || true
    echo "[SET]  Stopped Homebrew Ollama service (our LaunchDaemon takes over)"
  fi

  # 6. Wait for port 11434 to be free before loading daemon
  for i in $(seq 1 10); do
    if ! lsof -iTCP:11434 -sTCP:LISTEN &>/dev/null 2>&1; then
      break
    fi
    echo "[INFO] Waiting for port 11434 to clear... ($i/10)"
    sleep 2
  done
  if lsof -iTCP:11434 -sTCP:LISTEN &>/dev/null 2>&1; then
    echo "[WARN] Port 11434 still in use — daemon may fail to bind"
    lsof -iTCP:11434 -sTCP:LISTEN 2>/dev/null || true
  fi

  # --- Models directory ---
  OLLAMA_MODELS_DIR=$(echo "$CONFIG" | jq -r '.tools.ollama.models_dir')
  sudo mkdir -p "$OLLAMA_MODELS_DIR"
  sudo chown -R "${LLMSERVER_USER}:${LLMSERVER_USER}" "$(dirname "$OLLAMA_MODELS_DIR")"
  sudo mdutil -i off "$OLLAMA_MODELS_DIR" 2>/dev/null || true
  sudo mdutil -E  "$OLLAMA_MODELS_DIR" 2>/dev/null || true
  echo "[OK]   Models dir: $OLLAMA_MODELS_DIR (Spotlight excluded)"

  # Log directory
  sudo mkdir -p /var/log/ollama
  sudo chown "${LLMSERVER_USER}:${LLMSERVER_USER}" /var/log/ollama

  # --- RAM-based auto-tuning ---
  if   [[ $RAM_GB -le 16 ]]; then MAX_LOADED=1; NUM_PAR=1; MAX_CTX=8192
  elif [[ $RAM_GB -le 24 ]]; then MAX_LOADED=2; NUM_PAR=2; MAX_CTX=16384
  elif [[ $RAM_GB -le 32 ]]; then MAX_LOADED=2; NUM_PAR=3; MAX_CTX=32768
  elif [[ $RAM_GB -le 64 ]]; then MAX_LOADED=3; NUM_PAR=4; MAX_CTX=32768
  else                             MAX_LOADED=4; NUM_PAR=8; MAX_CTX=""
  fi
  # MAX_CTX="" means: don't set OLLAMA_MAX_CONTEXT — let Ollama use each model's
  # native context window. Only cap on RAM-constrained tiers to prevent OOM.

  # Config overrides take precedence over auto-tune
  CFG_MAX_LOADED=$(echo "$CONFIG" | jq -r '.tools.ollama.max_loaded_models // empty')
  CFG_NUM_PAR=$(echo "$CONFIG"    | jq -r '.tools.ollama.num_parallel // empty')
  CFG_MAX_CTX=$(echo "$CONFIG"    | jq -r '.tools.ollama.max_context // empty')
  [[ -n "$CFG_MAX_LOADED" ]] && MAX_LOADED=$CFG_MAX_LOADED
  [[ -n "$CFG_NUM_PAR"    ]] && NUM_PAR=$CFG_NUM_PAR
  [[ -n "$CFG_MAX_CTX"    ]] && MAX_CTX=$CFG_MAX_CTX

  OLLAMA_HOST=$(echo "$CONFIG"    | jq -r '.tools.ollama.host')
  [[ "$LOCALHOST_ONLY" == "true" ]] && OLLAMA_HOST="127.0.0.1:11434"
  KEEP_ALIVE=$(echo "$CONFIG"     | jq -r '.tools.ollama.keep_alive')
  GPU_PCT=$(echo "$CONFIG"        | jq -r '.tools.ollama.gpu_percent')
  FLASH_ATTN=$(echo "$CONFIG"     | jq -r '.tools.ollama.flash_attention | if . then "1" else "0" end')

  echo "[INFO] Ollama tuning: RAM=${RAM_GB}GB → MAX_LOADED=$MAX_LOADED NUM_PAR=$NUM_PAR MAX_CTX=${MAX_CTX:-model-native}"

  # --- LaunchDaemon plist ---
  OLLAMA_PLIST="/Library/LaunchDaemons/com.ollama.server.plist"

  sudo tee "$OLLAMA_PLIST" > /dev/null <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>com.ollama.server</string>

  <key>ProgramArguments</key>
  <array>
    <string>${OLLAMA_BIN}</string>
    <string>serve</string>
  </array>

  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>

  <key>StandardOutPath</key><string>/var/log/ollama/stdout.log</string>
  <key>StandardErrorPath</key><string>/var/log/ollama/stderr.log</string>

  <key>EnvironmentVariables</key>
  <dict>
    <!-- HOME is required: Ollama panics with 'panic: \$HOME is not defined' without it -->
    <key>HOME</key><string>${LLMSERVER_HOME}</string>
    <key>PATH</key><string>/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin</string>
    <key>OLLAMA_MODELS</key><string>${OLLAMA_MODELS_DIR}</string>
    <key>OLLAMA_HOST</key><string>${OLLAMA_HOST}</string>
    <key>OLLAMA_KEEP_ALIVE</key><string>${KEEP_ALIVE}</string>
    <key>OLLAMA_NUM_PARALLEL</key><string>${NUM_PAR}</string>
    <key>OLLAMA_MAX_LOADED_MODELS</key><string>${MAX_LOADED}</string>
    ${MAX_CTX:+<key>OLLAMA_MAX_CONTEXT</key><string>${MAX_CTX}</string>}
    <key>OLLAMA_FLASH_ATTENTION</key><string>${FLASH_ATTN}</string>
    <key>OLLAMA_NUM_GPU</key><string>1</string>
    <key>OLLAMA_GPU_PERCENT</key><string>${GPU_PCT}</string>
    <key>OLLAMA_ORIGINS</key><string>*</string>
  </dict>

  <key>WorkingDirectory</key><string>/tmp</string>
  <key>UserName</key><string>${LLMSERVER_USER}</string>
</dict>
</plist>
PLIST

  sudo chown root:wheel "$OLLAMA_PLIST"
  sudo chmod 644 "$OLLAMA_PLIST"
  load_daemon "$OLLAMA_PLIST"
  sleep 3

  check_endpoint "Ollama" "http://localhost:11434/api/tags" "models" 10 || true
  echo ""
fi

# ===========================================================================
# §2.3  RAPID-MLX
# ===========================================================================
if [[ "$(echo "$CONFIG" | jq -r '.tools.rapid_mlx.enabled')" == "true" ]]; then
  echo "========================================"
  echo "Installing Rapid-MLX"
  echo "========================================"

  if command -v rapid-mlx &>/dev/null; then
    echo "[SKIP] Rapid-MLX already installed: $(rapid-mlx --version 2>/dev/null || echo unknown)"
  else
    echo "[INFO] Installing Rapid-MLX via Homebrew..."
    brew tap homebrew/core --force 2>/dev/null || true
    if brew install raullenchai/rapid-mlx/rapid-mlx 2>/dev/null; then
      echo "[OK]   Rapid-MLX installed via Homebrew"
    else
      echo "[INFO] Homebrew install failed — trying pip3..."
      PY_MINOR=$(python3 --version 2>/dev/null | awk '{print $2}' | cut -d. -f2)
      if [[ "${PY_MINOR:-0}" -lt 10 ]]; then
        echo "ERROR: Python 3.10+ required for Rapid-MLX. Run: brew install python@3.12"
        exit 1
      fi
      pip3 install rapid-mlx --break-system-packages
      echo "[OK]   Rapid-MLX installed via pip3"
    fi
  fi

  # Install any extras specified in config (vision, audio, embeddings, etc.)
  while IFS= read -r extra; do
    [[ -z "$extra" ]] && continue
    pip3 install "rapid-mlx[${extra}]" --break-system-packages
    echo "[OK]   rapid-mlx[$extra] installed"
  done < <(echo "$CONFIG" | jq -r '.tools.rapid_mlx.extras[]?' 2>/dev/null || true)

  # Run built-in self-diagnostic
  rapid-mlx doctor 2>/dev/null || echo "[WARN] rapid-mlx doctor reported issues — check output above"

  # Model cache directory
  RMLX_CACHE="/Library/RapidMLX/cache"
  if [[ -f /tmp/mac-llm-precheck.json ]] && \
     [[ "$(jq -r '.storage.volume_configured // false' /tmp/mac-llm-precheck.json)" == "true" ]]; then
    VOL_ROOT=$(jq -r '.storage.model_root' /tmp/mac-llm-precheck.json)
    RMLX_CACHE="${VOL_ROOT}/rapid-mlx"
  fi
  sudo mkdir -p "$RMLX_CACHE"
  sudo chown -R "${LLMSERVER_USER}:${LLMSERVER_USER}" "$RMLX_CACHE"
  sudo mdutil -i off "$RMLX_CACHE" 2>/dev/null || true
  echo "[OK]   Rapid-MLX cache: $RMLX_CACHE"

  # Log directory
  sudo mkdir -p /var/log/rapid-mlx
  sudo chown "${LLMSERVER_USER}:${LLMSERVER_USER}" /var/log/rapid-mlx

  # Resolve binary path (Homebrew vs pip install to different locations)
  RMLX_BIN=$(command -v rapid-mlx || echo "/opt/homebrew/bin/rapid-mlx")
  RMLX_HOST=$(echo "$CONFIG"   | jq -r '.tools.rapid_mlx.host')
  [[ "$LOCALHOST_ONLY" == "true" ]] && RMLX_HOST="127.0.0.1"
  RMLX_PORT=$(echo "$CONFIG"   | jq -r '.tools.rapid_mlx.port')
  RMLX_MODEL=$(echo "$CONFIG"  | jq -r '.tools.rapid_mlx.model')
  RMLX_PREFILL=$(echo "$CONFIG"| jq -r '.tools.rapid_mlx.prefill_step_size')
  RMLX_NO_THINK=$(echo "$CONFIG"| jq -r '.tools.rapid_mlx.no_thinking')

  NO_THINKING_ARG=""
  [[ "$RMLX_NO_THINK" == "true" ]] && NO_THINKING_ARG="    <string>--no-thinking</string>"

  RMLX_PLIST="/Library/LaunchDaemons/com.rapid-mlx.server.plist"
  sudo tee "$RMLX_PLIST" > /dev/null <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>com.rapid-mlx.server</string>
  <key>ProgramArguments</key>
  <array>
    <string>${RMLX_BIN}</string>
    <string>serve</string>
    <string>${RMLX_MODEL}</string>
    <string>--host</string><string>${RMLX_HOST}</string>
    <string>--port</string><string>${RMLX_PORT}</string>
    <string>--prefill-step-size</string><string>${RMLX_PREFILL}</string>
${NO_THINKING_ARG}
  </array>
  <key>EnvironmentVariables</key>
  <dict>
    <key>HOME</key><string>${LLMSERVER_HOME}</string>
    <key>PATH</key><string>/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin</string>
    <key>RAPID_MLX_CACHE_DIR</key><string>${RMLX_CACHE}</string>
  </dict>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>StandardOutPath</key><string>/var/log/rapid-mlx/stdout.log</string>
  <key>StandardErrorPath</key><string>/var/log/rapid-mlx/stderr.log</string>
  <key>WorkingDirectory</key><string>/tmp</string>
  <key>UserName</key><string>${LLMSERVER_USER}</string>
</dict>
</plist>
PLIST

  sudo chown root:wheel "$RMLX_PLIST"
  sudo chmod 644 "$RMLX_PLIST"
  load_daemon "$RMLX_PLIST"
  # First start downloads model — API won't respond until complete (can take minutes)
  sleep 5
  echo "[NOTE] First start downloads model '$RMLX_MODEL' if not cached — API unavailable until done"
  echo "       Monitor: tail -f /var/log/rapid-mlx/stdout.log"
  check_endpoint "Rapid-MLX" "http://localhost:${RMLX_PORT}/v1/models" "." 10 || true
  echo ""
fi

# ===========================================================================
# §2.4  MLX-LM
# ===========================================================================
if [[ "$(echo "$CONFIG" | jq -r '.tools.mlx_lm.enabled')" == "true" ]]; then
  echo "========================================"
  echo "Installing mlx-lm"
  echo "========================================"

  if python3 -c "import mlx_lm" 2>/dev/null; then
    echo "[SKIP] mlx-lm already installed"
  else
    echo "[INFO] Installing mlx-lm via pip3..."
    pip3 install mlx-lm --break-system-packages
    echo "[OK]   mlx-lm installed"
  fi

  MLX_HOST=$(echo "$CONFIG"       | jq -r '.tools.mlx_lm.host')
  [[ "$LOCALHOST_ONLY" == "true" ]] && MLX_HOST="127.0.0.1"
  MLX_PORT=$(echo "$CONFIG"       | jq -r '.tools.mlx_lm.port')
  MLX_MODEL=$(echo "$CONFIG"      | jq -r '.tools.mlx_lm.default_model')
  MLX_MODEL_PATH=$(echo "$CONFIG" | jq -r '.tools.mlx_lm.model_path')

  # Find the python3 that has mlx_lm installed
  MLX_PYTHON=$(python3 -c "import sys; print(sys.executable)")

  sudo mkdir -p /var/log/mlx-lm "$MLX_MODEL_PATH"
  sudo chown "${LLMSERVER_USER}:${LLMSERVER_USER}" /var/log/mlx-lm
  sudo chown -R "${LLMSERVER_USER}:${LLMSERVER_USER}" "$MLX_MODEL_PATH"

  MLX_PLIST="/Library/LaunchDaemons/com.mlx-lm.server.plist"
  sudo tee "$MLX_PLIST" > /dev/null <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>com.mlx-lm.server</string>
  <key>ProgramArguments</key>
  <array>
    <string>${MLX_PYTHON}</string>
    <string>-m</string>
    <string>mlx_lm.server</string>
    <string>--host</string><string>${MLX_HOST}</string>
    <string>--port</string><string>${MLX_PORT}</string>
    <string>--model</string><string>${MLX_MODEL}</string>
  </array>
  <key>EnvironmentVariables</key>
  <dict>
    <key>HOME</key><string>${LLMSERVER_HOME}</string>
    <key>PATH</key><string>/usr/local/bin:/usr/bin:/bin</string>
    <key>TRANSFORMERS_CACHE</key><string>${MLX_MODEL_PATH}</string>
  </dict>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>StandardOutPath</key><string>/var/log/mlx-lm/stdout.log</string>
  <key>StandardErrorPath</key><string>/var/log/mlx-lm/stderr.log</string>
  <key>WorkingDirectory</key><string>/tmp</string>
  <key>UserName</key><string>${LLMSERVER_USER}</string>
</dict>
</plist>
PLIST

  sudo chown root:wheel "$MLX_PLIST"
  sudo chmod 644 "$MLX_PLIST"

  if [[ -n "$MLX_MODEL" ]]; then
    load_daemon "$MLX_PLIST"
    sleep 3
    check_endpoint "mlx-lm" "http://localhost:${MLX_PORT}/v1/models" "." 10 || true
  else
    echo "[SKIP] mlx-lm plist written but NOT started"
    echo "       Set tools.mlx_lm.default_model in config.json then run:"
    echo "       sudo launchctl bootstrap system $MLX_PLIST"
  fi
  echo ""
fi

# ===========================================================================
# §2.5  INFINITY (embedding server)
# ===========================================================================
if [[ "$(echo "$CONFIG" | jq -r '.tools.infinity.enabled')" == "true" ]]; then
  echo "========================================"
  echo "Installing Infinity Embedding Server"
  echo "========================================"

  if python3 -c "import infinity_emb" 2>/dev/null; then
    echo "[SKIP] Infinity already installed"
  else
    echo "[INFO] Installing Infinity via pip3..."
    pip3 install "infinity-emb[torch,optimum]" --break-system-packages
    echo "[OK]   Infinity installed"
  fi

  INF_HOST=$(echo "$CONFIG"  | jq -r '.tools.infinity.host')
  [[ "$LOCALHOST_ONLY" == "true" ]] && INF_HOST="127.0.0.1"
  INF_PORT=$(echo "$CONFIG"  | jq -r '.tools.infinity.port')
  INF_MODEL=$(echo "$CONFIG" | jq -r '.tools.infinity.model')
  INF_ENGINE=$(echo "$CONFIG"| jq -r '.tools.infinity.engine')
  INF_PYTHON=$(python3 -c "import sys; print(sys.executable)")

  sudo mkdir -p /var/log/infinity
  sudo chown "${LLMSERVER_USER}:${LLMSERVER_USER}" /var/log/infinity

  INF_PLIST="/Library/LaunchDaemons/com.infinity.server.plist"
  sudo tee "$INF_PLIST" > /dev/null <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>com.infinity.server</string>
  <key>ProgramArguments</key>
  <array>
    <string>${INF_PYTHON}</string>
    <string>-m</string>
    <string>infinity_emb</string>
    <string>v2</string>
    <string>--host</string><string>${INF_HOST}</string>
    <string>--port</string><string>${INF_PORT}</string>
    <string>--model-id</string><string>${INF_MODEL}</string>
    <string>--engine</string><string>${INF_ENGINE}</string>
    <!-- --device mps is required for GPU acceleration on Apple Silicon -->
    <string>--device</string><string>mps</string>
  </array>
  <key>EnvironmentVariables</key>
  <dict>
    <key>HOME</key><string>${LLMSERVER_HOME}</string>
    <key>PATH</key><string>/usr/local/bin:/usr/bin:/bin</string>
  </dict>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>StandardOutPath</key><string>/var/log/infinity/stdout.log</string>
  <key>StandardErrorPath</key><string>/var/log/infinity/stderr.log</string>
  <key>WorkingDirectory</key><string>/tmp</string>
  <key>UserName</key><string>${LLMSERVER_USER}</string>
</dict>
</plist>
PLIST

  sudo chown root:wheel "$INF_PLIST"
  sudo chmod 644 "$INF_PLIST"
  load_daemon "$INF_PLIST"
  sleep 3

  check_endpoint "Infinity" "http://localhost:${INF_PORT}/health" "." 10 || true
  echo ""
  echo "  Endpoint reference:"
  echo "    Embeddings: POST http://localhost:${INF_PORT}/v1/embeddings"
  echo "    Reranking:  POST http://localhost:${INF_PORT}/v1/rerank"
  echo "    Models:     GET  http://localhost:${INF_PORT}/v1/models"
  echo ""
fi

# ===========================================================================
# §2.6  EXO (distributed inference)
# ===========================================================================
if [[ "$(echo "$CONFIG" | jq -r '.tools.exo.enabled')" == "true" ]]; then
  echo "========================================"
  echo "Installing Exo (Distributed Inference)"
  echo "========================================"

  if command -v exo &>/dev/null; then
    echo "[SKIP] Exo already installed: $(exo --version 2>/dev/null || echo unknown)"
  else
    echo "[INFO] Installing Exo..."
    if brew install exo 2>/dev/null; then
      echo "[OK]   Exo installed via Homebrew"
    else
      pip3 install exo --break-system-packages
      echo "[OK]   Exo installed via pip3"
    fi
  fi

  EXO_PORT=$(echo "$CONFIG"      | jq -r '.tools.exo.chatgpt_api_port')
  EXO_DISCOVERY=$(echo "$CONFIG" | jq -r '.tools.exo.discovery_module')
  EXO_BIN=$(command -v exo || echo "/opt/homebrew/bin/exo")

  # Exo runs as LaunchAgent (user-level) — it needs user context for
  # mDNS/Bonjour/Tailscale peer discovery. Cannot run as LaunchDaemon.
  EXO_PLIST="$HOME/Library/LaunchAgents/com.exo.node.plist"
  mkdir -p "$HOME/Library/LaunchAgents"

  tee "$EXO_PLIST" > /dev/null <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>com.exo.node</string>
  <key>ProgramArguments</key>
  <array>
    <string>${EXO_BIN}</string>
    <string>--chatgpt-api-port</string><string>${EXO_PORT}</string>
    <string>--discovery-module</string><string>${EXO_DISCOVERY}</string>
  </array>
  <key>EnvironmentVariables</key>
  <dict>
    <key>HOME</key><string>${HOME}</string>
    <key>PATH</key><string>/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin</string>
  </dict>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>StandardOutPath</key><string>/tmp/exo-stdout.log</string>
  <key>StandardErrorPath</key><string>/tmp/exo-stderr.log</string>
</dict>
</plist>
PLIST

  # Bootstrap as user-level LaunchAgent (no sudo)
  launchctl bootout  "gui/$(id -u)/com.exo.node" 2>/dev/null || true
  launchctl bootstrap "gui/$(id -u)" "$EXO_PLIST"
  echo "[OK]   Exo LaunchAgent installed"
  echo ""
  echo "  [NOTICE] Exo requires auto-login for true headless operation."
  echo "           Without it, Exo only starts after a user logs in interactively."
  echo "           Configure: sudo sysadminctl -autologin set -userName <user> -password <pw>"
  echo ""
  echo "  [NOTICE] For Tailscale discovery: ensure tailscaled is running on all nodes."
  echo "  API endpoint: http://$(hostname):${EXO_PORT}/v1/chat/completions"
  echo ""
fi

# ===========================================================================
# Summary
# ===========================================================================
echo "========================================"
echo "install-tools.sh complete"
echo "========================================"
echo ""
echo "Installed services and their log locations:"
[[ "$(echo "$CONFIG" | jq -r '.tools.ollama.enabled')"    == "true" ]] && echo "  Ollama:    /var/log/ollama/{stdout,stderr}.log"
[[ "$(echo "$CONFIG" | jq -r '.tools.rapid_mlx.enabled')" == "true" ]] && echo "  Rapid-MLX: /var/log/rapid-mlx/{stdout,stderr}.log"
[[ "$(echo "$CONFIG" | jq -r '.tools.mlx_lm.enabled')"   == "true" ]] && echo "  mlx-lm:    /var/log/mlx-lm/{stdout,stderr}.log"
[[ "$(echo "$CONFIG" | jq -r '.tools.infinity.enabled')"  == "true" ]] && echo "  Infinity:  /var/log/infinity/{stdout,stderr}.log"
[[ "$(echo "$CONFIG" | jq -r '.tools.exo.enabled')"       == "true" ]] && echo "  Exo:       /tmp/exo-{stdout,stderr}.log"
echo ""
echo "Next step: ./verify.sh"
echo "Log written to: $LOG_FILE"
