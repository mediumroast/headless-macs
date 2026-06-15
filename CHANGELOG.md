# Changelog

All notable changes to headless-macs are documented here.

Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
Versions follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

---

## [Unreleased]

_Changes on the current branch not yet merged to main._

---

## [1.2.0] — 2026-06-14

Phase 5: Security hardening, operational improvements, Ollama lifecycle management, and unprivileged daemon execution.
Addresses peer-review feedback from Jeff (homelab operator running the same stack).

### Fixed

- **sysctl persistence broken** — `setup.sh` was writing to `/etc/sysctl.conf`, which macOS has ignored at boot since Catalina. Network tuning applied for the current session but silently reverted after every reboot. Replaced with a `RunAtLoad` LaunchDaemon (`com.llm-server.sysctl-tuning`) that re-applies all six TCP/socket keys on boot.
- **`pmset autorestart` missing** — power-failure restart was absent from `setup.sh`; the deprecated `pmset_to_ollama.sh` set it to `0`. Added `pmset_apply autorestart 1` to the power section.
- **SSH hardening wiped by OS updates** — `setup.sh` edited `/etc/ssh/sshd_config` directly via `sed`; macOS updates silently replace that file. Rewrote to use a drop-in at `/etc/ssh/sshd_config.d/100-headless.conf` which survives updates. Added an `authorized_keys` precheck gate: `PasswordAuthentication no` is only written when a key file is confirmed present, preventing lockout on headless boxes.
- **Homebrew Ollama service conflict** — if Ollama was previously installed via `brew install ollama`, `brew services` held port 11434 and our LaunchDaemon failed to bind. `install-tools.sh` now stops any Homebrew-managed Ollama service before bootstrapping the daemon.

### Changed

- **Network defaults hardened** — `config.json` defaults changed from `localhost_only: false` + `disable_firewall: true` to `localhost_only: true` + `disable_firewall: false`. Services now bind to `127.0.0.1` and the firewall is left on by default. LAN-accessible deployments must opt in explicitly. `setup.sh` firewall comment and jq fallback updated to match.
- **Serving daemons run as `_llmserver`** — Ollama, Rapid-MLX, mlx-lm, and Infinity LaunchDaemons now run as an unprivileged system user (`_llmserver`, UID 400–499, home `/Library/LLMServer`, shell `/usr/bin/false`, hidden from login screen) instead of root. Log and model directories are owned `_llmserver:_llmserver`. `restore.sh` removes the account and home directory on teardown.

### Added

- **`com.llm-server.maxfiles` LaunchDaemon** — raises system-wide file descriptor limits (soft 524288 / hard 1048576) at boot. Concurrent model loading and parallel inference connections can exhaust the default fd limit on busy nodes.
- **`com.llm-server.pmset-heal` daily timer** — a `StartCalendarInterval` LaunchDaemon that re-runs `setup.sh --power-only` at 03:00 each day. macOS updates silently reset pmset values; this closes that gap automatically. The `--power-only` flag runs only the power section and exits.
- **`update-tools.sh`** — new script for in-place Ollama binary upgrades: stops the daemon, runs the upstream installer, removes any re-added login item, re-bootstraps the daemon, and verifies the API. Prevents the common issue of running a new binary under the old daemon process.
- **`manage.sh update` command** — `./manage.sh update ollama` and menu option 16 wire through to `update-tools.sh`.
- **`CLAUDE.md`** — comprehensive project guidance file covering script architecture, output conventions, config.json rules, LaunchDaemon conventions, verify.sh contract, SIP-gating pattern, tool install pattern, hardware guards, sudo keepalive, logging pattern, known constraints, planning convention, end-of-session checklist, and versioning history.
- `verify.sh` checks: `autorestart` pmset value, sysctl-tuning daemon presence, sshd drop-in file, maxfiles soft limit, `_llmserver` account existence.
- `restore.sh`: removes sysctl-tuning, maxfiles, and pmset-heal daemons; removes sshd drop-in instead of restoring from backup; removes `_llmserver` account and home directory.
- `docs/known-issues.md`: updated pmset-reset entry to reference the self-heal timer; added Ollama update / daemon bounce entry; updated `$HOME` references to reflect `_llmserver` home.

### PR

[#3 Phase 5: Security hardening and operational improvements](https://github.com/miha42-github/headless-macs/pull/3)

---

## [1.1.0] — 2026-06-10

Phase 4: Modelfile system, KV cache sizing model, and client tooling documentation.

### Added

- **`modelfiles/` directory** with three production Modelfiles for qwen3-coder-next Q6_K:
  - `qwen3-coder-next-256k-agent.modelfile` — low temperature, strict tool-call rules for agentic tasks
  - `qwen3-coder-next-256k.modelfile` — higher temperature for chat
  - `qwen3-coder-next-128k.modelfile` — reduced context for memory headroom on smaller nodes
- **`docs/modelfile-guide.md`** — why Modelfiles are required, `num_ctx` and sampling parameter rationale, agent vs chat split pattern, `ollama create` workflow, `keep_alive -1` model pinning.
- **`docs/ram-sizing.md`** — expanded with KV cache sizing formula, nemotron-cascade-2 worked example, effective memory budget table.

### Changed

- **Zoo Code** — Roo Code shut down April 2026 (team pivoted to Roomote). Community fork **Zoo Code** (Apache 2.0, same codebase) launched May 16, 2026. All references updated throughout docs and README.
- **`docs/known-issues.md`** — added: VS Code Copilot agent-mode tool call loop bug (→ Zoo Code workaround), Remote-SSH + Ollama tunnel connection leak with TCP keepalive fix, MLX `num_ctx` caveat, Ollama UI context window metadata gap.
- **`docs/tool-comparison.md`** — Ollama 0.19 MLX speed update, Modelfile requirement note, mlx-lm `num_ctx` caveat.
- **`README.md`** — `modelfiles/` added to file tree, `ollama create` + `keep_alive` workflow in After Installation section, Zoo Code recommendation for agentic use.

### PR

[#2 Phase 4: Modelfile system, KV cache model, Zoo Code](https://github.com/miha42-github/headless-macs/pull/2)

---

## [1.0.0] — 2026-06-07

Phase 2: Complete production rewrite. First release intended for real headless inference nodes.

### Added

- **`precheck.sh`** — read-only system audit (no sudo, no changes). Checks hardware, RAM, macOS version, SIP, FileVault, auto-login, Xcode CLT, Homebrew, Python, port availability, storage. Writes `/tmp/mac-llm-precheck.json` for downstream scripts. Exit codes: `0` ready, `1` blockers, `2` warnings.
- **`config.json`** — single control plane for all scripts. Drives tool selection, network posture, storage layout, system flags, and power mode via `jq`.
- **`install-tools.sh`** — installs and configures Ollama, Rapid-MLX, mlx-lm, Infinity, and Exo as LaunchDaemons. Each tool gated by its `enabled` flag. RAM auto-tunes Ollama across five tiers (≤16 GB → ≥65 GB). All plists use `bootstrap`/`bootout` and include `HOME=/var/root`.
- **`verify.sh`** — health check report across system baseline and all enabled tools. Read-only, safe to run at any time. Exit `0` all clear, `1` failures, `2` warnings.
- **`restore.sh`** — undoes all changes made by `setup.sh` and `install-tools.sh`. Removes LaunchDaemons, restores pmset, re-enables suppressed services from pre-change snapshot.
- **`storage-volume.sh`** — external volume setup: validates APFS, creates model directory layout, excludes from Spotlight, wires `/Library` symlinks, adds fstab entry for boot-time auto-mount.
- **`docs/`** directory: `tool-comparison.md`, `ram-sizing.md`, `storage-guide.md`, `known-issues.md`.

### Changed

- **`setup.sh`** — complete rewrite as system baseline script (pmset, sysctl, service suppression, SSH hardening, Xcode CLT). Previous `setup.sh` renamed to `manage.sh`.
- **`scripts/ollama_setup.sh`** — idempotency fixes, correct `bootstrap`/`bootout` launchctl, `HOME=/var/root` in daemon plist.
- **`scripts/power_management.sh`** — idempotency via `pmset -g` state check before applying.
- **`README.md`** — full rewrite covering tool selection, hardware RAM reference, script reference, and troubleshooting.

### Design decisions

- All inference services bind `0.0.0.0` by default (remote access on); `network.localhost_only: true` restricts all services to loopback with one flag.
- Firewall disabled by default for unsigned Python services (Rapid-MLX, mlx-lm, Infinity).
- Every change checks current state before applying (`[SKIP]` when already correct).
- `launchctl load`/`unload` removed everywhere; `bootstrap system`/`bootout system` only.

### PR

[#1 Phase 2: Production headless inference server setup](https://github.com/miha42-github/headless-macs/pull/1)

---

## [0.2.0] — 2026-02-01

Modular refactor: split monolithic script into per-component scripts with a consistent interface.

### Added

- `manage.sh` — interactive menu and CLI dispatcher (`install`, `enable`, `disable`, `remove`, `status`).
- `scripts/homebrew_setup.sh` — Homebrew install/remove.
- `scripts/power_management.sh` — pmset headless profile.
- `scripts/ollama_setup.sh` — Ollama install, LaunchDaemon, enable/disable/remove lifecycle.
- `scripts/colima_setup.sh` — Colima + Docker setup.
- `lib/common.sh` — shared utility functions.
- `setup_colima.sh` — standalone Colima setup (later deprecated).

---

## [0.1.0] — 2025-12-27

Initial release: single-script pmset + Ollama LaunchDaemon setup.

### Added

- `pmset_to_ollama.sh` — sets headless pmset profile and installs Ollama as a LaunchDaemon.

---

[Unreleased]: https://github.com/miha42-github/headless-macs/compare/v1.2.0...HEAD
[1.2.0]: https://github.com/miha42-github/headless-macs/compare/v1.1.0...v1.2.0
[1.1.0]: https://github.com/miha42-github/headless-macs/compare/v1.0.0...v1.1.0
[1.0.0]: https://github.com/miha42-github/headless-macs/compare/v0.2.0...v1.0.0
[0.2.0]: https://github.com/miha42-github/headless-macs/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/miha42-github/headless-macs/releases/tag/v0.1.0
