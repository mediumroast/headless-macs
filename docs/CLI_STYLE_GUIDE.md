# Go CLI Style Guide

A design and implementation guide for Go tools that run non-interactively —
headless, over SSH, in cron jobs, or piped into scripts. Companion to
[TUI_STYLE_GUIDE.md](TUI_STYLE_GUIDE.md), which covers the interactive path.
Both guides share the same status-prefix vocabulary so tools that support both
modes produce consistent output regardless of how they are invoked.

---

## Contents

1. [Design Goals](#1-design-goals)
2. [Output Prefixes](#2-output-prefixes)
3. [Exit Codes](#3-exit-codes)
4. [Entry Point Structure](#4-entry-point-structure)
5. [Logging](#5-logging)
6. [Flags and Subcommands](#6-flags-and-subcommands)
7. [Config Loading](#7-config-loading)
8. [CLI Mode Flag](#8-cli-mode-flag)
9. [Stdout vs Stderr](#9-stdout-vs-stderr)
10. [Idempotency Language](#10-idempotency-language)
11. [Checklist](#11-checklist)

---

## 1. Design Goals

- **Scriptable** — every operation is a subcommand; exit codes signal state.
- **Consistent** — same prefix vocabulary as the TUI result screens.
- **Teed** — output goes to stdout *and* a timestamped log file simultaneously.
- **Quiet on skip** — `[SKIP]` rows are present but visually recede; parsers
  can ignore them by filtering for lines that start with `[SET]` or `[FAIL]`.
- **Self-contained** — `--help` and `--version` exit without side effects.

---

## 2. Output Prefixes

All CLI output uses bracket-prefixed plain text. No ANSI color codes in CLI
mode — output must be legible in log files, `grep`, `awk`, and email alerts.

| Prefix        | Meaning                                          |
|---|---|
| `[SET]`       | A change was applied                            |
| `[SKIP]`      | Already correct — nothing done                  |
| `[WARN]`      | Non-fatal issue; operator should investigate    |
| `[FAIL]`      | Fatal failure                                   |
| `[PASS]`      | Health check succeeded (verify only)            |
| `[INFO]`      | Informational — no action required              |
| `[NOTICE]`    | Important but not a warning                     |
| `[CONFIG]`    | Config file loaded or key read                  |
| `[BLOCKER]`   | Hard blocker found (precheck only)              |
| `[SKIP-SIP]`  | Skipped because SIP (or a system gate) is on    |
| `[BACKUP]`    | A file was backed up before modification        |
| `[SNAPSHOT]`  | Pre-change state captured                       |
| `[OK]`        | Post-install endpoint/API confirmed responding  |

### Formatting rules

```
[SET]  <message>
       <detail — 7-space indent to align with prefix width>
```

The prefix column is 7 characters wide including brackets and one trailing
space. Detail lines indent 7 spaces:

```
[WARN] pmset sleep value is 10, expected 0
       Run: sudo pmset -a sleep 0
```

Continuation lines do not repeat the prefix. Use them for fix hints, paths,
or supplementary context — not for multi-sentence prose.

---

## 3. Exit Codes

| Code | Meaning |
|---|---|
| `0` | All checks passed / all changes applied successfully |
| `1` | One or more `[FAIL]` or `[BLOCKER]` results |
| `2` | Warnings only, no failures |

Use these codes consistently across every subcommand. Scripts can detect
degraded-but-functional state with `if [ $? -eq 2 ]`.

```go
if r.Failures > 0 {
    os.Exit(1)
}
if r.Warnings > 0 {
    os.Exit(2)
}
// implicit os.Exit(0)
```

**Never use exit code 1 for flag errors.** Use it only for operational
failures. Flag/usage errors may use exit code 1 by convention, but document
the distinction clearly.

---

## 4. Entry Point Structure

```go
const version = "2.1.1"

const usage = `<appname> — <one-line description>

Usage:
  sudo <appname> [command]

Commands:
  precheck        Read-only audit — no changes
  baseline        Apply system settings
  install-tools   Install/configure serving stack
  verify          Health check — exits 0/1/2
  restore         Undo everything
  update-tools    In-place binary upgrades
  storage         External volume setup

  (no command)    Launch the interactive TUI

Options:
  --help, -h      Show this help and exit
  --version       Show version and exit

Exit codes for verify: 0 = all pass, 1 = failures, 2 = warnings only
Config: ~/.myapp/config.json
Logs:   /var/log/myapp/
`

func main() {
    args := os.Args[1:]

    // Scan all args — --help may appear after a subcommand
    for _, a := range args {
        if a == "--help" || a == "-h" {
            fmt.Print(usage)
            os.Exit(0)
        }
        if a == "--version" {
            fmt.Println("<appname> " + version)
            os.Exit(0)
        }
    }

    if len(args) > 0 {
        switch args[0] {
        case "precheck", "baseline", "install-tools", "verify",
             "restore", "update-tools", "storage":
            runCLI(args[0])
            return
        default:
            fmt.Fprintf(os.Stderr, "unknown command: %s\nRun '<appname> --help' for usage.\n", args[0])
            os.Exit(1)
        }
    }

    runTUI()
}
```

Key points:
- Scan **all** args for `--help`/`--version`, not just `args[0]`. A user
  typing `sudo myapp baseline --help` should see help, not run baseline.
- Unknown subcommands go to stderr with a usage pointer and exit 1.
- No subcommand → TUI. This keeps the binary useful in both contexts.

---

## 5. Logging

All CLI output is teed to a timestamped log file. The log directory is created
with elevated permissions before the tee is set up.

```go
const logDir = "/var/log/myapp"

func initLog(stage string) string {
    if err := os.MkdirAll(logDir, 0755); err != nil {
        fmt.Fprintf(os.Stderr, "[WARN] could not create log dir: %v\n", err)
        return ""
    }
    logFile := filepath.Join(logDir,
        fmt.Sprintf("%s-%s.log", stage, time.Now().Format("20060102-150405")))
    f, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
    if err != nil {
        fmt.Fprintf(os.Stderr, "[WARN] could not open log file: %v\n", err)
        return ""
    }
    // Tee stdout to both the terminal and the log file
    mw := io.MultiWriter(os.Stdout, f)
    // Replace os.Stdout for the logger — or pass mw to a logger instance
    log.SetOutput(mw)
    return logFile
}
```

In practice, the tee is set up at the ops layer rather than in `main`. The ops
package accepts an `io.Writer` and calls `io.MultiWriter(os.Stdout, logFile)`.

Print the log path at the end of every stage:

```
[INFO] Log written to: /var/log/myapp/verify-20260818-143012.log
```

---

## 6. Flags and Subcommands

Use subcommands, not flags, for distinct operations. `--help` and `--version`
are the only global flags.

Do not use `flag.Parse()` for subcommands — the standard library `flag`
package does not handle subcommand dispatch cleanly. Parse `os.Args[1:]`
directly for the simple switch-on-first-arg pattern shown above.

If a subcommand needs its own options, parse `args[1:]` inside `runCLI`:

```go
func runCLI(cmd string, args []string) {
    // args is os.Args[2:]
    switch cmd {
    case "verify":
        verbose := false
        for _, a := range args {
            if a == "--verbose" { verbose = true }
        }
        runVerify(verbose)
    }
}
```

Keep subcommand flags minimal. If a subcommand accumulates more than two or
three flags, reconsider whether those options belong in the config file instead.

---

## 7. Config Loading

```go
func loadConfig(cmd string) (*config.Config, error) {
    cfgPath := config.UserConfigPath()
    if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
        // restore doesn't need a config
        if cmd == "restore" {
            return &config.Config{}, nil
        }
        templatePath := findTemplate()
        if templatePath == "" {
            return nil, fmt.Errorf(
                "config not found and no template available — " +
                "run from repo directory or run the TUI first")
        }
        if err := config.Bootstrap(templatePath); err != nil {
            return nil, fmt.Errorf("could not create config: %w", err)
        }
        fmt.Printf("[INFO] Created config: %s\n", cfgPath)
    }
    return config.Load()
}
```

Rules:
- Never exit on missing config without trying to bootstrap from a template.
- `restore` is the one subcommand allowed to proceed with an empty config.
- Print `[INFO]` (not an error) when config is created on first run.
- Errors from `loadConfig` go to stderr and exit 1.

---

## 8. CLI Mode Flag

The logging package must suppress TUI-specific behavior when running headless.
A package-level bool guards this:

```go
// internal/log/logger.go
var CLIMode bool

func Init(scriptName string) (string, error)    { return InitMode(scriptName, false) }
func InitTUI(scriptName string) (string, error) { return InitMode(scriptName, true) }

func InitMode(scriptName string, tuiMode bool) (string, error) {
    if CLIMode {
        tuiMode = false  // CLI always wins, even if caller passes true
    }
    var out io.Writer
    if tuiMode {
        out = logFileOnly  // TUI: suppress stdout, log to file
    } else {
        out = io.MultiWriter(os.Stdout, logFile)  // CLI: tee
    }
    // ...
}
```

Set `CLIMode = true` at the top of `runCLI()`, before any ops calls:

```go
func runCLI(cmd string) {
    checkPlatform()
    ilog.CLIMode = true   // must be set before any ops.Run* call
    cfg, err := loadConfig(cmd)
    // ...
}
```

This allows ops packages to call `ilog.Init` / `ilog.InitTUI` without knowing
whether they are being driven by the TUI or the CLI.

---

## 9. Stdout vs Stderr

| Content | Stream |
|---|---|
| `[SET]`, `[SKIP]`, `[WARN]`, `[PASS]`, `[FAIL]`, `[INFO]` prefixed lines | stdout |
| `[BLOCKER]` prefixed lines | stdout (still tee'd to log) |
| Fatal errors that prevent the stage from running | stderr |
| Usage errors | stderr |
| Startup banners / log-path announcement | stdout |

The rule: anything that represents the result of the stage goes to stdout.
Anything that prevents the stage from starting (bad args, missing binary,
missing config) goes to stderr. This lets operators pipe stdout to monitoring
and stderr to alerting independently.

```go
// Stage result — stdout
fmt.Printf("[FAIL] %s\n", msg)

// Pre-stage fatal — stderr
fmt.Fprintf(os.Stderr, "ERROR: could not load config: %v\n", err)
os.Exit(1)
```

---

## 10. Idempotency Language

CLI output must communicate idempotency clearly. A re-run of any stage should
produce `[SKIP]` lines for everything already in the desired state and `[SET]`
only for things that actually changed.

Idempotency language patterns:

```
[SKIP] pmset sleep already set to 0
[SET]  pmset sleep → 0 (was: 10)
[SKIP] LaunchDaemon com.myapp.server already installed
[SET]  LaunchDaemon com.myapp.server installed
```

Use "already" in `[SKIP]` messages. Use present-tense action verbs in `[SET]`
messages. When a before/after value is available, show it: `→ <new> (was: <old>)`.

---

## 11. Checklist

### New CLI subcommand

- [ ] Added to the `switch` in `main()` and to the `usage` string
- [ ] Returns an ops result struct with `Failures`, `Warnings` int fields
- [ ] Exit code: `1` if `Failures > 0`, `2` if `Warnings > 0 && Failures == 0`
- [ ] `ilog.CLIMode = true` set before ops call
- [ ] Config loaded via `loadConfig(cmd)` — not `config.Load()` directly
- [ ] Teed log path printed at end of stage as `[INFO] Log written to: <path>`
- [ ] All output goes to stdout via prefix convention; pre-stage errors to stderr

### New project using this guide

- [ ] `--help` and `--version` scan all args, not just `args[0]`
- [ ] Subcommand dispatch via `switch args[0]` — no `flag.Parse()`
- [ ] Logging package has a `CLIMode` bool that suppresses TUI-only behavior
- [ ] Config bootstraps from a template if missing (except restore)
- [ ] Exit codes `0`/`1`/`2` used consistently across all subcommands
- [ ] No ANSI escape codes in CLI output — prefix-only plain text
- [ ] Output teed to `/var/log/<appname>/<stage>-<timestamp>.log`
- [ ] Unknown subcommand → stderr + exit 1 (not a panic or stack trace)
