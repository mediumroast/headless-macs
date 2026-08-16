package ops

// restore.go — undo all changes made by setup/install-tools (ports restore.sh).
//
// Reverses every change: removes LaunchDaemons/Agents, restores pmset,
// re-enables suppressed services, restores Spotlight, sshd_config, defaults,
// sysctl.conf, firewall, and removes the _llmserver service account.
//
// Must run as root. Destructive — caller must confirm before invoking.

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	ilog "github.com/mediumroast/headless-macs/internal/log"
)

// RestoreResult carries the outcome of RunRestore.
type RestoreResult struct {
	Actions  []BaselineAction
	Sets     int
	Skips    int
	Warnings int
	Failures int
	LogPath  string
}

func (r *RestoreResult) add(section string, status ActionStatus, msg, detail string) {
	r.Actions = append(r.Actions, BaselineAction{
		Section: section,
		Status:  status,
		Message: msg,
		Detail:  detail,
	})
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

// ---------------------------------------------------------------------------
// Entry point
// ---------------------------------------------------------------------------

// RunRestore undoes all headless-macs changes. Must run as root.
// The caller is responsible for confirming with the user before calling.
func RunRestore() (*RestoreResult, error) {
	if os.Getuid() != 0 {
		return nil, fmt.Errorf("restore requires root — run as: sudo headless-macs")
	}

	r := &RestoreResult{}

	logPath, err := ilog.InitTUI("restore")
	if err != nil {
		logPath = "/tmp/headless-macs-restore.log"
	}
	r.LogPath = logPath

	ilog.Info(fmt.Sprintf("headless-macs 2.0.0 — restore started at %s", time.Now().Format(time.RFC1123)))

	r.sectionRemoveDaemons()
	r.sectionRestorePmset()
	r.sectionRestoreServices()
	r.sectionRestoreSpotlight()
	r.sectionRestoreSSHD()
	r.sectionRestoreDefaults()
	r.sectionRestoreSysctlConf()
	r.sectionRestoreFirewall()
	r.sectionRemoveLLMServerAccount()

	r.add("DONE", ActionInfo,
		"Mac returned to pre-configuration state — reboot recommended", "")
	ilog.Info(fmt.Sprintf("Log written to: %s", logPath))
	return r, nil
}

// ---------------------------------------------------------------------------
// Section 1: Remove LaunchDaemons and Exo LaunchAgent
// ---------------------------------------------------------------------------

func (r *RestoreResult) sectionRemoveDaemons() {
	sec := "DAEMONS"

	daemons := []struct{ plist, label string }{
		{"/Library/LaunchDaemons/com.ollama.server.plist", "com.ollama.server"},
		{"/Library/LaunchDaemons/com.rapid-mlx.server.plist", "com.rapid-mlx.server"},
		{"/Library/LaunchDaemons/com.mlx-lm.server.plist", "com.mlx-lm.server"},
		{"/Library/LaunchDaemons/com.infinity.server.plist", "com.infinity.server"},
		{"/Library/LaunchDaemons/com.llm-server.caffeinate.plist", "com.llm-server.caffeinate"},
		{"/Library/LaunchDaemons/com.llm-server.sysctl-tuning.plist", "com.llm-server.sysctl-tuning"},
		{"/Library/LaunchDaemons/com.llm-server.maxfiles.plist", "com.llm-server.maxfiles"},
		{"/Library/LaunchDaemons/com.llm-server.pmset-heal.plist", "com.llm-server.pmset-heal"},
		{"/Library/LaunchDaemons/com.llm-server.storage-mount.plist", "com.llm-server.storage-mount"},
	}

	for _, d := range daemons {
		if _, err := os.Stat(d.plist); err == nil {
			_ = exec.Command("launchctl", "bootout", "system", d.plist).Run()
			os.Remove(d.plist)
			r.add(sec, ActionSet, "Removed "+d.plist, "")
		} else {
			r.add(sec, ActionSkip, d.plist+" (not present)", "")
		}
		// Belt-and-suspenders: disable by label even if plist is gone
		_ = exec.Command("launchctl", "disable", "system/"+d.label).Run()
	}

	// Exo LaunchAgent (user-level)
	uid := sudoUID()
	userHome := realUserHome()
	exoPlist := filepath.Join(userHome, "Library/LaunchAgents/com.exo.node.plist")
	if _, err := os.Stat(exoPlist); err == nil {
		_ = exec.Command("launchctl", "bootout", "gui/"+uid+"/com.exo.node").Run()
		os.Remove(exoPlist)
		r.add(sec, ActionSet, "Removed "+exoPlist, "")
	} else {
		r.add(sec, ActionSkip, exoPlist+" (not present)", "")
	}
}

// ---------------------------------------------------------------------------
// Section 2: Restore pmset to safe defaults
// ---------------------------------------------------------------------------

func (r *RestoreResult) sectionRestorePmset() {
	sec := "PMSET"

	settings := []struct{ key, value string }{
		{"sleep", "1"},
		{"disablesleep", "0"},
		{"disksleep", "10"},
		{"standby", "1"},
		{"autopoweroff", "1"},
		{"powernap", "1"},
		{"networkoversleep", "0"},
		{"womp", "0"},
		{"displaysleep", "10"},
		{"tcpkeepalive", "1"},
	}

	for _, s := range settings {
		if exec.Command("pmset", "-a", s.key, s.value).Run() == nil {
			r.add(sec, ActionSet, fmt.Sprintf("pmset %s %s", s.key, s.value), "")
		} else {
			r.add(sec, ActionWarn, fmt.Sprintf("pmset %s %s failed", s.key, s.value), "")
		}
	}
}

// ---------------------------------------------------------------------------
// Section 3: Re-enable suppressed services from snapshot
// ---------------------------------------------------------------------------

func (r *RestoreResult) sectionRestoreServices() {
	sec := "SERVICES"

	snapshotDir := "/var/log/mac-llm-setup/snapshots"
	entries, err := os.ReadDir(snapshotDir)
	if err != nil {
		r.add(sec, ActionWarn, "No service snapshot found — services not restored",
			"Re-enable manually via: sudo launchctl enable system/<service>")
		return
	}

	// Find most recent snapshot
	var latest string
	var latestTime time.Time
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "services-") && strings.HasSuffix(e.Name(), ".txt") {
			info, err := e.Info()
			if err == nil && info.ModTime().After(latestTime) {
				latest = filepath.Join(snapshotDir, e.Name())
				latestTime = info.ModTime()
			}
		}
	}

	if latest == "" {
		r.add(sec, ActionWarn, "No service snapshot found — services not restored",
			"Re-enable manually via: sudo launchctl enable system/<service>")
		return
	}

	r.add(sec, ActionInfo, "Restoring from snapshot: "+latest, "")

	f, err := os.Open(latest)
	if err != nil {
		r.add(sec, ActionWarn, "Could not read snapshot: "+err.Error(), "")
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		// Format: "com.apple.foo => false"
		if !strings.Contains(line, "=> false") {
			continue
		}
		svc := strings.Fields(line)[0]
		if exec.Command("launchctl", "enable", svc).Run() == nil {
			r.add(sec, ActionSet, "Re-enabled: "+svc, "")
		} else {
			r.add(sec, ActionSkip, svc+" (may not exist on this macOS version)", "")
		}
	}
}

// ---------------------------------------------------------------------------
// Section 4: Restore Spotlight
// ---------------------------------------------------------------------------

func (r *RestoreResult) sectionRestoreSpotlight() {
	sec := "SPOTLIGHT"
	if exec.Command("mdutil", "-a", "-i", "on").Run() == nil {
		r.add(sec, ActionSet, "Spotlight indexing re-enabled", "")
	} else {
		r.add(sec, ActionWarn, "Could not re-enable Spotlight", "")
	}
}

// ---------------------------------------------------------------------------
// Section 5: Restore sshd_config
// ---------------------------------------------------------------------------

func (r *RestoreResult) sectionRestoreSSHD() {
	sec := "SSHD"
	dropIn := "/etc/ssh/sshd_config.d/100-headless.conf"
	if _, err := os.Stat(dropIn); err == nil {
		os.Remove(dropIn)
		r.add(sec, ActionSet, "Removed "+dropIn, "")
		// Restart sshd (best-effort; macOS 26 uses kickstart)
		_ = exec.Command("launchctl", "kickstart", "-k", "system/com.openssh.sshd").Run()
		r.add(sec, ActionSet, "sshd restarted", "")
	} else {
		r.add(sec, ActionSkip, dropIn+" (not present)", "")
	}
}

// ---------------------------------------------------------------------------
// Section 6: Restore defaults
// ---------------------------------------------------------------------------

func (r *RestoreResult) sectionRestoreDefaults() {
	sec := "DEFAULTS"

	type defaultsOp struct {
		domain string
		key    string
	}

	// AirDrop / Handoff
	for _, op := range []defaultsOp{
		{"com.apple.NetworkBrowser", "DisableAirDrop"},
		{"com.apple.coreservices.useractivityd", "ActivityAdvertisingAllowed"},
		{"com.apple.coreservices.useractivityd", "ActivityReceivingAllowed"},
	} {
		_ = exec.Command("defaults", "delete", op.domain, op.key).Run()
	}
	r.add(sec, ActionSet, "AirDrop / Handoff defaults restored", "")

	// App Nap
	_ = exec.Command("defaults", "delete", "NSGlobalDomain", "NSAppSleepDisabled").Run()
	r.add(sec, ActionSet, "App Nap restored", "")

	// Notification Center DND
	for _, key := range []string{"dndStart", "dndEnd"} {
		_ = exec.Command("defaults", "delete", "com.apple.notificationcenterui", key).Run()
	}
	r.add(sec, ActionSet, "Notification Center DND restored", "")

	// Dock/Finder animations
	for _, pair := range [][]string{
		{"com.apple.dock", "launchanim"},
		{"com.apple.dock", "expose-animation-duration"},
		{"com.apple.finder", "DisableAllAnimations"},
	} {
		_ = exec.Command("defaults", "delete", pair[0], pair[1]).Run()
	}
	_ = exec.Command("killall", "Dock").Run()
	_ = exec.Command("killall", "Finder").Run()
	r.add(sec, ActionSet, "Dock and Finder animations restored", "")

	// Screen saver
	_ = exec.Command("defaults", "-currentHost", "delete", "com.apple.screensaver", "idleTime").Run()
	r.add(sec, ActionSet, "Screen saver restored", "")

	// Software Update
	_ = exec.Command("softwareupdate", "--schedule", "on").Run()
	for _, key := range []string{"AutomaticCheckEnabled", "AutomaticDownload", "AutomaticallyInstallMacOSUpdates"} {
		_ = exec.Command("defaults", "delete", "/Library/Preferences/com.apple.SoftwareUpdate", key).Run()
	}
	r.add(sec, ActionSet, "Automatic software updates restored", "")

	// Time Machine
	_ = exec.Command("tmutil", "enable").Run()
	r.add(sec, ActionSet, "Time Machine restored", "")
}

// ---------------------------------------------------------------------------
// Section 7: Remove sysctl.conf entries (legacy installs only)
// ---------------------------------------------------------------------------

func (r *RestoreResult) sectionRestoreSysctlConf() {
	sec := "SYSCTL-CONF"
	conf := "/etc/sysctl.conf"

	if _, err := os.Stat(conf); err != nil {
		r.add(sec, ActionSkip, conf+" (not present)", "")
		return
	}

	keys := []string{
		"net.inet.tcp.sendspace",
		"net.inet.tcp.recvspace",
		"kern.ipc.maxsockbuf",
		"net.inet.tcp.autorcvbufmax",
		"net.inet.tcp.autosndbufmax",
		"kern.ipc.somaxconn",
	}

	data, err := os.ReadFile(conf)
	if err != nil {
		r.add(sec, ActionWarn, "Could not read "+conf+": "+err.Error(), "")
		return
	}

	lines := strings.Split(string(data), "\n")
	var kept []string
	removed := 0
	for _, line := range lines {
		drop := false
		for _, k := range keys {
			if strings.HasPrefix(line, k+"=") {
				drop = true
				break
			}
		}
		if drop {
			r.add(sec, ActionSet, "Removed sysctl "+line+" from "+conf, "")
			removed++
		} else {
			kept = append(kept, line)
		}
	}

	if removed == 0 {
		r.add(sec, ActionSkip, "No mac-llm sysctl entries found in "+conf, "")
		return
	}

	content := strings.Join(kept, "\n")
	if strings.TrimSpace(content) == "" {
		os.Remove(conf)
		r.add(sec, ActionSet, conf+" removed (now empty)", "")
	} else {
		os.WriteFile(conf, []byte(content), 0644)
	}
}

// ---------------------------------------------------------------------------
// Section 8: Re-enable Application Firewall
// ---------------------------------------------------------------------------

func (r *RestoreResult) sectionRestoreFirewall() {
	sec := "FIREWALL"
	fw := "/usr/libexec/ApplicationFirewall/socketfilterfw"

	if _, err := os.Stat(fw); err != nil {
		r.add(sec, ActionWarn, "socketfilterfw not found — firewall state unchanged", "")
		return
	}

	out, _ := exec.Command(fw, "--getglobalstate").Output()
	if strings.Contains(strings.ToLower(string(out)), "enabled") {
		r.add(sec, ActionSkip, "Application Firewall already enabled", "")
	} else {
		if exec.Command(fw, "--setglobalstate", "on").Run() == nil {
			r.add(sec, ActionSet, "Application Firewall re-enabled", "")
		} else {
			r.add(sec, ActionWarn, "Could not re-enable Application Firewall", "")
		}
	}
}

// ---------------------------------------------------------------------------
// Section 9: Remove _llmserver service account
// ---------------------------------------------------------------------------

func (r *RestoreResult) sectionRemoveLLMServerAccount() {
	sec := "SERVICE-ACCOUNT"

	if exec.Command("id", "_llmserver").Run() == nil {
		_ = exec.Command("dscl", ".", "-delete", "/Users/_llmserver").Run()
		r.add(sec, ActionSet, "Removed _llmserver user", "")
	} else {
		r.add(sec, ActionSkip, "_llmserver user (not present)", "")
	}

	if exec.Command("dscl", ".", "-read", "/Groups/_llmserver", "PrimaryGroupID").Run() == nil {
		_ = exec.Command("dscl", ".", "-delete", "/Groups/_llmserver").Run()
		r.add(sec, ActionSet, "Removed _llmserver group", "")
	} else {
		r.add(sec, ActionSkip, "_llmserver group (not present)", "")
	}

	if _, err := os.Stat("/Library/LLMServer"); err == nil {
		os.RemoveAll("/Library/LLMServer")
		r.add(sec, ActionSet, "Removed /Library/LLMServer", "")
	} else {
		r.add(sec, ActionSkip, "/Library/LLMServer (not present)", "")
	}

	_ = exec.Command("dscacheutil", "-flushcache").Run()
}
