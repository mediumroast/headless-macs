#!/usr/bin/env bash
# verify.sh — Health Check Report for Mac LLM Optimizer
#
# Runs pass/fail checks across system baseline and all enabled tools.
# Safe to run at any time — read-only, no changes made.
#
# Exit codes:
#   0 — all checks passed
#   1 — one or more failures
#   2 — warnings only, no failures

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

# ---------------------------------------------------------------------------
# Counters and helpers
# ---------------------------------------------------------------------------
FAILURES=0
WARNINGS=0

_pass() { echo "  [PASS] $*"; }
_fail() { echo "  [FAIL] $*"; FAILURES=$((FAILURES + 1)); }
_warn() { echo "  [WARN] $*"; WARNINGS=$((WARNINGS + 1)); }
_skip() { echo "  [SKIP] $*"; }
_info() { echo "         $*"; }

# Check a pmset value
check_pmset() {
  local key="$1" expected="$2"
  local actual
  actual=$(pmset -g | awk -v k="$key" '$1==k{print $2}' | head -1)
  if [[ "$actual" == "$expected" ]]; then
    _pass "pmset $key=$actual"
  else
    _fail "pmset $key=${actual:-unset}  (expected $expected)"
  fi
}

# Check a sysctl value
check_sysctl() {
  local key="$1" expected="$2"
  local actual
  actual=$(sysctl -n "$key" 2>/dev/null || echo "unset")
  if [[ "$actual" == "$expected" ]]; then
    _pass "sysctl $key=$actual"
  else
    _warn "sysctl $key=${actual}  (expected $expected — reboot may be needed)"
  fi
}

# Check a launchd daemon is running
check_daemon() {
  local label="$1"
  if sudo launchctl print "system/${label}" 2>/dev/null | grep -q "state = running"; then
    _pass "$label running"
  else
    _fail "$label not running"
    _info "Diagnose: sudo launchctl print system/${label}"
  fi
}

# Check an HTTP endpoint
check_http() {
  local name="$1" url="$2" pattern="$3" timeout="${4:-5}"
  if curl -sf --max-time "$timeout" "$url" 2>/dev/null | grep -q "$pattern"; then
    _pass "$name API responding ($url)"
  else
    _fail "$name API not responding ($url)"
  fi
}

# ---------------------------------------------------------------------------
# Header
# ---------------------------------------------------------------------------
OS_VERSION=$(sw_vers -productVersion)
HW_MODEL=$(sysctl -n hw.model 2>/dev/null || echo "unknown")
RAM_GB=$(( $(sysctl -n hw.memsize) / 1024 / 1024 / 1024 ))
SIP_RAW=$(csrutil status 2>/dev/null || echo "unknown")
SIP_STATE="enabled"
echo "$SIP_RAW" | grep -q "disabled" && SIP_STATE="disabled"

echo "=== Mac LLM Optimizer — Health Report ==="
echo "Timestamp: $(date)"
echo "Hardware:  $HW_MODEL | ${RAM_GB}GB RAM | macOS $OS_VERSION"
echo "SIP:       $SIP_STATE"
echo ""

# ---------------------------------------------------------------------------
# SYSTEM — power, caffeinate, Spotlight, SSH
# ---------------------------------------------------------------------------
echo "--- SYSTEM ---"

check_pmset "sleep"         "0"
check_pmset "disksleep"     "0"
check_pmset "standby"       "0"
check_pmset "womp"          "1"
check_pmset "tcpkeepalive"  "1"
check_pmset "autorestart"   "1"

# Caffeinate daemon
if sudo launchctl print "system/com.llm-server.caffeinate" 2>/dev/null | grep -q "state = running"; then
  _pass "caffeinate daemon running"
else
  _warn "caffeinate daemon not running — sleep regression safety net missing"
  _info "Fix: sudo launchctl bootstrap system /Library/LaunchDaemons/com.llm-server.caffeinate.plist"
fi

# sysctl-tuning daemon
if sudo launchctl print "system/com.llm-server.sysctl-tuning" 2>/dev/null | grep -q "state = running\|last exit code = 0"; then
  _pass "sysctl-tuning daemon present (network tuning persists across reboots)"
else
  _warn "sysctl-tuning daemon not found — network tuning will not survive reboot"
  _info "Fix: sudo launchctl bootstrap system /Library/LaunchDaemons/com.llm-server.sysctl-tuning.plist"
fi

# Spotlight
MDUTIL_OUT=$(mdutil -s / 2>/dev/null || echo "")
if echo "$MDUTIL_OUT" | grep -qi "disabled\|off"; then
  _pass "Spotlight indexing disabled on /"
else
  _warn "Spotlight indexing may be active — run: sudo mdutil -a -i off"
fi

# SSH
if lsof -iTCP:22 -sTCP:LISTEN &>/dev/null 2>&1; then
  _pass "SSH enabled (port 22 listening)"
else
  _warn "SSH not enabled"
  _info "Fix (macOS 26+): sudo launchctl enable system/com.openssh.sshd && sudo launchctl kickstart -k system/com.openssh.sshd"
  _info "Fix (older macOS): sudo systemsetup -setremotelogin on"
fi

# sshd drop-in hardening
SSHD_DROPIN="/etc/ssh/sshd_config.d/100-headless.conf"
if [[ -f "$SSHD_DROPIN" ]]; then
  _pass "sshd drop-in present ($SSHD_DROPIN)"
  if grep -q "^PasswordAuthentication no" "$SSHD_DROPIN" 2>/dev/null; then
    _pass "sshd: PasswordAuthentication no (key-only login enforced)"
  else
    _warn "sshd: PasswordAuthentication no not set — password login still permitted"
    _info "Copy your public key to ~/.ssh/authorized_keys then re-run setup.sh"
  fi
else
  _warn "sshd drop-in not found — SSH hardening not applied"
  _info "Fix: sudo ./setup.sh"
fi

# _llmserver service account
if id "_llmserver" &>/dev/null 2>&1; then
  _pass "_llmserver service account exists (serving daemons run unprivileged)"
else
  _warn "_llmserver service account missing — serving daemons will run as root"
  _info "Fix: sudo ./install-tools.sh"
fi

# MacBook clamshell reminder
if sysctl -n hw.model 2>/dev/null | grep -qiE "MacBook"; then
  _warn "MacBook detected — confirm HDMI dummy plug is connected for headless operation"
fi

echo ""

# ---------------------------------------------------------------------------
# NETWORK — sysctl values
# ---------------------------------------------------------------------------
echo "--- NETWORK ---"

NETWORK_TUNING=$(echo "$CONFIG" | jq -r '.system.network_tuning // true')
if [[ "$NETWORK_TUNING" == "true" ]]; then
  check_sysctl "net.inet.tcp.sendspace"     "1048576"
  check_sysctl "net.inet.tcp.recvspace"     "1048576"
  check_sysctl "kern.ipc.maxsockbuf"        "8388608"
  check_sysctl "net.inet.tcp.autorcvbufmax" "8388608"
  check_sysctl "net.inet.tcp.autosndbufmax" "8388608"
  check_sysctl "kern.ipc.somaxconn"         "2048"
else
  _skip "Network tuning disabled in config.json"
fi

# File descriptor limits
MAXFILES_SOFT=$(launchctl limit maxfiles 2>/dev/null | awk '{print $2}')
if [[ -n "$MAXFILES_SOFT" ]] && [[ "$MAXFILES_SOFT" -ge 524288 ]] 2>/dev/null; then
  _pass "maxfiles soft limit: $MAXFILES_SOFT"
else
  _warn "maxfiles soft limit low (${MAXFILES_SOFT:-unknown}) — may exhaust under parallel inference"
  _info "Fix: sudo launchctl bootstrap system /Library/LaunchDaemons/com.llm-server.maxfiles.plist"
fi

echo ""

# ---------------------------------------------------------------------------
# STORAGE
# ---------------------------------------------------------------------------
echo "--- STORAGE ---"

USE_EXTERNAL_VOLUME=$(echo "$CONFIG" | jq -r '.storage.use_external_volume // false')
if [[ "$USE_EXTERNAL_VOLUME" == "true" ]]; then
  STORAGE_LABEL=$(echo "$CONFIG" | jq -r '.storage.volume_label // "LLMStorage"')
  STORAGE_MOUNT=$(echo "$CONFIG" | jq -r '.storage.volume_mount_point // empty')
  SYMLINK_INTERNAL=$(echo "$CONFIG" | jq -r '.storage.symlink_internal_paths // true')
  [[ -z "$STORAGE_MOUNT" ]] && STORAGE_MOUNT="/Volumes/${STORAGE_LABEL}"

  if mount | grep -Fq " on ${STORAGE_MOUNT} "; then
    _pass "External volume mounted at ${STORAGE_MOUNT}"
  else
    _fail "External volume not mounted at ${STORAGE_MOUNT}"
    _info "Fix: sudo diskutil mount -mountPoint '${STORAGE_MOUNT}' '${STORAGE_LABEL}'"
  fi

  STORAGE_INFO=$(diskutil info "$STORAGE_LABEL" 2>/dev/null || diskutil info "$STORAGE_MOUNT" 2>/dev/null || true)
  STORAGE_OWNERS=$(echo "$STORAGE_INFO" | awk -F': *' '/Owners/{print $2; exit}')
  if [[ "$STORAGE_OWNERS" == "Enabled" ]]; then
    _pass "External volume ownership enabled"
  else
    _fail "External volume ownership disabled"
    _info "Fix: sudo diskutil enableOwnership '${STORAGE_MOUNT}'"
  fi

  if sudo launchctl print system/com.llm-server.storage-mount >/dev/null 2>&1; then
    _pass "storage-mount LaunchDaemon installed"
  else
    _fail "storage-mount LaunchDaemon missing"
    _info "Fix: sudo ./storage-volume.sh"
  fi

  if [[ "$SYMLINK_INTERNAL" == "true" ]]; then
    if [[ -L "/Library/Ollama/models" ]]; then
      _pass "/Library/Ollama/models symlink present"
      _info "Target: $(readlink /Library/Ollama/models 2>/dev/null || echo unknown)"
    else
      _fail "/Library/Ollama/models is not symlinked to external storage"
      _info "Fix: sudo ./storage-volume.sh"
    fi
  else
    _skip "Internal /Library symlink wiring disabled in config.json"
  fi
else
  _skip "External storage disabled in config.json"
fi

echo ""

# ---------------------------------------------------------------------------
# OLLAMA
# ---------------------------------------------------------------------------
echo "--- OLLAMA ---"

if [[ "$(echo "$CONFIG" | jq -r '.tools.ollama.enabled')" == "true" ]]; then
  check_daemon "com.ollama.server"
  check_http "Ollama" "http://localhost:11434/api/tags" "models"

  # Model count
  MODEL_COUNT=$(curl -sf --max-time 5 http://localhost:11434/api/tags 2>/dev/null \
    | jq '.models | length' 2>/dev/null || echo "0")
  if [[ "$MODEL_COUNT" -gt 0 ]]; then
    _pass "Models loaded: $MODEL_COUNT"
    curl -sf --max-time 5 http://localhost:11434/api/tags 2>/dev/null \
      | jq -r '.models[].name' 2>/dev/null \
      | while IFS= read -r m; do _info "Loaded: $m"; done
  else
    _warn "No models pulled yet — run: ollama pull <model>"
  fi
else
  _skip "Not enabled in config.json"
fi

echo ""

# ---------------------------------------------------------------------------
# RAPID-MLX
# ---------------------------------------------------------------------------
echo "--- RAPID-MLX ---"

if [[ "$(echo "$CONFIG" | jq -r '.tools.rapid_mlx.enabled')" == "true" ]]; then
  check_daemon "com.rapid-mlx.server"
  RMLX_PORT=$(echo "$CONFIG" | jq -r '.tools.rapid_mlx.port')
  check_http "Rapid-MLX" "http://localhost:${RMLX_PORT}/v1/models" "." 15
else
  _skip "Not enabled in config.json"
fi

echo ""

# ---------------------------------------------------------------------------
# MLX-LM
# ---------------------------------------------------------------------------
echo "--- MLX-LM ---"

if [[ "$(echo "$CONFIG" | jq -r '.tools.mlx_lm.enabled')" == "true" ]]; then
  MLX_MODEL=$(echo "$CONFIG" | jq -r '.tools.mlx_lm.default_model')
  if [[ -z "$MLX_MODEL" ]]; then
    _warn "No default_model set in config.json — daemon not started"
  else
    MLX_PORT=$(echo "$CONFIG" | jq -r '.tools.mlx_lm.port')
    check_daemon "com.mlx-lm.server"
    check_http "mlx-lm" "http://localhost:${MLX_PORT}/v1/models" "." 10
  fi
else
  _skip "Not enabled in config.json"
fi

echo ""

# ---------------------------------------------------------------------------
# INFINITY
# ---------------------------------------------------------------------------
echo "--- INFINITY ---"

if [[ "$(echo "$CONFIG" | jq -r '.tools.infinity.enabled')" == "true" ]]; then
  INF_PORT=$(echo "$CONFIG" | jq -r '.tools.infinity.port')
  check_daemon "com.infinity.server"
  check_http "Infinity" "http://localhost:${INF_PORT}/health" "." 10
else
  _skip "Not enabled in config.json"
fi

echo ""

# ---------------------------------------------------------------------------
# EXO
# ---------------------------------------------------------------------------
echo "--- EXO ---"

if [[ "$(echo "$CONFIG" | jq -r '.tools.exo.enabled')" == "true" ]]; then
  EXO_PORT=$(echo "$CONFIG" | jq -r '.tools.exo.chatgpt_api_port')
  if launchctl print "gui/$(id -u)/com.exo.node" 2>/dev/null | grep -q "state = running"; then
    _pass "com.exo.node running"
  else
    _fail "com.exo.node not running"
  fi
  check_http "Exo" "http://localhost:${EXO_PORT}/v1/models" "." 10
else
  _skip "Not enabled in config.json"
fi

echo ""

# ---------------------------------------------------------------------------
# Memory pressure
# ---------------------------------------------------------------------------
echo "--- MEMORY ---"

MEM_FREE=$(memory_pressure 2>/dev/null \
  | awk '/System-wide memory free percentage/{gsub(/%/,"",$NF); print $NF}')

if [[ -n "$MEM_FREE" ]]; then
  if [[ "$MEM_FREE" -gt 20 ]]; then
    _pass "Memory pressure healthy (${MEM_FREE}% free)"
  elif [[ "$MEM_FREE" -gt 10 ]]; then
    _warn "Memory pressure elevated (${MEM_FREE}% free) — consider fewer loaded models"
  else
    _warn "Memory pressure critical (${MEM_FREE}% free) — inference may degrade"
  fi
else
  _warn "Could not read memory pressure"
fi

echo ""

# ---------------------------------------------------------------------------
# Result
# ---------------------------------------------------------------------------
echo "=== RESULT: ${WARNINGS} warning(s), ${FAILURES} failure(s) ==="
echo ""

if [[ $FAILURES -gt 0 ]]; then
  echo "Check log files:"
  [[ "$(echo "$CONFIG" | jq -r '.tools.ollama.enabled')"    == "true" ]] && \
    echo "  Ollama:    tail -f /var/log/ollama/stderr.log"
  [[ "$(echo "$CONFIG" | jq -r '.tools.rapid_mlx.enabled')" == "true" ]] && \
    echo "  Rapid-MLX: tail -f /var/log/rapid-mlx/stderr.log"
  [[ "$(echo "$CONFIG" | jq -r '.tools.mlx_lm.enabled')"   == "true" ]] && \
    echo "  mlx-lm:    tail -f /var/log/mlx-lm/stderr.log"
  [[ "$(echo "$CONFIG" | jq -r '.tools.infinity.enabled')"  == "true" ]] && \
    echo "  Infinity:  tail -f /var/log/infinity/stderr.log"
  echo ""
  exit 1   # exit 1 = failures (warnings don't change this)
fi

[[ $WARNINGS -gt 0 ]] && exit 2   # exit 2 = warnings only, no failures
exit 0
