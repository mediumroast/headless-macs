# PHASE 12 PLAN — v2.3.1 fixes: fabricated Ollama env vars, debug-bundle content, cleanup

**Status: planning only. Nothing implemented. Stopping here per instruction.**

Branch: `claude/v2.3.1-fixes`, created off `claude/debug-bundle-self-inclusion-fix`
(PR [#19](https://github.com/mediumroast/headless-macs/pull/19), not yet merged) —
this phase's bundle-content work directly builds on the same file (
`cmd/headless-macs-debug/main.go`) PR #19 already touches, so branching from
its tip avoids two divergent edits to the same functions that would need
reconciling later. If you'd rather these land as fully separate PRs, say so
and I'll rebase this branch onto `main` once #19 merges instead.

Target version: **v2.3.1** (Patch — bug fixes only; no new config keys, no
schema additions. Phase 12B changes what an *existing* config key means,
which is a behavior fix, not a new capability).

---

## Scope decision table

| Item | In scope | Out of scope |
|---|---|---|
| Remove `OLLAMA_GPU_PERCENT` (fabricated, confirmed absent from Ollama's own source) | ✅ | — |
| Fix Ollama's real verbosity control (`OLLAMA_LOG_LEVEL` also confirmed fabricated) | ✅ | Auditing every other tool's verbosity settings (rapid-mlx/mlx-lm/Infinity already confirmed real and correct via their own `--log-level` CLI flags per an existing code comment citing real docs — not re-checking those) |
| `headless-macs-debug logs`: drop `/var/log/mac-llm-setup` from bundle contents | ✅ | Changing what `headless-macs baseline`/`install-tools` write to that directory — only the *bundle's* contents, not the directory's real purpose |
| `headless-macs-debug logs`: cap to the N most recent log files per tool, not the whole rotation history | ✅ | Changing the `logrotate` retention policy itself (`100M x5`) — that's unrelated; this is only about what gets *bundled* for a quick pull, not what's kept on disk |
| `headless-macs-debug clean` — new subcommand to delete bundles | ✅ | Auto-cleanup / retention policy for bundles (e.g. "keep last N automatically") — starting with an explicit, manually-invoked delete-everything command; can add options later if wanted |
| Live testing on doppio-1 | ✅ (as always, your responsibility per established pattern this session) | — |

---

## Phase 12A — Remove `OLLAMA_GPU_PERCENT`

**Finding, verified against Ollama's actual current source** (`envconfig/config.go`
on `ollama/ollama` `main`, fetched and searched directly, not from memory or
secondary docs): there is no GPU-percentage environment variable anywhere in
Ollama's `AsMap()` — the complete list of every env var it recognizes. GPU-related
knobs that *do* exist: `OLLAMA_GPU_OVERHEAD` (reserve a fixed **byte** count of
VRAM per GPU — a completely different mechanism, not a percentage),
`OLLAMA_NUM_GPU` (already correctly hardcoded to `"1"` in the plist — a real,
separate setting), `OLLAMA_IGPU_ENABLE`, `OLLAMA_SCHED_SPREAD`. Ollama silently
ignores any environment variable it doesn't recognize, so `gpu_percent` in
`config.json` has been doing **nothing** — no error, no effect — since it was
added.

**Resolved: remove outright.** No `OLLAMA_GPU_OVERHEAD` replacement.

**Files touched:**
- `internal/ops/tools.go` — drop `gpuPct` parameter from `ollamaPlist()`, drop
  the `<key>OLLAMA_GPU_PERCENT</key>` line, drop the `gpuPct := ...` construction
  block in `installOllama()`
- `internal/config/config.go` — remove `GPUPercent int` from `OllamaTool`
- `config.json` — remove `"gpu_percent": 80`
- `docs/planning/PHASE_12_PLAN.md` (this file) — record the decision made

---

## Phase 12B — Fix Ollama's real logging-verbosity control

**Finding, same source verification:** `OLLAMA_LOG_LEVEL` is *also* not a real
Ollama environment variable — confirmed absent from `AsMap()`. The real
mechanism is `OLLAMA_DEBUG`, which `envconfig.LogLevel()` reads directly:
unset or `"0"`/`"false"` → INFO (default), `"1"`/`true` → DEBUG, any other
non-zero integer `N` → a slog level of `N * -4` (e.g. `"2"` → more verbose
than DEBUG, roughly TRACE). This is a **numeric verbosity toggle**, not a
named-level enum (`"warn"`/`"info"`/`"debug"` like the config currently
stores) — so, like 12A, this isn't a pure rename.

`internal/ops/verify.go` currently only checks that the plist *contains* the
`OLLAMA_LOG_LEVEL` key at all (a syntax-presence check, not a functional
one), so Verify has been reporting `[PASS] OLLAMA_LOG_LEVEL configured` this
whole time despite the setting doing nothing.

**Decision needed:** how should `tools.ollama.log_level` (currently a string,
default `"warn"`) map onto `OLLAMA_DEBUG`? Proposed default, happy to change:

| Config `log_level` | `OLLAMA_DEBUG` written |
|---|---|
| `"warn"` / `"info"` / unset | *(omit the key entirely — Ollama's own default)* |
| `"debug"` | `1` |
| `"trace"` | `2` |

This keeps the existing config key and its friendly string values working
exactly as before from the user's point of view — only the internal
translation to what actually gets written into the plist changes.

**Files touched:**
- `internal/ops/tools.go` — replace the `OLLAMA_LOG_LEVEL` plist key with
  `OLLAMA_DEBUG`, add the string→numeric mapping above in `installOllama()`
- `internal/ops/verify.go` — change the presence check to check for
  `OLLAMA_DEBUG` instead (still a syntax check, matching the same shallow
  depth as this check has always had for other tools — not scope-creeping
  into a live-behavior check here)

---

## Phase 12C — `headless-macs-debug logs`: stop over-capturing

Two independent fixes to `bundleDirs()`/`runLogs()` in
`cmd/headless-macs-debug/main.go`:

**1. Drop `/var/log/mac-llm-setup` (`opsLogDir`) from what gets bundled.**
Currently `runLogs()` always appends `opsLogDir` to the source list regardless
of which tool was requested — it's headless-macs's own operational logs
(setup/verify/baseline run logs, the logrotate status file, and — after PR
#19 lands — the lock file), not a serving tool's logs, and isn't what anyone
asking for `logs ollama` actually wants. Simple removal: drop the
`dirs = append(dirs, opsLogDir)` line entirely.

**2. Cap each tool's directory to its N most recent files, not everything.**
Each tool's log directory currently holds the live file(s) (`stdout.log`,
`stderr.log`) plus up to 5 rotated backups each (`stdout.log.1.gz` ...
`stdout.log.5.gz`, `logrotate`'s configured `100M x5` retention) — a bundle
today includes all of that, which is far more than needed for a quick "what's
going on right now" pull.

**Resolved:** per stream, not per directory — the live file (`stdout.log`/
`stderr.log`) plus its 2 most recent rotations (`.1.gz`, `.2.gz`), so 3 files
per stream / 6 per tool. The rotation count (`2` above) must be
**configurable**, not hardcoded.

**Sub-decision needed — how should it be configurable?**
`headless-macs-debug` is deliberately standalone (see the file's own header
comment and `toolLogDirs`'s comment — it keeps small local copies of things
rather than importing `internal/config`/`internal/ops`, so it stays a single
self-contained binary). Two ways to honor "configurable" without breaking
that:

- **(a) Read `config.json` directly, but minimally** — `headless-macs-debug`
  parses just the one field it needs (e.g. a new `debug.log_bundle_rotations`
  int, default `2`) out of `~/.headless_macs/config.json`, without importing
  `internal/config`'s full struct or the `internal/ops` package. Since this
  always runs as root (already required for rotation), it needs the same
  "whose home directory" logic `internal/ops/baseline.go`'s `sudoUID()`
  already has (find the invoking user via `$SUDO_UID`, not root's own home)
  — a small, local duplicate of that one helper, matching how `toolLogDirs`
  is already a local duplicate of `internal/ops/tools.go`'s paths. Fits the
  project's "config-driven, no hardcoded values" principle most closely, and
  means the TUI's Edit Config screen could expose it like any other setting.
- **(b) A `--keep=N` flag on `logs` itself** (default `2`) — simpler,
  no config-file parsing or sudo-user resolution needed, but the setting has
  to be remembered and typed on every invocation rather than being a
  standing preference, and can't be exposed in the Edit Config TUI screen.

Defaulting to **(a)** to match the project's own stated config-driven
philosophy, but this is the one with the most implementation weight of the
three open questions, so flagging it clearly rather than assuming.

**Files touched:**
- `cmd/headless-macs-debug/main.go` — `runLogs()` (drop `opsLogDir` append),
  `bundleDirs()` (per-stream recency capping, replacing the current
  walk-everything logic for tool directories specifically — `opsLogDir`'s
  removal above means this only ever applies to tool dirs now anyway), new
  minimal config read + sudo-user home resolution if option (a) is chosen
- `internal/config/config.go` / `config.json` — new `debug.log_bundle_rotations`
  int field (default `2`) if option (a) is chosen
- `internal/tui/config_editor.go` — expose the new field in the DEBUG section
  if option (a) is chosen (matching how `sudo_nopasswd_enabled` is already
  exposed there)

---

## Phase 12D — `headless-macs-debug clean`

New subcommand: `sudo headless-macs-debug clean` — deletes every file in
`/var/log/mac-llm-setup/bundles/` (matching the existing `debug-*.tar.gz`
naming, so it can't accidentally delete something unrelated if that
directory is ever used for anything else later). Prints what was removed and
how much space was freed, matching this binary's existing plain-stdout
convention (`logs` just prints the resulting path; `clean` would print each
removed file and a total).

No confirmation prompt — matches this binary's existing design (a small,
scriptable admin tool, not an interactive one; `logs` doesn't ask for
confirmation either despite forcing a real log rotation). Runs under the
same root requirement and lock (`acquireLock()`, from PR #19) as `logs`, so
it can't race a bundle that's mid-write.

Not in scope for this pass: age-based or count-based automatic retention
("keep last N", "delete anything older than X days"). This is a blunt,
manual "empty it out" command to start; happy to add retention options in a
follow-up if the manual version proves annoying to use.

**Files touched:**
- `cmd/headless-macs-debug/main.go` — new `runClean()` function, new `clean`
  case in `main()`'s command switch, updated `usage` string
- `README.md` — document the new subcommand under Debugging Tools

---

## Files-touched summary (all phases)

- `internal/ops/tools.go` — 12A, 12B
- `internal/config/config.go` — 12A, 12C (if option a)
- `config.json` — 12A, 12C (if option a)
- `internal/ops/verify.go` — 12B
- `internal/tui/config_editor.go` — 12C (if option a)
- `cmd/headless-macs-debug/main.go` — 12C, 12D
- `README.md` — 12D
- `CHANGELOG.md` — `[Unreleased]` entries for all four, per the end-of-session
  convention, moved to a dated `v2.3.1` section once implemented and merged

## Open questions before implementing (see each phase above for detail)

1. **12A:** ~~remove outright, or replace?~~ **resolved** — remove outright.
2. **12B:** confirm (or adjust) the proposed `log_level` string →
   `OLLAMA_DEBUG` numeric mapping table.
3. **12C:** ~~confirm "2 most recent" semantics~~ **resolved** — per-stream,
   live file + 2 most recent rotations, rotation count configurable. Still
   open: config-file field (option a, default) vs. a `--keep=N` flag
   (option b) for making that count configurable.
