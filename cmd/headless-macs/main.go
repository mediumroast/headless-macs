package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/mediumroast/headless-macs/internal/config"
	"github.com/mediumroast/headless-macs/internal/tui"
)

func main() {
	if runtime.GOOS != "darwin" {
		fmt.Fprintln(os.Stderr, "ERROR: macOS required.")
		os.Exit(1)
	}
	if runtime.GOARCH != "arm64" {
		fmt.Fprintln(os.Stderr, "ERROR: Apple Silicon (arm64) required.")
		os.Exit(1)
	}

	// Locate the repo template (same directory as the binary, or the working dir)
	templatePath := findTemplate()

	// First-run bootstrap: copy template to ~/.headless_macs/config.json if absent
	cfgPath := config.UserConfigPath()
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
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: could not load config: %v\n", err)
		os.Exit(1)
	}

	app := tui.NewApp(cfg)
	p := tea.NewProgram(app, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		os.Exit(1)
	}
}

// findTemplate searches for config.json relative to the binary and cwd.
func findTemplate() string {
	// cwd first (running from repo root)
	if _, err := os.Stat("config.json"); err == nil {
		return "config.json"
	}
	// same directory as the binary
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
