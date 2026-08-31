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

## Notes

- Items 1 and 2 are related and should be implemented together in the same
  phase — both touch the Ollama plist generation in the ops layer.
- newsyslog config should be added to `restore.sh` / the restore ops so it
  is cleaned up on restore.
- The `OLLAMA_MODELS` resolved-path logic applies only when
  `storage.use_external_volume` is `true`. When using internal storage the
  default Ollama path should be left unchanged.
