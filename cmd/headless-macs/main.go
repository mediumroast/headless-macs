package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/mediumroast/headless-macs/internal/config"
	ilog "github.com/mediumroast/headless-macs/internal/log"
	"github.com/mediumroast/headless-macs/internal/ops"
	"github.com/mediumroast/headless-macs/internal/tui"
)

const version = "2.1.1"

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

  (no command)    Launch the interactive TUI

Options:
  --help, -h      Show this help and exit
  --version       Show version and exit

Exit codes for verify: 0 = all pass, 1 = failures present, 2 = warnings only
Config: ~/.headless_macs/config.json
Logs:   /var/log/mac-llm-setup/
`

func main() {
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
		case "precheck", "baseline", "install-tools", "verify", "restore", "update-tools", "storage":
			runCLI(args[0])
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

	tui.Version = version
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

func runCLI(cmd string) {
	checkPlatform()
	ilog.CLIMode = true

	cfg, err := loadConfig(cmd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		os.Exit(1)
	}

	switch cmd {
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
