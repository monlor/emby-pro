package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBuildEnvRulesDefaultTarget(t *testing.T) {
	t.Setenv("OPENLIST_BASE_URL", "http://openlist:5244")
	t.Setenv("OPENLIST_USERNAME", "user")
	t.Setenv("OPENLIST_PASSWORD", "pass")
	t.Setenv("OPENLIST_PATHS", "/movies,/tv")
	t.Setenv("PLAY_TICKET_SECRET", "test-secret")
	t.Setenv("STRM_BASE_DIR", "/strm")
	t.Setenv("STRM_RULES_FILE", filepath.Join(t.TempDir(), "missing.yml"))
	t.Setenv("STRM_INDEX_DB", filepath.Join(t.TempDir(), "index.db"))

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(cfg.Rules) != 2 {
		t.Fatalf("expected 2 rules, got %d", len(cfg.Rules))
	}
	if got, want := cfg.Rules[0].TargetPath, "/strm/movies"; got != want {
		t.Fatalf("movies target = %s, want %s", got, want)
	}
	if got, want := cfg.Rules[1].TargetPath, "/strm/tv"; got != want {
		t.Fatalf("tv target = %s, want %s", got, want)
	}
	if got, want := cfg.Redirect.PublicURL, defaultRedirectPublicURL; got != want {
		t.Fatalf("redirect public url = %s, want %s", got, want)
	}
	if got, want := cfg.Redirect.ListenAddr, defaultRedirectListenAddr; got != want {
		t.Fatalf("redirect listen addr = %s, want %s", got, want)
	}
	if got, want := cfg.Redirect.RoutePrefix, defaultRedirectRoutePrefix; got != want {
		t.Fatalf("redirect route prefix = %s, want %s", got, want)
	}
	if got, want := cfg.Redirect.PlayTicketTTL, 12*time.Hour; got != want {
		t.Fatalf("play ticket ttl = %s, want %s", got, want)
	}
	if !cfg.Redirect.DirectPlayWeb {
		t.Fatalf("expected web direct play to be enabled by default")
	}
	if got, want := cfg.Emby.BaseURL, defaultEmbyBaseURL; got != want {
		t.Fatalf("emby base url = %s, want %s", got, want)
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

func TestLoadOpenListDirectPlayFlag(t *testing.T) {
	t.Setenv("OPENLIST_BASE_URL", "http://openlist:5244")
	t.Setenv("OPENLIST_USERNAME", "user")
	t.Setenv("OPENLIST_PASSWORD", "pass")
	t.Setenv("OPENLIST_PATHS", "/movies")
	t.Setenv("OPENLIST_DIRECT_PLAY", "false")
	t.Setenv("PLAY_TICKET_TTL", "6h")
	t.Setenv("PLAY_TICKET_SECRET", "test-secret")
	t.Setenv("STRM_BASE_DIR", "/strm")
	t.Setenv("STRM_RULES_FILE", filepath.Join(t.TempDir(), "missing.yml"))
	t.Setenv("STRM_INDEX_DB", filepath.Join(t.TempDir(), "index.db"))

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Redirect.DirectPlay {
		t.Fatalf("expected direct play to be disabled")
	}
	if got, want := cfg.Redirect.PlayTicketTTL, 6*time.Hour; got != want {
		t.Fatalf("play ticket ttl = %s, want %s", got, want)
	}
}

func TestLoadOpenListDirectPlayWebFlag(t *testing.T) {
	t.Setenv("OPENLIST_BASE_URL", "http://openlist:5244")
	t.Setenv("OPENLIST_USERNAME", "user")
	t.Setenv("OPENLIST_PASSWORD", "pass")
	t.Setenv("OPENLIST_PATHS", "/movies")
	t.Setenv("OPENLIST_DIRECT_PLAY_WEB", "true")
	t.Setenv("PLAY_TICKET_SECRET", "test-secret")
	t.Setenv("STRM_BASE_DIR", "/strm")
	t.Setenv("STRM_RULES_FILE", filepath.Join(t.TempDir(), "missing.yml"))
	t.Setenv("STRM_INDEX_DB", filepath.Join(t.TempDir(), "index.db"))

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !cfg.Redirect.DirectPlayWeb {
		t.Fatalf("expected web direct play to be enabled")
	}
}

func TestLoadSupportsExplicitPublicURL(t *testing.T) {
	t.Setenv("OPENLIST_BASE_URL", "http://openlist:5244")
	t.Setenv("OPENLIST_USERNAME", "user")
	t.Setenv("OPENLIST_PASSWORD", "pass")
	t.Setenv("OPENLIST_PATHS", "/movies")
	t.Setenv("PLAY_TICKET_SECRET", "test-secret")
	t.Setenv("PUBLIC_URL", "https://media.example.com/emby")
	t.Setenv("OPENLIST_PUBLIC_URL", "https://list.example.com")
	t.Setenv("STRM_BASE_DIR", "/strm")
	t.Setenv("STRM_RULES_FILE", filepath.Join(t.TempDir(), "missing.yml"))
	t.Setenv("STRM_INDEX_DB", filepath.Join(t.TempDir(), "index.db"))

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got, want := cfg.Redirect.PublicURL, "https://media.example.com/emby"; got != want {
		t.Fatalf("redirect public url = %s, want %s", got, want)
	}
	if got, want := cfg.OpenList.PublicURL, "https://list.example.com"; got != want {
		t.Fatalf("openlist public url = %s, want %s", got, want)
	}
}

func TestLoadAllowsEmptyOpenListPublicURL(t *testing.T) {
	t.Setenv("OPENLIST_BASE_URL", "http://openlist:5244")
	t.Setenv("OPENLIST_USERNAME", "user")
	t.Setenv("OPENLIST_PASSWORD", "pass")
	t.Setenv("OPENLIST_PATHS", "/movies")
	t.Setenv("PLAY_TICKET_SECRET", "test-secret")
	t.Setenv("STRM_BASE_DIR", "/strm")
	t.Setenv("STRM_RULES_FILE", filepath.Join(t.TempDir(), "missing.yml"))
	t.Setenv("STRM_INDEX_DB", filepath.Join(t.TempDir(), "index.db"))

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.OpenList.PublicURL != "" {
		t.Fatalf("expected empty openlist public url, got %s", cfg.OpenList.PublicURL)
	}
}

func TestLoadSupportsExplicitEmbyBaseURL(t *testing.T) {
	t.Setenv("OPENLIST_BASE_URL", "http://openlist:5244")
	t.Setenv("OPENLIST_USERNAME", "user")
	t.Setenv("OPENLIST_PASSWORD", "pass")
	t.Setenv("OPENLIST_PATHS", "/movies")
	t.Setenv("PLAY_TICKET_SECRET", "test-secret")
	t.Setenv("EMBY_BASE_URL", "https://media.example.com/emby")
	t.Setenv("STRM_BASE_DIR", "/strm")
	t.Setenv("STRM_RULES_FILE", filepath.Join(t.TempDir(), "missing.yml"))
	t.Setenv("STRM_INDEX_DB", filepath.Join(t.TempDir(), "index.db"))

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got, want := cfg.Emby.BaseURL, "https://media.example.com/emby"; got != want {
		t.Fatalf("emby base url = %s, want %s", got, want)
	}
}

func TestLoadGeneratesEphemeralPlayTicketSecretWhenMissing(t *testing.T) {
	t.Setenv("OPENLIST_BASE_URL", "http://openlist:5244")
	t.Setenv("OPENLIST_USERNAME", "user")
	t.Setenv("OPENLIST_PASSWORD", "pass")
	t.Setenv("OPENLIST_PATHS", "/movies")
	t.Setenv("STRM_BASE_DIR", "/strm")
	t.Setenv("STRM_RULES_FILE", filepath.Join(t.TempDir(), "missing.yml"))
	t.Setenv("STRM_INDEX_DB", filepath.Join(t.TempDir(), "index.db"))

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Redirect.PlayTicketSecret == "" {
		t.Fatalf("expected generated play ticket secret")
	}
	if !cfg.Redirect.EphemeralSecret {
		t.Fatalf("expected ephemeral secret flag to be true")
	}
}

func TestLoadRejectsLegacyEnvFlags(t *testing.T) {
	t.Setenv("OPENLIST_BASE_URL", "http://openlist:5244")
	t.Setenv("OPENLIST_USERNAME", "user")
	t.Setenv("OPENLIST_PASSWORD", "pass")
	t.Setenv("OPENLIST_PATHS", "/movies")
	t.Setenv("PLAY_TICKET_SECRET", "test-secret")
	t.Setenv("STRM_BASE_DIR", "/strm")
	t.Setenv("STRM_RULES_FILE", filepath.Join(t.TempDir(), "missing.yml"))
	t.Setenv("STRM_INDEX_DB", filepath.Join(t.TempDir(), "index.db"))
	t.Setenv("OPENLIST_DIRECT_LINK_PERMANENT", "false")

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "OPENLIST_DIRECT_LINK_PERMANENT") {
		t.Fatalf("expected removed OPENLIST_DIRECT_LINK_PERMANENT error, got %v", err)
	}

	if err := os.Unsetenv("OPENLIST_DIRECT_LINK_PERMANENT"); err != nil {
		t.Fatalf("os.Unsetenv() error = %v", err)
	}
	t.Setenv("REDIRECT_TARGET_MODE", "download")
	_, err = Load()
	if err == nil || !strings.Contains(err.Error(), "REDIRECT_TARGET_MODE") {
		t.Fatalf("expected removed REDIRECT_TARGET_MODE error, got %v", err)
	}
}

func TestLoadAllowsRuleFlattenToOverrideDefaults(t *testing.T) {
	t.Setenv("OPENLIST_BASE_URL", "http://openlist:5244")
	t.Setenv("OPENLIST_USERNAME", "user")
	t.Setenv("OPENLIST_PASSWORD", "pass")
	t.Setenv("PLAY_TICKET_SECRET", "test-secret")
	t.Setenv("STRM_BASE_DIR", "/strm")
	ruleFile := filepath.Join(t.TempDir(), "rules.yml")
	t.Setenv("STRM_RULES_FILE", ruleFile)
	t.Setenv("STRM_INDEX_DB", filepath.Join(t.TempDir(), "index.db"))

	content := []byte(`
defaults:
  flatten: true
rules:
  - name: movies
    source_path: /movies
    flatten: false
`)
	if err := os.WriteFile(ruleFile, content, 0o644); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Rules[0].FlattenValue() {
		t.Fatalf("expected flatten=false to override defaults.flatten=true")
	}
}

func TestLoadRejectsRemovedURLMode(t *testing.T) {
	t.Setenv("OPENLIST_BASE_URL", "http://openlist:5244")
	t.Setenv("OPENLIST_USERNAME", "user")
	t.Setenv("OPENLIST_PASSWORD", "pass")
	t.Setenv("PLAY_TICKET_SECRET", "test-secret")
	t.Setenv("STRM_BASE_DIR", "/strm")
	ruleFile := filepath.Join(t.TempDir(), "rules.yml")
	t.Setenv("STRM_RULES_FILE", ruleFile)
	t.Setenv("STRM_INDEX_DB", filepath.Join(t.TempDir(), "index.db"))

	content := []byte(`
defaults:
  url_mode: redirect
rules:
  - name: movies
    source_path: /movies
`)
	if err := os.WriteFile(ruleFile, content, 0o644); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "defaults.url_mode") {
		t.Fatalf("expected removed url_mode error, got %v", err)
	}
}

func TestLoadRejectsDuplicateRuleNames(t *testing.T) {
	t.Setenv("OPENLIST_BASE_URL", "http://openlist:5244")
	t.Setenv("OPENLIST_USERNAME", "user")
	t.Setenv("OPENLIST_PASSWORD", "pass")
	t.Setenv("PLAY_TICKET_SECRET", "test-secret")
	t.Setenv("STRM_BASE_DIR", "/strm")
	ruleFile := filepath.Join(t.TempDir(), "rules.yml")
	t.Setenv("STRM_RULES_FILE", ruleFile)
	t.Setenv("STRM_INDEX_DB", filepath.Join(t.TempDir(), "index.db"))

	content := []byte(`
rules:
  - name: media
    source_path: /movies
  - name: media
    source_path: /tv
`)
	if err := os.WriteFile(ruleFile, content, 0o644); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), `duplicate rule name "media"`) {
		t.Fatalf("expected duplicate rule name error, got %v", err)
	}
}
