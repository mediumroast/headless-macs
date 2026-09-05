# FUTURES.md — Planned Improvements

Items here are confirmed improvements worth making but not yet scheduled.
When an item is picked up, move it into a `PHASE_N_PLAN.md` and delete it here.

---

## Ollama Operational Hardening

### 1. Suppress stderr log spam from symlinked models directory

**Problem:** When `OLLAMA_MODELS` points to a symlink (e.g.
`/Library/Ollama/models → /Volumes/LLMStorage/models/ollama`), Ollama logs
`Error: mkdir /Library/Ollama/models: file exists: ensure path elements are
traversable` on every startup. Models work correctly — this is an Ollama
bug with symlink detection during init. The error loops until the daemon is
fully up, producing large volumes of noise in `stderr.log`.

**Fix:** Set `OLLAMA_MODELS` in the LaunchDaemon plist to the **resolved
absolute path** on the external volume, bypassing the symlink:

```xml
<key>OLLAMA_MODELS</key><string>/Volumes/LLMStorage/models/ollama</string>
```

This should be added to the `EnvironmentVariables` dict in
`/Library/LaunchDaemons/com.ollama.server.plist` and wired into
`install-tools.sh` / the Go ops layer so it is set automatically when
`storage.use_external_volume` is true and the resolved path is known.

**Scope:** `internal/ops/tools.go` (Ollama plist generation) + `config.json`
(no new config key needed — derive from `storage.volume_mount_point` +
`storage.models_subdir` + `"ollama"`).

---

### 2. Ollama log rotation and verbosity control

**Problem:** Ollama is extremely chatty at its default log level. On a busy
inference node `stderr.log` can reach 2+ GB within weeks. No rotation policy
is currently applied.

**Two-part fix:**

**Part A — Reduce verbosity at the source.**

Two independent log streams feed into `stderr.log`:

1. **Ollama's own Go logger** — controlled by `OLLAMA_LOG_LEVEL`. Setting this
   to `warn` suppresses Ollama's own request lines and model-loading info.

2. **llama-server's C++ logger** — produces all `slot`, `srv`, `cmn`, `sched`
   lines. **This cannot be suppressed by any env var in Ollama ≤ 0.33.2.**

   Ollama hardcodes `--log-verbosity 4` in the llama-server command. The env var
   `LLAMA_ARG_LOG_VERBOSITY` is explicitly overridden by that CLI arg (confirmed
   by runtime warning: `LLAMA_ARG_LOG_VERBOSITY … will be overwritten by command
   line argument --log-verbosity`).

   Lowering verbosity is also architecturally impossible: in the pinned
   llama.cpp version (`b10488`) the verbosity scale is `1=ERROR 2=WARN 3=INFO
   4=TRACE`, and the per-request noise is **split across INFO and TRACE**:
   - Timing/slot-selection lines → `SLT_INF` (level 3)
   - Sampler/cache/idle lines → `SLT_TRC` (level 4)

   Dropping to level 3 leaves all timing and slot-selection output. Dropping to
   level 2 loses the memory/offload startup lines Ollama's scheduler parses for
   accounting — this breaks scheduler correctness. Tried and rejected in
   ollama/ollama#16899.

   **Upstream fix (not yet merged):** Two open PRs implement a `runnerLogFilter`
   writer in Ollama's Go layer that filters known-routine lines before writing to
   stderr, leaving `--log-verbosity 4` and all memory parsing intact:
   - [ollama/ollama#17913](https://github.com/ollama/ollama/pull/17913) — allowlist-based filter
   - [ollama/ollama#16941](https://github.com/ollama/ollama/pull/16941) — similar approach

   When one of these merges, `OLLAMA_DEBUG=1` will bypass the filter (raw output
   for debugging); the default will be filtered. **Watch for this in the next
   Ollama minor release after 0.33.2.**

   **Current mitigation:** logrotate at 100 MB / 5 copies bounds total stderr
   to ~500 MB. Growth rate observed on doppio-1: ~20 MB/day at the current
   gpt-oss:120b workload.

Add `OLLAMA_LOG_LEVEL=warn` to the Ollama plist and wire into the ops layer:

```xml
<key>OLLAMA_LOG_LEVEL</key><string>warn</string>
```

```json
"tools": {
  "ollama": {
    "log_level": "warn"
  }
}
```

Do **not** add `LLAMA_ARG_LOG_VERBOSITY` to the plist — Ollama overrides it and
it generates a spurious startup warning.

**Part B — Add logrotate rotation.**

**Do not use newsyslog** for this. newsyslog rotates by renaming the log file,
which leaves the running process's file descriptor pointing to the old inode —
new writes go to the rotated file, not the fresh empty one. Ollama (and all
serving daemons) are managed by launchd via `StandardOutPath`/`StandardErrorPath`;
launchd opens the fd once at daemon start and does not reopen it after a rename.
The result is that no new log entries are written after rotation. Confirmed on
doppio-1.

Use **logrotate** (Homebrew) with `copytruncate` instead. `copytruncate`
copies the log content then truncates the original file to zero bytes in place,
leaving the fd valid — no restart needed.

**Do not use `brew services start logrotate`** — Homebrew's config file
(`/opt/homebrew/etc/logrotate.conf`) is owned by the brew user, not root.
logrotate refuses to read config files not owned by root when running as root.
Use a custom `com.llm-server.logrotate` LaunchDaemon instead (see below).

**Setup steps (validated on doppio-1):**

```bash
# 1. Install logrotate
brew install logrotate

# 2. Create the config directory if it doesn't exist (not present by default on macOS)
sudo mkdir -p /etc/logrotate.d

# 3. Create a root-owned config (sudo tee ensures root ownership)
sudo tee /etc/logrotate.d/llm-servers << 'EOF'
/var/log/ollama/stderr.log /var/log/ollama/stdout.log {
    size 100M
    rotate 5
    compress
    copytruncate
    missingok
    notifempty
    create 644 _llmserver wheel
}
EOF

# 4. Test the config (dry-run, no rotation)
sudo /opt/homebrew/opt/logrotate/sbin/logrotate -d /etc/logrotate.d/llm-servers

# 5. Create the LaunchDaemon plist
sudo tee /Library/LaunchDaemons/com.llm-server.logrotate.plist > /dev/null << 'EOF'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.llm-server.logrotate</string>
    <key>ProgramArguments</key>
    <array>
        <string>/opt/homebrew/opt/logrotate/sbin/logrotate</string>
        <string>-s</string>
        <string>/var/log/mac-llm-setup/logrotate.status</string>
        <string>/etc/logrotate.d/llm-servers</string>
    </array>
    <key>StartCalendarInterval</key>
    <dict>
        <key>Hour</key>
        <integer>2</integer>
        <key>Minute</key>
        <integer>0</integer>
    </dict>
    <key>RunAtLoad</key>
    <false/>
    <key>StandardOutPath</key>
    <string>/var/log/mac-llm-setup/logrotate-stdout.log</string>
    <key>StandardErrorPath</key>
    <string>/var/log/mac-llm-setup/logrotate-stderr.log</string>
</dict>
</plist>
EOF

# 6. Set permissions and load
sudo chown root:wheel /Library/LaunchDaemons/com.llm-server.logrotate.plist
sudo chmod 644 /Library/LaunchDaemons/com.llm-server.logrotate.plist
sudo launchctl bootstrap system /Library/LaunchDaemons/com.llm-server.logrotate.plist

# 7. Force an initial rotation to baseline logs before the next run
sudo /opt/homebrew/opt/logrotate/sbin/logrotate -f \
    -s /var/log/mac-llm-setup/logrotate.status \
    /etc/logrotate.d/llm-servers
```

The plist fires at 2 AM daily (`RunAtLoad = false` — does not rotate on boot).
State is tracked at `/var/log/mac-llm-setup/logrotate.status`, which is in the
existing pipeline log directory. stdout/stderr from the rotation job go to
`/var/log/mac-llm-setup/logrotate-stdout.log` and `logrotate-stderr.log` for
debugging.

At 100 MB per file with 5 copies, maximum stderr storage is ~500 MB.

**Scope:** `internal/ops/tools.go` (write `/etc/logrotate.d/llm-servers` and
install plist during Install Tools) + `internal/ops/restore.go` (remove both
on restore) + `config.json` (optional `log_level` key under `tools.ollama`).

---

## Log Management

### 1. Ollama — log verbosity and rotation

*(Verbosity fix applied manually on doppio-1: `OLLAMA_LOG_LEVEL=warn` and
`OLLAMA_MODELS` set to resolved path. Needs to be wired into the ops layer.)*

See Items 1 and 2 below for the implementation plan.

---

### 10. Serving tool log rotation and verbosity — Rapid-MLX, mlx-lm, Infinity, Exo

**Problem:** Only Ollama has a log verbosity control (`OLLAMA_LOG_LEVEL`).
The remaining serving tools have no verbosity control and no log rotation
configured — logs can grow unbounded. Exo additionally logs to `/tmp/` instead
of a persistent `/var/log/exo/` directory, meaning logs are lost on reboot and
are mixed into the system temp space.

**Findings per tool:**

| Tool | Log location | Verbosity control | Rotation |
|---|---|---|---|
| Rapid-MLX | `/var/log/rapid-mlx/` | None | None |
| mlx-lm | `/var/log/mlx-lm/` | None | None |
| Infinity | `/var/log/infinity/` | None | None |
| Exo | `/tmp/` (**wrong**) | None | None |

**Fix — Part A: Log verbosity**

Add `--log-level WARNING` to `ProgramArguments` for Python-based tools where
supported:

- `mlx-lm`: add `--log-level WARNING` to the `mlx_lm.server` invocation in
  `mlxLMPlist()` in `internal/ops/tools.go`
- `Infinity`: add `--log-level WARNING` to the `infinity_emb` invocation in
  `infinityPlist()`
- `Rapid-MLX`: check if `rapid-mlx serve` accepts `--log-level`; if not, set
  `PYTHONWARNINGS=ignore` and `LOGLEVEL=WARNING` in `EnvironmentVariables` as
  a fallback
- `Exo`: check if `exo` CLI accepts a log level flag; add if available

**Fix — Part B: Exo log directory (bug)**

Move Exo log paths from `/tmp/` to `/var/log/exo/`. Create the directory in
`installExo()` alongside the other tools:

```go
_ = os.MkdirAll("/var/log/exo", 0755)
```

Update `exoPlist()`:
```
StandardOutPath  →  /var/log/exo/stdout.log
StandardErrorPath →  /var/log/exo/stderr.log
```

**Fix — Part C: logrotate rotation for all tools**

Use **logrotate** (not newsyslog) with `copytruncate`. See Item 2 Part B for
the full rationale — newsyslog's rename approach leaves all launchd-managed
daemons writing to the old inode after rotation. Confirmed broken on doppio-1.

Extend `/etc/logrotate.d/llm-servers` to cover all enabled serving tools:

```
/var/log/ollama/stderr.log
/var/log/ollama/stdout.log
/var/log/rapid-mlx/stderr.log
/var/log/rapid-mlx/stdout.log
/var/log/mlx-lm/stderr.log
/var/log/mlx-lm/stdout.log
/var/log/infinity/stderr.log
/var/log/infinity/stdout.log
/var/log/exo/stderr.log
/var/log/exo/stdout.log
{
    size 100M
    rotate 5
    compress
    copytruncate
    missingok
    notifempty
    create 644 _llmserver wheel
}
```

The `com.llm-server.logrotate` LaunchDaemon (from Item 2 Part B) covers all
tools from a single plist — no additional daemon needed.

**Prerequisites before writing the config:**
- `sudo mkdir -p /etc/logrotate.d` — this directory does not exist by default
  on macOS and must be created explicitly
- Log directories for each tool must exist and be owned `_llmserver:wheel`
  before the daemon first starts
- Config file must be written with `sudo tee` so it is root-owned; logrotate
  refuses to read config files not owned by root when running as root

Rotation at 100 MB, 5 copies = max ~500 MB stderr per tool.

**Scope:** `internal/ops/tools.go` — all four `*Plist()` functions + log
directory creation for Exo + write `/etc/logrotate.d/llm-servers` and install
`com.llm-server.logrotate` plist during Install Tools.
`internal/ops/restore.go` — remove `/etc/logrotate.d/llm-servers` and bootout
`com.llm-server.logrotate` on restore.

---

## Notes (Ollama)

- Items 1 and 2 are related and should be implemented together in the same
  phase — both touch the Ollama plist generation in the ops layer.
- The logrotate config and `com.llm-server.logrotate` plist must be added to
  the restore ops so they are cleaned up on restore.
- The `OLLAMA_MODELS` resolved-path logic applies only when
  `storage.use_external_volume` is `true`. When using internal storage the
  default Ollama path should be left unchanged.

---

## Unnecessary Service Suppression

Services observed running on doppio-1 (M4 Max, 128 GB) that have no purpose
on a headless LLM inference node. Each adds CPU wakeups, background I/O,
and/or network activity that degrades inference latency and power efficiency.
None reclaim significant RAM individually, but together they tighten the node.

### 3. Suppress Content Caching (AssetCache)

**Problem:** `AssetCache`, `AssetCacheLocatorService`, and
`AssetCacheTetheratorService` are running. This is macOS's LAN content-caching
service — it proxies Apple software downloads for other devices on the network.
It has no value on an inference node and generates unnecessary disk and network
I/O.

**Fix:** Add to `setup.sh` / System Baseline ops:

```bash
sudo launchctl disable system/com.apple.AssetCache.builtin
sudo launchctl bootout system /System/Library/LaunchDaemons/com.apple.AssetCache.builtin.plist 2>/dev/null || true
```

Note: requires SIP off for the disable to persist across reboots (same
SIP-gating pattern as other service suppression in `setup.sh`).

**Scope:** `internal/ops/baseline.go` — add to service suppression section.
Add a corresponding check in `verify.go`.

---

### 4. Suppress mobileassetd

**Problem:** `mobileassetd` (72 MB RSS) downloads and manages Apple device
firmware assets — iPhone/iPad restore images, carrier bundles, etc. It is the
largest unnecessary process by RSS on a headless inference node.

**Fix:** Add to System Baseline ops:

```bash
sudo launchctl disable system/com.apple.MobileAssetUpdater
sudo launchctl bootout system /System/Library/LaunchDaemons/com.apple.MobileAssetUpdater.plist 2>/dev/null || true
```

**Scope:** `internal/ops/baseline.go` — service suppression section.

---

### 5. Suppress audio stack (coreaudiod, audiomxd)

**Problem:** `coreaudiod`, `audiomxd`, and associated CoreAudio driver processes
are running. No speakers, no microphone, and no audio use case exist on an
inference node. The audio stack generates periodic CPU wakeups.

**Fix:**

```bash
sudo launchctl disable system/com.apple.audio.coreaudiod
sudo launchctl disable system/com.apple.audiomxd
```

**Scope:** `internal/ops/baseline.go` — service suppression section. May
require SIP off to persist.

---

### 6. Suppress Find My beaconing (findmybeaconingd)

**Problem:** `findmybeaconingd` runs the Find My Network beacon — it
periodically broadcasts the device's location to Apple's Find My network.
No value on a rack/desk inference node; generates unnecessary Bluetooth and
network activity.

**Fix:**

```bash
sudo launchctl disable system/com.apple.findmybeaconingd
sudo launchctl bootout system /System/Library/LaunchDaemons/com.apple.findmybeaconingd.plist 2>/dev/null || true
```

**Scope:** `internal/ops/baseline.go` — service suppression section.

---

### 7. Suppress AirPlay helper (AirPlayXPCHelper)

**Problem:** `AirPlayXPCHelper` enables AirPlay receiver/sender functionality.
Not needed on a headless node.

**Fix:**

```bash
sudo launchctl disable system/com.apple.AirPlayXPCHelper
```

**Scope:** `internal/ops/baseline.go` — service suppression section.

---

### 8. Docker vmnetd — document as operator action

**Problem:** `com.docker.vmnetd` (Docker's privileged networking helper) is
running as root on doppio-1. headless-macs did not install Docker — it arrived
via a separate operator action. It adds a root-level network daemon and
background overhead on every boot.

**Fix:** This cannot be automated by headless-macs (it did not install Docker
and should not uninstall third-party software). Document in `precheck.sh` /
`RunPrecheck` as a `[WARN]` if Docker's vmnetd plist is detected:

```
/Library/LaunchDaemons/com.docker.vmnetd.plist
```

Print: `[WARN] Docker vmnetd detected — remove Docker if not required on this
inference node`.

**Scope:** `internal/ops/precheck.go` — add to service audit section.

---

### 9. Warn when high-memory tools are enabled together

**Problem:** Rapid-MLX loads its full model into unified memory on daemon start
and holds it until the daemon is stopped — regardless of whether any requests
are being served. On doppio-1 with qwen3-aftertaste-fused, this was ~20-25 GB
continuously consumed. When Rapid-MLX and Ollama are both enabled,
`MAX_LOADED_MODELS` for Ollama should be recalculated to account for the
Rapid-MLX model footprint, otherwise the node can be overcommitted.

Note: `rapid_mlx.enabled` is already `false` in the default `config.json`
template — this is correct. The issue is operator documentation: users enabling
Rapid-MLX should understand it permanently pins its model in memory and plan
their Ollama `MAX_LOADED_MODELS` accordingly.

**Fix:**
- Add a `[WARN]` to `RunPrecheck` when both `rapid_mlx.enabled` and
  `ollama.enabled` are true, noting that Rapid-MLX holds its model in memory
  continuously and the operator should account for this in Ollama tuning.
- Add a note to `docs/tool-comparison.md` and the README under Rapid-MLX's
  entry explaining the always-resident memory model.
- Consider adjusting the Ollama RAM-tuning logic in `install-tools` to subtract
  an estimated Rapid-MLX model footprint when both tools are enabled.

**Scope:** `internal/ops/precheck.go` (warn), `docs/tool-comparison.md` (note),
optionally `internal/ops/tools.go` (tuning adjustment).

---

## Notes (Service Suppression)

- Items 3–7 belong in the same phase — all are additions to the System Baseline
  service suppression section alongside the existing Spotlight, iCloud, etc.
  suppressions.
- All require the SIP-gating pattern from `setup.sh`: `disable` is skipped with
  `[SKIP-SIP]` when SIP is on; `bootout` is attempted regardless.
- Each suppressed service needs a corresponding `[PASS]/[WARN]` check in
  `verify.go` confirming it is not running.
- Item 8 (Docker) is precheck-only — headless-macs should warn, not act.

---

## Hardware Telemetry — macmon Integration

### 11. Install macmon as a root-level (system) daemon for CPU/GPU/ANE/thermal telemetry

**What it is:** [`macmon`](https://github.com/vladkens/macmon) is a Rust
tool that reads CPU/GPU/ANE power draw, per-cluster core frequency and
utilization, temperatures, fan RPM, and memory/swap stats on Apple Silicon.
It reads this through a private macOS API (the same data `powermetrics`
exposes) and — notably — **does not require root**, unlike `powermetrics`,
`pumas`, or `mactop`. It ships three relevant modes:

- `macmon pipe` — one-shot/streaming JSON output
- `macmon serve` — HTTP server exposing `GET /json` (snapshot) and
  `GET /metrics` (Prometheus text format)
- interactive TUI (not relevant for a headless daemon)

This would give `RunVerify`/`RunPrecheck` and any future observability work
(Grafana/Prometheus, see Item 12) real thermal/power telemetry that the
project currently has no source for — useful for confirming an inference
node isn't throttling under sustained load.

**The packaged install method doesn't fit this project's daemon model.**
Upstream ships `macmon serve --install`, which writes a **LaunchAgent** to
`~/Library/LaunchAgents/com.macmon.plist` — a per-user, login-session-scoped
service (Aqua/WindowServer bootstrap domain), auto-started at login. This
assumes an interactively-used Mac with a logged-in user, and needs no root
because LaunchAgents install without `sudo`.

This is the opposite of how every other service in this project runs.
`headless-macs` boxes are unattended: there is no guaranteed interactive
login session (see the auto-login gotcha in the "Known non-obvious
constraints" section of `CLAUDE.md`), and every serving daemon here is a
**system LaunchDaemon** running as the `_llmserver` service account, not a
per-user LaunchAgent. Using `macmon serve --install` as-is would silently
stop reporting the moment the box reboots without an active login session —
exactly the failure mode this project is designed to avoid.

**Confirmed on doppio-1 (2026-09-05):** macmon's telemetry access does *not*
require a GUI/Aqua session. A throwaway `com.llm-server.macmontest` system
LaunchDaemon, `UserName _llmserver`, `bootstrap system`, was tested with no
active console session:

```bash
sudo launchctl bootstrap system /Library/LaunchDaemons/com.llm-server.macmontest.plist
```

`stdout.log` returned real non-zero telemetry (`cpu_power: 0.0134`,
`sys_power: 8.57`, `cpu_temp_avg: 36.95`, live memory figures) with an empty
`stderr.log` — no panic, no permission error. IOReport access works from the
system bootstrap domain, running unprivileged, with nobody logged into the
console. The fallback path below (documenting this as a limitation) does
not apply — proceed with the LaunchDaemon implementation.

Write our own `com.llm-server.macmon` LaunchDaemon following the existing
infrastructure-daemon conventions in this file (write-once idempotent, not
tool-config-driven like the serving-tool daemons) — do **not** shell out to
`macmon serve --install`, since that writes to a path and domain this
project doesn't manage or clean up on Restore.

- Run as `_llmserver` (`UserName` key), not root — macmon needs no elevated
  privilege, and running a telemetry reader as root would be an unnecessary
  privilege escalation the rest of this project deliberately avoids.
- `HOME` still required in `EnvironmentVariables` per the standard daemon
  template (same panic risk as Ollama/mlx-lm if omitted — untested for
  macmon specifically, but cheap to include defensively).
- Log to `/var/log/macmon/{stdout,stderr}.log`, owned `_llmserver:wheel`,
  created before the plist is written (same pattern as every other tool).
- New config block:
  ```json
  "tools": {
    "macmon": {
      "enabled": false,
      "port": 9090,
      "interval_ms": 1000
    }
  }
  ```
- Add a MACMON section to `verify.go` with `check_http "macmon" "http://127.0.0.1:9090/json" "cpu_power"`.
- Add bootout + plist removal to `restore.go`.

**Confirmed working `ProgramArguments` on doppio-1 (macmon via `brew install
macmon`, 2026-09-05):**

```xml
<key>ProgramArguments</key>
<array>
    <string>/opt/homebrew/bin/macmon</string>
    <string>serve</string>
    <string>-p</string>
    <string>9090</string>
    <string>-i</string>
    <string>1000</string>
</array>
```

Verified: `state = running` under `_llmserver` with no console session,
`GET /json` returns real telemetry including `soc` chip info (cores, GPU
core count, frequency tables), and the port is reachable both on
`127.0.0.1` and the machine's LAN interface.

**`network.localhost_only` honoring needs a different approach than every
other tool.** The installed `macmon serve --help` on doppio-1 has **no
`--host`/`--bind` flag at all** — only `-p`/`--port` and `-i`/`--interval`.
(The upstream README documents `--host`, e.g. `macmon serve --host
127.0.0.1`, but the currently-brewed build doesn't expose it — a version gap
that needs re-checking at implementation time; `macmon --version` /
`brew info macmon` should be checked against the installed version before
assuming either flag exists.) Unlike Ollama/mlx-lm/Infinity, where
`install-tools.sh` sets a `host` field per config, macmon on this build
**always binds all interfaces** — confirmed by curling it from the LAN IP
in addition to loopback. If `network.localhost_only` is true and the
installed macmon version still lacks `--host`, binding restriction has to
happen at the network layer (a `pf` rule blocking non-loopback access to
9090, applied/removed alongside the daemon) rather than via a CLI flag —
or the ops layer should pin/require a macmon version new enough to support
`--host` if that check is cheap to add to `installMacmon()`.

**Scope:** `internal/ops/tools.go` (new `installMacmon()` + `macmonPlist()`),
`internal/ops/verify.go` (MACMON section), `internal/ops/restore.go`
(bootout `com.llm-server.macmon` + remove plist + remove `/var/log/macmon`),
`config.json` (new `tools.macmon` block). Per the Planning convention in
`CLAUDE.md`, this needs its own `PHASE_N_PLAN.md` before implementation — it
touches more than 2 files. The feasibility question is resolved; what's left
is normal implementation work.

---

## Security — Unauthenticated, Unencrypted Serving Endpoints

### 12. No TLS or authentication in front of any serving-tool daemon

**Problem (quick assessment, not a full design):** Every serving daemon this
project installs — Ollama, Rapid-MLX, mlx-lm, Infinity, Exo, and the
proposed macmon HTTP server above — binds plain HTTP with no authentication.
`network.localhost_only` controls *what interface* a tool binds to, but it
is not a security boundary once an operator legitimately sets it to `false`
for multi-machine inference (the documented, intended use case for a
dedicated inference node). At that point:

- Anyone who can reach the port can submit inference requests and consume
  compute, with no rate limiting or accounting.
- Ollama's HTTP API is not read-only — `/api/pull`, `/api/push`, and
  `/api/delete` allow an unauthenticated caller to download arbitrary models
  (disk/bandwidth exhaustion) or delete installed ones.
- Nothing is encrypted in transit — request/response bodies (prompts,
  completions) cross the network in cleartext, including on shared or
  untrusted LANs.
- None of Ollama, Rapid-MLX, mlx-lm, or Infinity have built-in auth or TLS
  support to turn on — this has to be solved outside the tool.

**Note on doppio-1:** the manually-installed `com.llm-server.macmon` daemon
(Item 11 dogfooding) is intentionally bound to `0.0.0.0:9090` with no
auth — a deliberate, temporary development-stage choice, not the intended
production posture. When Item 12 lands, macmon should move to loopback-only
with the rest of the serving tools, fronted by the same gateway.

**Direction (for a future phase, not sized yet):** The common fix for
"multiple backend services, none of which speak TLS or auth" is a reverse
proxy in front of them, terminating TLS and enforcing an API key or basic
auth, rather than patching each tool individually. A lightweight option
(e.g. Caddy, which does automatic TLS and has trivial config) run as its
own `com.llm-server.proxy`-style LaunchDaemon, with the underlying tools
force-bound to `127.0.0.1` regardless of `network.localhost_only` (the proxy
becomes the only listener on a non-loopback interface). This would need:

- A new `network.require_auth` / `network.tls` section in `config.json`.
- A decision on credential storage/rotation (a static API key is simplest
  but weakest; needs to not land in shell history or world-readable config).
- Changes to `install-tools.sh`'s / the Go ops layer's interpretation of
  `localhost_only` — today it sets each tool's own bind host; it would need
  to instead always force loopback and let the proxy own the external bind.
- A corresponding `verify.go` check that the proxy, not the raw tool port,
  is what's reachable from a non-loopback interface.

**Scope:** Not yet sized — this is flagged for design, not implementation.
Per the Planning convention, this needs a `PHASE_N_PLAN.md` with its own
scope decision table before any code is written. Candidate for Phase 8.

---
