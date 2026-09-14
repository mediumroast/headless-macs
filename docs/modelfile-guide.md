# Modelfile Guide — Ollama Configuration for Production Inference

## Why Modelfiles Matter

A Modelfile is the only mechanism that bakes configuration into a model's
metadata so clients see the correct values — not just the Ollama server.

**The critical distinction:**

| Method | Who sees it | Persists? |
|---|---|---|
| Ollama UI context window slider | Ollama server only | No — lost on restart |
| `OLLAMA_MAX_CONTEXT` env var | Ollama server only | Via plist |
| Modelfile `PARAMETER num_ctx` | Baked into the model's metadata | Yes — clients read it |

VS Code, Zoo Code, GitHub Copilot, and other Ollama-compatible clients read
`num_ctx` from the model's declared metadata via `/api/show`. If you set a
context size in the Ollama UI but the model card declares a different one,
clients send requests sized to the model card's value regardless — causing
the KV cache to grow and inference to slow progressively across a session.
See `docs/known-issues.md` for the specific symptom this causes.

---

## MLX Models vs GGUF Models

**GGUF models (Ollama's default format):** respect all Modelfile
parameters, including `num_ctx`. This is the format to use when `num_ctx`
needs to be controlled via a Modelfile.

**MLX-quantized models (Rapid-MLX, mlx-lm):** the context window is fixed
at model conversion time. A Modelfile's `num_ctx` has no effect on an MLX
model — to change its context window, the model must be re-converted with
a different `--max-position-embeddings` value.

Use GGUF + Ollama when Modelfile-level `num_ctx` control matters. Use MLX
when raw generation speed is the priority and the conversion-time context
window default is acceptable.

---

## Registering a Model with a Modelfile

```bash
ollama create <model-name> -f /path/to/your.modelfile
ollama list   # verify registration
```

The `FROM` line in a Modelfile must point to the actual model file's
location — `ollama create` references it in place rather than copying it.

For the full set of Modelfile parameters (`PARAMETER num_ctx`,
`temperature`, `TEMPLATE`, `SYSTEM`, etc.) and how they interact, see
[Ollama's own Modelfile reference](https://github.com/ollama/ollama/blob/main/docs/modelfile.md) —
that's the authoritative, up-to-date source rather than a copy that will
drift out of date here.

---

## Keeping a Model Warm

A large model has a cold-start penalty on its first request after loading.
To avoid it between sessions, pin the model in memory explicitly:

```bash
curl -s http://<ollama-host>:11434/api/generate \
  -d '{"model": "<model-name>", "keep_alive": -1}' \
  > /dev/null

curl -s http://<ollama-host>:11434/api/ps | jq '.models[].name'   # verify it's loaded
```

`keep_alive: -1` tells Ollama never to evict the model unless it needs the
memory for another one, or is explicitly unloaded — useful on a dedicated
inference node serving one primary model.
