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
- Metal-accelerated on Apple Silicon (MLX backend in preview as of March 2026)
- Most mature and widely tested option
- Works well as a fallback while Rapid-MLX is in beta

**Weaknesses**
- Slower generation than Rapid-MLX or raw mlx-lm on the same hardware
- No prompt caching for hybrid RNN-attention architectures (DeltaNet models)
- Basic tool-call support — no auto-recovery on malformed output

**When to choose Ollama**
- You want to get started quickly
- You need to manage many different models
- You're running general-purpose workloads (not a dedicated coding agent)
- You want a stable, well-documented option

---

## Rapid-MLX

Rapid-MLX is a production-grade MLX-based inference server built specifically for Apple Silicon. It reimplements the serving stack with continuous batching, optimised prefill chunking, DeltaNet state snapshots, 17 tool-call format parsers with auto-recovery, and reasoning/content separation for Qwen3 and DeepSeek-R1.

**Benchmarked at 2–4.2× faster than Ollama** on the same hardware.

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
- Requires `--device mps` flag (set in plist — handled automatically by `install-tools.sh`)
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
- Tailscale discovery works across networks (not just LAN)
- OpenAI-compatible API

**Weaknesses**
- Runs as LaunchAgent (user-level), not LaunchDaemon — requires auto-login for headless boot
- Each node must have Exo installed and running
- Beta — less tested than single-node options
- Bonjour discovery limited to LAN; Tailscale recommended for production

**When to choose Exo**
- You have multiple Apple Silicon Macs you want to use as a cluster
- The model you need is larger than any single machine's unified memory
- You're comfortable with a more complex setup

**Requirements**
- Auto-login configured on every node (`sysadminctl -autologin set`)
- Tailscale installed and running on all nodes (for cross-network discovery)
- Same Exo version on all nodes
