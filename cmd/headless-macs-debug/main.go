// Command headless-macs-debug is a small, separate debugging utility
// installed alongside headless-macs. It's intentionally its own binary
// rather than a subcommand of the main tool — see
// docs/planning/PHASE_11_PLAN.md, Phase 11F.
//
// Today it has one subcommand, `logs`, which forces an out-of-cycle
// rotation of every managed service's logs (reusing the shared
// logrotate config headless-macs install-tools already writes — this
// does not reimplement rotation logic) and bundles the result into a
// single timestamped tar.gz, ready to scp off the box. Named
// `headless-macs-debug` rather than `headless-macs-debug-logs` so more
// subcommands can be added later without a rename.
package main

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

const (
	logrotateConfigPath = "/etc/logrotate.d/llm-servers"
	logrotateStatusPath = "/var/log/mac-llm-setup/logrotate.status"
	opsLogDir           = "/var/log/mac-llm-setup"
	bundleDir           = "/var/log/mac-llm-setup/bundles"
)

// toolLogDirs maps the CLI-facing tool name to its log directory, for
// `logs <tool>` narrowing. Matches the paths internal/ops/tools.go
// writes to — kept as a small local copy rather than importing
// internal/ops, since this binary is deliberately standalone.
var toolLogDirs = map[string]string{
	"ollama":    "/var/log/ollama",
	"rapid-mlx": "/var/log/rapid-mlx",
	"mlx-lm":    "/var/log/mlx-lm",
	"infinity":  "/var/log/infinity",
	"exo":       "/var/log/exo",
	"macmon":    "/var/log/macmon",
}

const usage = `headless-macs-debug — debugging utilities for headless-macs

Usage:
  sudo headless-macs-debug logs [tool]

Commands:
  logs [tool]   Force a log rotation and bundle the result into a
                timestamped tar.gz under /var/log/mac-llm-setup/bundles/,
                ready to scp off the box. With no argument, bundles every
                managed tool's logs; with a tool name (ollama, rapid-mlx,
                mlx-lm, infinity, exo, macmon), bundles just that one.

Must be run as root (sudo) — log rotation needs to truncate files it
doesn't own. If you're running this over a non-interactive SSH session
and don't want a password prompt, see 'sudo headless-macs debug-tools'
to enable a narrowly-scoped passwordless sudo grant for this exact
binary (documented in README.md).
`

func main() {
	args := os.Args[1:]
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		fmt.Print(usage)
		os.Exit(0)
	}

	if os.Getuid() != 0 {
		fmt.Fprintln(os.Stderr, "ERROR: headless-macs-debug must run as root.")
		fmt.Fprintln(os.Stderr, "Run: sudo headless-macs-debug "+args[0])
		fmt.Fprintln(os.Stderr, "For passwordless SSH automation, see: sudo headless-macs debug-tools")
		os.Exit(1)
	}

	switch args[0] {
	case "logs":
		tool := ""
		if len(args) > 1 {
			tool = args[1]
		}
		if err := runLogs(tool); err != nil {
			fmt.Fprintln(os.Stderr, "ERROR:", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n%s", args[0], usage)
		os.Exit(1)
	}
}

func runLogs(tool string) error {
	if tool != "" {
		if _, ok := toolLogDirs[tool]; !ok {
			return fmt.Errorf("unknown tool %q (expected one of: ollama, rapid-mlx, mlx-lm, infinity, exo, macmon)", tool)
		}
	}

	// Force rotation via the existing shared logrotate config — does not
	// reimplement rotation logic, just triggers it out of its normal
	// daily schedule.
	if _, err := os.Stat(logrotateConfigPath); err == nil {
		cmd := exec.Command(logrotateBin(), "-f", "-s", logrotateStatusPath, logrotateConfigPath)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("logrotate failed: %v\n%s", err, out)
		}
	} else {
		fmt.Fprintln(os.Stderr, "WARNING: "+logrotateConfigPath+" not found — install-tools may not have run yet; bundling logs as-is, unrotated.")
	}

	if err := os.MkdirAll(bundleDir, 0755); err != nil {
		return fmt.Errorf("could not create %s: %w", bundleDir, err)
	}

	label := "all"
	dirs := make([]string, 0, len(toolLogDirs)+1)
	if tool != "" {
		label = tool
		dirs = append(dirs, toolLogDirs[tool])
	} else {
		for _, d := range toolLogDirs {
			dirs = append(dirs, d)
		}
	}
	dirs = append(dirs, opsLogDir)

	stamp := time.Now().Format("20060102-150405")
	bundlePath := filepath.Join(bundleDir, fmt.Sprintf("debug-%s-%s.tar.gz", label, stamp))
	if err := bundleDirs(bundlePath, dirs); err != nil {
		return fmt.Errorf("could not create bundle: %w", err)
	}

	fmt.Println(bundlePath)
	return nil
}

// logrotateBin locates the logrotate binary, matching
// internal/ops/tools.go's own fallback (Homebrew formula path when not
// on PATH).
func logrotateBin() string {
	if p, err := exec.LookPath("logrotate"); err == nil {
		return p
	}
	return "/opt/homebrew/opt/logrotate/sbin/logrotate"
}

// bundleDirs writes every regular file found under the given directories
// (skipping ones that don't exist — a tool that isn't enabled has no log
// dir at all) into a single gzipped tar at dest.
func bundleDirs(dest string, dirs []string) error {
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()

	gz := gzip.NewWriter(f)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()

	for _, dir := range dirs {
		info, err := os.Stat(dir)
		if err != nil || !info.IsDir() {
			continue
		}
		err = filepath.Walk(dir, func(path string, fi os.FileInfo, err error) error {
			if err != nil || fi.IsDir() {
				return nil
			}
			rel, err := filepath.Rel("/var/log", path)
			if err != nil {
				return nil
			}
			hdr, err := tar.FileInfoHeader(fi, "")
			if err != nil {
				return nil
			}
			hdr.Name = rel
			if err := tw.WriteHeader(hdr); err != nil {
				return err
			}
			src, err := os.Open(path)
			if err != nil {
				// A log a daemon has open for writing may still be
				// readable, but be defensive — skip rather than fail
				// the whole bundle over one unreadable file.
				return nil
			}
			defer src.Close()
			_, err = io.Copy(tw, src)
			return err
		})
		if err != nil {
			return err
		}
	}
	return nil
}
