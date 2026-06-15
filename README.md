# Mac LLM Optimizer

Configure an Apple Silicon Mac as a production-grade LLM inference node. Idempotent bash scripts that set up power management, network tuning, service suppression, and one or more serving tools — all driven by a single `config.json`.

**Supported tools:** Ollama · Rapid-MLX · mlx-lm · Infinity · Exo

**Requires:** Apple Silicon (M1 or later) · macOS 15 Sequoia or 26 Tahoe · Homebrew

---

## Quick Start

```bash
# 1. Clone
git clone https://github.com/miha42-github/headless-macs.git
cd headless-macs

# 2. Make scripts executable
chmod +x *.sh scripts/*.sh

# 3. Audit your system — read-only, no changes, no sudo
./precheck.sh

# 4. Review precheck output. If you want an external volume for model storage:
#    Edit config.json → set storage.use_external_volume: true
#    Then run (requires sudo):
sudo ./storage-volume.sh

# 5. Edit config.json to enable/configure your tools (Ollama is on by default)

# 6. Apply system baseline — pmset, network tuning, service suppression, SSH
sudo ./setup.sh

# 7. Install and start serving tools
sudo ./install-tools.sh

# 8. Verify everything is healthy
./verify.sh
```

---

## Tool Selection

| Tool | Best For | Port | Notes |
|---|---|---|---|
| **Ollama** | General inference, easy model management | 11434 | Enabled by default. `ollama pull` registry. |
| **Rapid-MLX** | Coding agents (Claude Code, Cursor, Aider) | 8000 | 2–4.2× faster than Ollama; 17 tool-call parsers; `rapid-mlx doctor` diagnostic. Beta. |
| **mlx-lm** | Custom HuggingFace models not in Rapid-MLX | 8080 | Use when you need a specific HF path. |
| **Infinity** | Embeddings + reranking for RAG pipelines | 7997 | MPS-accelerated. OpenAI-compatible `/v1/embeddings` and `/v1/rerank`. |
| **Exo** | Multi-Mac distributed inference | 52415 | Pools unified memory across devices. Requires auto-login. |

Enable tools in `config.json`:

```json
{
  "tools": {
    "ollama":    { "enabled": true  },
    "rapid_mlx": { "enabled": false },
    "mlx_lm":   { "enabled": false },
    "infinity":  { "enabled": false },
    "exo":       { "enabled": false }
  }
}
```

See [`docs/tool-comparison.md`](docs/tool-comparison.md) for a full comparison.

> **Network defaults:** Services bind to `localhost` (`127.0.0.1`) by default and the firewall is left enabled. If your inference clients are on other LAN hosts, set `"localhost_only": false` in `config.json`. If you run unsigned Python services (Rapid-MLX, mlx-lm, Infinity) and cannot manage per-app firewall rules, also set `"disable_firewall": true` — only do this on an isolated trusted network.

---

## Hardware RAM Reference

| Mac Model | RAM | Recommended Config |
|---|---|---|
| MacBook Air M3 | 16 GB | 7B Q8 · 1 model at a time |
| MacBook Air M3 | 24 GB | 13B Q8 or 7B Q8 + embeddings |
| MacBook Pro M4 | 32 GB | 32B Q4 or 13B Q8 · 2 models |
| Mac Mini M4 | 64 GB | 70B Q4 or 32B Q5 · 3 models |
| Mac Mini / Studio M4 Max | 128 GB | 70B Q8 or 32B Q8 + 70B Q4 · 3–4 models |
| Mac Studio M4 Ultra | 192 GB | 405B Q4 · multiple large models |

`install-tools.sh` automatically tunes Ollama's `MAX_LOADED_MODELS`, `NUM_PARALLEL`, and `MAX_CONTEXT` based on detected RAM. See [`docs/ram-sizing.md`](docs/ram-sizing.md).

---

## File Structure

```
headless-macs/
├── config.json            # All tuning parameters — edit this first
├── precheck.sh            # Read-only system audit — run first, no sudo
├── setup.sh               # System baseline: pmset, sysctl, services, SSH
├── install-tools.sh       # Serving stack: Ollama, Rapid-MLX, mlx-lm, Infinity, Exo
├── storage-volume.sh      # External volume setup and symlink wiring
├── verify.sh              # Health check report — run any time
├── restore.sh             # Undo all changes made by setup.sh
├── manage.sh              # Component orchestrator (Homebrew, Colima, legacy ops)
├── lib/
│   └── common.sh          # Shared utility functions
├── scripts/
│   ├── power_management.sh
│   ├── homebrew_setup.sh
│   ├── ollama_setup.sh
│   └── colima_setup.sh
├── modelfiles/
│   ├── qwen3-coder-next-256k-agent.modelfile  # Agent variant: low temp, tool rules
│   ├── qwen3-coder-next-256k.modelfile        # Chat variant: higher temp
│   └── qwen3-coder-next-128k.modelfile        # Reduced context for memory headroom
├── docs/
│   ├── modelfile-guide.md  # Modelfile system, parameter rationale, ollama create workflow
│   ├── tool-comparison.md  # Ollama vs Rapid-MLX vs mlx-lm vs Infinity vs Exo
│   ├── ram-sizing.md       # Model size × quantisation × RAM + KV cache reference
│   ├── storage-guide.md    # External volume: APFS, fstab, symlink map
│   └── known-issues.md     # Workarounds for common problems
├── pmset_to_ollama.sh     # [DEPRECATED]
└── setup_colima.sh        # [DEPRECATED]
```

---

## Script Reference

### `precheck.sh` — System Audit

No sudo. No changes. Run this first on any new machine.

```bash
./precheck.sh
```

Checks: hardware identity · RAM capability · macOS version · SIP · FileVault · auto-login · Xcode CLT · Homebrew · Python · port availability · storage · current pmset state

Writes `/tmp/mac-llm-precheck.json` for downstream scripts.

Exit codes: `0` = ready · `1` = blockers · `2` = warnings only

---

### `setup.sh` — System Baseline

Requires sudo. Idempotent — safe to run multiple times.

```bash
sudo ./setup.sh
```

- Power management: all pmset settings (including `autorestart` for power-failure recovery), caffeinate LaunchDaemon, MacBook clamshell warning
- Network: TCP buffer sizes via a `RunAtLoad` LaunchDaemon (persists across reboots)
- Service suppression: Spotlight, telemetry, Siri, iCloud, Biome (SIP-gated)
- UI: AirDrop, App Nap, animations, notifications, software update, Time Machine
- SSH: enables Remote Login, hardens sshd via a drop-in file at `/etc/ssh/sshd_config.d/100-headless.conf` (survives macOS updates)
- Xcode CLT: headless install via `softwareupdate`

A daily launchd timer (`com.llm-server.pmset-heal`) automatically re-applies power settings after macOS updates. Manual re-run still works.

---

### `storage-volume.sh` — External Volume Setup

Requires sudo. Only runs if `storage.use_external_volume: true` in `config.json`.

```bash
sudo ./storage-volume.sh
```

- Detects volume by label (from precheck cache or live diskutil)
- Validates filesystem (rejects ExFAT/FAT32/NTFS)
- Creates directory layout: `ollama/`, `rapid-mlx/`, `mlx-lm/`, `infinity/`, `exo/`, `gguf/`
- Excludes volume from Spotlight
- Wires `/Library` symlinks so `install-tools.sh` needs no changes
- Adds fstab entry for boot-time auto-mount

See [`docs/storage-guide.md`](docs/storage-guide.md).

---

### `install-tools.sh` — Tool Installation

Requires sudo. Each tool is gated by its `enabled` flag in `config.json`.

```bash
sudo ./install-tools.sh
```

Ollama is the only tool enabled by default. Enable others in `config.json` before running.

**Log locations:**

| Tool | Stdout | Stderr |
|---|---|---|
| Ollama | `/var/log/ollama/stdout.log` | `/var/log/ollama/stderr.log` |
| Rapid-MLX | `/var/log/rapid-mlx/stdout.log` | `/var/log/rapid-mlx/stderr.log` |
| mlx-lm | `/var/log/mlx-lm/stdout.log` | `/var/log/mlx-lm/stderr.log` |
| Infinity | `/var/log/infinity/stdout.log` | `/var/log/infinity/stderr.log` |
| Exo | `/tmp/exo-stdout.log` | `/tmp/exo-stderr.log` |

---

### `verify.sh` — Health Check

No changes made. Exit `0` = all clear · `1` = failures · `2` = warnings only.

```bash
./verify.sh
```

Checks: pmset values · caffeinate daemon · Spotlight · SSH · sysctl · per-tool daemon state · API endpoints · model count · memory pressure

---

### `restore.sh` — Undo All Changes

Requires sudo. Reverses everything `setup.sh` and `install-tools.sh` did.

```bash
sudo ./restore.sh
```

- Removes all LaunchDaemon and LaunchAgent plists
- Restores pmset to safe defaults
- Re-enables suppressed services from the pre-change snapshot
- Restores Spotlight, `sshd_config`, `defaults` changes, `sysctl.conf` entries

Prompts for confirmation before making changes. Recommends a reboot when done.

---

## After Installation

### Pull your first Ollama model

```bash
# Check what models suit your hardware
./precheck.sh | grep -A10 "MODEL CAPABILITY"

# Pull a model
ollama pull qwen2.5-coder:7b-instruct-q8_0

# Test inference
ollama run qwen2.5-coder:7b-instruct-q8_0 "write hello world in python"

# Verify the stack
./verify.sh
```

### Register production Modelfiles

Modelfiles bake `num_ctx` and sampling parameters into model metadata so clients see
the correct context window. Without a Modelfile, clients read the model card default.

```bash
# Register the production Modelfiles (update FROM path if GGUF is stored elsewhere)
ollama create qwen3-coder-next-256k-agent -f modelfiles/qwen3-coder-next-256k-agent.modelfile
ollama create qwen3-coder-next-256k       -f modelfiles/qwen3-coder-next-256k.modelfile
ollama create qwen3-coder-next-128k       -f modelfiles/qwen3-coder-next-128k.modelfile

# Pin the primary model in memory to avoid cold-start delays
curl -s http://localhost:11434/api/generate \
  -d '{"model": "qwen3-coder-next-256k-agent", "keep_alive": -1}' > /dev/null
```

See [`docs/modelfile-guide.md`](docs/modelfile-guide.md) for parameter rationale and
the two-model split pattern (agent vs chat).

### Point a coding agent at Ollama

```
Base URL: http://<mac-ip>:11434/v1
API Key:  (any string — Ollama ignores it)
Model:    qwen3-coder-next-256k-agent   (agentic tasks — use Zoo Code)
Model:    qwen3-coder-next-256k         (chat — use Opilot or Copilot)
```

**Note:** VS Code Copilot agent mode has a known tool call loop bug with local GGUF models.
Use Zoo Code for agentic tasks. See [`docs/known-issues.md`](docs/known-issues.md).

### Run containers alongside Ollama (Colima)

```bash
./manage.sh install colima

# Containers can reach host Ollama at:
# http://host.docker.internal:11434
docker run -e OLLAMA_BASE_URL=http://host.docker.internal:11434 myapp
```

---

## Troubleshooting

**Machine sleeps despite setup.sh**
```bash
pmset -g | grep -E "sleep|disablesleep|powermode"
sudo ./setup.sh   # idempotent — safe to re-run
```

**Ollama daemon not starting**
```bash
sudo launchctl print system/com.ollama.server
tail -50 /var/log/ollama/stderr.log
```

**Update Ollama to the latest version**
```bash
sudo ./update-tools.sh ollama
# or via manage.sh:
sudo ./manage.sh update ollama
```

**Something went wrong — clean slate**
```bash
sudo ./restore.sh
# then reboot
```

**Disable SIP (required for full service suppression on macOS 26 Tahoe — Apple Silicon)**

`setup.sh` warns and runs safely with SIP enabled, but some service-disable calls need SIP off to persist across reboots.

1. Shut down the Mac completely
2. Press and **hold** the power button — keep holding until you see "Loading startup options" or a gear/Options icon appears
3. Release the power button, then click **Options → Continue**
4. Select your startup volume (Macintosh HD) → **Next**
5. Select an admin user → enter password → **Continue**
6. From the menu bar: **Utilities → Terminal**
7. Run: `csrutil disable` then press Return
8. Restart: `reboot`

To re-enable SIP: boot into Recovery the same way and run `csrutil enable`.

See [`docs/known-issues.md`](docs/known-issues.md) for a full workarounds table.

---

## Contributing

Pull requests welcome. Please ensure:
- All scripts pass `bash -n <script>` (syntax check)
- Changes are idempotent — running twice produces `[SKIP]` for already-applied settings
- New tool plists include `UserName _llmserver`, `HOME=/Library/LLMServer`, and use `bootstrap`/`bootout`

## License

See [LICENSE](LICENSE).
