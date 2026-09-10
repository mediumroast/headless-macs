package ops

// disable.go — per-tool teardown (stop daemon, remove plist) triggered
// from the TUI config editor when an operator flips a tool's `enabled`
// flag from true to false and chooses "stop and uninstall now" at the
// confirmation screen. Deliberately narrower than RunRestore(), which
// removes every tool plus the full baseline — this touches exactly one
// tool's daemon and leaves its model/data directories untouched, per the
// operator's own choice. See PHASE_11_PLAN.md, Phase 11A / issue #13.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// DisableResult carries the outcome of DisableTool.
type DisableResult struct {
	Actions []BaselineAction
}

func (r *DisableResult) add(section string, status ActionStatus, msg, detail string) {
	r.Actions = append(r.Actions, BaselineAction{Section: section, Status: status, Message: msg, Detail: detail})
}

// DisableTool stops and removes one serving tool's daemon
// (LaunchDaemon/LaunchAgent plist), leaving its model/data directories
// untouched. toolKey matches the config.json tool key (e.g. "ollama",
// "rapid_mlx", "mlx_lm", "infinity", "exo", "macmon"). Must run as root.
func DisableTool(toolKey string) (*DisableResult, error) {
	if os.Getuid() != 0 {
		return nil, fmt.Errorf("disabling a tool requires root — run as: sudo headless-macs")
	}
	r := &DisableResult{}
	const sec = "DISABLE"

	switch toolKey {
	case "ollama":
		r.stopSystemDaemon(sec, "/Library/LaunchDaemons/com.ollama.server.plist", "com.ollama.server")
	case "rapid_mlx":
		r.stopSystemDaemon(sec, "/Library/LaunchDaemons/com.rapid-mlx.server.plist", "com.rapid-mlx.server")
	case "mlx_lm":
		r.stopSystemDaemon(sec, "/Library/LaunchDaemons/com.mlx-lm.server.plist", "com.mlx-lm.server")
	case "infinity":
		r.stopSystemDaemon(sec, "/Library/LaunchDaemons/com.infinity.server.plist", "com.infinity.server")
	case "macmon":
		r.stopSystemDaemon(sec, "/Library/LaunchDaemons/com.llm-server.macmon.plist", "com.llm-server.macmon")
	case "exo":
		uid := sudoUID()
		userHome := realUserHome()
		exoPlist := filepath.Join(userHome, "Library/LaunchAgents/com.exo.node.plist")
		if _, err := os.Stat(exoPlist); err == nil {
			_ = exec.Command("launchctl", "bootout", "gui/"+uid+"/com.exo.node").Run()
			if err := os.Remove(exoPlist); err != nil {
				r.add(sec, ActionWarn, "Could not remove "+exoPlist+": "+err.Error(), "")
			} else {
				r.add(sec, ActionSet, "Stopped and removed com.exo.node", exoPlist)
			}
		} else {
			r.add(sec, ActionSkip, "com.exo.node not installed", "")
		}
	default:
		r.add(sec, ActionWarn, "Unknown tool: "+toolKey, "")
	}
	return r, nil
}

// stopSystemDaemon bootouts and removes one system-domain LaunchDaemon
// plist, matching the exact pattern RunRestore()'s sectionRemoveDaemons()
// already uses for these same daemons — kept as a small shared step here
// rather than a full refactor of restore.go, since DisableTool only ever
// needs one daemon at a time.
func (r *DisableResult) stopSystemDaemon(section, plist, label string) {
	if _, err := os.Stat(plist); err == nil {
		_ = exec.Command("launchctl", "bootout", "system", plist).Run()
		if err := os.Remove(plist); err != nil {
			r.add(section, ActionWarn, "Could not remove "+plist+": "+err.Error(), "")
		} else {
			r.add(section, ActionSet, "Stopped and removed "+label, plist)
		}
	} else {
		r.add(section, ActionSkip, label+" not installed", "")
	}
	_ = exec.Command("launchctl", "disable", "system/"+label).Run()
}
