# CLAUDE.md — headless-macs project guidance

This file is read by Claude Code at the start of every session. It describes what the project is, how it is structured, all coding conventions, and what Claude must do at the end of major sessions. Read this in full before writing any code.

---

## Project intent

`headless-macs` configures Apple Silicon Macs as production-grade, unattended LLM inference nodes. The design goals are:

- **Idempotent** — every operation can be run multiple times; it skips settings that are already correct.
- **Config-driven** — one `config.json` (stored at `~/.headless_macs/config.json`) controls all operations. No hardcoded values.
- **Auditable** — Precheck (read-only) runs before anything; Verify (read-only) runs after. Changes are bracketed by audit tools.
- **Reversible** — Restore undoes everything System Baseline and Install Tools did.
- **Minimal surface** — the toolset does not install anything beyond what is needed for LLM inference.

---

## Architecture (v2 — Go binary)

The primary interface is the `headless-macs` Go binary (`cmd/headless-macs/main.go`). All operations that were formerly shell scripts are implemented as Go packages under `internal/ops/`:

```
internal/ops/precheck.go    RunPrecheck()       — read-only audit
internal/ops/baseline.go    RunBaseline()       — system baseline (pmset, sysctl, services, SSH)
internal/ops/storage.go     RunStorage()        — external volume setup
internal/ops/tools.go       RunTools()          — serving stack installation
internal/ops/verify.go      RunVerify()         — health check
internal/ops/restore.go     RunRestore()        — undo all changes
internal/ops/update.go      RunUpdateTools()    — in-place binary upgrade
```

The TUI (`internal/tui/`) presents these as screens in a Bubble Tea app. All ops functions are Go-native — they do not shell out to the bash scripts.

### Shell scripts (deprecated in v2 — do not modify)

The v1 bash pipeline has been moved to `deprecated/`. The scripts still work but are superseded by the Go binary and carry a deprecation notice in their headers. Do not add features to or modify them:

```
deprecated/precheck.sh        → RunPrecheck()
deprecated/setup.sh           → RunBaseline()
deprecated/install-tools.sh   → RunTools()
deprecated/storage-volume.sh  → RunStorage()
deprecated/verify.sh          → RunVerify()
deprecated/restore.sh         → RunRestore()
deprecated/update-tools.sh    → RunUpdateTools()
deprecated/manage.sh          Phase 2 orchestrator
deprecated/scripts/           Phase 2 per-component scripts
deprecated/lib/               Shared helpers for scripts/
deprecated/pmset_to_ollama.sh [DEPRECATED]
deprecated/setup_colima.sh    [DEPRECATED]
```

---

## Output conventions

There are two distinct output styles in this repo. Never mix them within a single script.

### Main pipeline scripts (setup.sh, install-tools.sh, verify.sh, precheck.sh, restore.sh, update-tools.sh, storage-volume.sh)

Plain text, bracket-prefixed, no color codes. All output is tee'd to a timestamped log file.

| Prefix | Meaning |
|---|---|
| `[SET]` | A change was applied |
| `[SKIP]` | Already correct — nothing done |
| `[WARN]` | Non-fatal issue; user should investigate |
| `[FAIL]` | Fatal check failure (verify.sh only) |
| `[PASS]` | Check succeeded (verify.sh only) |
| `[OK]` | Post-install endpoint/API confirmed responding |
| `[INFO]` | Informational, no action required |
| `[NOTICE]` | Important but not a warning (e.g. hardware-specific note) |
| `[BACKUP]` | A file was backed up before modification |
| `[BLOCKER]` | Hard blocker found (precheck.sh only) |
| `[SKIP-SIP]` | Change skipped because SIP is enabled |
| `[CONFIG]` | Config file loaded |
| `[SNAPSHOT]` | Pre-change state saved |

Indented continuation lines use 7 spaces (to align with the prefix width): `"       detail here"`.

### Legacy scripts (scripts/*.sh, manage.sh)

Use colored helpers from `lib/common.sh`: `print_status` (green ✓), `print_error` (red ✗), `print_warning` (yellow !), `print_info` (blue ℹ). These scripts are not tee-logged.

---

## config.json rules

- All config reads use `jq -r '.section.key // default'` with a sensible fallback — scripts must work with a minimal or missing config.
- New tool config goes under `"tools"`. New system/OS config goes under `"system"`. Network binding goes under `"network"`.
- The `"localhost_only"` flag in `"network"` is checked in `install-tools.sh` and overrides per-tool host values: when true, all services bind `127.0.0.1` regardless of what the tool's individual `host` field says.
- Boolean config values: always compare with `== "true"` (jq outputs the string `"true"`, not a bash boolean).
- Never read config with `source` or `eval`. Always `jq`.

---

## LaunchDaemon conventions

### Naming

| Prefix | Use |
|---|---|
| `com.llm-server.*` | Infrastructure daemons managed by `setup.sh` (caffeinate, sysctl-tuning, maxfiles, pmset-heal) |
| `com.<toolname>.server` | Serving-tool daemons managed by `install-tools.sh` (com.ollama.server, com.rapid-mlx.server, etc.) |

### Service account

Serving-tool daemons run as `_llmserver` (not root). `install-tools.sh` creates this account on first run:
- UID/GID auto-assigned from range 400–499
- Home: `/Library/LLMServer`
- Shell: `/usr/bin/false`
- Hidden from login screen (`IsHidden 1`)

Log and model directories are owned `_llmserver:_llmserver` so the daemon can write them.

### Required plist keys for all serving-tool daemons

```xml
<key>UserName</key><string>_llmserver</string>
<key>WorkingDirectory</key><string>/tmp</string>
<key>EnvironmentVariables</key>
<dict>
  <!-- HOME is required: without it Ollama and mlx-lm panic at startup -->
  <key>HOME</key><string>/Library/LLMServer</string>
  <key>PATH</key><string>/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin</string>
</dict>
<key>RunAtLoad</key><true/>
<key>KeepAlive</key><true/>
<key>StandardOutPath</key><string>/var/log/<toolname>/stdout.log</string>
<key>StandardErrorPath</key><string>/var/log/<toolname>/stderr.log</string>
```

### File permissions

All plists: `sudo chown root:wheel <plist>` + `sudo chmod 644 <plist>`.

### Loading / unloading

Always use `bootstrap`/`bootout`. Never use the deprecated `load`/`unload`.

```bash
sudo launchctl bootstrap system "$PLIST"   # install and start
sudo launchctl bootout   system "$PLIST"   # stop and uninstall
```

### Idempotency guard for infrastructure daemons

```bash
if [[ ! -f "$PLIST_PATH" ]]; then
  # write plist, chown, chmod, bootstrap
else
  echo "[SKIP] <label> already installed"
fi
```

### Log directories

Create before writing the plist: `sudo mkdir -p /var/log/<toolname> && sudo chown root:wheel /var/log/<toolname>`.

---

## verify.sh contract

`verify.sh` is the health check companion to `setup.sh` and `install-tools.sh`. Every feature added to either of those scripts **must** have a corresponding check in `verify.sh`.

### Exit codes

| Code | Meaning |
|---|---|
| `0` | All checks passed |
| `1` | One or more `[FAIL]` results |
| `2` | Warnings only, no failures |

### Helper functions (defined at top of verify.sh)

```bash
_pass()  # [PASS] — prints green pass, no counter
_fail()  # [FAIL] — prints red fail, increments FAILURES
_warn()  # [WARN] — prints yellow warn, increments WARNINGS
_skip()  # [SKIP] — informational skip
_info()  # indented continuation line (no prefix)
```

### Check helpers

```bash
check_pmset  "key" "expected_value"   # reads pmset -g
check_sysctl "key" "expected_value"   # reads sysctl -n; warns (not fails) since reboot may be needed
check_daemon "label"                  # checks launchctl state = running
check_http   "name" "url" "pattern"   # curl with grep
```

### Sections

Sections are separated by `echo "--- SECTION_NAME ---"`. Current sections: SYSTEM, NETWORK, OLLAMA, RAPID-MLX, MLX-LM, INFINITY, EXO, MEMORY.

---

## SIP-gating pattern

`launchctl disable` on system services requires SIP to be off. Always gate these calls:

```bash
SIP_ENABLED=true
echo "$SIP_RAW" | grep -q "disabled" && SIP_ENABLED=false

disable_service() {
  local domain="$1"
  if [[ "$SIP_ENABLED" == false ]]; then
    sudo launchctl disable "$domain" 2>/dev/null || true
    echo "[SET]  disabled $domain"
  else
    echo "[SKIP-SIP] $domain (requires SIP off for persistence)"
  fi
}
```

SIP detection runs once near the top of `setup.sh` and `verify.sh`. Do not re-detect it mid-script.

---

## Tool install pattern (install-tools.sh)

Every tool section in `install-tools.sh` follows this sequence:

1. Guard: `if [[ "$(echo "$CONFIG" | jq -r '.tools.<name>.enabled')" == "true" ]]; then`
2. Install binary (skip-if-present check on `command -v`)
3. Stop any conflicting process or service
4. Create log directory with `root:wheel` ownership
5. Resolve binary path and read all config values
6. Write the LaunchDaemon plist (always overwritten — plist reflects current config)
7. `sudo chown root:wheel` + `sudo chmod 644` on plist
8. `load_daemon "$PLIST"` (bootout then bootstrap)
9. `sleep 3` then `check_endpoint` call (non-fatal `|| true`)

The plist is always re-written even if it exists, because config values may have changed. This is different from infrastructure daemons (caffeinate, sysctl-tuning etc.) which are write-once-idempotent.

---

## Hardware and platform guards

Every main pipeline script starts with these two guards before doing anything else:

```bash
ARCH=$(uname -m)
if [[ "$ARCH" != "arm64" ]]; then
  echo "ERROR: This toolset requires Apple Silicon (arm64). Detected: $ARCH"
  exit 1
fi
if [[ "$(uname -s)" != "Darwin" ]]; then
  echo "ERROR: macOS required."
  exit 1
fi
```

---

## Sudo keepalive pattern

Scripts requiring sudo prompt once at the top and keep the ticket alive in the background:

```bash
sudo -v
while true; do sudo -n true; sleep 60; kill -0 "$$" || exit; done 2>/dev/null &
SUDO_PID=$!
trap 'kill $SUDO_PID 2>/dev/null' EXIT
```

---

## Logging pattern

Main pipeline scripts tee all output to a timestamped log under `/var/log/mac-llm-setup/`:

```bash
LOG_DIR="/var/log/mac-llm-setup"
sudo mkdir -p "$LOG_DIR"
LOG_FILE="$LOG_DIR/<scriptname>-$(date +%Y%m%d-%H%M%S).log"
exec > >(tee -a "$LOG_FILE") 2>&1
echo "=== <scriptname> started at $(date) ==="
```

Always print the log path at the end: `echo "Log written to: $LOG_FILE"`.

---

## Known non-obvious constraints

These are real gotchas. Do not "fix" or remove these guards — they exist because the obvious approach breaks:

- **`/etc/sysctl.conf` is ignored at boot since Catalina.** Persist sysctl settings with a `RunAtLoad` LaunchDaemon instead.
- **`net.inet.tcp.rfc1323` was removed in El Capitan.** Do not add it to the sysctl tuning set.
- **`serverperfmode` is Intel-only.** It silently breaks on Apple Silicon. Do not add it.
- **`powermode` (0/1/2) is Intel-only.** Apple Silicon has no equivalent pmset key — `highpowermode` does not exist on macOS 26 Tahoe either. High Performance Mode must be set via System Settings → Battery → Options → High Power Mode. Do not add any powermode variant to setup.sh.
- **`systemsetup -setremotelogin` is broken on macOS 26 Tahoe.** Use `launchctl enable system/com.openssh.sshd && launchctl kickstart -k system/com.openssh.sshd` as the primary method, with `systemsetup` as a fallback.
- **`launchctl load` / `launchctl unload` are deprecated in macOS 15+.** They may silently fail or misbehave. Use `bootstrap`/`bootout` exclusively.
- **`HOME` must be set in every daemon plist.** Ollama, mlx-lm, and Infinity all panic with `panic: $HOME is not defined` if HOME is absent. Set it to `/Library/LLMServer` (the `_llmserver` service account home). This is not fixable at the app level — it must be in the plist.
- **The Ollama installer re-adds the login item.** After running `ollama.com/install.sh`, the login item must be explicitly removed again with `osascript`.
- **`PasswordAuthentication no` without `authorized_keys` locks out a headless box.** Always check for a key file before writing that directive.
- **macOS updates reset pmset values and can overwrite `/etc/ssh/sshd_config`.** The sysctl-tuning and pmset-heal LaunchDaemons and the sshd drop-in file in `sshd_config.d/` were specifically designed to survive this.
- **`mdutil` must be run on the model directory explicitly**, not just on `/`. Spotlight will otherwise index model files.
- **`defaults write` auto-login is broken in macOS 15 Sequoia+.** Use `sysadminctl -autologin set` or System Settings.

---

## Planning convention

For any non-trivial change (touching more than 2 files, or requiring architectural decisions):

1. Create a `PHASE_N_PLAN.md` in the repo root before writing any code.
2. The plan must include: a scope decision table (in/out), one section per phase with exact file changes, and a files-touched summary.
3. Get user approval on the plan before implementing.
4. Commit the plan document alongside the implementation PR.

Phase numbering follows the project history: Phase 1 (initial), 2 (production rewrite), 3 (LiteLLM, planned), 4 (Modelfiles), 5 (security hardening), 6 (Go rewrite). Next is Phase 7.

---

## End-of-session checklist

After every session that produces a PR or significant changes, complete these steps before closing out.

### 1. Update CHANGELOG.md

Edit `CHANGELOG.md`. If the PR is not yet merged, add items to `[Unreleased]`. When a PR merges, move them into a new versioned section.

Version bump rules:
- **Patch** (`x.y.Z`): bug fixes, doc corrections, typo fixes.
- **Minor** (`x.Y.0`): new features, new scripts, new LaunchDaemons, new config keys — backward-compatible.
- **Major** (`X.0.0`): breaking `config.json` schema changes, script renames, removal of supported tools.

Each version section must contain:
- Date (YYYY-MM-DD)
- One-sentence phase description
- `### Fixed`, `### Changed`, `### Added` subsections (omit empty ones)
- `### PR` with a markdown link to the GitHub PR

Update the comparison links at the bottom of `CHANGELOG.md` to include the new version.

### 2. Create and push a git tag (after PR merges to main)

```bash
git checkout main && git pull

git tag -a v1.2.0 -m "Phase 5: Security hardening and operational improvements"
git push origin v1.2.0
```

The tag name must exactly match the version in `CHANGELOG.md`.

To create a full GitHub Release with notes drawn from the changelog:

```bash
gh release create v1.2.0 \
  --title "v1.2.0 — Phase 5: Security hardening" \
  --notes-file <(sed -n '/## \[1\.2\.0\]/,/## \[1\.1\.0\]/p' CHANGELOG.md | head -n -1)
```

### 3. Verify

```bash
git tag --list | sort
gh release list
```

---

## Branch and PR conventions

- Branch names are auto-generated by the worktree: `claude/<adjective>-<surname>-<hash>`.
- PR titles: `Phase N: <short description>` or `Fix: <what was broken>`.
- PR body: Summary bullets (drawn from CHANGELOG entry) + Test plan checklist.
- Every PR must have its `PHASE_N_PLAN.md` committed in the same branch.

---

## Versioning history

| Version | Date | Description |
|---|---|---|
| v0.1.0 | 2025-12-27 | Initial pmset + Ollama LaunchDaemon script |
| v0.2.0 | 2026-02-01 | Modular script refactor (manage.sh, scripts/) |
| v1.0.0 | 2026-06-07 | Phase 2: Production rewrite — precheck/setup/install-tools/verify/restore (PR #1) |
| v1.1.0 | 2026-06-10 | Phase 4: Modelfile system, KV cache model, Zoo Code (PR #2) |
| v1.2.0 | 2026-06-11 | Phase 5: Security hardening, operational improvements, Ollama lifecycle (PR #3) |
| v2.0.0 | 2026-08-15 | Phase 6: Go rewrite — TUI binary replaces shell pipeline (PR #4) |
