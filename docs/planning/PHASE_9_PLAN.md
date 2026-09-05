# PHASE 9 PLAN — macmon Hardware Telemetry Daemon

**Update: Phase 9G added — remediation for existing installs.** Confirms
this phase's own idempotency decision (tool-install pattern, not
write-once) already avoids Phase 7H's logrotate-style gap; the remaining
open item is discoverability of an opt-in feature an existing box's config
doesn't know about yet, not a correctness fix.

**Update: Precheck/Verify review completed.** Two additions found: macmon
needs adding to Precheck's existing prereqs/port-check lists (same pattern
as every other tool), and `sectionMacmon()`'s HTTP check depends on a
`checkHTTP()` capability (status/body-content matching) that doesn't exist
yet — traced to Phase 7I, where the fix belongs since it's a shared
helper, not macmon-specific.

## Intent

Install [`macmon`](https://github.com/vladkens/macmon) as a system
LaunchDaemon (`com.llm-server.macmon`) running unprivileged as `_llmserver`,
exposing CPU/GPU/ANE power, temperature, and memory telemetry over HTTP
(`GET /json`, `GET /metrics`). This implements FUTURES.md Item 11, whose
core feasibility question — whether macmon's IOReport-based telemetry works
from a system-domain LaunchDaemon with no console session — was confirmed
on doppio-1 on 2026-09-05 (real non-zero telemetry, no panic, no permission
error). This phase turns that confirmed manual install into config-driven,
idempotent code in the Go ops layer.

**Security is explicitly out of scope for this phase** (deferred per
FUTURES.md Item 12 / project decision) — macmon will bind to whatever
interface its `enabled`/network settings say to, with no TLS or auth, same
as every other serving tool today. See "Deferred" in the scope table below.

---

## Scope

| Item | In | Out |
|---|---|---|
| `installMacmon()` / `macmonPlist()` in `internal/ops/tools.go` | ✓ | |
| Homebrew install check for `macmon` (skip-if-present) | ✓ | |
| `tools.macmon` config block (`enabled`, `port`, `interval_ms`) | ✓ | |
| Log directory `/var/log/macmon/`, owned `_llmserver:wheel` | ✓ | |
| `com.llm-server.macmon` LaunchDaemon, `UserName _llmserver` | ✓ | |
| macmon version/flag detection (does the installed build support `--host`?) | ✓ | |
| `verify.go` MACMON section (`checkDaemon` + `checkHTTP`) | ✓ | |
| `restore.go` cleanup (bootout, remove plist, remove `/var/log/macmon`) | ✓ | |
| Best-effort `network.localhost_only` honoring (flag if supported, `[WARN]` if not) | ✓ | |
| Reverse proxy / TLS / API-key gateway (FUTURES.md Item 12) | | ✗ deferred |
| Grafana/Prometheus dashboard setup | | ✗ — future observability work; this phase only needs the `/metrics` endpoint to exist and respond |
| Removing the manually-installed doppio-1 test instance | ✓ (as a manual step before/alongside rollout — see Phase 9F) | |

---

## Resolved design decisions

| Question | Decision |
|---|---|
| Run as which user? | `_llmserver`, not root — macmon needs no elevated privilege (confirmed on doppio-1); running a telemetry reader as root would be an unnecessary privilege escalation this project otherwise avoids |
| Daemon naming | `com.llm-server.macmon` (infra-style prefix, per FUTURES.md's own precedent) — even though it behaves operationally more like a config-driven serving tool (see next row) |
| Idempotency pattern | **Tool-install pattern** (guard on `tools.macmon.enabled`, plist always rewritten to reflect current config, `loadDaemon()` bootout-then-bootstrap), *not* the write-once-only infra-daemon pattern. Rationale: macmon has a config block with values (`port`, `interval_ms`) a user can change, and needs to be installable/removable via the `enabled` flag — both are properties of the tool-install pattern (`installOllama()`, etc.), not of write-once infra daemons like `caffeinate`. FUTURES.md's original wording ("write-once idempotent, not tool-config-driven") is superseded by this decision — the `com.llm-server.*` label prefix is kept, but the behavior follows `installOllama()`'s shape. |
| `HOME` env var | Included defensively in `EnvironmentVariables` (`/Library/LLMServer`) per the standard daemon template, even though it wasn't observed to be required during doppio-1 testing |
| Binding behavior | The doppio-1-installed macmon build (`brew install macmon`) has **no `--host`/`--bind` flag** — confirmed via `macmon serve --help`. `installMacmon()` must detect this at install time (parse `macmon serve --help` output, or attempt `macmon --version` against a known minimum) and: if `--host` is supported, pass it according to `network.localhost_only` exactly like every other tool; if not, leave the default bind (all interfaces) and emit a `[WARN]` during install *and* a corresponding `verify.go` warning, rather than silently exposing the port |
| Uninstall / package removal | `restore` removes the daemon, plist, and logs — it does **not** `brew uninstall macmon`, matching how Restore treats every other tool's Homebrew package |

---

## Phases

### Phase 9A — Config schema
**Goal:** `tools.macmon` exists and loads.

- [ ] Add to `config.json`:
  ```json
  "macmon": {
    "enabled": false,
    "port": 9090,
    "interval_ms": 1000
  }
  ```
- [ ] Extend the config Go struct (wherever `tools.ollama` etc. are defined,
      likely `internal/config/config.go`) with a matching `Macmon` field
- [ ] Unit test: loading `config.json` yields the expected default macmon
      struct values

**Precheck additions found on review, not yet in the plan above:**

- [ ] Add `macmon` to `precheck.go`'s `prereqs` list (info-only, matching
      Ollama/Rapid-MLX's existing treatment: `commandExists` check,
      `"install-tools.sh will install"` fix hint). The existing list checks
      unconditionally regardless of each tool's `enabled` flag — Rapid-MLX
      is checked even though it defaults to `enabled: false` — so adding
      macmon unconditionally is consistent with that precedent, not new
      noise.
- [ ] Add macmon's port to `precheck.go`'s `toolPorts` list (`checkNetwork()`)
      so port-availability checking covers it like every other tool.
      **Note found while doing this:** `toolPorts` hardcodes each tool's
      *default* port rather than reading the operator's actual configured
      port from `cfg` — currently harmless only because the shipped
      `config.json` template's ports happen to match the hardcoded list.
      An operator who customizes `mlx_lm.port` (for example) would get a
      port-availability check against the wrong port, silently. Not
      introduced by this phase, but adding a sixth hardcoded entry
      compounds an existing fragility — worth fixing to read from `cfg`
      while touching this list, though not a hard requirement for Phase 9
      itself.

**Files touched:** `config.json`, `internal/config/config.go`, `internal/config/config_test.go`,
`internal/ops/precheck.go` (prereqs + port list; port-list config-driven fix optional)

---

### Phase 9B — `installMacmon()` and `macmonPlist()`
**Goal:** Port the confirmed doppio-1 manual install into `tools.go`.

- [ ] Guard: only proceed if `tools.macmon.enabled` is `true` (matches the
      tool-install pattern's step 1)
- [ ] Install `macmon` via Homebrew if not already present
      (`command -v macmon` skip-if-present check)
- [ ] Create `/var/log/macmon/`, owned `_llmserver:wheel`, before writing
      the plist
- [ ] Detect `--host` flag support (`macmon serve --help` output parsing);
      record the result for use in `macmonPlist()` and for the `[WARN]`
      surfaced in Phase 9E
- [ ] Build `ProgramArguments`:
  - Always: `serve`, `-p <port>`, `-i <interval_ms>`
  - If `--host` supported: add `--host 127.0.0.1` (or the configured host)
    honoring `network.localhost_only` exactly as other tools do
  - If not supported: omit; the daemon binds all interfaces
- [ ] `macmonPlist()`: `UserName _llmserver`, `WorkingDirectory /tmp`,
      `HOME=/Library/LLMServer` + standard `PATH` in `EnvironmentVariables`,
      `RunAtLoad`/`KeepAlive` true, logs to
      `/var/log/macmon/{stdout,stderr}.log`
- [ ] `chown root:wheel` + `chmod 644` on the plist; `loadDaemon()` (bootout
      then bootstrap) — matches every other tool's load pattern exactly
- [ ] Post-install `checkEndpoint()` call against `http://127.0.0.1:<port>/json`
      (non-fatal `|| true`, matching the existing pattern), confirming a
      `cpu_power` key in the response

**Files touched:** `internal/ops/tools.go`

---

### Phase 9C — Verify
**Goal:** `verify.go` gets a MACMON section.

- [ ] `sectionMacmon(cfg *config.Config)`: `checkDaemon("com.llm-server.macmon")`
      when `tools.macmon.enabled`; confirm `GET http://127.0.0.1:<port>/json`
      actually returns macmon's data, not just any 200
- [ ] `[WARN]` if the installed macmon build lacks `--host` support and
      `network.localhost_only` is `true` — i.e., the operator asked for
      loopback-only but this build can't honor it
- [ ] `[SKIP]` the whole section when `tools.macmon.enabled` is `false`

**Dependency resolved:** `checkHTTP()` previously only checked that an HTTP
round-trip succeeded — no status-code check, no body-content check at all,
which would have let a plain "port responds" check on `/json` pass even if
something *other* than macmon happened to be listening on 9090. Phase 7I
has since fixed this — `checkHTTP(section, name, url, pattern string,
timeoutSecs int)` now takes a real pattern argument, restoring the bash
`check_http "name" "url" "pattern"` contract FUTURES.md's original Item 11
scope assumed. `sectionMacmon()` should call it as
`r.checkHTTP(sec, "macmon", url, "cpu_power", timeout)` when this phase is
implemented — no further Verify-helper work needed first.

**Files touched:** `internal/ops/verify.go`

---

### Phase 9D — Restore
**Goal:** `restore` cleans up macmon exactly like it does every other tool.

- [ ] Bootout `com.llm-server.macmon`, remove its plist
- [ ] Remove `/var/log/macmon/`
- [ ] Do not `brew uninstall macmon` (matches existing Restore behavior for
      other tools' packages)

**Files touched:** `internal/ops/restore.go`

---

### Phase 9E — Documentation
**Goal:** Operators know macmon exists, what it exposes, and its current
binding limitation.

- [ ] Add a macmon entry to `docs/tool-comparison.md` (it's not a serving
      tool, so note it's a telemetry/observability addition, not an
      inference backend)
- [ ] Document the `--host`-flag version gap and the resulting `[WARN]`
      behavior in `README.md` or `docs/known-issues.md`, so an operator who
      sees the warning understands why and what upgrading `macmon` would fix

**Files touched:** `docs/tool-comparison.md`, `README.md` or `docs/known-issues.md`

---

### Phase 9F — doppio-1 reconciliation
**Goal:** Replace the manually-installed proof-of-concept with the real,
config-driven daemon.

- [ ] On doppio-1: bootout and remove the manually-installed
      `com.llm-server.macmon` plist and `/var/log/macmon/` created during
      FUTURES.md Item 11 validation
- [ ] Set `tools.macmon.enabled: true` in doppio-1's
      `~/.headless_macs/config.json`
- [ ] Run `sudo headless-macs install-tools` and confirm the daemon comes
      back up identically (same `GET /json` response shape) via the new
      code path
- [ ] Re-confirm the `0.0.0.0` binding decision from the FUTURES.md
      discussion still applies at the config level (`network.localhost_only: false`
      for now, per the existing project decision to defer security
      hardening)

**Files touched:** none (operational step on doppio-1, not a code change)

---

### Phase 9G — Remediation for existing installs
**Goal:** confirm macmon's rollout has no stale-artifact risk, and address
the one real gap this phase introduces — not a correctness bug like
Phase 7's, but a discoverability one.

**No stale-plist risk, by design.** Phase 9B already commits to the
tool-install (always-rewrite) pattern for `com.llm-server.macmon` rather
than the write-once infra pattern — see the "Idempotency pattern" row in
Resolved design decisions above, which made this call specifically to
avoid the class of bug Phase 7H found in `installLogRotate()`. Re-running
`install-tools` after enabling macmon, or after changing its `port`/
`interval_ms`, picks up the new values automatically every time, matching
Ollama/Rapid-MLX/mlx-lm/Infinity/Exo. Nothing further needed here.

**The real gap is discoverability, not correctness.** An existing box's
`config.json` simply won't have a `tools.macmon` block at all — Go's
zero-value default (`enabled: false`) means macmon silently stays off
forever, which is *correct* (opt-in features should default off), but
means the operator has no way to learn the capability exists short of
reading `CHANGELOG.md` or this repo's docs directly. "Stale config" here
manifests as "config technically fine, but missing a feature the operator
doesn't know to ask for" rather than "something is broken."

- [ ] Recommend: Precheck or the new Status screen (Phase 10) prints a
      one-time `[INFO]` (not a warning — nothing is wrong) when
      `tools.macmon` is entirely absent from the loaded config, e.g.
      `"macmon hardware telemetry available (v2.4.0+) — not configured; see
      README"`. Distinguish "key absent from file" from "key present with
      enabled: false" if practical (the latter means the operator already
      made an explicit choice and doesn't need a nudge) — this needs the
      raw-JSON-tree comparison already proposed in Phase 7H's stale-key
      detector, so consider building both on the same mechanism rather
      than two separate ad hoc checks.
- [ ] The Phase 7H stale-key detector itself has nothing to flag from this
      phase — macmon only *adds* config keys, it never renames or removes
      any. Confirmed, no action needed.

**Files touched (when implemented):** `internal/ops/precheck.go` (or
`internal/ops/status.go` once Phase 10 exists) — shares the raw-JSON-tree
mechanism from Phase 7H rather than introducing a second one.

---

## Files-touched summary

| File | Change |
|---|---|
| `config.json` | New `tools.macmon` block |
| `internal/config/config.go` | New `Macmon` config struct field |
| `internal/config/config_test.go` | Test coverage for macmon defaults |
| `internal/ops/tools.go` | New `installMacmon()` + `macmonPlist()` |
| `internal/ops/verify.go` | New `sectionMacmon()` |
| `internal/ops/restore.go` | Bootout + cleanup for `com.llm-server.macmon` |
| `docs/tool-comparison.md` | macmon entry |
| `README.md` / `docs/known-issues.md` | `--host` flag version-gap note |
| `internal/ops/precheck.go` | *(Phase 9G, not yet implemented)* "macmon available, not configured" discoverability nudge; macmon added to `prereqs`/`toolPorts` |

---

## Open questions

- Is there a `macmon` version that reliably ships with `--host` support
  across Homebrew's current formula? If the flag is consistently absent,
  consider whether `installMacmon()` should pin/require a minimum version,
  or whether the `[WARN]`-and-proceed behavior above is acceptable
  long-term (it is acceptable for this phase, given security hardening is
  deferred regardless).
