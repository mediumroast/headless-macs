# PHASE 7 PLAN — Serving-Tool Log Management

**Status: 7A–7I all implemented, build/pass unit tests, unverified on real
hardware.** Every checklist item across this plan is now checked off except
live verification on a real box (needs physical hardware, can't be done
from here) and the "which of exo/rapid-mlx have a real verbosity flag"
question, which was resolved by reading source directly instead. What's
left, in order: confirm on doppio-1 that `sudo headless-macs baseline` and
`install-tools` still behave correctly (especially the now-content-diff
idempotency on `caffeinate`/`sysctl-tuning`/`maxfiles`/`pmset-heal`/
`logrotate` — first run should show `[SET] ... installed`, a second
identical run should show `[SKIP] ... already installed and up to date`
for every one of them), and that Verify's new checks report correctly
against a real install.

Phase 7C's verbosity flags for mlx-lm, Infinity, and Rapid-MLX have all been
Phase 7C's verbosity flags for mlx-lm, Infinity, and Rapid-MLX have all been
confirmed correct by reading each tool's actual CLI source/docs on GitHub
(`ml-explore/mlx-lm`, `michaelfeil/infinity`, `raullenchai/Rapid-MLX`) —
Rapid-MLX's plist was corrected from a speculative env-var fallback to the
real `--log-level` flag as a result. What's left: run `sudo headless-macs
install-tools` end to end on a real box (doppio-1) to confirm nothing
regressed and the new `[PASS]`/`[WARN]` checks in Verify report correctly.

**Update (same session): the Exo bug found during Phase 7C research has now
been fixed**, along with a deeper Exo logging remedy that went beyond this
phase's original scope. See "Exo — CLI and logging fixes (post-hoc addition)"
below for what changed and why. This was scope creep relative to Phase 7's
original intent (pure log management), justified by the fact that the CLI
bug meant Exo could not start at all, discovered as a direct side effect of
verifying Phase 7C's logging flags from source.

**Update (same session): Phase 7H implemented — remediation for existing
installs.** Answered "how does a box that installed tools before this
phase shipped get these fixes." Tool plists were already self-healing
(re-run `install-tools`); the shared logrotate daemon was not — fixed with
content-diff idempotency, then applied to Phase 6's four infra daemons
too (`installLaunchDaemon()` in `baseline.go`, same bug, same fix, one
shared function). The stale-config-key detector is also implemented. The
CLAUDE.md convention this contradicted has been updated to match, not
just flagged.

**Update (same session): Phase 7I implemented — Precheck/Verify
completeness.** All three gaps found on review are fixed: mlx-lm/Infinity/
Rapid-MLX now get the same `--log-level` Verify check Ollama and Exo
already had; the logrotate check now reads the config file's actual
content, not just the daemon's loaded state; and `checkHTTP`/
`checkEndpoint` now genuinely verify status code and body content instead
of passing on any successful TCP round-trip. Precheck was deliberately
left alone, with the reasoning recorded.

## Intent

Reduce unbounded log growth and startup noise across every serving-tool
LaunchDaemon (Ollama, Rapid-MLX, mlx-lm, Infinity, Exo). Ollama specifically
gets a symlink-path fix and a verbosity control; every tool gets a
verbosity flag where the underlying binary supports one; Exo's log location
bug is fixed; and one shared `logrotate`-based LaunchDaemon bounds all of
it. This consolidates FUTURES.md Items 1, 2, and 10, which were flagged as
needing to land together — Item 2's logrotate daemon and Item 10's
extension of that same daemon to the other four tools would otherwise mean
writing the rotation config twice.

---

## Scope

| Item | In | Out |
|---|---|---|
| `OLLAMA_MODELS` resolved-path fix when using external volume | ✓ | |
| `OLLAMA_LOG_LEVEL=warn` on the Ollama plist | ✓ | |
| `tools.ollama.log_level` config key (default `warn`) | ✓ | |
| Exo log directory fix (`/tmp/` → `/var/log/exo/`) | ✓ | |
| `--log-level WARNING` on mlx-lm and Infinity daemons | ✓ | |
| Rapid-MLX verbosity (flag if supported, env fallback otherwise) | ✓ | |
| Unified `logrotate` config + `com.llm-server.logrotate` LaunchDaemon covering all five tools | ✓ | |
| `verify.go` checks for verbosity/log-path/rotation-daemon state | ✓ | |
| `restore.go` cleanup of the logrotate config and daemon | ✓ | |
| Upstream Ollama `runnerLogFilter` PRs (ollama/ollama#17913, #16941) | | ✗ — not ours to implement; revisit `OLLAMA_LOG_LEVEL` handling if/when one merges |
| Lowering llama-server's own `--log-verbosity` | | ✗ — confirmed architecturally unsafe (breaks Ollama's memory-accounting parse); see FUTURES.md history |
| New serving tools beyond the current five | | ✗ |

---

## Resolved design decisions

| Question | Decision |
|---|---|
| Rotation policy | `size 100M`, `rotate 5`, `compress`, `copytruncate`, uniform across all five tools |
| Rotation mechanism | `logrotate` (Homebrew), **not** `newsyslog` — `newsyslog`'s rename-based rotation leaves launchd-managed daemons writing to the old inode (`StandardOutPath`/`StandardErrorPath` fds are opened once at daemon start and never reopened); confirmed broken on doppio-1 |
| Rotation config scope | List all five tools' log paths unconditionally in `/etc/logrotate.d/llm-servers` with `missingok` — simpler than conditionally building the list from which tools are `enabled`, and harmless for tools not installed |
| Rotation schedule | `com.llm-server.logrotate` LaunchDaemon, `StartCalendarInterval` 2 AM daily, `RunAtLoad = false` (do not rotate on boot) |
| `logrotate` service management | Do **not** use `brew services start logrotate` — Homebrew's `/opt/homebrew/etc/logrotate.conf` is owned by the brew user, and `logrotate` refuses non-root-owned config when run as root. Use the dedicated LaunchDaemon instead. |
| `OLLAMA_MODELS` scope | Resolved-path override applies only when `storage.use_external_volume` is `true`; internal-storage installs are untouched |

---

## Phases

### Phase 7A — Ollama plist fixes
**Goal:** Fix the symlink startup error and cut Ollama's own log verbosity.

- [x] In `installOllama()` / `ollamaPlist()` (`internal/ops/tools.go`), when
      `storage.use_external_volume` is true, resolve `OLLAMA_MODELS` to the
      absolute path on the external volume (`storage.volume_mount_point` +
      `storage.models_subdir` + `"ollama"`) instead of the `/Library/Ollama/models`
      symlink — implemented via `filepath.EvalSymlinks(modelsDir)` on the
      configured/default models dir rather than reconstructing the path from
      storage config fields; more robust since it works regardless of how the
      symlink got there, and requires no `/tmp/mac-llm-precheck.json` to exist
- [x] Add `OLLAMA_LOG_LEVEL` to the plist's `EnvironmentVariables`, sourced
      from a new `tools.ollama.log_level` config key, default `"warn"`
- [x] Do **not** add `LLAMA_ARG_LOG_VERBOSITY` — Ollama's hardcoded
      `--log-verbosity 4` CLI arg overrides it and produces a spurious
      startup warning (confirmed behavior, Ollama ≤ 0.33.2)
- [x] Add `"ollama": {"log_level": "warn"}` to `config.json`'s `tools` block
      (also added `OllamaTool.LogLevel` to `internal/config/config.go`)

**Files touched:** `internal/ops/tools.go`, `config.json`

---

### Phase 7B — Exo log directory fix
**Goal:** Stop Exo from logging to `/tmp/` (lost on reboot, wrong location).

- [x] `installExo()`: create `/var/log/exo/` (`os.MkdirAll(..., 0755)`)
      alongside the other tools' log directories — owned by the real
      `SUDO_USER`, not `_llmserver`: Exo's plist has no `UserName` key and
      runs as a LaunchAgent under the logged-in user's GUI session, so
      `_llmserver` ownership would have made the log directory unwritable by
      the process actually running (deviation from the plan's original
      wording, caught while implementing)
- [x] `exoPlist()`: change `StandardOutPath`/`StandardErrorPath` from
      `/tmp/...` to `/var/log/exo/stdout.log` / `/var/log/exo/stderr.log`

**Files touched:** `internal/ops/tools.go`

---

### Phase 7C — Per-tool verbosity flags
**Goal:** Give every serving tool a way to quiet its own logging.

- [x] `mlxLMPlist()`: add `--log-level WARNING` to the `mlx_lm.server`
      `ProgramArguments` — **confirmed correct** against
      `ml-explore/mlx-lm`'s `mlx_lm/server.py`: `--log-level` takes
      `["DEBUG","INFO","WARNING","ERROR","CRITICAL"]` (uppercase), default
      `INFO`. (Side finding, not fixed here: invoking it as `-m mlx_lm.server`
      hits the module's own deprecation notice — `server.py`'s
      `if __name__ == "__main__":` block prints "Calling `python -m
      mlx_lm.server...` directly is deprecated. Use `mlx_lm.server...`"
      before still calling `main()`. Cosmetic — one extra stdout line per
      start, not a functional break — but worth switching to the
      `mlx_lm.server` console-script entry point in a future pass.)
- [x] `infinityPlist()`: add `--log-level warning` to the `infinity_emb`
      `ProgramArguments` — **confirmed correct** against
      `michaelfeil/infinity`'s `docs/docs/cli_v2.md`: `--log-level` takes
      `[critical|error|warning|info|debug|trace]` (lowercase), default
      `info`, env fallback `INFINITY_LOG_LEVEL`. Infinity has no built-in
      file logging (no `FileHandler`/`RotatingFileHandler` in the repo) —
      our `StandardOutPath`/`StandardErrorPath` redirection to
      `/var/log/infinity/` is its only log surface, confirmed complete.
- [x] `rapidMLXPlist()` / `installRapidMLX()`: **corrected after this plan
      was first implemented.** `raullenchai/Rapid-MLX`'s
      `docs/reference/cli.md` confirms `rapid-mlx serve --log-level` exists
      (`DEBUG`/`INFO`/`WARNING`/`ERROR`, case-insensitive, default `INFO`).
      Replaced the speculative `PYTHONWARNINGS`/`LOGLEVEL` env-var fallback
      with the real `--log-level WARNING` flag. (Bonus finding from the same
      doc: `rapid-mlx serve` already has native `--api-key`, `--cors-origins`,
      `--trusted-hosts`, and `--rate-limit` flags and defaults `--host` to
      `127.0.0.1` — relevant context for the deferred security work in
      FUTURES.md/Item 12 whenever that's picked up, since Rapid-MLX is
      already ahead of the other four tools there.)
- [x] Confirm none of these flags change the tools' HTTP behavior — resolved
      by reading each tool's actual argparse/CLI-docs source rather than
      guessing, in place of the originally-planned live-box test (no
      live-installed instance of any of these tools was available from this
      environment). Live confirmation on doppio-1 is still worth doing before
      calling this fully done, but the flag *validity* question — the part
      that risked an outright startup failure — is now answered from source,
      not guessed.

**Files touched:** `internal/ops/tools.go`

---

### Phase 7D — Unified logrotate config and daemon
**Goal:** One `logrotate`-based LaunchDaemon bounding all five tools' logs.

- [x] During `RunTools`, ensure `logrotate` is installed via Homebrew
      (skip-if-present, matching the existing tool-install pattern — checks
      the known Homebrew opt path directly since `logrotate` is not linked
      into `PATH` by the formula)
- [x] Create `/etc/logrotate.d/` if absent (`sudo mkdir -p`; not present by
      default on macOS)
- [x] Write `/etc/logrotate.d/llm-servers` (root-owned) listing
      `stdout.log`/`stderr.log` for all five tools (`ollama`, `rapid-mlx`,
      `mlx-lm`, `infinity`, `exo`) with `size 100M`, `rotate 5`, `compress`,
      `copytruncate`, `missingok`, `notifempty`, `create 644 _llmserver wheel`
- [x] Write `/Library/LaunchDaemons/com.llm-server.logrotate.plist`
      (`ProgramArguments`: `logrotate -s /var/log/mac-llm-setup/logrotate.status
      /etc/logrotate.d/llm-servers`, `StartCalendarInterval` 2 AM,
      `RunAtLoad = false`)
- [x] `chown root:wheel` + `chmod 644` on both the config and the plist;
      `bootstrap system` the daemon (via the existing `loadDaemon()` helper)
- [x] Force an initial rotation pass (`logrotate -f -s ...`) so all logs are
      baselined the first time this phase's code runs on an existing box —
      runs only inside the write-once "plist didn't already exist" branch,
      not on every `install-tools` invocation

**Files touched:** `internal/ops/tools.go`

---

### Phase 7E — Verify checks
**Goal:** `verify.go` confirms every fix in this phase actually took.

- [x] `sectionOllama()`: check `OLLAMA_MODELS` resolves to a real path (not
      a dangling/symlinked one) when external volume is configured; check
      `OLLAMA_LOG_LEVEL` is set — implemented by reading the written plist
      XML directly (new `extractPlistString()` helper) rather than
      inspecting the running process's environment, since that's simpler
      and the plist is regenerated on every `install-tools` run anyway
- [x] `sectionExo()`: check logs are being written to `/var/log/exo/`, not
      `/tmp/`
- [x] New check (in `sectionSystem()`, alongside caffeinate/sysctl-tuning):
      confirms `com.llm-server.logrotate` is loaded
- [x] `[WARN]` (not `[FAIL]`) if the logrotate daemon isn't found — matches
      the project's convention of failing only on things that break
      inference, not on log hygiene

**Files touched:** `internal/ops/verify.go`

---

### Phase 7F — Restore cleanup
**Goal:** `restore` undoes everything this phase adds.

- [x] Bootout `com.llm-server.logrotate` and remove its plist (added to the
      existing daemon list in `sectionRemoveDaemons()`)
- [x] Remove `/etc/logrotate.d/llm-servers`
- [x] Leave `/var/log/mac-llm-setup/logrotate.status` and the rotated tool
      logs themselves in place — Restore removes daemons/config, not
      historical logs (matches existing Restore philosophy of not deleting
      data)

**Files touched:** `internal/ops/restore.go`

---

### Phase 7G — Exo CLI and logging fixes (post-hoc addition)
**Goal:** Fix the CLI-flag bug found while verifying Phase 7C from source,
and remedy Exo's two internal log surfaces that Phase 7B's `/var/log/exo`
fix didn't reach.

**CLI bug:** `installExo()`/`exoPlist()` passed `--chatgpt-api-port` and
`--discovery-module` — neither exists in current exo (`exo-explore/exo`
`main`, confirmed by searching the whole repo, zero matches for either
flag name anywhere including old tests). Exo would reject both as
unrecognized arguments and fail to start entirely. There is no
"discovery module" concept in current exo at all — peer discovery is
zenoh/libp2p-based via `--bootstrap-peers`/`--namespace`/`--zenoh-port`/
`--discovery-port`.

**Logging:** confirmed from source that Exo has three log surfaces, not the
one Phase 7B addressed:
1. Main structured log (loguru) at `<EXO_HOME>/exo_log/exo.log` — rotates
   once per process start only, unbounded within a single long run
2. Runner subprocess log at `<EXO_HOME>/exo_log/runner_log/{stdout,stderr}.log`
   (`worker/runner/supervisor.py`) — **zero rotation at all**, plain
   unbounded append forever
3. Console sink → our launchd redirect (`/var/log/exo/{stdout,stderr}.log`,
   Phase 7B) — mostly duplicates #1's content

`EXO_HOME` defaults to `~/.exo` on macOS (confirmed: `_get_xdg_dir()` in
`exo/shared/constants.py` ignores `XDG_CACHE_HOME` on any non-Linux
platform), a hidden dotfile path inconsistent with this project's
`/Library/<Tool>/` convention and invisible to normal `/var/log` triage.

- [x] `installExo()`: fixed flag names — `--api-port` (was
      `--chatgpt-api-port`), removed `--discovery-module` entirely
- [x] Added `tools.exo.bootstrap_peers []string` config key (comma-joined
      into `--bootstrap-peers` when non-empty; omitted from
      `ProgramArguments` entirely when empty, since exo's own default is no
      peers) — replaces the non-functional `discovery_module` field
- [x] `installExo()`: create `/Library/Exo`, chown to the real console user
      (`SUDO_USER:staff`, same account Exo's LaunchAgent runs as); set
      `EXO_HOME=/Library/Exo` in the plist's `EnvironmentVariables` — this
      one var relocates exo's entire config/data/cache root (both log files
      plus its node identity file, PID file, and model cache) out of
      `~/.exo` into one discoverable, project-managed path
- [x] Extended `/etc/logrotate.d/llm-servers` (Phase 7D) with a second
      stanza covering `/Library/Exo/exo_log/exo.log` and
      `/Library/Exo/exo_log/runner_log/{stdout,stderr}.log`, owned by the
      console user rather than `_llmserver` — this is the fix for the
      previously-unbounded runner log specifically
- [x] `internal/config/config.go`: `ExoTool.DiscoveryModule` → `BootstrapPeers []string`
- [x] `internal/tui/config_editor.go`: replaced the "Discovery Module" text
      field with "Bootstrap Peers (comma-separated)", backed by a
      join/split adapter over the new `[]string` field
- [x] `verify.go`: new checks — flags the old plist's stale
      `--chatgpt-api-port`/`--discovery-module` as `[FAIL]` if present
      (catches a box that hasn't re-run Install Tools since this fix), and
      `[PASS]`/`[WARN]` on whether `EXO_HOME` is set in the running plist
- [ ] Live verification on a real box — same caveat as the rest of Phase 7:
      confirmed correct from source, not yet run against a live `exo`
      process. In particular, `copytruncate` safety for loguru's
      `enqueue=True` file sink and for `anyio.open_file(path, "a")` is
      inferred from POSIX append-mode semantics, not observed directly.

**Files touched:** `internal/ops/tools.go`, `internal/ops/verify.go`,
`internal/config/config.go`, `internal/tui/config_editor.go`, `config.json`

---

### Phase 7H — Remediation for existing installs
**Goal:** every fix in 7A–7G reaches a box that installed tools *before*
this phase shipped, once the operator re-runs the normal commands — with
no special "migration" step needed. This phase's own code currently
promises less than that, in one specific place.

**Tool plists need no new work — they're already self-healing.** Ollama,
Rapid-MLX, mlx-lm, Infinity, and Exo's plists are all unconditionally
rewritten on every `install-tools` run (the existing tool-install pattern
from `CLAUDE.md`: "The plist is always re-written even if it exists").
This means the actively-broken pre-Phase-7G Exo plist, or an old
Ollama plist missing `OLLAMA_LOG_LEVEL`, both get fixed automatically the
next time an operator runs `sudo headless-macs install-tools` — no
migration logic required. The remediation instruction is simply that one
command, and every `[WARN]`/`[FAIL]` Verify check added in this phase
should name it exactly (spot-checked above; all do).

**The logrotate daemon is a real gap, found while writing this section —
not hypothetical.** `installLogRotate()` follows CLAUDE.md's documented
"write-once idempotent" pattern for infrastructure daemons: it skips
entirely if `/Library/LaunchDaemons/com.llm-server.logrotate.plist`
already exists. But this phase's own Phase 7D and Phase 7G both write
into that same plist's config — Phase 7G added the Exo log-rotation
stanza *after* Phase 7D's version already existed as code. **Any box that
ran `install-tools` between those two points now has a
`com.llm-server.logrotate` that will never be updated, ever, no matter how
many times `install-tools` is re-run** — the file exists, so the guard
skips, permanently. Existence-based idempotency is the wrong tool here;
it was fine for Phase 6's infra daemons (`caffeinate`, `sysctl-tuning`,
`maxfiles`, `pmset-heal`, `storage-mount`) only because none of their
content has ever changed since — the same landmine exists for all five,
latent, if any of them ever does.

- [x] Changed `installLogRotate()`'s idempotency check from "does the plist
      file exist" to "does the plist/config content on disk match what
      current code would generate." Both the config file
      (`/etc/logrotate.d/llm-servers`) and the plist are now compared
      independently and only rewritten when different; the plist reload
      (`bootout` + `bootstrap`) only fires when the plist itself changed,
      and the forced initial rotation baseline only fires on a genuinely
      fresh install (not on every content tweak).
- [x] **Also applied to Phase 6's infra daemons, same session, since it's
      the same shared helper.** `installLaunchDaemon()` in
      `internal/ops/baseline.go` — used by `caffeinate`, `sysctl-tuning`,
      `maxfiles`, and `pmset-heal` — had the exact existence-only pattern
      CLAUDE.md documented as canonical. Fixed identically: content
      comparison, reload only on an actual change. `storage-mount`
      (`internal/ops/storage.go`) turned out to already always rewrite its
      plist unconditionally on every Storage Setup run — it was
      miscategorized as write-once in this plan's earlier draft; no fix
      needed there.
- [x] **Updated the CLAUDE.md convention to match**, rather than leaving
      it contradicting the code. "Idempotency guard for infrastructure
      daemons" now documents content comparison with the Go pattern shown
      inline, and explicitly deprecates the old bash existence check.
- [x] Added a generic stale/removed config-key detector to `RunPrecheck`
      (`internal/ops/precheck.go`, new `checkConfigKeys()`): derives the
      set of valid key paths from `config.Config`'s own zero-value JSON
      marshal (`knownConfigKeyPaths()`/`collectKeyPaths()`) rather than a
      hand-maintained list, so it stays in sync with the struct
      automatically. `renamedConfigKeys` gives `tools.exo.discovery_module`
      a specific explanation; anything else unrecognized gets a generic
      `[WARN]`. Verified against a synthetic stale config (temporary
      throwaway test, not committed) — correctly flagged both the known
      rename and an arbitrary made-up key, without flagging any real
      field.
- [x] Confirmed every new-in-Phase-7 config field already degrades safely
      when absent from an old `config.json`: `tools.ollama.log_level` →
      Go zero-value `""` → code defaults to `"warn"`;
      `tools.exo.bootstrap_peers` → zero-value `nil` slice → `--bootstrap-peers`
      is simply omitted, matching exo's own single-node default. No config
      field added in this phase requires migration on its own — treat "safe
      zero-value default" as a hard requirement for every field added in
      future phases too, not just a nice-to-have.

**Files touched:** `internal/ops/tools.go` (content-diff idempotency for
`installLogRotate()`), `internal/ops/baseline.go` (same fix for
`installLaunchDaemon()`), `internal/ops/precheck.go` (stale-key detector),
`CLAUDE.md` (infra-daemon idempotency convention, updated to match).

---

### Phase 7I — Precheck/Verify completeness (found on review, not yet implemented)
**Goal:** answer "does Precheck or Verify need more changes for what this
phase already did" directly, rather than assuming the checks added in 7E
are complete just because they exist.

**Verify gap — verbosity-flag coverage is inconsistent.** 7E added checks
for Ollama's `OLLAMA_LOG_LEVEL` and Exo's plist sanity, but **not** for the
`--log-level` flags Phase 7C added to mlx-lm, Infinity, and Rapid-MLX —
`sectionMLXLM()`, `sectionInfinity()`, and `sectionRapidMLX()` have no
check confirming those flags are actually present in the running plist.
Three of the five tools this phase touches for logging have no Verify
coverage for that specific fix.

- [x] Added `plistHasArg(plist, arg string) bool` and a shared
      `checkLogLevelFlag(section, plistPath)` wrapper around it, wired into
      `sectionRapidMLX()`, `sectionMLXLM()`, and `sectionInfinity()` —
      matching the rigor already applied to Ollama and Exo.

**Verify gap — the logrotate daemon check doesn't look at the config file's
content.** 7E's check confirms `com.llm-server.logrotate` is loaded, but
not that `/etc/logrotate.d/llm-servers` actually lists every path it
should. This matters specifically because of the Phase 7H bug above: a box
can have the daemon "present" (so this check passes) while running against
stale config content missing the Exo stanza added later in the same phase.
A content check would make that specific failure mode visible in Verify
*before* Phase 7H's fix ships, not just after.

- [x] Added a content check in `sectionSystem()`, right after the existing
      daemon-presence check: reads `/etc/logrotate.d/llm-servers` and
      confirms all thirteen expected paths are present, `[WARN]` naming
      exactly which are missing if any aren't found.

**Cross-cutting gap, surfaced while reviewing Phase 9's needs but affecting
every tool: `checkHTTP()` (`verify.go`) and `checkEndpoint()` (`tools.go`)
don't check HTTP status code or response body at all** — they pass on any
successful TCP round-trip, full stop. `checkEndpoint()` even still has an
unused `_ string` parameter sitting exactly where a match pattern would go
(`internal/ops/tools.go:1120`) — a placeholder from the original bash
`check_endpoint`/`check_http "name" "url" "pattern"` contract (documented
in `CLAUDE.md`'s verify.sh contract) that was never wired up when this was
ported to Go. This is pre-existing (not caused by Phases 7–9), but Phase
9's own plan already assumes it (FUTURES.md's Item 11 scope explicitly
specifies `check_http "macmon" ".../json" "cpu_power"`) — so it needs
fixing no later than Phase 9, not left as a latent gap.

- [x] Extended `checkHTTP()` (`verify.go`) and `checkEndpoint()`
      (`tools.go`) to take a real `pattern` parameter: status code checked
      (200–299), `pattern == "."` means "any non-empty body" (preserving
      the exact bash `grep .` idiom three of `checkEndpoint()`'s existing
      call sites already passed and relied on — a naive literal-period
      `strings.Contains` would have broken those, since a JSON body isn't
      guaranteed to contain an actual `.` character), `pattern == ""`
      skips content checking, anything else must appear verbatim in the
      body. Backfilled to all five of `verify.go`'s existing `checkHTTP`
      calls in the same pass — Ollama checks for `"models"`, the other
      four use `"."` (matching what `tools.go`'s install-time checks for
      the same tools already use). Verified with a throwaway table-driven
      test against `httptest` servers (5 cases: real pattern match, `.`
      idiom against a real body, `.` against an empty body, a
      deliberately-wrong pattern, and a 500 status) — all five behaved as
      designed; test was not committed.

**Precheck — deliberately no change recommended.** Considered adding
Exo-plist-staleness detection (the old `--chatgpt-api-port`/
`--discovery-module` check) to `RunPrecheck` as well as `RunVerify`, and
rejected it: Precheck's role is pre-installation gating on raw system
state ("should I proceed"), not auditing an existing headless-macs
install's health, which is what Verify exists for. An operator
troubleshooting an existing box runs Verify, not Precheck again — the
Phase 7E fix is already in the right place. Stating this explicitly so
it reads as a considered decision rather than an oversight.

**Files touched (when implemented):** `internal/ops/verify.go` (verbosity
parity checks, logrotate content check, `checkHTTP` pattern support),
`internal/ops/tools.go` (`checkEndpoint` pattern support, for symmetry).

---

## Files-touched summary

| File | Change |
|---|---|
| `internal/ops/tools.go` | Ollama `OLLAMA_MODELS`/`OLLAMA_LOG_LEVEL`, Exo log path + CLI-flag fix + `/Library/Exo`/`EXO_HOME`, mlx-lm/Infinity/Rapid-MLX verbosity flags, logrotate config + daemon install (content-diff idempotent, two ownership stanzas), `checkEndpoint` pattern-match support |
| `internal/ops/verify.go` | New checks: Ollama resolved-path + log level, Exo log location + plist-flag sanity + `EXO_HOME`, logrotate daemon running + config content, mlx-lm/Infinity/Rapid-MLX `--log-level` parity, `checkHTTP` pattern-match support |
| `internal/ops/baseline.go` | `installLaunchDaemon()` changed to content-diff idempotency (fixes caffeinate/sysctl-tuning/maxfiles/pmset-heal alongside logrotate) |
| `internal/ops/restore.go` | Remove logrotate config + daemon |
| `internal/ops/precheck.go` | Stale/removed config-key detector (`checkConfigKeys`, `knownConfigKeyPaths`, `collectKeyPaths`) |
| `internal/config/config.go` | New `tools.ollama.log_level`; `ExoTool.DiscoveryModule` replaced with `BootstrapPeers []string` |
| `internal/tui/config_editor.go` | Exo config field updated to match |
| `config.json` | New `tools.ollama.log_level`; `tools.exo.discovery_module` → `tools.exo.bootstrap_peers` |
| `CLAUDE.md` | Infra-daemon idempotency convention updated to content-diff, matching the code |

---

## Open questions

- Does `rapid-mlx serve` accept a `--log-level` flag? Needs checking against
  the actual CLI (`rapid-mlx serve --help`) before Phase 7C is implemented —
  if not, use the env-var fallback documented above.
- Does the `exo` CLI accept any verbosity flag? FUTURES.md Item 10 flagged
  this as unconfirmed; check during Phase 7C and drop the flag if none
  exists (env-only logging is out of scope if Exo has no equivalent).
- **(Phase 7H)** Should the content-diff idempotency fix extend to Phase 6's
  other write-once infra daemons (`caffeinate`, `sysctl-tuning`, `maxfiles`,
  `pmset-heal`, `storage-mount`) now, pre-emptively, or only if/when one of
  them actually changes content in a future phase? Doing it now for all six
  is more consistent; doing it only for `logrotate` is smaller and scoped to
  the actual current bug. Leaning toward "all six, same phase" since it's
  the same helper function either way — decide at implementation time.
- **(Phase 7H)** Does the stale-key detector belong in `RunPrecheck` (runs
  before every operation, read-only, matches its existing role) or as part
  of `config.Load()` itself (catches it even if an operator only runs
  `verify`/`status` and never precheck again after initial setup)? Current
  lean is Precheck, since that's already the project's designated
  "diagnose config problems" surface — but Precheck is not on every code
  path (e.g., `install-tools` can run without a fresh Precheck first), so a
  warning could be missed. Worth reconsidering whether `config.Load()`
  itself should surface this so it's unmissable regardless of which command
  runs first.
