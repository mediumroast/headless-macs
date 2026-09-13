# PHASE 13 PLAN — headless-macs-debug: start/stop debug sessions, log markers

**Status: planning only. Nothing implemented. Stopping here per instruction.**

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

**`start ollama` — proposed exact sequence:**
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
6. Insert a start marker into the (now fresh, post-rotation) log files —
   see Phase 13B. Ordering choice: marker written *before* the restart, so
   it's the very first line of the fresh log rather than landing somewhere
   after Ollama's own startup output.
7. Print a confirmation, including the plist path touched and a reminder
   that this is a temporary, out-of-band change (see the drift question in
   Phase 13D).

**`stop ollama` — proposed sequence:** the user's own description here
was: *"lowers the logging level to the standard level, restarts the
service and rotates the logs, when we rotate the logs at the end of the
session we should return"* — the last clause is incomplete (return *what*?
the bundle path? a summary? nothing, just confirmation text?). **Flagging
this literally rather than guessing at the missing word** — see Open
Question 1 below. Aside from that gap, proposed sequence:
1. Root check.
2. Insert a stop marker (Phase 13B) — done *before* the restart this time,
   so it's the last line of the debug session's log, still under debug
   verbosity, rather than after the daemon's already reverted.
3. `PlistBuddy -c "Delete :EnvironmentVariables:OLLAMA_DEBUG" plist` (no-op
   with a note if already absent).
4. Restart (`bootout` + `bootstrap`), same as `start`.
5. Rotate logs — same shared helper as `start` — so the debug session's
   logs (bounded by the start/stop markers) get cleanly archived together,
   separate from whatever normal-verbosity logging follows.
6. Print a confirmation — pending Open Question 1 for what else, if
   anything, this step should surface.

Note the rotate/restart order is *reversed* between `start` (rotate, then
restart) and `stop` (restart, then rotate) in this proposal — deliberately,
matching the user's own stated sequences for each, and it makes sense
either way: `start` wants the debug session beginning in a fresh file, and
`stop`'s later rotation is what boxes up the complete session (markers and
all) into its own archived rotation before quiet logging resumes.

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

**Design question this raises, not resolved here — see Open Question 2:**
should `mark` take the same `acquireLock()` flock `logs`/`clean` use? A
mark is a near-instant single append, fundamentally lighter-weight than a
multi-gigabyte bundle write; locking it against a long-running `logs`
invocation seems unnecessary, and a `mark` landing mid-`logs`-bundle can't
corrupt anything (the existing tar-writing code already tolerates a source
file changing size mid-copy). Leaning toward *not* sharing the lock, but
flagging rather than deciding unilaterally since it's a real behavior
choice.

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

## Phase 13D — Open questions

1. **What should `stop` "return" at the end?** The instruction's own
   sentence — *"when we rotate the logs at the end of the session we
   should return"* — is incomplete. Candidates: (a) nothing beyond a plain
   confirmation message, (b) the path to the just-rotated debug-session log
   archive (mirroring `logs`' own "print the resulting path" convention),
   (c) automatically bundle it (essentially calling the equivalent of
   `logs ollama` right after rotating) so a developer gets a ready-to-`scp`
   tarball of exactly the debug session in one command. (b) or (c) both
   seem plausible and useful; needs your call.
2. **Should `mark` share `logs`/`clean`'s exclusive lock, or run
   lock-free?** Leaning lock-free per the reasoning in Phase 13B, but
   flagging rather than assuming.
3. **Should `start`/`stop` automatically call the equivalent of `mark`
   internally** (as proposed in Phase 13A's sequences), **or should `mark`
   only ever be invoked manually, separately from `start`/`stop`?** The
   instructions describe three separate commands, but `start`/`stop`
   inserting their own markers automatically seems like the natural
   complete workflow (a debug session bounded start-to-finish without extra
   manual steps) — while `mark` remaining independently invocable is still
   useful on its own for marking sub-events *within* one long debug run
   without restarting the daemon each time. Proposing both (start/stop
   auto-mark, mark also stands alone) — confirm or adjust.
4. **Drift with `headless-macs install-tools`:** since `install-tools`
   always fully regenerates a tool's plist from `config.json` (this
   project's own documented, deliberate convention — the plist is
   "always re-written even if it exists, because config values may have
   changed"), running `install-tools` during an active debug session
   would silently wipe out `start`'s `OLLAMA_DEBUG` toggle (reverting to
   whatever `tools.ollama.debug` is set to in `config.json`). This seems
   like acceptable, even correct, behavior (a debug session is meant to be
   temporary; `install-tools` reasonably wins) but worth stating in
   `start`'s own output as a one-line note rather than leaving it as a
   silent surprise later — confirm that's sufficient, or if you'd rather
   `start` refuse to run while `install-tools` might be a concern
   (probably overkill, but flagging the option).

---

## Files-touched summary (once resolved)

- `cmd/headless-macs-debug/main.go` — `start`, `stop`, `mark` subcommands;
  a small per-tool debug-toggle table (Ollama only implemented); a shared
  rotate helper extracted from `runLogs()`'s existing rotation step; the
  `usage` string
- `README.md` — document all three new subcommands under Debugging Tools
- `CHANGELOG.md` — `[Unreleased]` entry once implemented
