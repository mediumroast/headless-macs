# RELEASE_STRATEGY.md — Branching, Versioning, and Release Process

This document is the single source of truth for how work moves from a
feature branch to a tagged release in `headless-macs`. It exists because
the project has already lived through one major rewrite (Phase 6, the Go
TUI) on a long-lived `V2` branch, and needs a stated convention before a
`V3` — or a second concurrent major — makes the ad hoc approach ambiguous.

---

## Summary

| Concept | Convention |
|---|---|
| Trunk | `main` — always the current major version, always releasable |
| Versioning | [Semantic Versioning](https://semver.org/spec/v2.0.0.html) (`MAJOR.MINOR.PATCH`) |
| Day-to-day work | Short-lived feature/fix branches off `main`, merged via PR |
| Major rewrites | A long-lived integration branch (`V<N>`), merged to `main` **once**, then retired |
| History of old majors | Git **tags**, not open branches — branches are for active work only |
| Maintenance of an old major | A `maintenance/v<N>.x` branch, created **only** if a backport is actually needed, not preemptively |
| Release marker | An annotated tag on `main` matching the `CHANGELOG.md` version exactly |

---

## Branch types

### `main`

`main` is the trunk. It is always the current major version and is always
in a releasable state. Two kinds of change land on `main`:

1. **Normal day-to-day work** — bug fixes, small features, doc corrections —
   merged directly via short-lived branches (see below).
2. **The final merge of a major-version integration branch** — a single PR
   that brings an entire `V<N>` branch's history in at once, timed to a
   version bump.

Once a major version has merged to `main`, `main` **is** that major
version. Ongoing `2.x` work (new minor/patch releases) happens directly on
`main`, not on a lingering `V2` branch — see [Why not keep the version
branch alive forever?](#why-not-keep-the-version-branch-alive-forever)
below.

### Feature/fix branches

For anything that isn't a major rewrite: branch off `main`, do the work,
PR back into `main`. This project's existing branch-naming convention
already covers this (`claude/<adjective>-<surname>-<hash>` for
Claude-Code-originated branches; `feature/...`, `fix/...` for
human-originated ones). These branches are short-lived — days, not months —
and are deleted after merge.

This covers essentially everything in `FUTURES.md` and each
`PHASE_N_PLAN.md`: Phase 7, 8, 9, and 10 (log management, service
suppression, macmon, TUI/CLI updates) are all **minor** work against the
current major (`v2.x`) and belong on short-lived branches off `main`, not
on a new version branch.

### Major-version integration branches (`V<N>`)

Reserved for changes large enough that stabilizing them on `main`
incrementally would leave `main` broken or half-migrated for an extended
period — a rewrite of the execution model, a breaking `config.json` schema
change, dropping support for something previously supported. Phase 6 (the
shell-to-Go rewrite) is the precedent: it lived on `V2` with feature
branches nested under it (`feature/V2/ollama-refinements` is exactly this
pattern) until it was ready to become `v2.0.0`.

Rules for a `V<N>` branch:

- Branched from `main` at the point the major-version work begins.
- Feature branches for sub-pieces of the rewrite nest under it
  (`feature/V<N>/<topic>`), merged into `V<N>` via PR — same review bar as
  anything merging to `main`.
- `V<N>` is periodically brought up to date with `main` (merge `main` into
  `V<N>`, not the other way) so it doesn't drift so far that the final
  merge is unreviewable. Do this whenever `main` gets a change `V<N>`
  needs — don't let more than a handful of unrelated `main` commits pile
  up before syncing.
- When ready, `V<N>` merges into `main` in one PR, and that merge is
  exactly what triggers the next major version bump (`v<N>.0.0`).
- **After that merge, `V<N>` is retired** — deleted or left frozen and
  unmerged-from-again. It is not where `v<N>.1.0` happens; `main` is.

### Maintenance branches (`maintenance/v<N>.x`)

Created only when there is an actual need to patch an old major after
`main` has moved past it — e.g., a security fix needs to reach someone
still running `v1.x` after `v2.0.0` has shipped. Branch from the old
major's release tag (`git checkout -b maintenance/v1.x v1.4.2`), cherry-pick
the fix, tag a new patch release (`v1.4.3`) from that branch.

Do **not** create a maintenance branch preemptively "just in case" — for a
project with effectively one deployment target (an operator's own Mac
fleet, upgraded in place), this will usually never be needed. If it turns
out nobody ever needs `v1.x` patched after `v2.0.0` ships, no maintenance
branch is ever created, and `v1.x`'s history lives entirely in its tags.

---

## Tags

Every release gets an annotated tag on `main`, named exactly after the
`CHANGELOG.md` version it corresponds to (`v2.1.1`, not `2.1.1` or
`release-2.1.1`). This already matches the End-of-session checklist in
`CLAUDE.md`:

```bash
git checkout main && git pull
git tag -a v2.2.0 -m "Phases 7-10: log management, service suppression, macmon, TUI/CLI restructure"
git push origin v2.2.0
gh release create v2.2.0 \
  --title "v2.2.0 — Phases 7-10: log management, service suppression, macmon, TUI/CLI restructure" \
  --notes-file <(sed -n '/## \[2\.2\.0\]/,/## \[2\.1\.1\]/p' CHANGELOG.md | head -n -1)
```

Tags, not branches, are how old versions are preserved for history. A tag
is free to leave in place forever; a branch accumulates drift and invites
someone to accidentally build on it. If a `V<N>` integration branch or a
`maintenance/v<N>.x` branch is deleted after it has served its purpose, the
tags it produced remain the permanent record.

---

## Versioning rules

Unchanged from `CLAUDE.md`'s existing version-bump rules — restated here
for completeness since this document is the canonical place for release
process:

| Bump | Trigger |
|---|---|
| **Patch** (`x.y.Z`) | Bug fixes, doc corrections, typo fixes |
| **Minor** (`x.Y.0`) | New features, new scripts, new LaunchDaemons, new config keys — backward-compatible |
| **Major** (`X.0.0`) | Breaking `config.json` schema changes, script/binary interface renames, removal of supported tools |

A `V<N>` integration branch merge is *always* a major bump by definition —
that's the criterion for using one in the first place (see [Major-version
integration branches](#major-version-integration-branches-vn) above). If a
change turns out not to be breaking, it doesn't need a `V<N>` branch; it's
a minor release on `main` directly.

---

## Worked example: Phases 7–10 vs. a hypothetical Phase 11 breaking change

To make the abstract rules concrete against this project's actual
near-term roadmap:

- **Phases 7, 8, 9, 10** (`docs/planning/PHASE_7_PLAN.md` through
  `PHASE_10_PLAN.md`): none of these break `config.json` compatibility or
  change the CLI's interface shape in a way that breaks existing scripts —
  they add new optional config keys (`tools.macmon`, `tools.ollama.log_level`),
  new daemons, and a new `status` subcommand. Each is individually
  **minor**-release-sized work, and the original version of this section
  planned them as four separate `main` releases in sequence — `v2.2.0`
  (Phase 7), `v2.3.0` (Phase 8), `v2.4.0` (Phase 9), `v2.5.0` (Phase 10).
  **That's not what happened in practice:** all four landed together on
  one long-lived branch and were never individually merged to `main`
  along the way. Since none of them shipped before the others, they
  became a single combined minor release, `v2.2.0`, covering all four
  phases at once — still correctly a **minor** bump (nothing about
  bundling them changes whether the change is breaking), just one
  release instead of four. No `V3` branch was needed for any of them.
  The lesson for next time: if a phase is going to sit unmerged for a
  while, tagging it as its own minor release before starting the next
  phase keeps this worked example's original (better) pattern true —
  bundling like this works, but it's the fallback, not the goal.
- **A hypothetical future breaking change** — e.g., Item 12's deferred
  TLS/auth work, if it ends up requiring `install-tools.sh`'s /
  `install-tools`'s `localhost_only` semantics to change incompatibly (forcing
  every tool to loopback and introducing a required gateway) — is exactly
  the kind of change that would justify branching `V3` from `main`, doing
  the work there (potentially across several feature branches nested under
  it), and merging once as `v3.0.0`.

---

## Why not keep the version branch alive forever?

This is worth stating explicitly since it's the one place this strategy
overrides what might feel like the natural instinct (keep `V2` around
since it's "where v2 work happens").

Two branches both receiving ongoing development (`main` for hotfixes,
`V2` for new v2.x features) means every fix on one has to be manually
ported to the other, and the final "catch up" merge gets harder the longer
they run in parallel — this is the exact failure mode long-lived
integration branches are meant to be used sparingly to avoid. Once `V2`
becomes `v2.0.0` on `main`, `main` already contains everything `V2` had;
there is no more information in `V2` that isn't in `main`, so there is
nothing left to gain by keeping it as a second place to commit to. All
future `v2.x` work is just... work on `main`. The branch's only remaining
value is historical, which a tag already provides at zero ongoing cost.
