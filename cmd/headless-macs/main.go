package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/mediumroast/headless-macs/internal/config"
	ilog "github.com/mediumroast/headless-macs/internal/log"
	"github.com/mediumroast/headless-macs/internal/ops"
	"github.com/mediumroast/headless-macs/internal/tui"
)

// version is a var, not a const, so the Makefile can override it at build
// time via -ldflags -X (which only works on package-level string vars).
// "dev" is the fallback for anyone running `go build` directly instead of
// `make build`. See docs/planning/PHASE_10_PLAN.md — Phase 10-Version.
var version = "dev"

const usage = `headless-macs — Apple Silicon LLM inference node manager

Usage:
  sudo headless-macs [command]

Commands (run non-interactively, output to stdout + log):
  precheck        Read-only audit — hardware, power, network, SIP
  baseline        Apply system baseline (pmset, sysctl, SSH, daemons)
  install-tools   Install/configure serving stack (Ollama, mlx-lm, etc.)
  verify          Health check — prints [PASS]/[FAIL]/[WARN] and exits 0/1/2
  restore         Undo everything baseline and install-tools applied
  update-tools    In-place binary upgrade for all enabled serving tools
  storage         Configure external model storage volume
  status          What's running and what it's costing you (add --watch to refresh)

  (no command)    Launch the interactive TUI

Options:
  --help, -h      Show this help and exit
  --version       Show version and exit

Exit codes for verify: 0 = all pass, 1 = failures present, 2 = warnings only
Config: ~/.headless_macs/config.json
Logs:   /var/log/mac-llm-setup/
`

func main() {
	ops.Version = version
	tui.Version = version

	args := os.Args[1:]
	for _, a := range args {
		if a == "--help" || a == "-h" {
			fmt.Print(usage)
			os.Exit(0)
		}
		if a == "--version" {
			fmt.Println("headless-macs " + version)
			os.Exit(0)
		}
	}
	if len(args) > 0 {
		switch args[0] {
		case "precheck", "baseline", "install-tools", "verify", "restore", "update-tools", "storage", "status":
			runCLI(args[0], args[1:])
			return
		default:
			fmt.Fprintf(os.Stderr, "unknown command: %s\nRun 'headless-macs --help' for usage.\n", args[0])
			os.Exit(1)
		}
	}

	runTUI()
}

// ---------------------------------------------------------------------------
// TUI mode
// ---------------------------------------------------------------------------

func runTUI() {
	checkPlatform()

	templatePath := findTemplate()

	cfgPath := config.UserConfigPath()
	firstRun := false
	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		if templatePath == "" {
			fmt.Fprintln(os.Stderr, "ERROR: config.json template not found. Run from the headless-macs repo directory.")
			os.Exit(1)
		}
		if err := config.Bootstrap(templatePath); err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: could not create config: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Created config: %s\n", cfgPath)
		firstRun = true
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: could not load config: %v\n", err)
		os.Exit(1)
	}

	app := tui.NewApp(cfg, firstRun)
	p := tea.NewProgram(app, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		os.Exit(1)
	}
}

// ---------------------------------------------------------------------------
// CLI (headless) mode
// ---------------------------------------------------------------------------

func runCLI(cmd string, rest []string) {
	checkPlatform()
	ilog.CLIMode = true
	printVersionNudge()

	cfg, err := loadConfig(cmd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		os.Exit(1)
	}

	switch cmd {
	case "status":
		watch := false
		for _, a := range rest {
			if a == "--watch" {
				watch = true
			}
		}
		runStatusCLI(cfg, watch)
		return

	case "precheck":
		r, err := ops.RunPrecheck(cfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
			os.Exit(1)
		}
		r.PrintText()
		if r.Readiness.Blockers > 0 {
			os.Exit(1)
		}
		if r.Readiness.Warnings > 0 {
			os.Exit(2)
		}

	case "baseline":
		r, err := ops.RunBaseline(cfg, ops.BaselineOptions{})
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
			os.Exit(1)
		}
		if r.Failures > 0 {
			os.Exit(1)
		}

	case "install-tools":
		r, err := ops.RunTools(cfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
			os.Exit(1)
		}
		if r.Failures > 0 {
			os.Exit(1)
		}

	case "verify":
		r, err := ops.RunVerify(cfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
			os.Exit(1)
		}
		if r.Failures > 0 {
			os.Exit(1)
		}
		if r.Warnings > 0 {
			os.Exit(2)
		}

	case "restore":
		r, err := ops.RunRestore()
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
			os.Exit(1)
		}
		if r.Failures > 0 {
			os.Exit(1)
		}

	case "update-tools":
		r, err := ops.RunUpdateTools(cfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
			os.Exit(1)
		}
		if r.Failures > 0 {
			os.Exit(1)
		}
		if r.Warnings > 0 {
			os.Exit(2)
		}

	case "storage":
		r, err := ops.RunStorage(cfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
			os.Exit(1)
		}
		if r.Failures > 0 {
			os.Exit(1)
		}
	}
}

// ---------------------------------------------------------------------------
// status subcommand
// ---------------------------------------------------------------------------

// runStatusCLI prints daemon state + resource use, optionally refreshing
// in place. Exit code is always 0 — status is informational, not a health
// gate (verify already owns pass/fail semantics).
func runStatusCLI(cfg *config.Config, watch bool) {
	interval := 2 * time.Second
	if cfg != nil && cfg.TUI.DashboardRefreshMs > 0 {
		interval = time.Duration(cfg.TUI.DashboardRefreshMs) * time.Millisecond
	}
	for {
		result, err := ops.RunStatus(cfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
			os.Exit(1)
		}
		if watch {
			fmt.Print("\033[H\033[2J") // clear screen for in-place refresh
		}
		printStatus(result)
		if !watch {
			return
		}
		time.Sleep(interval)
	}
}

func printStatus(r *ops.StatusResult) {
	fmt.Printf("headless-macs %s — status at %s\n\n", version, time.Now().Format(time.RFC1123))
	for _, d := range r.Daemons {
		if d.Running {
			fmt.Printf("[UP]   %-32s PID %-8d %8s  %5.1f%%\n",
				d.Label, d.PID, formatBytes(d.RSSBytes), d.CPUPercent)
		} else {
			fmt.Printf("[DOWN] %-32s\n", d.Label)
		}
	}
	if r.Hardware != nil {
		h := r.Hardware
		fmt.Println()
		fmt.Printf("cpu power  %5.1f W    cpu temp  %5.1f C\n", h.CPUPowerW, h.CPUTempC)
		fmt.Printf("gpu power  %5.1f W    gpu temp  %5.1f C\n", h.GPUPowerW, h.GPUTempC)
		fmt.Printf("sys power  %5.1f W\n", h.SysPowerW)
		if h.RAMTotalB > 0 {
			fmt.Printf("memory     %s / %s\n", formatBytes(h.RAMUsageB), formatBytes(h.RAMTotalB))
		}
	}
}

func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%dB", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%ciB", float64(b)/float64(div), "KMGTPE"[exp])
}

// printVersionNudge prints a low-key stderr line when the running binary's
// version differs from whatever last successfully ran Baseline/Install
// Tools on this box — including "never has," i.e. an existing pre-Phase-10
// install. Names the commands to re-run; does not enumerate what changed
// (that's CHANGELOG.md's job). See PHASE_10_PLAN.md, Phase 10G.
func printVersionNudge() {
	mismatched, marker := ops.VersionMismatch()
	if !mismatched {
		return
	}
	if marker == nil {
		fmt.Fprintf(os.Stderr, "[INFO] This box has never had 'baseline'/'install-tools' run under version %s — run them to apply current fixes.\n", version)
		return
	}
	fmt.Fprintf(os.Stderr, "[INFO] Running v%s, but this box was last configured by v%s — re-run 'sudo headless-macs baseline' / 'install-tools' to pick up fixes since then.\n",
		version, marker.Version)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func checkPlatform() {
	if runtime.GOOS != "darwin" {
		fmt.Fprintln(os.Stderr, "ERROR: macOS required.")
		os.Exit(1)
	}
	if runtime.GOARCH != "arm64" {
		fmt.Fprintln(os.Stderr, "ERROR: Apple Silicon (arm64) required.")
		os.Exit(1)
	}
}

func loadConfig(cmd string) (*config.Config, error) {
	cfgPath := config.UserConfigPath()
	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		if cmd == "restore" {
			// restore doesn't need a config
			return &config.Config{}, nil
		}
		templatePath := findTemplate()
		if templatePath == "" {
			return nil, fmt.Errorf("config not found and no template available — run from repo directory or run the TUI first")
		}
		if err := config.Bootstrap(templatePath); err != nil {
			return nil, fmt.Errorf("could not create config: %w", err)
		}
		fmt.Printf("[INFO] Created config: %s\n", cfgPath)
	}
	return config.Load()
}

// findTemplate searches for config.json relative to the binary and cwd.
func findTemplate() string {
	if _, err := os.Stat("config.json"); err == nil {
		return "config.json"
	}
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	candidate := filepath.Join(filepath.Dir(exe), "config.json")
	if _, err := os.Stat(candidate); err == nil {
		return candidate
	}
	return ""
}
