# PHASE 10 PLAN — TUI and CLI Changes for New Capabilities

**Update: Phase 10H added — remediation for existing installs (upgrade
awareness).** This is where the shared "tell the operator something needs
re-running" mechanism actually gets built — Phases 7H, 8F, and 9G each
identified a piece of this problem and pointed here for the implementation,
since this phase is already building the Status screen that's the natural
home for it.

## Intent

Phases 7–9 add new config surface (per-tool log verbosity, macmon), new
baseline behavior (five more suppressed services), and a new always-on
daemon (macmon) that, for the first time, gives this project a live source
of hardware telemetry. None of that is visible anywhere in the TUI or CLI
yet. This phase closes that gap: it exposes the new config and checks in
the existing screens, and adds a new **Service Status** screen/subcommand
that answers a question operators currently have no way to answer short of
SSHing in and running `launchctl print` by hand — "what's actually running
right now, and what is it costing me?"

This plan should be read as depending on Phases 7–9 landing first (it
surfaces their config keys and daemon labels), but is written to be
independently executable: if Phases 7–9 are re-scoped before this one is
picked up, only the specific field/section lists in Phase 10D/10E need
adjusting, not the overall approach.

---

## Scope

| Item | In | Out |
|---|---|---|
| `internal/ops/status.go` — new module reporting daemon running-state, PID, uptime | ✓ | |
| macmon-backed hardware panel (CPU/GPU power, temp, memory) when `tools.macmon.enabled` | ✓ | |
| Per-daemon resource use (RSS, CPU%) via `ps`, independent of macmon | ✓ | |
| New `headless-macs status` CLI subcommand (non-interactive) | ✓ | |
| New "Service Status" TUI screen, added to the main menu | ✓ | |
| Config editor: toggles/fields for `tools.macmon` and the new `tools.*.log_level` keys from Phase 7 | ✓ | |
| Verify screen: render new MACMON and service-suppression sections (Phases 8/9) without layout rework | ✓ | |
| Menu/CLI shape reassessment (is 8+ menu items and 7 subcommands still the right shape?) — analysis and, if warranted, restructuring | ✓ | |
| Version-stamp marker + upgrade-awareness nudge (Phase 10H) | ✓ | |
| Shared stale/unknown-config-key detector, reused by Phase 7H and 9G | ✓ | |
| Full Grafana/Prometheus dashboard UI | | ✗ — macmon's `/metrics` endpoint is the integration point; building a dashboard is separate, future work |
| Any UI for the deferred TLS/auth gateway (Item 12) | | ✗ — no gateway exists yet to have a UI for |
| Historical/trend graphs (sparklines, charts) in the TUI | | ✗ — Service Status is a point-in-time snapshot; charting is a future enhancement if the snapshot proves insufficient |

---

## Resolved design decisions

| Question | Decision |
|---|---|
| Where does daemon state come from? | `launchctl print system/<label>` per daemon, parsed for `state = running` and PID — same mechanism `verify.go`'s `checkDaemon()` already uses, factored into a shared helper rather than duplicated |
| Where does resource use come from? | Two independent sources, both optional and independently gradeable: (1) `ps -o pid,rss,%cpu -p <pid>` for each running daemon's PID (works regardless of macmon), (2) macmon's `GET /json` for system-wide power/thermal figures, only when `tools.macmon.enabled` |
| Degradation when macmon is disabled | Service Status still shows daemon up/down + per-daemon RSS/CPU% from `ps`; the hardware panel (power/temp) is omitted with a one-line note: "Enable macmon in config to see hardware telemetry" — never an error |
| CLI shape | Follow the existing subcommand style exactly (`precheck`, `baseline`, `install-tools`, `verify`, `restore`, `update-tools`, `storage` — plain subcommands, not `--run <phase>` flags, which is what Phase 6's original plan proposed but the shipped code did not adopt). Add `status` as one more subcommand of the same shape. |
| Menu shape | Append "Service Status" as a new entry rather than restructuring the existing flat list — see Phase 10F for the explicit reassessment and why a bigger restructuring is not justified yet |

---

## Phases

### Phase 10A — `internal/ops/status.go`
**Goal:** A single ops module answering "what's running and what is it
using," reusable by both the CLI and the TUI.

- [ ] `StatusResult` struct: one entry per managed daemon
      (`Label`, `Running bool`, `PID int`, `RSSBytes int64`, `CPUPercent float64`)
- [ ] `RunStatus(cfg *config.Config) (*StatusResult, error)`: enumerates the
      daemon labels relevant to the current config (only tools with
      `enabled: true`, plus the always-relevant infra daemons —
      `com.llm-server.caffeinate`, `.sysctl-tuning`, `.maxfiles`,
      `.pmset-heal`, `.logrotate` from Phase 7, and `.macmon` from Phase 9
      when enabled)
- [ ] Extract a shared `daemonState(label string) (running bool, pid int)`
      helper from `verify.go`'s existing `checkDaemon()` logic so both
      modules parse `launchctl print` the same way instead of duplicating
      the parsing
- [ ] For each running PID, shell out to `ps -o rss=,pcpu= -p <pid>` and
      parse the result
- [ ] When `tools.macmon.enabled`, additionally fetch
      `http://127.0.0.1:<port>/json` and include the parsed hardware
      snapshot (`cpu_power`, `gpu_power`, `sys_power`, `cpu_temp_avg`,
      `gpu_temp_avg`, `ram_usage`/`ram_total`) in `StatusResult`; on fetch
      failure, leave the hardware fields empty rather than failing the
      whole status call — this reads and parses macmon's JSON directly
      rather than going through `verify.go`'s `checkHTTP()`, so it isn't
      blocked on that helper's pattern-matching gap (Phase 7I/9C); worth
      confirming at implementation time that `sectionMacmon()` and
      `status.go` aren't duplicating the same fetch-and-parse logic once
      both exist

**Files touched:** `internal/ops/status.go` (new)

---

### Phase 10B — `headless-macs status` CLI subcommand
**Goal:** Non-interactive status output, same tone as `verify`.

- [ ] Add `"status"` to the subcommand switch in `main.go`, alongside
      `precheck`/`baseline`/etc.
- [ ] Plain-text table output: one line per daemon
      (`[UP]`/`[DOWN]` prefix, matching this project's bracket-prefix
      convention, then label, PID, RSS, CPU%)
- [ ] When macmon data is present, print a short hardware summary block
      after the daemon table (power/temp/memory) — otherwise print nothing
      extra (no "macmon not enabled" spam in the common case where the
      operator hasn't turned it on)
- [ ] Exit code `0` always (status is informational, not a health gate —
      `verify` already owns pass/fail semantics)

**Files touched:** `cmd/headless-macs/main.go`

---

### Phase 10C — "Service Status" TUI screen
**Goal:** The same information, live, in the TUI.

- [ ] New `internal/tui/status.go`: a Bubble Tea model rendering the daemon
      table plus the optional hardware panel
- [ ] Periodic refresh via `tea.Tick` (every 2–3 seconds) — this is the one
      TUI screen in the app that benefits from being "live" rather than a
      static one-shot report like the existing Verify screen
- [ ] Reuses `internal/ops/status.RunStatus()` — no separate data path from
      the CLI subcommand
- [ ] `q`/`Esc` returns to the main menu, matching existing screen
      conventions
- [ ] Dark-only color scheme per the project's established palette:
      running = green, down = red, hardware figures in the existing
      neutral/white value styling

**Files touched:** `internal/tui/status.go` (new)

---

### Phase 10D — Main menu entry
**Goal:** Make the new screen reachable.

- [ ] Add `{Label: "Service Status", Key: "s", Ready: true}` to `menuItems`
      in `internal/tui/menu.go`
- [ ] Wire the menu selection to push the Phase 10C screen, matching how
      existing entries (Precheck, Verify, etc.) are wired in `app.go`

**Files touched:** `internal/tui/menu.go`, `internal/tui/app.go`

---

### Phase 10E — Config editor and Verify screen updates
**Goal:** Phases 7–9's new config and checks are actually visible, not just
present in `config.json`/`verify.go` for someone to find by reading code.

- [ ] Config editor (`internal/tui/config_editor.go`): add a `macmon`
      toggle + `port`/`interval_ms` fields to the Tools section; add
      `log_level` fields for `ollama` (and any other tool that gained one
      in Phase 7C) so operators can tune verbosity without hand-editing
      `config.json`
- [ ] Verify screen: confirm the existing rendering is generic over
      sections (i.e., it already iterates `VerifyResult`'s sections rather
      than hardcoding a fixed list) — if it hardcodes section names, extend
      it to include MACMON and the five new suppressed-service checks; if
      it's already generic, this is a no-op beyond a manual check that the
      screen doesn't overflow its pane on a full-height terminal now that
      there are more sections

**Files touched:** `internal/tui/config_editor.go`, and possibly `internal/tui/run_screen.go` (wherever Verify results render) if it is not already generic

---

### Phase 10F — Menu/CLI shape reassessment
**Goal:** Answer, explicitly, whether the current flat structures still fit
— and record the answer here rather than leaving it implicit.

- [ ] **Main menu:** with Service Status added, the menu is 8 entries
      (Precheck, Storage Setup, System Baseline, Install Tools, Verify,
      Restore, Update Tools, Service Status). Evaluate whether this still
      reads cleanly as a flat list or whether grouping (e.g., a
      "Provision" group vs. an "Operate" group) is warranted. **Working
      assessment: 8 items is still within the range where a flat list is
      easy to scan; do not introduce grouping in this phase unless a 9th
      or 10th item is added by later work.** Record the actual decision
      made here once this phase is picked up, including if that
      assessment changes.
- [ ] **CLI subcommands:** with `status` added, there are 8 subcommands.
      Evaluate whether any need splitting (e.g., a `status --watch` flag
      for a refreshing terminal view, mirroring the TUI screen, versus a
      separate subcommand). **Working assessment: a `--watch` flag on
      `status` is worth adding in this phase** (`headless-macs status --watch`
      refreshes in place using the same interval as the TUI screen) since
      it's a small addition to Phase 10B and avoids inventing a ninth
      subcommand for a variant of an eighth.
- [ ] Document the final decision (not just the working assessment above)
      in this section before Phase 10 is marked complete, so future phases
      inherit a recorded rationale instead of re-litigating menu shape.

**Files touched:** none beyond what 10A–10E already touch, unless the
reassessment concludes restructuring is needed (in which case, record the
follow-up scope here rather than expanding this phase's file list
retroactively)

---

### Phase 10G — Documentation
**Goal:** Operators can discover the new screen/subcommand from the docs.

- [ ] Update `README.md`'s TUI/CLI usage section: document `Service Status`
      in the menu list and `headless-macs status [--watch]` in the
      subcommand list
- [ ] Update `CLAUDE.md`'s architecture section if the `internal/ops/`
      module table needs a `status.go` row (it currently lists one row per
      ops file; add one)

**Files touched:** `README.md`, `CLAUDE.md`

---

### Phase 10H — Remediation for existing installs: upgrade awareness
**Goal:** close the last gap Phases 7–9's remediation sections all lean
on — an operator has no signal that a re-run of `install-tools`/`baseline`
is even warranted after upgrading the binary. Phase 7H made tool plists
self-healing and flagged the logrotate gap; Phase 8F confirmed
`baseline` re-runs are safe/sufficient; Phase 9G deferred macmon's
discoverability nudge to "whichever of Precheck or Status lands the
shared mechanism first." This phase is where that mechanism actually
lands, since it's building the Status screen anyway.

**Problem:** none of Phases 7–9's fixes reach a running box until the
operator manually re-runs the relevant command — and today there is no
signal telling them a re-run is warranted. Someone who only upgrades the
`headless-macs` binary itself (`git pull && make build && sudo make
install`) and doesn't separately think to re-run Baseline/Install Tools
gets none of the intervening fixes, with no indication anything is out of
date. This is worse than a normal "check the changelog" problem because
this project's own model (idempotent, re-run-anytime operations) implies
re-running should be unnecessary busywork — nothing currently tells an
operator when that assumption stops holding.

- [ ] **Version-stamp marker file.** `RunBaseline` and `RunTools` each
      write a small marker (e.g. `/var/log/mac-llm-setup/.last-configured-version`)
      containing the running binary's version string + timestamp on
      successful completion.
- [ ] On TUI launch and on every CLI invocation, compare the current
      binary's version constant against that marker. If they differ (newer
      binary than the marker — including "marker absent," i.e. an existing
      pre-Phase-10 install), print a low-key, non-blocking nudge: TUI — a
      one-line banner on the main menu; CLI — a stderr line before normal
      output. Name the specific commands to re-run
      (`sudo headless-macs baseline` / `install-tools`); do **not** try to
      enumerate what changed release-by-release inline — that's
      `CHANGELOG.md`'s job, just point at re-running.
- [ ] **Share the raw-JSON-tree mechanism from Phase 7H's stale-key
      detector and Phase 9G's macmon-discoverability nudge** rather than
      building three separate ad hoc checks across three phases. This
      phase is the natural place for that shared implementation to live,
      since the Status screen is already the "operator's one place to look"
      surface this plan is built around — Precheck can call the same
      function rather than duplicating it.
- [ ] Surface both the version-mismatch nudge and any stale/unknown config
      keys in the Service Status screen itself (Phase 10C), not only in
      Precheck — an operator troubleshooting via Status shouldn't have to
      separately think to run Precheck to learn their config or install is
      out of sync.
- [ ] **Consider, don't commit to:** a Status-screen line listing
      newly-available-but-unconfigured optional features (macmon today,
      whatever's next later) — a natural extension of the version-mismatch
      nudge, flagged here as a nice-to-have rather than required scope for
      this phase.

**Files touched (when implemented):** `internal/ops/baseline.go` /
`internal/ops/tools.go` (write the version marker on success),
`internal/ops/status.go` (read it, surface mismatch + stale-key findings),
`internal/ops/precheck.go` (shared stale-key/nudge function, reused by
Phase 7H and 9G), `internal/tui/status.go` (render the nudge),
`cmd/headless-macs/main.go` (CLI-mode nudge line).

---

## Files-touched summary

| File | Change |
|---|---|
| `internal/ops/status.go` | New — daemon state + resource use + optional macmon hardware snapshot |
| `cmd/headless-macs/main.go` | New `status` subcommand (with `--watch`) |
| `internal/tui/status.go` | New — live Service Status screen |
| `internal/tui/menu.go` | New "Service Status" entry |
| `internal/tui/app.go` | Wire the new screen into the app's screen-switching |
| `internal/tui/config_editor.go` | macmon + log-level fields |
| `internal/tui/run_screen.go` (or wherever Verify renders) | Only if not already generic over sections |
| `README.md`, `CLAUDE.md` | Usage docs updated |
| `internal/ops/baseline.go` / `internal/ops/tools.go` | *(10H)* Write version-stamp marker on success |
| `internal/ops/precheck.go` | *(10H)* Shared stale-key/nudge function |

---

## Open questions

- Does the existing Verify-rendering code already iterate sections
  generically, or does it hardcode a fixed section list? This determines
  whether Phase 10E touches the TUI beyond the config editor. Check
  `internal/tui/run_screen.go` (or equivalent) when this phase is picked
  up, before estimating its size.
- The `--watch` flag decision in Phase 10F is a working assessment, not a
  final one — confirm it still makes sense once Phases 7–9 are actually
  implemented and it's clear how much there is to watch.
- **(Phase 10H)** Should the version-mismatch nudge fire on *every* CLI
  invocation (simple, but repetitive if an operator runs several commands
  in a row without acting on it) or only once per marker-mismatch, tracked
  some other way (adds state to track, more complex)? Leaning toward
  "every invocation, but one line, easy to ignore" — matches this
  project's existing style of informational `[WARN]`/`[INFO]` lines rather
  than a dismissible/stateful notification system. Decide at
  implementation time.
- **(Phase 10H)** Where exactly should the shared stale-key/nudge function
  live — `internal/config` (next to `Load()`, so it's unmissable
  regardless of which command runs first, per the open question raised in
  Phase 7H) or `internal/ops/precheck.go` (matches the project's existing
  "Precheck is where config problems get diagnosed" convention, but only
  runs on that code path)? This question is raised in three phases now
  (7H, 9G, 10H) precisely because it wasn't settled the first time —
  resolve it once, here, since Phase 10 is where the shared implementation
  actually gets written.
