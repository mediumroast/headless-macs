# PHASE 11 PLAN — Bug fixes (Issues #13–#17) + Debugging Log Tools

**⚠️ DO NOT EXECUTE. This plan needs sign-off before any code changes.**
Nothing in this document has been implemented. Branch `claude/phase-11-issue-fixes`
has been created off `main` and contains only this plan document so far.

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

- [ ] **Auto-migrate on load, independent of root-causing the save bug.**
      `config.Load()` (`internal/config/config.go`) should detect when the
      loaded file is missing sections/keys present in the current `Config`
      struct (compare against a freshly-`Bootstrap`'d template, or simpler:
      just always re-`Save()` once immediately after a successful `Load()`
      if the re-marshaled bytes differ from what was read) and persist the
      patched version back to disk. This directly fixes symptom #2
      (macmon section never appearing) regardless of whether #1's root
      cause is ever fully pinned down.
- [ ] **Add a post-save verification step** in `config_editor.go`'s `s`/`S`
      handler: after `config.Save(m.cfg)`, re-read the file and compare
      against what was just written; if they don't match, surface a
      `[FAIL]`-equivalent error state in the UI instead of silently
      reporting `[saved]`. This won't fix an unknown root cause, but it
      stops the editor from *lying* about save state, which is the
      sharper edge of the original complaint.
- [ ] **Tool-disable confirmation screen**, reusing the existing
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
- [ ] New `internal/ops` function for per-tool teardown (name TBD —
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
- [ ] Fill in `Plist` for these three entries in `phase8Suppressions`
      (`internal/ops/baseline.go`) with the paths above — the existing
      `bootout` call in the suppression loop then covers them with no
      other code change needed; this is now a one-line-per-service fix.
- [ ] Confirm `coreaudiod` doesn't immediately respawn after a bare
      `bootout` without `disable` going first (order already correct in
      the existing loop — `disable` runs before `bootout` — just needs
      confirming this actually holds for these three on a real box, after
      the fix lands).
- [ ] Update `verify.go`'s corresponding checks (already correct today —
      they check live state — but confirm they still pass immediately
      after a single `Baseline` run once this fix lands, not just on
      next-boot).

**Files touched:** `internal/ops/baseline.go` (`phase8Suppressions` list,
possibly its shape).

---

## Phase 11C — Issue #15: SSH section trusts exit codes, never confirms state

**Recap:** `sectionSSH()` declares `[SET]` from subprocess exit codes
alone; the `systemsetup` fallback discards its own exit code entirely.
`verify.go` does the real check (`launchctl print` parsed for
`state = running`/`waiting`) and correctly disagrees.

- [ ] Extract `verify.go`'s SSH-state check (`launchctl print
      system/com.openssh.sshd`, parsed for `state = running`/`waiting`)
      into a shared helper both `baseline.go` and `verify.go` call —
      single source of truth, can't drift apart again.
- [ ] `sectionSSH()` calls the shared check *after* attempting
      `enable`+`kickstart` (and after the `systemsetup` fallback, if that
      path is taken) and reports `[SET]` only if the check confirms
      success; otherwise `[WARN]` with the same fix hint `verify.go`
      already prints, so the two commands never again tell the operator
      different stories about the same thing.
- [ ] Stop discarding the `systemsetup -setremotelogin on` fallback's
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

- [ ] Add `updateMacmon()` to `internal/ops/update.go`, following the
      existing per-tool pattern (stop daemon → `brew upgrade macmon` →
      re-bootstrap → confirm via the same endpoint check `installMacmon()`
      already uses). Needs a way to detect "is a newer version available"
      before upgrading (`brew outdated macmon`, or just always run
      `brew upgrade macmon` and let Homebrew no-op if already current —
      simpler, matches how the other tools' update functions already
      behave by re-running their installer unconditionally).
- [ ] Add the `if cfg.Tools.Macmon.Enabled { r.updateMacmon() }` branch to
      `RunUpdateTools()` alongside the existing five.
- [ ] `docs/tool-comparison.md`'s macmon section already tells operators
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

- [ ] Cache `renderChecks()`'s output on the model (`cachedRows []string`)
      whenever `m.result`/`m.verifyResult` changes, rather than
      recomputing it ad hoc — this also avoids the current design's
      implicit assumption that `renderChecks()` is cheap/stable to call
      repeatedly per frame.
- [ ] Bound `m.scroll` (in all four key handlers — `up`/`down`/`pgup`/`pgdn`)
      and the `"↑ N more above"` count against `len(m.cachedRows)`, not
      `m.checkCount()`.
- [ ] **After the fix, verify live** (real terminal, real SSH session —
      this environment can't reproduce it) whether the originally-reported
      "title bar disappears while scrolling" symptom is actually resolved.
      If it persists after this fix, it's a separate bug (possibly
      terminal-client scrollback behavior, as the issue speculates, not a
      `headless-macs` rendering bug) and needs to be re-opened/re-scoped
      rather than assumed fixed.

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

- [x] **No debug-access toggle needed for read access.** Dropped from
      scope. `headless-macs-debug logs` doesn't need to change any
      permission or add anyone to any group to let an operator read or
      `scp` a log — that already works.
- [ ] What `headless-macs-debug logs` *does* still need privilege for is
      **triggering rotation itself** (`logrotate -f`) — that's a root-only
      action regardless of group membership, same as every other write
      operation this project performs, and is exactly what the pre-flight
      permission check (Q4 below) is checking for. This needs `sudo`, not
      `wheel` membership — no new group/permission machinery at all.
- [ ] **Not independently verified for every tool** — this was checked
      against Ollama's live log files specifically, on both boxes. All
      tools share the same `installLogRotate()`-generated config and the
      same `create 644 _llmserver wheel` line (Exo's stanza aside, which
      uses `wheel`/`staff` for a different reason — see above), so this
      almost certainly generalizes, but wasn't independently confirmed for
      Rapid-MLX/mlx-lm/Infinity/Exo/macmon's live files. Worth a quick
      spot-check of one more tool before fully closing this out, but not
      worth blocking the plan on — the mechanism generating these files'
      permissions is identical across tools.
- [ ] **The literal `wheel`+`774`+toggle ask is no longer needed as
      designed**, per the evidence above — but if a *future* need for
      broader operator access shows up (e.g. wanting to delete/rotate
      logs manually without `sudo`, which 644-world-readable doesn't
      grant), the toggle design above is still sound and can be revisited
      then. Not building it now against a problem that turned out to
      already be solved.

**4. Install location + a menu option to install/update it.**

**Revised per user direction:** the standalone tool is named
`headless-macs-debug` (not `headless-macs-debug-logs`), installed to
`/usr/local/bin/headless-macs-debug` — room for more subcommands beyond
logs later, rather than a single-purpose script name that would need
renaming/aliasing the moment a second debugging function shows up.

- [ ] `headless-macs-debug logs` — the rotate+bundle capability from Q1,
      runnable with no other flags for the "just rotate everything and
      tell me if it worked" default the user asked for: rotate via the
      existing shared logrotate config, bundle into the timestamped
      `tar.gz`, print the path, exit 0/non-zero for success/failure (and
      only that — no interactive prompts, so it works cleanly over a bare
      `ssh host headless-macs-debug logs`).
- [ ] No `debug-access enable|disable` subcommand — dropped, per the
      finding above that there's no read-access gap to toggle.
- [ ] **Permission check before doing anything**, per the user's
      explicit requirement: `headless-macs-debug logs` must confirm the
      invoking user can actually run `logrotate` (root, or `sudo`) before
      attempting anything, and fail fast with a clear message if not —
      not attempt partial work and fail confusingly partway through. A
      single, simple check now that the toggle is out of scope.
- [ ] New `internal/ops/debugtools.go` — `RunDebugTools(cfg *config.Config)`
      — writes the script content (a Go string constant, same pattern as
      this project's plist-content constants) to
      `/usr/local/bin/headless-macs-debug`, `chmod +x`, reports
      `[SET]`/`[SKIP]` by content comparison (same idempotency pattern as
      `installLaunchDaemon` — compare content, not just existence).
      Whether `headless-macs-debug` itself is implemented as a shell
      script (matches "install a script" from the user's original ask,
      simplest to embed as a Go string constant) or a second small Go
      binary (more consistent with this project's general move away from
      shell, better structured multi-subcommand handling) is worth a
      explicit decision before implementation — leaning shell script per
      the literal ask and to avoid a second compiled artifact/build
      target, but flagging the tradeoff rather than deciding unilaterally.
- [ ] New CLI subcommand `headless-macs debug-tools` (installs/updates
      `headless-macs-debug` itself — distinct from `headless-macs-debug`
      the installed tool), alongside the existing
      `precheck`/`baseline`/`install-tools`/etc. in `cmd/headless-macs/main.go`.
- [ ] New TUI sidebar entry — user's suggested framing "Install/Update
      Debugging Tools" (exact label TBD, needs to fit the sidebar's width
      budget — see `internal/tui/menu.go`'s existing items for the
      established naming length/style) — `internal/tui/menu.go`
      (`menuItems` list) plus a new screen or reuse of the existing
      `RunScreen` pattern already used for Baseline/Install Tools/Update
      Tools/Storage (`internal/tui/run_screen.go` already generically
      renders any `ops` result's `[SET]`/`[SKIP]`/`[WARN]` actions — this
      should slot in without a new screen type).

**Scope:** New capability — Minor version bump (new subcommand, new
config surface only if the wheel/`_llmserver` decision needs a config
key, which it likely doesn't since group membership is a one-time
`baseline`-style action, not an ongoing config toggle).

**Files touched:** new `internal/ops/debugtools.go`, `cmd/headless-macs/main.go`
(new subcommand + usage text), `internal/tui/menu.go` (new sidebar item),
`internal/tui/app.go` (new screen routing, reusing `RunScreen`),
`docs/known-issues.md` and/or a new `docs/debugging-guide.md` (usage docs).

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
5. ~~Phase 11F script name~~ — **resolved**: `headless-macs-debug`
   (binary/script name) with a `logs` subcommand — no second `debug-access`
   subcommand, per #1 above.
8. ~~Exact permission mode for `/var/log/<service>`~~ — **resolved**:
   confirmed `_llmserver:wheel 644` on live log files, both boxes — no
   mode change needed at all. Checked against Ollama specifically; all
   tools share the same `installLogRotate()`-generated config and the
   same `create 644 _llmserver wheel` line, so this almost certainly
   generalizes, but wasn't independently spot-checked for the other four
   tools — low-risk to leave unverified given the shared mechanism, not
   worth blocking on.

**Still open:**

6. **`headless-macs-debug` implementation form** — shell script (simpler,
   matches "install a script" literally, embeds as a Go string constant
   like this project's plist content) vs. a second small Go binary (more
   consistent with the project's general shell→Go migration, cleaner
   multi-subcommand handling) — see Phase 11F's revised Q4.
7. ~~Debug-access auto-expiry~~ — **moot**, no toggle to expire per #1
   above.

---

## Files-touched summary

| File | Phase(s) |
|---|---|
| `internal/config/config.go` | 11A |
| `internal/tui/config_editor.go` | 11A |
| `internal/tui/restore_confirm.go` (or new sibling) | 11A |
| `internal/ops/restore.go` | 11A |
| `internal/ops/disable.go` (new, name TBD) | 11A |
| `internal/ops/baseline.go` | 11B, 11C |
| `internal/ops/verify.go` | 11C |
| `internal/ops/update.go` | 11D |
| `internal/tui/precheck_screen.go` | 11E |
| `internal/ops/debugtools.go` (new) | 11F |
| `cmd/headless-macs/main.go` | 11F |
| `internal/tui/menu.go` | 11F |
| `internal/tui/app.go` | 11F |
| `docs/tool-comparison.md` | 11D (follow-up) |
| `docs/known-issues.md` / new `docs/debugging-guide.md` | 11F |
| `CHANGELOG.md` | all — end-of-session update |

---

**Status: awaiting review. Nothing implemented. Stopping here per
instruction.**
