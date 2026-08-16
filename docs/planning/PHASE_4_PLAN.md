# Phase 4 Plan — Modelfile Tooling, Memory Model, and Knowledge Gaps

**Date:** 2026-05-28
**Informed by:** session-summary-model-optimization.md + doppio-1 production inventory

---

## What This Phase Addresses

Phases 1–3 establish the inference stack. This phase documents and codifies
the operational knowledge that has accumulated in practice but is not yet
captured in the project — Modelfile configuration, the KV-cache memory model,
multi-context-window variants, agent vs. chat model splits, and known client
tooling failures. It also updates docs to reflect the current production model
inventory on doppio-1.

---

## Current Production Model Inventory (doppio-1, 128GB M4 Max Studio)

| Name | Size | Role | Notes |
|---|---|---|---|
| `qwen3-coder-next-q6k-256k-agent` | 65 GB | Agentic coding (primary) | Agent-optimised Modelfile; most recent |
| `qwen3-coder-next-q6k-256k` | 65 GB | Agentic coding (chat variant) | Full context, chat sampling params |
| `qwen3-coder-next-q6k-128k` | 65 GB | Agentic coding | Reduced context; more KV headroom |
| `qwen3-coder-next-q6k-64k` | 65 GB | Agentic coding | Further reduced context |
| `qwen3-coder-next-q6k-32k` | 65 GB | Agentic coding | Minimum context; maximum headroom |
| `qwen3.6:35b-mlx` | 21 GB | Generalist / creative / docs | MLX quant; kept for non-agentic tasks |
| `nemotron-cascade-2:30b` | 24 GB | — | See KV cache constraint below |
| `qwen2.5-coder:32b` | — | Fallback / comparison | |

### nemotron-cascade-2:30b constraint

24 GB weights look safe on paper, but the KV cache at large context windows
makes it unworkable alongside the 65 GB coder model. With both loaded:

```
65 GB (qwen3-coder-next) + 24 GB (nemotron) + KV cache (nemotron @ 256K ≈ 20–30 GB)
  + OS + other = exceeds 128 GB
```

The usable configuration is nemotron at ≤32K context with no other large model
loaded. Not practical for the primary workflow.

---

## 4.1 — Modelfile Documentation (new `docs/modelfile-guide.md`)

Modelfiles are currently absent from the project. This is a significant gap:
without a Modelfile, clients read `num_ctx` from model card metadata, not from
the Ollama UI override. VS Code Copilot and other clients send full-sized
requests based on the declared context — leading to the KV-cache
growth/slowdown problem described in the session summary.

**Document to cover:**

- Why Modelfiles are required (not optional): `num_ctx` must be baked into
  model metadata to be seen by clients
- MLX models (Rapid-MLX, mlx-lm) do **not** respect Modelfile `num_ctx` — the
  context window is fixed at MLX conversion time; only GGUF/Ollama models
  respect Modelfile parameters
- The multi-context-window pattern: one GGUF, multiple named Ollama models
  with different `num_ctx` values — when to use each
- Agent vs. chat Modelfile split: same weights, different sampling parameters
  for different use cases
- Key parameters with rationale (from production Modelfile):

| Parameter | Value | Rationale |
|---|---|---|
| `temperature` | 0.15 | Reduces tool call format errors; deterministic output |
| `top_p` | 0.70 | Keeps model in high-probability token space; improves schema adherence |
| `top_k` | 20 | Reinforces determinism without greedy decoding |
| `repeat_penalty` | 1.0 | Disabled — code legitimately repeats tokens |
| `num_predict` | -1 | Prevents silent mid-response truncation |
| `num_keep` | 4 | Pins system prompt tokens in KV cache between calls |
| `num_parallel` | 1 | Single-user; prevents memory contention |
| `<tool_response>` wrapper | in TEMPLATE | Required by Qwen3 tool schema; without it tool results are unparseable |

- `ollama create` workflow (the step missing from session-summary)
- Model warmup and `keep_alive: -1` to pin a model in memory across sessions

**Files:** `docs/modelfile-guide.md` (new), `README.md` (add link)

---

## 4.2 — KV Cache Memory Model (`docs/ram-sizing.md` update)

The current ram-sizing table lists model weights only. This understates real
memory pressure. Actual requirement:

```
unified_memory_required = model_weights + KV_cache + OS (~8 GB) + other loaded models
```

KV cache size (approximate):

```
KV_cache ≈ num_ctx × num_layers × 2 × num_heads × head_dim × bytes_per_element
```

For a 30B-class model at 256K context this is 20–30 GB on top of weights.

**Changes to `docs/ram-sizing.md`:**

- Add a KV cache sizing section / formula
- Add a worked example: nemotron-cascade-2:30b at 256K context on 128GB
- Revise the model recommendation table to note effective memory requirement
  (weights + KV @ recommended context) not just weights
- Add guidance: use lower `num_ctx` Modelfile variants when running multiple
  models simultaneously

**Files:** `docs/ram-sizing.md`

---

## 4.3 — Known Issues Updates (`docs/known-issues.md`)

### New: VS Code Copilot agent mode — tool call loop

VS Code Copilot agent mode is broken for local GGUF models via Ollama.
The model repeatedly issues the same 4–5 tool calls, receives `isError`
results, and retries indefinitely. No error is surfaced to the user.

- Root cause: Ollama models not correctly identified as supporting tool calls;
  tools field stripped from requests (microsoft/vscode-copilot-chat #3566)
- Fix: use Zoo Code, which implements its own agent loop and bypasses VS Code's
  Copilot orchestration layer entirely

### New: MLX models ignore Modelfile `num_ctx`

Rapid-MLX and mlx-lm serve MLX-quantised models. The context window is fixed
at MLX conversion time — Modelfile `num_ctx` has no effect. To change context
window on an MLX model, the model must be re-converted.

### New: VS Code connection leak (Remote-SSH + Ollama tunnel)

VS Code's Node.js HTTP connection pool never evicts idle connections. Under
Remote-SSH with an Ollama SSH tunnel, connection count climbs:
58 → 80 → 136 → 236 → 486 over 30 minutes.

Mitigation (Linux remote host):
```bash
sudo sysctl -w net.ipv4.tcp_keepalive_time=60
sudo sysctl -w net.ipv4.tcp_keepalive_intvl=10
sudo sysctl -w net.ipv4.tcp_keepalive_probes=3
sudo sysctl -w net.ipv4.tcp_fin_timeout=15
sudo sysctl -w net.ipv4.tcp_tw_reuse=1
```
Persist to `/etc/sysctl.conf`. Stabilises count at ~162.

Also reduce VS Code polling:
```json
"opilot.localModelRefreshInterval": 300,
"opilot.libraryRefreshInterval": 86400
```

**Files:** `docs/known-issues.md`

---

## 4.4 — Tool Comparison Update (`docs/tool-comparison.md`)

- Add a note to the Ollama section: Modelfile required for correct client
  context window behaviour (forward link to `docs/modelfile-guide.md`)
- Add a note to Rapid-MLX / mlx-lm: MLX models do not respect Modelfile
  `num_ctx` — context window is fixed at conversion time
- Add Zoo Code to the "Best for" list where Roo Code appears (or remove
  Roo Code entirely — Zoo Code is the current successor)

**Files:** `docs/tool-comparison.md`

---

## 4.5 — README Updates

- Add `docs/modelfile-guide.md` to the file structure tree and docs table
- Add model warmup / `keep_alive` to the "After Installation" section
- Note the two-model split pattern (coder model + generalist model) as a
  recommended configuration for 64 GB+ machines

**Files:** `README.md`

---

## Open Items (to be filled in)

_Additional detail to be added by owner:_

- [ ] Production Modelfile for `qwen3-coder-next-q6k-256k-agent` (full content)
- [ ] Production Modelfile for `qwen3-coder-next-q6k-256k` (chat variant)
- [ ] Nemotron Cascade 2 use case / role (if any) — or document as
      "does not fit this hardware configuration"
- [ ] Confirmed KV cache figures from production observation on doppio-1
- [ ] `ollama create` command used to register each named model
- [ ] Any additional context window variants and their intended use cases

---

## Files Changed / Created

| File | Change |
|---|---|
| `docs/modelfile-guide.md` | New |
| `docs/ram-sizing.md` | KV cache section + revised model table |
| `docs/known-issues.md` | VS Code tool loop, MLX num_ctx, connection leak |
| `docs/tool-comparison.md` | Modelfile notes, Zoo Code, MLX num_ctx caveat |
| `README.md` | modelfile-guide link, keep_alive pattern, two-model split |

---

## Sequencing

```
4.1  modelfile-guide.md     — no dependencies; start here
4.2  ram-sizing.md          — no dependencies; can run in parallel with 4.1
4.3  known-issues.md        — no dependencies; can run in parallel
4.4  tool-comparison.md     — after 4.1 (links to modelfile-guide)
4.5  README.md              — last (links to all new docs)
```
