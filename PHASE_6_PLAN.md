# PHASE 6 PLAN — Go TUI Rewrite

## Intent

Replace the multi-script shell pipeline with a single Go binary (`headless-macs`) that is
menu-driven, runs interactively, and performs all the same work in the correct order.
The shell scripts are retired; `config.json` format is preserved unchanged.

---

## Scope

| Item | In | Out |
|---|---|---|
| Precheck logic | ✓ | |
| Storage volume setup | ✓ | |
| System baseline (setup.sh) | ✓ | |
| Tool installation (install-tools.sh) | ✓ | |
| Health verify | ✓ | |
| Restore / undo | ✓ | |
| Update tools | ✓ | |
| config.json format change | | ✗ |
| Legacy scripts/ family (manage.sh, scripts/*.sh) | | ✗ |
| Deprecated scripts (pmset_to_ollama.sh etc.) | | ✗ |
| Non-Apple-Silicon support | | ✗ |
| New inference tools beyond current five | | ✗ |

---

## Resolved design decisions

| Question | Decision |
|---|---|
| Color scheme | Dark-only — no terminal theme adaptation |
| Log pane split | 40% progress / 60% log — fixed |
| Config editing | In scope for Phase 6 — first step before any execution; local copy at `~/.headless_macs/config.json` |

---

## Technology choices

| Concern | Choice | Rationale |
|---|---|---|
| Language | Go 1.22+ | Single static binary, no runtime deps, arm64 Darwin cross-compile trivial |
| TUI framework | [Bubble Tea](https://github.com/charmbracelet/bubbletea) | Component model, well-maintained, handles keyboard + resize cleanly |
| TUI styling | [Lip Gloss](https://github.com/charmbracelet/lipgloss) | Pairs with Bubble Tea; consistent color/border primitives |
| Config parsing | `encoding/json` + struct tags | No new dep; mirrors jq reads |
| Privileged ops | `os/exec` calling `sudo` | Binary is run as root via `sudo headless-macs`; individual commands still use sudo for auditability |
| Output log | `io.MultiWriter` → file + stdout | Matches current `tee` pattern; preserves `[SET]`/`[SKIP]`/etc. prefixes |
| Config storage | `~/.headless_macs/config.json` | User-local copy; repo `config.json` is the shipped template only |
| Build | `go build -o headless-macs ./cmd/headless-macs` | Single target, Makefile wrapper |

---

## Phases

### Phase 6A — Go project scaffold
**Goal:** Compilable skeleton with no logic yet.

- [ ] Create `go.mod` (`module github.com/mediumroast/headless-macs`) at repo root
- [ ] Add Bubble Tea + Lip Gloss as dependencies (`go get`)
- [ ] Create directory layout:
  ```
  cmd/headless-macs/main.go    — entry point, arg parsing, sudo check
  internal/config/config.go    — config.json loader → Go structs
  internal/tui/                — all Bubble Tea models
  internal/ops/                — business logic (one file per script)
  internal/log/logger.go       — [SET]/[SKIP]/[WARN]/[FAIL] helpers + tee to file
  Makefile                     — build, install, lint targets
  ```
- [ ] `main.go` prints version and exits cleanly — proves the build works
- [ ] `Makefile` has `build`, `install` (`/usr/local/bin/headless-macs`), `clean`

**Files touched:** `go.mod`, `go.sum`, `Makefile`, `cmd/headless-macs/main.go`, `internal/` tree (stubs)

---

### Phase 6B — Config layer and bootstrap
**Goal:** Read config into typed Go structs; on first run, copy the template to the user's local config and open the editor.

- [ ] Define Go structs mirroring the full `config.json` schema (tools, storage, system, network)
- [ ] Config search order: `~/.headless_macs/config.json` → repo `config.json` (template, read-only reference)
- [ ] **First-run bootstrap:** if `~/.headless_macs/config.json` does not exist, create `~/.headless_macs/`, copy repo `config.json` there, then immediately open the config editor screen (Phase 6B.1) before proceeding
- [ ] Config is passed as a value through the call stack — no global
- [ ] Save helper: write updated struct back to `~/.headless_macs/config.json` atomically (write temp file, rename)
- [ ] Unit test: load the repo's own `config.json`, assert key field values

**Files touched:** `internal/config/config.go`, `internal/config/config_test.go`

---

### Phase 6B.1 — Config editor TUI screen
**Goal:** Allow the user to review and edit all config values before any script runs; this is the mandatory first screen on every launch.

- [ ] Structured form rendered with Bubble Tea: grouped sections matching `config.json` (Tools, Storage, System, Network)
- [ ] Each field shows current value; editable fields use a text input or toggle widget
- [ ] Tool `enabled` flags rendered as `[✓] Ollama  [ ] Rapid-MLX  [ ] mlx-lm …` toggles
- [ ] Paths, ports, and string values use single-line text inputs
- [ ] Boolean system flags (disable_spotlight, etc.) use toggles
- [ ] `S` to save → writes `~/.headless_macs/config.json` → returns to main menu
- [ ] `R` to reset → reverts unsaved edits to last-saved state
- [ ] `Esc` / `q` to discard and return to main menu without saving
- [ ] Status bar at bottom: "Config: ~/.headless_macs/config.json  [modified]" or "[saved]"
- [ ] Dark-only color scheme: section headers in amber, field labels in grey, values in white, modified fields highlighted in cyan

**Files touched:** `internal/tui/config_editor.go`

---

### Phase 6C — Logger
**Goal:** Replace script `echo "[SET] ..."` with structured Go helpers; keep same output format.

- [ ] `log.Set(msg)`, `log.Skip(msg)`, `log.Warn(msg)`, `log.Fail(msg)`, `log.Pass(msg)`,
      `log.Info(msg)`, `log.OK(msg)`, `log.Notice(msg)` — same prefix table as CLAUDE.md
- [ ] On init: create `/var/log/mac-llm-setup/<command>-<timestamp>.log`, tee all output there
- [ ] TUI mode: logger writes to an in-TUI log pane (scrollable) instead of stdout

**Files touched:** `internal/log/logger.go`

---

### Phase 6D — Precheck module
**Goal:** Port `precheck.sh` logic to Go; write `/tmp/mac-llm-precheck.json`.

- [ ] Hardware checks: `uname -m`, `hw.model`, `hw.memsize`, macOS version
- [ ] State checks: SIP, FileVault, auto-login, Xcode CLT, Homebrew, Python, port availability
- [ ] Storage probe: detect external volume by label, compute free space
- [ ] RAM tier classification (same thresholds as current shell script)
- [ ] Write `/tmp/mac-llm-precheck.json` in same schema as current script
- [ ] Return structured `PrecheckResult` used by downstream modules

**Files touched:** `internal/ops/precheck.go`

---

### Phase 6E — System baseline module
**Goal:** Port `setup.sh` to Go.

- [ ] pmset application (sleep, disksleep, standby, womp, tcpkeepalive, autorestart)
- [ ] caffeinate LaunchDaemon (write-once idempotent)
- [ ] sysctl-tuning LaunchDaemon
- [ ] maxfiles LaunchDaemon
- [ ] pmset-heal LaunchDaemon
- [ ] Service suppression (Spotlight, telemetry, Siri, iCloud, etc.) with SIP gate
- [ ] SSH enable via `launchctl enable/kickstart`; sshd drop-in hardening
- [ ] All idempotency guards preserved (`[SKIP]` when already correct)

**Files touched:** `internal/ops/baseline.go`

---

### Phase 6F — Storage module
**Goal:** Port `storage-volume.sh` to Go.

- [ ] Skip entirely if `storage.use_external_volume` is false
- [ ] Volume detection, mount, ownership enable
- [ ] Directory layout creation (`ollama/`, `rapid-mlx/`, `mlx-lm/`, etc.)
- [ ] Spotlight exclusion on volume
- [ ] `/Library` symlink wiring
- [ ] fstab entry
- [ ] `com.llm-server.storage-mount` LaunchDaemon

**Files touched:** `internal/ops/storage.go`

---

### Phase 6G — Tool installation module
**Goal:** Port `install-tools.sh` to Go; each tool is a sub-function, same install pattern.

- [ ] `_llmserver` account creation (idempotent, self-healing)
- [ ] Ollama: install, menu bar kill, plist write, auto-tune (RAM → MAX_LOADED_MODELS / NUM_PARALLEL / MAX_CONTEXT)
- [ ] Rapid-MLX: install, cache dir setup, plist write (HF_HOME, HUGGINGFACE_HUB_CACHE)
- [ ] mlx-lm: install, plist write
- [ ] Infinity: install, plist write
- [ ] Exo: install, plist write
- [ ] `load_daemon` helper (bootout then bootstrap)
- [ ] Endpoint health check after each tool

**Files touched:** `internal/ops/tools.go`

---

### Phase 6H — Verify module
**Goal:** Port `verify.sh` to Go; return structured results for TUI display.

- [ ] `VerifyResult` struct with per-check Pass/Fail/Warn/Skip + message
- [ ] All current check sections: SYSTEM, NETWORK, STORAGE, OLLAMA, RAPID-MLX, MLX-LM, INFINITY, EXO, MEMORY
- [ ] Exit code logic (0/1/2) preserved for non-TUI invocation
- [ ] HTTP endpoint checks with configurable timeout

**Files touched:** `internal/ops/verify.go`

---

### Phase 6I — Restore and update modules
**Goal:** Port `restore.sh` and `update-tools.sh` to Go.

- [ ] Restore: bootout all LaunchDaemons by label, pmset reset, re-enable suppressed services, remove sshd drop-in, remove symlinks
- [ ] Restore prompts for confirmation before making changes (works in both TUI and non-TUI mode)
- [ ] Update: per-tool update logic (Ollama installer re-run, pip upgrade for Python tools)

**Files touched:** `internal/ops/restore.go`, `internal/ops/update.go`

---

### Phase 6J — TUI layer
**Goal:** Bubble Tea application wrapping all modules with a menu-driven interface.

- [ ] **Config editor opens automatically on every launch** (Phase 6B.1); user saves or discards before reaching the main menu
- [ ] **Main menu** — vertical list: Edit Config · Precheck · Storage Setup · System Baseline · Install Tools · Verify · Restore · Update · Quit
- [ ] **Run screen** — split pane: progress list (checks ticking off with `[PASS]`/`[SKIP]`/`[FAIL]` color) on left; scrollable log on right
- [ ] **Config review screen** — read-only display of active `config.json` values before any destructive step
- [ ] **Confirm screen** — shown before Restore and Install; requires explicit `y` keypress
- [ ] **Verify report screen** — color-coded pass/warn/fail table; press `q` to return to menu
- [ ] **Keyboard:** `↑↓` navigate, `Enter` select, `q`/`Ctrl-C` quit/back, `?` help overlay
- [ ] Non-interactive mode: `headless-macs --run all` skips TUI and runs full pipeline headlessly (for CI / scripted provisioning)

**Files touched:** `internal/tui/` (menu.go, run.go, verify.go, config.go, confirm.go, styles.go)

---

### Phase 6K — Shell script retirement and docs
**Goal:** Formally retire the shell scripts and update all documentation.

- [ ] Add deprecation notice to top of each retired script: `precheck.sh`, `setup.sh`, `install-tools.sh`, `verify.sh`, `restore.sh`, `update-tools.sh`, `storage-volume.sh`
- [ ] Scripts remain in repo but print a one-line notice and exit 0 (no silent removal — people may have them in cron)
- [ ] Update `README.md`: replace Quick Start shell commands with `sudo headless-macs`
- [ ] Update `CLAUDE.md`: note Phase 6 binary, update script architecture section
- [ ] Update `CHANGELOG.md` with v2.0.0 entry (breaking: script interface replaced by binary)
- [ ] Version bump: **v2.0.0** (shell interface replaced — major)

**Files touched:** all retired `.sh` files (deprecation header), `README.md`, `CLAUDE.md`, `CHANGELOG.md`

---

## Files-touched summary

| File / Directory | Change |
|---|---|
| `go.mod`, `go.sum` | New |
| `Makefile` | New |
| `cmd/headless-macs/main.go` | New |
| `internal/config/config.go` | New |
| `internal/log/logger.go` | New |
| `internal/ops/precheck.go` | New |
| `internal/ops/baseline.go` | New |
| `internal/ops/storage.go` | New |
| `internal/ops/tools.go` | New |
| `internal/ops/verify.go` | New |
| `internal/ops/restore.go` | New |
| `internal/ops/update.go` | New |
| `internal/tui/*.go` | New |
| `precheck.sh` → `setup.sh` → `install-tools.sh` → `verify.sh` → `restore.sh` → `update-tools.sh` → `storage-volume.sh` | Deprecation header added |
| `README.md`, `CLAUDE.md`, `CHANGELOG.md` | Updated |

---

## Build and install

```bash
make build                       # produces ./headless-macs
sudo make install                # copies to /usr/local/bin/headless-macs
sudo headless-macs               # launches TUI
sudo headless-macs --run all     # headless full pipeline (no TUI)
sudo headless-macs verify        # headless verify only
```

---

## Open questions

All resolved. See **Resolved design decisions** above.
