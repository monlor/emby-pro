package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEnsureConfigFileCreatesDefaultTemplate(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "app.yml")

	created, err := EnsureConfigFile(configPath)
	if err != nil {
		t.Fatalf("EnsureConfigFile() error = %v", err)
	}
	if !created {
		t.Fatalf("expected config file to be created")
	}

	cfg, err := LoadFromFile(configPath)
	if err != nil {
		t.Fatalf("LoadFromFile() error = %v", err)
	}
	if got, want := cfg.OpenList.BaseURL, defaultOpenListBaseURL; got != want {
		t.Fatalf("openlist base url = %s, want %s", got, want)
	}
	if got, want := cfg.Sync.BaseDir, defaultBaseDir; got != want {
		t.Fatalf("sync base dir = %s, want %s", got, want)
	}
	if len(cfg.Rules) != 0 {
		t.Fatalf("expected empty rules, got %d", len(cfg.Rules))
	}
}

func TestLoadFromFileAppliesDefaults(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "app.yml")
	if err := os.WriteFile(configPath, []byte(`
openlist:
  username: user
  password: pass
rules:
  - source_path: /movies
`), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	cfg, err := LoadFromFile(configPath)
	if err != nil {
		t.Fatalf("LoadFromFile() error = %v", err)
	}
	if got, want := cfg.OpenList.BaseURL, defaultOpenListBaseURL; got != want {
		t.Fatalf("openlist base url = %s, want %s", got, want)
	}
	if got, want := cfg.Redirect.PublicURL, defaultRedirectPublicURL; got != want {
		t.Fatalf("redirect public url = %s, want %s", got, want)
	}
	if cfg.Redirect.DirectPlayWeb {
		t.Fatalf("expected direct_play_web default to be false")
	}
	if got, want := cfg.Rules[0].TargetPath, ""; got != want {
		t.Fatalf("target path before validation = %q, want empty", got)
	}
}

func TestValidateNormalizesRules(t *testing.T) {
	cfg := Default()
	cfg.OpenList.Username = "user"
	cfg.OpenList.Password = "pass"
	cfg.Rules = []Rule{{SourcePath: "/movies"}}

	validated, err := Validate(cfg)
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if got, want := validated.Rules[0].TargetPath, "/strm/movies"; got != want {
		t.Fatalf("target path = %s, want %s", got, want)
	}
	if got, want := validated.Rules[0].Name, "movies"; got != want {
		t.Fatalf("rule name = %s, want %s", got, want)
	}
	if validated.Redirect.PlayTicketSecret == "" {
		t.Fatalf("expected generated play ticket secret")
	}
	if !validated.Redirect.EphemeralSecret {
		t.Fatalf("expected ephemeral play ticket secret")
	}
}

func TestValidateRejectsMissingAuth(t *testing.T) {
	cfg := Default()
	cfg.Rules = []Rule{{SourcePath: "/movies"}}

	_, err := Validate(cfg)
	if err == nil || !strings.Contains(err.Error(), "openlist.token or openlist.username/openlist.password") {
		t.Fatalf("expected auth error, got %v", err)
	}
}

func TestValidateRejectsMissingRules(t *testing.T) {
	cfg := Default()
	cfg.OpenList.Username = "user"
	cfg.OpenList.Password = "pass"

	_, err := Validate(cfg)
	if err == nil || !strings.Contains(err.Error(), "at least one rule") {
		t.Fatalf("expected rules error, got %v", err)
	}
}

func TestValidateRejectsInvalidThresholds(t *testing.T) {
	cfg := Default()
	cfg.OpenList.Username = "user"
	cfg.OpenList.Password = "pass"
	cfg.Rules = []Rule{{SourcePath: "/movies"}}
	cfg.Sync.UnchangedToWarm = 4
	cfg.Sync.UnchangedToCold = 3

	_, err := Validate(cfg)
	if err == nil || !strings.Contains(err.Error(), "adaptive sync unchanged thresholds") {
		t.Fatalf("expected threshold error, got %v", err)
	}
}

func TestCompileOptionalPattern(t *testing.T) {
	re, err := compileOptionalPattern("/sample|trailer/i")
	if err != nil {
		t.Fatalf("compileOptionalPattern() error = %v", err)
	}
	if !re.MatchString("Sample") {
		t.Fatalf("expected regex to match case-insensitively")
	}
}

func TestParseSizeBytes(t *testing.T) {
	got, err := parseSizeBytes("20M")
	if err != nil {
		t.Fatalf("parseSizeBytes() error = %v", err)
	}
	if want := int64(20 * 1024 * 1024); got != want {
		t.Fatalf("size = %d, want %d", got, want)
	}
}

func TestSaveToFileRoundTrip(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "app.yml")
	cfg := Default()
	cfg.OpenList.Username = "user"
	cfg.OpenList.Password = "pass"
	cfg.Redirect.PlayTicketTTL = 6 * time.Hour
	cfg.Rules = []Rule{{SourcePath: "/tv", TargetPath: "/strm/tv"}}

	if err := SaveToFile(configPath, cfg); err != nil {
		t.Fatalf("SaveToFile() error = %v", err)
	}

	loaded, err := LoadFromFile(configPath)
	if err != nil {
		t.Fatalf("LoadFromFile() error = %v", err)
	}
	if got, want := loaded.Redirect.PlayTicketTTL, 6*time.Hour; got != want {
		t.Fatalf("play ticket ttl = %s, want %s", got, want)
	}
	if got, want := loaded.Rules[0].SourcePath, "/tv"; got != want {
		t.Fatalf("source path = %s, want %s", got, want)
	}
}
