# FUTURES.md — Planned Improvements

Items here are confirmed improvements worth making but not yet scheduled.
When an item is picked up, move it into a `PHASE_N_PLAN.md` and delete it here.

---

## Security — Unauthenticated, Unencrypted Serving Endpoints

**Deferred.** This item is intentionally not yet scheduled into a
`PHASE_N_PLAN.md`. Phases 7–10 (Ollama/serving-tool log management,
service suppression, macmon telemetry, and the related TUI/CLI work,
released together as v2.2.0) have now landed — this is the next
candidate for a `PHASE_N_PLAN.md` once picked up.

### 1. No TLS or authentication in front of any serving-tool daemon

**Problem (quick assessment, not a full design):** Every serving daemon this
project installs — Ollama, Rapid-MLX, mlx-lm, Infinity, Exo, and the
macmon HTTP server (Phase 9) — binds plain HTTP with no authentication.
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
(dogfooding for what became Phase 9) is intentionally bound to `0.0.0.0:9090`
with no auth — a deliberate, temporary development-stage choice, not the
intended production posture. When this item lands, macmon should move to
loopback-only with the rest of the serving tools, fronted by the same
gateway.

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
scope decision table before any code is written.

---

## FileVault — Remote-Disable Automation

### 1. `fdesetup disable` is scriptable over SSH; Precheck only points at the GUI

**Problem:** Precheck (`internal/ops/precheck.go:271-282`) detects
FileVault via `fdesetup status` and blocks with a `[BLOCKER]` pointing at
System Settings → Privacy & Security → FileVault → Turn Off — also the
only fix `docs/known-issues.md` documents. That's a GUI-only instruction,
which is awkward for a genuinely headless box managed over SSH with no
monitor attached.

**What's missing (confirmed via testing/docs review, not assumed):**
`sudo fdesetup disable` is fully scriptable and works over a normal SSH
session — no physical access needed — as long as it's run *before* the
box has already rebooted headless with FileVault on. `sudo fdesetup
authrestart -delayminutes 0` additionally allows one subsequent restart
without landing at the pre-boot EFI password prompt at all.

**The one thing automation genuinely can't fix:** once a box has already
rebooted headless with FileVault on, it's stuck at the pre-boot EFI
password prompt — no `sshd`, no macOS, nothing reachable over the network.
Recovering from that state needs physical presence (keyboard + display, or
a remote-KVM/IPMI-equivalent). Any automation here is about preventing
that state, not escaping it after the fact.

**Direction (not sized yet):**
- Baseline could offer to run `sudo fdesetup disable` directly — with an
  explicit confirmation prompt, since this is a real security-posture
  change on the machine (full-disk encryption off), not a config-file
  toggle — instead of just blocking and pointing at the GUI.
- Needs to account for the "user who enabled FileVault" requirement:
  `fdesetup disable` authenticates against the account that turned
  FileVault on, so a plain `sudo` from a different admin account may not
  be sufficient depending on how it was originally enabled — needs
  verification before relying on it in an automated flow.
- Independent of whether Baseline ever automates the disable itself,
  Precheck's `[BLOCKER]` message and `docs/known-issues.md`'s FileVault
  row should at minimum mention the CLI path (`sudo fdesetup disable`) as
  a same-SSH-session alternative to the GUI.

**Scope:** Not sized. Touches `internal/ops/precheck.go` (blocker
message), `internal/ops/baseline.go` (if automated), `docs/known-issues.md`.

---

## Physical Bootstrap — Clear Onboarding for SIP and RDMA (Recovery Mode)

Both of these genuinely require Recovery Mode — booting with the power
button held, before macOS or `sshd` exists — so no amount of `headless-macs`
automation removes the physical-access step itself. **The exploration here
is about usability**: giving a new operator one clear, correctly-ordered
set of instructions to get through Recovery Mode once, rather than
discovering each requirement one blocker at a time. Not yet scoped into a
`PHASE_N_PLAN.md` — flagging the insight, not committing to a design.

### 1. SIP disable — already documented, worth revisiting for prominence

`docs/known-issues.md` already has a full "Entering Recovery Mode" walkthrough
for `csrutil disable`, and `internal/ops/precheck.go`'s `[BLOCKER]` message
points at it. What's unexplored: whether that's actually the first thing a
new operator sees, or something they only find after already hitting the
blocker mid-setup. Worth considering whether Precheck's very first run (or
a dedicated onboarding doc) should front-load "you'll need Recovery Mode
once, for this" before an operator gets partway through and back-tracks.

### 2. RDMA enable for Exo clusters — net-new, currently undocumented

**What it is:** macOS 26.2+ Tahoe added `rdma_ctl`, giving Thunderbolt 5
Macs (M4 Pro Mac Mini, M4 Max Mac Studio/MacBook Pro, M3 Ultra Mac Studio)
RDMA between directly-cabled machines — Exo can use this to cut inter-node
latency from ~300µs to ~3–9µs for tensor-parallel inference. Confirmed via
exo's own docs and independent benchmarking (Jeff Geerling), not assumed.

**Requirements, all physical or manual:**
- Recovery Mode on **each** node: `rdma_ctl enable`, then restart — same
  physical-access step as SIP, just a different command.
- A direct Thunderbolt 5 cable between every pair of clustered machines
  (this doesn't route through a switch — it's point-to-point). On Mac
  Studio, avoid the TB5 port next to the Ethernet port.
- Every node must run the **exact same macOS version**, including beta
  build numbers if applicable — mismatched versions can fail to discover
  each other over RDMA.
- Building from exo's source, a helper script
  (`tmp/set_rdma_network_config.sh`) disables Thunderbolt Bridge and sets
  DHCP on the RDMA-facing ports; unclear yet whether the Homebrew/pip
  install path needs the equivalent done manually.

**It's optional, not required:** Exo clusters over plain TCP/LAN (including
Wi-Fi) with zero RDMA setup — this is purely a latency optimization for
operators who want it, not a functional prerequisite. `headless-macs`'
Exo support today assumes the no-RDMA path; nothing currently detects
RDMA capability, prompts for it, or documents the setup.

**Scope:** Not sized. If this gets prioritized: Precheck could detect
Thunderbolt 5 hardware and macOS 26.2+ and surface RDMA as an available
option (not a requirement); `docs/tool-comparison.md`'s Exo section and
`docs/known-issues.md` would need a new subsection; unclear whether
`internal/ops/tools.go`'s Exo install path needs any changes at all, since
this is a macOS/exo-level concern once cabled and enabled, not something
`headless-macs` configures directly today.

---

## Community Config/Performance Snapshot — Real Numbers Instead of Estimates

**The problem this solves:** `docs/ram-sizing.md`'s hardware capability and
KV-cache tables are estimates and vendor-quoted figures, not real observed
numbers from actual running nodes — and the Mac hardware/model-naming
mistakes already fixed in this doc (see `CHANGELOG.md`'s `2.2.1` entry)
happened partly *because* the reference material was speculative rather
than sourced from real boxes. A lightweight way for operators to
contribute real, verified numbers back would let this table (and a
running community dataset — model × Mac chip × RAM tier → actual
tokens/sec, actual resident memory, actual TTFT) replace guesswork with
observed reality over time.

**What it is:** A new `headless-macs` capability (subcommand and/or TUI
screen — not sized yet which) that captures a point-in-time snapshot of
the running node and exports it as a well-structured Markdown file, ready
to become a GitHub issue on this repo. Nothing is transmitted
automatically — the file is generated locally, the operator reviews it,
and *they* decide whether and how to share it.

**What to capture:**
- **Hardware**: Mac model identifier (`Mac16,9`-style, already gathered by
  Precheck's `HardwareSnapshot`), chip, total RAM, core counts, form
  factor — all already collected by `internal/ops/precheck.go`, no new
  detection needed.
- **Serving configuration**: which tool(s) are enabled (`internal/ops/status.go`
  already enumerates managed daemons), and — this needs to be a live
  check, not a read of `config.json`, since config states *intent* and
  this needs to confirm *actual, currently-loaded* state — which model is
  genuinely loaded and serving right now. Ollama's `/api/ps` (currently
  loaded models) is the direct source for that; other tools would need
  an equivalent live probe rather than trusting their config file.
- **Resident memory**: `internal/ops/status.go`'s `RunStatus()` already
  gathers RSS per managed daemon PID — directly reusable, no new work.
- **Inference performance (tokens/sec, TTFT)**: needs new work — probing
  a running model with a small canned prompt and measuring the response.
  Ollama's own `/api/generate` response already returns exactly this
  (`eval_count`/`eval_duration` → tokens/sec, `prompt_eval_duration` →
  time-to-first-token) with no extra instrumentation needed — the other
  four tools would each need their own probe/measurement approach, which
  isn't sized here.

**What must never be captured (PII / identifying information):**
IP addresses, hostnames, MAC addresses, usernames, home directory paths
in model file paths, `tools.exo.bootstrap_peers` (other nodes' network
addresses), and anything else that identifies the operator or their
network — this needs an explicit denylist/scrub step reviewed carefully
before this ships, not just "avoid the obvious fields." The exported file
should be safe to paste into a public GitHub issue without a second look,
by design, not by operator diligence.

**Output format:** A single Markdown file (matching this project's
existing docs style — tables, not prose, for the data) containing the
captured fields above, plus instructions at the top of the file itself
for how to turn it into an issue:
1. Manually: copy the file's contents into a new issue at this repo's
   `/issues/new`.
2. Automatically, if the `gh` CLI happens to be on `PATH`: a ready-to-run
   `gh issue create --title "..." --body-file <path>` command line,
   printed for the operator to copy-paste and run themselves — not
   executed by `headless-macs` on the operator's behalf, and not gated on
   `gh` actually being present (most boxes won't have it; the manual path
   must work standalone regardless).

**Deliberately left open, not decided:** `headless-macs` should not grow
its own GitHub API client (issue creation, auth/token handling) just for
this — that's meaningfully more surface, maintenance, and credential
handling than this project takes on anywhere else today. Whether there's
a lighter-weight automation path worth adding later (a documented
`gh`-based one-liner is probably enough) is left as an open question for
whoever picks this up, not resolved here.

**Scope:** Not sized. New capability, not an extension of an existing
command — needs a `PHASE_N_PLAN.md` per the Planning convention before any
code is written, including a decision on where this lives (new `snapshot`
subcommand? TUI screen? both?) and exactly how the probe step measures
tokens/sec and TTFT for the four non-Ollama tools.
