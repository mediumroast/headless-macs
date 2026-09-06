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
	"io"
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

	logPath, err := ilog.InitTUI("verify")
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
	r.sectionMacmon(cfg)
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

// daemonStateInDomain parses `launchctl print <domain>/<label>` for run
// state and PID. Shared by checkDaemon/checkServiceSuppressed here and by
// internal/ops/status.go's RunStatus — one parse of launchctl's output
// format, not three. Most daemons live in the "system" domain; Exo's
// LaunchAgent lives in "gui/<uid>" instead (see daemonState below for the
// common case).
func daemonStateInDomain(domain, label string) (running bool, pid int) {
	out, _ := exec.Command("launchctl", "print", domain+"/"+label).Output()
	s := string(out)
	running = strings.Contains(s, "state = running")
	for _, line := range strings.Split(s, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 3 && fields[0] == "pid" && fields[1] == "=" {
			pid, _ = strconv.Atoi(fields[2])
			break
		}
	}
	return running, pid
}

// daemonState is daemonStateInDomain for the common "system" domain case.
func daemonState(label string) (running bool, pid int) {
	return daemonStateInDomain("system", label)
}

func (r *VerifyResult) checkDaemon(section, label string) bool {
	running, _ := daemonState(label)
	if running {
		r.pass(section, label+" running", "")
	} else {
		r.fail(section, label+" not running", "Diagnose: sudo launchctl print system/"+label)
	}
	return running
}

// checkLogLevelFlag confirms a tool's plist actually has --log-level in its
// ProgramArguments — added for mlx-lm, Infinity, and Rapid-MLX in the same
// phase that gave Ollama and Exo equivalent checks; these three had none
// (found on review, see PHASE_7_PLAN.md Phase 7I).
func (r *VerifyResult) checkLogLevelFlag(section, plistPath string) {
	data, err := os.ReadFile(plistPath)
	if err != nil {
		return // daemon not installed yet — checkDaemon already reports this
	}
	if plistHasArg(string(data), "--log-level") {
		r.pass(section, "--log-level flag present", "")
	} else {
		r.warn(section, "--log-level flag missing from plist — logging at tool default verbosity",
			"Fix: sudo headless-macs install-tools")
	}
}

// plistHasArg reports whether a <string>arg</string> element is present in
// a plist's raw XML — the ProgramArguments-array equivalent of
// extractPlistString, which only handles EnvironmentVariables' <key>/<string>
// dict pairs. Used to confirm a CLI flag (e.g. --log-level) is actually in
// the written plist, not just assumed from the generator code.
func plistHasArg(plist, arg string) bool {
	return strings.Contains(plist, "<string>"+arg+"</string>")
}

// extractPlistString pulls the value of <key>key</key><string>VALUE</string>
// from a plist's raw XML, matching the single-line format this project's
// plist generators emit. Returns "" if the key isn't present.
func extractPlistString(plist, key string) string {
	marker := "<key>" + key + "</key><string>"
	idx := strings.Index(plist, marker)
	if idx < 0 {
		return ""
	}
	rest := plist[idx+len(marker):]
	end := strings.Index(rest, "</string>")
	if end < 0 {
		return ""
	}
	return rest[:end]
}

// checkHTTP restores the original bash `check_http "name" "url" "pattern"`
// contract (documented in CLAUDE.md's verify.sh contract) that the Go port
// never carried over — it previously passed on any successful TCP
// round-trip, with no status-code or body check at all. `pattern == "."`
// means "any non-empty body" (the bash `grep .` idiom used by every
// existing install-time check in tools.go's checkEndpoint, kept consistent
// here); `pattern == ""` skips body checking; anything else must appear
// verbatim in the body. See PHASE_7_PLAN.md Phase 7I.
func (r *VerifyResult) checkHTTP(section, name, url, pattern string, timeoutSecs int) {
	client := &http.Client{Timeout: time.Duration(timeoutSecs) * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		r.fail(section, fmt.Sprintf("%s API not responding (%s)", name, url), "")
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		r.fail(section, fmt.Sprintf("%s API returned HTTP %d (%s)", name, resp.StatusCode, url), "")
		return
	}
	switch {
	case pattern == "":
		// no content check requested
	case pattern == ".":
		if len(body) == 0 {
			r.fail(section, fmt.Sprintf("%s API responded with an empty body (%s)", name, url), "")
			return
		}
	default:
		if !strings.Contains(string(body), pattern) {
			r.fail(section, fmt.Sprintf("%s API responded but body did not contain %q (%s)", name, pattern, url), "")
			return
		}
	}
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

	// SSH — check via launchctl (macOS 26 uses socket activation; port binding is unreliable)
	sshOut, _ := exec.Command("launchctl", "print", "system/com.openssh.sshd").Output()
	if strings.Contains(string(sshOut), "state = running") ||
		strings.Contains(string(sshOut), "state = waiting") {
		r.pass(sec, "SSH enabled (com.openssh.sshd running)", "")
	} else {
		r.warn(sec, "SSH not enabled",
			"Fix: sudo launchctl enable system/com.openssh.sshd && sudo launchctl kickstart -k system/com.openssh.sshd")
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

	// logrotate daemon (Phase 7) — StartCalendarInterval + RunAtLoad=false, so
	// "running" is not expected between 2 AM firings; any loaded/scheduled
	// state is a pass.
	lrOut, _ := exec.Command("launchctl", "print", "system/com.llm-server.logrotate").Output()
	lrStr := string(lrOut)
	if strings.Contains(lrStr, "state = running") || strings.Contains(lrStr, "state = waiting") ||
		strings.Contains(lrStr, "spawn scheduled") || strings.Contains(lrStr, "last exit code") {
		r.pass(sec, "logrotate daemon present (serving tool logs bounded to 100M x5)", "")
	} else {
		r.warn(sec, "logrotate daemon not found — serving tool logs may grow unbounded",
			"Fix: sudo headless-macs install-tools")
	}

	// Config content, not just daemon presence: the daemon being loaded
	// doesn't mean its config lists every path it should — a box that ran
	// install-tools before a later addition (e.g. Exo's log paths, added
	// after this config was first written) can have a "present" daemon
	// running against stale content. See PHASE_7_PLAN.md Phase 7H/7I.
	if data, err := os.ReadFile(logrotateConfigPath); err == nil {
		content := string(data)
		expectedPaths := []string{
			"/var/log/ollama/stderr.log",
			"/var/log/ollama/stdout.log",
			"/var/log/rapid-mlx/stderr.log",
			"/var/log/rapid-mlx/stdout.log",
			"/var/log/mlx-lm/stderr.log",
			"/var/log/mlx-lm/stdout.log",
			"/var/log/infinity/stderr.log",
			"/var/log/infinity/stdout.log",
			"/var/log/exo/stderr.log",
			"/var/log/exo/stdout.log",
			"/Library/Exo/exo_log/exo.log",
			"/Library/Exo/exo_log/runner_log/stdout.log",
			"/Library/Exo/exo_log/runner_log/stderr.log",
		}
		var missing []string
		for _, p := range expectedPaths {
			if !strings.Contains(content, p) {
				missing = append(missing, p)
			}
		}
		if len(missing) == 0 {
			r.pass(sec, "logrotate config covers all expected log paths", "")
		} else {
			r.warn(sec, fmt.Sprintf("logrotate config missing %d expected path(s): %s",
				len(missing), strings.Join(missing, ", ")),
				"Fix: sudo headless-macs install-tools (rewrites the config if content changed)")
		}
	}

	// Phase 8 service suppression — confirm each is actually not running.
	// Uses the same phase8Suppressions list baseline.go suppresses from and
	// restore.go re-enables from, so this check can't drift out of sync
	// with either side. [WARN], not [FAIL]: matches the [SKIP-SIP]
	// semantics from Baseline — a service SIP is preventing persistent
	// suppression for isn't this project's fault, and isn't severe enough
	// to fail Verify outright.
	for _, svc := range phase8Suppressions {
		r.checkServiceSuppressed(sec, svc.Label)
	}

	_ = sipEnabled // used in log header
}

// checkServiceSuppressed confirms a background service Baseline suppresses
// (Phase 8) is not currently running. Unlike checkDaemon, "not running" —
// including "no such service at all" — is the pass condition here.
func (r *VerifyResult) checkServiceSuppressed(section, label string) {
	running, _ := daemonState(label)
	if running {
		r.warn(section, label+" still running — Baseline suppression not applied or was reverted",
			"Fix: sudo headless-macs baseline")
		return
	}
	r.pass(section, label+" not running", "")
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
	r.checkHTTP(sec, "Ollama", "http://localhost:11434/api/tags", "models", 5)

	// Log verbosity + symlink-bypass checks (Phase 7)
	if plist, err := os.ReadFile("/Library/LaunchDaemons/com.ollama.server.plist"); err == nil {
		content := string(plist)
		if strings.Contains(content, "<key>OLLAMA_LOG_LEVEL</key>") {
			r.pass(sec, "OLLAMA_LOG_LEVEL configured", "")
		} else {
			r.warn(sec, "OLLAMA_LOG_LEVEL not set — Ollama logging at default verbosity",
				"Fix: sudo headless-macs install-tools")
		}
		if cfg.Storage.UseExternalVolume {
			if modelsDir := extractPlistString(content, "OLLAMA_MODELS"); modelsDir != "" {
				if fi, err := os.Lstat(modelsDir); err == nil && fi.Mode()&os.ModeSymlink != 0 {
					r.warn(sec, "OLLAMA_MODELS ("+modelsDir+") is a symlink — may trigger the startup traversal error",
						"Fix: sudo headless-macs install-tools (re-resolves the path)")
				} else {
					r.pass(sec, "OLLAMA_MODELS resolved to a real path ("+modelsDir+")", "")
				}
			}
		}
	}

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
	r.checkHTTP(sec, "Rapid-MLX", fmt.Sprintf("http://localhost:%s/v1/models", port), ".", 15)
	r.checkLogLevelFlag(sec, "/Library/LaunchDaemons/com.rapid-mlx.server.plist")
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
	r.checkHTTP(sec, "mlx-lm", fmt.Sprintf("http://localhost:%s/v1/models", port), ".", 10)
	r.checkLogLevelFlag(sec, "/Library/LaunchDaemons/com.mlx-lm.server.plist")
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
	r.checkHTTP(sec, "Infinity", fmt.Sprintf("http://localhost:%s/health", port), ".", 10)
	r.checkLogLevelFlag(sec, "/Library/LaunchDaemons/com.infinity.server.plist")
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
	r.checkHTTP(sec, "Exo", fmt.Sprintf("http://localhost:%s/v1/models", port), ".", 10)

	// Log location (Phase 7) — Exo used to log to /tmp/, which is lost on reboot.
	if _, err := os.Stat("/tmp/exo-stdout.log"); err == nil {
		r.warn(sec, "Exo still logging to /tmp — re-run Install Tools to move logs to /var/log/exo", "")
	} else if _, err := os.Stat("/var/log/exo"); err == nil {
		r.pass(sec, "Exo logging to /var/log/exo", "")
	}

	// Plist correctness + EXO_HOME (Phase 7 exo fixes). --chatgpt-api-port and
	// --discovery-module are not real exo flags — a plist written before this
	// fix would cause exo to reject them and fail to start.
	exoPlistPath := realUserHome() + "/Library/LaunchAgents/com.exo.node.plist"
	if data, err := os.ReadFile(exoPlistPath); err == nil {
		content := string(data)
		if strings.Contains(content, "--chatgpt-api-port") || strings.Contains(content, "--discovery-module") {
			r.fail(sec, "Exo plist has invalid flags (--chatgpt-api-port/--discovery-module do not exist in exo's CLI)",
				"Fix: sudo headless-macs install-tools")
		}
		if home := extractPlistString(content, "EXO_HOME"); home != "" {
			r.pass(sec, "EXO_HOME set to "+home, "")
		} else {
			r.warn(sec, "EXO_HOME not set — exo's own logs/state default to the hidden ~/.exo",
				"Fix: sudo headless-macs install-tools")
		}
	}
}

// ---------------------------------------------------------------------------
// MACMON
// ---------------------------------------------------------------------------

func (r *VerifyResult) sectionMacmon(cfg *config.Config) {
	sec := "MACMON"

	if !cfg.Tools.Macmon.Enabled {
		r.skip(sec, "Not enabled in config")
		return
	}

	r.checkDaemon(sec, "com.llm-server.macmon")

	port := fmt.Sprintf("%d", cfg.Tools.Macmon.Port)
	if cfg.Tools.Macmon.Port == 0 {
		port = "9090"
	}
	r.checkHTTP(sec, "macmon", fmt.Sprintf("http://127.0.0.1:%s/json", port), "cpu_power", 10)

	// Binding limitation (Phase 9) — some macmon builds have no --host flag
	// at all, so localhost_only can't be honored for this tool the way it
	// is for every other one; surface that clearly rather than letting it
	// pass silently as "exposed by config."
	if plist, err := os.ReadFile("/Library/LaunchDaemons/com.llm-server.macmon.plist"); err == nil {
		content := string(plist)
		if cfg.Network.LocalhostOnly && !plistHasArg(content, "--host") {
			r.warn(sec, "localhost_only is true but this macmon build has no --host flag — bound to all interfaces",
				"Upgrade macmon (brew upgrade macmon) and re-run install-tools")
		}
	}
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
