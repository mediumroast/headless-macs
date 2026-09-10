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
`docs/RELEASE_STRATEGY.md`'s versioning rules, this phase is a **Patch**
release (bug fixes only) — except Phase 11F (new logging capability),
which is **Minor** (new subcommand, new config surface). The whole phase
will ship as one version; see Open Questions for which number.

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

- [ ] **Before writing code:** confirm the real system plist paths for
      these three services on a live box (`launchctl print
      system/com.apple.audio.coreaudiod` etc., or
      `find /System/Library/LaunchDaemons /System/Library/LaunchAgents
      -iname '*coreaudiod*' -o -iname '*audiomxd*' -o -iname
      '*AirPlayXPCHelper*'`). The issue itself flagged this as unconfirmed
      — do not guess paths blind.
- [ ] If a static plist path exists for each: fill in `Plist` in
      `phase8Suppressions` (`internal/ops/baseline.go`), matching the
      other four entries — the existing `bootout` call in the suppression
      loop then covers them with no other code change needed.
- [ ] If any of the three has **no** static plist (e.g. XPC-activated,
      no `/System/Library/LaunchDaemons/*.plist`): `launchctl bootout`
      needs the domain/service-target form instead
      (`launchctl bootout system/<label>`) rather than a plist path —
      `phase8Suppressions`' `Plist string` field may need to become an
      enum/union (plist path vs. service target) to express this, or a
      second small suppression list for label-only bootouts.
- [ ] Confirm `coreaudiod` doesn't immediately respawn after a bare
      `bootout` without `disable` going first (order already correct in
      the existing loop — `disable` runs before `bootout` — just needs
      confirming this actually holds for these three on a real box).
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
at `774` so that group can act on the logs.**
**Recommend against the literal ask, propose an alternative, and leave
the final call to the user:**
- `wheel` is macOS's traditional superuser-adjacent group (historically
  tied to `su`-to-root on BSD-derived systems). Adding an operator to it
  grants broad group membership with implications well beyond log access,
  and doesn't fit this project's existing pattern of a narrowly-scoped
  service account (`_llmserver`) for exactly this kind of purpose.
- Every managed log directory (`/var/log/ollama`, `/var/log/exo`, etc.) is
  **already** owned `_llmserver:_llmserver` (confirmed in
  `internal/ops/tools.go` — every tool's install function does
  `chown _llmserver:_llmserver` on its log dir). Adding the operator's
  user to the **existing** `_llmserver` group, and changing the mode from
  `0755` to `0750` (owner rwx, group rx, no world access — logs may
  contain prompts/model output, not just operational chatter) accomplishes
  the same goal (group members can read/act on logs) without introducing
  a second, broader-scoped group or exposing logs world-readable (`774`
  would still leave "other" with read access).
- If group-*write* access (not just read) is actually needed (e.g. an
  operator needs to delete/rotate manually, not just read), `0770` is the
  next step up from `0750` — still short of world-readable `774`.
- **This needs the user's explicit decision before implementation**:
  `_llmserver` + `0750`/`0770` (recommended), or the literal `wheel` +
  `0774` ask, or something else.

**4. Install location + a menu option to install/update it.**
- Script installed to `/usr/local/bin/headless-macs-debug-logs` (avoids
  colliding with the main `headless-macs` binary name, matches the
  existing `/usr/local/bin` convention this project already uses for the
  main binary itself).
- New `internal/ops/debugtools.go` — `RunDebugTools(cfg *config.Config)`
  — writes the script content (a Go string constant, same pattern as this
  project's plist-content constants), `chmod +x`, reports `[SET]`/`[SKIP]`
  by content comparison (same idempotency pattern as `installLaunchDaemon`
  — compare content, not just existence).
- New CLI subcommand `headless-macs debug-tools`, alongside the existing
  `precheck`/`baseline`/`install-tools`/etc. in `cmd/headless-macs/main.go`.
- New TUI sidebar entry — user's suggested label "Install/Update Debugging
  Tools" (final label TBD, needs to fit the sidebar's width budget — see
  `internal/tui/menu.go`'s existing items for the established naming
  length/style) — `internal/tui/menu.go` (`menuItems` list) plus a new
  screen or reuse of the existing `RunScreen` pattern already used for
  Baseline/Install Tools/Update Tools/Storage (`internal/tui/run_screen.go`
  already generically renders any `ops` result's `[SET]`/`[SKIP]`/`[WARN]`
  actions — this should slot in without a new screen type).

**Scope:** New capability — Minor version bump (new subcommand, new
config surface only if the wheel/`_llmserver` decision needs a config
key, which it likely doesn't since group membership is a one-time
`baseline`-style action, not an ongoing config toggle).

**Files touched:** new `internal/ops/debugtools.go`, `cmd/headless-macs/main.go`
(new subcommand + usage text), `internal/tui/menu.go` (new sidebar item),
`internal/tui/app.go` (new screen routing, reusing `RunScreen`),
`docs/known-issues.md` and/or a new `docs/debugging-guide.md` (usage docs).

---

## Open questions (need answers before implementation starts)

1. **Phase 11F group/permission model** — `_llmserver` group + `0750`
   (recommended) vs. the literal `wheel` + `0774` ask vs. something else?
2. **Version number for this phase** — Phases 11A–11E are patch-level
   fixes; 11F is a new minor-level capability. Ship as one combined minor
   release (`v2.3.0`, following the same "shipped together" precedent as
   `v2.2.0`'s Phases 7–10), or split 11A–11E into a `v2.2.2` patch release
   first and 11F into its own `v2.3.0` afterward?
3. **Issue #13's teardown confirmation screen** — confirmed direction
   (reuse `restore_confirm.go`'s pattern) from the issue's own
   recommendation, but the exact three options' wording/defaults need a
   look before implementation.
4. **Issue #14's plist-path confirmation** — needs access to a live box
   (doppio-1/2) to confirm real paths before the fix can be written
   correctly rather than guessed.
5. **Phase 11F script name and TUI label** — `headless-macs-debug-logs`
   and "Install/Update Debugging Tools" are placeholders pending approval.

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
