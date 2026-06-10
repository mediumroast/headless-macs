# Modelfile Guide — Ollama Configuration for Production Inference

## Why Modelfiles Are Required

A Modelfile is not optional on a production inference server. It is the only mechanism that
bakes configuration into the model's metadata so clients see the correct values.

**The critical distinction:**

| Method | Who sees it | Persists? |
|---|---|---|
| Ollama UI context window slider | Ollama server only | No — lost on restart |
| `OLLAMA_MAX_CONTEXT` env var | Ollama server only | Via plist |
| Modelfile `PARAMETER num_ctx` | Baked into model metadata | Yes — clients read it |

VS Code, Zoo Code, and GitHub Copilot read `num_ctx` from the model's declared metadata via
`/api/show`. If you set 128K in the Ollama UI but the model card declares 256K, clients will
send 256K-sized requests regardless — causing the KV cache to grow and inference to slow
progressively across a session.

---

## MLX Models vs GGUF Models

**GGUF models (Ollama default):** Respect all Modelfile parameters including `num_ctx`.
This is the correct format for production use with Modelfiles.

**MLX-quantised models (Rapid-MLX, mlx-lm):** The context window is fixed at model
conversion time. Modelfile `num_ctx` has no effect. To change the context window on an
MLX model, the model must be re-converted with a different `--max-position-embeddings` value.

Use GGUF + Ollama when `num_ctx` control matters. Use MLX when raw generation speed
is the priority and context window is acceptable at its conversion-time default.

---

## Production Model: qwen3-coder-next Q6_K

The primary coding model on the doppios (128GB and 64GB configurations):

```
Architecture:  Qwen3-Next-80B-A3B — MoE, 3B parameters active per token
Quantisation:  Q6_K (~62 GB on disk)
Context:       256K tokens (262144)
Location:      /Users/mihay42/models/qwen3-coder-next-q6k/Qwen3-Coder-Next-Q6_K-merged.gguf
```

**Why Q6_K over Q8_0 or Q4_K_M:**
- Q8_0 at ~85GB leaves only ~43GB headroom on 128GB — insufficient when running
  embeddings alongside. `nomic-embed-text` adds ~275MB but the KV cache at 256K is the
  real pressure (see KV Cache section in `ram-sizing.md`)
- Q4_K_M at ~52GB loses more weight precision than Q6_K at ~62GB
- Q6_K leaves ~65GB free: enough for KV cache at full 256K context + embeddings + OS

**Why this model over alternatives:**
Trained on 800K *executable* tasks with environment interaction and reinforcement learning
— not static code-text pairs. In direct comparison against qwen3.6:35b on a structured
extraction task, qwen3-coder-next got item counts correct and faithfully represented source
state. qwen3.6 got counts wrong and editorialised.

---

## The Two-Modelfile Pattern: Agent vs Chat

The same GGUF weights serve two distinct Ollama model names via two Modelfiles. Clients
choose the appropriate one for their task type:

| Model name | Modelfile | Use case |
|---|---|---|
| `qwen3-coder-next-256k-agent` | `qwen3-coder-next-256k-agent.modelfile` | Zoo Code, agentic tasks, tool calling |
| `qwen3-coder-next-256k` | `qwen3-coder-next-256k.modelfile` | Chat, Copilot, explaining, writing docs |
| `qwen3-coder-next-128k` | `qwen3-coder-next-128k.modelfile` | When KV headroom is needed (multiple models loaded) |

The weights are loaded once into unified memory. Ollama references the same GGUF file from
both Modelfiles — no duplication on disk.

---

## Parameter Reference

### Agent Modelfile Parameters

| Parameter | Agent value | Chat value | Rationale |
|---|---|---|---|
| `num_ctx` | 262144 | 262144 | 256K — repository-scale context; GGUF respects this correctly |
| `num_keep` | 48 | 48 | Pins first 48 tokens (system prompt) in KV cache between calls; prevents re-encoding on each turn |
| `temperature` | 0.15 | 0.60 | Agent: low = deterministic tool call format; Chat: higher = more expressive prose |
| `top_k` | 20 | 40 | Agent: restricted to top 20 tokens; Chat: wider sampling for variety |
| `top_p` | 0.85 | 0.85 | Stays in high-probability space; improves schema adherence |
| `repeat_penalty` | 1.1 | 1.0 | Agent: slight penalty prevents identical tool call loops; Chat: disabled (code legitimately repeats) |
| `num_predict` | -1 | -1 | No token ceiling — without this the model silently truncates mid-response with no error |

### Why `temperature 0.15` for the agent

Tool call output is structured XML/JSON. At higher temperatures, the model samples from a
wider token distribution and occasionally produces malformed tag names, mismatched braces,
or invalid argument types. At 0.15, the model stays in the high-probability token space
where the correct format dominates. The tradeoff is less variation in prose, which is
acceptable for a coding assistant.

### Why `repeat_penalty 1.1` for the agent (not 1.0)

Without a repeat penalty, a model stuck in a tool call failure loop will reproduce the
exact same tool call on each retry. A penalty of 1.1 nudges it toward different approaches
after a failed call, reducing the tool call loop symptom without aggressively penalising
legitimate code repetition.

### Why `num_keep 48`

`num_keep` pins the first N tokens of context in the KV cache so they are never evicted
between requests in a session. The system prompt consumes the first tokens of every request.
Pinning them means the model doesn't re-encode the system prompt on every turn — reducing
time-to-first-token on subsequent requests in a session. 48 tokens covers the full system
prompt for both Modelfile variants.

---

## The TEMPLATE Section

The TEMPLATE is the most critical part of the Modelfile for tool calling. Both production
Modelfiles include a `<tool_response>` wrapper around tool results:

```
{{ else if eq .Role "tool" }}<|im_start|>user
<tool_response>
{{ .Content }}
</tool_response><|im_end|>
```

**Why this matters:** Qwen3's tool schema expects tool results wrapped in
`<tool_response>...</tool_response>`. Without this wrapper, the model cannot parse tool
results and enters a retry loop — issuing the same tool call repeatedly, getting errors back,
and never adapting. This is the fix for the VS Code Copilot tool call loop symptom.

The `<|im_start|>` and `<|im_end|>` markers are Qwen3's ChatML tokens. The STOP parameters
at the bottom ensure Ollama terminates generation at these boundaries.

---

## `/no_think` Flag

Both the agent Modelfile and the 128K legacy Modelfile include `/no_think` in the SYSTEM
prompt. This suppresses Qwen3's chain-of-thought `<think>...</think>` output blocks.

For the agent variant: the model still reasons internally when constructing tool calls, but
suppressing the output prevents thinking tokens from consuming context window and response
budget. The model's agentic training means it produces correct tool calls without surfacing
the reasoning.

For the chat variant (256K): `/no_think` is **not** present. Chat responses benefit from
visible reasoning when explaining architecture or debugging unfamiliar code.

---

## Registering Models with Ollama

```bash
# From the repo root on the doppio
ollama create qwen3-coder-next-256k-agent -f modelfiles/qwen3-coder-next-256k-agent.modelfile
ollama create qwen3-coder-next-256k       -f modelfiles/qwen3-coder-next-256k.modelfile
ollama create qwen3-coder-next-128k       -f modelfiles/qwen3-coder-next-128k.modelfile

# Verify registration
ollama list
```

**Important:** The `FROM` path in each Modelfile must point to the actual GGUF location on
the machine. The production path is:
```
/Users/mihay42/models/qwen3-coder-next-q6k/Qwen3-Coder-Next-Q6_K-merged.gguf
```

Update this path if the GGUF is stored elsewhere. The `ollama create` command does not copy
the file — it references it in place.

---

## Model Warmup and Memory Pinning

The 62GB GGUF has a cold-start pattern:

| Request | Behaviour |
|---|---|
| 1st | Slow (~30–60s) — loading from disk into unified memory |
| 2nd–3rd | Faster — KV cache warming |
| 4th+ | Steady state (~28 tok/s generation, ~23k tok/s prompt eval) |

**Don't start important work on the first request of a session.** Send a throwaway warmup:

```bash
ollama run qwen3-coder-next-256k-agent "hello" > /dev/null
```

**Pin the model in memory between sessions** to avoid cold starts entirely:

```bash
# Pin indefinitely (survives until Ollama is restarted)
curl -s http://doppio-1.lan:11434/api/generate \
  -d '{"model": "qwen3-coder-next-256k-agent", "keep_alive": -1}' \
  > /dev/null

# Verify it is loaded
curl -s http://doppio-1.lan:11434/api/ps | jq '.models[].name'
```

`keep_alive: -1` tells Ollama never to evict this model unless it needs memory for another
model or is explicitly unloaded. This is the recommended configuration for a dedicated
inference server with a single primary model.

---

## Context Window Variants

The 128K Modelfile exists for situations where KV cache headroom is limited:

| Modelfile | num_ctx | KV cache (approx) | Use when |
|---|---|---|---|
| `qwen3-coder-next-256k-agent` | 262144 | ~40–50 GB at full fill | Primary agentic use; doppio-1 sole user |
| `qwen3-coder-next-256k` | 262144 | ~40–50 GB at full fill | Chat; doppio-1 sole user |
| `qwen3-coder-next-128k` | 131072 | ~20–25 GB at full fill | Running a second model simultaneously; doppio-2 |

**Note:** KV cache grows with usage within a session, not on load. A model registered with
256K context only consumes the full KV cache if a client sends a 256K token request.
Typical coding sessions use 8–32K of context, so the KV cache stays well below the maximum.

---

## Recommended Client Configuration

| Client | Model to use | Endpoint |
|---|---|---|
| **Zoo Code** (agentic tasks) | `qwen3-coder-next-256k-agent` | `http://doppio-1.lan:11434` |
| **GitHub Copilot / Opilot** (chat) | `qwen3-coder-next-256k` | `http://doppio-1.lan:11434` |
| **Autocomplete** | `qwen2.5-coder:7b` (separate pull) | `http://doppio-1.lan:11434` |
| **Embeddings** | `nomic-embed-text` (separate pull) | `http://doppio-1.lan:11434` |

See `docs/known-issues.md` for the VS Code Copilot agent mode tool call loop issue and
why Zoo Code is used for agentic tasks.
