# Known Issues and Workarounds

## Critical — Must Fix Before Going Headless

| Issue | Affects | Symptom | Fix |
|---|---|---|---|
| **FileVault enabled** | All Macs | Headless reboots hang at password prompt — machine is unreachable | System Settings → Privacy & Security → FileVault → Turn Off. Cannot be scripted. |
| **`panic: $HOME is not defined`** | All Macs, all macOS | Ollama / mlx-lm / Infinity daemon crashes immediately on start | `HOME=/Library/LLMServer` is set in every LaunchDaemon plist by `headless-macs install-tools`. Daemons run as the `_llmserver` service account (not root). If you write your own plist, always include `HOME` pointing to that account's home directory. |
| **MacBook sleeps on lid close** | MacBooks only | Machine becomes unreachable when lid is closed | Purchase an HDMI or USB-C dummy plug (recommended). Alternative: `sudo pmset -a lidwake 0` — thermal risk if vents are blocked. |

---

## System Configuration

| Issue | Affects | Symptom | Fix |
|---|---|---|---|
| **pmset values reset after macOS update** | macOS 26 Tahoe | Machine starts sleeping again after an update | Handled automatically by the `com.llm-server.pmset-heal` daily timer installed by `headless-macs baseline` (runs at 03:00). Manual re-run `sudo headless-macs baseline` also works. |
| **SIP blocks persistent service disabling** | macOS 15+ / 26 Tahoe with SIP on | `launchctl disable` appears to succeed but service restarts after reboot | Disable SIP in Recovery Mode (see **Entering Recovery Mode** section below). `headless-macs baseline` warns and continues safely with SIP on — only service suppression persistence is affected. |
| **`launchctl load` / `unload` deprecated** | macOS 26 Tahoe | Commands silently fail or behave incorrectly | Use `launchctl bootstrap system <plist>` and `launchctl bootout system <plist>`. Every LaunchDaemon `headless-macs` installs already uses the correct commands. |
| **Sequoia 15.3+ sleep regression** | macOS 15.3+ | Machine sleeps despite pmset settings | The caffeinate LaunchDaemon (`com.llm-server.caffeinate`) installed by `headless-macs baseline` is the safety net. Verify it's running: `sudo launchctl print system/com.llm-server.caffeinate` |
| **`xcode-select --install` fails headless** | All headless Macs | GUI dialog appears with no display attached | Use the softwareupdate method. `headless-macs baseline` handles this automatically. |
| **`defaults write` auto-login broken** | macOS 15 Sequoia+ | Setting auto-login via defaults has no effect | Use `sudo sysadminctl -autologin set -userName <user> -password <pw>` or System Settings → Users & Groups. |

---

## Ollama

| Issue | Affects | Symptom | Fix |
|---|---|---|---|
| **Ollama login item conflicts with daemon** | All Macs | Two Ollama processes running; port conflicts; daemon fails to start | `headless-macs install-tools` removes the login item automatically via `osascript`. If still present: System Settings → General → Login Items → remove Ollama. |
| **SIP blocks `/usr/share` writes** | macOS 15+ | Permission denied when writing to system paths | Use `/Library` for models and config. All plists `headless-macs` writes use `/Library/Ollama/models`. |
| **Models stored in `~/.ollama` instead of configured dir** | All Macs | `OLLAMA_MODELS` env var ignored | `HOME=/Library/LLMServer` must be set alongside `OLLAMA_MODELS` in the plist. Without `HOME`, Ollama ignores `OLLAMA_MODELS` and falls back to `~/.ollama` of the running user. |
| **Ollama app vs daemon conflict** | All Macs | Port 11434 already in use when daemon starts | Stop the app: `pkill -f "Ollama.app"`. Remove login item. Run only the daemon installed by `headless-macs install-tools`. |
| **Updating Ollama binary doesn't restart daemon** | All Macs | New binary installed but daemon still serves old version | Use `sudo headless-macs update-tools` — stops every enabled tool's daemon (Ollama included), runs the upstream installer(s), removes any re-added login item, and re-bootstraps each daemon. There's no way to update a single tool in isolation — it updates all enabled tools. |

---

## Rapid-MLX

| Issue | Affects | Symptom | Fix |
|---|---|---|---|
| **API unavailable on first start** | All Macs | `curl localhost:8000/v1/models` times out | First start downloads the model. Can take several minutes for large models. Monitor: `tail -f /var/log/rapid-mlx/stdout.log` |
| **Slow cold-start on long prompts** | All Macs | First inference on a long prompt takes many seconds | Set `--prefill-step-size 8192`. `headless-macs install-tools` sets this in the plist automatically. |
| **Default port 8000 conflicts with mlx-lm** | Machines running both | One server fails to bind | Change one port in `config.json`. Default: Rapid-MLX=8000, mlx-lm=8080. |

---

## mlx-lm

| Issue | Affects | Symptom | Fix |
|---|---|---|---|
| **Server crashes immediately** | All Macs | Daemon starts then exits | `default_model` in `config.json` is empty or invalid. `headless-macs install-tools` writes the plist but doesn't bootstrap it if the model is not set. Set the model path then: `sudo launchctl bootstrap system /Library/LaunchDaemons/com.mlx-lm.server.plist` |
| **Model not found at path** | All Macs | `FileNotFoundError` in stderr log | Download first: `python3 -m mlx_lm.convert --hf-path <hf-repo> --mlx-path /Library/MLX/models/<name>` |

---

## Infinity

| Issue | Affects | Symptom | Fix |
|---|---|---|---|
| **Embedding throughput ~10× lower than expected** | All Macs | Inference is CPU-bound | `--device mps` is missing from the plist. `headless-macs install-tools` sets it automatically. Verify: `cat /Library/LaunchDaemons/com.infinity.server.plist \| grep mps` |
| **Model downloads on first start** | All Macs | API unavailable until HuggingFace model is cached | Normal behaviour. Monitor: `tail -f /var/log/infinity/stderr.log` |

---

## Exo

| Issue | Affects | Symptom | Fix |
|---|---|---|---|
| **Exo doesn't start after reboot** | All Macs | LaunchAgent not loaded | Exo runs as a LaunchAgent (user-level), not a LaunchDaemon. It only starts after a user logs in. Configure auto-login: `sudo sysadminctl -autologin set -userName <user> -password <pw>` |
| **Nodes can't discover each other** | Multi-Mac clusters | Each node works alone but won't cluster | Same-LAN/same-namespace nodes auto-discover via zenoh/libp2p with no config needed. For cross-network clustering, set `tools.exo.bootstrap_peers` in `config.json` to the other nodes' libp2p addresses. (**Corrected 2026-09**: this row previously described Tailscale-based discovery and a `--discovery-module` flag — neither exists in current exo, confirmed by reading `exo-explore/exo`'s actual CLI source. See `docs/tool-comparison.md`'s Exo section for the full explanation.) |
| **Mismatched Exo versions** | Multi-Mac clusters | Cluster forms but inference fails | All nodes must run the same Exo version. Update all nodes simultaneously. |

---

## External Storage

| Issue | Affects | Symptom | Fix |
|---|---|---|---|
| **Volume not mounted at boot** | All Macs with external storage | LaunchDaemon fails on first start after reboot — model dir doesn't exist | `headless-macs storage` adds an fstab entry. Verify: `cat /etc/fstab`. If missing, re-run `sudo headless-macs storage`. |
| **USB drive I/O slower than expected** | Macs using USB storage | Model load time 2–5× longer | Use a Thunderbolt enclosure for production. USB 3.x (5–10 Gbps) is acceptable for development. |
| **`disksleep` re-enabled after macOS update** | External drive users | Drive spins down mid-inference | Re-run `sudo headless-macs baseline` after any macOS update. |
| **ExFAT/FAT32 formatted drive** | All Macs | `_llmserver:_llmserver` ownership fails silently; models world-readable | Reformat as APFS: `diskutil eraseDisk APFS LLMStorage /dev/diskN` |
| **Spotlight re-indexes after macOS update** | External drive users | `mds` competes for I/O during inference | Re-run `sudo mdutil -i off /Volumes/LLMStorage` and verify `.metadata_never_index` is present. |
| **Volume label has spaces** | All Macs | fstab and symlink paths break | Use labels without spaces. `LLMStorage` not `LLM Storage`. |
| **`nobrowse` hides volume from Finder** | All Macs | Admin can't browse models in Finder | Remove `nobrowse` from the fstab entry if dual-use machine. Headless servers should keep it. |
| **Existing models not migrated** | Macs with prior Ollama install | Model library split across internal and external | `headless-macs storage` auto-migrates if the internal dir exists before symlinking. Run it once with `use_external_volume: true`. |

---

## Client Tooling

> **Note on Roo Code → Zoo Code:** Roo Code (3M installs) shut down in April 2026 when
> the team archived the project to focus on Roomote. The community forked it as
> **Zoo Code** (Apache 2.0, same codebase, same settings structure) which launched on the
> VS Code Marketplace May 16, 2026. All references in this repo use Zoo Code.
> Existing Roo Code configs export and import directly. GitHub: [Zoo-Code-Org/Zoo-Code](https://github.com/Zoo-Code-Org/Zoo-Code)

| Issue | Affects | Symptom | Fix |
|---|---|---|---|
| **VS Code Copilot agent mode tool call loop** | VS Code + local GGUF via Ollama | Model repeatedly issues same 4–5 tool calls, receives `isError` results, retries indefinitely. No error surfaced to user. | Use **Zoo Code** instead of VS Code Copilot agent mode. Zoo Code implements its own agent loop, bypasses VS Code's Copilot orchestration layer, and speaks directly to Ollama's OpenAI-compatible endpoint. Root cause: microsoft/vscode-copilot-chat #3566 — Ollama models not correctly identified as supporting tool calls; tools field stripped from requests. |
| **VS Code connection leak (Remote-SSH + Ollama tunnel)** | VS Code on a remote machine connecting to Ollama via SSH tunnel | TCP connection count climbs: 58 → 136 → 236 → 486 over 30 minutes. After ~5 minutes with a saturated pool, requests silently fail with no error shown. | On the remote (Linux) host, apply aggressive TCP keepalive: `sudo sysctl -w net.ipv4.tcp_keepalive_time=60 net.ipv4.tcp_keepalive_intvl=10 net.ipv4.tcp_keepalive_probes=3 net.ipv4.tcp_fin_timeout=15 net.ipv4.tcp_tw_reuse=1` — persist to `/etc/sysctl.conf`. Stabilises at ~162 connections. Also set `"opilot.localModelRefreshInterval": 300` in VS Code settings. |
| **MLX models ignore Modelfile `num_ctx`** | Rapid-MLX, mlx-lm | Setting `num_ctx` in a Modelfile has no effect on MLX-quantised models. Client sees wrong context window. | Context window is fixed at MLX conversion time. To change it, reconvert the model with the desired `--max-position-embeddings`. Use GGUF + Ollama when `num_ctx` control is required. |
| **Ollama UI context window does not update client metadata** | All Ollama clients | Setting context window in Ollama UI shows correct value in Ollama but client (VS Code, Zoo Code) still sends requests sized to the model card default. | Use a Modelfile with `PARAMETER num_ctx` set explicitly. This is the only mechanism that bakes `num_ctx` into the model metadata that clients read. See `docs/modelfile-guide.md`. |

---

## Entering Recovery Mode (Apple Silicon — macOS 26 Tahoe)

Required for: disabling SIP, running Startup Security Utility, erasing and reinstalling macOS.

> **Note:** This procedure applies to all Apple Silicon Macs (M1 and later). Intel Macs use Command+R at startup instead — these steps do not apply to Intel.

**Steps:**

1. Shut down the Mac completely. If it won't respond, hold the power button for up to 10 seconds until it powers off.
2. Press and **hold** the power button. Keep holding it past the normal startup chime.
3. Release when you see **"Loading startup options"** or a gear/Options icon on screen.
4. Click **Options**, then click **Continue** beneath it.
5. If prompted to select a volume, choose **Macintosh HD** → click **Next**.
6. Select an admin user account → click **Next** → enter the account password → click **Continue**.
7. You are now in Recovery Mode. The macOS Utilities window appears.

**To disable SIP:**

```
Utilities (menu bar) → Terminal
csrutil disable
reboot
```

**To re-enable SIP** (recommended after completing setup):

```
Boot into Recovery Mode using the same steps above
Utilities → Terminal
csrutil enable
reboot
```

**To check SIP status** at any time (no reboot required):

```bash
csrutil status
```

**macOS 26 Tahoe note:** Tahoe adds a **Recovery Assistant** under Utilities → Recovery Assistant that can automatically diagnose and repair startup problems. If your Mac is having startup issues, try this before manual intervention. SIP disable/enable via `csrutil` works identically to prior versions.

---

## M4-Specific

| Issue | Affects | Symptom | Fix |
|---|---|---|---|
| **No display = wrong GPU paths on M4 Mac Mini** | M4 Mac Mini headless | Some GPU acceleration paths not available | Connect an HDMI dummy plug. Required for proper framebuffer initialisation and correct VNC resolution. |
