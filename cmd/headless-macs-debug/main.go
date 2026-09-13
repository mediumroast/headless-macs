// Command headless-macs-debug is a small, separate debugging utility
// installed alongside headless-macs. It's intentionally its own binary
// rather than a subcommand of the main tool — see
// docs/planning/PHASE_11_PLAN.md, Phase 11F. Named `headless-macs-debug`
// rather than `headless-macs-debug-logs` so subcommands could be added
// later without a rename — see docs/planning/PHASE_13_PLAN.md, Phase 13,
// for `start`/`stop`/`mark`.
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
  sudo headless-macs-debug start <tool>
  sudo headless-macs-debug stop <tool>
  sudo headless-macs-debug mark <tool> --start | --stop

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
  start <tool>  Begin a debug session: put <tool>'s daemon into debug-level
                logging, force a log rotation, and restart it so the fresh
                logs start at debug verbosity. Currently supported: ollama
                only (see README.md for why the others differ). Running
                'sudo headless-macs install-tools' while a debug session is
                active reverts this — that's expected, not a bug.
  stop <tool>   End a debug session: return <tool>'s daemon to standard
                logging, restart it, and rotate the logs again so the
                debug-session output is archived on its own. Exits with a
                plain status code — no special output.
  mark <tool> --start | --stop
                Append a timestamped, grep-able marker line to <tool>'s
                stdout.log and stderr.log — independent of start/stop, for
                marking the boundary of whatever you're currently doing
                without restarting the daemon. Works for any of: ollama,
                rapid-mlx, mlx-lm, infinity, exo, macmon.

Must be run as root (sudo) — log rotation needs to truncate files it
doesn't own. If you're running this over a non-interactive SSH session
and don't want a password prompt, see 'sudo headless-macs debug-tools'
to enable a narrowly-scoped passwordless sudo grant for this exact
binary (documented in README.md).
`

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		fmt.Print(usage)
		os.Exit(0)
	}
	// Checked against every arg, not just args[0], so `logs --help` and
	// `clean --help` show this usage text too, without first hitting the
	// root-required check below — asking for help isn't a privileged
	// operation.
	for _, a := range args {
		if a == "--help" || a == "-h" {
			fmt.Print(usage)
			os.Exit(0)
		}
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
		// Without this, -h/--help (which flag registers automatically) or
		// an unknown flag prints Go's own bare auto-generated usage for
		// just this FlagSet's one flag, not this binary's actual usage
		// text — a different, less helpful message than everywhere else.
		fs.Usage = func() { fmt.Print(usage) }
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
	case "start":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "ERROR: start requires a tool name, e.g. 'start ollama'")
			os.Exit(1)
		}
		if err := runStart(args[1]); err != nil {
			fmt.Fprintln(os.Stderr, "ERROR:", err)
			os.Exit(1)
		}
	case "stop":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "ERROR: stop requires a tool name, e.g. 'stop ollama'")
			os.Exit(1)
		}
		if err := runStop(args[1]); err != nil {
			fmt.Fprintln(os.Stderr, "ERROR:", err)
			os.Exit(1)
		}
	case "mark":
		fs := flag.NewFlagSet("mark", flag.ExitOnError)
		fs.Usage = func() { fmt.Print(usage) }
		start := fs.Bool("start", false, "insert a debug-session start marker")
		stop := fs.Bool("stop", false, "insert a debug-session stop marker")
		_ = fs.Parse(args[1:])
		if fs.NArg() < 1 {
			fmt.Fprintln(os.Stderr, "ERROR: mark requires a tool name, e.g. 'mark ollama --start'")
			os.Exit(1)
		}
		if *start == *stop {
			fmt.Fprintln(os.Stderr, "ERROR: mark requires exactly one of --start or --stop")
			os.Exit(1)
		}
		if err := runMark(fs.Arg(0), *start); err != nil {
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

	if err := forceRotateLogs(); err != nil {
		return err
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

// forceRotateLogs triggers an out-of-cycle rotation via the existing
// shared logrotate config — does not reimplement rotation logic, just
// triggers it outside its normal daily schedule. Shared by runLogs,
// runStart, and runStop rather than duplicated in each.
func forceRotateLogs() error {
	if _, err := os.Stat(logrotateConfigPath); err != nil {
		fmt.Fprintln(os.Stderr, "WARNING: "+logrotateConfigPath+" not found — install-tools may not have run yet; skipping rotation.")
		return nil
	}
	cmd := exec.Command(logrotateBin(), "-f", "-s", logrotateStatusPath, logrotateConfigPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("logrotate failed: %v\n%s", err, out)
	}
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

// toolDebugToggle describes how to enable/disable debug-level logging for
// one tool's LaunchDaemon plist. Only "ollama" is implemented today —
// rapid-mlx/mlx-lm/Infinity toggle verbosity via a --log-level CLI
// argument (an array element in ProgramArguments), not an environment
// variable like Ollama, a materially different PlistBuddy operation
// shape; Exo and macmon have no verbosity toggle at all currently. See
// docs/planning/PHASE_13_PLAN.md, Phase 13C. Adding another tool later is
// "implement one more table entry and its enable/disable funcs," not a
// redesign of start/stop themselves.
type toolDebugToggle struct {
	plistPath string
	enable    func(plistPath string) error
	disable   func(plistPath string) error
}

var toolDebugToggles = map[string]toolDebugToggle{
	"ollama": {
		plistPath: "/Library/LaunchDaemons/com.ollama.server.plist",
		enable:    enableOllamaDebug,
		disable:   disableOllamaDebug,
	},
}

const ollamaDebugKey = ":EnvironmentVariables:OLLAMA_DEBUG"

// enableOllamaDebug adds OLLAMA_DEBUG=1 to the plist's EnvironmentVariables
// dict — Ollama's actual, sole documented verbosity switch (see
// docs/troubleshooting.mdx in ollama/ollama: "OLLAMA_DEBUG=1"), not the
// OLLAMA_LOG_LEVEL this project used to write before that was found to be
// fabricated (see PHASE_12_PLAN.md). PlistBuddy's Add fails if the key
// already exists, so presence is checked first and treated as a no-op
// rather than an error — this can be called on an already-debug-mode
// daemon safely.
func enableOllamaDebug(plistPath string) error {
	if plistBuddyHasKey(plistPath, ollamaDebugKey) {
		fmt.Println("OLLAMA_DEBUG already set — already in debug mode")
		return nil
	}
	return plistBuddy(plistPath, "Add "+ollamaDebugKey+" string 1")
}

// disableOllamaDebug removes OLLAMA_DEBUG from the plist. PlistBuddy's
// Delete fails if the key is absent, so presence is checked first and
// treated as a no-op — safe to call when not currently in debug mode.
func disableOllamaDebug(plistPath string) error {
	if !plistBuddyHasKey(plistPath, ollamaDebugKey) {
		fmt.Println("OLLAMA_DEBUG already unset — not in debug mode")
		return nil
	}
	return plistBuddy(plistPath, "Delete "+ollamaDebugKey)
}

// plistBuddyHasKey reports whether entry exists in plistPath, via
// PlistBuddy's own Print command rather than parsing the plist's XML —
// PlistBuddy already understands the format correctly, including array
// indices and nested dicts, so there's no reason to reimplement that.
func plistBuddyHasKey(plistPath, entry string) bool {
	return exec.Command("/usr/libexec/PlistBuddy", "-c", "Print "+entry, plistPath).Run() == nil
}

// plistBuddy runs one PlistBuddy command against plistPath, editing it in
// place — everything else in the file is left untouched, unlike
// regenerating the whole plist the way install-tools does.
func plistBuddy(plistPath, command string) error {
	out, err := exec.Command("/usr/libexec/PlistBuddy", "-c", command, plistPath).CombinedOutput()
	if err != nil {
		return fmt.Errorf("PlistBuddy %q on %s failed: %v\n%s", command, plistPath, err, out)
	}
	return nil
}

// restartDaemon reloads plistPath via bootout+bootstrap — the only way a
// LaunchDaemon picks up an EnvironmentVariables change, since those are
// read once at process start, never live-reloaded. bootout's error is
// ignored: it's expected and harmless if the daemon wasn't currently
// loaded for some reason.
func restartDaemon(plistPath string) error {
	_ = exec.Command("launchctl", "bootout", "system", plistPath).Run()
	out, err := exec.Command("launchctl", "bootstrap", "system", plistPath).CombinedOutput()
	if err != nil {
		return fmt.Errorf("launchctl bootstrap failed: %v\n%s", err, out)
	}
	return nil
}

// runStart begins a debug session for tool: enable debug-level logging in
// its plist, force a log rotation (so the debug session starts in a fresh
// file), then restart the daemon so the new plist actually takes effect.
func runStart(tool string) error {
	toggle, err := lookupDebugToggle(tool)
	if err != nil {
		return err
	}
	if err := toggle.enable(toggle.plistPath); err != nil {
		return err
	}
	if err := forceRotateLogs(); err != nil {
		return err
	}
	if err := restartDaemon(toggle.plistPath); err != nil {
		return err
	}
	fmt.Printf("%s is now in debug mode (%s)\n", tool, toggle.plistPath)
	fmt.Println("Note: running 'sudo headless-macs install-tools' while this debug session is active will revert this — it regenerates the plist from config.json.")
	return nil
}

// runStop ends a debug session for tool: disable debug-level logging,
// restart the daemon, then rotate the logs again so the debug session's
// output is archived on its own, separate from whatever normal-verbosity
// logging follows. No special output — just standard exit codes.
func runStop(tool string) error {
	toggle, err := lookupDebugToggle(tool)
	if err != nil {
		return err
	}
	if err := toggle.disable(toggle.plistPath); err != nil {
		return err
	}
	if err := restartDaemon(toggle.plistPath); err != nil {
		return err
	}
	return forceRotateLogs()
}

// lookupDebugToggle distinguishes an entirely unknown tool name from a
// known one that just doesn't support start/stop yet, so the two error
// messages don't get confused with each other.
func lookupDebugToggle(tool string) (toolDebugToggle, error) {
	if _, ok := toolLogDirs[tool]; !ok {
		return toolDebugToggle{}, fmt.Errorf("unknown tool %q (expected one of: ollama, rapid-mlx, mlx-lm, infinity, exo, macmon)", tool)
	}
	toggle, ok := toolDebugToggles[tool]
	if !ok {
		return toolDebugToggle{}, fmt.Errorf("start/stop not yet supported for %q (currently: ollama only)", tool)
	}
	return toggle, nil
}

// runMark appends a timestamped, grep-able marker line to tool's
// stdout.log and stderr.log — independent of start/stop, for marking a
// boundary in the logs without restarting the daemon. Safe to run
// concurrently with anything else touching these files: appending with
// O_APPEND is POSIX-guaranteed atomic up to PIPE_BUF for a single write(),
// so a second process (the daemon itself) writing at the same time can
// never see a torn/interleaved line — the same mechanism logger(1) and
// syslog rely on. Works for any tool in toolLogDirs, not just the ones
// start/stop support.
func runMark(tool string, isStart bool) error {
	dir, ok := toolLogDirs[tool]
	if !ok {
		return fmt.Errorf("unknown tool %q (expected one of: ollama, rapid-mlx, mlx-lm, infinity, exo, macmon)", tool)
	}
	label := "STOP"
	if isStart {
		label = "START"
	}
	marker := fmt.Sprintf("##### headless-macs-debug: DEBUG SESSION %s %s #####\n", label, time.Now().UTC().Format(time.RFC3339))

	var failures []string
	for _, name := range []string{"stdout.log", "stderr.log"} {
		path := filepath.Join(dir, name)
		f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", path, err))
			continue
		}
		_, writeErr := f.WriteString(marker)
		closeErr := f.Close()
		if writeErr != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", path, writeErr))
		} else if closeErr != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", path, closeErr))
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("could not write marker to: %s", strings.Join(failures, "; "))
	}
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
