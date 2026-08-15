package ops

// tools.go — serving tool installation (ports install-tools.sh).
//
// Installs and configures Ollama, Rapid-MLX, mlx-lm, Infinity, and Exo as
// LaunchDaemons (or LaunchAgent for Exo). Each tool is gated by its enabled
// flag in config. Must run as root.

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/mediumroast/headless-macs/internal/config"
	ilog "github.com/mediumroast/headless-macs/internal/log"
)

// ToolsResult carries the outcome of RunTools — same action model as Baseline/Storage.
type ToolsResult struct {
	Actions  []BaselineAction
	Sets     int
	Skips    int
	Warnings int
	Failures int
	LogPath  string
}

func (r *ToolsResult) add(section string, status ActionStatus, msg, detail string) {
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

// RunTools installs the serving stack. Must run as root.
func RunTools(cfg *config.Config) (*ToolsResult, error) {
	if os.Getuid() != 0 {
		return nil, fmt.Errorf("tool installation requires root — run as: sudo headless-macs")
	}

	r := &ToolsResult{}

	logPath, err := ilog.Init("install-tools")
	if err != nil {
		logPath = "/tmp/headless-macs-tools.log"
	}
	r.LogPath = logPath

	ilog.Info(fmt.Sprintf("headless-macs 2.0.0 — tool installation started at %s", time.Now().Format(time.RFC1123)))
	ilog.Info(fmt.Sprintf("Hardware: %s | %dGB RAM | macOS %s | arch: %s",
		sysctl("hw.model"),
		sysctlInt("hw.memsize")/1024/1024/1024,
		run("sw_vers", "-productVersion"),
		runtime.GOARCH,
	))

	localhostOnly := cfg.Network.LocalhostOnly
	if localhostOnly {
		r.add("CONFIG", ActionInfo, "localhost_only=true — all services bind 127.0.0.1", "")
	} else {
		r.add("CONFIG", ActionInfo, "Remote access enabled — all services bind 0.0.0.0", "")
	}

	r.ensureLLMServerUser()

	if cfg.Tools.Ollama.Enabled {
		r.installOllama(cfg, localhostOnly)
	} else {
		r.add("OLLAMA", ActionSkip, "Ollama disabled in config", "")
	}

	if cfg.Tools.RapidMLX.Enabled {
		r.installRapidMLX(cfg, localhostOnly)
	} else {
		r.add("RAPID-MLX", ActionSkip, "Rapid-MLX disabled in config", "")
	}

	if cfg.Tools.MLXLM.Enabled {
		r.installMLXLM(cfg, localhostOnly)
	} else {
		r.add("MLX-LM", ActionSkip, "mlx-lm disabled in config", "")
	}

	if cfg.Tools.Infinity.Enabled {
		r.installInfinity(cfg, localhostOnly)
	} else {
		r.add("INFINITY", ActionSkip, "Infinity disabled in config", "")
	}

	if cfg.Tools.Exo.Enabled {
		r.installExo(cfg)
	} else {
		r.add("EXO", ActionSkip, "Exo disabled in config", "")
	}

	ilog.Info(fmt.Sprintf("Log written to: %s", logPath))
	return r, nil
}

// ---------------------------------------------------------------------------
// Service account
// ---------------------------------------------------------------------------

const (
	llmserverUser = "_llmserver"
	llmserverHome = "/Library/LLMServer"
)

func (r *ToolsResult) ensureLLMServerUser() {
	section := "SERVICE-ACCOUNT"

	// --- Group ---
	gidOut, _ := exec.Command("dscl", ".", "-read", "/Groups/"+llmserverUser, "PrimaryGroupID").Output()
	gid := extractDSCLValue(string(gidOut))
	if gid != "" {
		r.add(section, ActionSkip, fmt.Sprintf("%s group already exists (GID %s)", llmserverUser, gid), "")
	} else {
		// Remove broken record if present
		if exec.Command("dscl", ".", "-list", "/Groups/"+llmserverUser).Run() == nil {
			r.add(section, ActionInfo, "Removing broken "+llmserverUser+" group (no valid GID)", "")
			_ = exec.Command("dscl", ".", "-delete", "/Groups/"+llmserverUser).Run()
		}
		id := findFreeID()
		if id == "" {
			r.add(section, ActionFail, "No available ID in range 400-499 for "+llmserverUser, "")
			return
		}
		gid = id
		_ = exec.Command("dscl", ".", "-create", "/Groups/"+llmserverUser).Run()
		_ = exec.Command("dscl", ".", "-create", "/Groups/"+llmserverUser, "PrimaryGroupID", gid).Run()
		_ = exec.Command("dscl", ".", "-create", "/Groups/"+llmserverUser, "RealName", "LLM Server").Run()
		_ = exec.Command("dscl", ".", "-create", "/Groups/"+llmserverUser, "Password", "*").Run()
		r.add(section, ActionSet, fmt.Sprintf("%s group created (GID %s)", llmserverUser, gid), "")
	}

	// --- User ---
	uidOut, _ := exec.Command("dscl", ".", "-read", "/Users/"+llmserverUser, "UniqueID").Output()
	uid := extractDSCLValue(string(uidOut))
	if uid != "" {
		r.add(section, ActionSkip, fmt.Sprintf("%s user already exists (UID %s)", llmserverUser, uid), "")
	} else {
		if exec.Command("dscl", ".", "-list", "/Users/"+llmserverUser).Run() == nil {
			r.add(section, ActionInfo, "Removing broken "+llmserverUser+" user (no valid UID)", "")
			_ = exec.Command("dscl", ".", "-delete", "/Users/"+llmserverUser).Run()
		}
		if uid == "" {
			uid = gid
		}
		_ = exec.Command("dscl", ".", "-create", "/Users/"+llmserverUser).Run()
		_ = exec.Command("dscl", ".", "-create", "/Users/"+llmserverUser, "UserShell", "/usr/bin/false").Run()
		_ = exec.Command("dscl", ".", "-create", "/Users/"+llmserverUser, "RealName", "LLM Server").Run()
		_ = exec.Command("dscl", ".", "-create", "/Users/"+llmserverUser, "UniqueID", uid).Run()
		_ = exec.Command("dscl", ".", "-create", "/Users/"+llmserverUser, "PrimaryGroupID", gid).Run()
		_ = exec.Command("dscl", ".", "-create", "/Users/"+llmserverUser, "NFSHomeDirectory", llmserverHome).Run()
		_ = exec.Command("dscl", ".", "-create", "/Users/"+llmserverUser, "Password", "*").Run()
		_ = exec.Command("dscl", ".", "-create", "/Users/"+llmserverUser, "IsHidden", "1").Run()
		_ = exec.Command("dscacheutil", "-flushcache").Run()
		r.add(section, ActionSet, fmt.Sprintf("%s user created (UID %s)", llmserverUser, uid), "")
	}

	// Home directory
	if err := os.MkdirAll(llmserverHome, 0750); err != nil {
		r.add(section, ActionWarn, "Could not create "+llmserverHome+": "+err.Error(), "")
	} else {
		_ = exec.Command("chown", llmserverUser+":"+llmserverUser, llmserverHome).Run()
		_ = exec.Command("chmod", "750", llmserverHome).Run()
		r.add(section, ActionInfo, llmserverHome+" owned by "+llmserverUser, "")
	}
}

// findFreeID finds a free UID/GID in range 400-499.
func findFreeID() string {
	usedOut, _ := exec.Command("dscl", ".", "-list", "/Groups", "PrimaryGroupID").Output()
	usedOut2, _ := exec.Command("dscl", ".", "-list", "/Users", "UniqueID").Output()
	used := make(map[string]bool)
	for _, line := range strings.Split(string(usedOut)+"\n"+string(usedOut2), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 {
			used[fields[1]] = true
		}
	}
	for i := 400; i <= 499; i++ {
		id := fmt.Sprintf("%d", i)
		if !used[id] {
			return id
		}
	}
	return ""
}

// extractDSCLValue pulls the value after ": " from dscl -read output.
func extractDSCLValue(out string) string {
	for _, line := range strings.Split(out, "\n") {
		if idx := strings.Index(line, ": "); idx >= 0 {
			v := strings.TrimSpace(line[idx+2:])
			if v != "" && v != "(null)" {
				return v
			}
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// Ollama
// ---------------------------------------------------------------------------

func (r *ToolsResult) installOllama(cfg *config.Config, localhostOnly bool) {
	section := "OLLAMA"

	// Install binary
	ollamaBin := "/usr/local/bin/ollama"
	if path, err := exec.LookPath("ollama"); err == nil {
		ver, _ := exec.Command("ollama", "--version").Output()
		r.add(section, ActionSkip, "Ollama already installed: "+strings.TrimSpace(string(ver)), "")
		ollamaBin = path
	} else {
		r.add(section, ActionInfo, "Downloading and installing Ollama…", "")
		cmd := exec.Command("/bin/sh", "-c", "curl -fsSL https://ollama.com/install.sh | sh")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			r.add(section, ActionFail, "Ollama install failed: "+err.Error(), "")
			return
		}
		if p, err := exec.LookPath("ollama"); err == nil {
			ollamaBin = p
		}
		r.add(section, ActionSet, "Ollama installed", "")
	}

	// Verify ARM64
	if fileOut, err := exec.Command("file", ollamaBin).Output(); err == nil {
		s := string(fileOut)
		if strings.Contains(s, "arm64") || strings.Contains(s, "universal") {
			r.add(section, ActionInfo, ollamaBin+" is ARM64-compatible", "")
		} else {
			r.add(section, ActionWarn, "Could not verify ARM64 support for "+ollamaBin, "")
		}
	}

	// Kill competing instances
	r.killOllamaConflicts(section)

	// Wait for port 11434 to clear
	r.waitForPort(section, 11434)

	// Models directory
	modelsDir := cfg.Tools.Ollama.ModelsDir
	if modelsDir == "" {
		modelsDir = "/Library/Ollama/models"
	}
	_ = os.MkdirAll(modelsDir, 0755)
	_ = exec.Command("chown", "-R", llmserverUser+":"+llmserverUser, filepath.Dir(modelsDir)).Run()
	_ = exec.Command("mdutil", "-i", "off", modelsDir).Run()
	_ = exec.Command("mdutil", "-E", modelsDir).Run()
	r.add(section, ActionInfo, "Models dir: "+modelsDir+" (Spotlight excluded)", "")

	// Log directory
	_ = os.MkdirAll("/var/log/ollama", 0755)
	_ = exec.Command("chown", llmserverUser+":"+llmserverUser, "/var/log/ollama").Run()

	// RAM-based auto-tuning
	ramGB := int(sysctlInt("hw.memsize") / 1024 / 1024 / 1024)
	maxLoaded, numPar, maxCtx := ollamaAutoTune(ramGB)

	host := cfg.Tools.Ollama.Host
	if host == "" {
		host = "0.0.0.0:11434"
	}
	if localhostOnly {
		host = "127.0.0.1:11434"
	}
	keepAlive := fmt.Sprintf("%d", cfg.Tools.Ollama.KeepAlive)
	if cfg.Tools.Ollama.KeepAlive == 0 {
		keepAlive = "-1"
	}
	gpuPct := fmt.Sprintf("%d", cfg.Tools.Ollama.GPUPercent)
	if cfg.Tools.Ollama.GPUPercent == 0 {
		gpuPct = "100"
	}
	flashAttn := "0"
	if cfg.Tools.Ollama.FlashAttention {
		flashAttn = "1"
	}

	ctxDisplay := maxCtx
	if ctxDisplay == "" {
		ctxDisplay = "model-native"
	}
	r.add(section, ActionInfo, fmt.Sprintf("Tuning: RAM=%dGB → MAX_LOADED=%d NUM_PAR=%d MAX_CTX=%s",
		ramGB, maxLoaded, numPar, ctxDisplay), "")

	plistPath := "/Library/LaunchDaemons/com.ollama.server.plist"
	plistContent := ollamaPlist(ollamaBin, host, modelsDir, llmserverHome, keepAlive,
		fmt.Sprintf("%d", numPar), fmt.Sprintf("%d", maxLoaded), maxCtx,
		flashAttn, gpuPct, llmserverUser)

	if err := os.WriteFile(plistPath, []byte(plistContent), 0644); err != nil {
		r.add(section, ActionFail, "Could not write Ollama plist: "+err.Error(), "")
		return
	}
	_ = exec.Command("chown", "root:wheel", plistPath).Run()
	_ = exec.Command("chmod", "644", plistPath).Run()
	loadDaemon(plistPath)
	r.add(section, ActionSet, "com.ollama.server installed and started", "")

	time.Sleep(3 * time.Second)
	r.checkEndpoint(section, "Ollama", "http://localhost:11434/api/tags", "models", 10)
}

func (r *ToolsResult) killOllamaConflicts(section string) {
	// Remove login item
	_ = exec.Command("osascript", "-e",
		`tell application "System Events" to delete login item "Ollama"`).Run()

	// Bootout existing system daemon
	oldPlist := "/Library/LaunchDaemons/com.ollama.server.plist"
	if _, err := os.Stat(oldPlist); err == nil {
		_ = exec.Command("launchctl", "bootout", "system", oldPlist).Run()
		time.Sleep(time.Second)
	}

	// Quit menu bar app and kill processes
	_ = exec.Command("osascript", "-e", `quit app "Ollama"`).Run()
	time.Sleep(time.Second)
	_ = exec.Command("killall", "Ollama").Run()
	_ = exec.Command("killall", "ollama").Run()

	// Remove user LaunchAgents
	uid := sudoUID()
	home := realUserHome()
	for _, agent := range []string{
		filepath.Join(home, "Library/LaunchAgents/com.ollama.ollama.plist"),
		filepath.Join(home, "Library/LaunchAgents/ollama.plist"),
	} {
		if _, err := os.Stat(agent); err == nil {
			_ = exec.Command("launchctl", "bootout", "gui/"+uid, agent).Run()
			os.Remove(agent)
			r.add(section, ActionSet, "Removed user LaunchAgent: "+agent, "")
		}
	}

	// Stop Homebrew Ollama if running
	if out, _ := exec.Command("brew", "services", "list").Output(); strings.Contains(string(out), "ollama") {
		_ = exec.Command("brew", "services", "stop", "ollama").Run()
		r.add(section, ActionSet, "Stopped Homebrew Ollama service", "")
	}
}

func (r *ToolsResult) waitForPort(section string, port int) {
	for i := 1; i <= 10; i++ {
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err == nil {
			ln.Close()
			return
		}
		r.add(section, ActionInfo, fmt.Sprintf("Waiting for port %d to clear… (%d/10)", port, i), "")
		time.Sleep(2 * time.Second)
	}
	// Final check
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		r.add(section, ActionWarn, fmt.Sprintf("Port %d still in use — daemon may fail to bind", port), "")
	} else {
		ln.Close()
	}
}

func ollamaAutoTune(ramGB int) (maxLoaded, numPar int, maxCtx string) {
	switch {
	case ramGB <= 16:
		return 1, 1, "8192"
	case ramGB <= 24:
		return 2, 2, "16384"
	case ramGB <= 32:
		return 2, 3, "32768"
	case ramGB <= 64:
		return 3, 4, "32768"
	default:
		return 4, 8, "" // native context window
	}
}

func ollamaPlist(bin, host, modelsDir, home, keepAlive, numPar, maxLoaded, maxCtx, flashAttn, gpuPct, user string) string {
	maxCtxKey := ""
	if maxCtx != "" {
		maxCtxKey = fmt.Sprintf("    <key>OLLAMA_MAX_CONTEXT</key><string>%s</string>\n", maxCtx)
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>com.ollama.server</string>
  <key>ProgramArguments</key>
  <array>
    <string>%s</string>
    <string>serve</string>
  </array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>StandardOutPath</key><string>/var/log/ollama/stdout.log</string>
  <key>StandardErrorPath</key><string>/var/log/ollama/stderr.log</string>
  <key>EnvironmentVariables</key>
  <dict>
    <key>HOME</key><string>%s</string>
    <key>PATH</key><string>/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin</string>
    <key>OLLAMA_MODELS</key><string>%s</string>
    <key>OLLAMA_HOST</key><string>%s</string>
    <key>OLLAMA_KEEP_ALIVE</key><string>%s</string>
    <key>OLLAMA_NUM_PARALLEL</key><string>%s</string>
    <key>OLLAMA_MAX_LOADED_MODELS</key><string>%s</string>
%s    <key>OLLAMA_FLASH_ATTENTION</key><string>%s</string>
    <key>OLLAMA_NUM_GPU</key><string>1</string>
    <key>OLLAMA_GPU_PERCENT</key><string>%s</string>
    <key>OLLAMA_ORIGINS</key><string>*</string>
  </dict>
  <key>WorkingDirectory</key><string>/tmp</string>
  <key>UserName</key><string>%s</string>
</dict>
</plist>
`, bin, home, modelsDir, host, keepAlive, numPar, maxLoaded, maxCtxKey, flashAttn, gpuPct, user)
}

// ---------------------------------------------------------------------------
// Rapid-MLX
// ---------------------------------------------------------------------------

func (r *ToolsResult) installRapidMLX(cfg *config.Config, localhostOnly bool) {
	section := "RAPID-MLX"

	if path, err := exec.LookPath("rapid-mlx"); err == nil {
		ver, _ := exec.Command("rapid-mlx", "--version").Output()
		r.add(section, ActionSkip, "Rapid-MLX already installed: "+strings.TrimSpace(string(ver)), "")
		_ = path
	} else {
		r.add(section, ActionInfo, "Installing Rapid-MLX via Homebrew…", "")
		installed := false
		if exec.Command("brew", "install", "raullenchai/rapid-mlx/rapid-mlx").Run() == nil {
			r.add(section, ActionSet, "Rapid-MLX installed via Homebrew", "")
			installed = true
		}
		if !installed {
			r.add(section, ActionInfo, "Homebrew install failed — trying pip3…", "")
			if exec.Command("pip3", "install", "rapid-mlx", "--break-system-packages").Run() == nil {
				r.add(section, ActionSet, "Rapid-MLX installed via pip3", "")
			} else {
				r.add(section, ActionFail, "Could not install Rapid-MLX via Homebrew or pip3", "")
				return
			}
		}
	}

	// Extras
	for _, extra := range cfg.Tools.RapidMLX.Extras {
		if extra == "" {
			continue
		}
		pkg := fmt.Sprintf("rapid-mlx[%s]", extra)
		if exec.Command("pip3", "install", pkg, "--break-system-packages").Run() == nil {
			r.add(section, ActionSet, pkg+" installed", "")
		} else {
			r.add(section, ActionWarn, "Could not install "+pkg, "")
		}
	}

	// Self-diagnostic
	if exec.Command("rapid-mlx", "doctor").Run() != nil {
		r.add(section, ActionWarn, "rapid-mlx doctor reported issues — check logs", "")
	}

	// Cache directory — config takes precedence, then external volume, then default
	cacheDir := cfg.Tools.RapidMLX.CacheDir
	if cacheDir == "" {
		cacheDir = "/Library/RapidMLX/cache"
		if data, err := os.ReadFile("/tmp/mac-llm-precheck.json"); err == nil {
			var pc map[string]interface{}
			if json.Unmarshal(data, &pc) == nil {
				if st, ok := pc["storage"].(map[string]interface{}); ok {
					if configured, _ := st["volume_configured"].(bool); configured {
						if root, _ := st["model_root"].(string); root != "" {
							cacheDir = filepath.Join(root, "rapid-mlx")
						}
					}
				}
			}
		}
	}
	_ = os.MkdirAll(cacheDir, 0755)
	_ = exec.Command("chown", "-R", llmserverUser+":"+llmserverUser, cacheDir).Run()
	_ = exec.Command("mdutil", "-i", "off", cacheDir).Run()
	r.add(section, ActionInfo, "Rapid-MLX cache: "+cacheDir, "")

	_ = os.MkdirAll("/var/log/rapid-mlx", 0755)
	_ = exec.Command("chown", llmserverUser+":"+llmserverUser, "/var/log/rapid-mlx").Run()

	rmlxBin := "/opt/homebrew/bin/rapid-mlx"
	if p, err := exec.LookPath("rapid-mlx"); err == nil {
		rmlxBin = p
	}

	host := cfg.Tools.RapidMLX.Host
	if host == "" {
		host = "0.0.0.0"
	}
	if localhostOnly {
		host = "127.0.0.1"
	}
	port := fmt.Sprintf("%d", cfg.Tools.RapidMLX.Port)
	if cfg.Tools.RapidMLX.Port == 0 {
		port = "8080"
	}
	model := cfg.Tools.RapidMLX.Model
	prefill := fmt.Sprintf("%d", cfg.Tools.RapidMLX.PrefillStep)
	if cfg.Tools.RapidMLX.PrefillStep == 0 {
		prefill = "512"
	}

	plistPath := "/Library/LaunchDaemons/com.rapid-mlx.server.plist"
	plistContent := rapidMLXPlist(rmlxBin, host, port, model, prefill, cfg.Tools.RapidMLX.NoThinking,
		cacheDir, llmserverHome, llmserverUser)
	if err := os.WriteFile(plistPath, []byte(plistContent), 0644); err != nil {
		r.add(section, ActionFail, "Could not write Rapid-MLX plist: "+err.Error(), "")
		return
	}
	_ = exec.Command("chown", "root:wheel", plistPath).Run()
	_ = exec.Command("chmod", "644", plistPath).Run()
	loadDaemon(plistPath)
	r.add(section, ActionSet, "com.rapid-mlx.server installed and started", "")

	time.Sleep(5 * time.Second)
	r.add(section, ActionInfo,
		fmt.Sprintf("First start downloads model %q if not cached — API unavailable until done", model),
		"Monitor: tail -f /var/log/rapid-mlx/stdout.log")
	r.checkEndpoint(section, "com.rapid-mlx.server",
		fmt.Sprintf("http://localhost:%s/v1/models", port), ".", 10)
}

func rapidMLXPlist(bin, host, port, model, prefill string, noThinking bool, cache, home, user string) string {
	noThinkArg := ""
	if noThinking {
		noThinkArg = "    <string>--no-thinking</string>\n"
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>com.rapid-mlx.server</string>
  <key>ProgramArguments</key>
  <array>
    <string>%s</string>
    <string>serve</string>
    <string>%s</string>
    <string>--host</string><string>%s</string>
    <string>--port</string><string>%s</string>
    <string>--prefill-step-size</string><string>%s</string>
%s  </array>
  <key>EnvironmentVariables</key>
  <dict>
    <key>HOME</key><string>%s</string>
    <key>PATH</key><string>/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin</string>
    <key>HF_HOME</key><string>%s</string>
    <key>HUGGINGFACE_HUB_CACHE</key><string>%s/hub</string>
  </dict>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>StandardOutPath</key><string>/var/log/rapid-mlx/stdout.log</string>
  <key>StandardErrorPath</key><string>/var/log/rapid-mlx/stderr.log</string>
  <key>WorkingDirectory</key><string>/tmp</string>
  <key>UserName</key><string>%s</string>
</dict>
</plist>
`, bin, model, host, port, prefill, noThinkArg, home, cache, cache, user)
}

// ---------------------------------------------------------------------------
// mlx-lm
// ---------------------------------------------------------------------------

func (r *ToolsResult) installMLXLM(cfg *config.Config, localhostOnly bool) {
	section := "MLX-LM"

	if exec.Command("python3", "-c", "import mlx_lm").Run() == nil {
		r.add(section, ActionSkip, "mlx-lm already installed", "")
	} else {
		r.add(section, ActionInfo, "Installing mlx-lm via pip3…", "")
		if exec.Command("pip3", "install", "mlx-lm", "--break-system-packages").Run() == nil {
			r.add(section, ActionSet, "mlx-lm installed", "")
		} else {
			r.add(section, ActionFail, "pip3 install mlx-lm failed", "")
			return
		}
	}

	host := cfg.Tools.MLXLM.Host
	if host == "" {
		host = "0.0.0.0"
	}
	if localhostOnly {
		host = "127.0.0.1"
	}
	port := fmt.Sprintf("%d", cfg.Tools.MLXLM.Port)
	if cfg.Tools.MLXLM.Port == 0 {
		port = "8000"
	}
	model := cfg.Tools.MLXLM.DefaultModel
	modelPath := cfg.Tools.MLXLM.ModelPath
	if modelPath == "" {
		modelPath = "/Library/MLX/models"
	}

	pyOut, _ := exec.Command("python3", "-c", "import sys; print(sys.executable)").Output()
	python := strings.TrimSpace(string(pyOut))
	if python == "" {
		python = "/usr/bin/python3"
	}

	_ = os.MkdirAll("/var/log/mlx-lm", 0755)
	_ = exec.Command("chown", llmserverUser+":"+llmserverUser, "/var/log/mlx-lm").Run()
	_ = os.MkdirAll(modelPath, 0755)
	_ = exec.Command("chown", "-R", llmserverUser+":"+llmserverUser, modelPath).Run()

	plistPath := "/Library/LaunchDaemons/com.mlx-lm.server.plist"
	plistContent := mlxLMPlist(python, host, port, model, modelPath, llmserverHome, llmserverUser)
	if err := os.WriteFile(plistPath, []byte(plistContent), 0644); err != nil {
		r.add(section, ActionFail, "Could not write mlx-lm plist: "+err.Error(), "")
		return
	}
	_ = exec.Command("chown", "root:wheel", plistPath).Run()
	_ = exec.Command("chmod", "644", plistPath).Run()

	if model != "" {
		loadDaemon(plistPath)
		r.add(section, ActionSet, "com.mlx-lm.server installed and started", "")
		time.Sleep(3 * time.Second)
		r.checkEndpoint(section, "mlx-lm",
			fmt.Sprintf("http://localhost:%s/v1/models", port), ".", 10)
	} else {
		r.add(section, ActionSkip,
			"mlx-lm plist written but NOT started (no default_model set)",
			fmt.Sprintf("Set tools.mlx_lm.default_model in config then: sudo launchctl bootstrap system %s", plistPath))
	}
}

func mlxLMPlist(python, host, port, model, modelPath, home, user string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>com.mlx-lm.server</string>
  <key>ProgramArguments</key>
  <array>
    <string>%s</string>
    <string>-m</string>
    <string>mlx_lm.server</string>
    <string>--host</string><string>%s</string>
    <string>--port</string><string>%s</string>
    <string>--model</string><string>%s</string>
  </array>
  <key>EnvironmentVariables</key>
  <dict>
    <key>HOME</key><string>%s</string>
    <key>PATH</key><string>/usr/local/bin:/usr/bin:/bin</string>
    <key>TRANSFORMERS_CACHE</key><string>%s</string>
  </dict>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>StandardOutPath</key><string>/var/log/mlx-lm/stdout.log</string>
  <key>StandardErrorPath</key><string>/var/log/mlx-lm/stderr.log</string>
  <key>WorkingDirectory</key><string>/tmp</string>
  <key>UserName</key><string>%s</string>
</dict>
</plist>
`, python, host, port, model, home, modelPath, user)
}

// ---------------------------------------------------------------------------
// Infinity
// ---------------------------------------------------------------------------

func (r *ToolsResult) installInfinity(cfg *config.Config, localhostOnly bool) {
	section := "INFINITY"

	if exec.Command("python3", "-c", "import infinity_emb").Run() == nil {
		r.add(section, ActionSkip, "Infinity already installed", "")
	} else {
		r.add(section, ActionInfo, "Installing Infinity via pip3…", "")
		if exec.Command("pip3", "install", "infinity-emb[torch,optimum]", "--break-system-packages").Run() == nil {
			r.add(section, ActionSet, "Infinity installed", "")
		} else {
			r.add(section, ActionFail, "pip3 install infinity-emb failed", "")
			return
		}
	}

	host := cfg.Tools.Infinity.Host
	if host == "" {
		host = "0.0.0.0"
	}
	if localhostOnly {
		host = "127.0.0.1"
	}
	port := fmt.Sprintf("%d", cfg.Tools.Infinity.Port)
	if cfg.Tools.Infinity.Port == 0 {
		port = "7997"
	}
	model := cfg.Tools.Infinity.Model
	engine := cfg.Tools.Infinity.Engine
	if engine == "" {
		engine = "torch"
	}

	pyOut, _ := exec.Command("python3", "-c", "import sys; print(sys.executable)").Output()
	python := strings.TrimSpace(string(pyOut))
	if python == "" {
		python = "/usr/bin/python3"
	}

	_ = os.MkdirAll("/var/log/infinity", 0755)
	_ = exec.Command("chown", llmserverUser+":"+llmserverUser, "/var/log/infinity").Run()

	plistPath := "/Library/LaunchDaemons/com.infinity.server.plist"
	plistContent := infinityPlist(python, host, port, model, engine, llmserverHome, llmserverUser)
	if err := os.WriteFile(plistPath, []byte(plistContent), 0644); err != nil {
		r.add(section, ActionFail, "Could not write Infinity plist: "+err.Error(), "")
		return
	}
	_ = exec.Command("chown", "root:wheel", plistPath).Run()
	_ = exec.Command("chmod", "644", plistPath).Run()
	loadDaemon(plistPath)
	r.add(section, ActionSet, "com.infinity.server installed and started", "")

	time.Sleep(3 * time.Second)
	r.checkEndpoint(section, "Infinity",
		fmt.Sprintf("http://localhost:%s/health", port), ".", 10)
	r.add(section, ActionInfo,
		fmt.Sprintf("Embeddings: POST http://localhost:%s/v1/embeddings", port), "")
	r.add(section, ActionInfo,
		fmt.Sprintf("Reranking:  POST http://localhost:%s/v1/rerank", port), "")
}

func infinityPlist(python, host, port, model, engine, home, user string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>com.infinity.server</string>
  <key>ProgramArguments</key>
  <array>
    <string>%s</string>
    <string>-m</string>
    <string>infinity_emb</string>
    <string>v2</string>
    <string>--host</string><string>%s</string>
    <string>--port</string><string>%s</string>
    <string>--model-id</string><string>%s</string>
    <string>--engine</string><string>%s</string>
    <string>--device</string><string>mps</string>
  </array>
  <key>EnvironmentVariables</key>
  <dict>
    <key>HOME</key><string>%s</string>
    <key>PATH</key><string>/usr/local/bin:/usr/bin:/bin</string>
  </dict>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>StandardOutPath</key><string>/var/log/infinity/stdout.log</string>
  <key>StandardErrorPath</key><string>/var/log/infinity/stderr.log</string>
  <key>WorkingDirectory</key><string>/tmp</string>
  <key>UserName</key><string>%s</string>
</dict>
</plist>
`, python, host, port, model, engine, home, user)
}

// ---------------------------------------------------------------------------
// Exo
// ---------------------------------------------------------------------------

func (r *ToolsResult) installExo(cfg *config.Config) {
	section := "EXO"

	if path, err := exec.LookPath("exo"); err == nil {
		ver, _ := exec.Command("exo", "--version").Output()
		r.add(section, ActionSkip, "Exo already installed: "+strings.TrimSpace(string(ver)), "")
		_ = path
	} else {
		r.add(section, ActionInfo, "Installing Exo…", "")
		if exec.Command("brew", "install", "exo").Run() == nil {
			r.add(section, ActionSet, "Exo installed via Homebrew", "")
		} else if exec.Command("pip3", "install", "exo", "--break-system-packages").Run() == nil {
			r.add(section, ActionSet, "Exo installed via pip3", "")
		} else {
			r.add(section, ActionFail, "Could not install Exo via Homebrew or pip3", "")
			return
		}
	}

	port := fmt.Sprintf("%d", cfg.Tools.Exo.ChatGPTAPIPort)
	if cfg.Tools.Exo.ChatGPTAPIPort == 0 {
		port = "52415"
	}
	discovery := cfg.Tools.Exo.DiscoveryModule
	if discovery == "" {
		discovery = "tailscale"
	}
	exoBin := "/opt/homebrew/bin/exo"
	if p, err := exec.LookPath("exo"); err == nil {
		exoBin = p
	}

	// Exo runs as a LaunchAgent — needs user context for peer discovery.
	// Write to the real user's LaunchAgents (not root's).
	userHome := realUserHome()
	agentDir := filepath.Join(userHome, "Library", "LaunchAgents")
	_ = os.MkdirAll(agentDir, 0755)

	plistPath := filepath.Join(agentDir, "com.exo.node.plist")
	plistContent := exoPlist(exoBin, port, discovery, userHome)
	if err := os.WriteFile(plistPath, []byte(plistContent), 0644); err != nil {
		r.add(section, ActionFail, "Could not write Exo plist: "+err.Error(), "")
		return
	}

	uid := sudoUID()
	_ = exec.Command("launchctl", "bootout", "gui/"+uid+"/com.exo.node").Run()
	if exec.Command("launchctl", "bootstrap", "gui/"+uid, plistPath).Run() == nil {
		r.add(section, ActionSet, "Exo LaunchAgent installed", "")
	} else {
		r.add(section, ActionWarn, "Could not bootstrap Exo LaunchAgent — may need interactive login", "")
	}

	hostname, _ := os.Hostname()
	r.add(section, ActionInfo,
		fmt.Sprintf("API endpoint: http://%s:%s/v1/chat/completions", hostname, port), "")
	r.add(section, ActionWarn,
		"Exo requires auto-login for true headless operation",
		"Configure: sudo sysadminctl -autologin set -userName <user> -password <pw>")
	r.add(section, ActionWarn, "For Tailscale discovery: ensure tailscaled is running on all nodes", "")
}

func exoPlist(bin, port, discovery, home string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>com.exo.node</string>
  <key>ProgramArguments</key>
  <array>
    <string>%s</string>
    <string>--chatgpt-api-port</string><string>%s</string>
    <string>--discovery-module</string><string>%s</string>
  </array>
  <key>EnvironmentVariables</key>
  <dict>
    <key>HOME</key><string>%s</string>
    <key>PATH</key><string>/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin</string>
  </dict>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>StandardOutPath</key><string>/tmp/exo-stdout.log</string>
  <key>StandardErrorPath</key><string>/tmp/exo-stderr.log</string>
</dict>
</plist>
`, bin, port, discovery, home)
}

// ---------------------------------------------------------------------------
// Shared helpers
// ---------------------------------------------------------------------------

// loadDaemon stops then starts a LaunchDaemon plist using bootstrap/bootout.
func loadDaemon(plist string) {
	_ = exec.Command("launchctl", "bootout", "system", plist).Run()
	time.Sleep(time.Second)
	_ = exec.Command("launchctl", "bootstrap", "system", plist).Run()
}

// checkEndpoint performs a simple HTTP GET and records the result.
func (r *ToolsResult) checkEndpoint(section, name, url, _ string, timeoutSecs int) {
	client := &http.Client{Timeout: time.Duration(timeoutSecs) * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		r.add(section, ActionWarn,
			fmt.Sprintf("%s API not yet responding — may still be starting", name),
			fmt.Sprintf("Check: sudo launchctl print system/%s", strings.ToLower(name)))
		return
	}
	defer resp.Body.Close()
	r.add(section, ActionSet, fmt.Sprintf("%s API responding (%s)", name, url), "")
}

// realUserHome returns the home directory of the user who invoked sudo,
// falling back to os.UserHomeDir().
func realUserHome() string {
	sudoUser := os.Getenv("SUDO_USER")
	if sudoUser != "" {
		out, err := exec.Command("dscl", ".", "-read", "/Users/"+sudoUser, "NFSHomeDirectory").Output()
		if err == nil {
			for _, line := range strings.Split(string(out), "\n") {
				if strings.Contains(line, "NFSHomeDirectory:") {
					parts := strings.SplitN(line, ": ", 2)
					if len(parts) == 2 {
						return strings.TrimSpace(parts[1])
					}
				}
			}
		}
		return "/Users/" + sudoUser
	}
	home, _ := os.UserHomeDir()
	return home
}
