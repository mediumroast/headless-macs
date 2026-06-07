# RAM Sizing — Model Selection Reference

## Auto-Tune Tiers (used by install-tools.sh)

`install-tools.sh` automatically sets Ollama's environment variables based on detected RAM:

| RAM | MAX_LOADED_MODELS | NUM_PARALLEL | MAX_CONTEXT |
|---|---|---|---|
| ≤ 16 GB | 1 | 1 | 8,192 |
| 17–24 GB | 2 | 2 | 16,384 |
| 25–32 GB | 2 | 3 | 32,768 |
| 33–64 GB | 3 | 4 | 32,768 |
| ≥ 65 GB | 4 | 8 | 65,536 |

Override any value in `config.json` under `tools.ollama`.

---

## Hardware Capability Reference

| Mac Model | RAM | Practical Capability |
|---|---|---|
| MacBook Air M3 | 16 GB | 7B Q8 only; 1 model at a time |
| MacBook Air M3 | 24 GB | 13B Q8 or 7B Q8 + embeddings |
| MacBook Pro M4 | 32 GB | 32B Q4 or 13B Q8; 2 models |
| Mac Mini M4 | 64 GB | 70B Q4 or 32B Q5; 3 models |
| Mac Studio M4 Max | 128 GB | 70B Q8 or multiple 32B/70B |
| Mac Studio M4 Ultra | 192 GB | 405B Q4; multiple large models |
| Mac Pro M2 Ultra | 192 GB | Same as Studio Ultra |

---

## Model Size × Quantisation Reference

| Model | Q4_K_M | Q5_K_M | Q8_0 | F16 |
|---|---|---|---|---|
| 3B | ~2 GB | ~2.5 GB | ~3.5 GB | ~6 GB |
| 7B | ~4.5 GB | ~5.5 GB | ~8 GB | ~14 GB |
| 8B | ~5 GB | ~6 GB | ~9 GB | ~16 GB |
| 13B | ~8 GB | ~10 GB | ~14 GB | ~26 GB |
| 14B | ~9 GB | ~11 GB | ~15 GB | ~28 GB |
| 32B | ~20 GB | ~24 GB | ~35 GB | ~64 GB |
| 70B | ~40 GB | ~48 GB | ~75 GB | ~140 GB |
| 72B | ~41 GB | ~49 GB | ~77 GB | ~144 GB |
| 405B | ~230 GB | — | — | — |

**Rule of thumb:** leave ~4 GB for macOS overhead and budget for KV cache (see below). On a 64 GB machine, your usable model budget is well under 60 GB at large context windows.

---

## Recommended Models by Hardware

### 16 GB (e.g. MacBook Air M3 base)

```bash
# Generation — one at a time
ollama pull qwen2.5-coder:7b-instruct-q8_0    # 8 GB — coding
ollama pull llama3.2:3b-instruct-q8_0         # 3.5 GB — fast general

# Embeddings (sideload alongside generation model)
ollama pull nomic-embed-text                   # ~275 MB
```

### 24 GB (e.g. MacBook Air M3 / Mac Mini M4 base)

```bash
# Generation
ollama pull qwen2.5-coder:7b-instruct-q8_0    # 8 GB
ollama pull llama3.1:8b-instruct-q8_0         # 9 GB

# Embeddings
ollama pull mxbai-embed-large                  # ~670 MB
```

### 32 GB (e.g. MacBook Pro M4)

```bash
# Generation
ollama pull qwen2.5-coder:32b-instruct-q4_K_M  # ~20 GB — best coding at this tier
ollama pull qwen2.5:14b-instruct-q8_0          # ~15 GB — general

# Embeddings
ollama pull mxbai-embed-large
```

### 64 GB (e.g. Mac Mini M4 Pro / Mac Studio M4 Max)

```bash
# Generation
ollama pull qwen2.5:72b-instruct-q4_K_M        # ~41 GB — large general
ollama pull qwen2.5-coder:32b-instruct-q5_K_M  # ~24 GB — coding
ollama pull deepseek-r1:32b-q5_K_M             # ~24 GB — reasoning

# Embeddings + reranking (via Infinity)
# michaelfeil/bge-small-en-v1.5 — fast, low memory
# BAAI/bge-large-en-v1.5        — higher quality
```

### 128 GB+ (e.g. Mac Studio M4 Ultra)

```bash
# Generation
ollama pull qwen2.5:72b-instruct-q8_0          # ~77 GB — best single-node quality
ollama pull qwen2.5-coder:32b-instruct-q8_0    # ~35 GB — coding
ollama pull llama3.1:70b-instruct-q8_0         # ~75 GB

# Multiple models loaded simultaneously at this tier
```

---

## KV Cache — The Hidden Memory Cost

Model weights are only part of the memory equation. The KV (key-value) cache grows with
context window size and is the primary cause of unexpected memory pressure on large models.

```
Total memory = model weights + KV cache + OS (~8 GB) + other loaded models
```

### KV Cache Size Formula

```
KV_cache ≈ num_ctx × num_layers × 2 × num_heads × head_dim × bytes_per_element
```

For practical estimation, use these approximate figures:

| Model class | Context | KV cache (approx) |
|---|---|---|
| 7B–8B dense | 128K | ~4–6 GB |
| 7B–8B dense | 256K | ~8–12 GB |
| 30B–35B dense | 128K | ~10–15 GB |
| 30B–35B dense | 256K | ~20–30 GB |
| 80B MoE (3B active) | 128K | ~8–12 GB |
| 80B MoE (3B active) | 256K | ~15–25 GB |

KV cache grows with **usage within a session**, not on load. A model registered with 256K
context only consumes the full cache if a client sends a full 256K token request.
Typical coding sessions use 8–32K, so the cache stays well below maximum.

### Real Example: nemotron-cascade-2:30b on 128 GB

```
62 GB  qwen3-coder-next Q6_K (primary model)
24 GB  nemotron-cascade-2:30b weights
25 GB  nemotron KV cache at 256K context
 8 GB  macOS overhead
─────
119 GB — leaves only 9 GB margin. Any additional load causes eviction.
```

At ≤32K context, nemotron's KV cache drops to ~5 GB, making both models loadable.
But a single 256K context request causes the primary model to be evicted.
**Verdict:** nemotron-cascade-2:30b is not compatible with the primary 256K workflow on 128 GB.

### Effective Memory Budget by Hardware

| Mac | RAM | OS | Primary model | KV headroom | Notes |
|---|---|---|---|---|---|
| Mac Mini M4 | 64 GB | 8 GB | 32B Q6_K (~26 GB) | ~30 GB | 128K context comfortable; 256K tight |
| Mac Studio M4 Max | 128 GB | 8 GB | 80B MoE Q6_K (~62 GB) | ~58 GB | 256K context comfortable; embeddings alongside |
| Mac Studio M4 Ultra | 192 GB | 8 GB | 80B MoE Q8_0 (~85 GB) | ~99 GB | Multiple large models; full 256K on all |

---

## Quantisation Quality Guide

| Quantisation | Quality | Speed | Use When |
|---|---|---|---|
| Q8_0 | Near-lossless | Fast | Fits in RAM — always prefer over lower quants |
| Q5_K_M | Excellent | Fast | Q8 doesn't fit; best quality/size trade-off |
| Q4_K_M | Good | Very fast | Need to fit a larger model in limited RAM |
| Q3_K_M | Acceptable | Very fast | Last resort for very limited RAM |
| F16 | Lossless | Moderate | Fine-tuning or evaluation only — not for inference |

**Recommendation:** Use Q8 if it fits. Drop to Q5 before Q4. Q4 is fine for most uses. Avoid Q3 except for 3B/7B models where the absolute size is small enough that quality degradation matters more.
