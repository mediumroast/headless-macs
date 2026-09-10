# Tool Comparison — When to Use Each

## Quick Selection Guide

| I want to… | Use |
|---|---|
| Run models with minimal setup, pull from a registry | **Ollama** |
| Maximum generation speed for a coding agent (Claude Code, Cursor, Aider) | **Rapid-MLX** |
| Serve a specific HuggingFace model not in Rapid-MLX's alias list | **mlx-lm** |
| Add embeddings or reranking to a RAG pipeline | **Infinity** |
| Pool multiple Macs to run a model too large for one machine | **Exo** |

---

## The Combinations That Make Sense

| Setup | Tools |
|---|---|
| Solo node, general use | Ollama only |
| Solo node, coding agent (Claude Code / Cursor) | Rapid-MLX + Infinity |
| Solo node, RAG app | Ollama + Infinity |
| Solo node, custom HuggingFace models | mlx-lm + Infinity |
| Multi-Mac cluster | Exo + Infinity on a dedicated node |

---

## Side-by-Side Comparison

| Dimension | Ollama | Rapid-MLX | mlx-lm | Infinity | Exo |
|---|---|---|---|---|---|
| **Primary use** | General inference + model management | Max speed + tool calling | Raw HF model serving | Embeddings + reranking | Distributed inference |
| **API** | OpenAI-compatible | OpenAI-compatible | OpenAI-compatible | OpenAI-compatible | OpenAI-compatible |
| **Default port** | 11434 | 8000 | 8080 | 7997 | 52415 |
| **Install method** | `curl install.sh` | `brew` / `pip3` | `pip3` | `pip3` | `brew` / `pip3` |
| **Model source** | `ollama pull <model>` | Alias-based (`rapid-mlx models`) | HuggingFace repo ID | HuggingFace repo ID | Auto via cluster |
| **Apple Silicon acceleration** | Metal (MLX backend preview) | Native MLX | Native MLX | MPS (`--device mps`) | MLX per node |
| **Prompt caching** | Partial | Yes, incl. DeltaNet for RNN hybrids | No | N/A | Partial |
| **Tool calling** | Basic | 17 parsers + auto-recovery | No | N/A | Basic |
| **Reasoning separation** | No | Yes (Qwen3, DeepSeek-R1) | No | N/A | No |
| **Built-in diagnostics** | Log inspection | `rapid-mlx doctor` | Log inspection | Log inspection | Log inspection |
| **Runs as** | LaunchDaemon (root) | LaunchDaemon (root) | LaunchDaemon (root) | LaunchDaemon (root) | LaunchAgent (user) |
| **Maturity** | Stable | Beta (v0.6, April 2026) | Stable | Stable | Beta |
| **Best for** | General use, easy model management | Coding agents, max speed | Custom HF models | RAG embeddings | Multi-Mac clusters |

---

## Ollama

**Strengths**
- Easiest model management — `ollama pull`, `ollama list`, `ollama run`
- Large model registry with quantised variants
- MLX backend on Apple Silicon since Ollama 0.19 (March 2026) — narrows speed gap with pure MLX tools
- Modelfile system bakes `num_ctx` and sampling parameters into model metadata — the only
  way clients see the correct context window (see `docs/modelfile-guide.md`)
- Most mature and widely tested option

**Weaknesses**
- VS Code Copilot agent mode is broken for local GGUF models — tool call loop bug
  (microsoft/vscode-copilot-chat #3566). Use Zoo Code for agentic tasks.
- Basic tool-call support — no auto-recovery on malformed output (mitigated by Modelfile tuning)
- `num_ctx` must be set via Modelfile, not the UI — Ollama UI override does not update
  the metadata that clients read

**When to choose Ollama**
- You want to get started quickly
- You need to manage many different models via a registry
- You're serving GGUF models with controlled context windows (Modelfile required)
- You're running general-purpose workloads with Zoo Code or Opilot as the client

---

## Rapid-MLX

Rapid-MLX is a production-grade MLX-based inference server built specifically for Apple Silicon. It reimplements the serving stack with continuous batching, optimised prefill chunking, DeltaNet state snapshots, 17 tool-call format parsers with auto-recovery, and reasoning/content separation for Qwen3 and DeepSeek-R1.

**Previously benchmarked at 2–4.2× faster than Ollama.** Since Ollama 0.19 adopted the MLX
backend on Apple Silicon (March 2026), the raw generation speed gap has narrowed to ~15–30%.
Rapid-MLX retains meaningful advantages in serving sophistication over Ollama 0.19.

**Strengths**
- Fastest generation on Apple Silicon
- DeltaNet prompt caching — works for hybrid RNN-attention models (Mamba, Jamba) that Ollama and mlx-lm cannot cache
- 17 tool-call parsers with auto-recovery on malformed output — critical for coding agent reliability
- Reasoning separation for Qwen3 / DeepSeek-R1 (`--no-thinking` flag strips reasoning tokens)
- `rapid-mlx doctor` built-in self-diagnostic
- `--prefill-step-size 8192` fixes slow cold-start on long prompts

**Weaknesses**
- Beta (v0.6, April 2026) — not yet suitable as your only option in critical production
- Model aliases (`rapid-mlx models`) don't cover every HF model — use mlx-lm for those
- First `serve` downloads the model — API unavailable until download completes
- Vision/audio require extras: `pip install 'rapid-mlx[vision]'`
- **Holds its model resident in unified memory for as long as the daemon
  runs, regardless of request activity** — confirmed ~20–25GB continuously
  consumed on doppio-1 with qwen3-aftertaste-fused. If you also run Ollama
  on the same node, account for this footprint when tuning Ollama's
  `MAX_LOADED_MODELS`, or the node can be overcommitted. `headless-macs`
  precheck warns when both are enabled together, but does not currently
  adjust Ollama's tuning for you.

**When to choose Rapid-MLX**
- Primary use case is a coding agent (Claude Code, Cursor, Aider, Continue)
- You need reliable tool calling
- Generation speed is the priority
- You're running Qwen3 or DeepSeek-R1 and want reasoning separation

**Rapid-MLX vs mlx-lm**
Use Rapid-MLX as the default. Fall back to raw mlx-lm only when you need a specific HuggingFace model path not covered by Rapid-MLX's model aliases.

---

## mlx-lm

Apple's own ML framework serving layer. Provides an OpenAI-compatible HTTP server for HuggingFace models.

**Strengths**
- Direct HuggingFace model path support — any `mlx-community/` quantised model
- Minimal dependency surface
- Official Apple framework

**Weaknesses**
- Lower-level than Rapid-MLX — no prompt caching, no tool-call recovery, no reasoning separation
- Requires downloading the model before the server can start (plist is written but not bootstrapped if `default_model` is empty)
- No built-in diagnostics

**When to choose mlx-lm**
- You need a specific HuggingFace model not aliased in Rapid-MLX
- You want minimal dependencies
- You're experimenting with Apple's MLX framework directly

**Important:** MLX models do not respect Modelfile `num_ctx`. The context window is fixed
at model conversion time. If clients need to see a specific context window via model
metadata, use GGUF + Ollama instead.

---

## Infinity

Production-grade embedding and reranking server. Uses MPS (Metal Performance Shaders) for GPU-accelerated inference on Apple Silicon.

**Key endpoints**
- `POST http://host:7997/v1/embeddings` — OpenAI-compatible embedding
- `POST http://host:7997/v1/rerank` — Cross-encoder reranking
- `GET  http://host:7997/v1/models` — List loaded models

**Strengths**
- MPS-accelerated — significantly faster than CPU-only embedding servers
- OpenAI-compatible — drop-in for `openai.Embedding.create()`
- Supports both embeddings and reranking from a single server
- High throughput for RAG pipelines

**Weaknesses**
- Embedding-only — not a generation server
- Requires `--device mps` flag (set in plist — handled automatically by `headless-macs install-tools`)
- Without `--device mps`, falls back to CPU at ~10× lower throughput

**When to choose Infinity**
- Any RAG pipeline — pairs with Ollama or Rapid-MLX for generation
- When you need reranking (cross-encoder) in addition to embeddings
- When embedding throughput matters at scale

---

## Exo

Clusters multiple Apple Silicon Macs into a single distributed inference node. Pools unified memory across devices to run models larger than any single machine can hold.

**Strengths**
- Run 405B models across 3× Mac Mini M4 64GB (pooling 192GB)
- OpenAI-compatible API

**Weaknesses**
- Runs as LaunchAgent (user-level), not LaunchDaemon — requires auto-login for headless boot
- Each node must have Exo installed and running
- Beta — less tested than single-node options

**When to choose Exo**
- You have multiple Apple Silicon Macs you want to use as a cluster
- The model you need is larger than any single machine's unified memory
- You're comfortable with a more complex setup

**Requirements**
- Auto-login configured on every node (`sysadminctl -autologin set`)
- Same Exo version on all nodes (`tools.exo.bootstrap_peers` in
  `config.json` lists other nodes' libp2p addresses to dial on startup for
  cross-network clustering; same-LAN/same-namespace nodes auto-discover
  without it — see `--namespace`/`--zenoh-port`/`--discovery-port` in
  exo's own `--help` for the underlying mechanism)

> **Corrected 2026-09:** earlier versions of this doc described Tailscale-
> based discovery and a `--discovery-module` flag. Neither exists in
> current exo — confirmed by reading `exo-explore/exo`'s actual CLI source
> while fixing `headless-macs`' Exo integration, which had been passing
> that flag and failing to start as a result. Peer discovery is
> zenoh/libp2p-based (`--bootstrap-peers`), not a pluggable
> tailscale-or-otherwise module.

---

## macmon (hardware telemetry — not an inference tool)

Not a serving/inference backend like the five tools above — [`macmon`](https://github.com/vladkens/macmon) is an optional hardware telemetry daemon (`com.llm-server.macmon`) exposing CPU/GPU/ANE power draw, temperature, and memory stats over HTTP (`GET /json`, `GET /metrics` in Prometheus format). It reads this through a private macOS API rather than `powermetrics`, so it needs no root privilege — confirmed to work from a system LaunchDaemon with no console session logged in.

**Why you'd enable it**
- Confirms an inference node isn't thermal-throttling under sustained load
- `/metrics` is a ready-made Prometheus scrape target if you're building any observability around this node
- Zero cost when disabled — `tools.macmon.enabled` defaults to `false`

**Known limitation**
- The Homebrew-installed `macmon` build has not consistently shipped a `--host`/`--bind` flag on `serve`. `headless-macs` detects this at install time: if the flag is present, `network.localhost_only` is honored exactly like every other tool; if it isn't, macmon binds all interfaces regardless of that setting, and both `install-tools` and `verify` surface a `[WARN]` explaining why. Run `sudo headless-macs update-tools` to upgrade macmon (and re-check for `--host` support) in place — `install-tools` alone only installs macmon if it's absent, it doesn't upgrade an existing one.
- No authentication or TLS, same as every other tool this project installs — deferred pending broader security work (see `FUTURES.md`).

**Requirements:** none beyond what `install-tools` handles — Homebrew install, `/var/log/macmon/` log directory, and the daemon itself are all automatic once `tools.macmon.enabled` is `true`.
