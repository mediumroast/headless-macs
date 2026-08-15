// Package ops contains the business logic for each pipeline stage.
package ops

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mediumroast/headless-macs/internal/config"
)

// ---------------------------------------------------------------------------
// Result types
// ---------------------------------------------------------------------------

type CheckStatus string

const (
	StatusOK      CheckStatus = "ok"
	StatusWarn    CheckStatus = "warn"
	StatusBlocker CheckStatus = "blocker"
	StatusInfo    CheckStatus = "info"
)

type CheckItem struct {
	Section string
	Status  CheckStatus
	Message string
	Detail  string // optional fix hint or sub-detail
}

type PrecheckResult struct {
	Timestamp  string       `json:"timestamp"`
	Hardware   HardwareInfo `json:"hardware"`
	Capability string       `json:"capability"`
	Security   SecurityInfo `json:"security"`
	Storage    StorageInfo  `json:"storage"`
	Readiness  Readiness    `json:"readiness"`

	// Not serialised — used by TUI and headless output
	Checks []CheckItem `json:"-"`
}

type HardwareInfo struct {
	Model     string `json:"model"`
	Chip      string `json:"chip"`
	Arch      string `json:"arch"`
	RAMGB     int    `json:"ram_gb"`
	IsLaptop  bool   `json:"is_laptop"`
	PerfCores string `json:"perf_cores"`
	EffCores  string `json:"eff_cores"`
}

type SecurityInfo struct {
	SIP           string `json:"sip"`
	FileVault     string `json:"filevault"`
	AutoLoginUser string `json:"auto_login_user"`
}

type StorageInfo struct {
	BootFreeGB               int    `json:"boot_free_gb"`
	ExternalVolumeLabelFound bool   `json:"external_volume_label_found"`
	ExternalVolumeMount      string `json:"external_volume_mount"`
	VolumeConfigured         bool   `json:"volume_configured"`
	ModelRoot                string `json:"model_root"`
}

type Readiness struct {
	Blockers   int  `json:"blockers"`
	Warnings   int  `json:"warnings"`
	CanProceed bool `json:"can_proceed"`
}

// ---------------------------------------------------------------------------
// Main entry point
// ---------------------------------------------------------------------------

// RunPrecheck performs a read-only audit of the system. It returns a
// PrecheckResult and writes /tmp/mac-llm-precheck.json for downstream ops.
func RunPrecheck(cfg *config.Config) (*PrecheckResult, error) {
	r := &PrecheckResult{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}

	r.checkHardware()
	r.checkCapability()
	r.checkSecurity()
	r.checkPrerequisites()
	r.checkNetwork(cfg)
	r.checkStorage(cfg)
	r.checkPower()
	r.finalise()

	if err := r.writeJSON(); err != nil {
		r.add("SUMMARY", StatusWarn, "Could not write /tmp/mac-llm-precheck.json", err.Error())
	}
	return r, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func (r *PrecheckResult) add(section string, status CheckStatus, msg, detail string) {
	r.Checks = append(r.Checks, CheckItem{Section: section, Status: status, Message: msg, Detail: detail})
}

func (r *PrecheckResult) blocker(section, msg, fix string) {
	r.Readiness.Blockers++
	r.add(section, StatusBlocker, msg, fix)
}

func (r *PrecheckResult) warn(section, msg, fix string) {
	r.Readiness.Warnings++
	r.add(section, StatusWarn, msg, fix)
}

func (r *PrecheckResult) ok(section, msg string) {
	r.add(section, StatusOK, msg, "")
}

func (r *PrecheckResult) info(section, msg string) {
	r.add(section, StatusInfo, msg, "")
}

func run(name string, args ...string) string {
	out, err := exec.Command(name, args...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func sysctl(key string) string {
	return run("sysctl", "-n", key)
}

func sysctlInt(key string) int {
	v, _ := strconv.Atoi(sysctl(key))
	return v
}

func commandExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func commandPath(name string) string {
	p, _ := exec.LookPath(name)
	return p
}

// ---------------------------------------------------------------------------
// Section 1: Hardware
// ---------------------------------------------------------------------------

func (r *PrecheckResult) checkHardware() {
	hw := &r.Hardware

	hw.Arch = run("uname", "-m")
	hw.Model = sysctl("hw.model")

	chip := sysctl("machdep.cpu.brand_string")
	if chip == "" {
		chip = run("bash", "-c", `system_profiler SPHardwareDataType 2>/dev/null | awk -F': ' '/Chip/{print $2}'`)
	}
	hw.Chip = chip

	memBytes := sysctlInt("hw.memsize")
	hw.RAMGB = memBytes / 1024 / 1024 / 1024

	hw.IsLaptop = strings.Contains(strings.ToLower(hw.Model), "macbook")

	hw.PerfCores = sysctl("hw.perflevel0.logicalcpu")
	if hw.PerfCores == "" {
		hw.PerfCores = sysctl("hw.logicalcpu")
	}
	hw.EffCores = sysctl("hw.perflevel1.logicalcpu")
	if hw.EffCores == "" {
		hw.EffCores = "N/A"
	}

	r.info("HARDWARE", fmt.Sprintf("Model: %s", hw.Model))
	r.info("HARDWARE", fmt.Sprintf("Chip:  %s", hw.Chip))
	r.info("HARDWARE", fmt.Sprintf("RAM:   %dGB", hw.RAMGB))
	r.info("HARDWARE", fmt.Sprintf("Cores: %s performance, %s efficiency", hw.PerfCores, hw.EffCores))
	if hw.IsLaptop {
		r.info("HARDWARE", "Form factor: Laptop — ensure HDMI dummy plug for headless operation")
	} else {
		r.info("HARDWARE", "Form factor: Desktop")
	}
}

// ---------------------------------------------------------------------------
// Section 2: RAM capability tier
// ---------------------------------------------------------------------------

type ramTier struct {
	capability string
	label      string
	models     []string
}

func classifyRAM(gb int) ramTier {
	switch {
	case gb <= 8:
		return ramTier{"minimal", fmt.Sprintf("%dGB: below practical minimum", gb), []string{"ollama pull llama3.2:3b"}}
	case gb <= 16:
		return ramTier{"7b", fmt.Sprintf("%dGB: 7B Q8 (~8GB)", gb), []string{"ollama pull qwen3:8b", "ollama pull nomic-embed-text"}}
	case gb <= 24:
		return ramTier{"13b", fmt.Sprintf("%dGB: 14B Q4 or 7B Q8 + embeddings", gb), []string{"ollama pull qwen3:14b", "ollama pull qwen3-coder:30b", "ollama pull nomic-embed-text"}}
	case gb <= 32:
		return ramTier{"32b", fmt.Sprintf("%dGB: 32B Q4 (~20GB) or 13B Q8", gb), []string{"ollama pull qwen3:32b", "ollama pull deepseek-r1:32b"}}
	case gb <= 64:
		return ramTier{"70b", fmt.Sprintf("%dGB: 70B Q4 (~43GB) or multiple 32B", gb), []string{"ollama pull llama3.3:70b", "ollama pull deepseek-r1:70b"}}
	case gb <= 128:
		return ramTier{"70b-q8", fmt.Sprintf("%dGB: 70B Q8 (~86GB) or 122B Q4 (~81GB)", gb), []string{"ollama pull llama3.3:70b", "ollama pull qwen3.5:122b"}}
	default:
		return ramTier{"235b", fmt.Sprintf("%dGB: 235B Q4 MoE (~142GB) — full capability", gb), []string{"ollama pull qwen3:235b", "ollama pull llama3.3:70b", "ollama pull mxbai-embed-large"}}
	}
}

func (r *PrecheckResult) checkCapability() {
	tier := classifyRAM(r.Hardware.RAMGB)
	r.Capability = tier.capability

	if tier.capability == "minimal" {
		r.warn("CAPABILITY", tier.label, "LLM inference will be severely constrained")
	} else {
		r.ok("CAPABILITY", tier.label)
	}
	for _, m := range tier.models {
		r.info("CAPABILITY", "Recommended: "+m)
	}
}

// ---------------------------------------------------------------------------
// Section 3: Security
// ---------------------------------------------------------------------------

func (r *PrecheckResult) checkSecurity() {
	sec := &r.Security

	// macOS version
	osVer := run("sw_vers", "-productVersion")
	r.info("SECURITY", fmt.Sprintf("macOS %s", osVer))

	// SIP
	sipRaw := run("csrutil", "status")
	if strings.Contains(sipRaw, "disabled") {
		sec.SIP = "disabled"
		r.ok("SECURITY", "SIP disabled — full service suppression available")
	} else if strings.Contains(sipRaw, "enabled") {
		sec.SIP = "enabled"
		r.warn("SECURITY", "SIP enabled — some service suppressions won't persist across reboots",
			"Boot Recovery Mode → Terminal → csrutil disable")
	} else {
		sec.SIP = "unknown"
		r.info("SECURITY", "SIP status unknown")
	}

	// FileVault — hard blocker for headless reboots
	fvRaw := run("fdesetup", "status")
	if strings.Contains(strings.ToLower(fvRaw), "filevault is on") {
		sec.FileVault = "on"
		r.blocker("SECURITY", "FileVault ON — headless reboots will hang at password prompt",
			"System Settings → Privacy & Security → FileVault → Turn Off")
	} else if strings.Contains(strings.ToLower(fvRaw), "filevault is off") {
		sec.FileVault = "off"
		r.ok("SECURITY", "FileVault off — headless reboots safe")
	} else {
		sec.FileVault = "unknown"
		r.info("SECURITY", "FileVault status unknown")
	}

	// Auto-login
	alUser := run("defaults", "read", "/Library/Preferences/com.apple.loginwindow", "autoLoginUser")
	sec.AutoLoginUser = alUser
	if alUser != "" {
		r.ok("SECURITY", fmt.Sprintf("Auto-login configured (%s)", alUser))
	} else {
		r.warn("SECURITY", "Auto-login not configured — required for Exo; recommended for all headless use",
			"sudo sysadminctl -autologin set -userName <user> -password <pw>")
	}

	// Xcode CLT
	if run("xcode-select", "-p") != "" {
		r.ok("SECURITY", fmt.Sprintf("Xcode CLT installed (%s)", run("xcode-select", "-p")))
	} else {
		r.warn("SECURITY", "Xcode CLT not installed — setup.sh will install via softwareupdate", "")
	}
}

// ---------------------------------------------------------------------------
// Section 4: Prerequisites
// ---------------------------------------------------------------------------

type prereq struct {
	name    string
	cmd     string
	blocker bool
	fix     string
}

var prereqs = []prereq{
	{"Homebrew", "brew", true, `/bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"`},
	{"Python 3", "python3", false, "brew install python@3.12"},
	{"pip3", "pip3", false, "brew install python@3.12"},
	{"jq", "jq", false, "brew install jq"},
	{"curl", "curl", false, "brew install curl"},
	{"git", "git", false, "brew install git"},
	{"Ollama", "ollama", false, "install-tools.sh will install"},
	{"Rapid-MLX", "rapid-mlx", false, "install-tools.sh will install"},
}

func (r *PrecheckResult) checkPrerequisites() {
	for _, p := range prereqs {
		if commandExists(p.cmd) {
			r.ok("PREREQS", fmt.Sprintf("%s: %s", p.name, commandPath(p.cmd)))
		} else if p.blocker {
			r.blocker("PREREQS", p.name+" not installed", p.fix)
		} else {
			r.info("PREREQS", p.name+" not installed — install-tools.sh will handle")
		}
	}

	// mlx_lm — Python module, not a binary
	if run("python3", "-c", "import mlx_lm; print('ok')") == "ok" {
		r.ok("PREREQS", "mlx_lm Python module installed")
	} else {
		r.info("PREREQS", "mlx_lm not installed — install-tools.sh will install via pip")
	}

	// Python version check (mlx-lm requires 3.10+)
	if commandExists("python3") {
		verStr := run("python3", "--version") // e.g. "Python 3.12.3"
		parts := strings.Fields(verStr)
		if len(parts) >= 2 {
			vparts := strings.SplitN(parts[1], ".", 3)
			major, _ := strconv.Atoi(vparts[0])
			minor := 0
			if len(vparts) >= 2 {
				minor, _ = strconv.Atoi(vparts[1])
			}
			if major >= 3 && minor >= 10 {
				r.ok("PREREQS", fmt.Sprintf("Python %s (mlx-lm requires 3.10+)", parts[1]))
			} else {
				r.warn("PREREQS", fmt.Sprintf("Python %s — mlx-lm and Infinity require 3.10+", parts[1]),
					"brew install python@3.12")
			}
		}
	}

	// Ollama running as user vs daemon
	ollamaPIDs := run("pgrep", "-x", "ollama")
	if ollamaPIDs != "" {
		pid := strings.Fields(ollamaPIDs)[0]
		ollamaUser := run("ps", "-o", "user=", "-p", pid)
		if strings.TrimSpace(ollamaUser) == "root" {
			r.ok("PREREQS", "Ollama running as root (LaunchDaemon)")
		} else {
			r.info("PREREQS", fmt.Sprintf("Ollama running as user '%s' (app/login item) — install-tools.sh will convert to LaunchDaemon", strings.TrimSpace(ollamaUser)))
		}
	}
}

// ---------------------------------------------------------------------------
// Section 5: Network & ports
// ---------------------------------------------------------------------------

type portCheck struct {
	Name string
	Port int
}

var toolPorts = []portCheck{
	{"Ollama", 11434},
	{"Rapid-MLX", 8000},
	{"mlx-lm", 8080},
	{"Infinity", 7997},
	{"Exo", 52415},
}

func (r *PrecheckResult) checkNetwork(cfg *config.Config) {
	// IP addresses
	ifaces, _ := net.Interfaces()
	for _, iface := range ifaces {
		addrs, _ := iface.Addrs()
		for _, addr := range addrs {
			ip, _, _ := net.ParseCIDR(addr.String())
			if ip == nil || ip.IsLoopback() || ip.To4() == nil {
				continue
			}
			r.info("NETWORK", fmt.Sprintf("Interface %s: %s", iface.Name, ip.String()))
		}
	}

	// Port availability
	for _, p := range toolPorts {
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p.Port))
		if err != nil {
			// Port in use — try to identify the process
			proc := run("bash", "-c", fmt.Sprintf(
				`lsof -Fpc -iTCP:%d -sTCP:LISTEN 2>/dev/null | head -4 | tr '\n' '|'`, p.Port))
			r.info("NETWORK", fmt.Sprintf("Port %d (%s): in use — %s", p.Port, p.Name, proc))
		} else {
			ln.Close()
			r.ok("NETWORK", fmt.Sprintf("Port %d (%s): available", p.Port, p.Name))
		}
	}

	// SSH
	ln, err := net.Listen("tcp", "127.0.0.1:22")
	if err != nil {
		r.ok("NETWORK", "SSH enabled (port 22 listening)")
	} else {
		ln.Close()
		r.warn("NETWORK", "SSH not enabled",
			"sudo launchctl enable system/com.openssh.sshd && sudo launchctl kickstart -k system/com.openssh.sshd")
	}

	// Firewall state
	fwState := run("defaults", "read", "/Library/Preferences/com.apple.alf", "globalstate")
	if fwState == "" {
		fwState = run("defaults", "read", "/Library/Preferences/com.apple.ApplicationFirewall", "globalstate")
	}
	switch fwState {
	case "0":
		r.warn("NETWORK", "Firewall off — consider enabling if API ports are network-accessible", "")
	case "1":
		r.ok("NETWORK", "Firewall on (essential services)")
	case "2":
		r.info("NETWORK", "Firewall on (block all) — may need rules for tool ports")
	default:
		r.info("NETWORK", "Firewall state unknown")
	}
}

// ---------------------------------------------------------------------------
// Section 6: Storage
// ---------------------------------------------------------------------------

func (r *PrecheckResult) checkStorage(cfg *config.Config) {
	stor := &r.Storage

	// Boot volume free space
	dfOut := run("df", "-g", "/")
	for i, line := range strings.Split(dfOut, "\n") {
		if i == 0 {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 4 {
			stor.BootFreeGB, _ = strconv.Atoi(fields[3])
		}
	}

	switch {
	case stor.BootFreeGB < 20:
		r.blocker("STORAGE", fmt.Sprintf("Critical: only %dGB free on boot volume", stor.BootFreeGB),
			"Free space or attach external volume")
	case stor.BootFreeGB < 50:
		r.warn("STORAGE", fmt.Sprintf("Boot volume has %dGB free — tight for large models", stor.BootFreeGB),
			"Consider external volume (storage.use_external_volume: true)")
	case stor.BootFreeGB < 100:
		r.info("STORAGE", fmt.Sprintf("Boot volume: %dGB free — room for a few models", stor.BootFreeGB))
	default:
		r.ok("STORAGE", fmt.Sprintf("Boot volume: %dGB free — ample", stor.BootFreeGB))
	}

	// Config-specified external volume
	volumeLabel := "LLMStorage"
	if cfg != nil {
		volumeLabel = cfg.Storage.VolumeLabel
	}

	mountInfo := run("diskutil", "info", volumeLabel)
	var mountPoint string
	for _, line := range strings.Split(mountInfo, "\n") {
		if strings.Contains(line, "Mount Point") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				mountPoint = strings.TrimSpace(parts[1])
			}
		}
	}

	if mountPoint != "" && mountPoint != "(null)" {
		stor.ExternalVolumeLabelFound = true
		stor.ExternalVolumeMount = mountPoint
		stor.VolumeConfigured = true
		stor.ModelRoot = filepath.Join(mountPoint, cfg.Storage.ModelsSubdir)
		freeOut := run("df", "-g", mountPoint)
		freeGB := ""
		for i, line := range strings.Split(freeOut, "\n") {
			if i == 0 {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) >= 4 {
				freeGB = fields[3] + "GB"
			}
		}
		r.ok("STORAGE", fmt.Sprintf("Volume '%s' found at %s (%s free)", volumeLabel, mountPoint, freeGB))
	} else {
		stor.ExternalVolumeLabelFound = false
		stor.ExternalVolumeMount = ""
		r.info("STORAGE", fmt.Sprintf("No volume labelled '%s' detected", volumeLabel))
		r.info("STORAGE", fmt.Sprintf("Format one with: diskutil eraseDisk APFS '%s' /dev/diskN", volumeLabel))
	}

	// Laptop HDMI dummy plug reminder
	if r.Hardware.IsLaptop {
		r.warn("STORAGE", "Laptop detected — verify HDMI dummy plug connected before going headless", "")
	}
}

// ---------------------------------------------------------------------------
// Section 7: Power state (read-only, informational)
// ---------------------------------------------------------------------------

var pmsetExpected = map[string]string{
	"sleep":     "0",
	"disksleep": "0",
	"standby":   "0",
	"womp":      "1",
	"autorestart": "1",
}

func (r *PrecheckResult) checkPower() {
	pmOut := run("pmset", "-g")
	for _, line := range strings.Split(pmOut, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		key := fields[0]
		val := fields[1]
		if expected, ok := pmsetExpected[key]; ok {
			if val == expected {
				r.ok("POWER", fmt.Sprintf("pmset %s=%s", key, val))
			} else {
				r.info("POWER", fmt.Sprintf("pmset %s=%s (setup.sh will set to %s)", key, val, expected))
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Section 8: Finalise counters
// ---------------------------------------------------------------------------

func (r *PrecheckResult) finalise() {
	r.Readiness.CanProceed = r.Readiness.Blockers == 0
}

// ---------------------------------------------------------------------------
// JSON output
// ---------------------------------------------------------------------------

func (r *PrecheckResult) writeJSON() error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile("/tmp/mac-llm-precheck.json", data, 0o644)
}

// PrintText prints a human-readable precheck report to stdout,
// matching the [BLOCKER]/[WARN]/[OK]/[INFO] prefix convention.
func (r *PrecheckResult) PrintText() {
	fmt.Println("=== Mac LLM Optimizer — System Precheck ===")
	fmt.Printf("Timestamp: %s\n", r.Timestamp)
	fmt.Printf("Hardware:  %s | %dGB RAM\n", r.Hardware.Model, r.Hardware.RAMGB)
	fmt.Println()
	fmt.Println("NOTE: Read-only audit — no changes will be made.")

	currentSection := ""
	for _, c := range r.Checks {
		if c.Section != currentSection {
			fmt.Printf("\n=== %s ===\n", c.Section)
			currentSection = c.Section
		}
		var prefix string
		switch c.Status {
		case StatusOK:
			prefix = "  [OK]      "
		case StatusWarn:
			prefix = "  [WARN]    "
		case StatusBlocker:
			prefix = "  [BLOCKER] "
		default:
			prefix = "  "
		}
		fmt.Println(prefix + c.Message)
		if c.Detail != "" {
			fmt.Println("             Fix: " + c.Detail)
		}
	}

	fmt.Printf("\n=== RESULT: %d blocker(s), %d warning(s) ===\n", r.Readiness.Blockers, r.Readiness.Warnings)
	if r.Readiness.CanProceed {
		if r.Readiness.Warnings > 0 {
			fmt.Println("  Can proceed — review warnings above.")
		} else {
			fmt.Println("  All clear — ready to run setup.")
		}
	} else {
		fmt.Println("  Blockers must be resolved before proceeding.")
	}
	fmt.Printf("\n  JSON written to: /tmp/mac-llm-precheck.json\n")
}
