package config

import (
	"testing"
	"time"

	"github.com/spf13/viper"
)

func TestLoadFromViper(t *testing.T) {
	v := viper.New()
	SetDefaults(v)
	v.Set("app.name", "Template")
	v.Set("server.port", 9090)
	v.Set("server.readTimeout", "30s")
	v.Set("workflow.databasePath", "data/test-workflows.db")
	v.Set("workflow.scriptEnabled", true)
	v.Set("workflow.scriptTimeout", "500ms")

	cfg, err := Load(v)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.App.Name != "Template" {
		t.Fatalf("expected app name override, got %q", cfg.App.Name)
	}
	if cfg.Server.Port != 9090 {
		t.Fatalf("expected port 9090, got %d", cfg.Server.Port)
	}
	if cfg.Server.ReadTimeout != 30*time.Second {
		t.Fatalf("expected duration decode, got %s", cfg.Server.ReadTimeout)
	}
	if cfg.Workflow.DatabasePath != "data/test-workflows.db" {
		t.Fatalf("expected workflow database path override, got %q", cfg.Workflow.DatabasePath)
	}
	if !cfg.Workflow.ScriptEnabled {
		t.Fatal("expected script activity flag override to load")
	}
	if cfg.Workflow.ScriptTimeout != 500*time.Millisecond {
		t.Fatalf("expected script timeout override, got %s", cfg.Workflow.ScriptTimeout)
	}
}
