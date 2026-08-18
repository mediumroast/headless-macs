# Go TUI Style Guide

A design and implementation guide for Go terminal applications built with
[Bubble Tea](https://github.com/charmbracelet/bubbletea) and
[Lip Gloss](https://github.com/charmbracelet/lipgloss). Extracted from the
headless-macs TUI, which serves as the reference implementation.

---

## Contents

1. [Color System](#1-color-system)
2. [Screen Anatomy](#2-screen-anatomy)
3. [Style Tokens](#3-style-tokens)
4. [Navigation Conventions](#4-navigation-conventions)
5. [Screen Patterns](#5-screen-patterns)
6. [Scrolling](#6-scrolling)
7. [Spinner and Loading State](#7-spinner-and-loading-state)
8. [Status Prefixes](#8-status-prefixes)
9. [Message Bus](#9-message-bus)
10. [State Machine](#10-state-machine)
11. [File and Package Layout](#11-file-and-package-layout)
12. [Checklist](#12-checklist)

---

## 1. Color System

The palette is **dark-first**. Terminal UIs cannot detect the user's background
color, so the design assumes a dark terminal. All colors are chosen to hold
contrast on a near-black (`#0F1117`) background.

### Palette

| Token        | Hex       | Role                                           |
|---|---|---|
| `colBg`      | `#0F1117` | Page background (referenced but rarely painted) |
| `colWhite`   | `#F9FAFB` | Primary text, active items                     |
| `colGrey`    | `#9CA3AF` | Secondary text, unselected menu items          |
| `colDimmed`  | `#4B5563` | Disabled items, key hints, `[SKIP]` text       |
| `colBorder`  | `#374151` | Horizontal rules, dividers                     |
| `colSlate`   | `#1E293B` | Selected row background                        |
| `colStatusBg`| `#1F2937` | Status bar background                          |
| `colSubHeader`| `#6B7280` | Tool / group headers inside a section          |
| `colAmber`   | `#D97706` | Section headers, menu title — the accent       |
| `colCyan`    | `#06B6D4` | Cursor, modified values, spinner               |
| `colGreen`   | `#10B981` | Success states, `[SET]`, `[PASS]`              |
| `colRed`     | `#EF4444` | Errors, blockers, `[FAIL]`                     |

### Semantic color rules

| Semantic meaning | Color   |
|---|---|
| Applied / success | green   |
| Warning / modified / attention | cyan    |
| Error / blocker / destructive | red     |
| Section accent | amber   |
| Navigation / help text | dimmed  |
| Active / selected | white on slate |
| Inactive | grey    |

**Do not introduce new palette entries.** Map every new color need to an
existing semantic role. If a new semantic role truly cannot be expressed with
the existing palette, document why before adding a token.

---

## 2. Screen Anatomy

Every screen shares the same fixed chrome: a title bar at the top and a status
bar at the bottom. The content area between them is scrollable.

```
┌────────────────────────────────────────────────────────────────┐
│  <app-name> v<version> — <screen-name>                         │  ← title bar
│────────────────────────────────────────────────────────────────│  ← divider
│                                                                │
│  [content area — scrollable]                                   │
│                                                                │
│────────────────────────────────────────────────────────────────│  ← divider
│  key hint 1  key hint 2  key hint 3                            │  ← status bar
└────────────────────────────────────────────────────────────────┘
```

### Title bar

```go
b.WriteString(styleTitle.Render(fmt.Sprintf(" myapp v%s — %s ", Version, screenName)))
b.WriteByte('\n')
b.WriteString(styleDivider.Render(strings.Repeat("─", max(m.width, 40))))
b.WriteByte('\n')
```

- Always bold white on `colBg`.
- Format: ` <app> v<version> — <screen> ` (note leading/trailing space for
  visual padding).
- The divider uses `─` (U+2500) repeated to terminal width.

### Status bar

```go
b.WriteString(styleStatusBar.Render(
    hint("↑↓", "navigate") + "  " + hint("enter", "select") + "  " + hint("q", "quit"),
))
```

- Always the last element rendered.
- Uses the `hint(key, desc)` helper: key in white, description in dimmed.
- Pairs are separated by two spaces.

### `hint()` helper

```go
func hint(key, desc string) string {
    return styleKeyName.Render(key) + styleKeyHint.Render(" "+desc)
}
```

One helper, used everywhere. Do not inline the color logic.

---

## 3. Style Tokens

Define all styles in a single `styles.go` file. Components import them as
package-level `var`s — no local style declarations in model files.

```go
// styles.go
package tui

import "github.com/charmbracelet/lipgloss"

var (
    colBg        = lipgloss.Color("#0F1117")
    colAmber     = lipgloss.Color("#D97706")
    // ... complete palette
)

var (
    styleTitle   = lipgloss.NewStyle().Bold(true).Foreground(colWhite).Background(colBg)
    styleDivider = lipgloss.NewStyle().Foreground(colBorder)
    // ... complete token set
)

func hint(key, desc string) string { ... }
```

### Full token reference

| Token                 | Use                                              |
|---|---|
| `styleTitle`          | Screen title bar text                           |
| `styleDivider`        | Horizontal rules                                |
| `styleSectionHeader`  | Section group labels (amber, bold)              |
| `styleToolHeader`     | Sub-group separator inside a section            |
| `styleFieldLabel`     | Left column of a two-column row (fixed width)   |
| `styleFieldValue`     | Right column, unmodified value                  |
| `styleFieldModified`  | Right column, modified or warning value         |
| `styleSelectedLabel`  | Left column of the currently selected row       |
| `styleSelectedValue`  | Right column, selected + unmodified             |
| `styleSelectedModified` | Right column, selected + modified             |
| `styleCursor`         | The `▶` cursor glyph                           |
| `styleStatusBar`      | Status bar background + padding                 |
| `styleStatusModified` | "[modified]" indicator — cyan, bold             |
| `styleStatusSaved`    | "[saved]" / success indicator — green, bold     |
| `styleKeyHint`        | Key hint descriptions, `[SKIP]` text            |
| `styleKeyName`        | Key hint key names — white                      |
| `styleMenuTitle`      | The menu's "What would you like to do?" prompt  |
| `styleMenuItem`       | Unselected menu item                            |
| `styleMenuItemSelected` | Highlighted menu item                         |
| `styleMenuItemDisabled` | Coming-soon / unavailable item                |
| `styleError`          | Error messages, blockers, warnings              |

---

## 4. Navigation Conventions

### Universal keys

| Key | Action |
|---|---|
| `↑` / `k` | Move up one row |
| `↓` / `j` | Move down one row |
| `PgUp` / `Ctrl+U` | Scroll up half a page |
| `PgDn` / `Ctrl+D` | Scroll down half a page |
| `q` / `Esc` | Return to parent screen / cancel |
| `Ctrl+C` | Quit application (handled at top-level `App`) |

`q` and `Esc` always send a `DiscardMsg{}` back to the parent model — they
never call `tea.Quit` directly from a child screen.

### Menu shortcuts

Every menu item has a single-character shortcut stored on the `MenuItem`
struct. The `Update` loop checks both the arrow+enter path and the direct
character shortcut:

```go
case tea.KeyMsg:
    switch msg.String() {
    case "up", "k":   m.cursor--
    case "down", "j": m.cursor++
    case "enter", " ":
        return m, func() tea.Msg { return MenuSelectMsg{Index: m.cursor} }
    default:
        for i, item := range m.items {
            if msg.String() == item.Key {
                return m, func() tea.Msg { return MenuSelectMsg{Index: i} }
            }
        }
    }
```

### Config editor keys

| Key | Action |
|---|---|
| `enter` / `space` | Toggle bool / enter text edit mode |
| `enter` (while editing) | Commit value |
| `Esc` (while editing) | Discard text change |
| `s` / `S` | Save and return to menu |
| `r` / `R` | Reset to saved state |
| `q` / `Q` / `Esc` | Discard and return to menu |

### Confirmation screen keys

Destructive-action screens accept only `y`/`Y` to confirm and any other key
to cancel. Never require two-key confirmation (e.g. "yes" typed in full) — it
adds friction without meaningful safety benefit.

---

## 5. Screen Patterns

### Menu screen

The menu is the root screen. It owns the app version in its title and the
`[key] Label` row format.

```go
var menuItems = []MenuItem{
    {Label: "Edit Config",    Key: "c", Ready: true},
    {Label: "Precheck",       Key: "p", Ready: true},
    {Label: "Not Yet Ready",  Key: "x", Ready: false},  // dimmed
    {Label: "Quit",           Key: "q", Ready: true},
}
```

Row rendering:

```go
cursor := "  "
if i == m.cursor { cursor = styleKeyName.Render("▶ ") }
label := fmt.Sprintf("[%s] %s", item.Key, item.Label)
if !item.Ready {
    line = cursor + styleMenuItemDisabled.Render(label + "  (coming soon)")
} else if i == m.cursor {
    line = cursor + styleMenuItemSelected.Render(label)
} else {
    line = cursor + styleMenuItem.Render(label)
}
```

### Run screen (ops stage)

Used for any long-running operation that produces a structured result. Two
states:

1. **Running** — spinner + single-line status message
2. **Done** — scrollable list of `[SET]`/`[SKIP]`/`[WARN]`/`[FAIL]` rows +
   summary line + log path

```go
if m.state == runStateRunning {
    b.WriteString("\n  " + m.spinner.View() + "  Running…\n")
    return b.String()
}
// ... render result rows
```

The same `RunScreenModel` handles multiple ops types by normalising them
through `stageActions()` and `stageSummary()` accessor methods.

### Precheck / verify screen

Functionally identical model reused for both Precheck and Verify — the `title`
field controls which name appears in the header. Renders `CheckItem` slices
with section grouping.

```go
func NewPrecheckModel() PrecheckModel { return PrecheckModel{title: "Precheck", ...} }
func NewVerifyModel()  PrecheckModel { return PrecheckModel{title: "Verify",   ...} }
```

### Config editor

A two-column scrollable form with three field kinds: `kindBool`, `kindString`,
`kindInt`. Fields are defined via closures that read and write directly into
the config struct — no intermediate binding layer.

```go
func boolField(label string, get func() bool, set func(bool)) field { ... }
func strField(label string, get func() string, set func(string)) field { ... }
func intField(label string, get func() int, set func(int)) field { ... }
```

Two-column layout uses a fixed-width left column:

```go
styleFieldLabel = lipgloss.NewStyle().Foreground(colGrey).Width(34)
```

Set `Width()` on the label style, not via padding. This keeps columns aligned
regardless of label text length.

Modified-value detection compares the current value against the original JSON
snapshot taken at editor creation. If values differ, the right column renders
in `styleFieldModified` (cyan) to signal unsaved changes.

### Confirmation screen

Used before any destructive action. Pattern:

1. Title bar: `… — <ActionName>`
2. Red warning line with `⚠` glyph
3. Bulleted list of consequences in `styleKeyHint`
4. Reboot advisory in `styleFieldModified`
5. Status bar: `hint("y", "confirm") + "  " + hint("any other key", "cancel")`

```go
b.WriteString(styleError.Render("  ⚠  This will remove all X:"))
for _, c := range consequences {
    b.WriteString(styleKeyHint.Render("    • " + c))
}
```

---

## 6. Scrolling

All content screens implement the same scroll pattern.

### `visibleRows()`

```go
func (m MyModel) visibleRows() int {
    v := m.height - 9 // title(2) + above-indicator(1) + below-indicator(1)
                      // + summary(2) + log(1) + status(1) + padding(1)
    if v < 1 { v = 1 }
    return v
}
```

Adjust the constant to match your screen's fixed chrome. Clamp to minimum 1.

### Render window

```go
end := m.scroll + visible
if end > len(rows) { end = len(rows) }
start := m.scroll
if start > end  { start = end }

for _, row := range rows[start:end] {
    b.WriteString(row)
    b.WriteByte('\n')
}
// Pad empty lines so the summary stays anchored at the bottom
for i := end - start; i < visible; i++ {
    b.WriteByte('\n')
}
```

Always pad to `visible` rows so the summary line and status bar do not jump
as the user scrolls.

### Above/below indicators

```go
if m.scroll > 0 {
    b.WriteString(styleKeyHint.Render(fmt.Sprintf("  ↑  %d more above\n", m.scroll)))
} else {
    b.WriteByte('\n')  // preserve height whether or not indicator is shown
}
// ... content rows ...
remaining := len(rows) - end
if remaining > 0 {
    b.WriteString(styleKeyHint.Render(fmt.Sprintf("  ↓  %d more below\n", remaining)))
} else {
    b.WriteByte('\n')
}
```

The empty `'\n'` when there is nothing above/below is required — it keeps
the content area height stable so the summary and status bar do not shift
position.

### Key handling for scroll

```go
case "up", "k":
    if m.scroll > 0 { m.scroll-- }
case "down", "j":
    if m.scroll < n-1 { m.scroll++ }
case "pgup", "ctrl+u":
    m.scroll -= visible / 2
    if m.scroll < 0 { m.scroll = 0 }
case "pgdn", "ctrl+d":
    m.scroll += visible / 2
    if m.scroll > n-1 { m.scroll = n - 1 }
    if m.scroll < 0  { m.scroll = 0 }
```

---

## 7. Spinner and Loading State

Use `spinner.Points` (the default multi-dot spinner) in `colCyan`.

```go
import "github.com/charmbracelet/bubbles/spinner"

s := spinner.New()
s.Spinner = spinner.Points
s.Style  = lipgloss.NewStyle().Foreground(colCyan)
```

The spinner only ticks while in the running state. Gate tick updates:

```go
case spinner.TickMsg:
    if m.state == runStateRunning {
        var cmd tea.Cmd
        m.spinner, cmd = m.spinner.Update(msg)
        return m, cmd
    }
```

Running-state message format:

```
  ⣾  Running… (read-only, no changes made)
```

Two leading spaces + spinner view + two spaces + status message. Keep the
status message specific about whether changes are being made and whether sudo
is required.

---

## 8. Status Prefixes

These prefixes are rendered in the TUI exactly as they appear in the CLI output,
so the TUI and headless modes stay readable at a glance.

| Prefix    | Lip Gloss style         | Meaning                              |
|---|---|---|
| `[SET]`   | `styleStatusSaved` (green) | A change was applied              |
| `[SKIP]`  | `styleKeyHint` (dimmed)   | Already correct, nothing done       |
| `[WARN]`  | `styleFieldModified` (cyan)| Non-fatal issue                    |
| `[FAIL]`  | `styleError` (red)        | Fatal failure                        |
| `[PASS]`  | `styleStatusSaved` (green) | Health check succeeded              |
| `[BLOK]`  | `styleError` (red)        | Hard blocker (precheck only)        |
| `[INFO]`  | `styleKeyHint` (dimmed)   | Informational                       |

Prefix column is 9 characters wide: `  [SET]  ` (2 leading, 5 chars, 2
trailing). Detail lines indent 9 characters to align under the message:

```go
rows = append(rows, styleKeyHint.Render(prefix) + render(a.Message))
if a.Detail != "" {
    rows = append(rows, styleKeyHint.Render("         ") + styleFieldModified.Render(a.Detail))
}
```

### `actionStyle()` helper

```go
func actionStyle(s ActionStatus) (prefix string, render func(string) string) {
    switch s {
    case ActionSet:
        return "  [SET]  ", func(m string) string { return styleStatusSaved.Render(m) }
    case ActionSkip:
        return "  [SKIP] ", func(m string) string { return styleKeyHint.Render(m) }
    case ActionWarn:
        return "  [WARN] ", func(m string) string { return styleFieldModified.Render(m) }
    case ActionFail:
        return "  [FAIL] ", func(m string) string { return styleError.Render(m) }
    default:
        return "         ", func(m string) string { return styleFieldValue.Render(m) }
    }
}
```

---

## 9. Message Bus

All inter-model communication uses typed messages passed through the Bubble
Tea runtime. Never call child model methods directly from the parent `Update`.

### Standard message types

```go
// Ops completion — one per ops stage
type BaselineDoneMsg struct {
    Result *ops.BaselineResult
    Err    error
}

// Generic navigation
type DiscardMsg struct{}          // child → parent: go back / cancel
type SavedMsg  struct{ Cfg *Config } // config editor → app: saved and close

// Destructive action gates
type RestoreConfirmedMsg struct{}  // confirmation screen → parent: proceed
```

### Launching ops in a goroutine

```go
func runBaselineCmd(cfg *config.Config) tea.Cmd {
    return func() tea.Msg {
        result, err := ops.RunBaseline(cfg, ops.BaselineOptions{})
        return BaselineDoneMsg{Result: result, Err: err}
    }
}
```

Start with `tea.Batch(runBaselineCmd(cfg), m.spinner.Tick)`. The `Tick`
must be included in the initial batch — without it the spinner never animates.

### Routing in `App.Update`

The parent `App` routes completed messages to the active child model:

```go
case BaselineDoneMsg:
    updated, cmd := a.runScreen.Update(msg)
    a.runScreen = updated.(RunScreenModel)
    return a, cmd
```

Child models receive the raw message and transition state themselves. The
parent does not inspect the result — it only routes.

---

## 10. State Machine

Every screen model has an explicit state enum. Two-state is the common case:

```go
type runState int
const (
    runStateRunning runState = iota
    runStateDone
)
```

Rules:
- Only the running state accepts `spinner.TickMsg`.
- Only the done state accepts scroll and navigation keys.
- The `View()` method switches on state, never on result nilness.

### Window size propagation

The parent `App` owns `width` and `height`. It propagates `tea.WindowSizeMsg`
to all child models in the `Update` path, and also copies dimensions onto new
child models at the point they are created:

```go
case "b":
    a.runScreen = NewRunScreen("System Baseline")
    a.runScreen.width  = a.width   // propagate current terminal size
    a.runScreen.height = a.height
    a.screen = screenBaseline
    return a, tea.Batch(runBaselineCmd(a.cfg), a.runScreen.spinner.Tick)
```

Without this, a newly created model has `width=0` and `height=0` until the
next resize event — which may never come, leaving `visibleRows()` returning 1.

---

## 11. File and Package Layout

```
internal/tui/
    tui.go             Package doc comment only
    app.go             App model: screen routing, WindowSizeMsg, message dispatch
    styles.go          Palette vars, style vars, hint() helper — nothing else
    menu.go            MenuModel, MenuItem, menuItems slice
    run_screen.go      RunScreenModel + all *DoneMsg types and run*Cmd funcs
    precheck_screen.go PrecheckModel (reused for Verify)
    config_editor.go   ConfigEditorModel + field kinds + buildFields()
    restore_confirm.go RestoreConfirmModel
```

**One model per file.** The run-command functions (`runBaselineCmd`, etc.) live
in the same file as the model that receives their `DoneMsg`. The `styles.go`
file imports nothing from the project — only Lip Gloss.

### Package-level `Version` variable

```go
// app.go
var Version = "dev"  // set by main.go before NewApp() is called
```

```go
// main.go
const version = "2.1.1"
// ...
tui.Version = version
app := tui.NewApp(cfg, firstRun)
```

Single source of truth: the `version` constant in `main.go`. The TUI reads it
at runtime via the package variable. No build-time `-ldflags` injection needed.

---

## 12. Checklist

Use this when building a new screen or porting this design to a new project.

### New screen

- [ ] Declare a `screen` iota constant in `app.go`
- [ ] Create `<name>.go` in the `tui` package
- [ ] Embed `width`, `height int` in the model struct
- [ ] Handle `tea.WindowSizeMsg` in `Update`
- [ ] Title bar: `styleTitle.Render(fmt.Sprintf(" <app> v%s — <screen> ", Version))`
- [ ] Divider: `styleDivider.Render(strings.Repeat("─", max(m.width, 40)))`
- [ ] Status bar: `styleStatusBar.Render(hint(...) + "  " + hint(...))`
- [ ] `q`/`Esc` sends `DiscardMsg{}`, never calls `tea.Quit`
- [ ] All styles reference tokens from `styles.go`, not inline `lipgloss.Color` literals

### New ops stage

- [ ] Define `<Stage>DoneMsg` with `Result` and `Err` fields
- [ ] Define `run<Stage>Cmd()` returning `tea.Cmd`
- [ ] Add state enum (`runStateRunning` / `runStateDone`)
- [ ] Propagate `width`/`height` when creating the model in `app.go`
- [ ] Start with `tea.Batch(run<Stage>Cmd(...), m.spinner.Tick)`
- [ ] Route `<Stage>DoneMsg` in `App.Update`
- [ ] Implement `visibleRows()` and the scroll render window
- [ ] Pad empty lines when scroll indicators are absent

### New project

- [ ] Copy `styles.go` verbatim; adjust only the hex palette
- [ ] Keep `hint()` unchanged — callers depend on its signature
- [ ] Set `Version` from a `const` in `main.go` before calling `NewApp`
- [ ] Use `tea.WithAltScreen()` in `tea.NewProgram`
- [ ] Implement `Ctrl+C` → `tea.Quit` only at the top-level `App.Update`
- [ ] Do not use `tea.Quit` in child models; route `DiscardMsg` upward instead

---

## Dependencies

```
github.com/charmbracelet/bubbletea   v1.3.10+
github.com/charmbracelet/bubbles     v0.20.0+  (spinner, textinput)
github.com/charmbracelet/lipgloss    v1.1.0+
```

Lip Gloss version must be ≥ 1.0 — the `v0.x` API differs for adaptive colors
and border rendering.
