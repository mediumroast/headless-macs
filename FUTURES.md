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

1. **Ollama's own Go logger** — controlled by `OLLAMA_LOG_LEVEL`. Set to `warn`
   to suppress GIN request lines and model-loading info. Note: `OLLAMA_LOG_LEVEL`
   does not appear in `ollama serve --help` (0.33.2); the recognized variable is
   `OLLAMA_DEBUG`. Behavior should be verified on the installed version.

2. **llama-server's C++ logger** — produces all slot operation, KV cache, and
   scheduling detail (lines prefixed `slot`, `srv`, `cmn`, `sched`). This stream
   is **not suppressible via environment variable** in Ollama 0.33.2.

   `llama-server --help` documents `LLAMA_ARG_LOG_VERBOSITY` as the env var for
   `--log-verbosity`, and `ollama serve --help` lists it as a passthrough.
   However, Ollama 0.33.2 hardcodes `--log-verbosity 4` in the llama-server
   command it constructs, and the CLI arg explicitly overrides the env var.
   Confirmed by the warning emitted at runtime:

   ```
   warn: LLAMA_ARG_LOG_VERBOSITY environment variable is set,
         but will be overwritten by command line argument --log-verbosity
   cmn  common_param: verbosity = 4
   ```

   This occurs even when `OLLAMA_DEBUG` is not set (log shows `OLLAMA_DEBUG:INFO`
   with debug disabled). Verbosity 4 is hardcoded regardless of debug mode in
   this version.

   **Current mitigation:** logrotate (100 MB / 5 copies) bounds total stderr
   storage to ~500 MB. The slot/srv/cmn output is useful diagnostically.

   **Upstream fix needed:** Ollama should expose a `OLLAMA_RUNNER_LOG_VERBOSITY`
   or similar variable that is not overridden by the hardcoded CLI arg. File an
   issue against Ollama if this remains a problem on newer versions.

Add `OLLAMA_LOG_LEVEL=warn` to the Ollama plist and wire it into the ops layer:

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

Do **not** add `LLAMA_ARG_LOG_VERBOSITY` to the plist — it is silently ignored
and generates a startup warning in the log.

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
