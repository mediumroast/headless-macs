package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadTemplate(t *testing.T) {
	// Bootstrap to a temp dir and load back
	tmp := t.TempDir() + "/config.json"

	// Use the repo template (two levels up from this file)
	template := "../../config.json"
	if err := bootstrapTo(template, tmp); err != nil {
		t.Fatalf("bootstrapTo: %v", err)
	}

	c, err := loadFrom(tmp)
	if err != nil {
		t.Fatalf("loadFrom: %v", err)
	}

	if !c.Tools.Ollama.Enabled {
		t.Error("expected ollama.enabled = true")
	}
	if c.Tools.Ollama.Host == "" {
		t.Error("expected ollama.host to be set")
	}
	if c.Network.LocalhostOnly {
		t.Error("expected network.localhost_only = false in template")
	}
	if c.Tools.Macmon.Enabled {
		t.Error("expected macmon.enabled = false in template (opt-in)")
	}
	if c.Tools.Macmon.Port != 9090 {
		t.Errorf("expected macmon.port = 9090, got %d", c.Tools.Macmon.Port)
	}
	if c.Tools.Macmon.IntervalMs != 1000 {
		t.Errorf("expected macmon.interval_ms = 1000, got %d", c.Tools.Macmon.IntervalMs)
	}
}

// withTestPaths redirects SystemConfigPath and $HOME to temp locations for
// the duration of one test, restoring both afterward — never touches the
// real /etc/headless-macs or the real invoking user's home.
func withTestPaths(t *testing.T) (systemPath, home string) {
	t.Helper()
	dir := t.TempDir()
	systemPath = filepath.Join(dir, "system", "config.json")
	home = filepath.Join(dir, "home")

	origSystem := SystemConfigPath
	origOverride := OverridePath
	origHome := os.Getenv("HOME")
	SystemConfigPath = systemPath
	OverridePath = ""
	t.Setenv("HOME", home)
	t.Cleanup(func() {
		SystemConfigPath = origSystem
		OverridePath = origOverride
		os.Setenv("HOME", origHome)
	})
	return systemPath, home
}

func TestMigrateOrBootstrap_Migrates(t *testing.T) {
	systemPath, home := withTestPaths(t)

	legacyDir := filepath.Join(home, ".headless_macs")
	if err := os.MkdirAll(legacyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	legacyPath := filepath.Join(legacyDir, "config.json")
	want := []byte(`{"tools":{"ollama":{"enabled":true}}}`)
	if err := os.WriteFile(legacyPath, want, 0o600); err != nil {
		t.Fatal(err)
	}

	fresh, err := MigrateOrBootstrap("../../config.json")
	if err != nil {
		t.Fatalf("MigrateOrBootstrap: %v", err)
	}
	if fresh {
		t.Error("expected fresh=false for a migration, got true")
	}
	got, err := os.ReadFile(systemPath)
	if err != nil {
		t.Fatalf("reading migrated file: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("migrated content = %q, want %q", got, want)
	}
	if _, err := os.Stat(legacyPath); err != nil {
		t.Error("expected the old legacy file to remain in place after migration")
	}
}

func TestMigrateOrBootstrap_NoOpWhenAlreadyPresent(t *testing.T) {
	systemPath, _ := withTestPaths(t)
	if err := os.MkdirAll(filepath.Dir(systemPath), 0o755); err != nil {
		t.Fatal(err)
	}
	existing := []byte(`{"already":"here"}`)
	if err := os.WriteFile(systemPath, existing, 0o644); err != nil {
		t.Fatal(err)
	}

	fresh, err := MigrateOrBootstrap("../../config.json")
	if err != nil {
		t.Fatalf("MigrateOrBootstrap: %v", err)
	}
	if fresh {
		t.Error("expected fresh=false when the system config already exists, got true")
	}
	got, _ := os.ReadFile(systemPath)
	if string(got) != string(existing) {
		t.Error("expected the already-present file to be left untouched")
	}
}

func TestMigrateOrBootstrap_FreshWhenNeitherExists(t *testing.T) {
	systemPath, _ := withTestPaths(t)

	fresh, err := MigrateOrBootstrap("../../config.json")
	if err != nil {
		t.Fatalf("MigrateOrBootstrap: %v", err)
	}
	if !fresh {
		t.Error("expected fresh=true when neither system nor legacy config exists, got false")
	}
	if _, err := os.Stat(systemPath); err != nil {
		t.Errorf("expected a fresh bootstrap to create %s: %v", systemPath, err)
	}
}

func TestMigrateOrBootstrap_OverridePathSkipsMigration(t *testing.T) {
	_, home := withTestPaths(t)

	legacyDir := filepath.Join(home, ".headless_macs")
	if err := os.MkdirAll(legacyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyDir, "config.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	overridePath := filepath.Join(dir, "explicit-config.json")
	OverridePath = overridePath

	fresh, err := MigrateOrBootstrap("../../config.json")
	if err != nil {
		t.Fatalf("MigrateOrBootstrap: %v", err)
	}
	if !fresh {
		t.Error("expected an explicit --config target to bootstrap fresh, not migrate, got fresh=false")
	}
	if _, err := os.Stat(overridePath); err != nil {
		t.Errorf("expected a fresh bootstrap at the override path: %v", err)
	}
}
