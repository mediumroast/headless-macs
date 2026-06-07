# Known Issues and Workarounds

## Critical — Must Fix Before Going Headless

| Issue | Affects | Symptom | Fix |
|---|---|---|---|
| **FileVault enabled** | All Macs | Headless reboots hang at password prompt — machine is unreachable | System Settings → Privacy & Security → FileVault → Turn Off. Cannot be scripted. |
| **`panic: $HOME is not defined`** | All Macs, all macOS | Ollama / mlx-lm / Infinity daemon crashes immediately on start | `HOME=/var/root` is set in every LaunchDaemon plist by `install-tools.sh`. If you write your own plist, always include it. |
| **MacBook sleeps on lid close** | MacBooks only | Machine becomes unreachable when lid is closed | Purchase an HDMI or USB-C dummy plug (recommended). Alternative: `sudo pmset -a lidwake 0` — thermal risk if vents are blocked. |

---

## System Configuration

| Issue | Affects | Symptom | Fix |
|---|---|---|---|
| **pmset values reset after macOS update** | macOS 26 Tahoe | Machine starts sleeping again after an update | Re-run `sudo ./setup.sh` after any macOS update. The caffeinate LaunchDaemon mitigates sleep regression but doesn't reset pmset. |
| **SIP blocks persistent service disabling** | macOS 15+ with SIP on | `launchctl disable` appears to succeed but service restarts after reboot | Disable SIP in Recovery Mode: boot → Terminal → `csrutil disable`. `setup.sh` warns and continues safely with SIP on. |
| **`launchctl load` / `unload` deprecated** | macOS 26 Tahoe | Commands silently fail or behave incorrectly | Use `launchctl bootstrap system <plist>` and `launchctl bootout system <plist>`. All scripts in this repo use the correct commands. |
| **Sequoia 15.3+ sleep regression** | macOS 15.3+ | Machine sleeps despite pmset settings | The caffeinate LaunchDaemon (`com.llm-server.caffeinate`) installed by `setup.sh` is the safety net. Verify it's running: `sudo launchctl print system/com.llm-server.caffeinate` |
| **`xcode-select --install` fails headless** | All headless Macs | GUI dialog appears with no display attached | Use the softwareupdate method. `setup.sh` handles this automatically. |
| **`defaults write` auto-login broken** | macOS 15 Sequoia+ | Setting auto-login via defaults has no effect | Use `sudo sysadminctl -autologin set -userName <user> -password <pw>` or System Settings → Users & Groups. |

---

## Ollama

| Issue | Affects | Symptom | Fix |
|---|---|---|---|
| **Ollama login item conflicts with daemon** | All Macs | Two Ollama processes running; port conflicts; daemon fails to start | `install-tools.sh` removes the login item automatically via `osascript`. If still present: System Settings → General → Login Items → remove Ollama. |
| **SIP blocks `/usr/share` writes** | macOS 15+ | Permission denied when writing to system paths | Use `/Library` for models and config. All plists in this repo use `/Library/Ollama/models`. |
| **Models stored in `~/.ollama` instead of configured dir** | All Macs | `OLLAMA_MODELS` env var ignored | `HOME=/var/root` must be set alongside `OLLAMA_MODELS` in the plist. Without `HOME`, Ollama ignores `OLLAMA_MODELS` and falls back to `~/.ollama` of the running user. |
| **Ollama app vs daemon conflict** | All Macs | Port 11434 already in use when daemon starts | Stop the app: `pkill -f "Ollama.app"`. Remove login item. Run only the daemon installed by `install-tools.sh`. |

---

## Rapid-MLX

| Issue | Affects | Symptom | Fix |
|---|---|---|---|
| **API unavailable on first start** | All Macs | `curl localhost:8000/v1/models` times out | First start downloads the model. Can take several minutes for large models. Monitor: `tail -f /var/log/rapid-mlx/stdout.log` |
| **Slow cold-start on long prompts** | All Macs | First inference on a long prompt takes many seconds | Set `--prefill-step-size 8192`. `install-tools.sh` sets this in the plist automatically. |
| **Default port 8000 conflicts with mlx-lm** | Machines running both | One server fails to bind | Change one port in `config.json`. Default: Rapid-MLX=8000, mlx-lm=8080. |

---

## mlx-lm

| Issue | Affects | Symptom | Fix |
|---|---|---|---|
| **Server crashes immediately** | All Macs | Daemon starts then exits | `default_model` in `config.json` is empty or invalid. `install-tools.sh` writes the plist but doesn't bootstrap it if the model is not set. Set the model path then: `sudo launchctl bootstrap system /Library/LaunchDaemons/com.mlx-lm.server.plist` |
| **Model not found at path** | All Macs | `FileNotFoundError` in stderr log | Download first: `python3 -m mlx_lm.convert --hf-path <hf-repo> --mlx-path /Library/MLX/models/<name>` |

---

## Infinity

| Issue | Affects | Symptom | Fix |
|---|---|---|---|
| **Embedding throughput ~10× lower than expected** | All Macs | Inference is CPU-bound | `--device mps` is missing from the plist. `install-tools.sh` sets it automatically. Verify: `cat /Library/LaunchDaemons/com.infinity.server.plist \| grep mps` |
| **Model downloads on first start** | All Macs | API unavailable until HuggingFace model is cached | Normal behaviour. Monitor: `tail -f /var/log/infinity/stderr.log` |

---

## Exo

| Issue | Affects | Symptom | Fix |
|---|---|---|---|
| **Exo doesn't start after reboot** | All Macs | LaunchAgent not loaded | Exo runs as a LaunchAgent (user-level), not a LaunchDaemon. It only starts after a user logs in. Configure auto-login: `sudo sysadminctl -autologin set -userName <user> -password <pw>` |
| **Nodes can't discover each other** | Multi-Mac clusters | Each node works alone but won't cluster | For Tailscale discovery: ensure `tailscaled` is running on all nodes. For Bonjour: ensure all nodes are on the same LAN. |
| **Mismatched Exo versions** | Multi-Mac clusters | Cluster forms but inference fails | All nodes must run the same Exo version. Update all nodes simultaneously. |

---

## External Storage

| Issue | Affects | Symptom | Fix |
|---|---|---|---|
| **Volume not mounted at boot** | All Macs with external storage | LaunchDaemon fails on first start after reboot — model dir doesn't exist | `storage-volume.sh` adds an fstab entry. Verify: `cat /etc/fstab`. If missing, re-run `sudo ./storage-volume.sh`. |
| **USB drive I/O slower than expected** | Macs using USB storage | Model load time 2–5× longer | Use a Thunderbolt enclosure for production. USB 3.x (5–10 Gbps) is acceptable for development. |
| **`disksleep` re-enabled after macOS update** | External drive users | Drive spins down mid-inference | Re-run `sudo ./setup.sh` after any macOS update. |
| **ExFAT/FAT32 formatted drive** | All Macs | `root:wheel` ownership fails silently; models world-readable | Reformat as APFS: `diskutil eraseDisk APFS LLMStorage /dev/diskN` |
| **Spotlight re-indexes after macOS update** | External drive users | `mds` competes for I/O during inference | Re-run `sudo mdutil -i off /Volumes/LLMStorage` and verify `.metadata_never_index` is present. |
| **Volume label has spaces** | All Macs | fstab and symlink paths break | Use labels without spaces. `LLMStorage` not `LLM Storage`. |
| **`nobrowse` hides volume from Finder** | All Macs | Admin can't browse models in Finder | Remove `nobrowse` from the fstab entry if dual-use machine. Headless servers should keep it. |
| **Existing models not migrated** | Macs with prior Ollama install | Model library split across internal and external | `storage-volume.sh` auto-migrates if the internal dir exists before symlinking. Run it once with `use_external_volume: true`. |

---

## M4-Specific

| Issue | Affects | Symptom | Fix |
|---|---|---|---|
| **No display = wrong GPU paths on M4 Mac Mini** | M4 Mac Mini headless | Some GPU acceleration paths not available | Connect an HDMI dummy plug. Required for proper framebuffer initialisation and correct VNC resolution. |
