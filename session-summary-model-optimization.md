# Session Summary — Model Selection & Qwen3-Coder-Next Optimization
## Sessions: 2026-05-02 through 2026-05-13

---

## Phase 1: Model selection (2026-05-02)

### Starting question

"Using Continue in VS Code and Ollama on-prem on a Mac M4 Max Studio with 128GB of memory — what is the best model that will provide the right balance of performance and quality?"

### Initial recommendation

Based on research and the M4 Max 128GB hardware profile, the initial stack was:

- **Chat/agentic:** `qwen3.5:35b` (MoE, 3B active parameters, ~20GB at 4-bit, fast on Apple Silicon)
- **Autocomplete:** `qwen2.5-coder:7b` (FIM support, low latency)
- **Embeddings:** `nomic-embed-text`

Rationale: the MoE architecture means only 3B parameters are active per token despite the 35B model size. Inference is fast because the memory bandwidth load is much lower than a dense model of equivalent quality. On 128GB hardware, the model leaves 100GB+ headroom.

### Continue.dev tool calling problems

Continue's agent mode was failing with `create_file` errors. Root cause: Continue's `apply` role without proper tool support falls back to trying agentic file write calls that aren't wired up correctly for Ollama-served models. Fixes attempted:

- Removed `apply` from model roles
- Separated chat model from apply model (7B for apply, 35B for chat)
- Tried `qwen2.5-coder:32b` as the agentic model instead (better tool calling history than qwen3.5)
- Added system message tools mode as fallback

Conclusion: Continue's local tool calling via Ollama was unreliable for agentic workflows at this time. Recommendation was to keep Continue for autocomplete and use cloud models or a different extension for agentic tasks.

---

## Phase 2: MLX context window problem and Modelfile introduction (2026-05-06)

### Problem: slowdown after first call

Using `qwen3.6:27b-coding-mxfp8` (MLX-quantized) via Opilot: first call fast, subsequent calls progressively slower. Root cause: MLX models don't pre-allocate KV cache — it grows dynamically and competes with model weights for unified memory bandwidth once a 256K context is reserved.

**Key insight:** Ollama's UI context window setting doesn't rewrite the model card metadata. VS Code Copilot reads the declared context from model metadata, not the runtime override. So setting 128K in the Ollama UI showed 256K in VS Code — Copilot kept sending full-sized requests.

**Fix: Modelfile approach.** Only a Modelfile bakes `num_ctx` into the model definition so Ollama advertises it correctly to clients.

### Why GGUF over MLX

MLX models do not respect Modelfile `num_ctx` parameters — the context window is fixed at model conversion time. GGUF models respect Modelfile parameters correctly. This was the decisive factor in switching from the MLX-quantized qwen3.6 to a GGUF-quantized model.

### First tuned Modelfile (qwen3.6 era)

```
FROM qwen3.6:27b-coding-mxfp8

TEMPLATE {{ .Prompt }}
RENDERER qwen3.5
PARSER qwen3.5
PARAMETER top_k 20
PARAMETER top_p 0.95
PARAMETER min_p 0
PARAMETER presence_penalty 0
PARAMETER repeat_penalty 1
PARAMETER temperature 0.6
PARAMETER num_ctx 131072
PARAMETER num_keep 4
PARAMETER num_parallel 1
```

Key parameters explained:
- `num_ctx 131072` — 128K context, down from 256K default; prevents KV cache from overwhelming memory bandwidth
- `num_keep 4` — pins first 4 tokens of context in KV cache between calls; prevents system prompt re-encoding on every turn
- `num_parallel 1` — single request at a time; prevents memory contention on a single-user setup
- `RENDERER qwen3.5` / `PARSER qwen3.5` — MLX-specific directives wiring up the correct tokenizer; must not be modified

---

## Phase 3: Model comparison — qwen3.6 vs qwen3-coder-next (2026-05-06)

### The candidates evaluated

| Model | Size | Context | Architecture | Key differentiator |
|-------|------|---------|--------------|-------------------|
| `mistral-medium-3.5` | 80GB | 256K | Dense 128B | 77.6% SWE-Bench, vision, reasoning toggle |
| `qwen3.6:35b` | 24GB | 256K | MoE 35B/3B active | Latest Qwen, thinking preservation, vision |
| `qwen3-coder-next` | 52GB | 256K | MoE 80B/3B active | Agentic training on 800K executable tasks |
| `devstral-small-2` | 15GB | 384K | Dense 24B | Smallest footprint, Apache 2.0 |

`qwen3.5` was excluded — `qwen3.6` is the direct successor released days later.
`mistral-medium-3.5` was excluded — 80GB leaves zero headroom on a 128GB machine.

### Head-to-head test

Both models were given the same task: parse a structured markdown checklist and produce a findings document.

**qwen3.6:35b:** Got item counts wrong (reported 12/20, source said 12/24). Quietly resolved an ambiguous item (15.5) rather than flagging it. Good prose quality and structure.

**qwen3-coder-next:** Got counts exactly right (12/24, 50%). Faithfully represented source state without editorializing. Slightly more mechanical prose.

**Result:**

| Dimension | qwen3.6:35b | qwen3-coder-next |
|-----------|-------------|-----------------|
| Factual accuracy | ⚠️ Failed (wrong count) | ✅ Correct |
| Faithfulness to source | ⚠️ Invented completions | ✅ Faithful |
| Structure/readability | ✅ Strong | ✅ Good |
| **Overall** | **C+** | **A-** |

**Decision:** `qwen3-coder-next` selected as primary agentic coding model. qwen3.6 retained as the better choice for generative/creative tasks (drafting, explaining architecture, writing docs from scratch).

### Why qwen3-coder-next is architecturally different

Built on Qwen3-Next-80B-A3B-Base with hybrid attention and MoE. Key training differentiator: trained on 800K *executable* tasks with environment interaction and reinforcement learning — not just static code-text pairs. This is why it outperforms on ground-truth extraction and instruction-following tasks. Non-thinking mode only (no `<think>` blocks), which is actually a feature for a coding assistant — no token budget wasted on chain-of-thought reasoning before responding.

---

## Phase 4: GGUF quantization selection and Modelfile for qwen3-coder-next (2026-05-09)

### Quantization decision

Available GGUF quants for qwen3-coder-next: Q4_K_M (52GB), Q6_K (~62GB), Q8_0 (85GB).

**Selected: Q6_K** — balance of quality preservation and memory footprint. Q4_K_M loses more weight precision; Q8_0 at 85GB leaves only 43GB headroom which creates pressure when running embeddings alongside. Q6_K at ~62GB leaves ~65GB free, enough to run `nomic-embed-text` without contention.

Model location on doppio-1:
```
/Users/mihay42/models/qwen3-coder-next-q6k/Qwen3-Coder-Next-Q6_K-merged.gguf
```

### Final Modelfile (production)

```
FROM /Users/mihay42/models/qwen3-coder-next-q6k/Qwen3-Coder-Next-Q6_K-merged.gguf

PARAMETER num_ctx 262144
PARAMETER num_keep 4
PARAMETER temperature 0.15
PARAMETER top_k 20
PARAMETER top_p 0.70
PARAMETER repeat_penalty 1.0
PARAMETER num_predict -1

SYSTEM "You are an expert coding assistant. Be concise and precise."

TEMPLATE """{{- if or .System .Tools }}<|im_start|>system
{{- if .System }}
{{ .System }}
{{- end }}
{{- if .Tools }}

# Tools

You may call one or more functions to assist with the user query.

You are provided with function signatures within <tools></tools> XML tags:
<tools>
{{- range .Tools }}
{"type": "function", "function": {{ .Function }}}
{{- end }}
</tools>

For each function call, return a json object with function name and arguments within
<tool_call></tool_call> XML tags:
<tool_call>
{"name": <function-name>, "arguments": <args-json-object>}
</tool_call>
{{- end }}
<|im_end|>
{{ end }}
{{- range .Messages }}
{{- if eq .Role "user" }}<|im_start|>user
{{ .Content }}<|im_end|>
{{ else if eq .Role "assistant" }}<|im_start|>assistant
{{ if .Content }}{{ .Content }}
{{- else if .ToolCalls }}
{{- range .ToolCalls }}<tool_call>
{"name": "{{ .Function.Name }}", "arguments": {{ .Function.Arguments }}}
</tool_call>{{ end }}
{{- end }}<|im_end|>
{{ else if eq .Role "tool" }}<|im_start|>user
<tool_response>
{{ .Content }}
</tool_response>
<|im_end|>
{{ end }}
{{- end }}<|im_start|>assistant
"""

PARAMETER stop <|im_start|>
PARAMETER stop <|im_end|>
```

### Key Modelfile decisions and rationale

**`temperature 0.15`** (down from 0.6 in original qwen3.6 modelfile)
- Lower temperature dramatically reduces tool call format errors
- The model sticks to high-probability tokens, which maps well to structured XML tool call syntax
- Coding tasks benefit from deterministic output; creativity is not the goal here

**`top_p 0.70`** (down from 0.95)
- Less sampling exploration; model stays in high-probability token space
- Combined with low temperature, significantly improves schema adherence on tool calls
- Tradeoff: slightly less varied prose in chat responses (acceptable for a coding assistant)

**`top_k 20`**
- Restricts sampling to top 20 tokens at each step
- Reinforces determinism without being as aggressive as greedy decoding

**`repeat_penalty 1.0`** (effectively disabled)
- Coding tasks legitimately repeat tokens (variable names, syntax patterns)
- A penalty > 1.0 would incorrectly penalise valid repetition in code

**`num_predict -1`**
- Removes the token generation ceiling
- Without this, the model silently truncates mid-response; no error, no warning

**`/no_think` removed from SYSTEM prompt**
- Earlier Modelfile versions included `/no_think` to suppress chain-of-thought
- Removed because the model needs to reason through tool call construction
- The non-thinking mode in qwen3-coder-next is already the default; the flag was redundant and was suspected to interfere with tool call reasoning

**TEMPLATE: `<tool_response>` wrapper**
- The critical fix for tool calling reliability
- Original Modelfile had bare tool results without a wrapper tag
- Qwen3's tool schema expects tool results wrapped in `<tool_response>...</tool_response>`
- Without this, the model cannot correctly parse tool results and enters retry loops

**`num_ctx 262144`** (full 256K restored)
- Unlike the qwen3.6 MLX model, the GGUF respects this parameter correctly
- VS Code sees 262144 in model metadata and sends accordingly
- 256K is legitimate for repository-scale tasks; context is managed at the session level

### Warmup behaviour

The 62GB GGUF exhibits a well-known cold start pattern:
- First request: slow (model loading from disk into unified memory, ~30-60 seconds)
- Requests 2-3: progressively faster (KV cache warming)
- Request 4+: steady state (~28 tok/s generation, ~23k tok/s prompt eval)

**Practical implication:** Don't start important work on the first request of a session. Warm the model with a throwaway request:
```bash
ollama run qwen3-coder-next-q6k-256k "summarize FastAPI" > /dev/null
```

Or pin the model in memory indefinitely to avoid cold starts between sessions:
```bash
curl -s http://doppio-1.lan:11434/api/generate -d '{
  "model": "qwen3-coder-next-q6k-256k:latest",
  "keep_alive": -1
}'
```

---

## Phase 5: VS Code connection leak investigation (2026-05-06 through 2026-05-09)

### The problem

VS Code on cafe-1 (connected via Remote-SSH) leaked TCP connections to the Ollama SSH tunnel. Count progression:

```
58   → initial VS Code launch
80   → after 2 minutes (idle)
136  → after 5 minutes (idle)
236  → after 10 minutes (one task)
486  → after 30 minutes
```

Each connection is a matched `node` + `ssh` process pair — VS Code's extension host Node.js HTTP client was opening connections and never closing them.

### Root cause

VS Code's core Node.js HTTP client connection pool never evicts idle connections. This is not an Opilot, Continue, or native Ollama integration bug — all three exhibit identical behaviour. The pool is shared across all extensions in VS Code's core.

The 5-minute silent failure mechanism: Node.js `undici` fetch has a hardcoded 300-second timeout. When the connection pool is saturated and Ollama is busy serving an inference request through a pool of 486 connections, the fetch times out exactly 300 seconds after submission with no user-visible error. The model may still be generating on doppio-1; the fetch silently fails on cafe-1.

### Fix applied (cafe-1)

Aggressive Linux kernel TCP keepalive settings to reclaim idle connections faster:

```bash
sudo sysctl -w net.ipv4.tcp_keepalive_time=60
sudo sysctl -w net.ipv4.tcp_keepalive_intvl=10
sudo sysctl -w net.ipv4.tcp_keepalive_probes=3
sudo sysctl -w net.ipv4.tcp_fin_timeout=15
sudo sysctl -w net.ipv4.tcp_tw_reuse=1
```

Persisted to `/etc/sysctl.conf`. Result: connection count stabilised around 162 rather than climbing to 486+.

### Opilot settings reduced

```json
{
  "opilot.localModelRefreshInterval": 300,
  "opilot.libraryRefreshInterval": 86400
}
```

Reduces background polling frequency from every 30 seconds to every 5 minutes. Less heartbeat churn = fewer connections opened.

### VS Code bug filed

Full reproduction case documented and filed against VS Code core. Key finding: this affects any VS Code Remote-SSH setup using a local Ollama model via SSH tunnel, regardless of which Ollama extension is used.

---

## Phase 6: Tool call loop — VS Code Copilot orchestration bug (2026-05-09)

### The symptom

In agent mode, the model repeatedly issues the same 4-5 tool calls, gets `isError` results back, then issues the same calls again. Never surfaces the error to the user. Never adapts. Session accumulates dozens of failed tool call pairs.

### Root cause

Found an open PR against `microsoft/vscode-copilot-chat` (#3566) specifically addressing this: Ollama models weren't being correctly identified as supporting tool calls, so the tools field was being stripped from requests. The model receives tool call results but without proper schemas cannot interpret them, and retries indefinitely.

The PR appeared merged into 1.119.0 but the symptom persisted — capability detection may now work (model appears in agent mode and issues tool calls), but the runtime schema or result handling is still broken for GGUF models with custom Modelfiles.

A comment was posted to the PR with the reproduction log showing the identical tool call pattern repeating across indices 3 → 9 → 15 → 21 → 26, with every tool result returning `isError`.

### Why Roo Code was adopted

The VS Code Copilot agent orchestration layer is the broken component — it's Microsoft's own code, not fixable by configuration. Roo Code implements its own agent loop independently of VS Code's Copilot orchestration, bypasses the broken tool call pipeline entirely, and speaks directly to Ollama's OpenAI-compatible endpoint. This was the decisive reason for switching from Opilot/Copilot agentic mode to Roo Code for all agentic tasks.

---

## Summary: what actually works and why

| Component | Choice | Reason |
|-----------|--------|--------|
| Model | qwen3-coder-next Q6_K GGUF | Beats qwen3.6 on factual accuracy; agentic training on executable tasks |
| Quantization | Q6_K (~62GB) | Quality/memory balance; leaves headroom for embeddings |
| Context window | 262144 (256K) | GGUF respects Modelfile; legitimate for repo-scale tasks |
| Temperature | 0.15 | Deterministic output; reduces tool call format errors |
| top_p | 0.70 | Stays in high-probability token space; improves schema adherence |
| Tool call template | `<tool_response>` wrapper | Required by Qwen3 schema; without it tool results are unparseable |
| `/no_think` | Removed | Model needs to reason through tool call construction |
| `num_predict` | -1 | Prevents silent truncation |
| `keep_alive` | -1 (session pin) | Prevents model eviction between requests |
| Agentic extension | Roo Code | Bypasses broken VS Code Copilot orchestration layer |
| Chat/autocomplete | Opilot | Works cleanly for non-agentic use |
| Connection leak mitigation | Linux TCP sysctl + reduced heartbeat | Keeps connection count at ~162 vs 486+ |
