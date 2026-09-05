# FUTURES.md — Planned Improvements

Items here are confirmed improvements worth making but not yet scheduled.
When an item is picked up, move it into a `PHASE_N_PLAN.md` and delete it here.

---

## Security — Unauthenticated, Unencrypted Serving Endpoints

**Deferred.** This item is intentionally not yet scheduled into a
`PHASE_N_PLAN.md` — Phases 7–10 (Ollama/serving-tool log management,
service suppression, macmon telemetry, and the related TUI/CLI work) are
being implemented first. Revisit this once that work lands.

### 1. No TLS or authentication in front of any serving-tool daemon

**Problem (quick assessment, not a full design):** Every serving daemon this
project installs — Ollama, Rapid-MLX, mlx-lm, Infinity, Exo, and the
macmon HTTP server (Phase 9) — binds plain HTTP with no authentication.
`network.localhost_only` controls *what interface* a tool binds to, but it
is not a security boundary once an operator legitimately sets it to `false`
for multi-machine inference (the documented, intended use case for a
dedicated inference node). At that point:

- Anyone who can reach the port can submit inference requests and consume
  compute, with no rate limiting or accounting.
- Ollama's HTTP API is not read-only — `/api/pull`, `/api/push`, and
  `/api/delete` allow an unauthenticated caller to download arbitrary models
  (disk/bandwidth exhaustion) or delete installed ones.
- Nothing is encrypted in transit — request/response bodies (prompts,
  completions) cross the network in cleartext, including on shared or
  untrusted LANs.
- None of Ollama, Rapid-MLX, mlx-lm, or Infinity have built-in auth or TLS
  support to turn on — this has to be solved outside the tool.

**Note on doppio-1:** the manually-installed `com.llm-server.macmon` daemon
(dogfooding for what became Phase 9) is intentionally bound to `0.0.0.0:9090`
with no auth — a deliberate, temporary development-stage choice, not the
intended production posture. When this item lands, macmon should move to
loopback-only with the rest of the serving tools, fronted by the same
gateway.

**Direction (for a future phase, not sized yet):** The common fix for
"multiple backend services, none of which speak TLS or auth" is a reverse
proxy in front of them, terminating TLS and enforcing an API key or basic
auth, rather than patching each tool individually. A lightweight option
(e.g. Caddy, which does automatic TLS and has trivial config) run as its
own `com.llm-server.proxy`-style LaunchDaemon, with the underlying tools
force-bound to `127.0.0.1` regardless of `network.localhost_only` (the proxy
becomes the only listener on a non-loopback interface). This would need:

- A new `network.require_auth` / `network.tls` section in `config.json`.
- A decision on credential storage/rotation (a static API key is simplest
  but weakest; needs to not land in shell history or world-readable config).
- Changes to `install-tools.sh`'s / the Go ops layer's interpretation of
  `localhost_only` — today it sets each tool's own bind host; it would need
  to instead always force loopback and let the proxy own the external bind.
- A corresponding `verify.go` check that the proxy, not the raw tool port,
  is what's reachable from a non-loopback interface.

**Scope:** Not yet sized — this is flagged for design, not implementation.
Per the Planning convention, this needs a `PHASE_N_PLAN.md` with its own
scope decision table before any code is written.
