# PHASE 14 PLAN — v2.3.1: config location, `--config` override, migration

**Status: planning only. Nothing implemented. Stopping here per instruction.**

Branch: `claude/system-config-path-fix`, created off `claude/v2.3.1-fixes`
(PR [#20](https://github.com/mediumroast/headless-macs/pull/20), not yet
merged). Target version: **v2.3.1** (Patch — this is a reliability fix to
existing config handling, not a new capability; the default config
*location* changes, but nothing about `config.json`'s own schema does).

## Why this exists

Investigating Phase 13's "does `install-tools` silently wipe a debug
toggle" question led to `config.UserConfigPath()`
(`internal/config/config.go:147`), which is `filepath.Join(os.UserHomeDir(),
".headless_macs", "config.json")`. Two things verified, not assumed:

1. **Go's `os.UserHomeDir()` on macOS is exactly `os.Getenv("HOME")`** —
   read directly from Go's own stdlib source. No `/etc/passwd` fallback,
   nothing smarter.
2. **`sudo`'s own documented default (`sudoers(5)`, verified against the
   primary man page): when `env_reset` is enabled — the standard
   default — `HOME` is initialized based on the *target* user, not the
   invoking user.** Every `headless-macs` invocation requires `sudo`, so
   under that default, `$HOME` during the process would be root's home
   (`/var/root`), not the actual operator's.

Live-tested on doppio-2: `sudo sh -c 'echo $HOME'` came back the
operator's own home directory (e.g. `/Users/<operator>`), not root's — this
box's actual sudo configuration preserves `HOME`, so nothing is broken
*today*. But that's this box's configuration, not anything this project
controls or can guarantee holds on every box, every macOS version, or
after any future sudoers change. Relying on externally-configured shell
behavior for "where is my config" is fragile by construction even when it
currently works.

**A second, independent finding from the same diagnostic:**
`ls -la ~<operator>/.headless_macs/config.json` came back **Permission
denied** — for the user's own file. Root cause, confirmed in
`bootstrapTo()`/`saveTo()`: the file is written mode `0o600` inside a
`0o700` directory, and since every write happens while running as root
(via `sudo`), the result is owned by root — unreadable to the actual human
operator without another `sudo`. README currently claims a user can "edit
`~/.headless_macs/config.json` directly" (line 199) — that's not actually
true as things stand.

## Recommended fix

Move the default config location to a fixed, well-known **system** path —
**`/etc/headless-macs/config.json`** — instead of anything derived from
`$HOME`. This matches how this project already treats everything else it
manages: `/var/log/mac-llm-setup`, `/Library/LaunchDaemons`,
`/Library/LLMServer` are all fixed system paths, never home-relative.
Removes the sudo-`HOME` dependency entirely, on every box, regardless of
that box's own sudoers configuration — and fixes the read-permission
friction as a side effect (directory `0755`, file `0644` — root-writable,
world-readable, matching how e.g. `/etc/ssh/sshd_config` itself is laid
out, and how this project already handles the sudoers drop-in).

Add the user's proposed **`--config <path>`** as an explicit override on
top of that fixed default — for flexibility (testing, alternate profiles)
— not as the thing carrying the correctness fix. Correctness shouldn't
depend on remembering to pass a flag.

---

## Scope decision table

| Item | In scope | Out of scope |
|---|---|---|
| Change `UserConfigPath()`'s default to `/etc/headless-macs/config.json` | ✅ | Changing where anything *else* this project writes lives (logs, LaunchDaemons, model dirs) — unaffected, already fixed system paths |
| `--config <path>` override flag, all `headless-macs` subcommands + TUI | ✅ | A `--config` flag on `headless-macs-debug` — that binary doesn't read config today (Phase 12C deliberately chose a CLI flag over a config field specifically because of this same reachability problem); worth revisiting *once* the default path is fixed and trivially reachable, but that's a separate future decision, not bundled into this fix |
| One-time migration from `~/.headless_macs/config.json` (any user) to the new system path | ✅ | Automatically deleting the old per-user file after migrating — see Open Question 2 |
| `make install`/`make uninstall` updates | ✅ | Automatic removal of `/etc/headless-macs/config.json` on `make uninstall` — see Open Question 3 |
| Fix the file permissions (`0600`→`0644`) so a human can read their own config without `sudo` | ✅ | Making the config directly *writable* without root — it should stay root-only to write, matching every other system file this project manages |
| Update README/CLAUDE.md's documented config path | ✅ | Rewriting historical `docs/planning/PHASE_*_PLAN.md` files that mention the old path as part of recording past decisions — those are historical records, not living docs (established convention this session) |

---

## Phase 14A — Fix the default path and permissions

`internal/config/config.go`:

```go
const SystemConfigPath = "/etc/headless-macs/config.json"

func UserConfigPath() string {
    return SystemConfigPath
}
```

(Keeping the function name `UserConfigPath()` for now to minimize the diff
across its several callers — see Open Question 1 on whether it should be
renamed given it's no longer user-relative at all.)

`bootstrapTo()`/`saveTo()`: directory `os.MkdirAll(filepath.Dir(dest), 0o755)`,
file `os.WriteFile(dest, data, 0o644)` — root-writable, world-readable.

**Files touched:** `internal/config/config.go` only for this part —
every caller (`cmd/headless-macs/main.go`, `internal/tui/config_editor.go`,
`internal/ops/storage.go`, `internal/ops/precheck.go`) already goes through
`UserConfigPath()`/`Load()`/`Save()`/`Bootstrap()`, so none of them need to
change for the path itself to move.

---

## Phase 14B — `--config <path>` override

Proposed mechanism: a settable package-level override in `internal/config`
(e.g. `config.OverridePath string`), checked first by `UserConfigPath()`
before falling back to `SystemConfigPath`. `cmd/headless-macs/main.go`
scans `os.Args` for `--config=<path>` or `--config <path>` *anywhere* in
the arguments (matching the same "anywhere, not just position 0" pattern
`headless-macs-debug`'s `--help` fix just used, for the same reason — this
shouldn't depend on argument order) before dispatching to a subcommand or
launching the TUI, and sets the override if present.

A package-level var is the least invasive shape here — `Load()`/`Save()`/
`Bootstrap()` all currently take no path argument, and changing their
signatures would ripple through every existing call site for something
that's an edge-case override, not the common path.

**Files touched:** `internal/config/config.go` (the override var + its
check in `UserConfigPath()`), `cmd/headless-macs/main.go` (arg scanning,
usage text), `internal/tui/config_editor.go`'s footer display of the
config path (already calls `UserConfigPath()`, so it picks up an override
automatically — just confirming no separate change needed there).

---

## Phase 14C — Migration from the old per-user location

On `Load()` (or `Bootstrap()`, whichever runs first on a given invocation):
if `SystemConfigPath` doesn't exist yet, but the *old* location does,
migrate instead of bootstrapping fresh from the template. The old
location's path still needs computing via `os.UserHomeDir()` — this is
the one place that fragility is still touched, but only as a **detection
check**, not as the thing correctness depends on: if `$HOME` happens to
resolve wrong on some box during this one-time check, the worst case is
migration simply doesn't find the old file and bootstraps fresh from the
template (identical to a brand-new install) — not silent corruption or a
worse outcome than today.

```go
func migrateOrBootstrap(templatePath string) error {
    if _, err := os.Stat(SystemConfigPath); err == nil {
        return nil // already in the new place
    }
    if home, err := os.UserHomeDir(); err == nil {
        old := filepath.Join(home, ".headless_macs", "config.json")
        if data, err := os.ReadFile(old); err == nil {
            fmt.Println("Migrating config from", old, "to", SystemConfigPath)
            return saveRaw(SystemConfigPath, data) // preserves the existing file's content exactly, not a re-marshal
        }
    }
    return Bootstrap(templatePath)
}
```

This runs once, automatically, the first time any `headless-macs` command
executes under the new version — the operator doesn't need to do anything.

**Files touched:** `internal/config/config.go` (new migration function),
`cmd/headless-macs/main.go` (call it instead of the current bare
`Bootstrap()` call at first-run).

---

## Phase 14D — `make install`/`make uninstall`, docs

- `make install`: no code change needed for the config itself — migration
  happens automatically on first run of the newly-installed binary, not as
  a build step. Worth adding an `@echo` note (matching the pattern already
  used for the `headless-macs-debug` NOPASSWD note) pointing at the new
  path, so `install` isn't silent about a location change existing
  installs will notice.
- `make uninstall`: see Open Question 3 — whether it should ever touch
  `/etc/headless-macs/` at all.
- `README.md`: update all three references (Quick Start comment, Tool
  Selection section's "editing `~/.headless_macs/config.json` directly"
  claim — now actually true, since the file becomes world-readable and
  root remains the only writer, matching every other config-edit path in
  this project — and the repo-layout comment).
- `CLAUDE.md`: update the "Config-driven" bullet under Project Intent —
  this is a project-instructions file read at the start of every session,
  worth getting exactly right.

**Files touched:** `Makefile`, `README.md`, `CLAUDE.md`.

---

## Open questions

1. **Should `UserConfigPath()` be renamed**, now that it's not user-relative
   at all (e.g. `ConfigPath()`)? Keeps every call site accurate to what it
   actually does, at the cost of a slightly larger diff (every caller's
   name, not just its behavior, would change) for a purely cosmetic
   improvement. Leaning toward renaming for clarity, but it's your call
   given it touches more files for no functional reason.
2. **Should the old `~/.headless_macs/config.json` be deleted after a
   successful migration, or left in place?** Leaving it is safer (no data
   loss if migration somehow picked the wrong file, or if a human wants to
   compare/verify), but means two copies exist afterward, one of them
   inert — potentially confusing if someone stumbles on it later and
   assumes it's still authoritative. Leaning toward **leaving it**, since
   this project's own stated design goal is being reversible and
   non-destructive by default, but flagging rather than deciding.
3. **Should `make uninstall` ever remove `/etc/headless-macs/config.json`?**
   This is real user configuration — tuned settings, not a build artifact.
   `make uninstall` today only removes the two installed binaries; leaving
   config alone matches that scope and this project's broader philosophy
   (`headless-macs restore` — a separate, explicit, deliberate action — is
   what undoes *system state*; `uninstall` removing config too would blur
   that line and risks a surprise data loss for anyone who reinstalls
   later expecting their settings to still be there). Recommending
   **no** — `uninstall` stays binaries-only — but flagging since it's a
   real design choice, not a technical constraint.

---

## Files-touched summary

- `internal/config/config.go` — default path, permissions, override var,
  migration function
- `cmd/headless-macs/main.go` — `--config` arg scanning, usage text,
  call the migration function instead of bare `Bootstrap()`
- `Makefile` — install-output note about the new config location
- `README.md`, `CLAUDE.md` — update the three-plus documented references
  to `~/.headless_macs/config.json`
- `CHANGELOG.md` — `[Unreleased]` entry once implemented
