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
| MacBook Air M3/M4 | 16 GB | 8B Q4 (qwen3:8b); 1 model at a time |
| MacBook Air M3 / Mac Mini M4 | 24 GB | 14B Q4 (qwen3:14b) or 30B MoE Q4 (qwen3-coder:30b) |
| MacBook Pro M4 / Mac Mini M4 Pro | 32 GB | 32B Q4 (qwen3:32b, deepseek-r1:32b); 2 models |
| Mac Mini M4 Max / Mac Studio M4 Max | 64 GB | 70B Q4 (llama3.3:70b, deepseek-r1:70b); 32B Q8 alongside |
| Mac Mini / Studio M4 Max (Mac16,9) | 128 GB | 70B Q8 or 122B Q4 (qwen3.5:122b); 70B Q4 + 32B Q8 pair; ~22–25 tok/s on 70B |
| Mac Studio M4 Ultra | 192 GB | 235B Q4 (qwen3:235b, 142 GB); multiple 70B models simultaneously |
| Mac Pro M2 Ultra | 192 GB | Same as Studio Ultra |

---

## Model Size × Quantization Reference

| Model | Q4_K_M | Q5_K_M | Q8_0 | F16 |
|---|---|---|---|---|
| 3B | ~2 GB | ~2.5 GB | ~3.5 GB | ~6 GB |
| 7B | ~4.5 GB | ~5.5 GB | ~8 GB | ~14 GB |
| 8B | ~5 GB | ~6 GB | ~9 GB | ~16 GB |
| 13B | ~8 GB | ~10 GB | ~14 GB | ~26 GB |
| 14B | ~9 GB | ~11 GB | ~15 GB | ~28 GB |
| 30B MoE¹ | ~19 GB | ~22 GB | ~38 GB | ~60 GB |
| 32B | ~20 GB | ~24 GB | ~35 GB | ~64 GB |
| 70B | ~40 GB | ~48 GB | ~75 GB | ~140 GB |
| 72B | ~41 GB | ~49 GB | ~77 GB | ~144 GB |
| 122B | ~81 GB | — | — | — |
| 235B MoE¹ | ~142 GB | — | — | — |

¹ MoE models have large total parameter counts but small active parameter counts per token. Memory for weights is determined by total parameters; compute is determined by active parameters. `qwen3-coder:30b` and `qwen3:235b` are both MoE.

**Rule of thumb:** leave ~4 GB for macOS overhead and budget for KV cache (see below). On a 64 GB machine, your usable model budget is well under 60 GB at large context windows.

---

## Recommended Models by Hardware

Model families current as of mid-2026. Qwen3/Qwen3-Coder supersede Qwen2.5/Qwen2.5-Coder.
Llama 3.3 70B remains the best general dense model at the 64 GB tier. DeepSeek-R1 (0528)
is the leading open reasoning model at every size. Llama 3.x and Qwen2.5 pull commands
still work but pull older-generation weights.

### 16 GB (e.g. MacBook Air M3/M4 base)

```bash
# General chat
ollama pull qwen3:8b                   # 5.2 GB — best general at this tier; thinking mode on demand

# Coding
ollama pull qwen2.5-coder:7b           # 4.4 GB — top HumanEval score at 7B class

# Reasoning / chain-of-thought
ollama pull deepseek-r1:8b             # 5.2 GB — 0528 distill; strong math and logic

# Embeddings (sideload alongside any generation model)
ollama pull nomic-embed-text           # 274 MB — most popular, best default
```

### 24 GB (e.g. MacBook Air M3 / Mac Mini M4 base)

```bash
# General chat
ollama pull qwen3:14b                  # 9.3 GB — fast, capable, 128K context

# Coding + agentic tasks (MoE: 30B params, only 3.3B active — efficient)
ollama pull qwen3-coder:30b            # 19 GB — 256K context; best local coding model

# Reasoning
ollama pull deepseek-r1:14b            # 9.0 GB — best mid-range reasoning

# Vision (text + image)
ollama pull gemma4:27b                 # 17 GB — vision, tool use, and thinking mode

# Embeddings
ollama pull mxbai-embed-large          # 670 MB — matches OpenAI ada-002 quality
```

### 32 GB (e.g. MacBook Pro M4 / Mac Mini M4 Pro)

```bash
# General chat
ollama pull qwen3:32b                  # 20 GB — top dense model at this tier

# Coding (pick one — both fit)
ollama pull qwen3-coder:30b            # 19 GB — agentic coding, 256K context
ollama pull qwen2.5-coder:32b          # 20 GB — 92.7% HumanEval; best pure coding

# Reasoning
ollama pull deepseek-r1:32b            # 20 GB — best local reasoning at this tier

# Embeddings
ollama pull mxbai-embed-large          # 670 MB
```

### 64 GB (e.g. Mac Mini M4 Max / Mac Studio M4 Max)

```bash
# General chat
ollama pull llama3.3:70b               # 43 GB — 128K context; excellent instruction following

# Coding
ollama pull qwen3-coder:30b            # 38 GB at Q8 — best agentic coding quality here
ollama pull qwen2.5-coder:32b          # 40 GB at Q8 — top HumanEval score

# Reasoning
ollama pull deepseek-r1:70b            # 43 GB — best local reasoning model available

# Simultaneous pair: general + coding in memory at once
# llama3.3:70b Q4 (~43 GB) + qwen3-coder:30b Q4 (~19 GB) = ~62 GB — comfortable

# Embeddings + reranking (via Infinity)
ollama pull bge-m3                     # 570 MB — multilingual, 8K context
```

### 128 GB (e.g. Mac Mini M4 Max, Mac Studio M4 Max — Mac16,9)

Run 70B-class models at Q8 (near-lossless). This is the sweet spot for this tier — Q8/70B
outperforms Q3/235B on most benchmarks. `qwen3:235b` (142 GB at Q4) does **not** fit here.

```bash
# General chat
ollama pull llama3.3:70b               # ~86 GB at Q8 — excellent quality; pin in memory
ollama pull qwen3.5:122b               # ~81 GB at Q4 — vision + text, 256K context

# Coding
ollama pull qwen3-coder:30b            # ~38 GB at Q8 — agentic; pair alongside 70B
ollama pull qwen2.5-coder:32b          # ~40 GB at Q8 — pure coding quality

# Reasoning
ollama pull deepseek-r1:70b            # ~86 GB at Q8 — best reasoning on a single node

# Simultaneous pair: two models resident at once
# llama3.3:70b Q4 (~43 GB) + qwen2.5-coder:32b Q8 (~40 GB) = ~83 GB — comfortable
# llama3.3:70b Q8 (~86 GB) alone, evicts on second large model request

# Embeddings
ollama pull bge-m3                     # 570 MB
```

### 192 GB (e.g. Mac Studio M4 Ultra)

```bash
# General chat — flagship local model
ollama pull qwen3:235b                 # 142 GB at Q4 — best openly-available general model

# Coding
ollama pull qwen3-coder:30b            # 38 GB at Q8 — run alongside qwen3:235b
ollama pull qwen2.5-coder:32b          # 40 GB at Q8

# Reasoning
ollama pull deepseek-r1:70b            # 86 GB at Q8 — run alongside a 32B coding model

# Multiple large models simultaneously:
# qwen3:235b Q4 (142 GB) + qwen3-coder:30b Q8 (38 GB) = ~180 GB — tight but fits
# llama3.3:70b Q8 (86 GB) + deepseek-r1:70b Q8 (86 GB) = ~172 GB — comfortable

# Embeddings
ollama pull bge-m3                     # 570 MB
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
| Mac Mini / Studio M4 Max (Mac16,9) | 128 GB | 8 GB | 70B Q8 (~77 GB) | ~43 GB | 256K context comfortable; 32B alongside at Q4 |
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
