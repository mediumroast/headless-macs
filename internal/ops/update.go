package ops

// update.go — in-place binary upgrade for serving tools (ports update-tools.sh).
//
// Updates tool binaries without touching daemon configs or model directories.
// Stops the daemon, runs the upstream installer, removes any re-added login
// items, then re-bootstraps. Must run as root.

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/mediumroast/headless-macs/internal/config"
	ilog "github.com/mediumroast/headless-macs/internal/log"
)

// UpdateResult carries the outcome of RunUpdateTools.
type UpdateResult struct {
	Actions  []BaselineAction
	Sets     int
	Skips    int
	Warnings int
	Failures int
	LogPath  string
}

func (r *UpdateResult) add(section string, status ActionStatus, msg, detail string) {
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

// RunUpdateTools updates all enabled tool binaries in-place. Must run as root.
func RunUpdateTools(cfg *config.Config) (*UpdateResult, error) {
	if os.Getuid() != 0 {
		return nil, fmt.Errorf("update requires root — run as: sudo headless-macs")
	}

	r := &UpdateResult{}

	logPath, err := ilog.InitTUI("update-tools")
	if err != nil {
		logPath = "/tmp/headless-macs-update.log"
	}
	r.LogPath = logPath

	ilog.Info(fmt.Sprintf("headless-macs 2.0.0 — update-tools started at %s", time.Now().Format(time.RFC1123)))

	if cfg.Tools.Ollama.Enabled {
		r.updateOllama()
	} else {
		r.add("OLLAMA", ActionSkip, "Ollama not enabled in config", "")
	}

	if cfg.Tools.RapidMLX.Enabled {
		r.updateRapidMLX()
	} else {
		r.add("RAPID-MLX", ActionSkip, "Rapid-MLX not enabled in config", "")
	}

	if cfg.Tools.MLXLM.Enabled {
		r.updateMLXLM()
	} else {
		r.add("MLX-LM", ActionSkip, "mlx-lm not enabled in config", "")
	}

	if cfg.Tools.Infinity.Enabled {
		r.updateInfinity()
	} else {
		r.add("INFINITY", ActionSkip, "Infinity not enabled in config", "")
	}

	if cfg.Tools.Exo.Enabled {
		r.updateExo()
	} else {
		r.add("EXO", ActionSkip, "Exo not enabled in config", "")
	}

	ilog.Info(fmt.Sprintf("Log written to: %s", logPath))
	return r, nil
}

// ---------------------------------------------------------------------------
// Ollama
// ---------------------------------------------------------------------------

func (r *UpdateResult) updateOllama() {
	sec := "OLLAMA"
	plist := "/Library/LaunchDaemons/com.ollama.server.plist"

	if _, err := os.Stat(plist); err != nil {
		r.add(sec, ActionWarn, "Plist not found — run Install Tools first", plist)
		return
	}

	// Stop daemon
	out, _ := exec.Command("launchctl", "print", "system/com.ollama.server").Output()
	if strings.Contains(string(out), "state = running") {
		r.add(sec, ActionInfo, "Stopping com.ollama.server…", "")
		_ = exec.Command("launchctl", "bootout", "system", plist).Run()
		time.Sleep(2 * time.Second)
		r.add(sec, ActionSet, "Daemon stopped", "")
	} else {
		r.add(sec, ActionInfo, "com.ollama.server not running — continuing", "")
	}

	// Remove stale app bundle — installer tries to rm it without sudo and fails
	if _, err := os.Stat("/Applications/Ollama.app"); err == nil {
		if err := os.RemoveAll("/Applications/Ollama.app"); err != nil {
			r.add(sec, ActionWarn, "Could not remove /Applications/Ollama.app: "+err.Error(), "")
		} else {
			r.add(sec, ActionSet, "Removed /Applications/Ollama.app", "")
		}
	}

	// Run upstream installer (best-effort — always re-bootstrap regardless of result)
	r.add(sec, ActionInfo, "Running Ollama installer…", "")
	var installStderr strings.Builder
	cmd := exec.Command("/bin/sh", "-c", "curl -fsSL https://ollama.com/install.sh | sh")
	cmd.Stdout = io.Discard
	cmd.Stderr = &installStderr
	installOK := cmd.Run() == nil
	if installOK {
		r.add(sec, ActionSet, "Ollama installer complete", "")
		// Re-remove login item (installer may re-add it)
		_ = exec.Command("osascript", "-e",
			`tell application "System Events" to delete login item "Ollama"`).Run()
		_ = exec.Command("pkill", "-f", "Ollama.app").Run()
		r.add(sec, ActionSet, "Login item removed (daemon manages startup)", "")
	} else {
		errSnip := strings.TrimSpace(installStderr.String())
		if len(errSnip) > 300 {
			errSnip = errSnip[len(errSnip)-300:]
		}
		detail := "To update manually: curl -fsSL https://ollama.com/install.sh | sh"
		if errSnip != "" {
			detail = "Installer error: " + errSnip + "\n" + detail
		}
		r.add(sec, ActionWarn, "Ollama installer failed — re-bootstrapping existing binary", detail)
	}

	// Re-bootstrap regardless — daemon was stopped above and must come back up
	if exec.Command("launchctl", "bootstrap", "system", plist).Run() == nil {
		r.add(sec, ActionSet, "com.ollama.server re-bootstrapped", "")
	} else {
		r.add(sec, ActionWarn, "Could not re-bootstrap com.ollama.server",
			"Check: sudo launchctl print system/com.ollama.server")
		return
	}

	time.Sleep(3 * time.Second)

	// Verify
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get("http://localhost:11434/api/tags")
	if err == nil {
		resp.Body.Close()
		ver, _ := exec.Command("ollama", "--version").Output()
		r.add(sec, ActionSet, "Ollama API responding — version: "+strings.TrimSpace(string(ver)), "")
	} else {
		r.add(sec, ActionWarn, "Ollama API not yet responding — may still be starting",
			"Check: sudo launchctl print system/com.ollama.server\nLogs: tail -f /var/log/ollama/stderr.log")
	}
}

// ---------------------------------------------------------------------------
// Rapid-MLX
// ---------------------------------------------------------------------------

func (r *UpdateResult) updateRapidMLX() {
	sec := "RAPID-MLX"
	plist := "/Library/LaunchDaemons/com.rapid-mlx.server.plist"

	if _, err := os.Stat(plist); err != nil {
		r.add(sec, ActionWarn, "Plist not found — run Install Tools first", plist)
		return
	}

	// Stop daemon
	_ = exec.Command("launchctl", "bootout", "system", plist).Run()
	time.Sleep(time.Second)
	r.add(sec, ActionSet, "Daemon stopped", "")

	// Update via pip3 (primary install method)
	r.add(sec, ActionInfo, "Updating Rapid-MLX via pip3…", "")
	updated := false
	if exec.Command("pip3", "install", "--upgrade", "rapid-mlx", "--break-system-packages").Run() == nil {
		r.add(sec, ActionSet, "Rapid-MLX updated via pip3", "")
		updated = true
	} else if exec.Command("brew", "upgrade", "raullenchai/rapid-mlx/rapid-mlx").Run() == nil {
		r.add(sec, ActionSet, "Rapid-MLX updated via Homebrew", "")
		updated = true
	}

	if !updated {
		r.add(sec, ActionWarn, "Could not update Rapid-MLX — already at latest or install failed", "")
	}

	// Re-bootstrap
	if exec.Command("launchctl", "bootstrap", "system", plist).Run() == nil {
		r.add(sec, ActionSet, "com.rapid-mlx.server re-bootstrapped", "")
	} else {
		r.add(sec, ActionWarn, "Could not re-bootstrap com.rapid-mlx.server", "")
	}
}

// ---------------------------------------------------------------------------
// mlx-lm
// ---------------------------------------------------------------------------

func (r *UpdateResult) updateMLXLM() {
	sec := "MLX-LM"
	plist := "/Library/LaunchDaemons/com.mlx-lm.server.plist"

	if _, err := os.Stat(plist); err != nil {
		r.add(sec, ActionWarn, "Plist not found — run Install Tools first", plist)
		return
	}

	_ = exec.Command("launchctl", "bootout", "system", plist).Run()
	time.Sleep(time.Second)
	r.add(sec, ActionSet, "Daemon stopped", "")

	r.add(sec, ActionInfo, "Updating mlx-lm via pip3…", "")
	if exec.Command("pip3", "install", "--upgrade", "mlx-lm", "--break-system-packages").Run() == nil {
		r.add(sec, ActionSet, "mlx-lm updated", "")
	} else {
		r.add(sec, ActionWarn, "Could not update mlx-lm — already at latest or install failed", "")
	}

	if exec.Command("launchctl", "bootstrap", "system", plist).Run() == nil {
		r.add(sec, ActionSet, "com.mlx-lm.server re-bootstrapped", "")
	} else {
		r.add(sec, ActionWarn, "Could not re-bootstrap com.mlx-lm.server", "")
	}
}

// ---------------------------------------------------------------------------
// Infinity
// ---------------------------------------------------------------------------

func (r *UpdateResult) updateInfinity() {
	sec := "INFINITY"
	plist := "/Library/LaunchDaemons/com.infinity.server.plist"

	if _, err := os.Stat(plist); err != nil {
		r.add(sec, ActionWarn, "Plist not found — run Install Tools first", plist)
		return
	}

	_ = exec.Command("launchctl", "bootout", "system", plist).Run()
	time.Sleep(time.Second)
	r.add(sec, ActionSet, "Daemon stopped", "")

	r.add(sec, ActionInfo, "Updating Infinity via pip3…", "")
	if exec.Command("pip3", "install", "--upgrade", "infinity-emb[torch,optimum]", "--break-system-packages").Run() == nil {
		r.add(sec, ActionSet, "Infinity updated", "")
	} else {
		r.add(sec, ActionWarn, "Could not update Infinity — already at latest or install failed", "")
	}

	if exec.Command("launchctl", "bootstrap", "system", plist).Run() == nil {
		r.add(sec, ActionSet, "com.infinity.server re-bootstrapped", "")
	} else {
		r.add(sec, ActionWarn, "Could not re-bootstrap com.infinity.server", "")
	}
}

// ---------------------------------------------------------------------------
// Exo
// ---------------------------------------------------------------------------

func (r *UpdateResult) updateExo() {
	sec := "EXO"
	uid := sudoUID()
	userHome := realUserHome()
	plist := userHome + "/Library/LaunchAgents/com.exo.node.plist"

	// Stop
	_ = exec.Command("launchctl", "bootout", "gui/"+uid+"/com.exo.node").Run()
	r.add(sec, ActionSet, "Daemon stopped", "")

	r.add(sec, ActionInfo, "Updating Exo…", "")
	updated := false
	if exec.Command("brew", "upgrade", "exo").Run() == nil {
		r.add(sec, ActionSet, "Exo updated via Homebrew", "")
		updated = true
	} else if exec.Command("pip3", "install", "--upgrade", "exo", "--break-system-packages").Run() == nil {
		r.add(sec, ActionSet, "Exo updated via pip3", "")
		updated = true
	}
	if !updated {
		r.add(sec, ActionWarn, "Could not update Exo — already at latest or install failed", "")
	}

	if _, err := os.Stat(plist); err == nil {
		if exec.Command("launchctl", "bootstrap", "gui/"+uid, plist).Run() == nil {
			r.add(sec, ActionSet, "com.exo.node re-bootstrapped", "")
		} else {
			r.add(sec, ActionWarn, "Could not re-bootstrap com.exo.node", "")
		}
	} else {
		r.add(sec, ActionWarn, "Exo plist not found — run Install Tools first", plist)
	}
}
