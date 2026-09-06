# PHASE 8 PLAN — Unnecessary Service Suppression

**Status: 8A–8F all implemented, build/vet/test clean.** One item remains
that can't be done from here: sanity-checking on real hardware (doppio-1)
that none of the five suppressed services were secretly load-bearing for
serving-tool behavior. Implementation deviated from the plan's literal
wording in one place — see Phase 8A — by using a single shared
`phase8Suppressions` list across suppress/restore/verify instead of three
independently hardcoded copies, which resolves Phase 8F's open question
about list drift more strongly than the test it proposed.

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

- [x] In `sectionServices()` (`internal/ops/baseline.go`), added:
  - `com.apple.AssetCache.builtin` (+ bootout of
    `/System/Library/LaunchDaemons/com.apple.AssetCache.builtin.plist`)
  - `com.apple.MobileAssetUpdater` (+ bootout of its plist)
  - `com.apple.audio.coreaudiod`
  - `com.apple.audiomxd`
  - `com.apple.findmybeaconingd` (+ bootout of its plist)
  - `com.apple.AirPlayXPCHelper`
- [x] Each uses the existing `disableService(section, domain, sipEnabled)`
      helper — no new helper needed
- [x] **Design change from the original plan wording:** rather than
      hardcoding this list separately in `baseline.go`, `restore.go`, and
      `verify.go` (which is exactly the drift risk Phase 8F's open
      question worried about and considered solving with a test), the
      five suppressions live in one shared `phase8Suppressions` var
      (`baseline.go`) — a `{Label, Plist}` pair per service, with the
      rationale as an inline comment per entry. `sectionServices()`,
      `sectionRestoreServices()` (restore.go), and the new
      `checkServiceSuppressed` loop (verify.go) all range over the same
      slice. This makes drift structurally impossible rather than
      detected after the fact — stronger than the test Phase 8F proposed,
      so no separate test was added; see Phase 8F below.
- [ ] Confirm none of these are dependencies of something inference-relevant
      — **not done**, needs a real box (doppio-1): sanity-check after
      suppression that Ollama/etc. still serve requests normally.

**Files touched:** `internal/ops/baseline.go`

---

### Phase 8B — Verify checks
**Goal:** `verify.go` confirms each suppressed service is actually down.

- [x] Added a `[PASS]`/`[WARN]` check per service in `sectionSystem()`
      (new `checkServiceSuppressed(section, label)` helper, "not running"
      is the pass condition — including "no such service found at all")
      via `launchctl print`, iterating the shared `phase8Suppressions` list
- [x] `[WARN]`, not `[FAIL]`, matching the `[SKIP-SIP]` semantics from
      Baseline

**Files touched:** `internal/ops/verify.go`

---

### Phase 8C — Precheck warnings
**Goal:** Flag two conditions this project should warn about but not act on.

- [x] New `checkAdvisories(cfg)` section (`ADVISORY`): detects
      `/Library/LaunchDaemons/com.docker.vmnetd.plist`; if present,
      `[WARN]`: `"Docker vmnetd detected — remove Docker if not required on
      this inference node"`
- [x] Same function: when `tools.rapid_mlx.enabled` and `tools.ollama.enabled`
      are both `true`, `[WARN]`: Rapid-MLX holds its model resident in
      unified memory continuously; operator should account for this when
      tuning Ollama's `MAX_LOADED_MODELS`

**Files touched:** `internal/ops/precheck.go`

---

### Phase 8D — Documentation
**Goal:** Make Rapid-MLX's always-resident memory model discoverable outside
the precheck warning.

- [x] Added a note to `docs/tool-comparison.md` under Rapid-MLX's
      Weaknesses, explaining the always-resident memory model and its
      interaction with Ollama's `MAX_LOADED_MODELS`
- [x] Added a matching blockquote note to `README.md`'s Tool Selection
      section, next to the existing Network Defaults note

**Files touched:** `docs/tool-comparison.md`, `README.md`

---

### Phase 8E — Restore
**Goal:** `restore` re-enables everything this phase suppresses.

- [x] In `sectionRestoreServices()` (`internal/ops/restore.go`), re-enable
      the same five service domains suppressed in Phase 8A — added as an
      explicit floor (iterating the shared `phase8Suppressions` list)
      running before the existing generic snapshot-based restore, not as a
      replacement for it. Reason: the snapshot only captures the
      disable-override state at whatever moment the *most recent* Baseline
      run started; if an earlier Baseline run already suppressed these
      before that snapshot was taken, the snapshot would show them as
      already-disabled, and the generic snapshot restore would (correctly
      by its own logic, but not what's wanted here) leave them alone.
      Re-enabling an already-enabled service is a harmless no-op, so the
      explicit floor and the generic restore can't conflict.

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

- [x] **Remediation path: re-run `sudo headless-macs baseline`.** No
      migration code needed — documented in CHANGELOG.md.
- [x] Confirmed Precheck's two new warnings need no remediation
      consideration — evaluated fresh on every run, nothing stored.
- [x] **The cross-version drift risk is now structurally closed, not just
      documented.** The plan's original concern — Baseline's suppression
      list and Restore's re-enable list shipping as two independently
      hardcoded lists that could drift apart across a release — is
      resolved by Phase 8A's `phase8Suppressions` refactor: there is only
      one list, used by both sides (and Verify), so there is nothing left
      to drift. This supersedes the "add a test asserting the two lists
      match" idea from the Open Questions below — a single shared list is
      stronger than a test that checks two copies agree, since it removes
      the second copy entirely.

**Files touched:** covered by 8A's `phase8Suppressions` refactor — no
separate remediation code was needed once that existed.

---

## Files-touched summary

| File | Change |
|---|---|
| `internal/ops/baseline.go` | Five new service suppressions via shared `phase8Suppressions` list in `sectionServices()` |
| `internal/ops/verify.go` | `checkServiceSuppressed()` + `[PASS]`/`[WARN]` per service, iterating `phase8Suppressions` |
| `internal/ops/precheck.go` | New `checkAdvisories()`: Docker vmnetd warning, Rapid-MLX+Ollama concurrent-memory warning |
| `internal/ops/restore.go` | Explicit re-enable floor iterating `phase8Suppressions`, ahead of the existing generic snapshot restore |
| `docs/tool-comparison.md` | Rapid-MLX always-resident memory note |
| `README.md` | Same note as a blockquote in Tool Selection |
| `CHANGELOG.md` | Remediation instruction for existing installs (re-run Baseline) |

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
- **(Phase 8F) Resolved during implementation:** the shared
  `phase8Suppressions` list closes this structurally — see Phase 8A and
  8F above. No test needed; there's nothing left that could drift.
