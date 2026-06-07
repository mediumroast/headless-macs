#!/usr/bin/env bash
# precheck.sh — Read-only system audit for Mac LLM Optimizer
#
# Run this FIRST before any other script. Requires no sudo. Makes zero changes.
# Produces a human-readable report and writes /tmp/mac-llm-precheck.json for
# downstream scripts (setup.sh, install-tools.sh, storage-volume.sh, verify.sh) to consume.
#
# Exit codes:
#   0 — all clear, ready to proceed
#   1 — hard blockers found (Intel CPU, FileVault on, no Homebrew, critical low disk)
#   2 — warnings only (SIP on, low RAM, no external volume, no auto-login)

set -uo pipefail

# ---------------------------------------------------------------------------
# Guard: Apple Silicon only
# ---------------------------------------------------------------------------
ARCH=$(uname -m)
if [[ "$ARCH" != "arm64" ]]; then
  echo "ERROR: This toolset requires Apple Silicon (arm64). Detected: $ARCH"
  exit 1
fi

# Guard: macOS only
if [[ "$(uname -s)" != "Darwin" ]]; then
  echo "ERROR: macOS required."
  exit 1
fi

# ---------------------------------------------------------------------------
# Globals populated by each section
# ---------------------------------------------------------------------------
BLOCKERS=0
WARNINGS=0

# Hardware
HW_MODEL=""
CHIP=""
RAM_GB=0
IS_LAPTOP=false
PERF_CORES=0
EFF_CORES="N/A"
CAPABILITY=""

# Security
SIP_STATE="unknown"
FV_STATE="unknown"
AL_USER=""

# Storage
BOOT_FREE=0
LABEL_MOUNT=""

# Config
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CONFIG_FILE="$SCRIPT_DIR/config.json"

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------
_header() { echo ""; echo "=== $1 ==="; }
_ok()   { echo "  [OK]      $*"; }
_warn() { echo "  [WARN]    $*"; WARNINGS=$((WARNINGS + 1)); }
_fail() { echo "  [BLOCKER] $*"; BLOCKERS=$((BLOCKERS + 1)); }
_info() { echo "  $*"; }

# ---------------------------------------------------------------------------
# Section 1: Hardware Identity
# ---------------------------------------------------------------------------
section_hardware() {
  _header "HARDWARE"

  CHIP=$(sysctl -n machdep.cpu.brand_string 2>/dev/null \
    || system_profiler SPHardwareDataType 2>/dev/null \
       | awk -F': ' '/Chip/{print $2}' \
    || echo "unknown")
  _info "Chip:        $CHIP"
  _info "Arch:        $ARCH"

  RAM_GB=$(( $(sysctl -n hw.memsize) / 1024 / 1024 / 1024 ))
  _info "RAM:         ${RAM_GB} GB"

  HW_MODEL=$(sysctl -n hw.model 2>/dev/null || echo "unknown")
  _info "Model:       $HW_MODEL"

  if echo "$HW_MODEL" | grep -qiE "MacBook"; then
    IS_LAPTOP=true
    _info "Form factor: Laptop — lid/battery/thermal notes apply"
  else
    IS_LAPTOP=false
    _info "Form factor: Desktop — simplified headless setup"
  fi

  PERF_CORES=$(sysctl -n hw.perflevel0.logicalcpu 2>/dev/null \
    || sysctl -n hw.logicalcpu 2>/dev/null \
    || echo "unknown")
  EFF_CORES=$(sysctl -n hw.perflevel1.logicalcpu 2>/dev/null || echo "N/A")
  _info "CPU cores:   ${PERF_CORES} performance, ${EFF_CORES} efficiency"
}

# ---------------------------------------------------------------------------
# Section 2: Model Capability (RAM-based)
# ---------------------------------------------------------------------------
section_capability() {
  _header "MODEL CAPABILITY"

  if   [[ $RAM_GB -le 8  ]]; then
    echo "  [WARN] ${RAM_GB}GB RAM: below practical minimum for LLM inference"
    echo "         Minimum viable: 3B Q8 (~4GB). Embedding-only workloads feasible."
    CAPABILITY="minimal"
    WARNINGS=$((WARNINGS + 1))
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

  echo ""
  echo "  Recommended models for this hardware:"
  case $CAPABILITY in
    minimal)
      _info "• ollama pull llama3.2:3b-instruct-q8_0"
      ;;
    7b)
      _info "• ollama pull qwen2.5-coder:7b-instruct-q8_0"
      _info "• ollama pull nomic-embed-text"
      ;;
    13b)
      _info "• ollama pull qwen2.5-coder:7b-instruct-q8_0"
      _info "• ollama pull mxbai-embed-large"
      _info "• ollama pull llama3.1:8b-instruct-q8_0"
      ;;
    32b)
      _info "• ollama pull qwen2.5-coder:32b-instruct-q5_K_M"
      _info "• ollama pull nomic-embed-text"
      ;;
    70b|70b-q8|405b)
      _info "• ollama pull qwen2.5:72b-instruct-q4_K_M"
      _info "• ollama pull qwen2.5-coder:32b-instruct-q5_K_M"
      _info "• ollama pull mxbai-embed-large"
      ;;
  esac
}

# ---------------------------------------------------------------------------
# Section 3: macOS and Security State
# ---------------------------------------------------------------------------
section_security() {
  _header "MACOS & SECURITY"

  local os_version os_name
  os_version=$(sw_vers -productVersion 2>/dev/null || echo "unknown")
  os_name=$(sw_vers -productName 2>/dev/null || echo "macOS")
  _info "OS:          $os_name $os_version"

  # SIP status (csrutil may need sudo on some configs; degrade gracefully)
  local sip_raw
  sip_raw=$(csrutil status 2>/dev/null || echo "unknown")
  if echo "$sip_raw" | grep -q "enabled"; then
    echo "  SIP:         ENABLED — persistent service disabling will not survive reboots"
    echo "               To disable: boot Recovery Mode → Terminal → 'csrutil disable'"
    SIP_STATE="enabled"
    WARNINGS=$((WARNINGS + 1))
  elif echo "$sip_raw" | grep -q "disabled"; then
    echo "  SIP:         disabled ✓ — full service suppression available"
    SIP_STATE="disabled"
  else
    echo "  SIP:         unknown (run as admin to check)"
    SIP_STATE="unknown"
  fi

  # FileVault — hard blocker for headless reboots
  local fv_raw
  fv_raw=$(fdesetup status 2>/dev/null || echo "unknown")
  if echo "$fv_raw" | grep -qi "FileVault is On"; then
    echo "  FileVault:   ENABLED ⚠ BLOCKER — headless reboots will hang at password prompt"
    echo "               Fix: System Settings → Privacy & Security → FileVault → Turn Off"
    FV_STATE="on"
    BLOCKERS=$((BLOCKERS + 1))
  elif echo "$fv_raw" | grep -qi "FileVault is Off"; then
    echo "  FileVault:   off ✓"
    FV_STATE="off"
  else
    echo "  FileVault:   unknown"
    FV_STATE="unknown"
  fi

  # Auto-login (required for Exo; good practice for all headless use)
  AL_USER=$(defaults read /Library/Preferences/com.apple.loginwindow autoLoginUser 2>/dev/null || echo "")
  if [[ -n "$AL_USER" ]]; then
    echo "  Auto-login:  configured ($AL_USER) ✓"
  else
    echo "  Auto-login:  NOT configured — required for Exo; recommended for all headless"
    echo "               Fix: sudo sysadminctl -autologin set -userName <user> -password <pw>"
    WARNINGS=$((WARNINGS + 1))
  fi

  # Xcode CLT
  if xcode-select -p &>/dev/null; then
    echo "  Xcode CLT:   installed ($(xcode-select -p))"
  else
    echo "  Xcode CLT:   NOT installed — required for Homebrew and build tools"
    echo "               setup.sh will install via softwareupdate (headless-safe)"
    WARNINGS=$((WARNINGS + 1))
  fi
}

# ---------------------------------------------------------------------------
# Section 4: Tool Prerequisites
# ---------------------------------------------------------------------------
section_prerequisites() {
  _header "TOOL PREREQUISITES"

  _check_binary() {
    local name="$1" cmd="$2"
    if command -v "$cmd" &>/dev/null; then
      echo "  [PRESENT] $name: $(command -v "$cmd")"
    else
      echo "  [MISSING] $name"
    fi
  }

  _check_binary "Homebrew"  "brew"
  _check_binary "Python 3"  "python3"
  _check_binary "pip3"      "pip3"
  _check_binary "jq"        "jq"
  _check_binary "curl"      "curl"
  _check_binary "git"       "git"

  # Tools that may or may not be installed yet
  _check_binary "Ollama"    "ollama"
  _check_binary "Rapid-MLX" "rapid-mlx"

  # mlx_lm is a Python module, not a standalone binary
  if python3 -c "import mlx_lm" 2>/dev/null; then
    echo "  [PRESENT] mlx_lm (Python module)"
  else
    echo "  [MISSING] mlx_lm (Python module) — install via: pip3 install mlx-lm"
  fi

  # Homebrew is a hard blocker — everything else installs through it
  if ! command -v brew &>/dev/null; then
    BLOCKERS=$((BLOCKERS + 1))
    echo "  [BLOCKER] Homebrew not installed — required for all tool installation"
    echo "            Install: /bin/bash -c \"\$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)\""
  fi

  # Python version check (mlx-lm and Infinity require 3.10+)
  if command -v python3 &>/dev/null; then
    local py_version py_major py_minor
    py_version=$(python3 --version 2>/dev/null | awk '{print $2}')
    py_major=$(echo "$py_version" | cut -d. -f1)
    py_minor=$(echo "$py_version" | cut -d. -f2)
    if [[ "${py_major:-0}" -ge 3 ]] && [[ "${py_minor:-0}" -ge 10 ]]; then
      echo "  Python:      $py_version ✓ (mlx-lm requires 3.10+)"
    else
      echo "  Python:      $py_version ⚠ — mlx-lm and Infinity require Python 3.10+"
      echo "               Fix: brew install python@3.12"
      WARNINGS=$((WARNINGS + 1))
    fi
  fi

  # Ollama running detection — distinguish app vs daemon
  if pgrep -x "ollama" &>/dev/null; then
    local ollama_pid ollama_user
    ollama_pid=$(pgrep -x "ollama" | head -1)
    ollama_user=$(ps -o user= -p "$ollama_pid" 2>/dev/null || echo "unknown")
    if [[ "$ollama_user" == "root" ]]; then
      echo "  Ollama:      running as root (LaunchDaemon) ✓"
    else
      echo "  Ollama:      running as user '$ollama_user' (app/login item)"
      echo "               install-tools.sh will convert to a LaunchDaemon"
    fi
  fi
}

# ---------------------------------------------------------------------------
# Section 5: Network and Ports
# ---------------------------------------------------------------------------
section_network() {
  _header "NETWORK & PORTS"

  echo "  IP addresses:"
  ifconfig 2>/dev/null \
    | awk '/inet / && !/127\.0\.0\.1/{print "    " $2}' \
    || echo "    (unable to read network interfaces)"

  echo ""

  _check_port() {
    local name="$1" port="$2"
    if lsof -iTCP:"$port" -sTCP:LISTEN &>/dev/null 2>&1; then
      local pid proc
      # -Fpc gives field-format output: p<pid> then c<command> — avoids space/escape issues
      local lsof_out
      lsof_out=$(lsof -Fpc -iTCP:"$port" -sTCP:LISTEN 2>/dev/null | head -4)
      pid=$(echo "$lsof_out"   | grep '^p' | head -1 | cut -c2-)
      proc=$(echo "$lsof_out" | grep '^c' | head -1 | cut -c2-)
      echo "  Port $port  ($name): IN USE by ${proc:-unknown} (pid $pid)"
    else
      echo "  Port $port  ($name): available ✓"
    fi
  }

  _check_port "Ollama    " 11434
  _check_port "Rapid-MLX " 8000
  _check_port "mlx-lm    " 8080
  _check_port "Infinity  " 7997
  _check_port "Exo       " 52415

  echo ""

  # SSH status — check port 22 directly; avoids sudo requirement
  if lsof -iTCP:22 -sTCP:LISTEN &>/dev/null 2>&1; then
    echo "  SSH:         enabled (sshd listening on port 22) ✓"
  else
    echo "  SSH:         disabled — enable with: sudo systemsetup -setremotelogin on"
  fi

  # Application Firewall state — check multiple pref locations (macOS 26 moved it)
  local fw_state
  fw_state=$(defaults read /Library/Preferences/com.apple.alf globalstate 2>/dev/null \
    || defaults read /Library/Preferences/com.apple.ApplicationFirewall globalstate 2>/dev/null \
    || echo "unknown")
  case "$fw_state" in
    0) echo "  Firewall:    off — consider enabling if API ports are network-accessible" ;;
    1) echo "  Firewall:    on (essential services only)" ;;
    2) echo "  Firewall:    on (block all incoming) — may need rules for ports 11434, 7997, etc." ;;
    *) echo "  Firewall:    unknown (macOS 26 may require 'socketfilterfw --getglobalstate')" ;;
  esac
}

# ---------------------------------------------------------------------------
# Section 6: Storage
# ---------------------------------------------------------------------------
section_storage() {
  _header "STORAGE"

  # Boot volume
  local boot_total boot_used
  boot_total=$(df -g / 2>/dev/null | awk 'NR==2{print $2}')
  boot_used=$(df -g  / 2>/dev/null | awk 'NR==2{print $3}')
  BOOT_FREE=$(df -g  / 2>/dev/null | awk 'NR==2{print $4}')

  local boot_disk
  boot_disk=$(df / 2>/dev/null | awk 'NR==2{print $1}')

  echo "  Boot volume: $boot_disk"
  echo "    Total: ${boot_total}GB | Used: ${boot_used}GB | Free: ${BOOT_FREE}GB"

  if   [[ $BOOT_FREE -ge 500 ]]; then
    echo "    Assessment: ample space — internal storage viable for model library"
  elif [[ $BOOT_FREE -ge 100 ]]; then
    echo "    Assessment: moderate space — room for a few large models"
  elif [[ $BOOT_FREE -ge 50 ]]; then
    echo "    Assessment: tight — consider external volume for model storage"
  else
    echo "    Assessment: CRITICAL LOW SPACE ⚠ — external volume strongly recommended"
  fi

  # All mounted volumes — filter out APFS system sub-volumes and virtual filesystems
  echo ""
  echo "  All mounted volumes:"
  df -g 2>/dev/null \
    | grep -v "tmpfs\|devfs\|map \|Filesystem\|/System/Volumes/" \
    | awk '{printf "    %-40s %4dGB free / %4dGB total\n", $NF, $4, $2}'

  # External volumes — use Device Location field to distinguish internal vs external
  echo ""
  echo "  External volumes:"
  local external_found=false

  while IFS= read -r disk_line; do
    local disk
    disk=$(echo "$disk_line" | awk '{print $1}')

    local disk_info location disk_size disk_protocol
    disk_info=$(diskutil info "$disk" 2>/dev/null)
    location=$(echo "$disk_info" | awk '/Device Location/{print $NF}')
    [[ "$location" != "External" ]] && continue

    disk_size=$(echo "$disk_info" | awk '/Disk Size/{print $3, $4}')
    disk_protocol=$(echo "$disk_info" | awk '/Device Protocol/{print $NF}')

    [[ -z "$disk_size" ]] && continue
    echo "    Disk: $disk — $disk_size — Protocol: ${disk_protocol:-unknown}"
    external_found=true

    while IFS= read -r part_line; do
      local part_id mount part_name part_size
      part_id=$(echo "$part_line" | grep -oE "disk[0-9]+s[0-9]+" | tail -1)
      [[ -z "$part_id" ]] && continue
      mount=$(diskutil info "/dev/$part_id" 2>/dev/null \
        | awk -F': ' '/Mount Point/{gsub(/^[[:space:]]+/,"",$2); print $2}')
      part_name=$(echo "$part_line" | awk '{print $2}')
      part_size=$(echo "$part_line" | awk '{print $(NF-1), $NF}')

      if [[ -n "$mount" && "$mount" != "(null)" && "$mount" != "" ]]; then
        local part_free
        part_free=$(df -g "$mount" 2>/dev/null | awk 'NR==2{print $4}')
        echo "      Volume: $part_name | $part_size | Mounted: $mount | Free: ${part_free}GB"
        case "${disk_protocol:-}" in
          USB)          echo "        ⚠  USB — adequate for cold storage; I/O slower than internal NVMe" ;;
          Thunderbolt*) echo "        ✓  Thunderbolt — fast enough for inference I/O" ;;
          PCI*)         echo "        ✓  PCIe — fast enough for inference I/O" ;;
        esac
      else
        echo "      Volume: $part_name | $part_size | NOT MOUNTED"
      fi
    done < <(diskutil list "$disk" 2>/dev/null \
      | grep -E "Apple_APFS|Apple_HFS|ExFAT|FAT32|NTFS")

  done < <(diskutil list 2>/dev/null | grep -E "^/dev/disk[0-9]+\s")

  if [[ "$external_found" != true ]]; then
    echo "    None detected — only internal storage present"
  fi

  # Check for config-specified volume label
  local cfg_label="LLMStorage"
  if [[ -f "$CONFIG_FILE" ]] && command -v jq &>/dev/null; then
    cfg_label=$(jq -r '.storage.volume_label // "LLMStorage"' "$CONFIG_FILE" 2>/dev/null || echo "LLMStorage")
  fi

  LABEL_MOUNT=$(diskutil info "$cfg_label" 2>/dev/null \
    | awk -F': ' '/Mount Point/{gsub(/^[[:space:]]+/,"",$2); print $2}')

  echo ""
  if [[ -n "$LABEL_MOUNT" && "$LABEL_MOUNT" != "(null)" ]]; then
    local label_free
    label_free=$(df -g "$LABEL_MOUNT" 2>/dev/null | awk 'NR==2{print $4}')
    echo "  ✓ Volume matching config label '$cfg_label' found at: $LABEL_MOUNT (${label_free}GB free)"
    echo "    storage-volume.sh will use this volume automatically."
  else
    echo "  No volume with label '$cfg_label' detected."
    echo "  Options:"
    echo "    1. Attach an external drive and format: diskutil eraseDisk APFS '$cfg_label' /dev/diskN"
    echo "    2. Change storage.volume_label in config.json to match an existing volume"
    echo "    3. Set storage.use_external_volume: false to use internal storage only"
    LABEL_MOUNT=""
  fi
}

# ---------------------------------------------------------------------------
# Section 7: Current Power Management State
# ---------------------------------------------------------------------------
section_power() {
  _header "CURRENT POWER STATE"

  pmset -g 2>/dev/null \
    | grep -E "^\s*(sleep|disksleep|disablesleep|standby|powernap|powermode|SleepDisabled)" \
    | while IFS= read -r line; do
        local key val
        key=$(echo "$line" | awk '{print $1}')
        val=$(echo "$line" | awk '{print $2}')
        case "$key" in
          sleep)       [[ "$val" != "0" ]] && echo "  [CHANGE NEEDED] $line" || echo "  [OK] $line" ;;
          disksleep)   [[ "$val" != "0" ]] && echo "  [CHANGE NEEDED] $line" || echo "  [OK] $line" ;;
          disablesleep)[[ "$val" != "1" ]] && echo "  [CHANGE NEEDED] $line" || echo "  [OK] $line" ;;
          powermode)   [[ "$val" != "2" ]] && echo "  [CHANGE NEEDED] $line  (need 2 for High Performance)" || echo "  [OK] $line" ;;
          *)           echo "  $line" ;;
        esac
      done

  echo ""
  echo "  Active sleep prevention assertions:"
  pmset -g assertions 2>/dev/null \
    | grep -E "PreventSystemSleep|PreventUserIdleSystemSleep" \
    | head -10 \
    || echo "    None active"
}

# ---------------------------------------------------------------------------
# Section 8: Readiness Summary + JSON output
# ---------------------------------------------------------------------------
section_summary() {
  _header "READINESS SUMMARY"

  # Blocker: low disk space
  [[ $BOOT_FREE -lt 20 ]] && { _fail "Less than 20GB free on boot volume — cannot safely install"; }

  # Warning: tight disk
  [[ $BOOT_FREE -lt 50 ]] && [[ $BOOT_FREE -ge 20 ]] && \
    { echo "  [WARN]    Boot volume has < 50GB free — consider external volume for models"; WARNINGS=$((WARNINGS + 1)); }

  # Warning: laptop without dummy plug reminder
  [[ "$IS_LAPTOP" == true ]] && \
    { echo "  [WARN]    Laptop — verify HDMI dummy plug connected before going headless"; WARNINGS=$((WARNINGS + 1)); }

  echo ""
  if [[ $BLOCKERS -eq 0 ]] && [[ $WARNINGS -eq 0 ]]; then
    echo "  ✓ All clear — ready to run setup.sh"
  elif [[ $BLOCKERS -eq 0 ]]; then
    echo "  ✓ Can proceed with ${WARNINGS} warning(s) — review above before continuing"
  else
    echo "  ✗ ${BLOCKERS} blocker(s) must be resolved before proceeding"
    [[ $WARNINGS -gt 0 ]] && echo "    (also ${WARNINGS} warning(s))"
  fi

  # JSON output for downstream scripts
  local can_proceed="true"
  [[ $BLOCKERS -gt 0 ]] && can_proceed="false"

  local label_found="false"
  [[ -n "$LABEL_MOUNT" ]] && label_found="true"

  cat > /tmp/mac-llm-precheck.json <<JSONEOF
{
  "timestamp": "$(date -u +%Y-%m-%dT%H:%M:%SZ)",
  "hardware": {
    "model": "$HW_MODEL",
    "chip": "$CHIP",
    "arch": "$ARCH",
    "ram_gb": $RAM_GB,
    "is_laptop": $IS_LAPTOP,
    "perf_cores": "$PERF_CORES",
    "eff_cores": "$EFF_CORES"
  },
  "capability": "$CAPABILITY",
  "security": {
    "sip": "$SIP_STATE",
    "filevault": "$FV_STATE",
    "auto_login_user": "${AL_USER:-null}"
  },
  "storage": {
    "boot_free_gb": $BOOT_FREE,
    "external_volume_label_found": $label_found,
    "external_volume_mount": "${LABEL_MOUNT:-null}"
  },
  "readiness": {
    "blockers": $BLOCKERS,
    "warnings": $WARNINGS,
    "can_proceed": $can_proceed
  }
}
JSONEOF

  echo ""
  echo "  JSON summary written to: /tmp/mac-llm-precheck.json"
}

# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------
main() {
  echo "=== Mac LLM Optimizer — System Precheck ==="
  echo "Timestamp: $(date)"
  echo "Hardware:  $(sysctl -n hw.model 2>/dev/null || echo unknown) | $(( $(sysctl -n hw.memsize) / 1024 / 1024 / 1024 ))GB RAM | macOS $(sw_vers -productVersion 2>/dev/null || echo unknown)"
  echo ""
  echo "NOTE: This script is read-only. No changes will be made."

  section_hardware
  section_capability
  section_security
  section_prerequisites
  section_network
  section_storage
  section_power
  section_summary

  echo ""

  # Exit codes: 0=clear, 1=blockers, 2=warnings only
  [[ $BLOCKERS -gt 0 ]] && exit 1
  [[ $WARNINGS -gt 0 ]] && exit 2
  exit 0
}

main "$@"
