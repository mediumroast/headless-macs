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
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

const (
	logrotateConfigPath = "/etc/logrotate.d/llm-servers"
	logrotateStatusPath = "/var/log/mac-llm-setup/logrotate.status"
	opsLogDir           = "/var/log/mac-llm-setup"
	bundleDir           = "/var/log/mac-llm-setup/bundles"
	lockPath            = "/var/log/mac-llm-setup/headless-macs-debug.lock"
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
  sudo headless-macs-debug logs [tool] [--keep=N]
  sudo headless-macs-debug clean

Commands:
  logs [tool]   Force a log rotation and bundle the result into a
                timestamped tar.gz under /var/log/mac-llm-setup/bundles/,
                ready to scp off the box. With no argument, bundles every
                managed tool's logs; with a tool name (ollama, rapid-mlx,
                mlx-lm, infinity, exo, macmon), bundles just that one.
                Only the live log file plus its --keep (default 2) most
                recent rotations are bundled per stream (stdout/stderr),
                not the tool's entire rotation history.
  clean         Delete every bundle under
                /var/log/mac-llm-setup/bundles/, freeing the space they
                use. No confirmation prompt — this is a scriptable admin
                tool, not an interactive one.

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
		fs := flag.NewFlagSet("logs", flag.ExitOnError)
		keep := fs.Int("keep", 2, "most recent rotations to bundle per log stream, in addition to the live file")
		_ = fs.Parse(args[1:])
		tool := ""
		if fs.NArg() > 0 {
			tool = fs.Arg(0)
		}
		if *keep < 0 {
			fmt.Fprintln(os.Stderr, "ERROR: --keep cannot be negative")
			os.Exit(1)
		}
		if err := runLogs(tool, *keep); err != nil {
			fmt.Fprintln(os.Stderr, "ERROR:", err)
			os.Exit(1)
		}
	case "clean":
		if err := runClean(); err != nil {
			fmt.Fprintln(os.Stderr, "ERROR:", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n%s", args[0], usage)
		os.Exit(1)
	}
}

func runLogs(tool string, keep int) error {
	if tool != "" {
		if _, ok := toolLogDirs[tool]; !ok {
			return fmt.Errorf("unknown tool %q (expected one of: ollama, rapid-mlx, mlx-lm, infinity, exo, macmon)", tool)
		}
	}

	// Only one `logs` run at a time. Without this, a caller whose SSH
	// session times out mid-run (the process it started keeps running
	// detached on the box) and then retries ends up with multiple
	// concurrent runs racing the same logrotate state file and piling up
	// CPU/disk work — found live, several stacked-up runs on doppio-1.
	// flock auto-releases if this process dies for any reason, so there's
	// no stale-lock cleanup to get wrong.
	unlock, err := acquireLock()
	if err != nil {
		return err
	}
	defer unlock()

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

	// /var/log/mac-llm-setup (opsLogDir) is headless-macs's own operational
	// logs, not a serving tool's — deliberately not included here. Nobody
	// asking for "logs ollama" wants baseline/verify run logs, and it isn't
	// what "logs" (bare) implies either.
	label := "all"
	dirs := make([]string, 0, len(toolLogDirs))
	if tool != "" {
		label = tool
		dirs = append(dirs, toolLogDirs[tool])
	} else {
		for _, d := range toolLogDirs {
			dirs = append(dirs, d)
		}
	}

	stamp := time.Now().Format("20060102-150405")
	bundlePath := filepath.Join(bundleDir, fmt.Sprintf("debug-%s-%s.tar.gz", label, stamp))
	if err := bundleDirs(bundlePath, dirs, keep); err != nil {
		return fmt.Errorf("could not create bundle: %w", err)
	}

	fmt.Println(bundlePath)
	return nil
}

// acquireLock takes an exclusive, non-blocking flock on lockPath so only
// one `logs` run can be in progress at a time. Returns an unlock func to
// defer; the lock is also released automatically if the process dies
// without calling it (flock is tied to the open file descriptor, not a
// PID file some other process would have to notice and clean up).
func acquireLock() (func(), error) {
	if err := os.MkdirAll(opsLogDir, 0755); err != nil {
		return nil, fmt.Errorf("could not create %s: %w", opsLogDir, err)
	}
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, fmt.Errorf("could not open lock file %s: %w", lockPath, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("another 'headless-macs-debug logs' run is already in progress (lock: %s)", lockPath)
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
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

// bundleDirs writes a capped selection of files from each given directory
// into a single gzipped tar at dest: for each log stream found (stdout,
// stderr, or any other filename treated as its own singleton stream), the
// live file plus its `keep` most recent rotations by mtime — not the
// tool's entire rotation history, which is far more than needed for a
// quick "what's going on right now" pull and, at logrotate's configured
// 100M x5 retention, can be a lot of data per tool.
//
// Directories are read non-recursively (os.ReadDir, not filepath.Walk) —
// these are flat log directories by convention, and reading them flat also
// means this can never again wander into bundleDir (where dest itself
// lives) the way an earlier version did when opsLogDir was still one of
// the source directories: every bundle recursively packed in every bundle
// before it, plus its own in-progress output file being written into
// itself mid-walk. Found live: a handful of runs on doppio-1 went 9MB ->
// 27MB -> ... -> 79GB from a source log directory that was never more than
// a few hundred KB. opsLogDir is no longer a bundle source at all now (see
// runLogs), so that scenario can't recur regardless.
func bundleDirs(dest string, dirs []string, keep int) error {
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
		entries, err := os.ReadDir(dir)
		if err != nil {
			// Directory doesn't exist — that tool isn't enabled. Not an
			// error; just nothing to bundle for it.
			continue
		}
		for _, path := range recentByStream(dir, entries, keep) {
			if err := writeFileToTar(tw, path); err != nil {
				return err
			}
		}
	}
	return nil
}

// recentByStream groups dir's direct-child files by log stream (stdout,
// stderr, or the file's own name if it matches neither — so anything
// unexpected still gets included rather than silently dropped) and returns
// the paths of the `keep`+1 most recently modified files in each group
// (the +1 accounts for the live file itself, which is always the newest).
func recentByStream(dir string, entries []os.DirEntry, keep int) []string {
	type namedEntry struct {
		name    string
		modTime time.Time
	}
	groups := map[string][]namedEntry{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		stream := streamOf(e.Name())
		groups[stream] = append(groups[stream], namedEntry{e.Name(), info.ModTime()})
	}

	limit := keep + 1
	var out []string
	for _, group := range groups {
		sort.Slice(group, func(i, j int) bool { return group[i].modTime.After(group[j].modTime) })
		n := limit
		if n > len(group) {
			n = len(group)
		}
		for _, ne := range group[:n] {
			out = append(out, filepath.Join(dir, ne.name))
		}
	}
	return out
}

// streamOf classifies a log filename into "stdout", "stderr", or (for
// anything that matches neither, so it's never silently excluded) its own
// literal name as a singleton stream.
func streamOf(name string) string {
	switch {
	case strings.HasPrefix(name, "stdout"):
		return "stdout"
	case strings.HasPrefix(name, "stderr"):
		return "stderr"
	default:
		return name
	}
}

// writeFileToTar adds one file to tw, named relative to /var/log (matching
// this binary's existing bundle-path convention).
func writeFileToTar(tw *tar.Writer, path string) error {
	rel, err := filepath.Rel("/var/log", path)
	if err != nil {
		return nil
	}
	src, err := os.Open(path)
	if err != nil {
		// A log a daemon has open for writing may still be readable, but
		// be defensive — skip rather than fail the whole bundle over one
		// unreadable file.
		return nil
	}
	defer src.Close()

	// Re-stat the *opened* file rather than trusting a stat taken before
	// this call — for a live log a daemon is still actively appending to,
	// the file can grow in between. tar requires the header's declared
	// size to exactly match what's written; a stale, smaller size here
	// causes "archive/tar: write too long" once io.Copy writes more bytes
	// than that (found live, bundling Ollama's actively-growing
	// stderr.log on doppio-1).
	liveInfo, err := src.Stat()
	if err != nil {
		return nil
	}
	hdr, err := tar.FileInfoHeader(liveInfo, "")
	if err != nil {
		return nil
	}
	hdr.Name = rel
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	// CopyN, not Copy: caps the write at exactly the declared size even if
	// the file keeps growing during the copy itself, closing the
	// remaining race window rather than just narrowing it.
	n, err := io.CopyN(tw, src, liveInfo.Size())
	if err != nil && err != io.EOF {
		return err
	}
	// The rarer opposite race: the file shrank (e.g. rotated) between the
	// Stat above and here, so fewer bytes were available than declared.
	// Pad explicitly to match the header's declared size exactly, rather
	// than assuming tar.Writer handles a short entry gracefully on its own.
	if pad := liveInfo.Size() - n; pad > 0 {
		if _, err := tw.Write(make([]byte, pad)); err != nil {
			return err
		}
	}
	return nil
}

// runClean deletes every bundle under bundleDir, freeing the space they
// use. Matches on the debug-*.tar.gz naming this binary itself writes, so
// it can't delete something unrelated if bundleDir is ever used for
// anything else later. No confirmation prompt — this is a scriptable admin
// tool, not an interactive one (logs doesn't confirm before forcing a real
// log rotation either). Uses the same lock as logs so it can't race a
// bundle that's still being written.
func runClean() error {
	unlock, err := acquireLock()
	if err != nil {
		return err
	}
	defer unlock()

	entries, err := os.ReadDir(bundleDir)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("nothing to clean —", bundleDir, "does not exist")
			return nil
		}
		return fmt.Errorf("could not read %s: %w", bundleDir, err)
	}

	var removed int
	var freed int64
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "debug-") || !strings.HasSuffix(e.Name(), ".tar.gz") {
			continue
		}
		path := filepath.Join(bundleDir, e.Name())
		if info, err := e.Info(); err == nil {
			freed += info.Size()
		}
		if err := os.Remove(path); err != nil {
			fmt.Fprintln(os.Stderr, "WARNING: could not remove "+path+": "+err.Error())
			continue
		}
		fmt.Println("removed", path)
		removed++
	}
	fmt.Printf("removed %d bundle(s), freed %s\n", removed, formatBytes(freed))
	return nil
}

func formatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
