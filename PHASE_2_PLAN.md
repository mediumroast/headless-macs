# Phase 2 Plan — Mac LLM Optimizer

**Built from:** `PHASE_1_PLAN.md` spec vs. current Phase 1 implementation  
**Date:** June 2026  
**Branch:** `claude/hopeful-booth-2506ee`

---

## 1. Where We Are (Phase 1 Delivered)

The Phase 1 refactoring defined in `PLANNING.md` is **complete**:

| Deliverable | Status |
|---|---|
| `lib/common.sh` — shared utilities | ✅ Done |
| `scripts/power_management.sh` — pmset wrapper | ✅ Done |
| `scripts/homebrew_setup.sh` — Homebrew installer | ✅ Done |
| `scripts/ollama_setup.sh` — Ollama install + launchd | ✅ Done |
| `scripts/colima_setup.sh` — Colima + Docker | ✅ Done |
| `setup.sh` — interactive menu / CLI orchestrator | ✅ Done |
| `pmset_to_ollama.sh` — deprecated with notice | ✅ Done |
| `setup_colima.sh` — deprecated with notice | ✅ Done |
| `README.md` — updated docs | ✅ Done |

---

## 2. What `PHASE_1_PLAN.md` Specifies (The Target)

`PHASE_1_PLAN.md` defines a new, more ambitious architecture called **Mac LLM Optimizer**. It is a ground-up redesign that treats the existing Phase 1 scripts as a starting point, not the final state. The key differences:

| Dimension | Phase 1 (current) | PHASE_1_PLAN.md target |
|---|---|---|
| Configuration | Interactive prompts | `config.json` as single control plane |
| Scripts | 4 component scripts + orchestrator | 6 purpose-built scripts + config + docs |
| Tools covered | Ollama, Colima | Ollama, Rapid-MLX, mlx-lm, Infinity, Exo |
| Serving stack | Colima/Docker oriented | LLM inference server oriented |
| Idempotency | None | Every change checks current state first |
| Logging | None | `/var/log/mac-llm-setup/<script>-<timestamp>.log` |
| launchctl API | `load`/`unload` (deprecated) | `bootstrap`/`bootout` (correct for macOS 15+) |
| HOME in plists | Missing | `HOME=/var/root` (non-negotiable — prevents panic) |
| RAM auto-tuning | None (hardcoded defaults) | RAM-tiered table → auto-sets Ollama env vars |
| System hardening | pmset only | pmset + network sysctl + service suppression + SSH |
| Precheck | None | Full read-only audit before any change |
| Recovery | None | `restore.sh` from pre-change snapshot |
| Health check | Basic status in each script | Structured pass/fail `verify.sh` with exit codes |
| External storage | None | `storage-volume.sh` + symlink wiring |

---

## 3. Gap Analysis — Specific Missing Pieces

### 3.1 Missing Files (Must Be Created)

| File | Spec Section | Purpose |
|---|---|---|
| `config.json` | §0.4 | Single control plane for all tunable parameters |
| `precheck.sh` | §9 | Read-only audit: hardware, SIP, FileVault, storage, ports |
| `install-tools.sh` | §2 | Install Ollama, Rapid-MLX, mlx-lm, Infinity, Exo |
| `verify.sh` | §3 | Structured pass/fail health report |
| `restore.sh` | §4 | Undo all changes via pre-change snapshot |
| `storage-volume.sh` | §10 | External volume setup + symlink wiring |
| `docs/tool-comparison.md` | §6 | When to use Ollama vs Rapid-MLX vs mlx-lm vs Infinity vs Exo |
| `docs/ram-sizing.md` | §6 | Model size / quantization / RAM reference table |
| `docs/storage-guide.md` | §6 | External volume guide: APFS vs HFS+, symlink map |
| `docs/known-issues.md` | §7 + §10.10 | Workaround table (HOME panic, SIP, FileVault, MacBook lid, etc.) |

### 3.2 Critical Bugs in Existing Scripts

These are correctness issues that must be fixed regardless of new feature work:

#### Bug 1: Deprecated `launchctl load/unload`
**Files:** `scripts/ollama_setup.sh` (lines 441, 443, 505, 565, 605, 607)  
**Fix:** Replace all `launchctl load <plist>` → `launchctl bootstrap system <plist>` and `launchctl unload <plist>` → `launchctl bootout system <plist>`  
**Why:** `load`/`unload` are deprecated in macOS 26 Tahoe and already behave incorrectly in macOS 15 Sequoia.

#### Bug 2: Missing `HOME=/var/root` in Ollama plist
**File:** `scripts/ollama_setup.sh` — `create_launchd_service()` function  
**Fix:** Add `<key>HOME</key><string>/var/root</string>` to the `EnvironmentVariables` dict in the plist template  
**Why:** Ollama panics with `panic: $HOME is not defined` when running as a LaunchDaemon without this. This is a known issue documented in `PHASE_1_PLAN.md` §7.

#### Bug 3: Missing `UserName` key in Ollama plist
**File:** `scripts/ollama_setup.sh`  
**Fix:** Add `<key>UserName</key><string>root</string>` to the plist  
**Why:** Required for system-level daemon ownership.

#### Bug 4: No idempotency — pmset applied unconditionally
**File:** `scripts/power_management.sh` — `apply_headless_settings()`  
**Fix:** Wrap each `pmset` call with a current-state check (see §0.2 pattern in PHASE_1_PLAN.md)  
**Why:** Repeated runs should be safe and report `[SKIP]` instead of noisily reapplying unchanged settings.

#### Bug 5: Ollama install uses wrong method
**File:** `scripts/ollama_setup.sh` — `install_ollama()`  
**Current:** Downloads `Ollama-darwin.zip` (the .app bundle)  
**Fix:** Use the recommended CLI install: `curl -fsSL https://ollama.com/install.sh | sh`  
**Why:** The `.app` bundle conflicts with the LaunchDaemon approach. The CLI installer places the binary at `/usr/local/bin/ollama` directly, which is what the LaunchDaemon plist expects. The spec also requires killing any Ollama login item after install.

### 3.3 Missing Features in Existing `setup.sh` (System Baseline Role)

The spec defines `setup.sh` as the **system baseline** script (not the orchestrator). The current `setup.sh` is an orchestrator. The naming needs resolving:

**Decision:** Rename current `setup.sh` → `manage.sh` and create a new `setup.sh` that implements the system baseline. This preserves the CLI users expect (`./manage.sh install all`) while adding the spec-required `./setup.sh`.

The new `setup.sh` (system baseline) must implement:

| Feature | Spec Section | Currently Missing |
|---|---|---|
| macOS version detection + `OS_MAJOR` | §0.1 | ✅ Missing |
| `HW_MODEL` detection | §0.1 | ✅ Missing |
| Centralized logging to `/var/log/mac-llm-setup/` | §0.1 | ✅ Missing |
| Idempotency wrapper for all pmset calls | §0.2 | ✅ Missing |
| SIP check (`csrutil status`) | §0.3 | ✅ Missing |
| `disablesleep 1` (macOS 26 primary mechanism) | §1.1 | ✅ Missing |
| `powermode 2` (High Performance) from config | §1.1 | ✅ Missing |
| MacBook detection + lid-close warning | §1.1 | ✅ Missing |
| `caffeinate -dimsu` LaunchDaemon | §1.1 | ✅ Missing |
| Network sysctl tuning via `/etc/sysctl.conf` | §1.2 | ✅ Missing |
| Pre-change service state snapshot | §1.3 | ✅ Missing |
| Spotlight suppression (`mdutil -a -i off`) | §1.3 | ✅ Missing |
| Service suppression (telemetry, Siri, iCloud, Biome) | §1.3 | ✅ Missing |
| `defaults write` for AirDrop, App Nap, animations | §1.3 | ✅ Missing |
| Software update disable | §1.3 | ✅ Missing |
| Time Machine disable + exclusions | §1.3 | ✅ Missing |
| SSH hardening (`sshd_config`) | §1.4 | ✅ Missing |
| Xcode CLT headless install via `softwareupdate` | §1.5 | ✅ Missing |

### 3.4 Missing Serving Tools in `install-tools.sh`

The new `install-tools.sh` replaces the per-tool setup functions with a unified, config-driven installer. Current scripts only cover Ollama (and Colima which is a container runtime, not a serving tool).

| Tool | Spec Section | Notes |
|---|---|---|
| **Ollama** | §2.2 | Needs RAM auto-tuning, `HOME=/var/root`, corrected plist, models dir setup |
| **Rapid-MLX** | §2.3 | New — brew tap install, LaunchDaemon, cache dir, first-start model download awareness |
| **mlx-lm** | §2.4 | New — pip install, LaunchDaemon, only starts if model configured |
| **Infinity** | §2.5 | New — pip install, LaunchDaemon with `--device mps`, embedding + rerank ports |
| **Exo** | §2.6 | New — LaunchAgent (not Daemon), requires auto-login, Tailscale/Bonjour discovery |

### 3.5 README Update Required

Current `README.md` reflects Phase 1. It needs a full rewrite to document:
- The new run-order: `precheck.sh` → `storage-volume.sh` (optional) → `setup.sh` → `install-tools.sh` → `verify.sh`
- Tool selection guide (when to use Ollama vs Rapid-MLX vs mlx-lm vs Infinity vs Exo)
- Hardware RAM reference table
- Updated file structure

---

## 4. Implementation Plan

Work is ordered by dependency and risk. Bug fixes come first (they're correct regardless of new work), then new scripts, then docs.

### Phase 2.0 — Critical Bug Fixes (Do First)
*These are correctness issues in the current codebase.*

1. Fix `launchctl load/unload` → `bootstrap/bootout` in `scripts/ollama_setup.sh`
2. Add `HOME=/var/root` and `UserName root` to Ollama plist template
3. Fix Ollama install method: use `curl -fsSL https://ollama.com/install.sh | sh`
4. Kill Ollama login item after install
5. Add idempotency to `scripts/power_management.sh` pmset calls
6. Move Ollama logs from `/tmp/` to `/var/log/ollama/` (create dir with correct perms)

### Phase 2.1 — Config Foundation

7. Create `config.json` with all defaults from §0.4  
8. Add `jq` dependency check to `lib/common.sh`  
9. Add `CONFIG` loading pattern to `lib/common.sh` (reusable across all new scripts)

### Phase 2.2 — Precheck Script

10. Create `precheck.sh` — implements all checks from §9.2:
    - Hardware identity (chip, arch, RAM, model, form factor, CPU cores)
    - Model capability table (RAM-based: what models can run)
    - macOS + security state (SIP, FileVault, auto-login, Xcode CLT)
    - Tool prerequisites (brew, python3, pip3, jq, ollama, rapid-mlx, curl, git)
    - Network and ports (IPs, port availability for all 5 tools, SSH, firewall)
    - Storage (boot volume free space, external volume detection, config label matching)
    - Current power state (pmset values + change-needed flags)
    - Readiness summary (blockers vs warnings, exit codes 0/1/2)
    - JSON output to `/tmp/mac-llm-precheck.json` for downstream scripts

### Phase 2.3 — System Baseline Rename + Rebuild

11. Rename current `setup.sh` → `manage.sh` (update all internal references and README)
12. Create new `setup.sh` — system baseline implementing §1:
    - Guard clauses (arch, macOS, logging init)
    - Load `config.json` via jq
    - SIP detection
    - pmset idempotency (all settings from §1.1 including `disablesleep` and `powermode`)
    - MacBook detection + clamshell warning
    - Caffeinate LaunchDaemon install
    - Network sysctl tuning via `/etc/sysctl.conf`
    - Pre-change service snapshot
    - Spotlight suppression
    - Telemetry / Siri / iCloud / Biome service suppression (SIP-gated)
    - `defaults write` changes (AirDrop, App Nap, animations, notifications, software update, Time Machine)
    - SSH hardening
    - Xcode CLT headless install

### Phase 2.4 — Tool Installer

13. Create `install-tools.sh` — unified tool installer implementing §2:
    - Homebrew prerequisite check
    - Ollama section (§2.2): corrected install, models dir, RAM auto-tuning, correct plist
    - Rapid-MLX section (§2.3): brew tap + install, extras, LaunchDaemon, model cache dir
    - mlx-lm section (§2.4): pip install, LaunchDaemon (only bootstrap if model configured)
    - Infinity section (§2.5): pip install, LaunchDaemon with `--device mps`
    - Exo section (§2.6): brew/pip install, LaunchAgent, auto-login requirement notice
    - Each section gated by `config.json` enabled flag

### Phase 2.5 — Verify and Restore Scripts

14. Create `verify.sh` — health report implementing §3:
    - Report header (timestamp, hardware, SIP)
    - System section: pmset checks, caffeinate daemon, Spotlight, SSH
    - Network section: sysctl values
    - Per-tool sections (gated by config): daemon state, API endpoint, model count
    - Memory pressure check
    - Final result line: `=== RESULT: N warnings, N failures ===`
    - Exit code 0 = all clear, non-zero = failures

15. Create `restore.sh` — undo all changes implementing §4:
    - pmset restore to safe defaults
    - Service re-enable from snapshot
    - Remove all LaunchDaemon plists (caffeinate, ollama, rapid-mlx, mlx-lm, infinity)
    - Remove LaunchAgent plist (exo)
    - Restore Spotlight (`mdutil -a -i on`)
    - Restore `sshd_config` from backup
    - Restore `defaults` changes
    - Restore `sysctl.conf` entries

### Phase 2.6 — External Storage Script

16. Create `storage-volume.sh` — external volume setup implementing §10:
    - Config check (`use_external_volume` flag)
    - Volume detection by label (precheck JSON fast path → live diskutil fallback)
    - Free space validation
    - Filesystem type validation (reject ExFAT/FAT32/NTFS)
    - Directory layout creation (`ollama/`, `rapid-mlx/`, `mlx-lm/`, `infinity/`, `exo/`, `gguf/`)
    - Spotlight exclusion on volume
    - Symlink wiring (`/Library/Ollama/models` → volume, etc.)
    - fstab entry for boot-time auto-mount
    - Config.json update with resolved paths
    - Verification pass

### Phase 2.7 — Documentation

17. Create `docs/tool-comparison.md` — full comparison table (Ollama vs Rapid-MLX vs mlx-lm vs Infinity vs Exo)
18. Create `docs/ram-sizing.md` — model size × quantization × RAM reference + tier table
19. Create `docs/storage-guide.md` — external volume guide: APFS vs HFS+, fstab, symlink map, mount-at-boot
20. Create `docs/known-issues.md` — full workarounds table from §7 + §10.10
21. Rewrite `README.md` — new quick start flow, updated file tree, tool selection guide, hardware RAM table

---

## 5. Naming Decision: `setup.sh` Conflict

The spec names its system-baseline script `setup.sh`, but the current codebase uses that name for the interactive orchestrator. The resolution:

```
Current:  setup.sh          → rename to → manage.sh
New:      setup.sh          = system baseline (spec §1)
```

`manage.sh` becomes the entry point users call for `install/enable/disable/remove/status` operations. It calls the individual component scripts (existing Phase 1 scripts) plus the new `install-tools.sh`.

All references to `./setup.sh` in the current README and deprecated scripts must be updated to `./manage.sh`.

---

## 6. File Tree After Phase 2

```
headless-macs/
├── config.json                      # All tuning parameters (new)
├── precheck.sh                      # Read-only system audit (new)
├── setup.sh                         # System baseline: pmset, sysctl, services, SSH (new)
├── install-tools.sh                 # Serving stack installation (new)
├── verify.sh                        # Health check report (new)
├── restore.sh                       # Undo all changes (new)
├── storage-volume.sh                # External volume setup (new)
├── manage.sh                        # Interactive menu + CLI orchestrator (renamed from setup.sh)
├── lib/
│   └── common.sh                    # Shared utilities + jq helper (updated)
├── scripts/
│   ├── power_management.sh          # pmset wrapper (bug-fixed)
│   ├── homebrew_setup.sh            # Homebrew (unchanged)
│   ├── ollama_setup.sh              # Ollama (bug-fixed: bootstrap/bootout, HOME, install method)
│   └── colima_setup.sh              # Colima + Docker (unchanged)
├── docs/
│   ├── tool-comparison.md           # (new)
│   ├── ram-sizing.md                # (new)
│   ├── storage-guide.md             # (new)
│   └── known-issues.md              # (new)
├── pmset_to_ollama.sh               # [DEPRECATED]
├── setup_colima.sh                  # [DEPRECATED]
├── PLANNING.md                      # Phase 1 plan (complete)
├── PHASE_1_PLAN.md                  # Phase 2 spec (this drives all new work)
├── PHASE_2_PLAN.md                  # This file
└── README.md                        # Rewritten for new flow
```

---

## 7. Key Invariants from the Spec (Non-Negotiable)

These constraints from `PHASE_1_PLAN.md` §8 must be enforced in all new code:

1. **`HOME=/var/root` in every LaunchDaemon plist** — Ollama, Rapid-MLX, mlx-lm, Infinity all panic without it
2. **`bootstrap`/`bootout` only** — `load`/`unload` are deprecated and broken on macOS 15+
3. **`config.json` is the single source of truth** — no hardcoded values in scripts; everything reads from config via `jq`
4. **All changes are idempotent** — every config change checks current state before applying
5. **Logging to `/var/log/mac-llm-setup/`** — every script tees to a timestamped log file
6. **`precheck.sh` first** — always run before any other script; requires no sudo
7. **Infinity needs `--device mps`** — without it, performance degrades ~10× on Apple Silicon
8. **Exo is LaunchAgent, not LaunchDaemon** — it needs user context for mDNS/Tailscale discovery

---

## 8. Out of Scope for Phase 2

- Support for Intel Macs (spec is Apple Silicon only; scripts abort on non-arm64)
- Automated testing framework (manual testing per §Testing Strategy in PLANNING.md)
- GUI or web interface
- Multi-Mac cluster configuration (Exo is installed but cluster config is per-node manual)
- Model downloading automation (users pull models manually after setup)

---

## 9. Success Criteria for Phase 2

1. `./precheck.sh` runs on a clean macOS 26 machine without sudo and produces a complete readiness report with JSON output
2. `./setup.sh` is fully idempotent — running it twice produces `[SKIP]` for all already-applied settings
3. `./install-tools.sh` with only `ollama.enabled: true` installs a working Ollama LaunchDaemon with `HOME=/var/root` and auto-tuned RAM settings
4. `./verify.sh` produces a structured report with correct `[PASS]`/`[FAIL]` for each enabled tool
5. `./restore.sh` cleanly undoes all changes from `./setup.sh`
6. `./storage-volume.sh` correctly symlinks model dirs when `use_external_volume: true` and a matching volume is attached
7. All new `launchctl` calls use `bootstrap`/`bootout` — zero `load`/`unload` calls in any script
8. No script fails on a second run (idempotency)
9. `./manage.sh status` correctly reflects the state set by the new scripts
