// Package config loads and saves headless-macs configuration.
// The active config lives at ~/.headless_macs/config.json.
// The repo config.json is the shipped template and is never modified.
package config

import (
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
}

type Tools struct {
	Ollama   OllamaTool   `json:"ollama"`
	RapidMLX RapidMLXTool `json:"rapid_mlx"`
	MLXLM    MLXLMTool    `json:"mlx_lm"`
	Infinity InfinityTool `json:"infinity"`
	Exo      ExoTool      `json:"exo"`
}

type OllamaTool struct {
	Enabled        bool   `json:"enabled"`
	Host           string `json:"host"`
	ModelsDir      string `json:"models_dir"`
	KeepAlive      int    `json:"keep_alive"`
	FlashAttention bool   `json:"flash_attention"`
	GPUPercent     int    `json:"gpu_percent"`
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
	Enabled          bool `json:"enabled"`
	ChatGPTAPIPort   int  `json:"chatgpt_api_port"`
	DiscoveryModule  string `json:"discovery_module"`
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
	return &c, nil
}

// Save writes the config back to the user config file atomically.
func Save(c *Config) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	dest := UserConfigPath()
	tmp := dest + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, dest)
}
