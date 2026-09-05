# PHASE 8 PLAN — Unnecessary Service Suppression

## Intent

Suppress macOS background services that have no purpose on a headless LLM
inference node and add two precheck-only warnings for conditions
`headless-macs` shouldn't act on directly. Consolidates FUTURES.md Items
3–9, which are all extensions of the existing System Baseline service
suppression section (Spotlight, iCloud, Siri, etc. are already suppressed
there — this phase adds five more).

---

## Scope

| Item | In | Out |
|---|---|---|
| Suppress `AssetCache`/`AssetCacheLocatorService`/`AssetCacheTetheratorService` | ✓ | |
| Suppress `mobileassetd` | ✓ | |
| Suppress `coreaudiod`/`audiomxd` | ✓ | |
| Suppress `findmybeaconingd` | ✓ | |
| Suppress `AirPlayXPCHelper` | ✓ | |
| `verify.go` checks confirming each is not running | ✓ | |
| `restore.go` re-enabling all five on Restore | ✓ | |
| Precheck `[WARN]` when Docker's `com.docker.vmnetd` is detected | ✓ | |
| Precheck `[WARN]` when Rapid-MLX and Ollama are both enabled | ✓ | |
| Docs: Rapid-MLX always-resident memory model note | ✓ | |
| Uninstalling Docker or any other third-party software | | ✗ — not this project's job |
| Automatic Ollama `MAX_LOADED_MODELS` adjustment for Rapid-MLX's memory footprint | | ✗ — documentation only for this phase; flagged as a possible future tuning enhancement, not required here |

---

## Resolved design decisions

| Question | Decision |
|---|---|
| Suppression pattern | Same SIP-gating pattern already used in `sectionServices()`: `disableService()` is skipped with `[SKIP-SIP]` when SIP is on; `bootout` is attempted unconditionally regardless of SIP state |
| Where these five services live | Same section as existing suppressions (`sectionServices()` in `baseline.go`) — no new section, just more entries |
| Docker detection | Detection only (file existence check on `/Library/LaunchDaemons/com.docker.vmnetd.plist`), no automated action |
| Rapid-MLX/Ollama warning | Precheck-time only (`checkPrerequisites()` or a new small check) — this is operator guidance, not a blocker |

---

## Phases

### Phase 8A — Service suppression additions
**Goal:** Five more services suppressed via System Baseline, matching the
existing pattern exactly.

- [ ] In `sectionServices()` (`internal/ops/baseline.go`), add:
  - `com.apple.AssetCache.builtin` (+ bootout of
    `/System/Library/LaunchDaemons/com.apple.AssetCache.builtin.plist`)
  - `com.apple.MobileAssetUpdater` (+ bootout of its plist)
  - `com.apple.audio.coreaudiod`
  - `com.apple.audiomxd`
  - `com.apple.findmybeaconingd` (+ bootout of its plist)
  - `com.apple.AirPlayXPCHelper`
- [ ] Each uses the existing `disableService(section, domain, sipEnabled)`
      helper — no new helper needed
- [ ] Confirm none of these are dependencies of something inference-relevant
      before merging (e.g., `coreaudiod` should have zero effect on serving
      tools — sanity-check on doppio-1 after suppression that Ollama/etc.
      still serve requests normally)

**Files touched:** `internal/ops/baseline.go`

---

### Phase 8B — Verify checks
**Goal:** `verify.go` confirms each suppressed service is actually down.

- [ ] Add a `[PASS]`/`[WARN]` check per service in the relevant `sectionSystem()`
      (or a new `sectionServices()` in verify, mirroring baseline's naming)
      confirming the service is not running via `launchctl print`
- [ ] `[WARN]`, not `[FAIL]`, when SIP prevents persistence — matches the
      `[SKIP-SIP]` semantics from Baseline

**Files touched:** `internal/ops/verify.go`

---

### Phase 8C — Precheck warnings
**Goal:** Flag two conditions this project should warn about but not act on.

- [ ] Detect `/Library/LaunchDaemons/com.docker.vmnetd.plist`; if present,
      `[WARN]`: `"Docker vmnetd detected — remove Docker if not required on
      this inference node"`
- [ ] When `tools.rapid_mlx.enabled` and `tools.ollama.enabled` are both
      `true`, `[WARN]`: Rapid-MLX holds its model resident in unified memory
      continuously (confirmed ~20–25 GB on doppio-1 with
      qwen3-aftertaste-fused); operator should account for this when tuning
      Ollama's `MAX_LOADED_MODELS`

**Files touched:** `internal/ops/precheck.go`

---

### Phase 8D — Documentation
**Goal:** Make Rapid-MLX's always-resident memory model discoverable outside
the precheck warning.

- [ ] Add a note to `docs/tool-comparison.md` under Rapid-MLX's entry
      explaining the always-resident memory model and its interaction with
      Ollama's `MAX_LOADED_MODELS`
- [ ] Add the same note to `README.md` wherever Rapid-MLX is introduced

**Files touched:** `docs/tool-comparison.md`, `README.md`

---

### Phase 8E — Restore
**Goal:** `restore` re-enables everything this phase suppresses.

- [ ] In `sectionRestoreServices()` (`internal/ops/restore.go`), re-enable
      the same five service domains suppressed in Phase 8A (mirrors how the
      existing suppressed services — Spotlight, iCloud, Siri, etc. — are
      already restored)

**Files touched:** `internal/ops/restore.go`

---

### Phase 8F — Remediation for existing installs
**Goal:** confirm an existing box gets these five suppressions without any
special migration step — and identify the one real cross-version risk.

Unlike Phase 7's `logrotate` daemon, none of this phase's changes write a
plist of ours that could go stale. `disableService()` calls
`launchctl disable` + `launchctl bootout` directly against **Apple's own**
system plists (`com.apple.AssetCache.builtin`, `com.apple.MobileAssetUpdater`,
etc.) — both operations are naturally idempotent: disabling an
already-disabled service, or booting out an already-gone daemon, are safe
no-ops either way. There is nothing here that "exists so it gets skipped
forever," because there's no artifact of ours to check for existence in
the first place.

- [ ] **Remediation path: re-run `sudo headless-macs baseline`.** No code
      change needed for this phase's own suppressions to reach an existing
      box — confirm this is explicitly documented wherever Phase 8 gets
      written up (README/CHANGELOG), so it's not assumed operators already
      know System Baseline is safe/expected to re-run.
- [ ] Precheck's two new warnings (Docker vmnetd, Rapid-MLX+Ollama memory)
      are evaluated fresh on every Precheck run — nothing is stored, so
      there's no "stale check" risk; upgrading the binary alone is
      sufficient for these two, no re-run of anything else required.
- [ ] **The one real risk: `sectionRestoreServices()`'s re-enable list and
      `sectionServices()`'s suppression list must ship as a single unit.**
      If an operator runs `Baseline` under this phase's new binary (five
      services suppressed) but later runs `Restore` under an *older* binary
      that doesn't know about those five domains, Restore will not
      re-enable them, leaving the box in a state neither version's Verify
      fully recognizes. This is a general risk for any phase that pairs a
      new suppression with its undo, not unique to Phase 8 — but worth
      stating here since it's the first phase since Phase 6 to extend that
      pairing. Mitigation is procedural, not code: never let Baseline's
      suppression list and Restore's re-enable list diverge across a
      release boundary — land and tag them together, per
      `docs/RELEASE_STRATEGY.md`'s versioning rules.

**Files touched:** none beyond 8A–8E — this section is a design
confirmation, not new implementation work.

---

## Files-touched summary

| File | Change |
|---|---|
| `internal/ops/baseline.go` | Five new service suppressions in `sectionServices()` |
| `internal/ops/verify.go` | Checks confirming each suppressed service is not running |
| `internal/ops/precheck.go` | Docker vmnetd warning, Rapid-MLX+Ollama concurrent-memory warning |
| `internal/ops/restore.go` | Re-enable the five services in `sectionRestoreServices()` |
| `docs/tool-comparison.md` | Rapid-MLX always-resident memory note |
| `README.md` | Same note, in the Rapid-MLX section; remediation instruction for existing installs |

---

## Precheck/Verify review (no gap found)

Checked explicitly, per the same review applied to Phase 7: this phase's
existing Verify coverage (8B — one `[PASS]`/`[WARN]` per suppressed
service) is complete against its own scope, nothing missing. Precheck
deliberately gets no new check for the five suppressed services
themselves — Precheck doesn't preview any of Baseline's *existing*
suppressions either (Spotlight, iCloud, Siri, etc., from Phase 6), so
adding a preview for only the five new ones would be inconsistent with
how Precheck already treats this category of change. Precheck's two new
items (Docker vmnetd, Rapid-MLX+Ollama memory) are correctly placed
already — both are informational/advisory, not previews of an action
Baseline will take, which is exactly what Precheck is for.

---

## Open questions

- None outstanding on the original scope — FUTURES.md's own notes on these
  items ("Items 3–7 belong in the same phase," "Item 8 is precheck-only,"
  "Item 9 is warn + doc note") already resolve the shape of this phase.
- **(Phase 8F)** Is a procedural release-process note enough to prevent
  Baseline/Restore suppression-list drift, or does this project want an
  automated check (e.g., a test asserting every domain in
  `sectionServices()`'s suppression list has a matching entry in
  `sectionRestoreServices()`'s re-enable list)? Leaning toward adding that
  test cheaply whenever Phase 8 is implemented, rather than relying on
  process alone.
