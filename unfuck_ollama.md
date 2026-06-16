# unfuck_ollama.md — Diagnosis and fix plan

## What the user sees

`sudo ./install-tools.sh` prints the tool plan then silently exits.
Zero output from `ensure_llmserver_user`. Ollama still running as a menu
bar app. No system daemon. No error message.

---

## Root cause #1 — `set -euo pipefail` kills the script before the first echo

The very first statement in `ensure_llmserver_user` is:

```bash
uid=$(dscl . -read "/Groups/$LLMSERVER_USER" PrimaryGroupID 2>/dev/null \
      | grep -oE '[0-9]+' | head -1)
```

When the group has a broken/missing PrimaryGroupID (left over from the
first failed run), `dscl` outputs nothing. `grep -oE '[0-9]+'` on empty
input exits with code **1** (no match). With `set -o pipefail` the whole
pipeline exits 1. The assignment `uid=$(...)` inherits that exit code.
`set -e` sees a non-zero exit and **aborts the script immediately** —
before printing a single line.

That is why we see the plan output and then the shell prompt with no
error, no `[SKIP]`, no `[FAIL]`. The abort is silent.

Same bug would hit `existing_uid=$(dscl ...)` for the same reason.

**Fix:** append `|| true` to both command-substitution assignments so
the assignment always exits 0 regardless of pipeline result.

```bash
uid=$(dscl . -read "/Groups/$LLMSERVER_USER" PrimaryGroupID 2>/dev/null \
      | grep -oE '[0-9]+' | head -1) || true

existing_uid=$(dscl . -read "/Users/$LLMSERVER_USER" UniqueID 2>/dev/null \
               | grep -oE '[0-9]+' | head -1) || true
```

---

## Root cause #2 — broken dscl records from the first failed run

The first run crashed with an empty `$uid` before writing valid GID/UID
to Directory Services. The group record exists but has no `PrimaryGroupID`
attribute; the user record may exist with an empty `UniqueID`. These
broken records block clean recreation and cause the `dscl -create` calls
to return `eDSRecordAlreadyExists`.

**Fix:** after reading `uid`/`existing_uid` (now safe from root cause #1),
detect an empty value and delete the broken record before recreating.
This is already in the rewritten function — it just never ran because
root cause #1 aborted first.

---

## Root cause #3 — Ollama menu bar app holds port 11434

`pkill -f "Ollama.app"` does not match the actual process name. The app
stays alive, holds port 11434, and the LaunchDaemon fails to bind.

**Fix (already committed):** `osascript quit` + `killall Ollama` +
wait loop on `lsof -iTCP:11434`.

---

## Fix plan

### Step 1 — Add `|| true` to both dscl read assignments

In `ensure_llmserver_user`, lines:

```bash
uid=$(dscl . -read "/Groups/$LLMSERVER_USER" PrimaryGroupID 2>/dev/null \
      | grep -oE '[0-9]+' | head -1) || true

existing_uid=$(dscl . -read "/Users/$LLMSERVER_USER" UniqueID 2>/dev/null \
               | grep -oE '[0-9]+' | head -1) || true
```

### Step 2 — Verify broken-record detection runs

After step 1 unblocks the script, the existing self-healing logic should
detect empty `uid` / `existing_uid`, delete the broken records, and
recreate them with valid IDs.

### Step 3 — Verify Ollama is stopped before daemon load

The port-clearing logic (killall + wait loop) should run cleanly now
that the script gets past `ensure_llmserver_user`.

### Step 4 — Smoke test

After `git pull && sudo ./install-tools.sh`:

```bash
sudo launchctl print system/com.ollama.server   # state = running
curl -s http://localhost:11434/api/tags          # {"models":[...]}
ps aux | grep ollama                             # one process, user = _llmserver
./verify.sh
```

---

## Files to change

| File | Change |
|---|---|
| `install-tools.sh` | Add `|| true` to both dscl read assignments in `ensure_llmserver_user` |

---

## What NOT to do

- Do not rewrite the whole function again — the logic is correct, just
  the `set -e` interaction with `grep` on empty input is the kill shot.
- Do not disable `set -euo pipefail` — it catches real bugs elsewhere.
- Do not tell the user to manually delete the dscl records — the script
  should self-heal.
