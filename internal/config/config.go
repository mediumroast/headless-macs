// Package config loads and saves headless-macs configuration.
// The active config lives at ~/.headless_macs/config.json.
// The repo config.json is the shipped template and is never modified.
package config

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
)

// Config mirrors the full config.json schema.
type Config struct {
	Tools   Tools   `json:"tools"`
	Storage Storage `json:"storage"`
	System  System  `json:"system"`
	Network Network `json:"network"`
	TUI     TUI     `json:"tui"`
	Debug   Debug   `json:"debug"`
}

// Debug holds settings for headless-macs-debug, the standalone debugging
// utility (Phase 11F). Added for its NOPASSWD sudo toggle.
type Debug struct {
	// SudoNopasswdEnabled declares intent only — the target username is
	// never stored here (prompted interactively and validated at the
	// moment the toggle is applied, not persisted), so `headless-macs
	// debug-tools` is what actually syncs /etc/sudoers.d to match this.
	SudoNopasswdEnabled bool `json:"sudo_nopasswd_enabled"`
}

// TUI holds settings for the interactive terminal UI itself, as opposed
// to anything it manages. Added for the Dashboard screen (Phase 10).
type TUI struct {
	// DashboardRefreshMs is how often the Dashboard screen re-fetches
	// daemon/hardware status, in milliseconds. 0 (unset) means the
	// built-in default (2000ms).
	DashboardRefreshMs int `json:"dashboard_refresh_ms"`
}

type Tools struct {
	Ollama   OllamaTool   `json:"ollama"`
	RapidMLX RapidMLXTool `json:"rapid_mlx"`
	MLXLM    MLXLMTool    `json:"mlx_lm"`
	Infinity InfinityTool `json:"infinity"`
	Exo      ExoTool      `json:"exo"`
	Macmon   MacmonTool   `json:"macmon"`
}

type OllamaTool struct {
	Enabled        bool   `json:"enabled"`
	Host           string `json:"host"`
	ModelsDir      string `json:"models_dir"`
	KeepAlive      int    `json:"keep_alive"`
	FlashAttention bool   `json:"flash_attention"`
	GPUPercent     int    `json:"gpu_percent"`
	LogLevel       string `json:"log_level"`
}

type RapidMLXTool struct {
	Enabled       bool     `json:"enabled"`
	Host          string   `json:"host"`
	Port          int      `json:"port"`
	Model         string   `json:"model"`
	CacheDir      string   `json:"cache_dir"`
	PrefillStep   int      `json:"prefill_step_size"`
	NoThinking    bool     `json:"no_thinking"`
	Extras        []string `json:"extras"`
}

type MLXLMTool struct {
	Enabled      bool   `json:"enabled"`
	Host         string `json:"host"`
	Port         int    `json:"port"`
	ModelPath    string `json:"model_path"`
	DefaultModel string `json:"default_model"`
}

type InfinityTool struct {
	Enabled bool   `json:"enabled"`
	Host    string `json:"host"`
	Port    int    `json:"port"`
	Model   string `json:"model"`
	Engine  string `json:"engine"`
}

type ExoTool struct {
	Enabled bool `json:"enabled"`
	// ChatGPTAPIPort maps to exo's real --api-port flag (the JSON key name
	// is kept for config compatibility — it's still the port serving exo's
	// OpenAI/ChatGPT-compatible API).
	ChatGPTAPIPort int `json:"chatgpt_api_port"`
	// BootstrapPeers maps to exo's --bootstrap-peers flag (comma-separated
	// libp2p multiaddrs of other nodes to dial on startup). Empty means a
	// single-node exo instance. Replaces the former discovery_module field,
	// which mapped to a --discovery-module flag that does not exist in any
	// current exo release — see PHASE_7_PLAN.md history.
	BootstrapPeers []string `json:"bootstrap_peers"`
}

// MacmonTool configures the macmon hardware telemetry daemon
// (com.llm-server.macmon) — CPU/GPU/ANE power, temperature, and memory
// stats over HTTP. Not a serving/inference tool; opt-in, disabled by
// default. See PHASE_9_PLAN.md.
type MacmonTool struct {
	Enabled    bool `json:"enabled"`
	Port       int  `json:"port"`
	IntervalMs int  `json:"interval_ms"`
}

type Storage struct {
	UseExternalVolume  bool   `json:"use_external_volume"`
	VolumeLabel        string `json:"volume_label"`
	VolumeMountPoint   string `json:"volume_mount_point"`
	ModelsSubdir       string `json:"models_subdir"`
	AutoDetectVolume   bool   `json:"auto_detect_volume"`
	MinFreeGB          int    `json:"min_free_gb"`
	SymlinkInternalPaths bool `json:"symlink_internal_paths"`
}

type System struct {
	DisableSpotlight      bool `json:"disable_spotlight"`
	DisableSoftwareUpdate bool `json:"disable_software_update"`
	DisableTimeMachine    bool `json:"disable_time_machine"`
	DisableICloud         bool `json:"disable_icloud"`
	DisableAirdropHandoff bool `json:"disable_airdrop_handoff"`
	DisableNotifications  bool `json:"disable_notifications"`
	DisableTelemetry      bool `json:"disable_telemetry"`
	DisableSiri           bool `json:"disable_siri"`
	DisableMediaServices  bool `json:"disable_media_services"`
	NetworkTuning         bool `json:"network_tuning"`
	PowerMode             int  `json:"power_mode"`
}

type Network struct {
	LocalhostOnly   bool `json:"localhost_only"`
	DisableFirewall bool `json:"disable_firewall"`
}

// UserConfigPath returns ~/.headless_macs/config.json.
func UserConfigPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".headless_macs", "config.json")
}

// Load reads the user config. If it does not exist, returns ErrNotFound
// so the caller can trigger the first-run bootstrap.
var ErrNotFound = os.ErrNotExist

// Load reads and parses the user config file.
func Load() (*Config, error) {
	return loadFrom(UserConfigPath())
}

// Bootstrap copies the template config to the user config location.
// templatePath is the path to the repo's config.json.
func Bootstrap(templatePath string) error {
	return bootstrapTo(templatePath, UserConfigPath())
}

func bootstrapTo(src, dest string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return err
	}
	return os.WriteFile(dest, data, 0o600)
}

func loadFrom(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, err
	}

	// Auto-migrate: a config file written before a schema addition (e.g.
	// the macmon or tui sections) unmarshals fine — Go leaves the new
	// fields at their zero value — but the file on disk stays on the old
	// shape until something else happens to save it, which may never
	// happen. Re-marshaling and comparing catches that gap immediately on
	// load rather than leaving it to an unrelated future save. Harmless
	// when the only difference is formatting (field order, whitespace) —
	// this just re-normalizes the file in that case too. See issue #13.
	//
	// Writes back to `path` specifically, not UserConfigPath() via
	// Save() — loadFrom is also used in tests with an arbitrary temp
	// path, and writing to the real user config as a side effect of
	// loading a different file would be a bug in its own right.
	if patched, err := json.MarshalIndent(&c, "", "  "); err == nil {
		if !bytes.Equal(bytes.TrimSpace(data), bytes.TrimSpace(patched)) {
			_ = saveTo(path, &c)
		}
	}

	return &c, nil
}

// Save writes the config back to the user config file atomically.
func Save(c *Config) error {
	return saveTo(UserConfigPath(), c)
}

// saveTo writes c to dest atomically (write to a .tmp file, then rename).
// Shared by Save() (always UserConfigPath()) and loadFrom()'s
// auto-migrate step (whatever path it was given, which is UserConfigPath()
// in real usage but may be a test's temp file).
func saveTo(dest string, c *Config) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := dest + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, dest)
}
