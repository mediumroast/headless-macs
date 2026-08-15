package ops

// verify.go — health check report (ports verify.sh).
//
// Read-only: makes no changes. Checks system baseline, all enabled tools,
// storage, and memory pressure. Requires root for daemon state checks.
//
// Returns a *PrecheckResult (reusing its CheckItem/CheckStatus types) so the
// same TUI screen can render both precheck and verify output.

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/mediumroast/headless-macs/internal/config"
	ilog "github.com/mediumroast/headless-macs/internal/log"
)

// VerifyResult wraps PrecheckResult with verify-specific counters.
// Reusing CheckItem/CheckStatus (OK=pass, Warn=warn, Blocker=fail, Info=info/skip)
// lets the same PrecheckModel screen render it without duplication.
type VerifyResult struct {
	Checks   []CheckItem
	Passes   int
	Warnings int
	Failures int
	LogPath  string
}

func (r *VerifyResult) pass(section, msg, detail string) {
	r.Checks = append(r.Checks, CheckItem{Section: section, Status: StatusOK, Message: msg, Detail: detail})
	r.Passes++
	ilog.Pass(msg)
	if detail != "" {
		ilog.Detail(detail)
	}
}

func (r *VerifyResult) fail(section, msg, detail string) {
	r.Checks = append(r.Checks, CheckItem{Section: section, Status: StatusBlocker, Message: msg, Detail: detail})
	r.Failures++
	ilog.Fail(msg)
	if detail != "" {
		ilog.Detail(detail)
	}
}

func (r *VerifyResult) warn(section, msg, detail string) {
	r.Checks = append(r.Checks, CheckItem{Section: section, Status: StatusWarn, Message: msg, Detail: detail})
	r.Warnings++
	ilog.Warn(msg)
	if detail != "" {
		ilog.Detail(detail)
	}
}

func (r *VerifyResult) skip(section, msg string) {
	r.Checks = append(r.Checks, CheckItem{Section: section, Status: StatusInfo, Message: "[SKIP] " + msg})
	ilog.Skip(msg)
}

func (r *VerifyResult) info(section, msg string) {
	r.Checks = append(r.Checks, CheckItem{Section: section, Status: StatusInfo, Message: msg})
	ilog.Info(msg)
}

// ---------------------------------------------------------------------------
// Entry point
// ---------------------------------------------------------------------------

// RunVerify performs the health check. Read-only; requires root for daemon checks.
func RunVerify(cfg *config.Config) (*VerifyResult, error) {
	if os.Getuid() != 0 {
		return nil, fmt.Errorf("verify requires root for daemon state checks — run as: sudo headless-macs")
	}

	r := &VerifyResult{}

	logPath, err := ilog.Init("verify")
	if err != nil {
		logPath = "/tmp/headless-macs-verify.log"
	}
	r.LogPath = logPath

	sipEnabled := detectSIP()
	sipState := "disabled"
	if sipEnabled {
		sipState = "enabled"
	}

	ilog.Info(fmt.Sprintf("headless-macs 2.0.0 — health report at %s", time.Now().Format(time.RFC1123)))
	ilog.Info(fmt.Sprintf("Hardware: %s | %dGB RAM | macOS %s | SIP: %s",
		sysctl("hw.model"),
		sysctlInt("hw.memsize")/1024/1024/1024,
		run("sw_vers", "-productVersion"),
		sipState,
	))

	r.sectionSystem(cfg, sipEnabled)
	r.sectionNetwork(cfg)
	r.sectionStorage(cfg)
	r.sectionOllama(cfg)
	r.sectionRapidMLX(cfg)
	r.sectionMLXLM(cfg)
	r.sectionInfinity(cfg)
	r.sectionExo(cfg)
	r.sectionMemory()

	ilog.Info(fmt.Sprintf("Result: %d pass  %d warn  %d fail", r.Passes, r.Warnings, r.Failures))
	ilog.Info(fmt.Sprintf("Log written to: %s", logPath))
	return r, nil
}

// ---------------------------------------------------------------------------
// Section helpers
// ---------------------------------------------------------------------------

func (r *VerifyResult) checkPmset(section, key, expected string) {
	out, _ := exec.Command("pmset", "-g").Output()
	actual := ""
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == key {
			actual = fields[1]
			break
		}
	}
	if actual == expected {
		r.pass(section, fmt.Sprintf("pmset %s=%s", key, actual), "")
	} else {
		r.fail(section, fmt.Sprintf("pmset %s=%s  (expected %s)", key, func() string {
			if actual == "" {
				return "unset"
			}
			return actual
		}(), expected), "")
	}
}

func (r *VerifyResult) checkSysctl(section, key, expected string) {
	actual := strings.TrimSpace(run("sysctl", "-n", key))
	if actual == "" {
		actual = "unset"
	}
	if actual == expected {
		r.pass(section, fmt.Sprintf("sysctl %s=%s", key, actual), "")
	} else {
		r.warn(section,
			fmt.Sprintf("sysctl %s=%s  (expected %s — reboot may be needed)", key, actual, expected), "")
	}
}

func (r *VerifyResult) checkDaemon(section, label string) bool {
	out, _ := exec.Command("launchctl", "print", "system/"+label).Output()
	running := strings.Contains(string(out), "state = running")
	if running {
		r.pass(section, label+" running", "")
	} else {
		r.fail(section, label+" not running", "Diagnose: sudo launchctl print system/"+label)
	}
	return running
}

func (r *VerifyResult) checkHTTP(section, name, url string, timeoutSecs int) {
	client := &http.Client{Timeout: time.Duration(timeoutSecs) * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		r.fail(section, fmt.Sprintf("%s API not responding (%s)", name, url), "")
		return
	}
	resp.Body.Close()
	r.pass(section, fmt.Sprintf("%s API responding (%s)", name, url), "")
}

// ---------------------------------------------------------------------------
// SYSTEM
// ---------------------------------------------------------------------------

func (r *VerifyResult) sectionSystem(cfg *config.Config, sipEnabled bool) {
	sec := "SYSTEM"

	// pmset
	r.checkPmset(sec, "sleep", "0")
	r.checkPmset(sec, "disksleep", "0")
	r.checkPmset(sec, "standby", "0")
	r.checkPmset(sec, "womp", "1")
	r.checkPmset(sec, "tcpkeepalive", "1")
	r.checkPmset(sec, "autorestart", "1")

	// Caffeinate daemon
	out, _ := exec.Command("launchctl", "print", "system/com.llm-server.caffeinate").Output()
	if strings.Contains(string(out), "state = running") {
		r.pass(sec, "caffeinate daemon running", "")
	} else {
		r.warn(sec, "caffeinate daemon not running — sleep regression safety net missing",
			"Fix: sudo launchctl bootstrap system /Library/LaunchDaemons/com.llm-server.caffeinate.plist")
	}

	// sysctl-tuning daemon (exits after applying settings; check for installed plist)
	out2, _ := exec.Command("launchctl", "print", "system/com.llm-server.sysctl-tuning").Output()
	s2 := string(out2)
	if strings.Contains(s2, "state = running") || strings.Contains(s2, "last exit code = 0") {
		r.pass(sec, "sysctl-tuning daemon present (network tuning persists across reboots)", "")
	} else {
		r.warn(sec, "sysctl-tuning daemon not found — network tuning will not survive reboot",
			"Fix: sudo launchctl bootstrap system /Library/LaunchDaemons/com.llm-server.sysctl-tuning.plist")
	}

	// Spotlight on boot volume
	mdOut, _ := exec.Command("mdutil", "-s", "/").Output()
	mdStr := strings.ToLower(string(mdOut))
	if strings.Contains(mdStr, "disabled") || strings.Contains(mdStr, "off") {
		r.pass(sec, "Spotlight indexing disabled on /", "")
	} else {
		r.warn(sec, "Spotlight indexing may be active", "Fix: sudo mdutil -a -i off")
	}

	// SSH
	ln, err := net.Listen("tcp", "127.0.0.1:22")
	if err != nil {
		// Port 22 is in use — SSH is listening
		r.pass(sec, "SSH enabled (port 22 listening)", "")
	} else {
		ln.Close()
		r.warn(sec, "SSH not enabled",
			"Fix (macOS 26+): sudo launchctl enable system/com.openssh.sshd && sudo launchctl kickstart -k system/com.openssh.sshd")
	}

	// sshd drop-in
	dropIn := "/etc/ssh/sshd_config.d/100-headless.conf"
	if _, err := os.Stat(dropIn); err == nil {
		r.pass(sec, "sshd drop-in present ("+dropIn+")", "")
		if data, readErr := os.ReadFile(dropIn); readErr == nil &&
			strings.Contains(string(data), "PasswordAuthentication no") {
			r.pass(sec, "sshd: PasswordAuthentication no (key-only login enforced)", "")
		} else {
			r.warn(sec, "sshd: PasswordAuthentication no not set — password login still permitted",
				"Copy your public key to ~/.ssh/authorized_keys then re-run setup")
		}
	} else {
		r.warn(sec, "sshd drop-in not found — SSH hardening not applied",
			"Fix: sudo headless-macs (run System Baseline)")
	}

	// _llmserver service account
	if exec.Command("id", "_llmserver").Run() == nil {
		r.pass(sec, "_llmserver service account exists (serving daemons run unprivileged)", "")
	} else {
		r.warn(sec, "_llmserver service account missing — serving daemons will run as root",
			"Fix: sudo headless-macs (run Install Tools)")
	}

	// MacBook clamshell reminder
	if strings.Contains(strings.ToLower(sysctl("hw.model")), "macbook") {
		r.warn(sec, "MacBook detected — confirm HDMI dummy plug is connected for headless operation", "")
	}

	_ = sipEnabled // used in log header
}

// ---------------------------------------------------------------------------
// NETWORK
// ---------------------------------------------------------------------------

func (r *VerifyResult) sectionNetwork(cfg *config.Config) {
	sec := "NETWORK"

	if cfg.System.NetworkTuning {
		r.checkSysctl(sec, "net.inet.tcp.sendspace", "1048576")
		r.checkSysctl(sec, "net.inet.tcp.recvspace", "1048576")
		r.checkSysctl(sec, "kern.ipc.maxsockbuf", "8388608")
		r.checkSysctl(sec, "net.inet.tcp.autorcvbufmax", "8388608")
		r.checkSysctl(sec, "net.inet.tcp.autosndbufmax", "8388608")
		r.checkSysctl(sec, "kern.ipc.somaxconn", "2048")
	} else {
		r.skip(sec, "Network tuning disabled in config")
	}

	// maxfiles
	out, _ := exec.Command("launchctl", "limit", "maxfiles").Output()
	fields := strings.Fields(string(out))
	softLimit := 0
	if len(fields) >= 2 {
		softLimit, _ = strconv.Atoi(fields[1])
	}
	if softLimit >= 524288 {
		r.pass(sec, fmt.Sprintf("maxfiles soft limit: %d", softLimit), "")
	} else {
		r.warn(sec,
			fmt.Sprintf("maxfiles soft limit low (%d) — may exhaust under parallel inference", softLimit),
			"Fix: sudo launchctl bootstrap system /Library/LaunchDaemons/com.llm-server.maxfiles.plist")
	}
}

// ---------------------------------------------------------------------------
// STORAGE
// ---------------------------------------------------------------------------

func (r *VerifyResult) sectionStorage(cfg *config.Config) {
	sec := "STORAGE"

	if !cfg.Storage.UseExternalVolume {
		r.skip(sec, "External storage disabled in config")
		return
	}

	label := cfg.Storage.VolumeLabel
	mountPoint := cfg.Storage.VolumeMountPoint
	if mountPoint == "" {
		mountPoint = "/Volumes/" + label
	}
	symlinkInternal := cfg.Storage.SymlinkInternalPaths

	// Volume mounted?
	mountOut, _ := exec.Command("mount").Output()
	if strings.Contains(string(mountOut), " on "+mountPoint+" ") {
		r.pass(sec, "External volume mounted at "+mountPoint, "")
	} else {
		r.fail(sec, "External volume not mounted at "+mountPoint,
			"Fix: sudo diskutil mount -mountPoint '"+mountPoint+"' '"+label+"'")
	}

	// Ownership enabled
	infoOut, _ := exec.Command("diskutil", "info", label).Output()
	if strings.Contains(string(infoOut), "Owners") {
		owners := extractDiskutilField(string(infoOut), "Owners")
		if strings.EqualFold(owners, "Enabled") {
			r.pass(sec, "External volume ownership enabled", "")
		} else {
			r.fail(sec, "External volume ownership disabled",
				"Fix: sudo diskutil enableOwnership '"+mountPoint+"'")
		}
	}

	// storage-mount daemon
	dmOut, _ := exec.Command("launchctl", "print", "system/com.llm-server.storage-mount").Output()
	if strings.Contains(string(dmOut), "state = running") || strings.Contains(string(dmOut), "last exit") {
		r.pass(sec, "storage-mount LaunchDaemon installed", "")
	} else {
		r.fail(sec, "storage-mount LaunchDaemon missing", "Fix: sudo headless-macs (run Storage Setup)")
	}

	// Symlinks
	if symlinkInternal {
		symlink := "/Library/Ollama/models"
		if fi, err := os.Lstat(symlink); err == nil && fi.Mode()&os.ModeSymlink != 0 {
			target, _ := os.Readlink(symlink)
			r.pass(sec, symlink+" symlink present", "Target: "+target)
		} else {
			r.fail(sec, symlink+" is not symlinked to external storage",
				"Fix: sudo headless-macs (run Storage Setup)")
		}
	} else {
		r.skip(sec, "Internal /Library symlink wiring disabled in config")
	}
}

// ---------------------------------------------------------------------------
// OLLAMA
// ---------------------------------------------------------------------------

func (r *VerifyResult) sectionOllama(cfg *config.Config) {
	sec := "OLLAMA"

	if !cfg.Tools.Ollama.Enabled {
		r.skip(sec, "Not enabled in config")
		return
	}

	r.checkDaemon(sec, "com.ollama.server")
	r.checkHTTP(sec, "Ollama", "http://localhost:11434/api/tags", 5)

	// Model count
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("http://localhost:11434/api/tags")
	if err == nil {
		defer resp.Body.Close()
		var body struct {
			Models []json.RawMessage `json:"models"`
		}
		if json.NewDecoder(resp.Body).Decode(&body) == nil {
			count := len(body.Models)
			if count > 0 {
				r.pass(sec, fmt.Sprintf("Models loaded: %d", count), "")
			} else {
				r.warn(sec, "No models pulled yet", "Run: ollama pull <model>")
			}
		}
	}
}

// ---------------------------------------------------------------------------
// RAPID-MLX
// ---------------------------------------------------------------------------

func (r *VerifyResult) sectionRapidMLX(cfg *config.Config) {
	sec := "RAPID-MLX"

	if !cfg.Tools.RapidMLX.Enabled {
		r.skip(sec, "Not enabled in config")
		return
	}

	r.checkDaemon(sec, "com.rapid-mlx.server")
	port := fmt.Sprintf("%d", cfg.Tools.RapidMLX.Port)
	if cfg.Tools.RapidMLX.Port == 0 {
		port = "8080"
	}
	r.checkHTTP(sec, "Rapid-MLX", fmt.Sprintf("http://localhost:%s/v1/models", port), 15)
}

// ---------------------------------------------------------------------------
// MLX-LM
// ---------------------------------------------------------------------------

func (r *VerifyResult) sectionMLXLM(cfg *config.Config) {
	sec := "MLX-LM"

	if !cfg.Tools.MLXLM.Enabled {
		r.skip(sec, "Not enabled in config")
		return
	}

	model := cfg.Tools.MLXLM.DefaultModel
	if model == "" {
		r.warn(sec, "No default_model set in config — daemon not started", "")
		return
	}

	port := fmt.Sprintf("%d", cfg.Tools.MLXLM.Port)
	if cfg.Tools.MLXLM.Port == 0 {
		port = "8000"
	}
	r.checkDaemon(sec, "com.mlx-lm.server")
	r.checkHTTP(sec, "mlx-lm", fmt.Sprintf("http://localhost:%s/v1/models", port), 10)
}

// ---------------------------------------------------------------------------
// INFINITY
// ---------------------------------------------------------------------------

func (r *VerifyResult) sectionInfinity(cfg *config.Config) {
	sec := "INFINITY"

	if !cfg.Tools.Infinity.Enabled {
		r.skip(sec, "Not enabled in config")
		return
	}

	port := fmt.Sprintf("%d", cfg.Tools.Infinity.Port)
	if cfg.Tools.Infinity.Port == 0 {
		port = "7997"
	}
	r.checkDaemon(sec, "com.infinity.server")
	r.checkHTTP(sec, "Infinity", fmt.Sprintf("http://localhost:%s/health", port), 10)
}

// ---------------------------------------------------------------------------
// EXO
// ---------------------------------------------------------------------------

func (r *VerifyResult) sectionExo(cfg *config.Config) {
	sec := "EXO"

	if !cfg.Tools.Exo.Enabled {
		r.skip(sec, "Not enabled in config")
		return
	}

	uid := sudoUID()
	out, _ := exec.Command("launchctl", "print", "gui/"+uid+"/com.exo.node").Output()
	if strings.Contains(string(out), "state = running") {
		r.pass(sec, "com.exo.node running", "")
	} else {
		r.fail(sec, "com.exo.node not running", "")
	}

	port := fmt.Sprintf("%d", cfg.Tools.Exo.ChatGPTAPIPort)
	if cfg.Tools.Exo.ChatGPTAPIPort == 0 {
		port = "52415"
	}
	r.checkHTTP(sec, "Exo", fmt.Sprintf("http://localhost:%s/v1/models", port), 10)
}

// ---------------------------------------------------------------------------
// MEMORY
// ---------------------------------------------------------------------------

func (r *VerifyResult) sectionMemory() {
	sec := "MEMORY"

	out, err := exec.Command("memory_pressure").Output()
	if err != nil {
		r.warn(sec, "Could not read memory pressure", "")
		return
	}

	freePct := 0
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "System-wide memory free percentage") {
			// "System-wide memory free percentage: 42%"
			parts := strings.Split(line, ":")
			if len(parts) >= 2 {
				val := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(parts[1]), "%"))
				freePct, _ = strconv.Atoi(val)
			}
		}
	}

	switch {
	case freePct > 20:
		r.pass(sec, fmt.Sprintf("Memory pressure healthy (%d%% free)", freePct), "")
	case freePct > 10:
		r.warn(sec, fmt.Sprintf("Memory pressure elevated (%d%% free)", freePct),
			"Consider fewer loaded models")
	default:
		r.warn(sec, fmt.Sprintf("Memory pressure critical (%d%% free)", freePct),
			"Inference may degrade — unload unused models")
	}
}
