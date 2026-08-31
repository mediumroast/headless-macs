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
Add `OLLAMA_LOG_LEVEL=warn` to the Ollama LaunchDaemon plist's
`EnvironmentVariables`. This cuts log volume to actual warnings and errors only.
Wire it into the ops layer so it is set by default and overridable via config:

```json
"tools": {
  "ollama": {
    "log_level": "warn"
  }
}
```

**Part B — Add newsyslog rotation.**
Create `/etc/newsyslog.d/ollama.conf` during `install-tools` to cap log size
and keep a bounded number of rotated copies:

```
/var/log/ollama/stderr.log  root:wheel  644  5  102400  *  JG
/var/log/ollama/stdout.log  root:wheel  644  3  10240   *  JG
```

Columns: path · owner:group · mode · copies to keep · rotate at KB ·
schedule (`*` = daily) · flags (`J`=bzip2 compress, `G`=send signal on
rotation). At 100 MB per file with 5 copies, maximum stderr storage is ~500 MB.

**Scope:** `internal/ops/tools.go` (plist generation + newsyslog file write)
+ `config.json` (optional `log_level` key under `tools.ollama`).

---

## Notes (Ollama)

- Items 1 and 2 are related and should be implemented together in the same
  phase — both touch the Ollama plist generation in the ops layer.
- newsyslog config should be added to `restore.sh` / the restore ops so it
  is cleaned up on restore.
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
