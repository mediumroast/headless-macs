# Phase 5: Jeff's Feedback — Bug Fixes, Security, and Operational Hardening

Source: peer review from a homelab operator running the same setup.  
Feedback items are numbered as in his document; items are grouped into phases by priority.

---

## Scope decision: what is and isn't in this phase

| # | Item | Decision |
|---|---|---|
| 1 | sysctl persistence broken | **In — Phase A** (silent correctness bug) |
| 2 | `pmset autorestart 1` missing | **In — Phase A** (silent reliability gap) |
| 3 | Network defaults wide open | **In — Phase B** (security default) |
| 4 | Daemons run as root | **Out of this phase** — architectural change touching `install-tools.sh` and all plist generation; tracked as follow-on |
| 5 | SSH hardening via sed on main config | **In — Phase B** (survives OS update; lockout risk fix) |
| 6 | Raise maxfiles | **In — Phase C** (cheap, fits existing pattern) |
| 7 | pmset self-heal via launchd timer | **In — Phase C** (closes known-issues gap) |

---

## Phase A — Silent bugs (correctness)

### A1: Replace `/etc/sysctl.conf` with a LaunchDaemon

**Problem:** `sysctl_apply()` in `setup.sh` appends to `/etc/sysctl.conf`. macOS stopped
reading that file at boot since Catalina. The `sysctl -w` call applies for the current
session only — `verify.sh` passes immediately after setup but the tuning is gone after
the first reboot.

**Changes:**

**`setup.sh`**
- Remove the `sysctl_apply()` helper function (lines 113–124).
- In Section 2 (Network Stack Tuning), replace the six `sysctl_apply` calls with a single
  block that installs `/Library/LaunchDaemons/com.llm-server.sysctl-tuning.plist`.
- The plist uses `RunAtLoad` + `ProgramArguments` containing `/usr/sbin/sysctl -w` for
  each of the six keys. The existing caffeinate plist (lines 180–203) is the exact pattern
  to follow — same structure, same `root:wheel 644` ownership.
- After installing the plist, apply all six keys live with `sysctl -w` for the current
  session (same as before).
- Idempotency: skip if the plist already exists (same guard used for caffeinate).

**`verify.sh`**
- The six `check_sysctl` calls already work correctly (they read live kernel values).
- Add a check that `com.llm-server.sysctl-tuning` daemon is running, mirroring the
  caffeinate daemon check at line 133.

**No changes to `config.json`.**

---

### A2: Add `pmset autorestart 1` to setup.sh

**Problem:** Restart-after-power-failure is not set in `setup.sh`. The deprecated
`pmset_to_ollama.sh` sets it to `0`, which is actively wrong for a 24/7 headless node.

**Changes:**

**`setup.sh`**
- Add `pmset_apply autorestart 1` in Section 1 (Power Management), after `networkoversleep`
  (around line 153). Add a comment: `# Restart after power failure — essential for 24/7
  headless operation`.

**`verify.sh`**
- Add `check_pmset "autorestart" "1"` in the SYSTEM section alongside the other pmset
  checks (around line 127).

---

## Phase B — Security

### B1: Flip insecure network defaults in config.json

**Problem:** `config.json` ships with `localhost_only: false` and `disable_firewall: true`.
On a home LAN this exposes unauthenticated Ollama/mlx-lm/Infinity APIs to every device,
including Ollama's model pull/delete endpoints.

**Changes:**

**`config.json`**
- Change `"localhost_only": false` → `"localhost_only": true`.
- Change `"disable_firewall": true` → `"disable_firewall": false`.

**`setup.sh`** (Section 6, Application Firewall, lines 447–475)
- Update the comment block to reflect the new default: firewall is now left **on** by
  default; users who need to disable it (e.g. unsigned Python services on a trusted
  isolated LAN) set `disable_firewall: true`.
- The logic in `setup.sh` already handles both states; only the comment and the jq
  fallback value need to change (`// false` instead of `// true`).

**`README.md`**  
- Add a note in the Quick Start or Configuration section explaining: "By default the
  firewall is left enabled and services bind to `localhost`. Change
  `network.localhost_only` and `network.disable_firewall` in `config.json` if your
  inference clients are on other LAN hosts."

---

### B2: Replace sshd_config sed-editing with a drop-in file

**Problem:** `setup.sh` edits `/etc/ssh/sshd_config` directly via `sed`. macOS updates
replace that file, silently removing the hardening. Modern macOS supports drop-in files
under `/etc/ssh/sshd_config.d/` which survive OS updates. Additionally, setting
`PasswordAuthentication no` before confirming key-based login is working is a lockout
risk on a headless box.

**Changes:**

**`setup.sh`** (Section 4, SSH Hardening, lines 374–409)
- Remove the `SSHD_BACKUP`, `set_sshd()` helper, and all `sed`/`tee -a` logic.
- Replace with a block that writes
  `/etc/ssh/sshd_config.d/100-headless.conf` containing the same six directives:

  ```
  PermitRootLogin no
  PasswordAuthentication no
  PubkeyAuthentication yes
  MaxAuthTries 3
  ClientAliveInterval 120
  ClientAliveCountMax 10
  ```

- Add a **precheck gate** before writing `PasswordAuthentication no`: check whether
  `~/.ssh/authorized_keys` or `/etc/ssh/authorized_keys` exists for the current user.
  If neither exists, print a `[WARN]` and skip the `PasswordAuthentication no` line
  (write all other directives). Document the manual step:
  `"Copy your public key to ~/.ssh/authorized_keys before re-running setup.sh to enable
  key-only login."`
- Idempotency: skip if `/etc/ssh/sshd_config.d/100-headless.conf` already exists and
  its contents are unchanged (simple `diff` or checksum check — same guard pattern used
  elsewhere in the script).
- Because macOS sshd is launchd socket-activated, drop the explicit
  `launchctl stop/start` — new connections pick up the drop-in automatically. Keep an
  informational `[SET] sshd drop-in written` line.

**`verify.sh`**
- Replace the current SSH port-listen check (line 149) with a check that the drop-in
  file exists and that `PasswordAuthentication` is set to `no` only when
  `authorized_keys` is present.

---

## Phase C — Operational hardening

### C1: Raise maxfiles via LaunchDaemon

**Problem:** Concurrent model loading and parallel inference connections can exhaust the
default file-descriptor limit. A `limit.maxfiles` LaunchDaemon is standard practice for
inference servers and fits the existing plist pattern.

**Changes:**

**`setup.sh`** — add a new Section 7 (after the firewall section):
- Install `/Library/LaunchDaemons/com.llm-server.maxfiles.plist` with:
  ```xml
  <key>Label</key><string>com.llm-server.maxfiles</string>
  <key>ProgramArguments</key>
  <array>
    <string>launchctl</string>
    <string>limit</string>
    <string>maxfiles</string>
    <string>524288</string>
    <string>1048576</string>
  </array>
  <key>RunAtLoad</key><true/>
  ```
- Idempotency: skip if plist already exists.
- Apply live: `sudo launchctl limit maxfiles 524288 1048576` for the current session.

**`verify.sh`**
- Add a check: `launchctl limit maxfiles` and confirm soft ≥ 524288.

---

### C2: pmset self-heal via daily launchd timer

**Problem:** `known-issues.md` documents that macOS updates reset pmset values, with
"re-run `setup.sh`" as the manual fix. Since all scripts are idempotent, a launchd timer
can close this gap automatically.

**Changes:**

**`setup.sh`** — add to Section 1 (Power Management), after the caffeinate plist block:
- Install `/Library/LaunchDaemons/com.llm-server.pmset-heal.plist`:
  - `StartCalendarInterval` → daily at 03:00.
  - `ProgramArguments` → `/bin/bash /path/to/setup.sh --power-only` (see below).
  - `StandardOutPath` / `StandardErrorPath` → `/var/log/mac-llm-setup/pmset-heal.log`.
- Idempotency: skip if plist already exists.

**`setup.sh`** — add a `--power-only` flag:
- When invoked with `--power-only`, run only Section 1 (pmset block + caffeinate daemon
  check) and exit. This makes the timer re-run safe without triggering SSH hardening,
  service suppression, etc.
- The flag is only needed for the timer invocation; normal `setup.sh` runs are unchanged.

**`docs/known-issues.md`**
- Update the "pmset values reset after macOS update" row: change the Fix column from
  "Re-run `sudo ./setup.sh`" to "Handled automatically by the `com.llm-server.pmset-heal`
  daily timer installed by `setup.sh`. Manual re-run still works."

---

## Files touched summary

| File | Phases |
|---|---|
| `setup.sh` | A1, A2, B1, B2, C1, C2 |
| `verify.sh` | A1, A2, B2, C1 |
| `config.json` | B1 |
| `README.md` | B1 |
| `docs/known-issues.md` | C2 |
| New: `/Library/LaunchDaemons/com.llm-server.sysctl-tuning.plist` (written at runtime) | A1 |
| New: `/etc/ssh/sshd_config.d/100-headless.conf` (written at runtime) | B2 |
| New: `/Library/LaunchDaemons/com.llm-server.maxfiles.plist` (written at runtime) | C1 |
| New: `/Library/LaunchDaemons/com.llm-server.pmset-heal.plist` (written at runtime) | C2 |

---

## Phase D — Ollama lifecycle management (new findings)

Two questions raised during planning revealed additional gaps not in Jeff's original
feedback.

### D1: Ollama login-item / app conflict — already handled, with one gap

**Finding:** The login-item removal and app-kill are already implemented in two places:
- `install-tools.sh:155-158`: `osascript ... delete login item "Ollama"` + `pkill -f "Ollama.app"`
- `scripts/ollama_setup.sh:151-156`: same two calls inside `install_ollama()`

The design is correct — the installer removes the GUI login item and kills any running
app instance before bootstrapping the LaunchDaemon. **No change needed for the `.app` path.**

**Gap:** If a user previously installed Ollama via Homebrew (`brew install ollama`),
`brew services` may have registered a separate LaunchDaemon (`homebrew.mxcl.ollama`).
The current code does not stop or disable that service, which can cause port 11434
conflicts when our `com.ollama.server` daemon starts.

**Change:**

**`install-tools.sh`** (Ollama section, around line 155):
- Before bootstrapping `com.ollama.server`, add a check-and-stop for any Homebrew
  Ollama service:
  ```bash
  # Stop any Homebrew-managed Ollama service to avoid port 11434 conflict
  if brew services list 2>/dev/null | grep -q "^ollama"; then
    brew services stop ollama 2>/dev/null || true
    echo "[SET]  Stopped Homebrew Ollama service (our LaunchDaemon takes over)"
  fi
  ```
- This is safe to run even if Homebrew isn't installed (the `brew services list` guard
  prevents errors).

---

### D2: Ollama updates — no mechanism exists

**Finding:** `install-tools.sh:138-143` skips Ollama entirely if `ollama` is already in
`$PATH`. The `ollama.com/install.sh` installer is idempotent and upgrades in-place, but
the current code never calls it a second time. After a binary update, the LaunchDaemon
also needs to be bounced to serve from the new binary. There is no `update` path in any
script.

**Change:** Add a new `update-tools.sh` script with an Ollama update flow:

```
update-tools.sh [ollama]
```

Steps performed for Ollama:
1. Stop the daemon: `sudo launchctl bootout system /Library/LaunchDaemons/com.ollama.server.plist`
2. Run the upstream installer: `curl -fsSL https://ollama.com/install.sh | sh`
3. Re-remove the login item (the installer may re-add it): `osascript ...` + `pkill -f "Ollama.app"`
4. Re-bootstrap the daemon: `sudo launchctl bootstrap system /Library/LaunchDaemons/com.ollama.server.plist`
5. Verify the API responds.

The script will be structured to accept a component argument (`ollama` initially; extensible
to `rapid-mlx`, `mlx-lm` etc. later). It follows the same idempotency-and-logging pattern
as `install-tools.sh`.

**`manage.sh`**
- Add an `update` command alongside `install` / `enable` / `disable` / `remove`.
  The interactive menu gains an entry: `16) Update Ollama`.

**`README.md`**
- Add an "Updating Ollama" section pointing to `sudo ./update-tools.sh ollama`.

**`docs/known-issues.md`**
- No Ollama-specific update issue is documented currently; add a row in the Ollama
  table: "Ollama binary update doesn't restart daemon — use `sudo ./update-tools.sh ollama`."

---

## Files touched summary (updated)

| File | Phases |
|---|---|
| `setup.sh` | A1, A2, B1, B2, C1, C2 |
| `verify.sh` | A1, A2, B2, C1 |
| `config.json` | B1 |
| `README.md` | B1, D2 |
| `docs/known-issues.md` | C2, D2 |
| `install-tools.sh` | D1 |
| `manage.sh` | D2 |
| New: `update-tools.sh` | D2 |
| New: `/Library/LaunchDaemons/com.llm-server.sysctl-tuning.plist` (written at runtime) | A1 |
| New: `/etc/ssh/sshd_config.d/100-headless.conf` (written at runtime) | B2 |
| New: `/Library/LaunchDaemons/com.llm-server.maxfiles.plist` (written at runtime) | C1 |
| New: `/Library/LaunchDaemons/com.llm-server.pmset-heal.plist` (written at runtime) | C2 |

---

## Out of scope for this phase

**Item #4 (daemons run as root):** Valid security improvement — running `UserName` +
`HOME` in `EnvironmentVariables` inside each LaunchDaemon plist instead of `HOME=/var/root`
as root. Deferred because it requires modifying `install-tools.sh`'s plist generation for
every tool (Ollama, Rapid-MLX, mlx-lm, Infinity), testing that `OLLAMA_MODELS` is honoured
under the unprivileged user, and likely an update to `known-issues.md`. Tracking as a
follow-on issue.
