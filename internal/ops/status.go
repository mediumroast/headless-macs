package ops

// status.go — "what's running and what is it costing me" (ports the
// Phase 10 Dashboard/CLI status data layer). Read-only, no root required
// for the launchctl/ps calls this makes, though the daemons themselves
// mostly run as root or _llmserver.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/mediumroast/headless-macs/internal/config"
)

// DaemonStatus is one managed daemon's current state.
type DaemonStatus struct {
	Label      string
	Running    bool
	PID        int
	RSSBytes   int64
	CPUPercent float64
}

// HardwareSnapshot is macmon's telemetry, parsed. Nil in StatusResult when
// macmon is disabled or its endpoint didn't respond.
type HardwareSnapshot struct {
	CPUPowerW float64
	GPUPowerW float64
	SysPowerW float64
	CPUTempC  float64
	GPUTempC  float64
	RAMUsageB int64
	RAMTotalB int64
}

// StatusResult is RunStatus's output — shared by the CLI `status`
// subcommand and the TUI Dashboard, so both read the exact same data.
type StatusResult struct {
	Daemons  []DaemonStatus
	Hardware *HardwareSnapshot
}

// RunStatus reports daemon state and resource use for every daemon
// relevant to the current config: the always-on infra daemons, plus one
// entry per enabled serving tool, plus macmon when enabled.
func RunStatus(cfg *config.Config) (*StatusResult, error) {
	r := &StatusResult{}

	infra := []string{
		"com.llm-server.caffeinate",
		"com.llm-server.maxfiles",
		"com.llm-server.pmset-heal",
		"com.llm-server.logrotate",
	}
	if cfg.System.NetworkTuning {
		infra = append(infra, "com.llm-server.sysctl-tuning")
	}
	for _, label := range infra {
		r.Daemons = append(r.Daemons, daemonStatusFor("system", label))
	}

	if cfg.Tools.Ollama.Enabled {
		r.Daemons = append(r.Daemons, daemonStatusFor("system", "com.ollama.server"))
	}
	if cfg.Tools.RapidMLX.Enabled {
		r.Daemons = append(r.Daemons, daemonStatusFor("system", "com.rapid-mlx.server"))
	}
	if cfg.Tools.MLXLM.Enabled {
		r.Daemons = append(r.Daemons, daemonStatusFor("system", "com.mlx-lm.server"))
	}
	if cfg.Tools.Infinity.Enabled {
		r.Daemons = append(r.Daemons, daemonStatusFor("system", "com.infinity.server"))
	}
	if cfg.Tools.Exo.Enabled {
		// Exo's LaunchAgent lives in the console user's gui/<uid> domain,
		// not system — see installExo() in tools.go.
		r.Daemons = append(r.Daemons, daemonStatusFor("gui/"+sudoUID(), "com.exo.node"))
	}
	if cfg.Tools.Macmon.Enabled {
		r.Daemons = append(r.Daemons, daemonStatusFor("system", "com.llm-server.macmon"))
		r.Hardware = fetchMacmonHardware(cfg.Tools.Macmon.Port)
	}

	return r, nil
}

func daemonStatusFor(domain, label string) DaemonStatus {
	running, pid := daemonStateInDomain(domain, label)
	ds := DaemonStatus{Label: label, Running: running, PID: pid}
	if running && pid > 0 {
		ds.RSSBytes, ds.CPUPercent = psStats(pid)
	}
	return ds
}

// psStats returns RSS (bytes) and CPU% for a PID, or zeros if `ps` fails
// (e.g. the process exited between the launchctl check and this call).
func psStats(pid int) (rssBytes int64, cpuPercent float64) {
	out, err := exec.Command("ps", "-o", "rss=,pcpu=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return 0, 0
	}
	fields := strings.Fields(string(out))
	if len(fields) != 2 {
		return 0, 0
	}
	rssKB, _ := strconv.ParseInt(fields[0], 10, 64)
	cpuPercent, _ = strconv.ParseFloat(fields[1], 64)
	return rssKB * 1024, cpuPercent
}

// fetchMacmonHardware reads macmon's /json endpoint directly (not through
// verify.go's checkHTTP — that's a pass/fail check, this needs the parsed
// values). Returns nil on any failure so a temporarily-unreachable macmon
// degrades the Dashboard's hardware panel, not the whole status call.
func fetchMacmonHardware(port int) *HardwareSnapshot {
	if port == 0 {
		port = 9090
	}
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/json", port))
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	var raw struct {
		CPUPower float64 `json:"cpu_power"`
		GPUPower float64 `json:"gpu_power"`
		SysPower float64 `json:"sys_power"`
		Temp     struct {
			CPUTempAvg float64 `json:"cpu_temp_avg"`
			GPUTempAvg float64 `json:"gpu_temp_avg"`
		} `json:"temp"`
		Memory struct {
			RAMUsage int64 `json:"ram_usage"`
			RAMTotal int64 `json:"ram_total"`
		} `json:"memory"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil
	}
	return &HardwareSnapshot{
		CPUPowerW: raw.CPUPower,
		GPUPowerW: raw.GPUPower,
		SysPowerW: raw.SysPower,
		CPUTempC:  raw.Temp.CPUTempAvg,
		GPUTempC:  raw.Temp.GPUTempAvg,
		RAMUsageB: raw.Memory.RAMUsage,
		RAMTotalB: raw.Memory.RAMTotal,
	}
}
