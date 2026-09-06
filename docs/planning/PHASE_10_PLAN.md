# PHASE 10 PLAN — TUI Structure Redesign and CLI Changes

**✅ IMPLEMENTED (2026-09-06).** Every section below is checked off with
its actual implementation notes, including honest call-outs of what was
deliberately deferred (Dashboard scrolling, per-screen visibility gating
of the refresh tick, stale-config-key surfacing on the Dashboard) and
what could not be verified in this sandboxed environment (real-terminal
visual rendering, the narrow-width sidebar-collapse threshold). See the
end of each phase's checklist for specifics. `go build ./... && go vet
./... && go test ./...` and `gofmt -l` are clean as of this update.

---

## Response to 2026-09-06 feedback

**Update, same day, second round:** two more items added below —
**Phase 10-Paint** (paint `colBg` everywhere instead of relying on the
terminal's own theme, confirmed as the actual cause of the Termius
screenshot looking different) and **Phase 10-Version** (the running
binary's version string is stale — `const version = "2.1.1"` hasn't moved
across three merged phases of new features, which also means Phase 10G's
version-nudge feature would compare a binary against itself and never
fire). Neither changes any color, palette entry, or semantic convention in
`docs/TUI_STYLE_GUIDE.md` — see the Scope table for the explicit
in/out line on that.

Two separate things happened and I want to be precise about which is which,
because only one of them is real.

**1. "Why did you change the look and feel?" — I didn't, and I checked
rather than assert that.**

```
git diff --stat 700ce55..8a21af8 -- internal/tui/
 internal/tui/config_editor.go | 10 +++++++++-
 1 file changed, 9 insertions(+), 1 deletion(-)
```

That's the entire TUI diff across the Phase 7, 8, and 9 commits — one field
added to the config editor (Exo's bootstrap-peers list). `styles.go`,
`menu.go`, `app.go`, `run_screen.go`, and `precheck_screen.go` — every file
that controls color, layout, or chrome — have a byte-for-byte empty diff.
The screenshots you posted of v2.1.1's menu/Precheck/Verify screens are
still exactly what the current code renders for those three screens; I
never touched the renderer. If something looked different on doppio-2,
it's not from anything in these three phases — worth tracking down
separately, but not by assuming I redesigned something I didn't touch.

**2. The structural complaint is real and this plan didn't address it.**
The original Phase 10 draft (below, superseded) treated "Service Status"
as one more entry appended to the existing flat, full-screen menu list —
select an item, it takes over the whole terminal, `q` goes back to the
flat list. That is a real gap against what you actually wanted: a
persistent left-hand navigation pane with a live content area next to it,
defaulting to a dashboard view of what's running — not one more page in a
stack of full-screen swaps. That's a bigger change than the original plan
scoped, and it's what the rest of this document redesigns.

One correction on numbering: you referenced "Phase 9" for the TUI refresh
— Phase 9 is the macmon daemon (`PHASE_9_PLAN.md`, already implemented).
This is Phase 10 (`PHASE_10_PLAN.md`), the TUI/CLI plan. Working from that
file — let me know if you meant something else.

**Non-negotiable constraint for everything below:** the color palette,
style tokens, status prefixes (`[PASS]`/`[WARN]`/`[SET]`/`[SKIP]`/`[FAIL]`),
and per-screen conventions in `docs/TUI_STYLE_GUIDE.md` do not change. This
plan is a layout/navigation restructuring, not a redesign — every token
referenced below (`styleTitle`, `colAmber`, etc.) is quoted from that file,
not invented here.

---

## Intent

Give the TUI the shape you described: a persistent function-selection pane
on the left, a content pane on the right/center that changes based on
selection, and a **Dashboard** as the default view showing what's actually
running on the box right now — daemon state, resource use, and (when
macmon is enabled) live hardware telemetry. This replaces the "flat menu →
full-screen swap → back to flat menu" navigation model everywhere in the
current TUI.

This still needs to expose Phases 7–9's new config and checks (macmon
fields, `log_level` fields, MACMON/service-suppression Verify sections) —
that part of the original plan is unchanged in substance, just relocated
into the new shell.

---

## Wireframes

Colors are noted in `[brackets]` referencing the exact token names from
`docs/TUI_STYLE_GUIDE.md` §1 — the ASCII below can't render color, so
don't read plain text as "no color," read the bracket notes as the actual
styling.

### 1. Dashboard — the default view on launch (after first-run config bootstrap, same as today)

```
┌──────────────────────────────────────────────────────────────────────────────┐
│ headless-macs v2.1.1                                          [styleTitle]   │
│────────────────────────────────────────────────────────────────────────────── [styleDivider]
│ ▸ Dashboard    │  SERVICES                                    [styleSectionHeader, amber]
│   Edit Config  │  [PASS] com.ollama.server         running   PID 1234  0.4% 2.1G
│   Precheck     │  [PASS] com.rapid-mlx.server       running   PID 1240  1.1% 22.3G
│   Storage      │  [SKIP] com.mlx-lm.server          disabled
│   Baseline     │  [SKIP] com.infinity.server        disabled
│   Install      │  [SKIP] com.exo.node               disabled
│   Verify       │  [SKIP] com.llm-server.macmon      disabled
│   Restore      │
│   Update       │  HARDWARE                                    [styleSectionHeader, amber]
│   Quit         │  [INFO] macmon disabled — enable tools.macmon for CPU/GPU/temp
│                │
│                │
│                │
│                │  v2.1.1 configured this box · binary v2.1.1 · up to date
│────────────────────────────────────────────────────────────────────────────── [styleDivider]
│ ↑↓ navigate   enter select   q quit                            [styleStatusBar]
└──────────────────────────────────────────────────────────────────────────────┘
  ^sidebar, fixed width       ^content pane, fills remaining width
  [styleMenuItemSelected      [existing screen-rendering styles — pass/warn/
   for "▸ Dashboard"]          skip/fail prefixes exactly as verify.go emits]
```

With macmon enabled, the HARDWARE block becomes:

```
│  HARDWARE                                    [styleSectionHeader, amber]
│  cpu power   8.4 W        cpu temp   42.1 C     [styleFieldValue]
│  gpu power   3.1 W        gpu temp   38.7 C
│  memory      42 / 128 GB
```

### 2. Verify — sidebar stays, content pane swaps

```
┌──────────────────────────────────────────────────────────────────────────────┐
│ headless-macs v2.1.1                                          [styleTitle]   │
│──────────────────────────────────────────────────────────────────────────────│
│   Dashboard    │  SYSTEM                                      [styleSectionHeader]
│   Edit Config  │  [PASS] pmset sleep=0
│   Precheck     │  [PASS] caffeinate daemon running
│   Storage      │  [WARN] SSH not enabled
│   Baseline     │         Fix: sudo launchctl enable ...
│   Install      │
│ ▸ Verify       │  MACMON                                      [styleSectionHeader]
│   Restore      │  [PASS] com.llm-server.macmon running
│   Update       │  [PASS] macmon API responding (cpu_power)
│   Quit         │
│                │  ↓ 12 more below                              [styleKeyHint]
│                │
│                │  29 passed  1 warning  0 failures
│──────────────────────────────────────────────────────────────────────────────│
│ ↑↓/PgUp/PgDn scroll   q back to Dashboard                      [styleStatusBar]
└──────────────────────────────────────────────────────────────────────────────┘
```

Scrolling, section grouping, and the summary line are unchanged from the
current `PrecheckModel` (`docs/TUI_STYLE_GUIDE.md` §5, §6) — only the
content pane's width shrinks to make room for the sidebar, and `q` returns
to Dashboard instead of a bare menu.

### 3. Config Editor — same two-column field layout, inside the content pane

```
┌──────────────────────────────────────────────────────────────────────────────┐
│ headless-macs v2.1.1                                          [styleTitle]   │
│──────────────────────────────────────────────────────────────────────────────│
│   Dashboard    │  TOOLS — Ollama                              [styleToolHeader]
│ ▸ Edit Config  │  Enabled                          [x]
│   Precheck     │  Log Level                        warn
│   Storage      │
│   Baseline     │  TOOLS — macmon                              [styleToolHeader]
│   Install      │  Enabled                          [ ]
│   Verify       │  Port                             9090
│   Restore      │  Interval (ms)                    1000
│   Update       │
│   Quit         │
│                │  Config: ~/.headless_macs/config.json [saved] [styleStatusSaved]
│──────────────────────────────────────────────────────────────────────────────│
│ enter edit   s save   r reset   q discard                      [styleStatusBar]
└──────────────────────────────────────────────────────────────────────────────┘
```

### 4. Narrow terminal (< ~80 columns) — resolved 2026-09-06: collapse to icons

Below some width threshold the two-pane layout doesn't fit. **Decision:
sidebar collapses to a single-character rail** (`▸`/single-character
markers, no labels), content pane keeps most of the width — not a drop
to today's flat-menu behavior. Exact width threshold still needs picking
against a real narrow terminal at implementation time (likely ~60–70
columns, based on the sidebar's longest label plus a usable content pane).

---

## Scope

| Item | In | Out |
|---|---|---|
| Persistent sidebar + content-pane shell (`app.go` rework) | ✓ | |
| Sidebar = today's flat menu items, vertically docked left, always visible | ✓ | |
| **Dashboard** as the default content pane (daemon state + resource use + optional macmon hardware panel + version-nudge line) | ✓ | |
| `internal/ops/status.go` — daemon state/resource-use data source, shared by Dashboard and CLI | ✓ | |
| `headless-macs status` CLI subcommand (non-interactive) | ✓ | |
| Existing screens (Precheck/Verify/Config Editor/Run Screen/Restore Confirm) adapted to render inside the bounded content pane instead of full-screen | ✓ | |
| Config editor: `tools.macmon` + `tools.*.log_level` fields | ✓ | |
| Verify screen: MACMON + Phase 8 service-suppression sections (confirm generic rendering handles this — likely already true) | ✓ | |
| Version-stamp marker + upgrade-awareness nudge (surfaced on the Dashboard) | ✓ | |
| Shared stale/unknown-config-key detector (already implemented in Phase 7H/9G's `precheck.go`; Dashboard surfaces it too) | ✓ | |
| **Full-screen background painting** (`colBg` behind everything, not terminal-theme-dependent) | ✓ | |
| **Build-time version injection** (`const`→`var`, `git describe` via `-ldflags`) so the title bar and Phase 10G's upgrade nudge are always truthful | ✓ | |
| **Any change to colors, style tokens, or status-prefix conventions** | | ✗ — `docs/TUI_STYLE_GUIDE.md`'s palette/semantics are unchanged; painting the *existing* `colBg` everywhere is not a new color |
| Narrow-terminal fallback: collapse sidebar to icon-only rail (resolved 2026-09-06) | ✓ | |
| Full Grafana/Prometheus dashboard UI | | ✗ — macmon's `/metrics` endpoint is the integration point; a real dashboard is separate, future work |
| Any UI for the deferred TLS/auth gateway (Item 12) | | ✗ — no gateway exists yet to have a UI for |
| Historical/trend graphs (sparklines, charts) | | ✗ — Dashboard is a live point-in-time snapshot; charting is a future enhancement if that proves insufficient |

---

## Resolved design decisions

| Question | Decision |
|---|---|
| Where does daemon state come from? | `launchctl print system/<label>` per daemon, parsed for `state = running` and PID — factored out of `verify.go`'s existing `checkDaemon()` into a shared helper rather than duplicated |
| Where does resource use come from? | `ps -o pid,rss,%cpu -p <pid>` per running daemon's PID (works regardless of macmon); macmon's `GET /json` for system-wide power/thermal, only when `tools.macmon.enabled` |
| Degradation when macmon is disabled | Dashboard still shows daemon up/down + per-daemon RSS/CPU%; the HARDWARE block shows a one-line `[INFO]` instead of numbers — never an error, never blank space |
| CLI shape | Existing plain-subcommand style (`precheck`, `baseline`, ... ), `status` added the same way |
| **Shell architecture** | Sidebar and content pane are both owned by `App` (`app.go`), replacing the current "one active full-screen child model" pattern with "sidebar (always rendered) + one active content model (rendered into a bounded sub-region)." Each existing screen model needs its width/height reduced by the sidebar's width, not the full terminal — this is the core rework; see Phase 10-Shell below. |
| Does the sidebar replace `menu.go`? | Yes — `MenuModel`'s item list becomes the sidebar's item list; the render target changes from full-screen to a fixed-width left column, and selecting an item switches the active content model instead of pushing a new full-screen screen |
| First-run config bootstrap | Unchanged — a fresh install still opens the Config Editor before anything else, per the existing Phase 6B.1 behavior; the *shape* of that editor screen changes (now inside the shell, if terminal width allows) but the "config must be reviewed before proceeding" gate stays |

---

## Phases

### Phase 10A — `internal/ops/status.go`
**Goal:** A single ops module answering "what's running and what is it
using," shared by the Dashboard content pane and the CLI subcommand.

- [x] `DaemonStatus`/`HardwareSnapshot`/`StatusResult` structs, matching
      the planned fields
- [x] `RunStatus(cfg)`: always-on infra daemons (`sysctl-tuning` only when
      `cfg.System.NetworkTuning`) + one entry per enabled tool + macmon
      when enabled
- [x] Extracted `daemonStateInDomain(domain, label)` from `verify.go`'s
      `checkDaemon()`/`checkServiceSuppressed()` (both now call it too —
      `daemonState()` is the "system"-domain convenience wrapper). Exo
      needed the general `domain` parameter since its LaunchAgent lives in
      `gui/<uid>`, not `system` — not anticipated in the original plan
      wording, handled correctly
- [x] `psStats(pid)` via `ps -o rss=,pcpu=`
- [x] `fetchMacmonHardware(port)` — direct fetch, not through `checkHTTP()`,
      nil on any failure

**Files touched:** `internal/ops/status.go` (new), `internal/ops/verify.go`
(shared `daemonStateInDomain`/`daemonState`)

---

### Phase 10B — `headless-macs status` CLI subcommand
**Goal:** Non-interactive status output, same tone as `verify`.

- [x] Added `"status"` to the subcommand switch in `main.go`
- [x] Plain-text table: `[UP]`/`[DOWN]` prefix, label, PID, RSS
      (human-readable via `formatBytes`), CPU%
- [x] Hardware summary block when macmon data present; nothing extra
      otherwise
- [x] Exit code `0` always
- [x] `--watch` flag — reads the same `cfg.TUI.DashboardRefreshMs` as the
      TUI Dashboard (not a separate hardcoded interval)

**Files touched:** `cmd/headless-macs/main.go`

---

### Phase 10-Version — Accurate version reporting
**Goal:** the version shown in the TUI title bar and `--version` is always
actually true of the binary running, not a hand-maintained string someone
forgot to bump — and Phase 10G's whole upgrade-nudge mechanism depends on
this being reliable, so it belongs before that phase, not after.

**The concrete problem, found while checking this:** `const version =
"2.1.1"` (`cmd/headless-macs/main.go:17`) hasn't moved since the tagged
v2.1.1 release, even though this branch now carries three full phases of
new features (macmon, five new suppressions, Ollama/Exo/logging fixes) on
top of it. Every local build anyone makes from this branch — including
the doppio-2 build this feedback came from — reports itself as
indistinguishable from the actual released v2.1.1. That's not just
cosmetically wrong: Phase 10G's version-stamp nudge *compares the running
binary's version against a stored marker* to tell an operator "you're
running newer code than what last configured this box." If the version
string never changes on a branch that plainly has changed, that comparison
is meaningless — the whole feature silently does nothing.

**Two problems to fix, not one:**

1. **Immediate:** decide and set the right version for what's actually on
   this branch right now, per `CLAUDE.md`'s bump rules (new config keys,
   new daemon, new checks — all backward-compatible — is Minor territory,
   same reasoning already used in `docs/RELEASE_STRATEGY.md`'s worked
   example for Phases 7–9).
2. **Structural:** make it hard for the version string to go stale again
   silently, since that's exactly what happened here — nobody touched
   `main.go`'s constant across three merged phases because nothing forced
   the question.

**Proposed structural fix — build-time version injection:**

- [x] `const version` → `var version = "dev"` in `main.go`
- [x] `Makefile`: `VERSION := $(shell git describe --tags --always --dirty ...)`,
      injected via `-ldflags -X main.version=$(VERSION)`. Verified end to
      end: `make build && ./headless-macs --version` printed
      `v2.1.1-17-g8a21af8-dirty` — correctly reflecting 17 commits ahead of
      the last tag with uncommitted changes, not a stale "2.1.1"
- [x] Plain `go build` (bypassing the Makefile) confirmed to fall back to
      `"dev"` — no special-casing needed beyond the var's own default
- [x] `--version` and the TUI title bar needed no changes — both already
      read the single `version` variable

**Also set:** `ops.Version = version` alongside the existing
`tui.Version = version` in `main.go` — the ops package needed its own copy
for `WriteVersionMarker()`/`VersionMismatch()` (Phase 10G), not
anticipated as a separate wiring step in the original plan text but a
direct consequence of it.

**Files touched:** `cmd/headless-macs/main.go` (`const` → `var`), `Makefile`
(`VERSION`/`GOFLAGS`)

---

### Phase 10-Shell — Sidebar + content-pane app shell
**Goal:** The actual structural change. Everything else in this plan
depends on this landing first.

This is materially bigger than the original Phase 10 draft assumed (which
only proposed adding one more entry to the existing flat menu). Flagging
that honestly rather than folding it into a smaller-sounding task.

- [x] Folded into `app.go` rather than a separate `shell.go` — sidebar
      width (`sidebarWidth()` in menu.go, sized off the longest `[k] Label`
      string) and content-pane width (`App.contentDims()`) are small
      enough functions that a separate file wasn't warranted
- [x] `App.View()`: one `styleTitle` bar, one `styleDivider`, sidebar +
      `│` divider + content pane joined line-by-line, one more
      `styleDivider`, one `styleStatusBar.Width(a.width)` — single title
      and status bar for the whole shell, no per-screen title bars
- [x] Every content model's `width`/`height` set from a **synthetic
      content-pane-sized `tea.WindowSizeMsg`** that `App` constructs and
      forwards on every real resize — chosen over passing explicit
      width/height parameters to `Body()` on each render call, so every
      screen keeps a parameter-free `Body()`/`StatusHints()` pair reading
      its own stored dimensions, matching how `WindowSizeMsg` propagation
      already worked pre-Phase-10. `visibleRows()` recalibrated in each of
      `PrecheckModel`/`RunScreenModel`/`ConfigEditorModel` to subtract only
      each screen's own internal chrome (scroll indicators, internal
      divider, summary/log lines) now that shared title/status chrome is
      accounted for once, at the shell level, via `contentDims()`'s
      `paneH = a.height - 4`
- [x] `menu.go`'s `MenuModel` repurposed as the sidebar (kept the type
      name to minimize churn) — same items, same key-shortcut handling,
      renders as a fixed-width column via new `Body()`; `MenuSelectMsg`
      unchanged
- [x] `q`/`Esc` from any content screen sends `DiscardMsg{}` → `a.screen =
      screenDashboard` (was `screenMenu`)
- [x] `Dashboard` added first in `menuItems`, key `d` (not previously
      used); selected by default on launch (`NewApp`'s `startScreen`)
- [x] Narrow-terminal collapse: `MenuModel.Body()` checks
      `m.width <= sidebarCompactWidth` (5) and renders `[k]`-only rows.
      `narrowThreshold = 70` (a constant, not yet tuned against a real
      terminal) is what `App.contentDims()` checks to decide whether the
      sidebar gets `sidebarWidth()` or `sidebarCompactWidth` in the first
      place — **the exact threshold is still a guess, flagged in Open
      Questions, needs adjusting on a real terminal**

**Also found and fixed, not anticipated in the original plan:** `View()`
methods still needed to exist on every screen model (with that exact
name) purely to satisfy `tea.Model`'s interface requirement, since
`Update()`'s declared return type is `tea.Model` — Go checks interface
conformance at that point regardless of whether anything actually calls
`View()`. Each now reassembles a full-screen render from `Body()` +
`StatusHints()` for that reason alone; the shell never calls them.

**Files touched:** `internal/tui/app.go` (full rewrite), `internal/tui/menu.go`
(repurposed as sidebar), `internal/tui/precheck_screen.go`,
`internal/tui/run_screen.go`, `internal/tui/config_editor.go`,
`internal/tui/restore_confirm.go` (each split into `Body()`/`StatusHints()`
+ recalibrated `visibleRows()`)

---

### Phase 10-Paint — Full-screen background painting
**Goal:** the app's background is what `styles.go` says it is
(`colBg`, `#0F1117`) on every terminal, not whatever theme the user's
terminal emulator happens to be set to — confirmed on 2026-09-06 that
today's behavior is intentional-but-incomplete, not a bug introduced by
Phases 7–9: `docs/TUI_STYLE_GUIDE.md` itself documents `colBg` as "Page
background (referenced but rarely painted)," and grepping `styles.go`
confirms it — `Background(colBg)` appears exactly once, on `styleTitle`
(the title bar). Every other style (`styleFieldValue`, `styleKeyHint`,
`styleMenuItem`, `styleSectionHeader`, etc.) sets a foreground color only;
whatever's behind that text is the terminal's own background. This is why
the Termius screenshot showed a different shade than expected — nothing
here changed, the terminal theme did.

**Why this is a real (if small) technical problem, not a one-line fix:**
Lip Gloss renders each styled fragment independently, and each call to
`.Render()` emits its own ANSI reset at the end of that fragment. Simply
wrapping an *already-rendered* multi-fragment line in one more outer
`Background()` call afterward does not reliably repaint the whole line —
any reset sequence already baked into the middle of that string (from an
inner fragment ending) cuts the outer background back to default for
whatever comes after it. The fix has two parts, and the second one needs
validating against real screen output before it's trusted:

- [x] **Confirmed the exact failure mode by direct experiment before
      writing any fix** — a standalone Lip Gloss program forcing truecolor
      output showed precisely what the plan predicted: after a styled
      fragment's own reset, a subsequent unstyled fragment on the same
      line does NOT inherit an outer wrap's background, while content at a
      line's start or in Lip Gloss's own added padding does. This is not
      the same thing as "add `Background(colBg)` to a list of tokens" —
      several tokens turned out to be **dual-context** (used both in body
      content, wanting `colBg`, and inside the status bar, wanting
      `colStatusBg`) and needed splitting into separate tokens rather than
      one shared background:
  - `hint()`'s internal styling moved to new `styleStatusKeyName`/
    `styleStatusKeyHint` (colStatusBg) — `styleKeyName`/`styleKeyHint`
    keep colBg for their other (body-context) uses
  - `styleStatusSaved`/`styleStatusModified` are used in body-content
    summary lines (colBg) **and** the config editor's inline
    `[saved]`/`[modified]` indicator — resolved by moving that indicator
    into `ConfigEditorModel.Body()` (see Phase 10-Shell) rather than the
    shared status bar, so both tokens stay single-context (colBg)
  - New `statusGap()` helper for the naked `"  "` literal separators
    between multiple `hint()` calls on one status-bar line — found by the
    same experiment, not part of the original per-token plan
  - Every other base style (`styleDivider`, `styleSectionHeader`,
    `styleToolHeader`, `styleFieldLabel`, `styleFieldValue`,
    `styleFieldModified`, `styleCursor`, `styleMenuTitle`, `styleMenuItem`,
    `styleMenuItemDisabled`, `styleError`) got `Background(colBg)` as
    planned. `styleSelectedLabel`/`Value`/`Modified`/`styleMenuItemSelected`
    (colSlate) and `styleStatusBar` (colStatusBg) untouched, as planned
- [x] Outer wrap: `stylePage(width, height)` in `styles.go`, applied once
      around `App.View()`'s entire output — confirmed by the same
      experiment that Lip Gloss correctly re-applies its background before
      each line's own trailing padding, which is what actually makes this
      work for line-end/bottom-of-screen fill
- [x] **Validated, not assumed — and this found a real, reproducible bug:**
      a smoke test rendering the Dashboard's nudge line at realistic widths
      showed it overflowing the content pane and visually breaking sidebar
      alignment, exactly the risk this item warned about. Fixed with
      `.MaxWidth(n)` (confirmed by a second direct experiment to truncate
      cleanly with a proper reset, not corrupt the ANSI stream) on: the
      Dashboard's nudge/error lines, and the message/detail text in
      `PrecheckModel.renderChecks()` and `RunScreenModel.renderActions()`
      (budgeted for each line's prefix width) — this second set wasn't
      caught by the smoke test directly but is the same class of risk the
      user's own Verify screenshot demonstrated (a long `ssh` fix command)
- [x] Also found and fixed while re-checking every screen (not
      hypothetical, found by grep + the same experiment): naked `" "`
      separators between label and value in `ConfigEditorModel`'s
      `renderBoolRow()`/`renderTextRow()` (both selected and unselected
      variants, plus the inline text-edit mode) — fixed by folding the
      separator into the *value* style's own render rather than the
      label's (the label has a fixed `Width(34)` for column alignment;
      folding text into it would have shifted that padding instead)

**Files touched:** `internal/tui/styles.go` (background additions, token
splits, `stylePage()`, `statusGap()`), `internal/tui/app.go` (outer wrap),
`internal/tui/menu.go`, `internal/tui/config_editor.go`,
`internal/tui/precheck_screen.go`, `internal/tui/run_screen.go`,
`internal/tui/restore_confirm.go` (gap fixes)

---

### Phase 10C — Dashboard content pane
**Goal:** The live view described above — the thing that should be the
first thing you see.

- [x] New `internal/tui/dashboard.go`: `DashboardModel` renders
      `ops.RunStatus()`'s output inside the content pane — SERVICES table
      (`renderDaemonRow()`), HARDWARE block (or the fallback when macmon
      isn't reachable), version-nudge line at the bottom
- [x] Periodic refresh via `tea.Tick` (`dashboardTickCmd`), interval from
      the new `config.json` key `tui.dashboard_refresh_ms` (new `TUI`
      struct in `internal/config/config.go`, its own top-level section —
      not folded into an existing one), default `2000`ms when unset/zero.
      **Known deviation, stated plainly:** the tick/fetch loop is not
      gated by which screen is currently visible — it runs continuously
      once the app starts, not only while the Dashboard is the active
      content pane. This was a deliberate simplification to avoid
      threading visibility state through `app.go`'s message routing, not
      an oversight, but it does mean background CPU/process-list polling
      happens even while, say, the Config Editor is open.
- [x] Reuses `internal/ops/status.RunStatus()` — confirmed no separate
      data path from the `status` CLI subcommand (both call the same
      function)
- [x] Same palette as everywhere else: running = green
      (`styleStatusSaved`), down/disabled = dimmed (`styleKeyHint`),
      hardware figures in `styleFieldValue`

**Not done / unverified:** no scrolling in this v1 — if the daemon list
plus hardware block exceeds the terminal height, it will simply overflow
rather than scroll like Precheck/Verify do. No real-terminal visual
check was possible in this environment (see Phase 10-Paint); only
synthetic ANSI-byte-level tests were run.

**Files touched:** `internal/tui/dashboard.go` (new),
`internal/config/config.go` (`TUI` struct), `config.json` (new key)

---

### Phase 10D — Config editor and Verify screen updates
**Goal:** Phases 7–9's new config and checks are actually visible.

- [x] Config editor: added a `macmon` tool-header section (Enabled/Port/
      Interval ms) and an Ollama `Log Level` field to `buildFields()` in
      `internal/tui/config_editor.go`. Also added a `TUI` section exposing
      `Dashboard Refresh (ms)` (new in this phase, not carried over from
      Phase 7C) so the new config key from 10C is editable, not just
      settable by hand-editing `config.json`. No other tool gained a
      `log_level` key in Phase 7C, so Ollama is the only one that needed
      this field.
- [x] **Confirmed 2026-09-06** (not deferred to implementation time):
      `internal/tui/precheck_screen.go` (lines 263–271) already renders
      sections generically — it prints a new `styleSectionHeader` whenever
      `CheckItem.Section` changes value, with no hardcoded section-name
      list anywhere. MACMON and the Phase 8 suppression sections already
      render correctly today with **zero code changes**. The only
      remaining item here is a manual check that the content pane's
      narrower width (now that the sidebar takes some columns) doesn't
      overflow/wrap awkwardly with more sections present — a verification
      step, not a rendering change.

**Files touched:** `internal/tui/config_editor.go` only — Verify's
renderer needs no changes.

---

### Phase 10E — CLI shape
**Goal:** Settle the `status` subcommand's variants; the menu-shape half
of the original Phase 10F is superseded by Phase 10-Shell (there's no
longer a "flat list too long" question — the sidebar structure answers it
directly).

- [x] `headless-macs status --watch`: implemented in
      `cmd/headless-macs/main.go`'s `runStatusCLI()` — clears the screen
      and re-renders on the same `tui.dashboard_refresh_ms` interval as
      the Dashboard (falls back to 2s if the config value is unset/zero)
- [x] Sidebar-vs-flat-list is resolved by Phase 10-Shell's actual
      implementation; no further menu-shape work needed here

**Files touched:** `cmd/headless-macs/main.go`

---

### Phase 10F — Documentation
**Goal:** Operators can discover the new shape from the docs.

- [x] `README.md`: rewrote the "Interactive TUI" section for the
      sidebar+Dashboard shape; added `status [--watch]` to the CLI
      subcommand list with a note about the version-nudge stderr line;
      added the explicit note that the existing screenshots are stale
      (see below)
- [x] `CLAUDE.md`: added `internal/ops/status.go` and
      `internal/ops/versionmarker.go` rows to the ops-package table.
      (There is no separate `shell.go` — the shell lives in the existing
      `internal/tui/app.go`, so no row was needed for that.)
- [ ] **New TUI screenshots — NOT done.** This environment has no real
      terminal to capture from (all verification here was synthetic
      Go-test ANSI-byte inspection, not visual rendering — see Phase
      10-Paint). `README.md` states plainly that its screenshots are
      stale rather than showing fabricated/incorrect ones. Retaking them
      needs a real terminal session on actual hardware.

**Files touched:** `README.md`, `CLAUDE.md`

---

### Phase 10G — Upgrade awareness (version-stamp nudge)
**Goal:** An operator who upgrades the binary has a signal that Baseline/
Install Tools need re-running — surfaced on the Dashboard, where they'll
actually see it.

- [x] Version-stamp marker: new `internal/ops/versionmarker.go` —
      `VersionMarker` struct, `WriteVersionMarker()`, `ReadVersionMarker()`,
      `VersionMismatch()`. Written to
      `/var/log/mac-llm-setup/.last-configured-version` by `RunBaseline`
      (full-run path only — deliberately *not* the `PowerOnly` early-return
      path the pmset-heal cron trigger uses, since that's not a real
      "this box was reconfigured" event) and by `RunTools`, both on
      success.
- [x] Mismatch detection wired into both surfaces: `printVersionNudge()`
      in `main.go` prints a stderr `[INFO]` line before every CLI command's
      output (naming the exact commands to re-run, not enumerating what
      changed), and the Dashboard's `Body()` renders the same nudge as its
      bottom line when a mismatch is present.
- [ ] **Deviation from plan, done differently on purpose:** did *not*
      reuse `precheck.go`'s `checkConfigKeys()`/`knownConfigKeyPaths()`/
      `collectKeyPaths()` mechanism — that machinery is about detecting
      stale/unknown *config keys*, a different problem from comparing the
      *running binary's version* against a marker file. Building a fourth
      implementation was avoided by not conflating the two rather than by
      sharing code between them.
- [ ] **Not done:** stale/unknown config keys are surfaced only via
      Precheck, as before this phase — they do not additionally appear on
      the Dashboard. The plan's rationale for wanting this ("operators
      will actually see it there") is still valid; this just wasn't
      implemented in this pass.

**Files touched:** `internal/ops/versionmarker.go` (new),
`internal/ops/baseline.go`, `internal/ops/tools.go`,
`cmd/headless-macs/main.go`, `internal/tui/dashboard.go`
(write the marker), `internal/ops/precheck.go` (already has the shared
mechanism — reused, not duplicated), `internal/tui/dashboard.go`,
`cmd/headless-macs/main.go`

---

## Files-touched summary

| File | Change |
|---|---|
| `internal/ops/status.go` | New — daemon state + resource use + optional macmon hardware snapshot |
| `cmd/headless-macs/main.go` | New `status` subcommand (with `--watch`), version-nudge stderr line |
| `internal/tui/app.go` | Sidebar + content-pane shell (major rework) |
| `internal/tui/shell.go` | New (or folded into `app.go`) — layout/sizing logic |
| `internal/tui/menu.go` | Repurposed: sidebar model instead of full-screen menu |
| `internal/tui/dashboard.go` | New — default content pane |
| `internal/tui/precheck_screen.go`, `run_screen.go`, `config_editor.go`, `restore_confirm.go` | Width/height adjusted for content-pane sizing; per-screen title bars removed |
| `internal/tui/config_editor.go` | macmon + log-level fields |
| `internal/ops/baseline.go` / `internal/ops/tools.go` | Write version-stamp marker on success |
| `internal/tui/styles.go` | `Background(colBg)` added to every base style (not the intentional `colSlate`/`colStatusBg` accents) |
| `cmd/headless-macs/main.go` | `version` changed from `const` to `var` (required for `-ldflags -X` injection) |
| `Makefile` | `VERSION`/`GOFLAGS` compute version from `git describe` at build time |
| `README.md`, `CLAUDE.md` | Usage docs + new screenshots |

---

## Open questions — resolved 2026-09-06

1. **Narrow-terminal fallback (wireframe 4):** ✅ **Collapse to icons.**
   Threshold still needs a concrete number at implementation time (likely
   somewhere around 60–70 columns, based on the sidebar's current
   longest-label width plus a usable content pane) — pick it by testing
   against an actual narrow terminal, not by guessing here.
2. **Sidebar item order:** ✅ **Confirmed as drafted** — Dashboard, Edit
   Config, Precheck, Storage, Baseline, Install, Verify, Restore, Update,
   Quit.
3. **Per-screen title bars:** ✅ **Confirmed — one shell-level title bar**,
   no per-content-pane header repeating the screen name.
4. **Does Verify already render sections generically?** ✅ **Yes, confirmed
   by reading `internal/tui/precheck_screen.go` directly** (lines 263–271):
   it watches for `CheckItem.Section` changing value and prints a new
   `styleSectionHeader` when it does — there is no hardcoded list of
   section names anywhere in the renderer. This means **MACMON and the
   Phase 8 suppression sections already render correctly today, with zero
   TUI code changes** — they show up automatically because `verify.go`
   already emits `CheckItem{Section: "MACMON", ...}` etc. Phase 10D's
   Verify-screen item is now just "confirm the content pane doesn't
   overflow with more sections," not a rendering change.
5. **Dashboard refresh interval:** ✅ **Configurable, defaulting from
   `config.json`** — not a fixed constant. Needs a new config key (e.g.
   `tui.dashboard_refresh_ms`, or fold into a broader `tui` config section
   if one doesn't exist yet — check `internal/config/config.go`'s current
   top-level sections before naming this) read by `internal/tui/dashboard.go`'s
   `tea.Tick` interval, with a sane default (2–3s) when unset.
6. **(Phase 10-Version)** What should the *next tagged release* version
   actually be once Phases 7–10 are all done — one combined minor bump
   covering all four, or the separate v2.2.0–v2.5.0 sequence
   `docs/RELEASE_STRATEGY.md`'s worked example laid out? Still open — the
   `git describe`-based dev-build fix works regardless of what the next
   tag ends up being, but the actual tag/release decision is yours.
