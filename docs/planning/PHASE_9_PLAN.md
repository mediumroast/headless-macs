# PHASE 9 PLAN — macmon Hardware Telemetry Daemon

**Status: 9A–9E and 9G all implemented, build/vet/test clean.** Every
checklist item is checked off except Phase 9F (doppio-1 reconciliation),
which genuinely has to be run on that box, not from here — exact commands
are in that section. Also fixed in passing while writing the
`tool-comparison.md` macmon entry: that file's Exo section still described
Tailscale-based discovery, stale since Phase 7's Exo CLI fix — corrected
alongside the new content rather than left standing next to it.

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

- [x] Added to `config.json`:
  ```json
  "macmon": {
    "enabled": false,
    "port": 9090,
    "interval_ms": 1000
  }
  ```
- [x] Extended the config Go struct: new `MacmonTool` type
      (`internal/config/config.go`), `Tools.Macmon MacmonTool \`json:"macmon"\``
- [x] Unit test: `TestLoadTemplate` now asserts `Macmon.Enabled == false`,
      `Macmon.Port == 9090`, `Macmon.IntervalMs == 1000` from the shipped
      template

**Precheck additions found on review, not yet in the plan above:**

- [x] Added `macmon` to `precheck.go`'s `prereqs` list, matching
      Ollama/Rapid-MLX's existing treatment exactly
- [x] Added macmon to `precheck.go`'s port-availability check — and fixed
      the fragility noted here while touching it: `toolPorts` (a static
      var) is replaced by `effectiveToolPorts(cfg)`, which reads each
      tool's actual configured port and falls back to that tool's own
      install-time default only when unset, for Rapid-MLX/mlx-lm/Infinity/
      Exo/macmon. Ollama's port stays a fixed constant (it's embedded in a
      `Host` string field, not a separate `Port` int, so extracting it
      would need string parsing — left as a narrower, still-open gap, not
      addressed by this pass).

**Files touched:** `config.json`, `internal/config/config.go`, `internal/config/config_test.go`,
`internal/ops/precheck.go` (prereqs + `effectiveToolPorts()`)

---

### Phase 9B — `installMacmon()` and `macmonPlist()`
**Goal:** Port the confirmed doppio-1 manual install into `tools.go`.

- [x] Guard: `RunTools` dispatches to `installMacmon()` only when
      `tools.macmon.enabled` is `true`, matching every other tool's
      `if cfg.Tools.X.Enabled { ... } else { ActionSkip }` pattern exactly
- [x] Install `macmon` via Homebrew if not already present
      (`exec.LookPath("macmon")` skip-if-present check, matching Ollama's
      pattern rather than a raw `command -v` shell-out)
- [x] Create `/var/log/macmon/`, owned `_llmserver:wheel`, before writing
      the plist
- [x] Detect `--host` flag support: `macmon serve --help` output checked
      for the `--host` substring
- [x] Built `ProgramArguments`:
  - Always: `serve`, `-p <port>`, `-i <interval_ms>`
  - If `--host` supported: added, honoring `network.localhost_only`
  - If not supported: omitted entirely (not passed empty) — the daemon
    binds all interfaces, and a `[WARN]` is emitted when this collides
    with `localhost_only: true`
- [x] `macmonPlist()`: `UserName _llmserver`, `WorkingDirectory /tmp`,
      `HOME=/Library/LLMServer` + standard `PATH` in `EnvironmentVariables`,
      `RunAtLoad`/`KeepAlive` true, logs to
      `/var/log/macmon/{stdout,stderr}.log`
- [x] `chown root:wheel` + `chmod 644` on the plist; `loadDaemon()` (bootout
      then bootstrap) — matches every other tool's load pattern exactly
- [x] Post-install `checkEndpoint()` call against `http://127.0.0.1:<port>/json`
      with pattern `"cpu_power"` — made possible by Phase 7I's
      `checkEndpoint`/`checkHTTP` pattern-match fix landing first

**Files touched:** `internal/ops/tools.go`

---

### Phase 9C — Verify
**Goal:** `verify.go` gets a MACMON section.

- [x] `sectionMacmon(cfg *config.Config)`: `checkDaemon("com.llm-server.macmon")`
      when `tools.macmon.enabled`; `checkHTTP(..., "cpu_power", ...)` confirms
      it's actually macmon responding, not just any 200
- [x] `[WARN]` when the installed macmon build lacks `--host` in its
      written plist and `network.localhost_only` is `true`
- [x] `[SKIP]` the whole section when `tools.macmon.enabled` is `false`

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

- [x] Bootout `com.llm-server.macmon`, remove its plist (added to
      `sectionRemoveDaemons()`'s existing daemon list)
- [x] Remove `/var/log/macmon/`
- [x] Do not `brew uninstall macmon` (matches existing Restore behavior for
      other tools' packages)

**Files touched:** `internal/ops/restore.go`

---

### Phase 9E — Documentation
**Goal:** Operators know macmon exists, what it exposes, and its current
binding limitation.

- [x] Added a macmon entry to `docs/tool-comparison.md`, framed as a
      telemetry/observability addition, not an inference backend
- [x] Documented the `--host`-flag version gap and the `[WARN]` behavior in
      both `README.md` (new blockquote, matching the existing
      Rapid-MLX-memory/Network-defaults note style) and
      `docs/tool-comparison.md`
- [x] **Found and fixed while touching this file, not part of the original
      plan:** `docs/tool-comparison.md`'s Exo section still described
      Tailscale-based discovery and referenced a `--discovery-module` flag
      — both stale since Phase 7's Exo CLI fix (neither exists in current
      exo). Corrected with an inline note explaining what changed and why,
      rather than leaving actively-wrong documentation next to the new
      macmon entry.

**Files touched:** `docs/tool-comparison.md`, `README.md`

---

### Phase 9F — doppio-1 reconciliation
**Goal:** Replace the manually-installed proof-of-concept with the real,
config-driven daemon.

**Not done — needs to be run on doppio-1 itself, not from this session.**
Steps, unchanged from the original plan:

```bash
# 1. Remove the manual proof-of-concept from FUTURES.md Item 11 validation
sudo launchctl bootout system /Library/LaunchDaemons/com.llm-server.macmon.plist
sudo rm /Library/LaunchDaemons/com.llm-server.macmon.plist
sudo rm -rf /var/log/macmon

# 2. Enable macmon in config
#    edit ~/.headless_macs/config.json: "macmon": { "enabled": true, ... }

# 3. Reinstall via the new code path
sudo headless-macs install-tools

# 4. Confirm
curl -s http://127.0.0.1:9090/json | jq .
sudo headless-macs verify   # should show a MACMON section, [PASS] throughout
```

- [ ] Bootout/remove the manual instance
- [ ] Enable `tools.macmon` in doppio-1's config
- [ ] Re-run `install-tools`, confirm `GET /json` matches the shape
      validated manually in FUTURES.md Item 11
- [ ] Re-confirm the `0.0.0.0` binding decision still applies
      (`network.localhost_only: false`, deferred security hardening)

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

- [x] Implemented in `checkConfigKeys()` (`precheck.go`), reusing the
      `userPaths` map Phase 7H's stale-key detector already builds — no
      second JSON parse. New `newOptInFeatures map[string]string` (path →
      message) is checked after the stale-key warnings: an `[INFO]` fires
      only when the section is entirely absent (`!userPaths["tools.macmon"]`),
      not when it's present with `enabled: false` — the distinction the
      plan asked for, verified with a throwaway table-driven test (both
      directions correct, test not committed). Adding a future opt-in
      section just means one more map entry, not a new check.
- [x] Confirmed the stale-key detector itself has nothing to flag from
      this phase — macmon only adds config keys, never renames or removes
      any.

**Files touched:** `internal/ops/precheck.go`

---

## Files-touched summary

| File | Change |
|---|---|
| `config.json` | New `tools.macmon` block |
| `internal/config/config.go` | New `MacmonTool` struct, `Tools.Macmon` field |
| `internal/config/config_test.go` | Test coverage for macmon defaults |
| `internal/ops/tools.go` | New `installMacmon()` + `macmonPlist()`, dispatched from `RunTools` |
| `internal/ops/verify.go` | New `sectionMacmon()`, dispatched from `RunVerify` |
| `internal/ops/restore.go` | Bootout + plist removal + `/var/log/macmon` cleanup |
| `internal/ops/precheck.go` | macmon added to `prereqs`; `toolPorts` replaced by config-driven `effectiveToolPorts()`; discoverability nudge (`newOptInFeatures`) in `checkConfigKeys()` |
| `docs/tool-comparison.md` | macmon entry; also corrected stale Exo/Tailscale content found in passing |
| `README.md` | macmon table row, config example, `--host` version-gap blockquote |

---

## Open questions

- Is there a `macmon` version that reliably ships with `--host` support
  across Homebrew's current formula? If the flag is consistently absent,
  consider whether `installMacmon()` should pin/require a minimum version,
  or whether the `[WARN]`-and-proceed behavior above is acceptable
  long-term (it is acceptable for this phase, given security hardening is
  deferred regardless).
