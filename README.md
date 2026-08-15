# headless-macs

Configure an Apple Silicon Mac as a production-grade LLM inference node — all from a single interactive TUI binary.

**v2.0.0** replaces the bash pipeline with a Go binary (`headless-macs`) that runs precheck, storage setup, system baseline, tool installation, health check, restore, and update from one menu. The shell scripts remain in the repo for reference but are no longer maintained.

**Supported tools:** Ollama · Rapid-MLX · mlx-lm · Infinity · Exo

**Requires:** Apple Silicon (M1 or later) · macOS 15 Sequoia or 26 Tahoe · Homebrew · Go 1.22+

---

## Quick Start

```bash
# 1. Clone
git clone https://github.com/miha42-github/headless-macs.git
cd headless-macs

# 2. Build the binary
go build -o headless-macs ./cmd/headless-macs

# 3. Run — first launch copies config.json to ~/.headless_macs/config.json
sudo ./headless-macs
```

The TUI menu appears. Recommended run order:

| Step | Menu key | What it does |
|---|---|---|
| 1 | `p` | Precheck — read-only audit, no sudo needed |
| 2 | `c` | Edit Config — enable tools, set storage options |
| 3 | `t` | Storage Setup — external volume (if enabled) |
| 4 | `b` | System Baseline — pmset, sysctl, services, SSH |
| 5 | `i` | Install Tools — daemons for enabled tools |
| 6 | `v` | Verify — health check of everything installed |

Press `q` at any time to return to the menu or quit.

---

## Tool Selection

| Tool | Best For | Port | Notes |
|---|---|---|---|
| **Ollama** | General inference, easy model management | 11434 | Enabled by default. `ollama pull` registry. |
| **Rapid-MLX** | Coding agents (Claude Code, Cursor, Aider) | 8000 | 2–4.2× faster than Ollama; 17 tool-call parsers; `rapid-mlx doctor` diagnostic. Beta. |
| **mlx-lm** | Custom HuggingFace models not in Rapid-MLX | 8080 | Use when you need a specific HF path. |
| **Infinity** | Embeddings + reranking for RAG pipelines | 7997 | MPS-accelerated. OpenAI-compatible `/v1/embeddings` and `/v1/rerank`. |
| **Exo** | Multi-Mac distributed inference | 52415 | Pools unified memory across devices. Requires auto-login. |

Enable tools through the **Edit Config** screen (`c` from the menu), or by editing `~/.headless_macs/config.json` directly:

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

> **Network defaults:** Services bind to `localhost` (`127.0.0.1`) by default and the firewall is left enabled. Set `"localhost_only": false` to allow LAN clients. If you run unsigned Python services (Rapid-MLX, mlx-lm, Infinity) and cannot manage per-app firewall rules, also set `"disable_firewall": true` — only do this on an isolated trusted network.

---

## Hardware RAM Reference

| Mac Model | RAM | Recommended Config |
|---|---|---|
| MacBook Air M3/M4 | 16 GB | `qwen3:8b` (5 GB) · 1 model at a time |
| MacBook Air M3 / Mac Mini M4 | 24 GB | `qwen3:14b` or `qwen3-coder:30b` (19 GB MoE) |
| MacBook Pro M4 / Mac Mini M4 Pro | 32 GB | `qwen3:32b` (20 GB) or `deepseek-r1:32b` · 2 models |
| Mac Mini M4 Max / Mac Studio M4 Max | 64 GB | `llama3.3:70b` Q4 (43 GB) or `deepseek-r1:70b` · 3 models |
| Mac Mini / Studio M4 Max (Mac16,9) | 128 GB | `llama3.3:70b` Q8 (86 GB) or `qwen3.5:122b` Q4 (81 GB) |
| Mac Studio M4 Ultra | 192 GB | `qwen3:235b` Q4 (142 GB) · multiple large models simultaneously |
| Mac Pro M2 Ultra | 192 GB | Same as Studio M4 Ultra |

Install Tools automatically tunes Ollama's `MAX_LOADED_MODELS`, `NUM_PARALLEL`, and `MAX_CONTEXT` based on detected RAM. See [`docs/ram-sizing.md`](docs/ram-sizing.md).

---

## File Structure

```
headless-macs/
├── cmd/
│   └── headless-macs/
│       └── main.go            # Binary entry point
├── internal/
│   ├── config/                # Config load/save, schema, bootstrap
│   ├── ops/                   # All system operations (precheck, baseline, tools, etc.)
│   ├── tui/                   # Bubble Tea TUI (menu, screens, styles)
│   └── log/                   # Structured log writer
├── config.json                # Config template (copied to ~/.headless_macs/ on first run)
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
│
│   Shell scripts (v1 — deprecated, still functional):
├── precheck.sh            # → Precheck menu item
├── setup.sh               # → System Baseline menu item
├── install-tools.sh       # → Install Tools menu item
├── storage-volume.sh      # → Storage Setup menu item
├── verify.sh              # → Verify menu item
├── restore.sh             # → Restore menu item
├── update-tools.sh        # → Update Tools menu item
├── manage.sh              # Phase 2 orchestrator (legacy)
├── pmset_to_ollama.sh     # [DEPRECATED]
└── setup_colima.sh        # [DEPRECATED]
```

---

## After Installation

### Pull your first Ollama model

```bash
# Pull a model — examples by RAM tier:
ollama pull qwen3:8b               # 16 GB — best general at this size
ollama pull qwen3:14b              # 24 GB — fast, 128K context
ollama pull qwen3-coder:30b        # 24 GB+ — best local coding model (MoE, 19 GB)
ollama pull qwen3:32b              # 32 GB — top dense model at tier
ollama pull llama3.3:70b           # 64 GB+ — excellent general-purpose 70B
ollama pull deepseek-r1:70b        # 64 GB+ — leading open reasoning model

# Test inference
ollama run qwen3:8b "write hello world in python"

# Re-run Verify to confirm the daemon is healthy after model pull
sudo ./headless-macs    # → v (Verify)
```

See [`docs/ram-sizing.md`](docs/ram-sizing.md) for full model recommendations by hardware tier.

### Register production Modelfiles

Modelfiles bake `num_ctx` and sampling parameters into model metadata so clients see the correct context window.

```bash
ollama create qwen3-coder-next-256k-agent -f modelfiles/qwen3-coder-next-256k-agent.modelfile
ollama create qwen3-coder-next-256k       -f modelfiles/qwen3-coder-next-256k.modelfile
ollama create qwen3-coder-next-128k       -f modelfiles/qwen3-coder-next-128k.modelfile

# Pin the primary model in memory to avoid cold-start delays
curl -s http://localhost:11434/api/generate \
  -d '{"model": "qwen3-coder-next-256k-agent", "keep_alive": -1}' > /dev/null
```

See [`docs/modelfile-guide.md`](docs/modelfile-guide.md) for parameter rationale and the agent vs chat split pattern.

### Point a coding agent at Ollama

```
Base URL: http://<mac-ip>:11434/v1
API Key:  (any string — Ollama ignores it)
Model:    qwen3-coder-next-256k-agent   (agentic tasks — use Zoo Code)
Model:    qwen3-coder-next-256k         (chat — use Opilot or Copilot)
```

**Note:** VS Code Copilot agent mode has a known tool call loop bug with local GGUF models. Use Zoo Code for agentic tasks. See [`docs/known-issues.md`](docs/known-issues.md).

---

## Troubleshooting

**Machine sleeps despite System Baseline**
```bash
pmset -g | grep -E "sleep|disablesleep|powermode"
sudo ./headless-macs    # → b (System Baseline, idempotent — safe to re-run)
```

**Ollama daemon not starting**
```bash
sudo launchctl print system/com.ollama.server
tail -50 /var/log/ollama/stderr.log
```

**Update Ollama to the latest version**
```bash
sudo ./headless-macs    # → u (Update Tools)
```

**Something went wrong — clean slate**
```bash
sudo ./headless-macs    # → r (Restore), then reboot
```

**Disable SIP (required for full service suppression on macOS 26 Tahoe — Apple Silicon)**

System Baseline warns and runs safely with SIP enabled, but some service-disable calls need SIP off to persist across reboots.

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
- `go build ./...` passes with no errors
- `go vet ./...` produces no warnings
- New ops stages follow the `BaselineAction` / `XxxResult` pattern in `internal/ops/`
- New LaunchDaemon plists include `UserName _llmserver`, `HOME=/Library/LLMServer`, and use `bootstrap`/`bootout`
- All shell script changes are idempotent — running twice produces `[SKIP]` for already-applied settings

## License

See [LICENSE](LICENSE).
