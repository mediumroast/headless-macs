#!/usr/bin/env bash
# storage-volume.sh — External Volume Setup for Mac LLM Optimizer
#
# Configures an external drive as the model storage location for all serving
# tools. Creates a standardised directory layout, excludes the volume from
# Spotlight, wires /Library symlinks so install-tools.sh needs no changes,
# and adds an fstab entry so the volume mounts before LaunchDaemons start.
#
# Run order: precheck.sh → setup.sh → storage-volume.sh → install-tools.sh
#
# Requires: sudo, diskutil, jq
# Idempotent: safe to run multiple times

set -euo pipefail

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
# Config — load before anything that requires sudo so early-exit is clean
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

USE_EXTERNAL=$(echo "$CONFIG"     | jq -r '.storage.use_external_volume')
VOLUME_LABEL=$(echo "$CONFIG"     | jq -r '.storage.volume_label')
MODELS_SUBDIR=$(echo "$CONFIG"    | jq -r '.storage.models_subdir')
MIN_FREE_GB=$(echo "$CONFIG"      | jq -r '.storage.min_free_gb')
SYMLINK_INTERNAL=$(echo "$CONFIG" | jq -r '.storage.symlink_internal_paths')

# ---------------------------------------------------------------------------
# Early exit if external storage is not requested (no sudo needed)
# ---------------------------------------------------------------------------
if [[ "$USE_EXTERNAL" != "true" ]]; then
  echo "[INFO] storage.use_external_volume is false in config.json — nothing to do."
  echo "       To use an external volume:"
  echo "         1. Set storage.use_external_volume: true in config.json"
  echo "         2. Set storage.volume_label to your drive's name"
  echo "         3. Re-run this script"
  exit 0
fi

# ---------------------------------------------------------------------------
# Logging (after early-exit so no sudo is needed when skipping)
# ---------------------------------------------------------------------------
LOG_DIR="/var/log/mac-llm-setup"
sudo mkdir -p "$LOG_DIR"
LOG_FILE="$LOG_DIR/storage-volume-$(date +%Y%m%d-%H%M%S).log"
exec > >(tee -a "$LOG_FILE") 2>&1
echo "=== storage-volume.sh started at $(date) ==="
echo ""

echo "[CONFIG] External volume label: '$VOLUME_LABEL'"
echo "[CONFIG] Models subdirectory:   '$MODELS_SUBDIR'"
echo "[CONFIG] Minimum free space:    ${MIN_FREE_GB}GB"
echo "[CONFIG] Symlink internal paths: $SYMLINK_INTERNAL"
echo ""

# ---------------------------------------------------------------------------
# Sudo keepalive
# ---------------------------------------------------------------------------
sudo -v
while true; do sudo -n true; sleep 60; kill -0 "$$" || exit; done 2>/dev/null &
SUDO_PID=$!
trap 'kill $SUDO_PID 2>/dev/null' EXIT

# ===========================================================================
# Section 1: Locate the volume
# ===========================================================================
echo "========================================"
echo "Section 1: Locate Volume '$VOLUME_LABEL'"
echo "========================================"
echo ""

EXPECTED_MOUNT="/Volumes/${VOLUME_LABEL}"
VOL_DEVICE=""
VOL_UUID=""
VOLUME_MOUNT=""

# Fast path: use precheck JSON if available and fresh
PRECHECK_JSON="/tmp/mac-llm-precheck.json"
if [[ -f "$PRECHECK_JSON" ]]; then
  CACHED_MOUNT=$(jq -r '.storage.external_volume_mount // empty' "$PRECHECK_JSON" 2>/dev/null || echo "")
  CACHED_LABEL=$(jq -r '.storage.external_volume_label_found // false' "$PRECHECK_JSON" 2>/dev/null || echo "false")
  if [[ "$CACHED_LABEL" == "true" && -n "$CACHED_MOUNT" && -d "$CACHED_MOUNT" ]]; then
    VOLUME_MOUNT="$CACHED_MOUNT"
    echo "[INFO] Volume found via precheck cache: $VOLUME_MOUNT"
  fi
fi

# Live fallback: ask diskutil directly and mount the volume if it exists but
# is not currently attached at its expected path.
if [[ -z "$VOLUME_MOUNT" ]]; then
  VOL_INFO=$(diskutil info "$VOLUME_LABEL" 2>/dev/null || true)
  VOL_DEVICE=$(echo "$VOL_INFO" | awk -F': *' '/Device Identifier/{print $2; exit}')
  VOL_UUID=$(echo "$VOL_INFO" | awk -F': *' '/Volume UUID/{print $2; exit}')
  VOLUME_MOUNT=$(echo "$VOL_INFO" | awk -F': *' '/Mount Point/{print $2; exit}')

  if [[ -n "$VOL_DEVICE" && ( -z "$VOLUME_MOUNT" || "$VOLUME_MOUNT" == "Not mounted" || "$VOLUME_MOUNT" == "(null)" ) ]]; then
    echo "[INFO] Volume '$VOLUME_LABEL' found as $VOL_DEVICE but not mounted"
    sudo mkdir -p "$EXPECTED_MOUNT"
    if sudo diskutil mount -mountPoint "$EXPECTED_MOUNT" "$VOL_DEVICE" >/dev/null 2>&1; then
      VOLUME_MOUNT="$EXPECTED_MOUNT"
      echo "[SET]  Mounted '$VOLUME_LABEL' at $VOLUME_MOUNT"
    else
      echo "[WARN] diskutil could not mount '$VOLUME_LABEL' at $EXPECTED_MOUNT"
    fi
  fi
fi

if [[ -z "$VOLUME_MOUNT" || "$VOLUME_MOUNT" == "Not mounted" || "$VOLUME_MOUNT" == "(null)" || ! -d "$VOLUME_MOUNT" ]]; then
  echo "ERROR: No volume with label '$VOLUME_LABEL' found or not mounted."
  echo ""
  echo "To prepare an external drive:"
  echo "  1. Connect the drive"
  echo "  2. Format as APFS (recommended):"
  echo "       diskutil eraseDisk APFS '$VOLUME_LABEL' /dev/diskN"
  echo "     (Find diskN with: diskutil list)"
  echo "  3. Re-run this script"
  echo ""
  echo "To use a different label: update storage.volume_label in config.json"
  exit 1
fi

echo "[OK]   Volume '$VOLUME_LABEL' mounted at: $VOLUME_MOUNT"

# ===========================================================================
# Section 2: Validate the volume
# ===========================================================================
echo ""
echo "========================================"
echo "Section 2: Validate Volume"
echo "========================================"
echo ""

if [[ -z "$VOL_DEVICE" || -z "$VOL_UUID" ]]; then
  VOL_INFO=$(diskutil info "$VOLUME_MOUNT" 2>/dev/null || true)
  [[ -z "$VOL_DEVICE" ]] && VOL_DEVICE=$(echo "$VOL_INFO" | awk -F': *' '/Device Identifier/{print $2; exit}')
  [[ -z "$VOL_UUID"   ]] && VOL_UUID=$(echo "$VOL_INFO" | awk -F': *' '/Volume UUID/{print $2; exit}')
fi

# Free space check
VOL_FREE_GB=$(df -g "$VOLUME_MOUNT" 2>/dev/null | awk 'NR==2{print $4}')
VOL_TOTAL_GB=$(df -g "$VOLUME_MOUNT" 2>/dev/null | awk 'NR==2{print $2}')

echo "[INFO] Volume size:  ${VOL_TOTAL_GB}GB total, ${VOL_FREE_GB}GB free"

if [[ "$VOL_FREE_GB" -lt "$MIN_FREE_GB" ]]; then
  echo "ERROR: Volume '$VOLUME_LABEL' has only ${VOL_FREE_GB}GB free."
  echo "       Minimum required: ${MIN_FREE_GB}GB (set in storage.min_free_gb)"
  exit 1
fi
echo "[OK]   Free space: ${VOL_FREE_GB}GB ≥ ${MIN_FREE_GB}GB minimum"

# Filesystem type check — ExFAT/FAT32/NTFS lack Unix permissions
VOL_FS=$(diskutil info "$VOLUME_MOUNT" 2>/dev/null \
  | awk '/Type \(Bundle\)/{print $NF}' \
  | tr '[:upper:]' '[:lower:]')

if echo "$VOL_FS" | grep -qiE "exfat|fat32|msdos|ntfs"; then
  echo "ERROR: Volume filesystem is '$VOL_FS'."
  echo "       ExFAT/FAT32/NTFS do not support Unix permissions required for model storage."
  echo "       Reformat as APFS: diskutil eraseDisk APFS '$VOLUME_LABEL' /dev/diskN"
  exit 1
fi
echo "[OK]   Filesystem: $VOL_FS (APFS or HFS+ — permissions supported)"

VOL_OWNERS=$(diskutil info "$VOLUME_MOUNT" 2>/dev/null \
  | awk -F': *' '/Owners/{print $2; exit}')
if [[ "$VOL_OWNERS" == "Enabled" ]]; then
  echo "[SKIP] Ownership already enabled on $VOLUME_MOUNT"
else
  sudo diskutil enableOwnership "$VOLUME_MOUNT" >/dev/null
  echo "[SET]  Ownership enabled on $VOLUME_MOUNT"
fi

# ===========================================================================
# Section 3: Directory layout
# ===========================================================================
echo ""
echo "========================================"
echo "Section 3: Create Directory Layout"
echo "========================================"
echo ""

MODEL_ROOT="${VOLUME_MOUNT}/${MODELS_SUBDIR}"
OLLAMA_VOL_DIR="${MODEL_ROOT}/ollama"
RAPID_MLX_VOL_DIR="${MODEL_ROOT}/rapid-mlx"
MLX_VOL_DIR="${MODEL_ROOT}/mlx-lm"
INFINITY_VOL_DIR="${MODEL_ROOT}/infinity"
EXO_VOL_DIR="${MODEL_ROOT}/exo"
SHARED_GGUF_DIR="${MODEL_ROOT}/gguf"   # Raw .gguf files usable by Ollama and llama.cpp

for dir in \
  "$OLLAMA_VOL_DIR" \
  "$RAPID_MLX_VOL_DIR" \
  "$MLX_VOL_DIR" \
  "$INFINITY_VOL_DIR" \
  "$EXO_VOL_DIR" \
  "$SHARED_GGUF_DIR"; do
  if [[ -d "$dir" ]]; then
    echo "[SKIP] $dir (exists)"
  else
    sudo mkdir -p "$dir"
    echo "[SET]  $dir"
  fi
done

sudo chown -R root:wheel "$MODEL_ROOT"
sudo chmod -R 755 "$MODEL_ROOT"
echo "[OK]   Ownership: root:wheel, permissions: 755"

# ===========================================================================
# Section 4: Spotlight exclusion
# ===========================================================================
echo ""
echo "========================================"
echo "Section 4: Exclude Volume from Spotlight"
echo "========================================"
echo ""

# Without this, mds indexes every .gguf and .safetensors file and
# competes for I/O bandwidth during inference.
sudo mdutil -i off "$VOLUME_MOUNT" 2>/dev/null && \
  echo "[SET]  Spotlight indexing disabled on $VOLUME_MOUNT" || \
  echo "[WARN] Could not disable Spotlight on $VOLUME_MOUNT"

sudo mdutil -E "$VOLUME_MOUNT" 2>/dev/null && \
  echo "[SET]  Spotlight index erased on $VOLUME_MOUNT" || true

# Belt-and-suspenders: hidden marker file prevents future indexing
touch "${VOLUME_MOUNT}/.metadata_never_index" 2>/dev/null && \
  echo "[SET]  .metadata_never_index marker placed" || true

# ===========================================================================
# Section 5: Symlink wiring
# ===========================================================================
echo ""
echo "========================================"
echo "Section 5: Symlink Internal Paths → Volume"
echo "========================================"
echo ""

if [[ "$SYMLINK_INTERNAL" == "true" ]]; then

  wire_symlink() {
    local internal_path="$1"
    local volume_path="$2"
    local label="$3"

    # If the internal path is a real directory (not a symlink), migrate contents
    if [[ -d "$internal_path" && ! -L "$internal_path" ]]; then
      echo "[MIGRATE] Moving existing $label from $internal_path → $volume_path"
      sudo rsync -a --remove-source-files "$internal_path/" "$volume_path/" 2>/dev/null || true
      sudo rm -rf "$internal_path"
    fi

    # Ensure parent directory exists
    sudo mkdir -p "$(dirname "$internal_path")"

    if [[ -L "$internal_path" ]]; then
      CURRENT_TARGET=$(readlink "$internal_path")
      if [[ "$CURRENT_TARGET" == "$volume_path" ]]; then
        echo "[SKIP]   $internal_path → $volume_path (already correct)"
      else
        sudo ln -sf "$volume_path" "$internal_path"
        echo "[UPDATED] $internal_path → $volume_path  (was: $CURRENT_TARGET)"
      fi
    else
      sudo ln -s "$volume_path" "$internal_path"
      echo "[SYMLINK] $internal_path → $volume_path"
    fi
  }

  wire_symlink "/Library/Ollama/models"  "$OLLAMA_VOL_DIR"    "Ollama models"
  wire_symlink "/Library/RapidMLX/cache" "$RAPID_MLX_VOL_DIR" "Rapid-MLX cache"
  wire_symlink "/Library/MLX/models"     "$MLX_VOL_DIR"       "mlx-lm models"
  wire_symlink "/Library/Infinity"       "$INFINITY_VOL_DIR"  "Infinity models"

  echo ""
  echo "  Symlink map (install-tools.sh uses /Library paths unchanged):"
  echo "    /Library/Ollama/models  → $OLLAMA_VOL_DIR"
  echo "    /Library/RapidMLX/cache → $RAPID_MLX_VOL_DIR"
  echo "    /Library/MLX/models     → $MLX_VOL_DIR"
  echo "    /Library/Infinity       → $INFINITY_VOL_DIR"

else
  echo "[SKIP] Symlink wiring disabled (storage.symlink_internal_paths: false)"
  echo "       install-tools.sh will write config.json paths after this section."
fi

# ===========================================================================
# Section 6: fstab entry for boot-time auto-mount
# ===========================================================================
echo ""
echo "========================================"
echo "Section 6: fstab — Auto-Mount at Boot"
echo "========================================"
echo ""

# LaunchDaemons start before Finder mounts volumes. Without an fstab entry,
# the daemon's model directory won't exist at first boot after a reboot.
if [[ -z "$VOL_UUID" ]]; then
  VOL_UUID=$(diskutil info "$VOLUME_MOUNT" 2>/dev/null \
    | awk '/Volume UUID/{print $NF}')
fi
if [[ -z "$VOL_DEVICE" ]]; then
  VOL_DEVICE=$(diskutil info "$VOLUME_MOUNT" 2>/dev/null \
    | awk '/Device Identifier/{print $NF}')
fi

if [[ -z "$VOL_UUID" ]]; then
  echo "[WARN] Could not determine UUID for $VOLUME_MOUNT"
  echo "       Volume may not auto-mount before LaunchDaemons start on reboot."
  echo "       Try: diskutil info '$VOLUME_LABEL' | grep UUID"
else
  echo "[INFO] Volume UUID: $VOL_UUID"
  FSTAB_ENTRY="UUID=${VOL_UUID} ${VOLUME_MOUNT} apfs rw,auto,nobrowse 0 0"
  # nobrowse: volume won't clutter the Finder sidebar (appropriate for a server)
  # auto: mount at boot
  # rw: read-write

  FSTAB="/etc/fstab"
  if grep -q "$VOL_UUID" "$FSTAB" 2>/dev/null; then
    echo "[SKIP] fstab entry for '$VOLUME_LABEL' already present"
  else
    echo "$FSTAB_ENTRY" | sudo tee -a "$FSTAB" > /dev/null
    echo "[SET]  fstab entry added:"
    echo "       $FSTAB_ENTRY"
    echo "[OK]   Volume will auto-mount at boot before LaunchDaemons start"
  fi
fi

MOUNT_HELPER_PLIST="/Library/LaunchDaemons/com.llm-server.storage-mount.plist"
if [[ -n "$VOL_DEVICE" ]]; then
  sudo tee "$MOUNT_HELPER_PLIST" > /dev/null <<PLIST_EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>com.llm-server.storage-mount</string>
  <key>ProgramArguments</key>
  <array>
    <string>/bin/sh</string>
    <string>-c</string>
    <string>/bin/mkdir -p '${VOLUME_MOUNT}' &amp;&amp; /usr/sbin/diskutil mount -mountPoint '${VOLUME_MOUNT}' '${VOL_DEVICE}' &gt;/dev/null 2&gt;&amp;1 || true; /usr/sbin/diskutil enableOwnership '${VOLUME_MOUNT}' &gt;/dev/null 2&gt;&amp;1 || true</string>
  </array>
  <key>RunAtLoad</key><true/>
  <key>StandardOutPath</key><string>/var/log/mac-llm-setup/storage-mount.log</string>
  <key>StandardErrorPath</key><string>/var/log/mac-llm-setup/storage-mount.log</string>
</dict>
</plist>
PLIST_EOF
  sudo chown root:wheel "$MOUNT_HELPER_PLIST"
  sudo chmod 644 "$MOUNT_HELPER_PLIST"
  sudo launchctl bootout system "$MOUNT_HELPER_PLIST" 2>/dev/null || true
  sudo launchctl bootstrap system "$MOUNT_HELPER_PLIST"
  echo "[SET]  storage-mount LaunchDaemon installed and started"
  echo "[INFO] It re-mounts $VOLUME_MOUNT and re-enables ownership at boot"
else
  echo "[WARN] Could not determine device identifier for $VOLUME_MOUNT"
  echo "       Skipping storage-mount LaunchDaemon install"
fi

# ===========================================================================
# Section 7: Update config.json with resolved paths (symlink mode OFF only)
# ===========================================================================
if [[ "$SYMLINK_INTERNAL" != "true" ]]; then
  echo ""
  echo "========================================"
  echo "Section 7: Update config.json with Volume Paths"
  echo "========================================"
  echo ""

  TMP_CONFIG=$(mktemp)
  jq \
    --arg ollama_dir  "$OLLAMA_VOL_DIR" \
    --arg mlx_dir     "$MLX_VOL_DIR" \
    --arg inf_dir     "$INFINITY_VOL_DIR" \
    --arg mount       "$VOLUME_MOUNT" \
    '.tools.ollama.models_dir     = $ollama_dir |
     .tools.mlx_lm.model_path    = $mlx_dir |
     .storage.volume_mount_point  = $mount' \
    "$CONFIG_FILE" > "$TMP_CONFIG"

  sudo mv "$TMP_CONFIG" "$CONFIG_FILE"
  echo "[SET]  config.json updated with volume paths"
  echo "       tools.ollama.models_dir → $OLLAMA_VOL_DIR"
  echo "       tools.mlx_lm.model_path → $MLX_VOL_DIR"
fi

# ===========================================================================
# Section 8: Update precheck JSON with volume info (for verify.sh)
# ===========================================================================
if [[ -f "$PRECHECK_JSON" ]]; then
  TMP_PC=$(mktemp)
  jq \
    --arg  mount      "$VOLUME_MOUNT" \
    --arg  root       "$MODEL_ROOT" \
    --argjson free    "$VOL_FREE_GB" \
    '.storage.volume_configured = true |
     .storage.volume_mount      = $mount |
     .storage.model_root        = $root |
     .storage.free_gb           = $free' \
    "$PRECHECK_JSON" > "$TMP_PC"
  mv "$TMP_PC" "$PRECHECK_JSON"
  echo "[SET]  /tmp/mac-llm-precheck.json updated with volume info"
fi

# ===========================================================================
# Section 9: Verification pass
# ===========================================================================
echo ""
echo "========================================"
echo "Section 9: Verification"
echo "========================================"
echo ""

PASS=0; FAIL=0

_vpass() { echo "  [PASS] $*"; PASS=$((PASS+1)); }
_vfail() { echo "  [FAIL] $*"; FAIL=$((FAIL+1)); }

# Directories
for dir in "$OLLAMA_VOL_DIR" "$RAPID_MLX_VOL_DIR" "$MLX_VOL_DIR" "$INFINITY_VOL_DIR"; do
  if [[ -d "$dir" ]]; then
    OWNER=$(stat -f "%Su:%Sg" "$dir" 2>/dev/null || echo "unknown")
    _vpass "$dir  (owner: $OWNER)"
  else
    _vfail "$dir missing"
  fi
done

# Symlinks
if [[ "$SYMLINK_INTERNAL" == "true" ]]; then
  for link in "/Library/Ollama/models" "/Library/RapidMLX/cache" "/Library/MLX/models"; do
    if [[ -L "$link" && -d "$link" ]]; then
      _vpass "$link → $(readlink "$link")"
    elif [[ -L "$link" ]]; then
      _vfail "$link is a dangling symlink → $(readlink "$link")"
    fi
    # (symlink not present yet is OK if that tool isn't enabled)
  done
fi

# Spotlight off
MDUTIL_OUT=$(mdutil -s "$VOLUME_MOUNT" 2>/dev/null || echo "")
if echo "$MDUTIL_OUT" | grep -qi "disabled\|off"; then
  _vpass "Spotlight disabled on $VOLUME_MOUNT"
else
  _vfail "Spotlight may still be active on $VOLUME_MOUNT"
fi

# fstab
if [[ -n "$VOL_UUID" ]] && grep -q "$VOL_UUID" /etc/fstab 2>/dev/null; then
  _vpass "fstab entry present for $VOLUME_LABEL"
else
  _vfail "fstab entry missing for $VOLUME_LABEL"
fi

echo ""
echo "Verification: $PASS passed, $FAIL failed"

echo ""
echo "========================================"
echo "storage-volume.sh complete"
echo "========================================"
echo ""
echo "Volume '$VOLUME_LABEL' is configured at: $VOLUME_MOUNT"
echo "Model storage root: $MODEL_ROOT"
echo ""
echo "Next step: sudo ./install-tools.sh"
echo "Log written to: $LOG_FILE"

[[ $FAIL -gt 0 ]] && exit 1
exit 0
