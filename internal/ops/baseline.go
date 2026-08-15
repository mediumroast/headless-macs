package ops

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/mediumroast/headless-macs/internal/config"
	ilog "github.com/mediumroast/headless-macs/internal/log"
)

// ---------------------------------------------------------------------------
// Result types
// ---------------------------------------------------------------------------

type ActionStatus string

const (
	ActionSet  ActionStatus = "set"
	ActionSkip ActionStatus = "skip"
	ActionWarn ActionStatus = "warn"
	ActionInfo ActionStatus = "info"
	ActionFail ActionStatus = "fail"
)

type BaselineAction struct {
	Section string
	Status  ActionStatus
	Message string
	Detail  string
}

type BaselineResult struct {
	Actions  []BaselineAction
	Sets     int
	Skips    int
	Warnings int
	Failures int
	LogPath  string
}

// BaselineOptions controls which sections run.
type BaselineOptions struct {
	PowerOnly bool // true when invoked by the pmset-heal timer
}

// ---------------------------------------------------------------------------
// Main entry point
// ---------------------------------------------------------------------------

// RunBaseline applies the system baseline. Must run as root (sudo).
func RunBaseline(cfg *config.Config, opts BaselineOptions) (*BaselineResult, error) {
	if os.Getuid() != 0 {
		return nil, fmt.Errorf("baseline requires root — run as: sudo headless-macs")
	}

	r := &BaselineResult{}

	logPath, err := ilog.Init("setup")
	if err != nil {
		logPath = "/tmp/headless-macs-setup.log"
	}
	r.LogPath = logPath

	ilog.Info(fmt.Sprintf("headless-macs %s — system baseline started at %s", "2.0.0", time.Now().Format(time.RFC1123)))
	ilog.Info(fmt.Sprintf("Hardware: %s | %dGB RAM | macOS %s",
		sysctl("hw.model"),
		sysctlInt("hw.memsize")/1024/1024/1024,
		run("sw_vers", "-productVersion"),
	))

	sipEnabled := detectSIP()
	if sipEnabled {
		ilog.Warn("SIP enabled — some service suppression won't persist across reboots")
		r.add("INIT", ActionWarn, "SIP enabled — some service suppressions require SIP off to persist",
			"Boot Recovery Mode → Terminal → csrutil disable")
	} else {
		ilog.Info("SIP disabled — full service suppression available")
	}

	r.sectionPower(cfg)
	if opts.PowerOnly {
		r.add("DONE", ActionInfo, "Power-only run complete (invoked by pmset-heal timer)", "")
		r.printSummary()
		return r, nil
	}

	r.sectionNetwork(cfg, sipEnabled)
	r.sectionServices(cfg, sipEnabled)
	r.sectionSSH()
	r.sectionXcodeCLT()
	r.sectionFirewall(cfg)
	r.sectionMaxfiles()

	r.printSummary()
	ilog.Info(fmt.Sprintf("Log written to: %s", logPath))
	return r, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func (r *BaselineResult) add(section string, status ActionStatus, msg, detail string) {
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

func detectSIP() bool {
	out := run("csrutil", "status")
	return !strings.Contains(out, "disabled")
}

// sudoUID returns the UID of the user who invoked sudo, for gui/ launchctl domains.
func sudoUID() string {
	if uid := os.Getenv("SUDO_UID"); uid != "" {
		return uid
	}
	return "0"
}

func guiDomain(service string) string {
	return "gui/" + sudoUID() + "/" + service
}

// runCmd runs a command and returns combined output + error.
func runCmd(name string, args ...string) error {
	return exec.Command(name, args...).Run()
}

// runCmdOut runs a command and returns stdout.
func runCmdOut(name string, args ...string) string {
	out, _ := exec.Command(name, args...).Output()
	return strings.TrimSpace(string(out))
}

// ---------------------------------------------------------------------------
// Idempotency primitives
// ---------------------------------------------------------------------------

// applyPmset sets a pmset key if the current value differs.
func (r *BaselineResult) applyPmset(section, key, value string) {
	current := ""
	pmOut := runCmdOut("pmset", "-g")
	for _, line := range strings.Split(pmOut, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == key {
			current = fields[1]
			break
		}
	}
	if current == value {
		r.add(section, ActionSkip, fmt.Sprintf("pmset %s already %s", key, value), "")
		return
	}
	if err := runCmd("pmset", "-a", key, value); err != nil {
		r.add(section, ActionFail, fmt.Sprintf("pmset %s %s failed: %v", key, value, err), "")
		return
	}
	r.add(section, ActionSet, fmt.Sprintf("pmset %s %s  (was: %s)", key, value, orUnset(current)), "")
}

// disableService disables a launchd service if SIP allows it.
func (r *BaselineResult) disableService(section, domain string, sipEnabled bool) {
	if sipEnabled {
		r.add(section, ActionSkip, fmt.Sprintf("[SKIP-SIP] %s (requires SIP off for persistence)", domain), "")
		return
	}
	_ = runCmd("launchctl", "disable", domain)
	r.add(section, ActionSet, fmt.Sprintf("disabled %s", domain), "")
}

// installLaunchDaemon writes a plist and bootstraps it if not already present.
func (r *BaselineResult) installLaunchDaemon(section, plistPath, label, content string) {
	if _, err := os.Stat(plistPath); err == nil {
		r.add(section, ActionSkip, fmt.Sprintf("%s already installed", label), "")
		return
	}
	if err := os.WriteFile(plistPath, []byte(content), 0o644); err != nil {
		r.add(section, ActionFail, fmt.Sprintf("write %s: %v", plistPath, err), "")
		return
	}
	_ = runCmd("chown", "root:wheel", plistPath)
	_ = runCmd("chmod", "644", plistPath)
	if err := runCmd("launchctl", "bootstrap", "system", plistPath); err != nil {
		r.add(section, ActionWarn, fmt.Sprintf("%s written but bootstrap failed: %v", label, err), "")
		return
	}
	r.add(section, ActionSet, fmt.Sprintf("%s installed and started", label), "")
}

func (r *BaselineResult) defaultsWrite(section string, args ...string) {
	if err := runCmd("defaults", args...); err != nil {
		r.add(section, ActionWarn, fmt.Sprintf("defaults write failed (%v): %v", args, err), "")
	}
}

func (r *BaselineResult) printSummary() {
	ilog.Info(fmt.Sprintf("Baseline complete: %d set, %d skipped, %d warnings, %d failures",
		r.Sets, r.Skips, r.Warnings, r.Failures))
}

func orUnset(s string) string {
	if s == "" {
		return "unset"
	}
	return s
}

// ---------------------------------------------------------------------------
// Section 1: Power Management
// ---------------------------------------------------------------------------

const caffeinatePlistContent = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>com.llm-server.caffeinate</string>
  <key>ProgramArguments</key>
  <array>
    <string>/usr/bin/caffeinate</string>
    <string>-dimsu</string>
  </array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
</dict>
</plist>
`

const pmsetHealPlistContent = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>com.llm-server.pmset-heal</string>
  <key>ProgramArguments</key>
  <array>
    <string>/usr/local/bin/headless-macs</string>
    <string>--power-only</string>
  </array>
  <key>StartCalendarInterval</key>
  <dict>
    <key>Hour</key><integer>3</integer>
    <key>Minute</key><integer>0</integer>
  </dict>
  <key>StandardOutPath</key><string>/var/log/mac-llm-setup/pmset-heal.log</string>
  <key>StandardErrorPath</key><string>/var/log/mac-llm-setup/pmset-heal.log</string>
</dict>
</plist>
`

func (r *BaselineResult) sectionPower(cfg *config.Config) {
	sec := "POWER"
	ilog.Info("=== Section 1: Power Management ===")

	pmSettings := []struct{ key, val string }{
		{"sleep", "0"},
		{"disablesleep", "1"},
		{"disksleep", "0"},
		{"standby", "0"},
		{"autopoweroff", "0"},
		{"powernap", "0"},
		{"networkoversleep", "0"},
		{"autorestart", "1"},
		{"womp", "1"},
		{"tcpkeepalive", "1"},
		{"displaysleep", "10"},
	}
	for _, s := range pmSettings {
		r.applyPmset(sec, s.key, s.val)
	}

	// MacBook-specific settings
	hwModel := sysctl("hw.model")
	if strings.Contains(strings.ToLower(hwModel), "macbook") {
		_ = runCmd("pmset", "-b", "sleep", "0")
		_ = runCmd("pmset", "-c", "sleep", "0")
		r.add(sec, ActionSet, "MacBook: battery and AC sleep set to 0", "")
		r.add(sec, ActionWarn, "MacBook: lid-close (clamshell) headless requires HDMI dummy plug",
			"Purchase an HDMI dummy plug before going headless")
	}

	r.installLaunchDaemon(sec,
		"/Library/LaunchDaemons/com.llm-server.caffeinate.plist",
		"com.llm-server.caffeinate",
		caffeinatePlistContent,
	)
	r.installLaunchDaemon(sec,
		"/Library/LaunchDaemons/com.llm-server.pmset-heal.plist",
		"com.llm-server.pmset-heal",
		pmsetHealPlistContent,
	)
}

// ---------------------------------------------------------------------------
// Section 2: Network Stack Tuning
// ---------------------------------------------------------------------------

const sysctlPlistContent = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>com.llm-server.sysctl-tuning</string>
  <key>ProgramArguments</key>
  <array>
    <string>/bin/sh</string>
    <string>-c</string>
    <string>/usr/sbin/sysctl -w net.inet.tcp.sendspace=1048576; /usr/sbin/sysctl -w net.inet.tcp.recvspace=1048576; /usr/sbin/sysctl -w kern.ipc.maxsockbuf=8388608; /usr/sbin/sysctl -w net.inet.tcp.autorcvbufmax=8388608; /usr/sbin/sysctl -w net.inet.tcp.autosndbufmax=8388608; /usr/sbin/sysctl -w kern.ipc.somaxconn=2048</string>
  </array>
  <key>RunAtLoad</key><true/>
</dict>
</plist>
`

var sysctlTunings = []struct{ key, val string }{
	{"net.inet.tcp.sendspace", "1048576"},
	{"net.inet.tcp.recvspace", "1048576"},
	{"kern.ipc.maxsockbuf", "8388608"},
	{"net.inet.tcp.autorcvbufmax", "8388608"},
	{"net.inet.tcp.autosndbufmax", "8388608"},
	{"kern.ipc.somaxconn", "2048"},
}

func (r *BaselineResult) sectionNetwork(cfg *config.Config, sipEnabled bool) {
	sec := "NETWORK"
	ilog.Info("=== Section 2: Network Stack Tuning ===")

	if cfg.System.NetworkTuning {
		r.installLaunchDaemon(sec,
			"/Library/LaunchDaemons/com.llm-server.sysctl-tuning.plist",
			"com.llm-server.sysctl-tuning",
			sysctlPlistContent,
		)
		// Apply live for current session
		// NOTE: net.inet.tcp.rfc1323 removed in El Capitan — do not add
		// NOTE: serverperfmode is Intel-only — breaks on Apple Silicon — do not add
		for _, t := range sysctlTunings {
			_ = runCmd("sysctl", "-w", t.key+"="+t.val)
		}
		r.add(sec, ActionSet, "Network sysctl tuning applied (live + persistent via LaunchDaemon)", "")
	} else {
		r.add(sec, ActionSkip, "Network tuning disabled in config.json", "")
	}
}

// ---------------------------------------------------------------------------
// Section 3: Service Suppression
// ---------------------------------------------------------------------------

func (r *BaselineResult) sectionServices(cfg *config.Config, sipEnabled bool) {
	sec := "SERVICES"
	ilog.Info("=== Section 3: Service Suppression ===")

	// Snapshot — required for restore
	snapshotDir := "/var/log/mac-llm-setup/snapshots"
	_ = os.MkdirAll(snapshotDir, 0o755)
	snapshotPath := filepath.Join(snapshotDir, "services-"+time.Now().Format("20060102")+".txt")
	if _, err := os.Stat(snapshotPath); os.IsNotExist(err) {
		out1 := runCmdOut("launchctl", "print-disabled", "system")
		out2 := runCmdOut("launchctl", "print-disabled", fmt.Sprintf("gui/%s", sudoUID()))
		_ = os.WriteFile(snapshotPath, []byte(out1+"\n"+out2), 0o644)
		r.add(sec, ActionSet, "Pre-change service snapshot saved", snapshotPath)
	} else {
		r.add(sec, ActionSkip, "Service snapshot already exists for today", snapshotPath)
	}

	// Spotlight — highest priority; suppress regardless of SIP
	_ = runCmd("mdutil", "-a", "-i", "off")
	_ = runCmd("mdutil", "-i", "off", "/Library/Ollama")
	r.add(sec, ActionSet, "Spotlight indexing disabled", "")
	if !sipEnabled {
		_ = runCmd("launchctl", "bootout", "system",
			"/System/Library/LaunchDaemons/com.apple.metadata.mds.plist")
		_ = runCmd("launchctl", "disable", "system/com.apple.metadata.mds")
		r.add(sec, ActionSet, "Spotlight daemon disabled (SIP off)", "")
	}

	// Telemetry
	if cfg.System.DisableTelemetry {
		for _, svc := range []string{
			"system/com.apple.analyticsd",
			"system/com.apple.diagnosticd",
			"system/com.apple.spindump",
			"system/com.apple.tailspind",
			"system/com.apple.triald",
			guiDomain("com.apple.UsageTrackingAgent"),
		} {
			r.disableService(sec, svc, sipEnabled)
		}
	}

	// Siri
	if cfg.System.DisableSiri {
		for _, svc := range []string{
			guiDomain("com.apple.Siri"),
			guiDomain("com.apple.siriknowledged"),
			guiDomain("com.apple.assistant_service"),
			"system/com.apple.siriinferenced",
		} {
			r.disableService(sec, svc, sipEnabled)
		}
	}

	// iCloud
	if cfg.System.DisableICloud {
		for _, svc := range []string{
			guiDomain("com.apple.cloudd"),
			guiDomain("com.apple.cloudpaird"),
			guiDomain("com.apple.iCloudNotificationAgent"),
		} {
			r.disableService(sec, svc, sipEnabled)
		}
	}

	// Media services
	if cfg.System.DisableMediaServices {
		for _, svc := range []string{
			guiDomain("com.apple.AMPArtworkAgent"),
			guiDomain("com.apple.AMPLibraryAgent"),
			guiDomain("com.apple.music.d"),
		} {
			r.disableService(sec, svc, sipEnabled)
		}
	}

	// Biome / knowledge graph — competes for ANE bandwidth
	for _, svc := range []string{
		"system/com.apple.biomed",
		guiDomain("com.apple.biomesyncd"),
		guiDomain("com.apple.contextstored"),
		guiDomain("com.apple.knowledge-agent"),
		guiDomain("com.apple.LiveLookup"),
		guiDomain("com.apple.parsecd"),
		guiDomain("com.apple.tipsd"),
	} {
		r.disableService(sec, svc, sipEnabled)
	}

	// AirDrop / Handoff
	if cfg.System.DisableAirdropHandoff {
		r.defaultsWrite(sec, "write", "com.apple.NetworkBrowser", "DisableAirDrop", "-bool", "true")
		r.defaultsWrite(sec, "write", "com.apple.coreservices.useractivityd", "ActivityAdvertisingAllowed", "-bool", "false")
		r.defaultsWrite(sec, "write", "com.apple.coreservices.useractivityd", "ActivityReceivingAllowed", "-bool", "false")
		r.add(sec, ActionSet, "AirDrop and Handoff disabled", "")
	}

	// App Nap
	r.defaultsWrite(sec, "write", "NSGlobalDomain", "NSAppSleepDisabled", "-bool", "YES")
	r.add(sec, ActionSet, "App Nap disabled", "")

	// Notifications DND
	if cfg.System.DisableNotifications {
		r.defaultsWrite(sec, "write", "com.apple.notificationcenterui", "dndStart", "-int", "0")
		r.defaultsWrite(sec, "write", "com.apple.notificationcenterui", "dndEnd", "-int", "1440")
		r.add(sec, ActionSet, "Notification Center DND enabled (all day)", "")
	}

	// UI animations — reduce WindowServer overhead on headless machine
	r.defaultsWrite(sec, "write", "com.apple.dock", "launchanim", "-bool", "false")
	r.defaultsWrite(sec, "write", "com.apple.dock", "expose-animation-duration", "-float", "0")
	r.defaultsWrite(sec, "write", "com.apple.finder", "DisableAllAnimations", "-bool", "true")
	_ = runCmd("killall", "Dock")
	_ = runCmd("killall", "Finder")
	r.add(sec, ActionSet, "Dock and Finder animations disabled", "")

	// Screen saver
	r.defaultsWrite(sec, "-currentHost", "write", "com.apple.screensaver", "idleTime", "-int", "0")
	r.add(sec, ActionSet, "Screen saver disabled", "")

	// Software Update
	if cfg.System.DisableSoftwareUpdate {
		_ = runCmd("softwareupdate", "--schedule", "off")
		r.defaultsWrite(sec, "write", "/Library/Preferences/com.apple.SoftwareUpdate", "AutomaticCheckEnabled", "-bool", "false")
		r.defaultsWrite(sec, "write", "/Library/Preferences/com.apple.SoftwareUpdate", "AutomaticDownload", "-bool", "false")
		r.defaultsWrite(sec, "write", "/Library/Preferences/com.apple.SoftwareUpdate", "AutomaticallyInstallMacOSUpdates", "-bool", "false")
		r.add(sec, ActionSet, "Automatic software updates disabled", "")
	}

	// Time Machine
	if cfg.System.DisableTimeMachine {
		_ = runCmd("tmutil", "disable")
		_ = runCmd("tmutil", "addexclusion", "/Library/Ollama")
		r.add(sec, ActionSet, "Time Machine disabled; /Library/Ollama excluded", "")
	}
}

// ---------------------------------------------------------------------------
// Section 4: SSH Hardening
// ---------------------------------------------------------------------------

const sshdDropinHeader = "# Managed by headless-macs — do not edit manually.\n# To change settings, re-run: sudo headless-macs\n"

func (r *BaselineResult) sectionSSH() {
	sec := "SSH"
	ilog.Info("=== Section 4: SSH Hardening ===")

	// Enable SSH — systemsetup is broken on macOS 26+; launchctl is primary
	err1 := runCmd("launchctl", "enable", "system/com.openssh.sshd")
	err2 := runCmd("launchctl", "kickstart", "-k", "system/com.openssh.sshd")
	if err1 == nil && err2 == nil {
		r.add(sec, ActionSet, "SSH enabled via launchctl", "")
	} else {
		_ = runCmd("systemsetup", "-setremotelogin", "on")
		r.add(sec, ActionSet, "SSH enabled via systemsetup (fallback)", "")
	}

	// Authorized keys check
	homeDir, _ := os.UserHomeDir()
	sudoHome := os.Getenv("SUDO_USER")
	authKeysFound := false
	if _, err := os.Stat(filepath.Join(homeDir, ".ssh", "authorized_keys")); err == nil {
		authKeysFound = true
	}
	if sudoHome != "" {
		if _, err := os.Stat(filepath.Join("/Users", sudoHome, ".ssh", "authorized_keys")); err == nil {
			authKeysFound = true
		}
	}
	if _, err := os.Stat("/etc/ssh/authorized_keys"); err == nil {
		authKeysFound = true
	}

	// Build drop-in content
	dropin := sshdDropinHeader +
		"PermitRootLogin no\n" +
		"PubkeyAuthentication yes\n" +
		"MaxAuthTries 3\n" +
		"ClientAliveInterval 120\n" +
		"ClientAliveCountMax 10\n"

	if authKeysFound {
		dropin += "PasswordAuthentication no\n"
	} else {
		r.add(sec, ActionWarn, "No authorized_keys found — skipping PasswordAuthentication no to avoid lockout",
			"Copy your public key to ~/.ssh/authorized_keys, then re-run setup")
	}

	// Write drop-in if content differs
	dropinDir := "/etc/ssh/sshd_config.d"
	dropinPath := filepath.Join(dropinDir, "100-headless.conf")
	_ = os.MkdirAll(dropinDir, 0o755)

	existing, _ := os.ReadFile(dropinPath)
	if string(existing) == dropin {
		r.add(sec, ActionSkip, "sshd drop-in already up to date", dropinPath)
	} else {
		if err := os.WriteFile(dropinPath, []byte(dropin), 0o644); err != nil {
			r.add(sec, ActionFail, fmt.Sprintf("write sshd drop-in: %v", err), "")
		} else {
			r.add(sec, ActionSet, "sshd drop-in written", dropinPath)
			r.add(sec, ActionInfo, "New SSH connections will pick up config automatically (socket-activated sshd)", "")
		}
	}
}

// ---------------------------------------------------------------------------
// Section 5: Xcode CLT
// ---------------------------------------------------------------------------

func (r *BaselineResult) sectionXcodeCLT() {
	sec := "XCODE"
	ilog.Info("=== Section 5: Xcode Command Line Tools ===")

	if path := runCmdOut("xcode-select", "-p"); path != "" {
		r.add(sec, ActionSkip, fmt.Sprintf("Xcode CLT already installed: %s", path), "")
		return
	}

	r.add(sec, ActionInfo, "Installing Xcode CLT (headless method via softwareupdate)…", "")
	_ = os.WriteFile("/tmp/.com.apple.dt.CommandLineTools.installondemand.in-progress", nil, 0o644)

	listOut := runCmdOut("softwareupdate", "--list")
	cltProd := ""
	for _, line := range strings.Split(listOut, "\n") {
		if strings.Contains(line, "Command Line Tools") && strings.Contains(line, "*") {
			parts := strings.SplitN(line, "*", 2)
			if len(parts) == 2 {
				cltProd = strings.TrimSpace(parts[1])
			}
		}
	}
	_ = os.Remove("/tmp/.com.apple.dt.CommandLineTools.installondemand.in-progress")

	if cltProd == "" {
		r.add(sec, ActionWarn, "CLT not found in softwareupdate list — may require GUI install",
			"Run: xcode-select --install (if a display is attached)")
		return
	}
	if err := runCmd("softwareupdate", "--install", cltProd, "--verbose"); err != nil {
		r.add(sec, ActionFail, fmt.Sprintf("CLT install failed: %v", err), "")
		return
	}
	r.add(sec, ActionSet, "Xcode Command Line Tools installed", "")
}

// ---------------------------------------------------------------------------
// Section 6: Application Firewall
// ---------------------------------------------------------------------------

func (r *BaselineResult) sectionFirewall(cfg *config.Config) {
	sec := "FIREWALL"
	ilog.Info("=== Section 6: Application Firewall ===")

	fw := "/usr/libexec/ApplicationFirewall/socketfilterfw"
	if cfg.Network.DisableFirewall {
		state := runCmdOut(fw, "--getglobalstate")
		if strings.Contains(strings.ToLower(state), "disabled") {
			r.add(sec, ActionSkip, "Application Firewall already disabled", "")
		} else {
			if err := runCmd(fw, "--setglobalstate", "off"); err != nil {
				r.add(sec, ActionFail, fmt.Sprintf("disable firewall: %v", err), "")
			} else {
				r.add(sec, ActionSet, "Application Firewall disabled (network.disable_firewall: true)",
					"Ensure inference nodes are on a trusted isolated network")
			}
		}
	} else {
		r.add(sec, ActionSkip, "Firewall left enabled (default — network.disable_firewall: false)",
			"If clients are on other LAN hosts, set network.localhost_only: false")
	}
}

// ---------------------------------------------------------------------------
// Section 7: File Descriptor Limits
// ---------------------------------------------------------------------------

const maxfilesPlistContent = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>com.llm-server.maxfiles</string>
  <key>ProgramArguments</key>
  <array>
    <string>/bin/launchctl</string>
    <string>limit</string>
    <string>maxfiles</string>
    <string>524288</string>
    <string>1048576</string>
  </array>
  <key>RunAtLoad</key><true/>
</dict>
</plist>
`

func (r *BaselineResult) sectionMaxfiles() {
	sec := "MAXFILES"
	ilog.Info("=== Section 7: File Descriptor Limits ===")

	r.installLaunchDaemon(sec,
		"/Library/LaunchDaemons/com.llm-server.maxfiles.plist",
		"com.llm-server.maxfiles",
		maxfilesPlistContent,
	)
	_ = runCmd("launchctl", "limit", "maxfiles", "524288", "1048576")
	r.add(sec, ActionSet, "File descriptor limits raised (soft: 524288 / hard: 1048576)", "")
}
