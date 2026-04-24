package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monlor/emby-pro/internal/config"
)

func TestLoadValidatedConfigCreatesTemplateOnFirstRun(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "app.yml")

	_, err := loadValidatedConfig(configPath)
	if err == nil {
		t.Fatalf("expected validation error for default template")
	}
	if !strings.Contains(err.Error(), "openlist.token or openlist.username/openlist.password is required") {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, statErr := os.Stat(configPath); statErr != nil {
		t.Fatalf("expected config template to be created, stat error = %v", statErr)
	}
}

func TestLoadValidatedConfigValidatesRules(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "app.yml")
	cfg := config.Default()
	cfg.OpenList.Username = "user"
	cfg.OpenList.Password = "pass"
	cfg.Rules = []config.Rule{{SourcePath: "/movies"}}

	if err := config.SaveToFile(configPath, cfg); err != nil {
		t.Fatalf("SaveToFile() error = %v", err)
	}

	validated, err := loadValidatedConfig(configPath)
	if err != nil {
		t.Fatalf("loadValidatedConfig() error = %v", err)
	}
	if got, want := validated.Rules[0].TargetPath, "/strm/movies"; got != want {
		t.Fatalf("target path = %s, want %s", got, want)
	}
}
