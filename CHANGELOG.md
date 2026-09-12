# Changelog

All notable changes to headless-macs are documented here.

Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
Versions follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

---

## [Unreleased]

Targeted for `v2.3.1` (Patch). Includes PR [#19](https://github.com/mediumroast/headless-macs/pull/19)'s fixes, which this work builds on — see that PR for its own detail.

### Fixed

- **`OLLAMA_GPU_PERCENT` was a fabricated environment variable** — confirmed
  absent from Ollama's actual current source (`envconfig/config.go` on
  `ollama/ollama` `main`, fetched and searched directly). Ollama silently
  ignores unrecognized environment variables, so `tools.ollama.gpu_percent`
  in `config.json` has been doing nothing since it was added. Removed
  outright — no real equivalent exists (the closest real setting,
  `OLLAMA_GPU_OVERHEAD`, reserves an absolute byte count of VRAM, not a
  percentage, and isn't a drop-in replacement).
- **`OLLAMA_LOG_LEVEL` was also fabricated** — same source verification.
  Ollama's actual, sole documented verbosity switch is `OLLAMA_DEBUG`
  (confirmed against every `.mdx` doc page in the repo: the only usage
  anywhere is `OLLAMA_DEBUG=1`, a plain boolean — not the multi-tier
  `"warn"/"debug"/"trace"` enum the removed setting implied). `verify.go`'s
  corresponding check only ever confirmed the (fake) key's presence in the
  plist, never that it did anything, so it had been reporting
  `[PASS] OLLAMA_LOG_LEVEL configured` this whole time regardless.
  `tools.ollama.log_level` (string) is replaced with `tools.ollama.debug`
  (bool, default `false`), mirroring Ollama's real interface exactly.
- **`headless-macs-debug logs` bundled far more than intended** — every
  bundle included `/var/log/mac-llm-setup` (headless-macs's own operational
  logs, not a serving tool's — dropped entirely) and each tool's *entire*
  rotation history (up to `logrotate`'s configured `100M x5` retention per
  stream). Now bundles, per log stream, only the live file plus its `--keep`
  (default `2`) most recent rotations — a CLI flag rather than a config
  value, since this runs over SSH where the config file may not be
  conveniently reachable.

### Added

- **`headless-macs-debug clean`** — deletes every bundle under
  `/var/log/mac-llm-setup/bundles/`, freeing the space they use. No
  confirmation prompt (matches this binary's existing scriptable,
  non-interactive design) and no automatic retention policy yet — a
  deliberately blunt manual "empty it out" to start.

## [2.3.0] — 2026-09-10

Phase 11: fixes for issues #13–#17, plus a new debugging-tools capability.
Live-tested on doppio-1, which surfaced and fixed several additional real
bugs beyond the original diagnoses — listed below alongside the planned
fixes.

### Fixed

- **Config editor: tool enable/disable toggle didn't persist; config
  file never gained new sections; no teardown when disabling a tool**
  (#13) — `config.Load()` now auto-migrates a config file predating a
  schema addition (e.g. missing the macmon/tui sections), patching and
  saving it back immediately instead of waiting for an unrelated future
  save. The editor's save handler now verifies the write by re-reading
  the file, surfacing `[save failed: ...]` instead of silently claiming
  `[saved]`. New confirmation screen when a tool's `enabled` flag
  transitions `true` → `false`: stop and uninstall now (new
  `internal/ops/disable.go`, leaving model/data directories untouched),
  save config only (default), or cancel.
- **Baseline: three Phase 8 service suppressions (coreaudiod, audiomxd,
  AirPlayXPCHelper) never stopped the running process** (#14) — those
  three had an empty `Plist` field in `phase8Suppressions`, which
  skipped the `bootout` call every other entry gets. Real plist paths
  confirmed live on doppio-1 and doppio-2, not guessed.
- **Baseline: SSH section reported success from subprocess exit codes,
  never verified sshd actually came up** (#15) — extracted Verify's
  real state check into a shared `sshEnabledLive()` helper both
  functions now call, so they can't disagree about the same thing
  again. That shared check itself needed a second fix once live-tested:
  it originally parsed `launchctl print`'s `state` field, but
  `com.openssh.sshd` is inetd-compatible/socket-activated, so its
  top-level state legitimately reads `not running` while idle — there's
  no `launchctl` state string that reliably means "armed and listening"
  for this service class. Replaced with a live TCP dial to
  `127.0.0.1:22` that reads back the `SSH-` protocol banner, the same
  approach every other tool's HTTP endpoint check uses.
- **`update-tools` had no macmon support at all** (#16) — added
  `updateMacmon()`, matching the existing per-tool update pattern.
- **Precheck/Verify: scroll bounds computed from raw check count, not
  rendered row count** (#17) — `m.scroll` was bounded against
  `checkCount()` but used as an index into `renderChecks()`'s output,
  which has more entries (section headers, blank separators, detail
  rows). Cached the rendered rows and bound scroll against their real
  length instead. Live testing found this fix incomplete: the identical
  bug existed in a second, near-duplicate screen implementation
  (`run_screen.go`, used by Baseline/Storage/Tools/Restore/Update/Debug
  Tools) that #17's original fix never touched, fixed the same way. The
  actual root cause of the reported "title bar disappears" symptom
  turned out to be a separate, genuine `lipgloss` bug: `Style.Render()`
  with `Background()` set, given a string with an embedded trailing
  `\n`, pads a synthetic second line with spaces and no closing newline
  of its own, merging the scroll indicator into whatever got written
  next — reproduced directly against this repo's `lipgloss` dependency
  before fixing, by moving the `\n` outside the styled `Render()` call.
- **`archive/tar: write too long` bundling an actively-growing log
  file** — `headless-macs-debug logs` built tar headers from a stale
  `os.FileInfo` taken before the file could grow further (hit live
  bundling Ollama's actively-growing `stderr.log`). Fixed by re-statting
  the already-open file and capping the copy with `io.CopyN`.
- **`headless-macs-debug` invoked by bare name fails over
  non-interactive SSH** — `ssh host 'cmd'` runs a non-login shell, which
  typically excludes `/usr/local/bin` from `$PATH`. Documented the
  full-path invocation for scripted/automated use.
- **`make install` only installed one of the two binaries this project
  builds** — now installs both `headless-macs` and `headless-macs-debug`.

### Changed

- **`internal/ops/assets/headless-macs-debug` is no longer committed to
  git** — it was tracked as a real binary so a fresh clone's
  `go build ./...` would work, but a Go build isn't byte-reproducible
  across machines/toolchains even from identical source, so every local
  `make build` permanently diverged from the committed copy and broke
  `git pull`. `make debug-binary`/`make build` regenerate it fresh
  instead; a bare `go build ./...` now needs one of those run once
  after cloning first.

### Added

- **`headless-macs-debug`** — a small, separate binary for pulling logs
  off a node without the full TUI. `logs [tool]` forces an out-of-cycle
  rotation (reusing the existing shared logrotate config) and bundles
  the result into a timestamped `tar.gz`, ready to `scp` off the box.
  Distributed via `go:embed` inside the main binary rather than a
  second file to remember to copy.
- **`debug.sudo_nopasswd_enabled`** config key and `headless-macs
  debug-tools` / `x` (Debugging Tools) — a narrowly-scoped, toggleable
  `NOPASSWD` sudo grant for exactly `headless-macs-debug`, so it can
  run non-interactively over SSH. Target username is prompted
  interactively (never stored in config) and validated to exist before
  anything is written; the sudoers drop-in is validated with
  `visudo -c` before installing. Documented in `README.md` as an
  explicit escalation/de-escalation path.
- **`tea.WithMouseCellMotion()`** — `tea.WithAltScreen()` alone doesn't
  stop a terminal from doing its own native viewport scroll on a
  trackpad/scroll-wheel gesture (a real, separate bubbletea gotcha found
  investigating the scroll-indicator symptom above; not itself the fix
  for that symptom, but worth keeping).
- **`make uninstall`** — removes both installed binaries; `make clean`
  now also covers the (no longer committed) debug-binary embed asset.

### PR

[#18](https://github.com/mediumroast/headless-macs/pull/18)

---

## [2.2.1] — 2026-09-07

Docs-only bugfix release: corrects factually wrong hardware/model claims
and stale command references introduced or left over from Phases 7–10 and
earlier.

### Fixed

- **`docs/ram-sizing.md` and `README.md`'s Hardware RAM Reference cited
  hardware that doesn't exist** — "Mac Mini M4 Max" (Mac Mini has never
  shipped with an M4 Max chip; it tops out at M4 Pro) and "Mac Studio M4
  Ultra" (there is no M4 Ultra — M4 Max lacks the UltraFusion connector
  Ultra chips require, so Apple's 2025 Studio refresh paired M4 Max with
  the previous-gen M3 Ultra instead). Corrected against real
  `headless-macs precheck` output and current Apple/Ollama library data;
  added footnotes explaining both errors and a note on how volatile
  Apple's Ultra-tier RAM configs have been through 2026. Also fixed:
  `gemma4:27b` → the real tag `gemma4:26b`, and the Ollama auto-tune
  table's `MAX_CONTEXT` for ≥65GB (said `65,536`; the code leaves it
  unset/native)
- **`docs/known-issues.md` and `docs/storage-guide.md` instructed readers
  to run deprecated v1 shell scripts** (`setup.sh`, `install-tools.sh`,
  `storage-volume.sh`, `update-tools.sh`) instead of the current
  `headless-macs` subcommands — every fix cell in both documents predated
  the Phase 6 Go rewrite and was never updated. Every instruction
  cross-checked against the actual Go implementation before rewriting
  (not just renamed) — one fix (`update-tools`) doesn't support the
  scripts' old per-tool argument, it updates all enabled tools; ownership
  corrected from `root:wheel` to the real `_llmserver:_llmserver`
  convention
- **`docs/known-issues.md`'s Exo troubleshooting row still described
  Tailscale-based discovery**, contradicted by `docs/tool-comparison.md`'s
  own prior correction (Phase 7) — neither Tailscale discovery nor a
  `--discovery-module` flag exist in current exo. Brought into sync

### PR

[#10 Docs: fix nonexistent Mac hardware claims and deprecated script refs](https://github.com/mediumroast/headless-macs/pull/10)

---

## [2.2.0] — 2026-09-06

Phases 7–10, shipped together on one branch rather than as four separate
minor releases: Phase 7 (serving-tool log management), Phase 8
(unnecessary service suppression), Phase 9 (macmon hardware telemetry
daemon), Phase 10 (TUI structure redesign — sidebar + Dashboard — and the
version-injection fix). See `docs/RELEASE_STRATEGY.md`'s worked example
for why this is one release instead of `v2.2.0`–`v2.5.0`.

### Fixed

- **Ollama symlinked-models startup error** — `OLLAMA_MODELS` is now resolved
  through `filepath.EvalSymlinks` before being written to the plist, so a
  symlinked models directory (the default when using an external storage
  volume) no longer triggers Ollama's `ensure path elements are traversable`
  error loop on every startup
- **Exo logs lost on reboot** — `com.exo.node` now logs to `/var/log/exo/`
  instead of `/tmp/`
- **Exo would not start at all** — `installExo()`/`exoPlist()` passed
  `--chatgpt-api-port` and `--discovery-module`, neither of which exist in
  any current exo release (confirmed against `exo-explore/exo` `main`);
  exo's argparse would reject both as unrecognized arguments. Fixed to use
  the real `--api-port` flag; `--discovery-module` (no such concept in
  current exo) is removed, replaced by the new `tools.exo.bootstrap_peers`
  config key mapping to exo's actual `--bootstrap-peers` flag
- **Exo's own internal logs were unbounded and hidden** — exo manages two
  log files itself outside of launchd's stdout/stderr capture:
  `exo_log/exo.log` (rotates only once, at process start — unbounded within
  a long-running process) and `exo_log/runner_log/{stdout,stderr}.log` (no
  rotation at all, ever). Both defaulted to the hidden `~/.exo`. Fixed by
  setting `EXO_HOME=/Library/Exo` (relocating exo's whole state root to a
  discoverable, project-managed path) and extending the shared logrotate
  config to cover both files
- **The shared logrotate daemon could never pick up a config change** —
  `installLogRotate()` skipped entirely once its plist existed, so the Exo
  log-rotation stanza above would never have reached a box that had
  already run `install-tools` once. Same bug, same fix, found in the four
  Phase 6 infrastructure daemons (`caffeinate`, `sysctl-tuning`,
  `maxfiles`, `pmset-heal` — all routed through one shared
  `installLaunchDaemon()` helper): idempotency is now based on comparing
  generated content against what's on disk, not just whether the file
  exists, and a content change now triggers a proper reload
  (`bootout`+`bootstrap`) instead of never being applied
- **`checkHTTP`/`checkEndpoint` verified nothing about the response** —
  both passed on any successful TCP round-trip regardless of HTTP status
  code or body content (a leftover, never-implemented parameter from the
  original bash `check_http`/`check_endpoint "name" "url" "pattern"`
  contract). Now check status code and, when given a real pattern, body
  content — restoring the original bash behavior. Backfilled to every
  existing Ollama/Rapid-MLX/mlx-lm/Infinity/Exo check in `verify.go`
- **`docs/tool-comparison.md`'s Exo section described discovery mechanisms
  that don't exist** — Tailscale-based discovery and a `--discovery-module`
  flag, stale since the Exo CLI fix above. Corrected to describe the real
  `--bootstrap-peers`/`--namespace`/zenoh-based mechanism
- **The version string could never actually change** — `main.go`'s
  `version` was a `const`, which Go's `-ldflags -X` injection cannot
  target (it only works on package-level string `var`s) — so it had been
  stuck at the last tagged release across three merged phases of
  unreleased changes, making every local build indistinguishable from
  v2.1.1 regardless of what it actually contained, and quietly defeating
  the new upgrade-nudge feature below (it compares versions by string
  equality). Fixed: `version` is now a `var`, and the Makefile computes it
  from `git describe --tags --always --dirty` at build time
- **Version string displayed as `vv2.1.1-...`** — `git describe --tags`
  (what the Makefile injects) already includes the tag's own `v` prefix,
  but every display site (all five TUI title bars plus the CLI's upgrade
  nudge and `status` header) also hardcoded a literal `v` in front of it.
  Found by inspecting real screenshots from doppio-2 after the version-var
  fix above. Fixed by stripping a leading `v` once in `main()` before
  assigning to `ops.Version`/`tui.Version`, so every existing `v%s` call
  site now renders correctly instead of needing six separate edits
- **The TUI's background didn't paint the app's own colors** — most style
  tokens set only a foreground color, so whatever showed behind the text
  was the terminal emulator's own theme, not `colBg` as
  `docs/TUI_STYLE_GUIDE.md` intended (it even said as much: "referenced
  but rarely painted"). Not a regression — confirmed via `git diff` that
  nothing in Phases 7–9 touched any styling file. Fixed by giving every
  body-context style an explicit background and wrapping the whole
  screen in one outer full-width/height paint; two related gaps found by
  direct experiment while fixing this — separators between multiple
  status-bar hints, and the config editor's label/value spacing — needed
  their own fix since Lip Gloss doesn't repaint background across an
  inner style's reset, only at a line's start or its own added padding

### Added

- **TUI restructured**: a persistent left sidebar (previously a
  full-screen menu you navigated into and back out of) plus a content
  pane, with a new **Dashboard** as the default view — live daemon
  state, resource use (RSS/CPU%), macmon hardware telemetry when enabled,
  and the upgrade-nudge line below. Below ~70 columns the sidebar
  collapses to an icon-only rail. New `internal/ops/status.go`
  (`RunStatus`) backs both the Dashboard and the new `headless-macs
  status [--watch]` CLI subcommand — same data, one source
- **Upgrade-awareness nudge**: `baseline`/`install-tools` now record
  which version last configured a box
  (`/var/log/mac-llm-setup/.last-configured-version`); the Dashboard and
  every CLI command print a one-line notice when the running binary
  differs from that marker, naming the commands to re-run
- **`tui.dashboard_refresh_ms`** config key — Dashboard's refresh
  interval, configurable rather than fixed
- **Five more background services suppressed** on System Baseline: LAN
  Content Caching (`com.apple.AssetCache.builtin`), `mobileassetd`
  (largest unnecessary process by RSS observed on doppio-1), the audio
  stack (`coreaudiod`/`audiomxd` — no speakers/mic/audio use case
  headless), Find My beaconing, and the AirPlay receiver/sender helper.
  Same SIP-gated pattern as the existing Spotlight/iCloud/Siri
  suppressions; a single shared list (`phase8Suppressions`) drives
  suppression, Restore's re-enable, and Verify's health check, so the
  three can't drift out of sync with each other
- **Precheck advisories**: warns when Docker's `com.docker.vmnetd` is
  detected (headless-macs didn't install it and won't remove it, but flags
  it), and when Rapid-MLX and Ollama are both enabled (Rapid-MLX holds its
  model resident in memory continuously — see the new note in
  `docs/tool-comparison.md` and `README.md`)
- **`tools.ollama.log_level`** config key (default `warn`) — sets
  `OLLAMA_LOG_LEVEL` in the daemon's environment to cut Ollama's own request/
  model-loading log noise
- **`--log-level` verbosity flags** on mlx-lm, Infinity, and Rapid-MLX's
  daemon invocations — confirmed against each tool's upstream CLI
  source/docs (`ml-explore/mlx-lm`, `michaelfeil/infinity`,
  `raullenchai/Rapid-MLX`) rather than guessed
- **Shared `logrotate` LaunchDaemon** (`com.llm-server.logrotate`) — bounds
  every serving tool's `stdout.log`/`stderr.log` to 100 MB × 5 rotations,
  daily at 2 AM, via `copytruncate` (not `newsyslog`, which breaks
  launchd-managed daemons' open file descriptors on rotation)
- New Verify checks: Ollama's `OLLAMA_MODELS`/`OLLAMA_LOG_LEVEL` state, Exo's
  log location + plist flag sanity + `EXO_HOME`, the logrotate daemon's
  presence *and* config content, `--log-level` presence for
  mlx-lm/Infinity/Rapid-MLX (previously only Ollama and Exo had this), and
  a `[PASS]`/`[WARN]` per Phase 8 suppressed service confirming it's
  actually not running
- Restore now removes the logrotate daemon and config
- **Precheck now flags stale/unrecognized `config.json` keys** — derives
  the set of valid keys from the `Config` struct itself, so a renamed or
  removed field (like `tools.exo.discovery_module` above) gets a specific
  `[WARN]` explaining what changed, instead of silently sitting in the
  file forever doing nothing
- **macmon hardware telemetry daemon** (`com.llm-server.macmon`, new
  `tools.macmon` config block, disabled by default) — CPU/GPU/ANE power,
  temperature, and memory stats over HTTP (`GET /json`, `/metrics` in
  Prometheus format), running unprivileged as `_llmserver`. Detects
  whether the installed macmon build supports a `--host` flag and honors
  `network.localhost_only` when it does; `[WARN]`s in both `install-tools`
  and `verify` when it doesn't, rather than silently binding all
  interfaces. New Verify `MACMON` section, Restore cleanup, and Precheck
  coverage (added to `prereqs` and the port-availability check)
- **Precheck's port-availability check is now config-driven** — the
  hardcoded default-port list (`toolPorts`) is replaced by
  `effectiveToolPorts(cfg)`, reading each tool's actual configured port
  and only falling back to the tool's own default when unset. Previously
  harmless only because the shipped template's ports happened to match
  the hardcoded list; an operator who customized a tool's port got checked
  against the wrong one, silently
- **Precheck nudges about newly-available opt-in features** — when a
  config section like `tools.macmon` is entirely absent from an existing
  install's `config.json` (not just disabled), Precheck prints a one-time
  `[INFO]` pointing at the docs, distinguishing "never configured" from
  "explicitly disabled" (the latter gets no nudge)

### Changed

- **`tools.exo.discovery_module`** (string) replaced by
  **`tools.exo.bootstrap_peers`** (array of strings) — the former mapped to
  a `--discovery-module` flag that never existed in exo's CLI, so no
  working config could have depended on it
- **README screenshots retaken** on real hardware (doppio-2, Terminal.app)
  showing the actual Phase 10 sidebar + Dashboard shell — Dashboard, Edit
  Config, Precheck, System Baseline, Storage Setup, Verify, and Update
  Tools. The old pre-Phase-10 flat-menu screenshots (`images/Screenshot
  {1,2,3}.jpg`) are removed rather than kept alongside, since they no
  longer match any current screen

### Upgrading an existing install

All four phases above are picked up automatically the next time their
respective command runs — there is no separate migration step:

- **Phase 7's fixes** (Ollama, Exo, mlx-lm/Infinity/Rapid-MLX logging, the
  shared logrotate daemon): re-run `sudo headless-macs install-tools`.
- **Phase 8's suppressions**: re-run `sudo headless-macs baseline`.
- **Phase 9's macmon**: opt-in — set `tools.macmon.enabled: true` in
  `config.json`, then re-run `sudo headless-macs install-tools`. Precheck
  will mention it's available if you haven't seen the note.
- **Phase 10's TUI**: nothing to run — just rebuild (`make build`) and
  launch. The Dashboard's upgrade-nudge line and every CLI command's
  stderr nudge only start working once a `baseline`/`install-tools` run
  under this version has written the first version marker.

Run `sudo headless-macs verify` afterward to confirm — its `[WARN]`/`[FAIL]`
messages name the exact command to fix whatever they flag.

### PR

[#8 Phase 10 (v2.2.0): serving-tool log management, service suppression, macmon, TUI/CLI restructure](https://github.com/mediumroast/headless-macs/pull/8)

---

## [2.1.1] — 2026-08-15

### Fixed

- **TUI version string** — all screens showed `v2.0.0-dev`; version is now set from the single constant in `main.go` via `tui.Version`
- **`.gitignore` pattern** — `headless-macs` was matching `cmd/headless-macs/` directory and blocking staging of source files; changed to `/headless-macs` (rooted pattern)

### PR

_#5 Fix: version string and .gitignore pattern (link after merge)_

---

## [2.1.0] — 2026-08-15

Post-launch fixes and CLI headless support found during first real-world run on doppio-1 (M4 Max, macOS 26).

### Added

- **CLI subcommands** — every pipeline stage is now scriptable without the TUI:
  ```
  sudo headless-macs precheck
  sudo headless-macs baseline
  sudo headless-macs install-tools
  sudo headless-macs verify
  sudo headless-macs restore
  sudo headless-macs update-tools
  sudo headless-macs storage
  ```
  Output uses the same `[SET]`/`[PASS]`/`[WARN]` prefix convention. Exit codes: `0` success, `1` failures, `2` warnings only. Suitable for cron or remote SSH automation.
- **`--help` / `--version` flags** — print and exit without starting the TUI. Work in any position (`headless-macs precheck --help`).

### Fixed

- **TUI output corruption on all operation screens** — `internal/log/logger.go` was unconditionally writing to `os.Stdout` via `io.MultiWriter`. Every `[SET]`/`[WARN]`/`[PASS]` line trashed the Bubble Tea alt-screen renderer. Added `InitTUI()` (file-only) and `CLIMode` override; all six `ops/` entry points changed to `InitTUI`.
- **Precheck CLI produced no output** — `RunPrecheck` accumulates results in a struct for the TUI but never printed them in CLI mode. Added `r.PrintText()` call in the CLI dispatcher.
- **Ollama update always showed `[WARN]` on headless servers** — the installer script exits non-zero because it tries to `open -a Ollama` (GUI launch, `RBSRequestErrorDomain Code=5`) even after successfully installing the binary. Installer exit code is now intentionally ignored; success is determined by comparing `ollama --version` before and after.
- **Ollama version string garbled when daemon is stopped** — `ollama --version` prints warning lines (`Warning: could not connect to a running Ollama instance`) before the version when the daemon is not running. Added `ollamaVersion()` helper that scans for `"version is "` across all output lines instead of taking the full string.
- **`headless-macs precheck` CLI printed no output** — `RunPrecheck` builds a result struct for the TUI; `PrintText()` was never called in headless mode.

### PR

_#4 Phase 6 post-launch fixes (link after merge)_

---

## [2.0.0] — 2026-08-15

Phase 6: Go rewrite. Replaces the bash pipeline with a single compiled binary and interactive Bubble Tea TUI.

### Added

- **`cmd/headless-macs/`** — Go binary entry point. Bootstraps `~/.headless_macs/config.json` on first run; opens the TUI with `tea.WithAltScreen`.
- **`internal/config/`** — typed config schema matching `config.json`; `Load`/`Save`/`Bootstrap` functions; `UserConfigPath()` returning `~/.headless_macs/config.json`.
- **`internal/log/`** — structured log writer that mirrors the shell pipeline prefix conventions (`[SET]`, `[SKIP]`, `[WARN]`, `[FAIL]`, `[PASS]`, etc.) and tees to timestamped files under `/var/log/mac-llm-setup/`.
- **`internal/ops/precheck.go`** — port of `precheck.sh`; `RunPrecheck()` returns `*PrecheckResult` with a `[]CheckItem` list; writes `/tmp/mac-llm-precheck.json`.
- **`internal/ops/baseline.go`** — port of `setup.sh`; `RunBaseline()` with `BaselineOptions`; all 8 sections (power, sysctl, service suppression, UI defaults, SSH, Spotlight, Xcode CLT, firewall).
- **`internal/ops/storage.go`** — port of `storage-volume.sh`; `RunStorage()` with diskutil integration, volume ownership, Spotlight exclusion, fstab entry, and `com.llm-server.storage-mount` LaunchDaemon.
- **`internal/ops/tools.go`** — port of `install-tools.sh`; `RunTools()` creates `_llmserver` service account (UID/GID 400–499), writes LaunchDaemon plists for all 5 tools, and runs `loadDaemon()`; Ollama RAM auto-tuning via `ollamaAutoTune(ramGB)`.
- **`internal/ops/verify.go`** — port of `verify.sh`; `RunVerify()` returns `*VerifyResult` with `[]CheckItem`; helpers `checkPmset`, `checkSysctl`, `checkDaemon`, `checkHTTP`.
- **`internal/ops/restore.go`** — port of `restore.sh`; `RunRestore()` in 9 sections; reads service snapshot for re-enable; removes `_llmserver` account and home.
- **`internal/ops/update.go`** — port of `update-tools.sh`; `RunUpdateTools()` with per-tool stop → upgrade → re-bootstrap pattern.
- **`internal/tui/`** — Bubble Tea TUI package:
  - `app.go` — top-level `App` model; screen routing; `spinner.TickMsg` forwarding.
  - `menu.go` — main menu with single-key shortcuts; all 8 ops items ready.
  - `config_editor.go` — scrollable config editor with bool toggle, text/int inline editing, live modified indicator, and save/reset/cancel.
  - `precheck_screen.go` — shared model for Precheck and Verify; spinner; scroll indicators; `[BLOK]` vs `[FAIL]` prefix by context; log path footer.
  - `run_screen.go` — shared model for Baseline, Storage, Tools, Restore, Update; spinner; scroll indicators; page up/down; summary bar with counts and log path.
  - `restore_confirm.go` — confirmation screen before destructive restore; `y` confirms, any other key cancels.
  - `styles.go` — dark-only Lip Gloss palette.

### Changed

- **`precheck.sh`, `setup.sh`, `install-tools.sh`, `storage-volume.sh`, `verify.sh`, `restore.sh`, `update-tools.sh`, `manage.sh`** — marked deprecated in v2.0.0; all retain their existing functionality but are no longer actively maintained.
- **`README.md`** — rewritten for v2: Go Quick Start, menu key reference table, updated file structure showing `cmd/` and `internal/`, v2 troubleshooting commands.
- **`CLAUDE.md`** — versioning history updated; deprecated script list extended to cover v1 shell pipeline.

### PR

_#4 Phase 6: Go rewrite — TUI binary replaces shell pipeline (link after merge)_

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

[#3 Phase 5: Security hardening and operational improvements](https://github.com/mediumroast/headless-macs/pull/3)

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

[#2 Phase 4: Modelfile system, KV cache model, Zoo Code](https://github.com/mediumroast/headless-macs/pull/2)

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

[#1 Phase 2: Production headless inference server setup](https://github.com/mediumroast/headless-macs/pull/1)

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

[Unreleased]: https://github.com/mediumroast/headless-macs/compare/v2.3.0...HEAD
[2.3.0]: https://github.com/mediumroast/headless-macs/compare/v2.2.1...v2.3.0
[2.2.1]: https://github.com/mediumroast/headless-macs/compare/v2.2.0...v2.2.1
[2.2.0]: https://github.com/mediumroast/headless-macs/compare/v2.1.1...v2.2.0
[2.1.1]: https://github.com/mediumroast/headless-macs/compare/v2.1.0...v2.1.1
[2.1.0]: https://github.com/mediumroast/headless-macs/compare/v2.0.0...v2.1.0
[2.0.0]: https://github.com/mediumroast/headless-macs/compare/v1.2.0...v2.0.0
[1.2.0]: https://github.com/mediumroast/headless-macs/compare/v1.1.0...v1.2.0
[1.1.0]: https://github.com/mediumroast/headless-macs/compare/v1.0.0...v1.1.0
[1.0.0]: https://github.com/mediumroast/headless-macs/compare/v0.2.0...v1.0.0
[0.2.0]: https://github.com/mediumroast/headless-macs/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/mediumroast/headless-macs/releases/tag/v0.1.0
