package config

import (
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
}
