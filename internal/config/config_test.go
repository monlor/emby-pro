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
	if got, want := cfg.Sync.MinFileSize, int64(defaultMinFileSize); got != want {
		t.Fatalf("min file size = %d, want %d", got, want)
	}
	if !cfg.Redirect.DirectPlayWeb {
		t.Fatalf("expected web direct play to be enabled by default")
	}
	if got, want := cfg.Emby.BaseURL, defaultEmbyBaseURL; got != want {
		t.Fatalf("emby base url = %s, want %s", got, want)
	}
	if got, want := cfg.Emby.RequestTimeout, 15*time.Second; got != want {
		t.Fatalf("emby request timeout = %s, want %s", got, want)
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

func TestLoadOpenListFastPlaybackInfoFlag(t *testing.T) {
	t.Setenv("OPENLIST_BASE_URL", "http://openlist:5244")
	t.Setenv("OPENLIST_USERNAME", "user")
	t.Setenv("OPENLIST_PASSWORD", "pass")
	t.Setenv("OPENLIST_PATHS", "/movies")
	t.Setenv("OPENLIST_FAST_PLAYBACKINFO", "true")
	t.Setenv("PLAY_TICKET_SECRET", "test-secret")
	t.Setenv("STRM_BASE_DIR", "/strm")
	t.Setenv("STRM_RULES_FILE", filepath.Join(t.TempDir(), "missing.yml"))
	t.Setenv("STRM_INDEX_DB", filepath.Join(t.TempDir(), "index.db"))

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !cfg.Redirect.FastPlaybackInfo {
		t.Fatalf("expected fast playbackinfo to be enabled")
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

func TestLoadSupportsExplicitMinFileSize(t *testing.T) {
	t.Setenv("OPENLIST_BASE_URL", "http://openlist:5244")
	t.Setenv("OPENLIST_USERNAME", "user")
	t.Setenv("OPENLIST_PASSWORD", "pass")
	t.Setenv("OPENLIST_PATHS", "/movies")
	t.Setenv("PLAY_TICKET_SECRET", "test-secret")
	t.Setenv("STRM_MIN_FILE_SIZE", "20M")
	t.Setenv("STRM_BASE_DIR", "/strm")
	t.Setenv("STRM_RULES_FILE", filepath.Join(t.TempDir(), "missing.yml"))
	t.Setenv("STRM_INDEX_DB", filepath.Join(t.TempDir(), "index.db"))

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got, want := cfg.Sync.MinFileSize, int64(20*1024*1024); got != want {
		t.Fatalf("min file size = %d, want %d", got, want)
	}
}

func TestLoadRejectsInvalidMinFileSize(t *testing.T) {
	t.Setenv("OPENLIST_BASE_URL", "http://openlist:5244")
	t.Setenv("OPENLIST_USERNAME", "user")
	t.Setenv("OPENLIST_PASSWORD", "pass")
	t.Setenv("OPENLIST_PATHS", "/movies")
	t.Setenv("PLAY_TICKET_SECRET", "test-secret")
	t.Setenv("STRM_MIN_FILE_SIZE", "15XYZ")
	t.Setenv("STRM_BASE_DIR", "/strm")
	t.Setenv("STRM_RULES_FILE", filepath.Join(t.TempDir(), "missing.yml"))
	t.Setenv("STRM_INDEX_DB", filepath.Join(t.TempDir(), "index.db"))

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "STRM_MIN_FILE_SIZE") {
		t.Fatalf("expected invalid STRM_MIN_FILE_SIZE error, got %v", err)
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

func TestLoadSupportsPathMappings(t *testing.T) {
	t.Setenv("OPENLIST_BASE_URL", "http://openlist:5244")
	t.Setenv("OPENLIST_USERNAME", "user")
	t.Setenv("OPENLIST_PASSWORD", "pass")
	t.Setenv("OPENLIST_PATHS", "/115pan_cookie")
	t.Setenv("STRM_PATH_MAPPINGS", "/115pan_cookie:/115pan,/quark_cookie:/quark")
	t.Setenv("PLAY_TICKET_SECRET", "test-secret")
	t.Setenv("STRM_BASE_DIR", "/strm")
	t.Setenv("STRM_RULES_FILE", filepath.Join(t.TempDir(), "missing.yml"))
	t.Setenv("STRM_INDEX_DB", filepath.Join(t.TempDir(), "index.db"))

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(cfg.Redirect.PathMappings) != 2 {
		t.Fatalf("expected 2 path mappings, got %d", len(cfg.Redirect.PathMappings))
	}
	if got, want := cfg.Rules[0].SourcePath, "/115pan_cookie"; got != want {
		t.Fatalf("rule source path = %s, want %s", got, want)
	}
}

func TestLoadSupportsAdaptiveSyncAndRateLimitConfig(t *testing.T) {
	t.Setenv("OPENLIST_BASE_URL", "http://openlist:5244")
	t.Setenv("OPENLIST_USERNAME", "user")
	t.Setenv("OPENLIST_PASSWORD", "pass")
	t.Setenv("OPENLIST_PATHS", "/movies")
	t.Setenv("PLAY_TICKET_SECRET", "test-secret")
	t.Setenv("OPENLIST_RATE_LIMIT_QPS", "0.2")
	t.Setenv("OPENLIST_RATE_LIMIT_BURST", "1")
	t.Setenv("STRM_HOT_INTERVAL", "30m")
	t.Setenv("STRM_WARM_INTERVAL", "6h")
	t.Setenv("STRM_COLD_INTERVAL", "24h")
	t.Setenv("STRM_HOT_JITTER", "10m")
	t.Setenv("STRM_WARM_JITTER", "1h")
	t.Setenv("STRM_COLD_JITTER", "4h")
	t.Setenv("STRM_UNCHANGED_TO_WARM", "3")
	t.Setenv("STRM_UNCHANGED_TO_COLD", "7")
	t.Setenv("STRM_FAILURE_BACKOFF_MAX", "24h")
	t.Setenv("STRM_RULE_COOLDOWN", "6h")
	t.Setenv("STRM_RULES_FILE", filepath.Join(t.TempDir(), "missing.yml"))
	t.Setenv("STRM_INDEX_DB", filepath.Join(t.TempDir(), "index.db"))

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got, want := cfg.OpenList.RateLimitQPS, 0.2; got != want {
		t.Fatalf("rate limit qps = %v, want %v", got, want)
	}
	if got, want := cfg.Sync.WarmInterval, 6*time.Hour; got != want {
		t.Fatalf("warm interval = %s, want %s", got, want)
	}
	if got, want := cfg.Sync.RuleCooldown, 6*time.Hour; got != want {
		t.Fatalf("rule cooldown = %s, want %s", got, want)
	}
}

func TestLoadSupportsSyncProfilePreset(t *testing.T) {
	t.Setenv("OPENLIST_BASE_URL", "http://openlist:5244")
	t.Setenv("OPENLIST_USERNAME", "user")
	t.Setenv("OPENLIST_PASSWORD", "pass")
	t.Setenv("OPENLIST_PATHS", "/movies")
	t.Setenv("PLAY_TICKET_SECRET", "test-secret")
	t.Setenv("STRM_SYNC_PROFILE", "conservative")
	t.Setenv("STRM_RULES_FILE", filepath.Join(t.TempDir(), "missing.yml"))
	t.Setenv("STRM_INDEX_DB", filepath.Join(t.TempDir(), "index.db"))

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got, want := cfg.OpenList.RateLimitQPS, 0.2; got != want {
		t.Fatalf("rate limit qps = %v, want %v", got, want)
	}
	if got, want := cfg.Sync.MaxDirsPerCycle, 20; got != want {
		t.Fatalf("max dirs per cycle = %d, want %d", got, want)
	}
	if got, want := cfg.Sync.MaxRequestsPerCycle, 60; got != want {
		t.Fatalf("max requests per cycle = %d, want %d", got, want)
	}
}

func TestLoadAllowsExplicitOverridesOnTopOfSyncProfile(t *testing.T) {
	t.Setenv("OPENLIST_BASE_URL", "http://openlist:5244")
	t.Setenv("OPENLIST_USERNAME", "user")
	t.Setenv("OPENLIST_PASSWORD", "pass")
	t.Setenv("OPENLIST_PATHS", "/movies")
	t.Setenv("PLAY_TICKET_SECRET", "test-secret")
	t.Setenv("STRM_SYNC_PROFILE", "conservative")
	t.Setenv("STRM_MAX_DIRS_PER_CYCLE", "35")
	t.Setenv("OPENLIST_RATE_LIMIT_QPS", "0.4")
	t.Setenv("STRM_RULES_FILE", filepath.Join(t.TempDir(), "missing.yml"))
	t.Setenv("STRM_INDEX_DB", filepath.Join(t.TempDir(), "index.db"))

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got, want := cfg.Sync.MaxDirsPerCycle, 35; got != want {
		t.Fatalf("max dirs per cycle = %d, want %d", got, want)
	}
	if got, want := cfg.OpenList.RateLimitQPS, 0.4; got != want {
		t.Fatalf("rate limit qps = %v, want %v", got, want)
	}
	if got, want := cfg.Sync.MaxRequestsPerCycle, 60; got != want {
		t.Fatalf("max requests per cycle = %d, want %d", got, want)
	}
}

func TestLoadRejectsInvalidSyncProfile(t *testing.T) {
	t.Setenv("OPENLIST_BASE_URL", "http://openlist:5244")
	t.Setenv("OPENLIST_USERNAME", "user")
	t.Setenv("OPENLIST_PASSWORD", "pass")
	t.Setenv("OPENLIST_PATHS", "/movies")
	t.Setenv("PLAY_TICKET_SECRET", "test-secret")
	t.Setenv("STRM_SYNC_PROFILE", "fast-and-loose")
	t.Setenv("STRM_RULES_FILE", filepath.Join(t.TempDir(), "missing.yml"))
	t.Setenv("STRM_INDEX_DB", filepath.Join(t.TempDir(), "index.db"))

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "STRM_SYNC_PROFILE") {
		t.Fatalf("expected STRM_SYNC_PROFILE error, got %v", err)
	}
}

func TestLoadRejectsRemovedScanIntervalEnv(t *testing.T) {
	t.Setenv("OPENLIST_BASE_URL", "http://openlist:5244")
	t.Setenv("OPENLIST_USERNAME", "user")
	t.Setenv("OPENLIST_PASSWORD", "pass")
	t.Setenv("OPENLIST_PATHS", "/movies")
	t.Setenv("PLAY_TICKET_SECRET", "test-secret")
	t.Setenv("STRM_SCAN_INTERVAL", "300")
	t.Setenv("STRM_RULES_FILE", filepath.Join(t.TempDir(), "missing.yml"))
	t.Setenv("STRM_INDEX_DB", filepath.Join(t.TempDir(), "index.db"))

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "STRM_SCAN_INTERVAL") {
		t.Fatalf("expected removed STRM_SCAN_INTERVAL error, got %v", err)
	}
}

func TestLoadRejectsDuplicatePathMappingSourcePrefix(t *testing.T) {
	t.Setenv("OPENLIST_BASE_URL", "http://openlist:5244")
	t.Setenv("OPENLIST_USERNAME", "user")
	t.Setenv("OPENLIST_PASSWORD", "pass")
	t.Setenv("OPENLIST_PATHS", "/115pan_cookie")
	t.Setenv("STRM_PATH_MAPPINGS", "/115pan_cookie:/115pan,/115pan_cookie:/115pan-bak")
	t.Setenv("PLAY_TICKET_SECRET", "test-secret")
	t.Setenv("STRM_BASE_DIR", "/strm")
	t.Setenv("STRM_RULES_FILE", filepath.Join(t.TempDir(), "missing.yml"))
	t.Setenv("STRM_INDEX_DB", filepath.Join(t.TempDir(), "index.db"))

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "STRM_PATH_MAPPINGS") {
		t.Fatalf("expected STRM_PATH_MAPPINGS error, got %v", err)
	}
}

func TestMapSourceToPublicPathUsesLongestSourcePrefix(t *testing.T) {
	mappings := []PathMapping{
		{SourcePrefix: "/115pan_cookie/series", PublicPrefix: "/115pan/series"},
		{SourcePrefix: "/115pan_cookie", PublicPrefix: "/115pan"},
	}

	if got, want := MapSourceToPublicPath(mappings, "/115pan_cookie/series/demo.mp4"), "/115pan/series/demo.mp4"; got != want {
		t.Fatalf("MapSourceToPublicPath() = %s, want %s", got, want)
	}
	if got, want := MapSourceToPublicPath(mappings, "/115pan_cookie/movie/demo.mp4"), "/115pan/movie/demo.mp4"; got != want {
		t.Fatalf("MapSourceToPublicPath() = %s, want %s", got, want)
	}
	if got, want := MapPublicToSourcePath(mappings, "/115pan/series/demo.mp4"), "/115pan_cookie/series/demo.mp4"; got != want {
		t.Fatalf("MapPublicToSourcePath() = %s, want %s", got, want)
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
