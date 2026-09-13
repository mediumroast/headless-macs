// Package config loads and saves headless-macs configuration.
// The active config lives at SystemConfigPath (/etc/headless-macs/config.json)
// unless overridden with --config. The repo config.json is the shipped
// template and is never modified.
package config

import (
	"bytes"
	"encoding/json"
	"fmt"
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
	// Debug maps directly onto OLLAMA_DEBUG — Ollama's actual, sole
	// documented verbosity switch (docs/troubleshooting.mdx: "OLLAMA_DEBUG=1"),
	// a plain on/off. Not a level enum — Ollama doesn't have one; a prior
	// "log_level" string field here implied a multi-tier system Ollama never
	// recognized (found alongside a similarly fabricated GPUPercent/
	// OLLAMA_GPU_PERCENT, removed entirely — see PHASE_12_PLAN.md).
	Debug bool `json:"debug"`
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

// SystemConfigPath is the fixed, system-wide location of the active
// config. Deliberately not user-relative: every headless-macs invocation
// requires root (via sudo), and $HOME under sudo is governed entirely by
// that box's own sudoers configuration (env_reset's documented default
// actually initializes HOME from the *target* user, i.e. root, not the
// invoker) — not anything this project controls or can guarantee holds
// on every box. A fixed system path matches how this project already
// treats everything else it manages (/var/log/mac-llm-setup,
// /Library/LaunchDaemons, /Library/LLMServer). See PHASE_14_PLAN.md.
// var, not const, so tests can override it without touching the real
// /etc/headless-macs — real usage never assigns to it.
var SystemConfigPath = "/etc/headless-macs/config.json"

// legacyConfigPath is the old, pre-Phase-14 per-invoking-user location —
// consulted only once, by migrateOrBootstrap, to carry an existing
// installation's config forward. Not used for anything else: if
// os.UserHomeDir() happens to resolve wrong on some box, the only
// consequence is a fresh bootstrap from the template, identical to a
// brand-new install — never silent corruption or a worse outcome.
func legacyConfigPath() (string, bool) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false
	}
	return filepath.Join(home, ".headless_macs", "config.json"), true
}

// OverridePath, when set, takes precedence over SystemConfigPath —
// set by cmd/headless-macs/main.go when --config <path> is passed.
var OverridePath string

// ConfigPath returns the active config file's location: OverridePath if
// set via --config, otherwise the fixed system path. Renamed from
// UserConfigPath(), which stopped being an accurate name the moment the
// default stopped being user-relative.
func ConfigPath() string {
	if OverridePath != "" {
		return OverridePath
	}
	return SystemConfigPath
}

// Load reads the user config. If it does not exist, returns ErrNotFound
// so the caller can trigger the first-run bootstrap.
var ErrNotFound = os.ErrNotExist

// Load reads and parses the active config file.
func Load() (*Config, error) {
	return loadFrom(ConfigPath())
}

// Bootstrap copies the template config to the active config location.
// templatePath is the path to the repo's config.json. Callers should
// generally prefer MigrateOrBootstrap, which also carries an existing
// pre-Phase-14 config forward instead of starting fresh.
func Bootstrap(templatePath string) error {
	return bootstrapTo(templatePath, ConfigPath())
}

// MigrateOrBootstrap is what a first-run should actually call: if the
// active config path (ConfigPath()) already exists, it's a no-op. If not,
// and an old-style per-user config exists at legacyConfigPath(), that
// file's exact bytes are copied to the new location (left in place at the
// old path too — reversible-by-default, not deleted) rather than
// discarding an existing installation's tuned settings. Only falls back
// to Bootstrap (fresh from the template) if neither exists, or if
// OverridePath is set (an explicit --config target is never migrated
// into implicitly).
//
// The returned bool is true only for a genuine fresh-from-template
// bootstrap — never for a migration, which carries a real, already-tuned
// installation forward and must not be treated as a blank-slate first run
// (e.g. routed into the TUI's first-run onboarding screen).
func MigrateOrBootstrap(templatePath string) (freshBootstrap bool, err error) {
	dest := ConfigPath()
	if _, statErr := os.Stat(dest); statErr == nil {
		return false, nil // already in place
	}
	if OverridePath == "" {
		if old, ok := legacyConfigPath(); ok {
			if data, readErr := os.ReadFile(old); readErr == nil {
				fmt.Printf("Migrating config from %s to %s\n", old, dest)
				return false, saveRawBytes(dest, data)
			}
		}
	}
	if templatePath == "" {
		return false, fmt.Errorf("config not found and no template available")
	}
	return true, Bootstrap(templatePath)
}

func bootstrapTo(src, dest string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return saveRawBytes(dest, data)
}

// saveRawBytes writes data to dest verbatim (no JSON re-marshaling) —
// shared by bootstrapTo (copying the template) and MigrateOrBootstrap
// (copying an existing config's exact bytes forward, not a reformatted
// re-save of it).
func saveRawBytes(dest string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dest, data, 0o644)
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
	// Writes back to `path` specifically, not ConfigPath() via Save() —
	// loadFrom is also used in tests with an arbitrary temp path, and
	// writing to the real active config as a side effect of loading a
	// different file would be a bug in its own right.
	if patched, err := json.MarshalIndent(&c, "", "  "); err == nil {
		if !bytes.Equal(bytes.TrimSpace(data), bytes.TrimSpace(patched)) {
			_ = saveTo(path, &c)
		}
	}

	return &c, nil
}

// Save writes the config back to the active config file atomically.
func Save(c *Config) error {
	return saveTo(ConfigPath(), c)
}

// saveTo writes c to dest atomically (write to a .tmp file, then rename).
// Shared by Save() (always ConfigPath()) and loadFrom()'s auto-migrate
// step (whatever path it was given, which is ConfigPath() in real usage
// but may be a test's temp file). Mode 0o644, not 0o600: every write
// happens while running as root (headless-macs always requires sudo), so
// a root-only-readable file would be unreadable to the actual human
// operator without another sudo — confirmed as a real, live bug at the
// old per-user location. World-readable/root-writable matches how this
// project already treats e.g. the sudoers drop-in.
func saveTo(dest string, c *Config) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := dest + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, dest)
}
