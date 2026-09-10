# PHASE 11 PLAN — Bug fixes (Issues #13–#17) + Debugging Log Tools

**✅ IMPLEMENTED, mostly live-verified on doppio-1.** All six phases
(11A–11F) are committed on `claude/phase-11-issue-fixes`.
`go build ./...`, `go vet ./...`, `go test ./...`, and `make build`
(full go:embed chain) are all clean. Issues #13–#17 are closed with
fix-summary comments. Live testing on doppio-1 found and fixed several
real bugs beyond the original diagnoses (see Phase 11E and the SSH
liveness check in Phase 11C — both root-caused and fixed with evidence,
not guessed) and confirmed the coreaudiod suppression and full
NOPASSWD/`headless-macs-debug` flow (enable, disable, and non-interactive
SSH end to end) working correctly. Not yet merged to main — still open:
the config-editor teardown confirmation flow hasn't been live-tested
(see the closeout section below), doppio-2 hasn't been tested at all,
and the PR description update is pending until those are done.

---

## Why these five issues are grouped into one phase

All five are independently-diagnosed, already-filed bugs
([#13](https://github.com/mediumroast/headless-macs/issues/13),
[#14](https://github.com/mediumroast/headless-macs/issues/14),
[#15](https://github.com/mediumroast/headless-macs/issues/15),
[#16](https://github.com/mediumroast/headless-macs/issues/16),
[#17](https://github.com/mediumroast/headless-macs/issues/17)) — no new
diagnosis happens in this plan, only fix design. They're bundled into one
phase/branch because they're small, don't touch each other's code, and
none individually justifies its own branch/PR/release cycle. Per
`docs/RELEASE_STRATEGY.md`'s versioning rules, Phases 11A–11E are
individually **Patch**-level (bug fixes only) and 11F is **Minor** (new
subcommand). Per user decision, the whole phase ships together as one
**`v2.3.0`** release — the Minor bump from 11F's new feature governs the
combined version, same as how `v2.2.0` absorbed Phases 7–10 together.

---

## Scope decision table

| In scope | Out of scope |
|---|---|
| Fix issues #13–#17 as diagnosed | Any new bug-hunting beyond what's already filed |
| New `debug-logs` capability (Phase 11F) — rotate-on-demand + capture bundle script, install/update path, TUI + CLI surface | A push-based log-shipping/upload mechanism (this stays pull-only, via `scp`, per the user's ask) |
| Deciding wheel-vs-`_llmserver` group question (recommendation given, needs user approval) | Actually changing `/var/log/*` permissions before that decision is made |
| Confirming real plist paths for issue #14's three services before writing the fix | Broader service-suppression audit beyond the three named services |

---

## Phase 11A — Issue #13: Config editor save/persistence + tool teardown

**Recap of diagnosis (already filed, not re-litigated here):** no smoking-gun
bug found in the toggle→save code path via static reading; the missing
`macmon` section is best explained as a downstream symptom of `Save()`
never having successfully run. Separately confirmed: `RunTools()` has no
teardown path for a tool transitioning enabled→disabled, and `RunRestore()`
only supports removing everything, not one tool.

- [x] **Auto-migrate on load, independent of root-causing the save bug.**
      `config.Load()` (`internal/config/config.go`) should detect when the
      loaded file is missing sections/keys present in the current `Config`
      struct (compare against a freshly-`Bootstrap`'d template, or simpler:
      just always re-`Save()` once immediately after a successful `Load()`
      if the re-marshaled bytes differ from what was read) and persist the
      patched version back to disk. This directly fixes symptom #2
      (macmon section never appearing) regardless of whether #1's root
      cause is ever fully pinned down.
- [x] **Add a post-save verification step** in `config_editor.go`'s `s`/`S`
      handler: after `config.Save(m.cfg)`, re-read the file and compare
      against what was just written; if they don't match, surface a
      `[FAIL]`-equivalent error state in the UI instead of silently
      reporting `[saved]`. This won't fix an unknown root cause, but it
      stops the editor from *lying* about save state, which is the
      sharper edge of the original complaint.
- [x] **Tool-disable confirmation screen**, reusing the existing
      `restore_confirm.go` pattern: when Save detects an `enabled` flag
      transitioned `true` → `false` (diff working copy against
      `origJSON`, which the model already tracks), route through a
      confirmation screen offering:
      1. Stop and uninstall the daemon now (`launchctl bootout` + plist
         removal), leaving model/data directories untouched
      2. Save the config change only; daemon stays running until the next
         `Install Tools` run or reboot
      3. Cancel — leave `enabled` as `true`
      Default/pre-selected option should be (2), the least destructive,
      not (1).
- [x] New `internal/ops` function for per-tool teardown (name TBD —
      `RunDisableTool(cfg *config.Config, tool string)`?), factored out of
      `RunRestore()`'s existing per-tool bootout logic rather than
      duplicated, so the suppress/restore/disable-one-tool paths share one
      source of truth the same way `phase8Suppressions` already does for
      service suppression.

**Files touched:** `internal/config/config.go` (auto-migrate-on-load),
`internal/tui/config_editor.go` (post-save verification, confirm-screen
routing), `internal/tui/restore_confirm.go` or a new sibling file (reused
confirm pattern), `internal/ops/restore.go` (factor out per-tool teardown),
possibly a new `internal/ops/disable.go`.

---

## Phase 11B — Issue #14: coreaudiod / audiomxd / AirPlayXPCHelper never stop

**Recap:** `disableService()` only runs `launchctl disable`, which
prevents future starts but doesn't stop an already-running process. Three
of seven `phase8Suppressions` entries have `Plist: ""`, which skips the
`bootout` call every other entry gets.

- [x] **Confirmed on both doppio-1 and doppio-2** (identical results on
      both, via `launchctl print` and a direct `find` cross-check — real
      plist paths, not guessed): all three services have ordinary static
      system LaunchDaemon plists, the same shape as the other four
      `phase8Suppressions` entries. No enum/union or label-only bootout
      fallback needed — the simple case applies.
      ```
      com.apple.audio.coreaudiod  → /System/Library/LaunchDaemons/com.apple.audio.coreaudiod.plist
      com.apple.audiomxd          → /System/Library/LaunchDaemons/com.apple.audiomxd.plist
      com.apple.AirPlayXPCHelper  → /System/Library/LaunchDaemons/com.apple.AirPlayXPCHelper.plist
      ```
- [x] Fill in `Plist` for these three entries in `phase8Suppressions`
      (`internal/ops/baseline.go`) with the paths above — the existing
      `bootout` call in the suppression loop then covers them with no
      other code change needed; this is now a one-line-per-service fix.
- [x] Confirm `coreaudiod` doesn't immediately respawn after a bare
      `bootout` without `disable` going first — **confirmed live on
      doppio-1**: a Verify run after Baseline showed
      `[PASS] com.apple.audio.coreaudiod not running`, on a separate
      invocation from Baseline itself (not just momentarily suppressed).
- [x] Update `verify.go`'s corresponding checks — **confirmed live**,
      same Verify run above showed all three (`coreaudiod`, `audiomxd`,
      `AirPlayXPCHelper`) passing immediately after a Baseline run.

**Files touched:** `internal/ops/baseline.go` (`phase8Suppressions` list,
possibly its shape).

---

## Phase 11C — Issue #15: SSH section trusts exit codes, never confirms state

**Recap:** `sectionSSH()` declares `[SET]` from subprocess exit codes
alone; the `systemsetup` fallback discards its own exit code entirely.
`verify.go` does the real check (`launchctl print` parsed for
`state = running`/`waiting`) and correctly disagrees.

- [x] Extract `verify.go`'s SSH-state check (`launchctl print
      system/com.openssh.sshd`, parsed for `state = running`/`waiting`)
      into a shared helper both `baseline.go` and `verify.go` call —
      single source of truth, can't drift apart again.
- [x] `sectionSSH()` calls the shared check *after* attempting
      `enable`+`kickstart` (and after the `systemsetup` fallback, if that
      path is taken) and reports `[SET]` only if the check confirms
      success; otherwise `[WARN]` with the same fix hint `verify.go`
      already prints, so the two commands never again tell the operator
      different stories about the same thing.
- [x] Stop discarding the `systemsetup -setremotelogin on` fallback's
      exit code — `CLAUDE.md` already documents this path as broken on
      macOS 26 Tahoe; if the check above still applies afterward, the
      discarded exit code stops mattering functionally, but silently
      swallowing errors elsewhere in the same function is worth cleaning
      up while in this code.

**Files touched:** `internal/ops/baseline.go` (`sectionSSH()`),
`internal/ops/verify.go` (extract shared check).

---

## Phase 11D — Issue #16: `update-tools` has no macmon support

**Recap:** `RunUpdateTools()` has a branch for every tool except macmon;
`installMacmon()` also never upgrades an existing install, only installs
if absent.

- [x] Add `updateMacmon()` to `internal/ops/update.go`, following the
      existing per-tool pattern (stop daemon → `brew upgrade macmon` →
      re-bootstrap → confirm via the same endpoint check `installMacmon()`
      already uses). Needs a way to detect "is a newer version available"
      before upgrading (`brew outdated macmon`, or just always run
      `brew upgrade macmon` and let Homebrew no-op if already current —
      simpler, matches how the other tools' update functions already
      behave by re-running their installer unconditionally).
- [x] Add the `if cfg.Tools.Macmon.Enabled { r.updateMacmon() }` branch to
      `RunUpdateTools()` alongside the existing five.
- [x] `docs/tool-comparison.md`'s macmon section already tells operators
      to `brew upgrade macmon && re-run install-tools` for the `--host`
      flag limitation — once this lands, that instruction becomes
      literally accurate via `update-tools` too; consider updating the
      doc to recommend `update-tools` instead/in addition.

**Files touched:** `internal/ops/update.go`, `docs/tool-comparison.md`
(doc accuracy follow-up).

---

## Phase 11E — Issue #17: Precheck/Verify scroll index mismatch

**Recap:** `m.scroll`'s bounds are computed from `checkCount()` (raw check
items) but used as an index into `renderChecks()`'s output (which also
contains section-header and blank-separator rows, plus a second row per
check with a `Detail`) — the two counts diverge, underselling the "N more
above" indicator and potentially making the tail of a long list
unreachable on short terminals. The reported visual symptom (title bar
disappearing while scrolling) is **not** confirmed to be caused by this —
flagged in the issue as needing live reproduction.

- [x] Cache `renderChecks()`'s output on the model (`cachedRows []string`)
      whenever `m.result`/`m.verifyResult` changes, rather than
      recomputing it ad hoc — this also avoids the current design's
      implicit assumption that `renderChecks()` is cheap/stable to call
      repeatedly per frame.
- [x] Bound `m.scroll` (in all four key handlers — `up`/`down`/`pgup`/`pgdn`)
      and the `"↑ N more above"` count against `len(m.cachedRows)`, not
      `m.checkCount()`.
- [x] **After the fix, verify live** — **confirmed on doppio-1, but the
      original scroll-index fix alone was not sufficient.** Two more real
      bugs surfaced and were fixed after this one, on the same branch:
      (1) `run_screen.go` (Baseline/Storage/Tools/Restore/Update/Debug
      Tools) had the identical scroll-bounds bug in a separate,
      near-duplicate screen implementation this fix never touched —
      fixed the same way. (2) The actual root cause of the header
      disappearing on Verify was a real lipgloss bug, not a terminal
      quirk: `Style.Render()` with `Background()` set, given a string
      with an embedded trailing `\n`, pads a synthetic second line with
      spaces and no closing newline, merging the "N more above/below"
      indicator into whatever got written next — reproduced directly
      against this repo's lipgloss dependency, fixed by moving the `\n`
      outside the styled `Render()` call in both files (4 call sites).
      All three fixes are live-confirmed working together on doppio-1.

**Files touched:** `internal/tui/precheck_screen.go`.

---

## Phase 11F — Debugging Log Tools (new capability)

**Goal, in the user's own framing:** be able to force a log rotation and
pull ("capture") the result for any managed service over a plain SSH/SCP
workflow, without needing the full TUI, and without needing to hand-parse
live (unrotated) log files that a daemon still has open.

### Design questions asked, answered here

**1. Rotate (SSH) and capture (SCP) logs for any service.**
Recommend a standalone script (not a new Go subcommand alone — see Q4)
that:
- Forces an out-of-cycle rotation using the **existing** shared logrotate
  config (`/etc/logrotate.d/llm-servers`, already covering every tool's
  `stdout.log`/`stderr.log` plus Exo's two extra log files — see Phase 7):
  `sudo logrotate -f -s /var/log/mac-llm-setup/logrotate.status
  /etc/logrotate.d/llm-servers`. Reuses the config that's already the
  single source of truth for what gets rotated — does not reimplement
  rotation logic.
- Bundles the rotated logs (plus `/var/log/mac-llm-setup/` itself — the
  ops-log directory Precheck/Baseline/Verify/etc already write to) into
  one timestamped `tar.gz` at a predictable path, e.g.
  `/var/log/mac-llm-setup/bundles/debug-<label>-<timestamp>.tar.gz`,
  where `<label>` optionally narrows to one tool (`ollama`, `exo`, etc.)
  or defaults to everything.
- Prints the resulting path at the end, so "capture via SCP" is just the
  operator running `scp` themselves afterward — this script never pushes
  anywhere itself, matching the pull-only, no-new-network-surface posture
  the rest of this project already has.

**2 & 3. A non-root operator user added to a group, `/var/log/<service>`
made group-actionable so that group can work with the logs.**

**Correction to the original recommendation in this plan** — checked
against the actual logrotate config generated in
`internal/ops/tools.go` (`installLogRotate()`, Phase 7) rather than only
against directory-level `chown` calls as the first draft did: the
`create 644 _llmserver wheel` line already governs the *files* logrotate
produces on Ollama/Rapid-MLX/mlx-lm/Infinity's rotated logs — `wheel` is
already the coordination group `root` (which runs logrotate) and
`_llmserver` (which owns the live daemons) meet on today, specifically
because root needs to create/touch files during rotation that an
`_llmserver`-run daemon can keep writing to afterward. Exo's stanza uses
`wheel` or `staff` depending on how it's installed, for the same
root/non-`_llmserver`-owner reason. So `wheel` isn't a foreign concept
being introduced here — it's already load-bearing in the rotation
framework, and directory-level ownership (`_llmserver:_llmserver`, set
at daemon-install time) is a separate, narrower thing from the
rotated-file group logrotate itself assigns.

**Finding that changes the plan again, confirmed on both boxes:** the
*live* (not-yet-rotated) `stdout.log`/`stderr.log` files are already
`_llmserver:wheel 644` — `rw-r--r--`, world-readable — not just the
directory. So there is, right now, **no read-access gap to solve at all**:
any user on the box can already read Ollama's logs, live or rotated,
today. This wasn't obvious from the directory-level check alone (which
only confirmed the directory itself, `755`, was already permissive) —
the file-level check was the piece that actually settles it.

- [x] **No group/permission toggle needed for read access.** Dropped
      from scope. `headless-macs-debug logs` doesn't need to change any
      permission or add anyone to any group to let an operator read or
      `scp` a log — that already works. `644` grants *read*, not *write*
      — `logrotate -f`'s `copytruncate` needs to truncate the original
      file in place, which does need write access, so this doesn't fully
      dispose of the privilege question — see the write-access design
      below.

**The real remaining question: how does a non-interactive SSH session
get the privilege to run `logrotate -f` (which needs write access root
already has, that plain `644`-world-readable doesn't grant) without an
interactive sudo password prompt?** Discussed three options directly
with the user; decision made:

- **Ruled out: widening group write access.** Group `wheel` today only
  has *read* (`644`). Granting the group write access so any
  `_llmserver:wheel`-group member could truncate live logs is a bigger
  and stranger blast radius than either option below — any group member
  could rotate/corrupt any service's live logs at any time, not just the
  one operator running the debug tool.
- **Ruled out: setuid-root binary.** Real option, but: (a) **macOS's
  kernel ignores the setuid bit on interpreted scripts** (any `#!`
  shebang script) — this has been standard Unix kernel behavior since
  the 1990s, closing a well-known TOCTOU race-condition exploit class —
  so this path would have required `headless-macs-debug` to be a
  compiled binary regardless of the separate binary-vs-script decision
  below; (b) a setuid-root binary is a classic, well-studied
  privilege-escalation surface — any bug in it (path handling, argument
  injection, a trusted environment variable) becomes a root exploit for
  anyone who can execute it, and it loses the accountability trail
  `sudo`'s own logging provides (who ran what, when) unless the binary
  does its own logging. A meaningfully bigger security commitment than
  anything else in this project, which has deliberately kept every
  daemon unprivileged (`_llmserver`, not root) specifically to avoid this
  class of risk.
- [x] **Adopted: narrowly-scoped passwordless (`NOPASSWD`) sudo,
      toggleable.** A `sudoers.d` drop-in granting a specific operator
      user `NOPASSWD` access to *exactly* one command (not blanket
      `NOPASSWD: ALL`) — the standard, well-trodden Unix pattern for
      "let this one automated action run as root without an interactive
      prompt." Every invocation still shows up in `sudo`'s own audit log
      tied to the real invoking user — accountability preserved, unlike
      setuid. Reversible: removing the drop-in file fully revokes it.
      **Must be toggleable from the TUI** (user requirement) and **must
      be documented in `README.md`** as an explicit escalation/
      de-escalation path — see the new checklist items below.
- [ ] **Not independently verified for every tool's write-side
      requirement** — same caveat as before: checked against Ollama
      specifically, the mechanism (shared logrotate config) is identical
      across tools, low-risk to leave unverified for the other four.
      **Confirmed live for Ollama only** — full rotate → bundle → sudo
      NOPASSWD over non-interactive SSH → `scp` flow all confirmed on
      doppio-1, including the NOPASSWD enable/disable round-trip (both
      directions verified: granted access works, revoked access
      correctly goes back to requiring a password). The other four tools
      (`rapid-mlx`, `mlx-lm`, `infinity`, `exo`) remain untested — none
      are enabled on the boxes available for testing. README now says so
      explicitly under Debugging Tools.
      **Also found and fixed along the way, not originally anticipated:**
      the full-path-vs-bare-name gotcha for `headless-macs-debug` over
      non-interactive SSH (a non-login shell's `$PATH` typically excludes
      `/usr/local/bin`) — documented in README with the correct
      full-path invocation.

**4. Install location, distribution, and menu option.**

**Finalized per user direction:** `headless-macs-debug` is a **small
compiled Go binary**, not a shell script — this was already the forced
outcome if setuid had been chosen (macOS ignores setuid on scripts), and
the user has now chosen it directly regardless, so the binary-vs-script
open question from earlier is resolved. Named `headless-macs-debug`
(not `headless-macs-debug-logs`) — room for more subcommands beyond
`logs` later, rather than a single-purpose name needing renaming the
moment a second debugging function shows up.

**Distribution architecture (new decision, needed now that it's a real
binary, not a string constant):** a second `cmd/` entry,
`cmd/headless-macs-debug/main.go`, built as its own artifact by the
`Makefile`. To keep single-binary distribution for the *operator*
(today, copying just `headless-macs` to a target box is enough — that
should stay true), the main `headless-macs` binary should `//go:embed`
the compiled `headless-macs-debug` binary's bytes at build time (Go's
`embed` package, same major-version toolchain this project already
requires) and `RunDebugTools()` writes those embedded bytes out to
`/usr/local/bin/headless-macs-debug` + `chmod +x` when installed/updated.
This needs `Makefile` changes to build `cmd/headless-macs-debug` *before*
`cmd/headless-macs` (embed source must exist at build time) — a real,
non-trivial build-ordering change worth flagging, not a one-line addition.

- [x] `headless-macs-debug logs` — the rotate+bundle capability from Q1,
      runnable with no other flags for the "just rotate everything and
      tell me if it worked" default the user asked for: rotate via the
      existing shared logrotate config, bundle into the timestamped
      `tar.gz`, print the path, exit 0/non-zero for success/failure (and
      only that — no interactive prompts, so it works cleanly over a bare
      `ssh host headless-macs-debug logs`).
- [x] **Permission check before doing anything**: confirm the invoking
      user can actually run `logrotate` as root — either already root, or
      covered by the `NOPASSWD` grant below — before attempting anything,
      and fail fast with a clear message (naming the `debug-tools`
      command to enable escalation) if not.
- [x] New `internal/ops/debugtools.go` — `RunDebugTools(cfg *config.Config)`
      — installs/updates the embedded `headless-macs-debug` binary
      (`[SET]`/`[SKIP]` by content comparison, same idempotency pattern as
      `installLaunchDaemon`), **and** syncs the `NOPASSWD` sudoers grant to
      match a config-driven toggle (see below) — one function covering
      both halves of "Install/Update Debugging Tools."
- [x] **`NOPASSWD` toggle, config-driven** (recommended design — matches
      this project's existing "config declares intent, an apply step
      realizes it" model, e.g. `tools.X.enabled`, rather than inventing a
      new interaction pattern): a new `debug.sudo_nopasswd_enabled` (name
      TBD) boolean in `config.json`, editable in the existing Edit Config
      screen like any other boolean — reuses that screen's already-built
      toggle UI rather than a new one. `RunDebugTools()` reads this on
      every run and syncs `/etc/sudoers.d/headless-macs-debug` to match:
      writes a narrowly-scoped grant (`Cmnd_Alias` pinned to the literal
      `/usr/local/bin/headless-macs-debug` path, validated with
      `visudo -c` before installing — a malformed sudoers file is a real
      way to break `sudo` system-wide, this check is not optional) when
      `true` and the file is missing; removes it when `false` and present.
      **Per user decision:** the target username is not stored in
      `config.json` at all — prompted interactively at the moment
      `debug-tools` applies an `enable`, and validated (`id <username>`
      or `dscl . -read /Users/<username>`) before the sudoers drop-in is
      written; refuses clearly, writes nothing, if the user doesn't
      exist. `disable` needs no username — it just removes the drop-in.
- [x] **`README.md` documentation of the escalation/de-escalation path**
      (explicit user requirement) — a new section (near the existing
      "Security scope" callout, matching its tone) explaining: exactly
      what `NOPASSWD` access is granted and to which single command, why
      (`headless-macs-debug logs` needs to run non-interactively over
      SSH), how to check whether it's currently enabled, how to disable
      it, and the honest tradeoff (narrowly scoped to one exact binary
      path, not blanket root access — but still real elevated access,
      enable it deliberately, not by default).
- [x] New CLI subcommand `headless-macs debug-tools` (installs/updates
      `headless-macs-debug` and syncs the sudoers grant to the config
      toggle's current state — one command covers both, since the
      config edit already happened in Edit Config), alongside the
      existing `precheck`/`baseline`/`install-tools`/etc. in
      `cmd/headless-macs/main.go`.
- [x] New TUI sidebar entry — user's suggested framing "Install/Update
      Debugging Tools" (exact label TBD, needs to fit the sidebar's width
      budget — see `internal/tui/menu.go`'s existing items for the
      established naming length/style) — `internal/tui/menu.go`
      (`menuItems` list). **Revised per the interactive-username
      decision above:** this can no longer be a pure `RunScreen` reuse
      when enabling the toggle — it needs a short text-input step first
      (reusing `config_editor.go`'s existing text-edit-mode UI, not a new
      pattern) to collect and validate the username, *then* runs and
      reports via the same `RunScreen`-style `[SET]`/`[SKIP]`/`[WARN]`
      list every other action screen already uses, since `RunDebugTools()`'s
      output is the same `[SET]`/`[SKIP]`/`[WARN]` action-list shape every
      other `ops` function already produces. Disabling the toggle needs no
      username and can stay a plain `RunScreen` reuse throughout.

**Scope:** New capability — Minor version bump (new subcommand, new
`cmd/` build target, new `config.json` key, new sudoers-file management).

**Files touched:** new `cmd/headless-macs-debug/main.go`, new
`internal/ops/debugtools.go`, `internal/config/config.go` (new
`debug.sudo_nopasswd_enabled` key), `internal/tui/config_editor.go` (new
toggle field), `cmd/headless-macs/main.go` (new subcommand + usage text,
`//go:embed` directive), `internal/tui/menu.go` (new sidebar item),
`internal/tui/app.go` (new screen routing, reusing `RunScreen`),
`Makefile` (build-order change for the new `cmd/` target), `README.md`
(escalation/de-escalation documentation), `docs/known-issues.md` and/or
a new `docs/debugging-guide.md` (usage docs).

---

## Open questions

**Answered:**

1. ~~Phase 11F group/permission model~~ — **resolved, and simpler than
   expected**: confirmed on both boxes that live (not just rotated) log
   files are already `_llmserver:wheel 644` — world-readable. No
   read-access gap exists to toggle. Dropped the debug-access
   enable/disable design entirely; the only privilege
   `headless-macs-debug logs` actually needs is root/`sudo` to run
   `logrotate` itself, checked once up front (see Phase 11F's revised Q4).
2. ~~Version number~~ — **resolved: `v2.3.0`**, one combined release
   (Phases 11A–11E's fixes ship alongside 11F's new feature), matching
   the `v2.2.0` precedent of Phases 7–10 shipping together.
3. ~~Issue #13's teardown confirmation screen — what "wording/defaults"
   meant~~ — **clarified, not a new question**: two concrete decisions
   the confirm screen needs before implementation, same shape as
   `restore_confirm.go`'s existing design: (a) the exact button/option
   text for the three choices (stop+uninstall / save-only / cancel), and
   (b) which one is pre-highlighted when the screen opens — `restore_confirm.go`
   defaults its cursor to the *safer* option, and this screen should too
   (recommend defaulting to "save config only," the least destructive of
   the three, or "cancel" if an even more conservative default is
   preferred — not "stop and uninstall," which should require an
   explicit deliberate move to reach).
4. ~~Issue #14's plist-path confirmation~~ — **resolved**, see Phase 11B
   above; both doppio-1 and doppio-2 agree.
5. ~~Phase 11F script name~~ — **resolved**: `headless-macs-debug`.
6. ~~`headless-macs-debug` implementation form~~ — **resolved: compiled
   Go binary**, not a shell script (also the forced outcome had setuid
   been chosen instead — macOS ignores setuid on scripts). New
   `cmd/headless-macs-debug/` build target, distributed via `go:embed` in
   the main `headless-macs` binary so operators still only need to copy
   one file. See Phase 11F's revised Q4.
7. ~~Debug-access mechanism~~ — **resolved: narrowly-scoped, toggleable
   `NOPASSWD` sudo**, not setuid or group-write widening. Config-driven
   toggle (`debug.sudo_nopasswd_enabled`, exact key name TBD), synced by
   `RunDebugTools()`/`headless-macs debug-tools`, documented in
   `README.md`. See Phase 11F's revised Q2&3 and Q4 above.
8. ~~Exact permission mode for `/var/log/<service>`~~ — **resolved**:
   confirmed `_llmserver:wheel 644` on live log files, both boxes — read
   access needs no change; the write-side gap (needed for rotation) is
   what the `NOPASSWD` sudo grant above solves. Checked against Ollama
   specifically; all tools share the same `installLogRotate()`-generated
   config, so this almost certainly generalizes, but wasn't independently
   spot-checked for the other four tools — low-risk to leave unverified.

**Resolved (user decision):**

9. ~~`debug.sudo_nopasswd_enabled` target username~~ — **resolved:
   prompt interactively, and validate the named user actually exists
   before writing anything to sudoers.** Not derived from `SUDO_USER`,
   not a second config key holding a name that could go stale. Concretely:
   `RunDebugTools()`/`headless-macs debug-tools` prompts for a username
   (CLI: a plain stdin prompt; TUI: see the consequence noted below),
   checks it with `id <username>` (or `dscl . -read /Users/<username>`)
   before writing the sudoers drop-in, and refuses with a clear error —
   not a partial/broken sudoers file — if the user doesn't exist.
   **Consequence for the TUI design in Q4 above:** this means the
   "Install/Update Debugging Tools" screen can't be a pure
   run-and-report `RunScreen` reuse after all when enabling the toggle —
   it needs an interactive text-input step first (matching
   `config_editor.go`'s existing text-edit mode, which already has the
   UI pattern this needs), then runs and reports. Still no *new* UI
   pattern needs inventing, just composing two that already exist
   (`config_editor.go`'s text input + `run_screen.go`'s result list),
   rather than the single-screen-reuse assumption Q4 originally stated.
10. ~~Exact `Cmnd_Alias` scope~~ — **resolved: pin to the whole
    `/usr/local/bin/headless-macs-debug` path**, not the `logs`
    subcommand specifically — matches the recommendation, confirmed by
    the user.
    flagging the tradeoff rather than deciding unilaterally.

---

## Files-touched summary

| File | Phase(s) |
|---|---|
| `internal/tui/restore_confirm.go` (or new sibling) | 11A |
| `internal/ops/restore.go` | 11A |
| `internal/ops/disable.go` (new, name TBD) | 11A |
| `internal/ops/baseline.go` | 11B, 11C |
| `internal/ops/verify.go` | 11C |
| `internal/ops/update.go` | 11D |
| `internal/tui/precheck_screen.go` | 11E |
| `cmd/headless-macs-debug/main.go` (new) | 11F |
| `internal/ops/debugtools.go` (new) | 11F |
| `internal/config/config.go` | 11A, 11F |
| `internal/tui/config_editor.go` | 11A, 11F |
| `cmd/headless-macs/main.go` | 11F |
| `internal/tui/menu.go` | 11F |
| `internal/tui/app.go` | 11F |
| `Makefile` | 11F |
| `README.md` | 11F |
| `docs/tool-comparison.md` | 11D (follow-up) |
| `docs/known-issues.md` / new `docs/debugging-guide.md` | 11F |
| `CHANGELOG.md` | all — end-of-session update |

---

## Implementation & closeout process

- [x] **Close each issue as its phase lands**, not all at once at the end
      — as soon as a phase's fix is implemented and verified, close its
      GitHub issue with a comment summarizing what actually changed
      (file/line references, not just "fixed"), matching the level of
      detail the issue's own diagnosis was filed with:
      - Phase 11A → [#13](https://github.com/mediumroast/headless-macs/issues/13)
      - Phase 11B → [#14](https://github.com/mediumroast/headless-macs/issues/14)
      - Phase 11C → [#15](https://github.com/mediumroast/headless-macs/issues/15)
      - Phase 11D → [#16](https://github.com/mediumroast/headless-macs/issues/16)
      - Phase 11E → [#17](https://github.com/mediumroast/headless-macs/issues/17)
      - Phase 11F has no filed issue (new feature, not a diagnosed bug) —
        nothing to close for it.
- [x] **User will test the final build on doppio-1 and doppio-2**
      directly — **done for doppio-1**: `coreaudiod`/`audiomxd`/
      `AirPlayXPCHelper` suppression confirmed via Verify, the scroll
      symptom confirmed fixed (after two additional bugs found and fixed
      along the way — see Phase 11E's entry above), and the NOPASSWD
      sudo + `headless-macs-debug` flow confirmed fully end to end
      including the revoke path. doppio-2 not yet tested.
- [ ] **Config-editor teardown confirmation flow (issue #13, Phase 11A)
      not yet live-tested** — the three-way choice (Cancel / Save only /
      Stop and uninstall) when disabling a tool in Edit Config has only
      been verified by code review, not exercised live. To test:
      disable a tool (e.g. `macmon` — lower stakes than Ollama, whose
      daemon is in active use), try each of the three choices in turn,
      and for each one check **two** things, not just one — (a)
      `config.json`'s `enabled` field for that tool (confirms the save
      happened, or didn't, for Cancel), and (b) actual daemon state via
      `sudo launchctl print system/com.<tool>.server` (confirms whether
      the daemon is still running — Save only — or was actually stopped
      and removed — Stop and uninstall). Checking only the config file
      wouldn't catch a bug where the wrong choice's daemon-side effect
      leaked into another choice.
- [ ] **Once all of Phase 11 (11A–11F) is implemented and merged as
      `v2.3.0`**, update the PR description (not just leave it as
      originally opened) to include a summary of each closed issue and
      what changed — not just bare links — and/or a link to this
      planning document for full detail. Matches this project's existing
      convention (`CLAUDE.md`'s PR-body convention, `CHANGELOG.md`'s
      per-version `### PR` links) of a PR/changelog entry actually
      summarizing content, not just pointing elsewhere.

---

**Status: awaiting review. Nothing implemented. Stopping here per
instruction.**
