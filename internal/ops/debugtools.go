package ops

// debugtools.go — installs the standalone headless-macs-debug binary and
// manages the narrowly-scoped, toggleable NOPASSWD sudo grant it needs to
// run non-interactively over SSH. See docs/planning/PHASE_11_PLAN.md,
// Phase 11F.
//
// headless-macs-debug is embedded into this binary at build time
// (go:embed below) rather than shipped as a second file operators have
// to remember to copy — RunDebugTools() writes the embedded bytes out to
// /usr/local/bin/headless-macs-debug.
//
// The embedded asset (internal/ops/assets/headless-macs-debug) is
// committed to the repo as a bootstrapping fallback: go:embed requires
// the file to exist at compile time for the *whole module*, not just
// when building cmd/headless-macs, so a fresh clone with no prior build
// step would otherwise fail `go build ./...` entirely. `make build`
// always rebuilds cmd/headless-macs-debug and refreshes this file first,
// so the committed copy is a convenience baseline, not the source of
// truth for released binaries — it goes stale between manual rebuilds,
// which is expected and harmless since `make build` fixes it.
import (
	"bytes"
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"os/user"

	ilog "github.com/mediumroast/headless-macs/internal/log"
)

//go:embed assets/headless-macs-debug
var debugBinary []byte

const (
	debugBinaryPath  = "/usr/local/bin/headless-macs-debug"
	debugSudoersPath = "/etc/sudoers.d/headless-macs-debug"
)

// DebugToolsResult carries the outcome of RunDebugTools.
type DebugToolsResult struct {
	Actions  []BaselineAction
	Sets     int
	Skips    int
	Warnings int
	Failures int
	LogPath  string
}

// add follows the same pattern as every other Result type in this
// package (BaselineResult, UpdateResult, ...) — logs via ilog as each
// action happens, not just at the end, so CLI mode's tee'd log file
// reflects the run in real time.
func (r *DebugToolsResult) add(section string, status ActionStatus, msg, detail string) {
	r.Actions = append(r.Actions, BaselineAction{Section: section, Status: status, Message: msg, Detail: detail})
	switch status {
	case ActionSet:
		r.Sets++
		ilog.Set(msg)
	case ActionSkip:
		r.Skips++
		ilog.Skip(msg)
	case ActionWarn:
		r.Warnings++
		ilog.Warn(msg)
	case ActionFail:
		r.Failures++
		ilog.Fail(msg)
	default:
		ilog.Info(msg)
	}
	if detail != "" {
		ilog.Detail(detail)
	}
}

// RunDebugTools installs/updates the embedded headless-macs-debug binary
// and syncs the NOPASSWD sudoers grant to match sudoNopasswdEnabled.
// username is only used (and required) when enabling the grant — the
// caller is responsible for prompting for it interactively and
// validating it before calling this with enabled=true; disabling needs
// no username. Must run as root.
func RunDebugTools(sudoNopasswdEnabled bool, username string) (*DebugToolsResult, error) {
	if os.Getuid() != 0 {
		return nil, fmt.Errorf("debug-tools requires root — run as: sudo headless-macs")
	}
	r := &DebugToolsResult{}

	if logPath, err := ilog.InitTUI("debug-tools"); err == nil {
		r.LogPath = logPath
	}

	r.installBinary()
	r.syncSudoers(sudoNopasswdEnabled, username)

	return r, nil
}

// installBinary writes the embedded headless-macs-debug binary to
// /usr/local/bin, idempotent by content comparison (same pattern as
// installLaunchDaemon in tools.go — compare content, not just existence,
// so a rebuilt binary from a newer headless-macs version actually
// reaches an existing install).
func (r *DebugToolsResult) installBinary() {
	sec := "DEBUG-TOOLS"
	existing, err := os.ReadFile(debugBinaryPath)
	if err == nil && bytes.Equal(existing, debugBinary) {
		r.add(sec, ActionSkip, "headless-macs-debug already installed and up to date", debugBinaryPath)
		return
	}
	if err := os.WriteFile(debugBinaryPath, debugBinary, 0755); err != nil {
		r.add(sec, ActionFail, "Could not write "+debugBinaryPath+": "+err.Error(), "")
		return
	}
	r.add(sec, ActionSet, "headless-macs-debug installed", debugBinaryPath)
}

// syncSudoers writes or removes the NOPASSWD sudoers drop-in to match
// the requested state. Validates the drop-in with `visudo -c` before
// installing it — a malformed sudoers file is a real way to break sudo
// system-wide, this check is not optional — and validates the named
// user actually exists before granting anything to them.
func (r *DebugToolsResult) syncSudoers(enabled bool, username string) {
	sec := "DEBUG-ACCESS"

	if !enabled {
		if _, err := os.Stat(debugSudoersPath); err == nil {
			if err := os.Remove(debugSudoersPath); err != nil {
				r.add(sec, ActionFail, "Could not remove "+debugSudoersPath+": "+err.Error(), "")
				return
			}
			r.add(sec, ActionSet, "NOPASSWD sudo access for headless-macs-debug revoked", debugSudoersPath)
		} else {
			r.add(sec, ActionSkip, "NOPASSWD sudo access already disabled", "")
		}
		return
	}

	if username == "" {
		r.add(sec, ActionFail, "No username given for NOPASSWD grant — cannot enable", "")
		return
	}
	if _, err := user.Lookup(username); err != nil {
		r.add(sec, ActionFail, fmt.Sprintf("User %q does not exist — refusing to write sudoers rule", username), "")
		return
	}

	content := fmt.Sprintf(
		"# Managed by headless-macs — do not edit manually.\n"+
			"# Grants %s passwordless sudo for exactly one command, so\n"+
			"# headless-macs-debug can run non-interactively over SSH.\n"+
			"# To revoke: sudo headless-macs debug-tools (disable the toggle in Edit Config first).\n"+
			"%s ALL=(root) NOPASSWD: %s\n",
		username, username, debugBinaryPath,
	)

	if existing, err := os.ReadFile(debugSudoersPath); err == nil && string(existing) == content {
		r.add(sec, ActionSkip, "NOPASSWD sudo access already granted to "+username, "")
		return
	}

	tmp := debugSudoersPath + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0440); err != nil {
		r.add(sec, ActionFail, "Could not write sudoers drop-in: "+err.Error(), "")
		return
	}
	// Never install an unvalidated sudoers file — a syntax error here can
	// break sudo for the whole system, not just this grant.
	if out, err := exec.Command("visudo", "-c", "-f", tmp).CombinedOutput(); err != nil {
		_ = os.Remove(tmp)
		r.add(sec, ActionFail, "Sudoers syntax check failed, not installed: "+string(out), "")
		return
	}
	if err := os.Rename(tmp, debugSudoersPath); err != nil {
		_ = os.Remove(tmp)
		r.add(sec, ActionFail, "Could not install sudoers drop-in: "+err.Error(), "")
		return
	}
	_ = os.Chmod(debugSudoersPath, 0440)
	r.add(sec, ActionSet, "NOPASSWD sudo access granted to "+username+" for "+debugBinaryPath, debugSudoersPath)
}
