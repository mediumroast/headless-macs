# Mac LLM Optimizer — Claude Code Build Specification

**Target:** Apple Silicon Mac (any model: Mini, MacBook Pro/Air, Studio, Pro)  
**OS:** macOS 26 Tahoe (primary), macOS 15 Sequoia (secondary)  
**Purpose:** Idempotent toolset to configure a Mac as a production-grade LLM inference node  
**Serving targets:** Ollama, MLX / mlx-lm, Infinity (embedding server), Exo (distributed inference)  
**Generated:** June 2026

---

## TL;DR for the Agent

Build five bash scripts plus one JSON configuration file. Every script is idempotent, logs to `/var/log/mac-llm-setup/`, detects Apple Silicon vs Intel and aborts on Intel, and checks macOS version to apply version-specific workarounds. The scripts are:

| Script | Purpose | Run Order |
|--------|---------|-----------|
| `precheck.sh` | Read-only audit: hardware, storage, SIP, FileVault, volumes — outputs what's possible | 1st — before anything else |
| `setup.sh` | System baseline: pmset, SIP check, network tuning, service suppression | 2nd |
| `install-tools.sh` | Install and configure serving tools (Ollama, mlx-lm, Infinity, Exo) | 3rd |
| `verify.sh` | Pass/fail health report for the whole stack | Any time after install |
| `restore.sh` | Undo all changes using a pre-change snapshot | Recovery only |

One shared config file `config.json` drives all tool selection and tuning parameters. The agent generates all six files.

**`precheck.sh` is always run first and requires no sudo.** It makes zero changes. Its output tells the operator what hardware they have, what constraints apply, which tools are viable, and whether an external volume is present and usable for model storage — before any irreversible steps are taken.

---

## 0. Shared Requirements

### 0.1 Guard Clauses — Every Script Must Include These

```bash
#!/usr/bin/env bash
set -euo pipefail

# --- Guard: Apple Silicon only ---
ARCH=$(uname -m)
if [[ "$ARCH" != "arm64" ]]; then
  echo "ERROR: This toolset requires Apple Silicon (arm64). Detected: $ARCH"
  exit 1
fi

# --- Guard: macOS only ---
if [[ "$(uname -s)" != "Darwin" ]]; then
  echo "ERROR: macOS required."
  exit 1
fi

# --- Detect macOS version ---
OS_VERSION=$(sw_vers -productVersion)
OS_MAJOR=$(echo "$OS_VERSION" | cut -d. -f1)
# macOS 26 = Tahoe, 15 = Sequoia, 14 = Sonoma

# --- Detect Mac model ---
HW_MODEL=$(sysctl -n hw.model 2>/dev/null || echo "unknown")
RAM_GB=$(( $(sysctl -n hw.memsize) / 1024 / 1024 / 1024 ))

# --- Logging ---
LOG_DIR="/var/log/mac-llm-setup"
sudo mkdir -p "$LOG_DIR"
LOG_FILE="$LOG_DIR/$(basename "$0" .sh)-$(date +%Y%m%d-%H%M%S).log"
exec > >(tee -a "$LOG_FILE") 2>&1
echo "=== $(basename "$0") started at $(date) ==="
echo "Hardware: $HW_MODEL | RAM: ${RAM_GB}GB | macOS: $OS_VERSION"
```

### 0.2 Idempotency Pattern

Every configuration change must check current state before applying. Pattern:

```bash
# Example: pmset idempotency
current=$(pmset -g | grep "^  sleep" | awk '{print $2}')
if [[ "$current" != "0" ]]; then
  sudo pmset -a sleep 0
  echo "[SET] pmset sleep 0"
else
  echo "[SKIP] pmset sleep already 0"
fi
```

### 0.3 SIP Check Pattern

```bash
SIP_STATUS=$(csrutil status 2>/dev/null || echo "unknown")
SIP_ENABLED=true
if echo "$SIP_STATUS" | grep -q "disabled"; then
  SIP_ENABLED=false
fi

if [[ "$SIP_ENABLED" == "true" ]]; then
  echo "WARNING: SIP is enabled. Some service suppression changes will not survive reboots."
  echo "To disable SIP: boot to Recovery Mode → Terminal → 'csrutil disable'"
  echo "Continuing with non-SIP-required changes only..."
fi
```

### 0.4 Config File — `config.json`

The agent must generate this file. All scripts source it via `jq`. Defaults shown:

```json
{
  "tools": {
    "ollama": {
      "enabled": true,
      "host": "0.0.0.0:11434",
      "models_dir": "/Library/Ollama/models",
      "keep_alive": 86400,
      "num_parallel": 4,
      "max_loaded_models": 3,
      "max_context": 32768,
      "flash_attention": true,
      "gpu_percent": 80
    },
    "rapid_mlx": {
      "enabled": false,
      "host": "0.0.0.0",
      "port": 8000,
      "model": "qwen3.5-4b",
      "prefill_step_size": 8192,
      "no_thinking": false,
      "extras": []
    },
    "mlx_lm": {
      "enabled": false,
      "host": "0.0.0.0",
      "port": 8080,
      "model_path": "/Library/MLX/models",
      "default_model": ""
    },
    "infinity": {
      "enabled": false,
      "host": "0.0.0.0",
      "port": 7997,
      "model": "michaelfeil/bge-small-en-v1.5",
      "engine": "torch"
    },
    "exo": {
      "enabled": false,
      "discovery_module": "tailscale",
      "chatgpt_api_port": 52415
    }
  },
  "storage": {
    "use_external_volume": false,
    "volume_label": "LLMStorage",
    "volume_mount_point": "",
    "models_subdir": "models",
    "auto_detect_volume": true,
    "min_free_gb": 100,
    "symlink_internal_paths": true
  },
  "system": {
    "disable_spotlight": true,
    "disable_software_update": true,
    "disable_time_machine": true,
    "disable_icloud": true,
    "disable_airdrop_handoff": true,
    "disable_notifications": true,
    "disable_telemetry": true,
    "disable_siri": true,
    "disable_media_services": true,
    "network_tuning": true,
    "power_mode": 2
  }
}
```

The agent should parse `config.json` with `jq` at the top of each script. Install `jq` if missing:

```bash
if ! command -v jq &>/dev/null; then
  if command -v brew &>/dev/null; then
    brew install jq
  else
    echo "ERROR: jq required. Install Homebrew first: https://brew.sh"
    exit 1
  fi
fi
CONFIG=$(cat "$(dirname "$0")/config.json")
```

---

## 1. `setup.sh` — System Baseline

### 1.1 Power Management

Apply every `pmset` setting. Use the idempotency pattern from §0.2.

```bash
# --- Core sleep prevention ---
sudo pmset -a sleep 0
sudo pmset -a disablesleep 1       # macOS 26 Tahoe primary mechanism
sudo pmset -a disksleep 0
sudo pmset -a standby 0
sudo pmset -a autopoweroff 0
sudo pmset -a powernap 0
sudo pmset -a networkoversleep 0

# --- Remote access ---
sudo pmset -a womp 1               # Wake on magic packet
sudo pmset -a tcpkeepalive 1       # Keep SSH alive during long inference

# --- Display (can sleep — headless) ---
sudo pmset -a displaysleep 10

# --- Performance mode ---
# Read from config.json; default is 2 (High Performance)
POWER_MODE=$(echo "$CONFIG" | jq -r '.system.power_mode')
sudo pmset -a powermode "$POWER_MODE"
```

**MacBook detection and clamshell handling:**

```bash
# Detect if this is a laptop
if echo "$HW_MODEL" | grep -qiE "MacBook"; then
  echo "NOTICE: MacBook detected. Lid-close behavior requires a physical HDMI dummy plug"
  echo "        OR run: sudo pmset -a lidwake 0  (thermal risk if vents blocked)"
  echo "        Recommended: purchase an HDMI/USB-C dummy plug before going headless."
  # Set battery + AC explicitly for MacBooks
  sudo pmset -b sleep 0
  sudo pmset -c sleep 0
fi
```

**Caffeinate LaunchDaemon** (belt-and-suspenders for Sequoia sleep regression):

```bash
CAFFEINATE_PLIST="/Library/LaunchDaemons/com.llm-server.caffeinate.plist"
if [[ ! -f "$CAFFEINATE_PLIST" ]]; then
  sudo tee "$CAFFEINATE_PLIST" > /dev/null <<'EOF'
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
EOF
  sudo chown root:wheel "$CAFFEINATE_PLIST"
  sudo chmod 644 "$CAFFEINATE_PLIST"
  sudo launchctl bootstrap system "$CAFFEINATE_PLIST"
  echo "[SET] caffeinate LaunchDaemon installed"
fi
```

### 1.2 Network Stack Tuning

Only apply if `config.json` has `network_tuning: true`. Apply via `sysctl.conf` for persistence:

```bash
SYSCTL_CONF="/etc/sysctl.conf"

apply_sysctl() {
  local key="$1" value="$2"
  if ! grep -q "^${key}=" "$SYSCTL_CONF" 2>/dev/null; then
    echo "${key}=${value}" | sudo tee -a "$SYSCTL_CONF" > /dev/null
    sudo sysctl -w "${key}=${value}"
    echo "[SET] sysctl ${key}=${value}"
  else
    echo "[SKIP] sysctl ${key} already configured"
  fi
}

apply_sysctl "net.inet.tcp.sendspace"    "1048576"
apply_sysctl "net.inet.tcp.recvspace"    "1048576"
apply_sysctl "kern.ipc.maxsockbuf"       "8388608"
apply_sysctl "net.inet.tcp.autorcvbufmax" "8388608"
apply_sysctl "net.inet.tcp.autosndbufmax" "8388608"
apply_sysctl "kern.ipc.somaxconn"        "2048"

# IMPORTANT: Do NOT add net.inet.tcp.rfc1323 — removed in El Capitan
# IMPORTANT: Do NOT add serverperfmode — Intel only, breaks on Apple Silicon
```

### 1.3 Service Suppression

**Pre-change snapshot** (required for `restore.sh`):

```bash
SNAPSHOT_DIR="/var/log/mac-llm-setup/snapshots"
sudo mkdir -p "$SNAPSHOT_DIR"
SNAPSHOT="$SNAPSHOT_DIR/services-$(date +%Y%m%d).txt"
if [[ ! -f "$SNAPSHOT" ]]; then
  launchctl print-disabled system > "$SNAPSHOT"
  launchctl print-disabled "gui/$(id -u)" >> "$SNAPSHOT"
  echo "[SNAPSHOT] Pre-change service state saved to $SNAPSHOT"
fi
```

**Spotlight (always suppress — biggest inference competitor):**

```bash
sudo mdutil -a -i off
sudo mdutil -i off /Library/Ollama 2>/dev/null || true

# SIP-off path: fully unload
if [[ "$SIP_ENABLED" == "false" ]]; then
  sudo launchctl bootout system /System/Library/LaunchDaemons/com.apple.metadata.mds.plist 2>/dev/null || true
  sudo launchctl disable system/com.apple.metadata.mds 2>/dev/null || true
fi
```

**Telemetry services** (SIP required for persistence):

```bash
disable_service() {
  local domain="$1"
  if [[ "$SIP_ENABLED" == "false" ]]; then
    sudo launchctl disable "$domain" 2>/dev/null || true
    echo "[SET] disabled $domain"
  else
    echo "[SKIP-SIP] $domain (requires SIP off for persistence)"
  fi
}

# Telemetry
disable_service "system/com.apple.analyticsd"
disable_service "system/com.apple.diagnosticd"
disable_service "system/com.apple.spindump"
disable_service "system/com.apple.tailspind"
disable_service "system/com.apple.triald"
disable_service "gui/$(id -u)/com.apple.UsageTrackingAgent"

# Siri
disable_service "gui/$(id -u)/com.apple.Siri"
disable_service "gui/$(id -u)/com.apple.siriknowledged"
disable_service "gui/$(id -u)/com.apple.assistant_service"
disable_service "system/com.apple.siriinferenced"

# iCloud (conditional)
if [[ "$(echo "$CONFIG" | jq -r '.system.disable_icloud')" == "true" ]]; then
  disable_service "gui/$(id -u)/com.apple.cloudd"
  disable_service "gui/$(id -u)/com.apple.cloudpaird"
  disable_service "gui/$(id -u)/com.apple.iCloudNotificationAgent"
fi

# Media services
disable_service "gui/$(id -u)/com.apple.AMPArtworkAgent"
disable_service "gui/$(id -u)/com.apple.AMPLibraryAgent"
disable_service "gui/$(id -u)/com.apple.music.d"

# Biome / knowledge graph (heavy background ML — kills ANE bandwidth)
disable_service "system/com.apple.biomed"
disable_service "gui/$(id -u)/com.apple.biomesyncd"
disable_service "gui/$(id -u)/com.apple.contextstored"
disable_service "gui/$(id -u)/com.apple.knowledge-agent"
disable_service "gui/$(id -u)/com.apple.LiveLookup"
disable_service "gui/$(id -u)/com.apple.parsecd"
disable_service "gui/$(id -u)/com.apple.tipsd"
```

**Non-SIP defaults changes (no SIP requirement):**

```bash
# AirDrop / Handoff
defaults write com.apple.NetworkBrowser DisableAirDrop -bool true
defaults write com.apple.coreservices.useractivityd ActivityAdvertisingAllowed -bool false
defaults write com.apple.coreservices.useractivityd ActivityReceivingAllowed -bool false

# App Nap (throttles background processes)
defaults write NSGlobalDomain NSAppSleepDisabled -bool YES

# Notification Center
defaults write com.apple.notificationcenterui dndStart -int 0
defaults write com.apple.notificationcenterui dndEnd -int 1440

# Dock/Finder animations (reduce WindowServer load)
defaults write com.apple.dock launchanim -bool false
defaults write com.apple.dock expose-animation-duration -float 0
defaults write com.apple.finder DisableAllAnimations -bool true
killall Dock 2>/dev/null || true
killall Finder 2>/dev/null || true

# Screen saver off
defaults -currentHost write com.apple.screensaver idleTime 0

# Software update
if [[ "$(echo "$CONFIG" | jq -r '.system.disable_software_update')" == "true" ]]; then
  sudo softwareupdate --schedule off
  sudo defaults write /Library/Preferences/com.apple.SoftwareUpdate AutomaticCheckEnabled -bool false
  sudo defaults write /Library/Preferences/com.apple.SoftwareUpdate AutomaticDownload -bool false
  sudo defaults write /Library/Preferences/com.apple.SoftwareUpdate AutomaticallyInstallMacOSUpdates -bool false
fi

# Time Machine
if [[ "$(echo "$CONFIG" | jq -r '.system.disable_time_machine')" == "true" ]]; then
  sudo tmutil disable 2>/dev/null || true
  sudo tmutil addexclusion /Library/Ollama 2>/dev/null || true
fi
```

### 1.4 SSH Hardening

```bash
SSHD_CONFIG="/etc/ssh/sshd_config"
sudo cp "$SSHD_CONFIG" "${SSHD_CONFIG}.bak-$(date +%Y%m%d)" 2>/dev/null || true

# Enable SSH
sudo systemsetup -setremotelogin on 2>/dev/null || true

set_sshd() {
  local key="$1" value="$2"
  if grep -qE "^#?${key}" "$SSHD_CONFIG"; then
    sudo sed -i '' "s/^#\?${key}.*/${key} ${value}/" "$SSHD_CONFIG"
  else
    echo "${key} ${value}" | sudo tee -a "$SSHD_CONFIG" > /dev/null
  fi
}

set_sshd "PermitRootLogin" "no"
set_sshd "PasswordAuthentication" "no"
set_sshd "PubkeyAuthentication" "yes"
set_sshd "MaxAuthTries" "3"
set_sshd "ClientAliveInterval" "120"
set_sshd "ClientAliveCountMax" "10"

sudo launchctl stop com.openssh.sshd 2>/dev/null || true
sudo launchctl start com.openssh.sshd 2>/dev/null || true
```

### 1.5 Xcode Command Line Tools (Headless-Safe)

```bash
if ! xcode-select -p &>/dev/null; then
  echo "Installing Xcode Command Line Tools (headless method)..."
  touch /tmp/.com.apple.dt.CommandLineTools.installondemand.in-progress
  PROD=$(softwareupdate --list 2>&1 | grep -B 1 "Command Line Tools" | \
    head -n 1 | awk -F'*' '{print $2}' | sed 's/^ *//' | tr -d '\n')
  if [[ -n "$PROD" ]]; then
    sudo softwareupdate --install "$PROD" --verbose
  else
    echo "WARNING: Could not find CLT in software update. May require GUI install."
  fi
  rm -f /tmp/.com.apple.dt.CommandLineTools.installondemand.in-progress
fi
```

---

## 2. `install-tools.sh` — Serving Stack Installation

### 2.1 Homebrew Check

```bash
if ! command -v brew &>/dev/null; then
  echo "ERROR: Homebrew not found. Install from https://brew.sh before running this script."
  echo "       On Apple Silicon: /bin/bash -c \"\$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)\""
  exit 1
fi
```

### 2.2 Ollama

Only install and configure if `config.json` has `tools.ollama.enabled: true`.

**Install:**

```bash
if ! command -v ollama &>/dev/null; then
  # Prefer CLI install over .app for LaunchDaemon compatibility
  curl -fsSL https://ollama.com/install.sh | sh
  # Verify ARM64 binary
  ARCH_CHECK=$(file /usr/local/bin/ollama 2>/dev/null || file "$(which ollama)")
  if ! echo "$ARCH_CHECK" | grep -q "arm64"; then
    echo "WARNING: ollama binary may not include native arm64 slice"
  fi
fi

# Kill app-based Ollama that conflicts with daemon
pkill ollama 2>/dev/null || true
osascript -e 'tell application "System Events" to delete login item "Ollama"' 2>/dev/null || true
```

**Models directory (SIP-safe):**

```bash
MODELS_DIR=$(echo "$CONFIG" | jq -r '.tools.ollama.models_dir')
sudo mkdir -p "$MODELS_DIR"
sudo chown -R root:wheel "$(dirname "$MODELS_DIR")"
sudo mdutil -i off "$MODELS_DIR" 2>/dev/null || true
sudo mdutil -E "$MODELS_DIR" 2>/dev/null || true
```

**RAM-based configuration tuning:**

The agent must implement this logic to auto-tune Ollama environment variables based on detected RAM. These values feed into the LaunchDaemon plist:

| RAM | MAX_LOADED_MODELS | NUM_PARALLEL | MAX_CONTEXT |
|-----|------------------|--------------|-------------|
| ≤ 16 GB | 1 | 1 | 8192 |
| 17–24 GB | 2 | 2 | 16384 |
| 25–32 GB | 2 | 3 | 32768 |
| 33–64 GB | 3 | 4 | 32768 |
| ≥ 65 GB | 4 | 8 | 65536 |

```bash
# Auto-tune based on RAM
if   [[ $RAM_GB -le 16 ]]; then MAX_LOADED=1; NUM_PAR=1; MAX_CTX=8192
elif [[ $RAM_GB -le 24 ]]; then MAX_LOADED=2; NUM_PAR=2; MAX_CTX=16384
elif [[ $RAM_GB -le 32 ]]; then MAX_LOADED=2; NUM_PAR=3; MAX_CTX=32768
elif [[ $RAM_GB -le 64 ]]; then MAX_LOADED=3; NUM_PAR=4; MAX_CTX=32768
else                             MAX_LOADED=4; NUM_PAR=8; MAX_CTX=65536
fi

# Allow config.json overrides
CFG_MAX_LOADED=$(echo "$CONFIG" | jq -r '.tools.ollama.max_loaded_models // empty')
CFG_NUM_PAR=$(echo "$CONFIG" | jq -r '.tools.ollama.num_parallel // empty')
CFG_MAX_CTX=$(echo "$CONFIG" | jq -r '.tools.ollama.max_context // empty')
[[ -n "$CFG_MAX_LOADED" ]] && MAX_LOADED=$CFG_MAX_LOADED
[[ -n "$CFG_NUM_PAR" ]]    && NUM_PAR=$CFG_NUM_PAR
[[ -n "$CFG_MAX_CTX" ]]    && MAX_CTX=$CFG_MAX_CTX
```

**LaunchDaemon plist:**

```bash
OLLAMA_HOST=$(echo "$CONFIG" | jq -r '.tools.ollama.host')
KEEP_ALIVE=$(echo "$CONFIG" | jq -r '.tools.ollama.keep_alive')
GPU_PCT=$(echo "$CONFIG" | jq -r '.tools.ollama.gpu_percent')
FLASH_ATTN=$(echo "$CONFIG" | jq -r '.tools.ollama.flash_attention | if . then "1" else "0" end')

OLLAMA_PLIST="/Library/LaunchDaemons/com.ollama.server.plist"

sudo tee "$OLLAMA_PLIST" > /dev/null <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>com.ollama.server</string>

  <key>ProgramArguments</key>
  <array>
    <string>/usr/local/bin/ollama</string>
    <string>serve</string>
  </array>

  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>

  <key>StandardOutPath</key>
  <string>/var/log/ollama/stdout.log</string>
  <key>StandardErrorPath</key>
  <string>/var/log/ollama/stderr.log</string>

  <key>EnvironmentVariables</key>
  <dict>
    <!-- CRITICAL: Ollama panics without HOME in daemon context -->
    <key>HOME</key><string>/var/root</string>
    <key>PATH</key><string>/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin</string>

    <key>OLLAMA_MODELS</key><string>${MODELS_DIR}</string>
    <key>OLLAMA_HOST</key><string>${OLLAMA_HOST}</string>
    <key>OLLAMA_KEEP_ALIVE</key><string>${KEEP_ALIVE}</string>
    <key>OLLAMA_NUM_PARALLEL</key><string>${NUM_PAR}</string>
    <key>OLLAMA_MAX_LOADED_MODELS</key><string>${MAX_LOADED}</string>
    <key>OLLAMA_MAX_CONTEXT</key><string>${MAX_CTX}</string>
    <key>OLLAMA_FLASH_ATTENTION</key><string>${FLASH_ATTN}</string>
    <key>OLLAMA_NUM_GPU</key><string>1</string>
    <key>OLLAMA_GPU_PERCENT</key><string>${GPU_PCT}</string>
    <key>OLLAMA_ORIGINS</key><string>*</string>
  </dict>

  <key>WorkingDirectory</key><string>/tmp</string>
  <key>UserName</key><string>root</string>
</dict>
</plist>
EOF

sudo mkdir -p /var/log/ollama
sudo chown root:wheel "$OLLAMA_PLIST"
sudo chmod 644 "$OLLAMA_PLIST"

# Unload if already running, then bootstrap fresh
sudo launchctl bootout system "$OLLAMA_PLIST" 2>/dev/null || true
sleep 1
sudo launchctl bootstrap system "$OLLAMA_PLIST"
sleep 3

# Verify
if sudo launchctl print system/com.ollama.server 2>/dev/null | grep -q "state = running"; then
  echo "[OK] Ollama daemon running"
  curl -s http://localhost:11434/api/tags > /dev/null && echo "[OK] Ollama API responding"
else
  echo "[FAIL] Ollama daemon not running. Check: cat /var/log/ollama/stderr.log"
fi
```

### 2.3 Rapid-MLX

Only install if `config.json` has `tools.rapid_mlx.enabled: true`.

Rapid-MLX is a production-grade MLX-based inference server built specifically for Apple Silicon. It is not a wrapper around mlx-lm — it reimplements the serving stack with: continuous batching, optimized prefill chunking, DeltaNet state snapshots (prompt caching for hybrid RNN-attention architectures that plain mlx-lm cannot cache at all), 17 tool-call format parsers with auto-recovery on malformed output, and clean reasoning/content separation for Qwen3 and DeepSeek-R1. Benchmarked at 2–4.2× faster than Ollama on the same hardware. Default port: `8000`.

**Install:**

```bash
if ! command -v rapid-mlx &>/dev/null; then
  if command -v brew &>/dev/null; then
    # Pre-tap required for Homebrew 5.x sandbox compatibility
    brew tap homebrew/core --force 2>/dev/null || true
    brew install raullenchai/rapid-mlx/rapid-mlx
  else
    # pip fallback — requires Python 3.10+
    PY_MINOR=$(python3 --version 2>/dev/null | awk '{print $2}' | cut -d. -f2)
    if [[ "${PY_MINOR:-0}" -lt 10 ]]; then
      echo "ERROR: Python 3.10+ required for Rapid-MLX. Run: brew install python@3.12"
      exit 1
    fi
    pip3 install rapid-mlx --break-system-packages
  fi
fi

# Install optional extras from config (vision, audio, embeddings, etc.)
EXTRAS=$(echo "$CONFIG" | jq -r '.tools.rapid_mlx.extras[]?' 2>/dev/null || true)
for extra in $EXTRAS; do
  pip3 install "rapid-mlx[${extra}]" --break-system-packages
  echo "[SET] rapid-mlx[$extra] installed"
done

# Run built-in self-diagnostic
rapid-mlx doctor
```

**Model cache directory** (wired to storage volume if configured):

```bash
USE_EXTERNAL=$(echo "$CONFIG" | jq -r '.storage.use_external_volume')
VOLUME_MOUNT=$(jq -r '.storage.external_volume_mount // empty' /tmp/mac-llm-precheck.json 2>/dev/null || echo "")

if [[ "$USE_EXTERNAL" == "true" ]] && [[ -n "$VOLUME_MOUNT" ]]; then
  RAPID_MLX_CACHE="${VOLUME_MOUNT}/models/rapid-mlx"
else
  RAPID_MLX_CACHE="/Library/RapidMLX/cache"
fi

sudo mkdir -p "$RAPID_MLX_CACHE"
sudo chown -R root:wheel "$RAPID_MLX_CACHE"
sudo mdutil -i off "$RAPID_MLX_CACHE" 2>/dev/null || true
echo "[SET] Rapid-MLX cache: $RAPID_MLX_CACHE"
```

**LaunchDaemon:**

```bash
RMLX_HOST=$(echo "$CONFIG" | jq -r '.tools.rapid_mlx.host')
RMLX_PORT=$(echo "$CONFIG" | jq -r '.tools.rapid_mlx.port')
RMLX_MODEL=$(echo "$CONFIG" | jq -r '.tools.rapid_mlx.model')
RMLX_PREFILL=$(echo "$CONFIG" | jq -r '.tools.rapid_mlx.prefill_step_size')
RMLX_NO_THINKING=$(echo "$CONFIG" | jq -r '.tools.rapid_mlx.no_thinking')

# Resolve binary — brew and pip install to different locations
RMLX_BIN=$(command -v rapid-mlx || echo "/opt/homebrew/bin/rapid-mlx")

# Build the plist — conditional --no-thinking flag
NO_THINKING_ENTRY=""
if [[ "$RMLX_NO_THINKING" == "true" ]]; then
  NO_THINKING_ENTRY="    <string>--no-thinking</string>"
fi

RMLX_PLIST="/Library/LaunchDaemons/com.rapid-mlx.server.plist"
sudo tee "$RMLX_PLIST" > /dev/null <<PLISTEOF
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
    ${NO_THINKING_ENTRY}
  </array>
  <key>EnvironmentVariables</key>
  <dict>
    <!-- HOME required: rapid-mlx caches model weights and state under HOME -->
    <key>HOME</key><string>/var/root</string>
    <key>PATH</key><string>/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin</string>
    <key>RAPID_MLX_CACHE_DIR</key><string>${RAPID_MLX_CACHE}</string>
  </dict>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>StandardOutPath</key><string>/var/log/rapid-mlx/stdout.log</string>
  <key>StandardErrorPath</key><string>/var/log/rapid-mlx/stderr.log</string>
  <key>WorkingDirectory</key><string>/tmp</string>
  <key>UserName</key><string>root</string>
</dict>
</plist>
PLISTEOF

sudo mkdir -p /var/log/rapid-mlx
sudo chown root:wheel "$RMLX_PLIST"
sudo chmod 644 "$RMLX_PLIST"
sudo launchctl bootout system "$RMLX_PLIST" 2>/dev/null || true
sleep 1
sudo launchctl bootstrap system "$RMLX_PLIST"
# NOTE: First start downloads the model if not cached — API won't respond until complete.
# verify.sh uses a longer timeout (30s) to account for this.
sleep 5

if curl -sf --max-time 10 "http://localhost:${RMLX_PORT}/v1/models" | grep -q "data"; then
  echo "[OK] Rapid-MLX API responding on port $RMLX_PORT"
else
  echo "[WARN] Rapid-MLX may still be downloading model on first start."
  echo "       Monitor: tail -f /var/log/rapid-mlx/stdout.log"
  echo "       Diagnostic: rapid-mlx doctor"
fi
```

**Rapid-MLX notes for the agent to document in generated README:**
- Default port is `8000` — different from mlx-lm's `8080`. Do not co-install both without changing one port.
- Model aliases (`qwen3.5-4b`, `gemma-4-26b`, `qwen3.5-35b`, etc.) are resolved internally. Run `rapid-mlx models` for the full list. No HuggingFace path required.
- First `serve` downloads the model to `RAPID_MLX_CACHE_DIR` if not cached. On a headless daemon start, the API endpoint is unavailable until download completes — this can take several minutes for large models. Plan around this on initial deploy.
- `--prefill-step-size 8192` is the fix for slow cold-start on long prompts. Always set it.
- `--no-thinking` strips reasoning tokens from Qwen3/DeepSeek-R1 responses. Set `no_thinking: true` in config for coding agent workloads (Claude Code, Cursor) where reasoning tokens interfere with tool-call parsing.
- `rapid-mlx doctor` is a built-in self-diagnostic — always run it first when troubleshooting, before checking logs.
- Vision/audio require extras: `pip install 'rapid-mlx[vision]'` or `[audio]`. Not needed for text/code workloads.
- Rapid-MLX is beta (v0.6.0, April 2026). For stable production use, keep Ollama as fallback.

**Rapid-MLX vs Ollama — the honest trade-off:**

| Dimension | Rapid-MLX | Ollama |
|-----------|-----------|--------|
| Generation speed (Apple Silicon) | 2–4.2× faster | Baseline |
| Tool calling | 17 parsers + auto-recovery | Basic |
| Prompt cache | Yes, incl. DeltaNet for RNN hybrids | Partial |
| Reasoning separation | Yes (Qwen3, DeepSeek-R1) | No |
| Model management | Alias-based (`rapid-mlx models`) | `ollama pull` + registry |
| Built-in diagnostics | `rapid-mlx doctor` | Log inspection |
| Maturity | Beta (v0.6, April 2026) | Stable |
| Best for | Coding agents, tool calling, max speed | General use, model management |

---

### 2.4 MLX / mlx-lm

Only install if `config.json` has `tools.mlx_lm.enabled: true`.

MLX is Apple's own ML framework. `mlx-lm` provides an OpenAI-compatible HTTP server. Prefer Rapid-MLX for most use cases — use mlx-lm directly only when you need to serve a specific HuggingFace model path not covered by Rapid-MLX's model aliases.

```bash
# Install mlx-lm via pip (requires Python 3.10+)
if ! python3 -c "import mlx_lm" 2>/dev/null; then
  pip3 install mlx-lm --break-system-packages
fi

MLX_HOST=$(echo "$CONFIG" | jq -r '.tools.mlx_lm.host')
MLX_PORT=$(echo "$CONFIG" | jq -r '.tools.mlx_lm.port')
MLX_MODEL=$(echo "$CONFIG" | jq -r '.tools.mlx_lm.default_model')
MLX_MODEL_PATH=$(echo "$CONFIG" | jq -r '.tools.mlx_lm.model_path')

# LaunchDaemon for mlx-lm server
MLX_PLIST="/Library/LaunchDaemons/com.mlx-lm.server.plist"
sudo tee "$MLX_PLIST" > /dev/null <<PLISTEOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>com.mlx-lm.server</string>
  <key>ProgramArguments</key>
  <array>
    <string>/usr/local/bin/python3</string>
    <string>-m</string>
    <string>mlx_lm.server</string>
    <string>--host</string><string>${MLX_HOST}</string>
    <string>--port</string><string>${MLX_PORT}</string>
    <string>--model</string><string>${MLX_MODEL}</string>
  </array>
  <key>EnvironmentVariables</key>
  <dict>
    <key>HOME</key><string>/var/root</string>
    <key>PATH</key><string>/usr/local/bin:/usr/bin:/bin</string>
    <key>TRANSFORMERS_CACHE</key><string>${MLX_MODEL_PATH}</string>
  </dict>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>StandardOutPath</key><string>/var/log/mlx-lm/stdout.log</string>
  <key>StandardErrorPath</key><string>/var/log/mlx-lm/stderr.log</string>
</dict>
</plist>
PLISTEOF

sudo mkdir -p /var/log/mlx-lm /Library/MLX/models
sudo chown root:wheel "$MLX_PLIST"
sudo chmod 644 "$MLX_PLIST"
sudo launchctl bootout system "$MLX_PLIST" 2>/dev/null || true
if [[ -n "$MLX_MODEL" ]]; then
  sudo launchctl bootstrap system "$MLX_PLIST"
  echo "[OK] mlx-lm LaunchDaemon installed. Models dir: $MLX_MODEL_PATH"
else
  echo "[SKIP] mlx-lm plist written but NOT started — set tools.mlx_lm.default_model in config.json"
  echo "       Example: mlx-community/Mistral-7B-Instruct-v0.3-4bit"
fi
```

**mlx-lm notes for the agent to document in generated README:**
- Exposes OpenAI-compatible API at `http://host:port/v1/`
- Models are HuggingFace repo IDs (e.g., `mlx-community/Llama-3.2-3B-Instruct-4bit`)
- Download a model before starting: `python3 -m mlx_lm.convert --hf-path <hf-repo-id> --mlx-path /Library/MLX/models/<name>`
- For most use cases, prefer Rapid-MLX — it adds prompt caching, tool-call recovery, and is faster

### 2.5 Infinity Embedding Server

Only install if `config.json` has `tools.infinity.enabled: true`.

Infinity is an OpenAI-compatible embedding server built for production throughput. On Apple Silicon it uses MPS (Metal Performance Shaders) for GPU-accelerated embeddings.

```bash
# Install Infinity
if ! python3 -c "import infinity_emb" 2>/dev/null; then
  pip3 install "infinity-emb[torch,optimum]" --break-system-packages
fi

INF_HOST=$(echo "$CONFIG" | jq -r '.tools.infinity.host')
INF_PORT=$(echo "$CONFIG" | jq -r '.tools.infinity.port')
INF_MODEL=$(echo "$CONFIG" | jq -r '.tools.infinity.model')
INF_ENGINE=$(echo "$CONFIG" | jq -r '.tools.infinity.engine')

INFINITY_PLIST="/Library/LaunchDaemons/com.infinity.server.plist"
sudo tee "$INFINITY_PLIST" > /dev/null <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>com.infinity.server</string>
  <key>ProgramArguments</key>
  <array>
    <string>/usr/local/bin/python3</string>
    <string>-m</string>
    <string>infinity_emb</string>
    <string>v2</string>
    <string>--host</string><string>${INF_HOST}</string>
    <string>--port</string><string>${INF_PORT}</string>
    <string>--model-id</string><string>${INF_MODEL}</string>
    <string>--engine</string><string>${INF_ENGINE}</string>
    <string>--device</string><string>mps</string>
  </array>
  <key>EnvironmentVariables</key>
  <dict>
    <key>HOME</key><string>/var/root</string>
    <key>PATH</key><string>/usr/local/bin:/usr/bin:/bin</string>
  </dict>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>StandardOutPath</key><string>/var/log/infinity/stdout.log</string>
  <key>StandardErrorPath</key><string>/var/log/infinity/stderr.log</string>
</dict>
</plist>
EOF

sudo mkdir -p /var/log/infinity
sudo chown root:wheel "$INFINITY_PLIST"
sudo chmod 644 "$INFINITY_PLIST"
sudo launchctl bootout system "$INFINITY_PLIST" 2>/dev/null || true
sudo launchctl bootstrap system "$INFINITY_PLIST"
sleep 3

# Verify endpoint
if curl -s "http://localhost:${INF_PORT}/health" | grep -q "ok\|healthy"; then
  echo "[OK] Infinity server responding on port $INF_PORT"
else
  echo "[WARN] Infinity may still be loading model. Check: cat /var/log/infinity/stderr.log"
fi
```

**Port reference (document in README):**
- Embedding endpoint: `POST http://host:7997/v1/embeddings`
- Reranking endpoint: `POST http://host:7997/v1/rerank`
- Models endpoint: `GET http://host:7997/v1/models`
- OpenAI-compatible — drop-in for `openai.Embedding.create()`

### 2.6 Exo (Distributed Inference)

Only install if `config.json` has `tools.exo.enabled: true`.

Exo clusters multiple Apple Silicon Macs (or mixed hardware) into a single distributed inference node. Useful when you have multiple Macs and want to pool unified memory to run models larger than any single machine can hold.

```bash
# Install Exo
if ! command -v exo &>/dev/null; then
  brew install exo 2>/dev/null || pip3 install exo --break-system-packages
fi

EXO_PORT=$(echo "$CONFIG" | jq -r '.tools.exo.chatgpt_api_port')
EXO_DISCOVERY=$(echo "$CONFIG" | jq -r '.tools.exo.discovery_module')

# Exo runs as a user-level process (it does peer discovery which requires user context)
# Use LaunchAgent instead of LaunchDaemon
EXO_PLIST="$HOME/Library/LaunchAgents/com.exo.node.plist"
mkdir -p "$HOME/Library/LaunchAgents"
tee "$EXO_PLIST" > /dev/null <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>com.exo.node</string>
  <key>ProgramArguments</key>
  <array>
    <string>$(which exo)</string>
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
EOF

launchctl bootout "gui/$(id -u)/com.exo.node" 2>/dev/null || true
launchctl bootstrap "gui/$(id -u)" "$EXO_PLIST"
echo "[OK] Exo LaunchAgent installed"
echo "NOTICE: Exo requires auto-login to be configured (System Settings → Login Items)"
echo "        For Tailscale discovery: ensure tailscaled is running on all nodes"
```

**Exo notes for README:**
- Exo runs as LaunchAgent (user-level), not LaunchDaemon — it needs user session for Bonjour/Tailscale peer discovery
- Requires auto-login configured (`sysadminctl -autologin set`) for true headless boot
- API endpoint: `http://host:52415/v1/chat/completions` (OpenAI-compatible)
- Discovery options: `tailscale` (recommended for cross-network clusters), `bonjour` (LAN only)
- Each node in the cluster must have Exo installed and running

---

## 3. `verify.sh` — Health Check Report

The verify script runs every check and produces a structured pass/fail report. Exit code 0 means all enabled tools are healthy. Non-zero means at least one check failed.

### 3.1 Report Structure

Output should look like:

```
=== Mac LLM Optimizer — Health Report ===
Timestamp: 2026-06-05 14:23:01
Hardware:  Mac Mini (Mac14,3) | 64 GB RAM | macOS 26.2
SIP:       disabled ✓

--- SYSTEM ---
[PASS] pmset sleep=0
[PASS] pmset disksleep=0
[PASS] pmset powermode=2
[PASS] caffeinate daemon running
[PASS] Spotlight indexing disabled
[PASS] SSH enabled
[WARN] MacBook detected — confirm HDMI dummy plug is connected

--- NETWORK ---
[PASS] net.inet.tcp.sendspace=1048576
[PASS] kern.ipc.somaxconn=2048

--- OLLAMA ---
[PASS] Daemon state=running
[PASS] API responding (http://localhost:11434)
[PASS] Models loaded: 2
[INFO] Loaded: qwen2.5-coder:7b-instruct-q8_0, nomic-embed-text

--- RAPID-MLX ---
[SKIP] Not enabled in config.json

--- MLX-LM ---
[SKIP] Not enabled in config.json

--- INFINITY ---
[PASS] Daemon state=running
[PASS] API responding (http://localhost:7997/health)

--- EXO ---
[SKIP] Not enabled in config.json

=== RESULT: 1 warning, 0 failures ===
```

### 3.2 Checks to Implement

**System checks:**

```bash
check_pmset() {
  local key="$1" expected="$2"
  local actual
  actual=$(pmset -g | grep -E "^\s*${key}" | awk '{print $2}')
  if [[ "$actual" == "$expected" ]]; then
    echo "[PASS] pmset ${key}=${actual}"
  else
    echo "[FAIL] pmset ${key}=${actual} (expected ${expected})"
    FAILURES=$((FAILURES + 1))
  fi
}

check_pmset "sleep" "0"
check_pmset "disksleep" "0"
check_pmset "standby" "0"
check_pmset "womp" "1"
check_pmset "tcpkeepalive" "1"
check_pmset "powermode" "2"
```

**Daemon checks:**

```bash
check_daemon() {
  local label="$1"
  if sudo launchctl print "system/${label}" 2>/dev/null | grep -q "state = running"; then
    echo "[PASS] ${label} running"
  else
    echo "[FAIL] ${label} not running"
    FAILURES=$((FAILURES + 1))
  fi
}
```

**API checks:**

```bash
check_http() {
  local name="$1" url="$2" expected_pattern="$3"
  if curl -sf --max-time 5 "$url" | grep -q "$expected_pattern"; then
    echo "[PASS] ${name} API responding ($url)"
  else
    echo "[FAIL] ${name} API not responding ($url)"
    FAILURES=$((FAILURES + 1))
  fi
}

# Ollama
check_http "Ollama" "http://localhost:11434/api/tags" "models"

# Rapid-MLX
check_http "Rapid-MLX" "http://localhost:$(echo "$CONFIG" | jq -r '.tools.rapid_mlx.port')/v1/models" "."

# mlx-lm
check_http "mlx-lm" "http://localhost:$(echo "$CONFIG" | jq -r '.tools.mlx_lm.port')/v1/models" "."

# Infinity
check_http "Infinity" "http://localhost:$(echo "$CONFIG" | jq -r '.tools.infinity.port')/health" "."
```

**Memory pressure check:**

```bash
MEM_PRESSURE=$(memory_pressure 2>/dev/null | grep "System-wide memory free percentage" | awk '{print $NF}' | tr -d '%')
if [[ -n "$MEM_PRESSURE" ]] && [[ "$MEM_PRESSURE" -gt 20 ]]; then
  echo "[PASS] Memory pressure healthy (${MEM_PRESSURE}% free)"
elif [[ -n "$MEM_PRESSURE" ]]; then
  echo "[WARN] Memory pressure elevated (${MEM_PRESSURE}% free)"
  WARNINGS=$((WARNINGS + 1))
fi
```

---

## 4. `restore.sh` — Undo All Changes

The restore script reverses every change made by `setup.sh`. It is critical this is generated and tested — a dedicated inference machine that needs to be returned to normal use must be fully recoverable.

### 4.1 pmset Restore

```bash
# Restore macOS defaults (not Apple's actual defaults — safe neutral values)
sudo pmset -a sleep 1
sudo pmset -a disablesleep 0
sudo pmset -a disksleep 10
sudo pmset -a standby 1
sudo pmset -a autopoweroff 1
sudo pmset -a powernap 1
sudo pmset -a displaysleep 10
sudo pmset -a powermode 1    # Automatic
```

### 4.2 Service Re-enable

```bash
# Re-enable all disabled services from snapshot
if [[ -f "$SNAPSHOT" ]]; then
  echo "Restoring services from snapshot: $SNAPSHOT"
  # Parse services that were explicitly disabled by setup.sh and re-enable
  while IFS= read -r line; do
    if echo "$line" | grep -q "=> false"; then
      SERVICE=$(echo "$line" | awk '{print $1}')
      sudo launchctl enable "$SERVICE" 2>/dev/null || true
    fi
  done < "$SNAPSHOT"
else
  echo "WARNING: No snapshot found at $SNAPSHOT — manual service restoration required"
fi
```

### 4.3 Remove LaunchDaemons

```bash
remove_daemon() {
  local plist="$1"
  if [[ -f "$plist" ]]; then
    sudo launchctl bootout system "$plist" 2>/dev/null || true
    sudo rm -f "$plist"
    echo "[REMOVED] $plist"
  fi
}

remove_daemon "/Library/LaunchDaemons/com.llm-server.caffeinate.plist"
remove_daemon "/Library/LaunchDaemons/com.ollama.server.plist"
remove_daemon "/Library/LaunchDaemons/com.rapid-mlx.server.plist"
remove_daemon "/Library/LaunchDaemons/com.mlx-lm.server.plist"
remove_daemon "/Library/LaunchDaemons/com.infinity.server.plist"
```

### 4.4 Restore Spotlight

```bash
sudo mdutil -a -i on
echo "[RESTORED] Spotlight indexing"
```

### 4.5 Restore sshd_config

```bash
if [[ -f "/etc/ssh/sshd_config.bak-$(date +%Y%m%d)" ]]; then
  sudo cp "/etc/ssh/sshd_config.bak-$(date +%Y%m%d)" /etc/ssh/sshd_config
  sudo launchctl stop com.openssh.sshd
  sudo launchctl start com.openssh.sshd
fi
```

### 4.6 Restore defaults

```bash
defaults delete com.apple.NetworkBrowser DisableAirDrop 2>/dev/null || true
defaults delete NSGlobalDomain NSAppSleepDisabled 2>/dev/null || true
defaults delete com.apple.dock launchanim 2>/dev/null || true
defaults delete com.apple.finder DisableAllAnimations 2>/dev/null || true
killall Dock 2>/dev/null || true
killall Finder 2>/dev/null || true
```

---

## 5. Generated File Tree

The agent should produce exactly these files:

```
mac-llm-optimizer/
├── README.md                   # Usage guide, hardware reference table, tool comparison
├── config.json                 # All tuning parameters (see §0.4)
├── precheck.sh                 # Read-only system audit — run first (§9)
├── setup.sh                    # System baseline (§1)
├── install-tools.sh            # Tool installation (§2)
├── storage-volume.sh           # External volume setup and symlink wiring (§10)
├── verify.sh                   # Health check (§3)
├── restore.sh                  # Undo all changes (§4)
└── docs/
    ├── tool-comparison.md      # Ollama vs MLX vs Infinity vs Exo: when to use each
    ├── ram-sizing.md           # Model size / quantization / RAM reference
    ├── storage-guide.md        # External volume setup, APFS vs HFS+, symlink map
    └── known-issues.md        # Workarounds table from source documents
```

---

## 6. README.md Requirements

The agent must generate a `README.md` that includes:

### Quick Start

```bash
# 1. Clone or download this directory
# 2. Run precheck first — no sudo required, no changes made:
./precheck.sh

# 3. Review precheck output. If an external volume is available and desired:
#    Edit config.json → set storage.use_external_volume: true, storage.volume_label
#    Then run storage setup (requires sudo):
sudo ./storage-volume.sh

# 4. Edit config.json to enable/configure your tools
# 5. Run system baseline (requires sudo):
sudo ./setup.sh

# 6. Install tools (requires sudo):
sudo ./install-tools.sh

# 7. Verify everything is healthy:
./verify.sh
```

### Tool Selection Guide

| Tool | Best For | API | Port | Apple Silicon Notes |
|------|----------|-----|------|---------------------|
| **Ollama** | General inference, model management, easy model pulling | OpenAI-compatible | 11434 | Mature, Metal-accelerated, easy `ollama pull`. Now uses MLX backend on Apple Silicon (preview, March 2026) |
| **Rapid-MLX** | Maximum speed + tool calling for coding agents (Claude Code, Cursor, Aider) | OpenAI-compatible | 8000 | 2–4.2× faster than Ollama; DeltaNet prompt cache; 17 tool parsers; built-in `rapid-mlx doctor` diagnostic |
| **mlx-lm** | Raw MLX server when you need direct HuggingFace model control | OpenAI-compatible | 8080 | Lower-level than Rapid-MLX; use when you need a specific HF model not aliased in Rapid-MLX |
| **Infinity** | High-throughput embeddings and reranking for RAG pipelines | OpenAI-compatible | 7997 | MPS-accelerated; supports `/v1/embeddings` and `/v1/rerank`; production embedding throughput |
| **Exo** | Multi-Mac distributed inference; models too large for one machine | OpenAI-compatible | 52415 | Pools unified memory across devices via Tailscale or Bonjour |

**The combinations that make sense:**
- **Solo node, general use:** Ollama only
- **Solo node, coding agent (Claude Code / Cursor):** Rapid-MLX (generation) + Infinity (embeddings)
- **Solo node, RAG app:** Ollama (generation) + Infinity (embeddings)
- **Solo node, max raw throughput with custom HF models:** mlx-lm (generation) + Infinity (embeddings)
- **Multi-Mac cluster:** Exo (generation across nodes) + Infinity on dedicated node

**Rapid-MLX vs mlx-lm — when to choose which:**
Rapid-MLX is the right default for most setups. It wraps mlx-lm with production features: prompt caching (including DeltaNet snapshots for hybrid RNN-attention models that mlx-lm cannot cache), 17 tool-call parsers with auto-recovery on malformed output, reasoning separation for Qwen3/DeepSeek-R1, and a built-in `rapid-mlx doctor` diagnostic. Use raw mlx-lm only when you need a specific HuggingFace model path not covered by Rapid-MLX's model aliases, or when you want the minimal dependency surface.

### Hardware RAM Reference

| Mac Model | RAM | Recommended Serving Config |
|-----------|-----|---------------------------|
| MacBook Air M3 | 16 GB | 7B Q8 only; 1 model loaded |
| MacBook Air M3 | 24 GB | 13B Q8 or 7B Q8 + embeddings |
| MacBook Pro M4 | 32 GB | 32B Q4 or 13B Q8; 2 models |
| Mac Mini M4 | 64 GB | 70B Q4 or 32B Q5; 3 models |
| Mac Studio M4 Ultra | 192 GB | 405B Q4; multiple large models |
| Mac Pro M2 Ultra | 192 GB | Same as Studio Ultra |

---

## 7. Known Issues Reference

The agent must include this table in `docs/known-issues.md`, synthesized from both source documents:

| Issue | Affects | Fix |
|-------|---------|-----|
| `panic: $HOME is not defined` | All Macs, all macOS | Set `HOME=/var/root` in LaunchDaemon plist — always required |
| pmset values reset after macOS update | macOS 26 Tahoe | Re-run `setup.sh` post-update; or add a LaunchDaemon that runs pmset at boot |
| MacBook sleeps on lid close | MacBooks only | HDMI dummy plug (recommended) or `sudo pmset -a lidwake 0` (thermal risk) |
| Sequoia 15.3+ sleep regression | macOS 15.3+ | `caffeinate -dimsu` LaunchDaemon is safety net — install regardless |
| SIP blocks persistent service disabling | macOS 15+ with SIP on | Disable SIP in Recovery Mode first; `setup.sh` warns and continues safely |
| M4 framebuffer without display | M4 Mac Mini | HDMI dummy plug required for proper GPU paths and VNC resolution |
| FileVault blocks headless boot | All Macs with FV enabled | Disable FileVault before going headless — cannot be scripted, requires GUI |
| `defaults write` auto-login broken | macOS 15 Sequoia+ | Use `sysadminctl -autologin set` or System Settings |
| `launchctl load` deprecated | macOS 26 Tahoe | Use `bootstrap`/`bootout` — scripts use correct commands |
| Ollama login item conflicts with daemon | All Macs | `setup.sh` removes Ollama login item automatically |
| SIP blocks `/usr/share` writes | macOS 15+ | Use `/Library` for models and config — all plists use `/Library/Ollama` |
| `xcode-select --install` fails headless | All headless Macs | Use `softwareupdate` method in `setup.sh` |
| Exo needs user session for peer discovery | All Macs | Runs as LaunchAgent, requires auto-login — cannot be LaunchDaemon |
| `mlx-lm` requires model before server start | All Macs | Download model first; plist is written but not bootstrapped if model path empty |

---

## 8. Implementation Notes for Claude Code

1. **Script execution order matters.** `setup.sh` must complete before `install-tools.sh`. The `verify.sh` can run any time after install.

2. **`config.json` is the single control plane.** Do not hardcode values in scripts. Every tunable parameter reads from `config.json`.

3. **All `launchctl` calls use `bootstrap`/`bootout`**, never `load`/`unload` — the latter is deprecated as of macOS 26 Tahoe.

4. **The `HOME=/var/root` entry in every LaunchDaemon plist is non-negotiable.** Ollama, mlx-lm, and Infinity all panic without `$HOME` defined. This is not a bug to be fixed upstream in the short term.

5. **Infinity requires `--device mps`** to use GPU on Apple Silicon. Without this flag it falls back to CPU and performance degrades by ~10×.

6. **Exo is the only tool that runs as a LaunchAgent**, not a LaunchDaemon, because it requires user context for mDNS/Bonjour/Tailscale peer discovery. This means it only starts after auto-login, not at raw boot.

7. **Test the restore script.** A configuration that cannot be reversed is a configuration that will eventually cause problems. `restore.sh` must be tested on the same hardware before the machine is put into production.

8. **`verify.sh` should be run as a cron job or scheduled LaunchDaemon** to catch silent failures — daemons that crash and fail to restart, memory pressure creeping up, or pmset values silently reset after a macOS update.

---

## 9. `precheck.sh` — Read-Only System Audit

`precheck.sh` runs before any other script. It requires no `sudo`, makes zero changes, and exits with a structured report. Its job is to tell the operator exactly what they have, what constraints apply, and what is and isn't possible on this specific machine before anything is configured.

### 9.1 Design Rules

- **No writes, no side effects.** Every command is read-only. If a check requires `sudo` for a specific detail (e.g., `csrutil status`), attempt it but degrade gracefully if not available.
- **Output is human-readable and machine-parseable.** Emit a plain-text report to stdout AND write a JSON summary to `/tmp/mac-llm-precheck.json` so downstream scripts can consume results.
- **Exit code reflects blockers.** Exit 0 = can proceed. Exit 1 = hard blockers found (Intel CPU, no Homebrew, FileVault on, etc.). Exit 2 = warnings only (SIP on, low RAM, no external volume).

### 9.2 Report Sections and Checks

#### Section: Hardware Identity

```bash
echo "=== HARDWARE ==="

# Chip and architecture
CHIP=$(sysctl -n machdep.cpu.brand_string 2>/dev/null || system_profiler SPHardwareDataType | grep "Chip" | awk -F: '{print $2}' | xargs)
echo "  Chip:        $CHIP"
echo "  Arch:        $(uname -m)"

# RAM
RAM_BYTES=$(sysctl -n hw.memsize)
RAM_GB=$(( RAM_BYTES / 1024 / 1024 / 1024 ))
echo "  RAM:         ${RAM_GB} GB"

# Mac model
HW_MODEL=$(sysctl -n hw.model)
echo "  Model:       $HW_MODEL"

# Is this a laptop?
IS_LAPTOP=false
if echo "$HW_MODEL" | grep -qiE "MacBook"; then
  IS_LAPTOP=true
  echo "  Form factor: Laptop — lid/battery/thermal notes apply"
else
  echo "  Form factor: Desktop — simplified headless setup"
fi

# CPU core count
PERF_CORES=$(sysctl -n hw.perflevel0.logicalcpu 2>/dev/null || sysctl -n hw.logicalcpu)
EFF_CORES=$(sysctl -n hw.perflevel1.logicalcpu 2>/dev/null || echo "N/A")
echo "  CPU cores:   ${PERF_CORES} performance, ${EFF_CORES} efficiency"
```

#### Section: What Models Can Run — RAM-Based Capability Table

```bash
echo ""
echo "=== MODEL CAPABILITY ==="

# Derive capabilities from RAM
if   [[ $RAM_GB -le 8  ]]; then
  echo "  [WARN] ${RAM_GB}GB RAM: below practical minimum for LLM inference"
  echo "         Minimum viable: 3B Q8 (~4GB). Embedding-only workloads feasible."
  CAPABILITY="minimal"
elif [[ $RAM_GB -le 16 ]]; then
  echo "  [OK]   ${RAM_GB}GB RAM: 7B Q8 (~8GB) — leave headroom for OS (~4GB used)"
  echo "         Max practical: 7B Q8. One model loaded at a time."
  CAPABILITY="7b"
elif [[ $RAM_GB -le 24 ]]; then
  echo "  [OK]   ${RAM_GB}GB RAM: 13B Q8 (~14GB) or 7B Q8 + embeddings model"
  echo "         Can load 2 models simultaneously."
  CAPABILITY="13b"
elif [[ $RAM_GB -le 32 ]]; then
  echo "  [OK]   ${RAM_GB}GB RAM: 32B Q4 (~20GB) or 13B Q8. 2 models simultaneously."
  CAPABILITY="32b"
elif [[ $RAM_GB -le 64 ]]; then
  echo "  [GOOD] ${RAM_GB}GB RAM: 70B Q4 (~40GB) or 32B Q5. 3 models simultaneously."
  CAPABILITY="70b"
elif [[ $RAM_GB -le 128 ]]; then
  echo "  [GREAT] ${RAM_GB}GB RAM: 70B Q8 or multiple 32B/70B models simultaneously."
  CAPABILITY="70b-q8"
else
  echo "  [EXCELLENT] ${RAM_GB}GB RAM: 405B Q4 or multiple 70B models. Full capability."
  CAPABILITY="405b"
fi

# Recommended models by capability
echo ""
echo "  Recommended models for this hardware:"
case $CAPABILITY in
  minimal)  echo "    • ollama pull llama3.2:3b-instruct-q8_0" ;;
  7b)       echo "    • ollama pull qwen2.5-coder:7b-instruct-q8_0"
            echo "    • ollama pull nomic-embed-text" ;;
  13b)      echo "    • ollama pull qwen2.5-coder:7b-instruct-q8_0"
            echo "    • ollama pull mxbai-embed-large"
            echo "    • ollama pull llama3.1:8b-instruct-q8_0" ;;
  32b)      echo "    • ollama pull qwen2.5-coder:32b-instruct-q5_K_M"
            echo "    • ollama pull nomic-embed-text (sideload with generation model)" ;;
  70b|70b-q8|405b)
            echo "    • ollama pull qwen2.5:72b-instruct-q4_K_M"
            echo "    • ollama pull qwen2.5-coder:32b-instruct-q5_K_M"
            echo "    • ollama pull mxbai-embed-large" ;;
esac
```

#### Section: macOS and Security State

```bash
echo ""
echo "=== MACOS & SECURITY ==="

OS_VERSION=$(sw_vers -productVersion)
OS_NAME=$(sw_vers -productName)
echo "  OS:          $OS_NAME $OS_VERSION"

# SIP status
SIP_RAW=$(csrutil status 2>/dev/null || echo "unknown")
if echo "$SIP_RAW" | grep -q "enabled"; then
  echo "  SIP:         ENABLED — persistent service disabling will not survive reboots"
  echo "               To disable: boot Recovery Mode → Terminal → 'csrutil disable'"
  SIP_STATE="enabled"
elif echo "$SIP_RAW" | grep -q "disabled"; then
  echo "  SIP:         disabled ✓ — full service suppression available"
  SIP_STATE="disabled"
else
  echo "  SIP:         unknown (run as admin to check)"
  SIP_STATE="unknown"
fi

# FileVault — hard blocker for headless
FV_RAW=$(fdesetup status 2>/dev/null || echo "unknown")
if echo "$FV_RAW" | grep -qi "on"; then
  echo "  FileVault:   ENABLED ⚠ BLOCKER — headless reboots will hang waiting for password"
  echo "               Fix: System Settings → Privacy & Security → FileVault → Turn Off"
  FV_STATE="on"
  BLOCKERS=$((BLOCKERS + 1))
elif echo "$FV_RAW" | grep -qi "off"; then
  echo "  FileVault:   off ✓"
  FV_STATE="off"
else
  echo "  FileVault:   unknown"
  FV_STATE="unknown"
fi

# Auto-login check (indirect — check if a user is configured for auto-login)
AL_USER=$(defaults read /Library/Preferences/com.apple.loginwindow autoLoginUser 2>/dev/null || echo "")
if [[ -n "$AL_USER" ]]; then
  echo "  Auto-login:  configured ($AL_USER) ✓"
else
  echo "  Auto-login:  NOT configured — required for Exo and LaunchAgent-based tools"
  echo "               Fix: sudo sysadminctl -autologin set -userName <user> -password <pw>"
fi

# Xcode CLT
if xcode-select -p &>/dev/null; then
  echo "  Xcode CLT:   installed ($(xcode-select -p))"
else
  echo "  Xcode CLT:   NOT installed — required for Homebrew and build tools"
  echo "               setup.sh will install via softwareupdate (headless-safe)"
fi
```

#### Section: Tool Prerequisites

```bash
echo ""
echo "=== TOOL PREREQUISITES ==="

check_binary() {
  local name="$1" cmd="$2"
  if command -v "$cmd" &>/dev/null; then
    echo "  [PRESENT] $name: $(command -v "$cmd")"
  else
    echo "  [MISSING] $name"
  fi
}

check_binary "Homebrew"   "brew"
check_binary "Python 3"   "python3"
check_binary "pip3"       "pip3"
check_binary "jq"         "jq"
check_binary "Ollama"     "ollama"
check_binary "Rapid-MLX"  "rapid-mlx"
check_binary "mlx_lm"     "python3 -c 'import mlx_lm' 2>/dev/null && echo ok"
check_binary "curl"       "curl"
check_binary "git"        "git"

# Python version check (mlx-lm requires 3.10+)
PY_VERSION=$(python3 --version 2>/dev/null | awk '{print $2}')
PY_MAJOR=$(echo "$PY_VERSION" | cut -d. -f1)
PY_MINOR=$(echo "$PY_VERSION" | cut -d. -f2)
if [[ "$PY_MAJOR" -ge 3 ]] && [[ "$PY_MINOR" -ge 10 ]]; then
  echo "  Python:      $PY_VERSION ✓ (mlx-lm requires 3.10+)"
else
  echo "  Python:      $PY_VERSION ⚠ — mlx-lm requires Python 3.10 or later"
fi

# Check if Ollama is already running (and as what — app vs daemon)
if pgrep -x "ollama" &>/dev/null; then
  OLLAMA_PID=$(pgrep -x "ollama")
  # Check if it's running as root (daemon) or as user (app/login item)
  OLLAMA_USER=$(ps -o user= -p "$OLLAMA_PID" 2>/dev/null || echo "unknown")
  if [[ "$OLLAMA_USER" == "root" ]]; then
    echo "  Ollama:      running as root (LaunchDaemon) ✓"
  else
    echo "  Ollama:      running as user '$OLLAMA_USER' (app/login item) — will be converted to daemon"
  fi
fi
```

#### Section: Network and Ports

```bash
echo ""
echo "=== NETWORK & PORTS ==="

# Current IP addresses
echo "  IP addresses:"
ifconfig | grep "inet " | grep -v "127.0.0.1" | awk '{print "    " $2}'

# Port availability check
check_port() {
  local name="$1" port="$2"
  if lsof -iTCP:"$port" -sTCP:LISTEN &>/dev/null; then
    local proc
    proc=$(lsof -iTCP:"$port" -sTCP:LISTEN | tail -1 | awk '{print $1}')
    echo "  Port $port ($name): IN USE by $proc"
  else
    echo "  Port $port ($name): available ✓"
  fi
}

check_port "Ollama"     11434
check_port "Rapid-MLX"  8000
check_port "mlx-lm"    8080
check_port "Infinity"   7997
check_port "Exo"        52415

# SSH status
SSH_STATUS=$(sudo systemsetup -getremotelogin 2>/dev/null || echo "unknown")
echo "  SSH:         $SSH_STATUS"

# Firewall
FW_STATUS=$(defaults read /Library/Preferences/com.apple.alf globalstate 2>/dev/null || echo "unknown")
case "$FW_STATUS" in
  0) echo "  Firewall:    off — consider enabling if API is network-accessible" ;;
  1) echo "  Firewall:    on (essential services only)" ;;
  2) echo "  Firewall:    on (block all incoming) — may need rule for port 11434" ;;
  *) echo "  Firewall:    unknown" ;;
esac
```

#### Section: Storage — The Critical One

This is the most important section for operators choosing between internal and external volume storage.

```bash
echo ""
echo "=== STORAGE ==="

# Internal disk — boot volume
BOOT_DISK=$(df / | tail -1 | awk '{print $1}')
BOOT_TOTAL=$(df -g / | tail -1 | awk '{print $2}')
BOOT_USED=$(df -g / | tail -1 | awk '{print $3}')
BOOT_FREE=$(df -g / | tail -1 | awk '{print $4}')
echo "  Boot volume: $BOOT_DISK"
echo "    Total: ${BOOT_TOTAL}GB | Used: ${BOOT_USED}GB | Free: ${BOOT_FREE}GB"

# Estimate model storage headroom on internal
if [[ $BOOT_FREE -ge 500 ]]; then
  echo "    Assessment: ample space — internal storage viable for model library"
elif [[ $BOOT_FREE -ge 100 ]]; then
  echo "    Assessment: moderate space — can store a few large models internally"
elif [[ $BOOT_FREE -ge 50 ]]; then
  echo "    Assessment: tight — recommend external volume for model storage"
else
  echo "    Assessment: CRITICAL LOW SPACE ⚠ — external volume strongly recommended"
fi

# All mounted volumes
echo ""
echo "  All mounted volumes:"
df -g | grep -v "tmpfs\|devfs\|map " | tail -n +2 | while IFS= read -r line; do
  VOL_PATH=$(echo "$line" | awk '{print $NF}')
  VOL_FREE=$(echo "$line" | awk '{print $4}')
  VOL_TOTAL=$(echo "$line" | awk '{print $2}')
  VOL_FS=$(diskutil info "$VOL_PATH" 2>/dev/null | grep "Type (Bundle)" | awk '{print $NF}')
  printf "    %-40s %4dGB free / %4dGB total  [%s]\n" "$VOL_PATH" "$VOL_FREE" "$VOL_TOTAL" "${VOL_FS:-unknown}"
done

# External volumes — enumerate with rich detail
echo ""
echo "  External / non-boot volumes:"
EXTERNAL_FOUND=false

diskutil list | grep -E "^/dev/disk[0-9]+" | while read -r DISK_LINE; do
  DISK=$(echo "$DISK_LINE" | awk '{print $1}')
  # Skip the boot disk
  BOOT_DISK_BASE=$(diskutil info / | grep "Part of Whole" | awk '{print "/dev/"$NF}')
  [[ "$DISK" == "$BOOT_DISK_BASE" ]] && continue

  # Get disk info
  DISK_SIZE=$(diskutil info "$DISK" 2>/dev/null | grep "Disk Size" | awk '{print $3, $4}')
  DISK_TYPE=$(diskutil info "$DISK" 2>/dev/null | grep "Solid State" | awk '{print $NF}')
  REMOVABLE=$(diskutil info "$DISK" 2>/dev/null | grep "Removable Media" | awk '{print $NF}')
  PROTOCOL=$(diskutil info "$DISK" 2>/dev/null | grep "Device Protocol" | awk '{print $NF}')

  echo "    Disk: $DISK — $DISK_SIZE — Protocol: $PROTOCOL — Removable: $REMOVABLE"

  # Enumerate partitions/volumes on this disk
  diskutil list "$DISK" | grep -E "Apple_APFS|Apple_HFS|ExFAT|FAT32|NTFS" | while read -r PART_LINE; do
    PART_NAME=$(echo "$PART_LINE" | awk '{print $2}')
    PART_SIZE=$(echo "$PART_LINE" | awk '{print $(NF-1), $NF}')
    PART_ID=$(echo "$PART_LINE" | awk '{print $NF}' | grep -oE "disk[0-9]+s[0-9]+" || echo "")
    MOUNT=$(diskutil info "/dev/$PART_ID" 2>/dev/null | grep "Mount Point" | awk -F: '{print $2}' | xargs)

    if [[ -n "$MOUNT" ]]; then
      PART_FREE=$(df -g "$MOUNT" 2>/dev/null | tail -1 | awk '{print $4}')
      echo "      Volume: $PART_NAME | Size: $PART_SIZE | Mounted: $MOUNT | Free: ${PART_FREE}GB"
      # Score this volume for model storage suitability
      if [[ "$PROTOCOL" == "USB" ]]; then
        echo "        ⚠  USB — adequate for cold storage; inference I/O will be slower than internal NVMe"
      elif [[ "$PROTOCOL" =~ "Thunderbolt|PCI" ]]; then
        echo "        ✓  Thunderbolt/PCIe — fast enough for inference I/O (comparable to internal)"
      fi
    else
      echo "      Volume: $PART_NAME | Size: $PART_SIZE | NOT MOUNTED"
    fi
    EXTERNAL_FOUND=true
  done
done

if [[ "$EXTERNAL_FOUND" != "true" ]]; then
  echo "    None detected — only internal boot volume present"
fi

# Check if an existing volume matches the config label
CFG_LABEL=$(jq -r '.storage.volume_label // "LLMStorage"' "$(dirname "$0")/config.json" 2>/dev/null || echo "LLMStorage")
LABEL_MOUNT=$(diskutil info "$CFG_LABEL" 2>/dev/null | grep "Mount Point" | awk -F: '{print $2}' | xargs)
if [[ -n "$LABEL_MOUNT" ]]; then
  LABEL_FREE=$(df -g "$LABEL_MOUNT" | tail -1 | awk '{print $4}')
  echo ""
  echo "  ✓ Volume matching config label '$CFG_LABEL' found at: $LABEL_MOUNT (${LABEL_FREE}GB free)"
  echo "    storage-volume.sh will use this volume automatically."
else
  echo ""
  echo "  No volume with label '$CFG_LABEL' detected."
  echo "  Options:"
  echo "    1. Attach an external drive and format it with label '$CFG_LABEL' (APFS recommended)"
  echo "    2. Change storage.volume_label in config.json to match an existing volume name"
  echo "    3. Set storage.use_external_volume: false to use internal storage only"
fi
```

#### Section: Power Management Current State

```bash
echo ""
echo "=== CURRENT POWER STATE ==="

pmset -g | grep -E "sleep |disksleep|standby|powernap|powermode|SleepDisabled" | while IFS= read -r line; do
  KEY=$(echo "$line" | awk '{print $1}')
  VAL=$(echo "$line" | awk '{print $2}')
  # Flag values that will need to change
  case "$KEY" in
    "sleep")       [[ "$VAL" != "0" ]] && echo "  [CHANGE NEEDED] $line" || echo "  [OK] $line" ;;
    "disksleep")   [[ "$VAL" != "0" ]] && echo "  [CHANGE NEEDED] $line" || echo "  [OK] $line" ;;
    "powermode")   [[ "$VAL" != "2" ]] && echo "  [CHANGE NEEDED] $line (need 2 for High Performance)" || echo "  [OK] $line" ;;
    *)             echo "  $line" ;;
  esac
done

# Sleep prevention assertions
echo ""
echo "  Current sleep prevention assertions:"
pmset -g assertions | grep -E "PreventSystemSleep|PreventUserIdleSystemSleep" | head -10 || echo "    None active"
```

#### Section: Final Readiness Summary and JSON Output

```bash
echo ""
echo "=== READINESS SUMMARY ==="

BLOCKERS=0
WARNINGS=0

# Evaluate blockers
[[ "$(uname -m)" != "arm64" ]]                   && { echo "  [BLOCKER] Not Apple Silicon"; BLOCKERS=$((BLOCKERS+1)); }
[[ "$FV_STATE" == "on" ]]                         && { echo "  [BLOCKER] FileVault enabled — disable before going headless"; BLOCKERS=$((BLOCKERS+1)); }
! command -v brew &>/dev/null                     && { echo "  [BLOCKER] Homebrew not installed"; BLOCKERS=$((BLOCKERS+1)); }
[[ $BOOT_FREE -lt 20 ]]                           && { echo "  [BLOCKER] Less than 20GB free on boot volume"; BLOCKERS=$((BLOCKERS+1)); }

# Evaluate warnings
[[ "$SIP_STATE" == "enabled" ]]                   && { echo "  [WARN] SIP on — persistent service suppression needs Recovery Mode"; WARNINGS=$((WARNINGS+1)); }
[[ $BOOT_FREE -lt 50 ]] && [[ $BOOT_FREE -ge 20 ]] && { echo "  [WARN] Boot volume has < 50GB free — consider external volume"; WARNINGS=$((WARNINGS+1)); }
[[ "$IS_LAPTOP" == "true" ]]                      && { echo "  [WARN] Laptop — verify HDMI dummy plug before going headless"; WARNINGS=$((WARNINGS+1)); }
[[ -z "$AL_USER" ]]                               && { echo "  [WARN] Auto-login not configured — required for Exo"; WARNINGS=$((WARNINGS+1)); }
! command -v python3 &>/dev/null                  && { echo "  [WARN] Python 3 not found — mlx-lm and Infinity unavailable"; WARNINGS=$((WARNINGS+1)); }

if [[ $BLOCKERS -eq 0 ]] && [[ $WARNINGS -eq 0 ]]; then
  echo "  ✓ All clear — ready to run setup.sh"
elif [[ $BLOCKERS -eq 0 ]]; then
  echo "  ✓ Can proceed with $WARNINGS warning(s) — review above before continuing"
else
  echo "  ✗ $BLOCKERS blocker(s) must be resolved before proceeding"
fi

# Write JSON summary for downstream script consumption
cat > /tmp/mac-llm-precheck.json <<JSONEOF
{
  "timestamp": "$(date -u +%Y-%m-%dT%H:%M:%SZ)",
  "hardware": {
    "model": "$HW_MODEL",
    "chip": "$CHIP",
    "arch": "$(uname -m)",
    "ram_gb": $RAM_GB,
    "is_laptop": $IS_LAPTOP
  },
  "capability": "$CAPABILITY",
  "security": {
    "sip": "$SIP_STATE",
    "filevault": "$FV_STATE",
    "auto_login_user": "${AL_USER:-null}"
  },
  "storage": {
    "boot_free_gb": $BOOT_FREE,
    "external_volume_label_found": $([ -n "$LABEL_MOUNT" ] && echo "true" || echo "false"),
    "external_volume_mount": "${LABEL_MOUNT:-null}"
  },
  "readiness": {
    "blockers": $BLOCKERS,
    "warnings": $WARNINGS,
    "can_proceed": $([ $BLOCKERS -eq 0 ] && echo "true" || echo "false")
  }
}
JSONEOF
echo ""
echo "  JSON summary written to: /tmp/mac-llm-precheck.json"

# Exit codes: 0=clear, 1=blockers, 2=warnings only
[[ $BLOCKERS -gt 0 ]] && exit 1
[[ $WARNINGS -gt 0 ]] && exit 2
exit 0
```

---

## 10. `storage-volume.sh` — External Volume Setup

Model storage on an external volume has significant trade-offs. This section specifies how the agent should handle volume detection, formatting guidance, directory layout, symlink wiring into all serving tools, and the `disksleep 0` / Spotlight exclusion requirements that make external volumes viable for inference.

### 10.1 Why External Volume Storage

A 70B Q4 model is ~40GB. A modest library — one large generation model, one mid-size coder model, one embedding model — hits 60–80GB easily. On a Mac Mini M4 with the base 256GB SSD, that's 25–30% of the entire boot volume consumed by model weights. External APFS volumes over Thunderbolt 4 deliver sequential read speeds of 2–3 GB/s, which is fast enough for model loading without meaningful inference latency penalty. USB 3.x (5–10 Gbps) is slower but acceptable for cold loads.

### 10.2 Volume Requirements

The agent must document and enforce these requirements:

| Requirement | Why |
|------------|-----|
| APFS format (preferred) or HFS+ Journaled | ExFAT and FAT32 lack Unix permissions — `root:wheel` ownership of model dirs won't work correctly |
| Case-sensitive APFS optional | Not required; standard APFS works fine |
| Volume must be auto-mounted at boot | Required for LaunchDaemon startup; configure via `/etc/fstab` or leave macOS to handle via `/Volumes/` |
| `disksleep 0` enforced | `setup.sh` handles this — without it, drives spin down mid-inference |
| Spotlight excluded | Without exclusion, `mds` will index every .gguf and .safetensors file — killing I/O during inference |
| `OLLAMA_MODELS` env var updated | LaunchDaemon plist must point to the volume path, not `/Library/Ollama/models` |

### 10.3 Volume Detection Logic

```bash
# Read config
USE_EXTERNAL=$(echo "$CONFIG" | jq -r '.storage.use_external_volume')
VOLUME_LABEL=$(echo "$CONFIG" | jq -r '.storage.volume_label')
MODELS_SUBDIR=$(echo "$CONFIG" | jq -r '.storage.models_subdir')
MIN_FREE_GB=$(echo "$CONFIG" | jq -r '.storage.min_free_gb')
SYMLINK_INTERNAL=$(echo "$CONFIG" | jq -r '.storage.symlink_internal_paths')

if [[ "$USE_EXTERNAL" != "true" ]]; then
  echo "storage.use_external_volume is false in config.json — skipping"
  exit 0
fi

# Auto-detect volume by label
VOLUME_MOUNT=""

# Check precheck output first (fast path)
if [[ -f /tmp/mac-llm-precheck.json ]]; then
  VOLUME_MOUNT=$(jq -r '.storage.external_volume_mount // empty' /tmp/mac-llm-precheck.json)
fi

# Fall back to live detection
if [[ -z "$VOLUME_MOUNT" ]]; then
  VOLUME_MOUNT=$(diskutil info "$VOLUME_LABEL" 2>/dev/null | grep "Mount Point" | awk -F: '{print $2}' | xargs)
fi

if [[ -z "$VOLUME_MOUNT" ]]; then
  echo "ERROR: No volume with label '$VOLUME_LABEL' found."
  echo ""
  echo "To prepare an external drive:"
  echo "  1. Connect the drive"
  echo "  2. Format it (APFS recommended):"
  echo "       diskutil eraseDisk APFS '$VOLUME_LABEL' /dev/diskN"
  echo "     (Replace diskN with the correct disk identifier — check: diskutil list)"
  echo "  3. Re-run this script"
  echo ""
  echo "To use a different label, update storage.volume_label in config.json"
  exit 1
fi

# Check free space
VOL_FREE_GB=$(df -g "$VOLUME_MOUNT" | tail -1 | awk '{print $4}')
if [[ $VOL_FREE_GB -lt $MIN_FREE_GB ]]; then
  echo "ERROR: Volume '$VOLUME_LABEL' has only ${VOL_FREE_GB}GB free (minimum: ${MIN_FREE_GB}GB)"
  exit 1
fi

# Verify filesystem is APFS or HFS+ (reject ExFAT/FAT32)
VOL_FS=$(diskutil info "$VOLUME_MOUNT" 2>/dev/null | grep "Type (Bundle)" | awk '{print $NF}')
if echo "$VOL_FS" | grep -qiE "exfat|fat32|msdos|ntfs"; then
  echo "ERROR: Volume filesystem is '$VOL_FS' — APFS or HFS+ Journaled required"
  echo "       ExFAT/FAT32/NTFS do not support Unix permissions needed for model storage"
  echo "       Reformat: diskutil eraseDisk APFS '$VOLUME_LABEL' /dev/diskN"
  exit 1
fi

echo "[OK] Volume '$VOLUME_LABEL' found at: $VOLUME_MOUNT (${VOL_FREE_GB}GB free, ${VOL_FS})"
```

### 10.4 Directory Layout on Volume

```bash
# The standardized layout on the external volume
# All tools share the same volume root; each has its own subdirectory

MODEL_ROOT="${VOLUME_MOUNT}/${MODELS_SUBDIR}"
OLLAMA_VOL_DIR="${MODEL_ROOT}/ollama"
RAPID_MLX_VOL_DIR="${MODEL_ROOT}/rapid-mlx"
MLX_VOL_DIR="${MODEL_ROOT}/mlx-lm"
INFINITY_VOL_DIR="${MODEL_ROOT}/infinity"
EXO_VOL_DIR="${MODEL_ROOT}/exo"
SHARED_GGUF_DIR="${MODEL_ROOT}/gguf"   # Raw .gguf files usable by both Ollama and llama.cpp

sudo mkdir -p "$OLLAMA_VOL_DIR"
sudo mkdir -p "$RAPID_MLX_VOL_DIR"
sudo mkdir -p "$MLX_VOL_DIR"
sudo mkdir -p "$INFINITY_VOL_DIR"
sudo mkdir -p "$EXO_VOL_DIR"
sudo mkdir -p "$SHARED_GGUF_DIR"

# Set ownership
sudo chown -R root:wheel "$MODEL_ROOT"
sudo chmod -R 755 "$MODEL_ROOT"

echo "[SET] Volume directory layout created at: $MODEL_ROOT"
echo "      $OLLAMA_VOL_DIR"
echo "      $RAPID_MLX_VOL_DIR"
echo "      $MLX_VOL_DIR"
echo "      $INFINITY_VOL_DIR"
echo "      $SHARED_GGUF_DIR"
```

### 10.5 Spotlight Exclusion on Volume

```bash
# Exclude model directories from Spotlight — critical
# Without this, mds will index every .gguf file and compete for I/O during inference
sudo mdutil -i off "$VOLUME_MOUNT"
sudo mdutil -E "$VOLUME_MOUNT"

# Belt-and-suspenders: add .metadata_never_index file
touch "${VOLUME_MOUNT}/.metadata_never_index"

echo "[SET] Spotlight indexing disabled on $VOLUME_MOUNT"
```

### 10.6 Symlink Wiring — Internal Paths Redirect to Volume

When `storage.symlink_internal_paths` is `true`, create symlinks from the canonical internal paths used by the LaunchDaemon plists to the actual volume locations. This means `install-tools.sh` does not need to know whether storage is internal or external — it always writes plists pointing to `/Library/Ollama/models`, `/Library/MLX/models`, etc., and the symlinks transparently redirect to the volume.

```bash
if [[ "$SYMLINK_INTERNAL" == "true" ]]; then
  wire_symlink() {
    local internal_path="$1"
    local volume_path="$2"
    local label="$3"

    # If the internal path already exists as a real directory, move contents to volume
    if [[ -d "$internal_path" ]] && [[ ! -L "$internal_path" ]]; then
      echo "[MIGRATE] Moving existing $label from $internal_path to $volume_path"
      sudo rsync -a --remove-source-files "$internal_path/" "$volume_path/"
      sudo rm -rf "$internal_path"
    fi

    # Create parent directory if needed
    sudo mkdir -p "$(dirname "$internal_path")"

    # Create symlink
    if [[ ! -L "$internal_path" ]]; then
      sudo ln -s "$volume_path" "$internal_path"
      echo "[SYMLINK] $internal_path → $volume_path"
    else
      CURRENT_TARGET=$(readlink "$internal_path")
      if [[ "$CURRENT_TARGET" != "$volume_path" ]]; then
        sudo ln -sf "$volume_path" "$internal_path"
        echo "[UPDATED] $internal_path → $volume_path (was: $CURRENT_TARGET)"
      else
        echo "[SKIP] $internal_path → $volume_path (already correct)"
      fi
    fi
  }

  wire_symlink "/Library/Ollama/models"  "$OLLAMA_VOL_DIR"    "Ollama models"
  wire_symlink "/Library/RapidMLX/cache" "$RAPID_MLX_VOL_DIR" "Rapid-MLX cache"
  wire_symlink "/Library/MLX/models"     "$MLX_VOL_DIR"       "mlx-lm models"
  wire_symlink "/Library/Infinity"       "$INFINITY_VOL_DIR"  "Infinity models"

  echo ""
  echo "[OK] Symlink map:"
  echo "     /Library/Ollama/models  → $OLLAMA_VOL_DIR"
  echo "     /Library/MLX/models     → $MLX_VOL_DIR"
  echo "     /Library/Infinity       → $INFINITY_VOL_DIR"
  echo ""
  echo "     LaunchDaemon plists can use canonical /Library paths unchanged."
  echo "     All model I/O transparently goes to $VOLUME_MOUNT."
fi
```

### 10.7 fstab — Ensure Volume Mounts at Boot

For a LaunchDaemon to succeed, its model directory must exist before it starts. If the volume isn't mounted at boot, the daemon fails on the first start. The safest approach on macOS is to verify the volume has an `/etc/fstab` entry so macOS mounts it during early boot rather than waiting for Finder to trigger it.

```bash
# Get the volume UUID for fstab
VOL_UUID=$(diskutil info "$VOLUME_MOUNT" | grep "Volume UUID" | awk '{print $NF}')

if [[ -z "$VOL_UUID" ]]; then
  echo "WARNING: Could not determine UUID for $VOLUME_MOUNT — skipping fstab entry"
  echo "         Volume may not auto-mount before LaunchDaemons start on reboot"
else
  FSTAB_ENTRY="UUID=${VOL_UUID} ${VOLUME_MOUNT} apfs rw,auto,nobrowse 0 0"
  # nobrowse: volume won't appear in Finder sidebar (server-appropriate)
  # auto: mount at boot
  # rw: read-write

  if grep -q "$VOL_UUID" /etc/fstab 2>/dev/null; then
    echo "[SKIP] fstab entry for $VOLUME_LABEL already present"
  else
    echo "$FSTAB_ENTRY" | sudo tee -a /etc/fstab > /dev/null
    echo "[SET] fstab entry added for $VOLUME_LABEL (UUID: $VOL_UUID)"
    echo "      Volume will auto-mount at boot before LaunchDaemons start"
  fi
fi
```

### 10.8 Update config.json with Resolved Paths

After volume setup completes, update `config.json` with the resolved volume paths so `install-tools.sh` can pick them up:

```bash
# Write resolved paths back to config.json
# Only if symlink mode is OFF (if symlinks are on, /Library paths are used directly)
if [[ "$SYMLINK_INTERNAL" != "true" ]]; then
  CONFIG_PATH="$(dirname "$0")/config.json"
  TMP_CONFIG=$(mktemp)

  jq --arg ollama_dir "$OLLAMA_VOL_DIR" \
     --arg mlx_dir "$MLX_VOL_DIR" \
     --arg inf_model_path "$INFINITY_VOL_DIR" \
     '.tools.ollama.models_dir = $ollama_dir |
      .tools.mlx_lm.model_path = $mlx_dir |
      .tools.infinity.model_path = $inf_model_path |
      .storage.volume_mount_point = $ollama_dir' \
     "$CONFIG_PATH" > "$TMP_CONFIG"

  sudo mv "$TMP_CONFIG" "$CONFIG_PATH"
  echo "[SET] config.json updated with volume paths"
fi
```

### 10.9 Volume Setup Verification

```bash
echo ""
echo "=== STORAGE VOLUME VERIFICATION ==="

# Check directories exist and have correct ownership
for dir in "$OLLAMA_VOL_DIR" "$MLX_VOL_DIR" "$INFINITY_VOL_DIR"; do
  if [[ -d "$dir" ]]; then
    OWNER=$(stat -f "%Su:%Sg" "$dir")
    echo "[PASS] $dir exists (owner: $OWNER)"
  else
    echo "[FAIL] $dir missing"
  fi
done

# Check symlinks resolve correctly
if [[ "$SYMLINK_INTERNAL" == "true" ]]; then
  for link in "/Library/Ollama/models" "/Library/MLX/models"; do
    if [[ -L "$link" ]] && [[ -d "$link" ]]; then
      echo "[PASS] $link → $(readlink "$link")"
    elif [[ -L "$link" ]]; then
      echo "[FAIL] $link is a dangling symlink → $(readlink "$link")"
    fi
  done
fi

# Check Spotlight is off
MDUTIL_STATUS=$(mdutil -s "$VOLUME_MOUNT" 2>/dev/null)
if echo "$MDUTIL_STATUS" | grep -q "disabled\|off"; then
  echo "[PASS] Spotlight disabled on $VOLUME_MOUNT"
else
  echo "[WARN] Spotlight may still be active on $VOLUME_MOUNT"
fi

# Write volume info to precheck JSON for verify.sh consumption
if [[ -f /tmp/mac-llm-precheck.json ]]; then
  TMP=$(mktemp)
  jq --arg mount "$VOLUME_MOUNT" \
     --arg root "$MODEL_ROOT" \
     --argjson free "$VOL_FREE_GB" \
     '.storage.volume_configured = true |
      .storage.volume_mount = $mount |
      .storage.model_root = $root |
      .storage.free_gb = $free' \
     /tmp/mac-llm-precheck.json > "$TMP"
  mv "$TMP" /tmp/mac-llm-precheck.json
fi

echo ""
echo "storage-volume.sh complete. Run install-tools.sh next."
```

### 10.10 External Volume Known Issues

Add these to `docs/known-issues.md`:

| Issue | Impact | Fix |
|-------|--------|-----|
| Volume not mounted at boot | LaunchDaemon fails on first start after reboot | Add fstab entry via `storage-volume.sh` (§10.7) |
| USB drive I/O slower than NVMe | Model load time 2–5× longer on cold start | Use Thunderbolt enclosure for production; USB acceptable for development |
| `disksleep` re-enabled by macOS update | Drive spins down mid-inference | Re-run `setup.sh` after any macOS update; `caffeinate` daemon mitigates |
| ExFAT/FAT32 formatted drive | `root:wheel` ownership fails silently | Reformat as APFS: `diskutil eraseDisk APFS LLMStorage /dev/diskN` |
| Spotlight re-indexes after macOS update | `mds` competes for I/O | Re-run `sudo mdutil -i off <volume>` and verify `.metadata_never_index` present |
| Volume label has spaces | fstab and symlink paths break | Use labels without spaces: `LLMStorage` not `LLM Storage` |
| `nobrowse` hides volume from Finder | Admin can't browse models in Finder | Remove `nobrowse` from fstab if dual-use machine; server machines should keep it |
| Existing Ollama models on internal not migrated | Model library split across locations | `storage-volume.sh` auto-migrates if internal dir exists before symlinking |

---

*Specification built from: `headless_mac_setup_guide.md` (macOS 26 Tahoe, Mac Mini M4 64GB, June 2026) and `macos-headless-model-serving.md` (macOS 15 Sequoia, Mac Studio). Covers Ollama, mlx-lm, Infinity, and Exo.*
