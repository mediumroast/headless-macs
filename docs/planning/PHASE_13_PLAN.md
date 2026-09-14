# PHASE 13 PLAN — headless-macs-debug: start/stop debug sessions, log markers

**Status: plan complete, all open questions resolved. Nothing implemented
yet.**

Branch: `claude/debug-session-start-stop-mark`, created off
`claude/v2.3.1-fixes` (PR [#20](https://github.com/mediumroast/headless-macs/pull/20),
not yet merged) — `start`/`stop` toggle the same `OLLAMA_DEBUG` setting PR
#20 just fixed to be real, so this builds directly on that.

Target version: **v2.4.0** (Minor — new subcommands, new capability, not a
bug fix; doesn't touch `config.json`'s schema at all).

---

## Scope decision table

| Item | In scope | Out of scope |
|---|---|---|
| `headless-macs-debug start ollama` — enable debug mode, rotate, restart | ✅ | Any tool other than Ollama for the initial implementation (see "Generic design" below) |
| `headless-macs-debug stop ollama` — disable debug mode, restart, rotate | ✅ | Same as above |
| `headless-macs-debug mark <tool> --start` / `--stop` — log markers | ✅ (feasibility confirmed — see Phase 13B) | Markers for anything other than a tool's own `stdout.log`/`stderr.log` (e.g. not marking `/var/log/mac-llm-setup`'s operational logs) |
| Generic per-tool scaffolding so other tools can be added later | ✅ (design only) | Actually wiring up rapid-mlx/mlx-lm/Infinity/Exo/macmon now — each needs materially different plist-editing logic (see Phase 13C) |

---

## Phase 13A — `start`/`stop`: verified mechanics

**How Ollama's debug toggle actually gets applied.** `EnvironmentVariables`
in a LaunchDaemon plist are read once, at process start — there's no live
reload. So "enable debug mode" always requires a restart to take effect,
which is exactly why the user's requested flow already includes one.

**How to edit one key in an existing plist without touching anything
else.** `headless-macs-debug` doesn't have (and per the standalone-binary
design just reaffirmed in Phase 12, shouldn't gain) access to
`internal/config`'s full config struct or the other settings
`installOllama()` would need to regenerate the *whole* plist from scratch
(host, models dir, keep-alive, etc.) — so this can't just call the same
plist-generation code `install-tools` uses. The right tool is
`/usr/libexec/PlistBuddy` (confirmed present, standard on every macOS
install, already how this project's own `internal/ops` code could
plausibly extend rather than reaching for a new plist-parsing dependency)
— it edits one key in place, leaving everything else in the file
untouched. Verified its exact idempotency behavior directly rather than
assuming:

```
$ PlistBuddy -c "Add :EnvironmentVariables:OLLAMA_DEBUG string 1" x.plist   # first time: exit 0
$ PlistBuddy -c "Add :EnvironmentVariables:OLLAMA_DEBUG string 1" x.plist   # second time: exit 1, "Entry Already Exists"
$ PlistBuddy -c "Set :EnvironmentVariables:OLLAMA_DEBUG 1" x.plist          # only works if the key already exists
$ PlistBuddy -c "Delete :EnvironmentVariables:OLLAMA_DEBUG" x.plist         # exit 1 if it doesn't exist
```

Neither `Add` nor `Delete` is idempotent on their own — `start` must check
whether the key is already present (`Print :EnvironmentVariables:OLLAMA_DEBUG`,
exit 0 if it exists) before deciding `Add` vs. `Set`; `stop` must check
before `Delete`, and treat "already absent" as success, not an error.

**`start ollama` — resolved sequence** (no auto-marking — `mark` is a
fully separate, manually-invoked command; see Open Question 3, now
resolved):
1. Root check (already required for everything else this binary does).
2. `PlistBuddy -c "Print :EnvironmentVariables:OLLAMA_DEBUG" plist` — if
   already present, print a note ("already in debug mode") and skip to
   step 4 rather than erroring.
3. `PlistBuddy -c "Add :EnvironmentVariables:OLLAMA_DEBUG string 1" plist`.
4. Force-rotate logs — reusing the exact same `logrotate -f` call `logs`
   already makes (factored into a small shared helper rather than
   duplicated), so the pre-debug-session logs get cleanly archived before
   any debug output appears.
5. Restart: `launchctl bootout system <plist>` then
   `launchctl bootstrap system <plist>` (per this project's own documented
   LaunchDaemon convention — `bootstrap`/`bootout` only, never
   `load`/`unload`).
6. Print a confirmation — the plist path touched, and a one-line note that
   running `install-tools` while this debug session is active will revert
   this toggle (resolved in Open Question 4 below).

**`stop ollama` — resolved sequence:**
1. Root check.
2. `PlistBuddy -c "Delete :EnvironmentVariables:OLLAMA_DEBUG" plist`
   (no-op with a note if already absent).
3. Restart (`bootout` + `bootstrap`), same as `start`.
4. Rotate logs — same shared helper as `start` — so the debug session's
   logs get cleanly archived separately from whatever normal-verbosity
   logging follows.
5. Exit — standard exit codes only (`0` success, non-zero on any step's
   failure), no special output beyond whatever plain confirmation text
   each step above already prints (resolved in Open Question 1 below: no
   bundle path, no auto-bundling).

Note the rotate/restart order is *reversed* between `start` (rotate, then
restart) and `stop` (restart, then rotate) — deliberately, matching the
user's own stated sequences for each, and it makes sense either way:
`start` wants the debug session beginning in a fresh file, and `stop`'s
later rotation is what boxes up the complete session into its own
archived rotation before quiet logging resumes. A developer who wants that
archived debug-session log off the box afterward runs `logs ollama`
themselves, same as any other time — `stop` doesn't do it for them.

---

## Phase 13B — `mark <tool> --start` / `--stop`: feasibility

**Feasible — confirmed, not just assumed.** The mechanism: open
`stdout.log` and `stderr.log` with `O_APPEND|O_WRONLY` and write one line
to each. POSIX guarantees a single `write()` call to an `O_APPEND`-opened
file descriptor is atomic up to `PIPE_BUF` (a few KB on macOS) — so a
second process (Ollama's own daemon, writing its normal output
concurrently) can never see a torn/interleaved write from our marker line;
worst case the two processes' lines simply appear in whichever order the
kernel happened to serialize them, never corrupted mid-line. This is the
same mechanism things like `logger`(1) and syslog rely on to let multiple
processes safely share one append-only log file — not a novel or risky
technique.

Practical details:
- Root (already required) can write to `/var/log/ollama/*.log` regardless
  of them being owned by `_llmserver:wheel` — no permission obstacle.
- Marker text needs to be distinctive enough to reliably `grep` for and
  clearly not confusable with anything Ollama itself would log, e.g.:
  ```
  ##### headless-macs-debug: DEBUG SESSION START 2026-09-13T14:32:07Z #####
  ##### headless-macs-debug: DEBUG SESSION STOP  2026-09-13T14:41:52Z #####
  ```
- Written to *both* `stdout.log` and `stderr.log` — a developer reading
  either stream should see the same boundary.
- If a log file doesn't exist yet (tool never started, or not enabled),
  fail that one file gracefully with a warning rather than aborting the
  whole command — matches this binary's existing defensive style
  elsewhere (`writeFileToTar`'s unreadable-file handling).

**Resolved: `mark` does not share `logs`/`clean`'s `acquireLock()` flock.**
It's a near-instant single append, fundamentally lighter-weight than a
multi-gigabyte bundle write; a `mark` landing mid-`logs`-bundle can't
corrupt anything (the existing tar-writing code already tolerates a source
file changing size mid-copy) or corrupt the log file itself (Phase 13B's
`O_APPEND` atomicity guarantee above).

---

## Phase 13C — Generic design, Ollama-only implementation

Per instruction: design so other tools can be added later, implement only
Ollama now. The real complication, found checking each tool's actual plist
generator in `internal/ops/tools.go`: **they don't all toggle verbosity the
same way.**

| Tool | Verbosity mechanism | Toggle shape |
|---|---|---|
| Ollama | `OLLAMA_DEBUG` environment variable | Add/Set/Delete one `EnvironmentVariables` dict key (this phase's design) |
| rapid-mlx, mlx-lm, Infinity | `--log-level <value>` **CLI argument** in `ProgramArguments` | Find-and-replace an array element pair — a materially different PlistBuddy operation shape (array index manipulation, not a dict key) |
| Exo | No verbosity flag at all currently | Nothing to toggle — would need its own investigation first |
| macmon | No verbosity flag (a telemetry daemon, not an inference server) | Likely doesn't apply |

So a genuinely generic `start`/`stop` needs, per tool: (a) which mechanism
applies (env var vs. CLI arg vs. "not supported yet"), (b) the plist path
and label, (c) confirmation that a `bootout`+`bootstrap` restart is safe/
expected for that daemon (should be, for all of these — LaunchDaemon
convention isn't tool-specific). Proposed scaffold: a small
per-tool table (mirroring `toolLogDirs`'s existing shape) mapping tool name
→ `{plistPath, label, debugToggle}`, where `debugToggle` is implemented
only for `ollama` initially and returns a clear "not yet supported for
<tool>" error for everything else — so the command surface
(`start <tool>`, `stop <tool>`, `mark <tool> --start/--stop`) is already
generic-shaped, and adding rapid-mlx/mlx-lm/Infinity later is "implement
one more table entry," not a redesign. `mark` itself is already
tool-agnostic regardless (it only needs `toolLogDirs`, which already
covers all six).

---

## Phase 13D — Resolved decisions

All four open questions are now resolved with the user; this plan is
complete and ready to implement.

1. **What should `stop` "return" at the end?** Just standard exit codes —
   `0` on success, non-zero on failure. No bundle path, no auto-bundling.
   A developer who wants the archived debug-session log off the box runs
   `logs ollama` themselves afterward, same as any other time.
2. **Should `mark` share `logs`/`clean`'s exclusive lock?** No — lock-free,
   per the reasoning in Phase 13B (a near-instant append can't corrupt
   anything even landing mid-bundle).
3. **Should `start`/`stop` auto-insert markers, or should `mark` stay
   fully manual?** Fully manual and separate — auto-marking was judged
   confusing. `start`/`stop` never call `mark`'s logic; `mark` is only
   ever invoked directly by the operator.
4. **Drift with `headless-macs install-tools`:** confirmed as expected,
   acceptable behavior (a debug session is meant to be temporary;
   `install-tools` regenerating the plist from `config.json` reasonably
   wins) — `start` prints a one-line note about this interaction in its
   own output rather than leaving it as a silent surprise later, matching
   this project's existing pattern of proactive nudges (the SIP warning,
   the version-mismatch nudge). `start` does not refuse to run or attempt
   to detect/prevent a concurrent `install-tools` — just informs.

---

## Files-touched summary

- `cmd/headless-macs-debug/main.go` — `start`, `stop`, `mark` subcommands;
  a small per-tool debug-toggle table (Ollama only implemented); a shared
  rotate helper extracted from `runLogs()`'s existing rotation step; the
  `usage` string
- `README.md` — document all three new subcommands under Debugging Tools
- `CHANGELOG.md` — `[Unreleased]` entry once implemented
