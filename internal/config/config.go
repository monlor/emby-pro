package config

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/monlor/emby-pro/internal/pathutil"
	"gopkg.in/yaml.v3"
)

const (
	defaultRuleFile            = "/config/strm-rules.yml"
	defaultIndexDB             = "/config/strm-index.db"
	defaultBaseDir             = "/strm"
	defaultEmbyBaseURL         = "http://127.0.0.1:8096"
	defaultEmbyValidatePath    = "/System/Info"
	defaultRedirectListenAddr  = ":28096"
	defaultRedirectPublicURL   = "http://127.0.0.1:28096"
	defaultRedirectRoutePrefix = "/strm"
	defaultMinFileSize         = 15 * 1024 * 1024
)

type Config struct {
	OpenList OpenListConfig
	Emby     EmbyConfig
	Redirect RedirectConfig
	Sync     SyncConfig
	Rules    []Rule
}

type OpenListConfig struct {
	BaseURL            string
	PublicURL          string
	Token              string
	Username           string
	Password           string
	RequestTimeout     time.Duration
	Retry              int
	RetryBackoff       time.Duration
	ListPerPage        int
	InsecureSkipVerify bool
	DisableHTTP2       bool
}

type SyncConfig struct {
	BaseDir             string
	RuleFile            string
	IndexDB             string
	ScanInterval        time.Duration
	FullRescanInterval  time.Duration
	MaxDirsPerCycle     int
	MaxRequestsPerCycle int
	MinFileSize         int64
	VideoExts           map[string]struct{}
	CleanRemoved        bool
	Overwrite           bool
	LogLevel            string
}

type EmbyConfig struct {
	BaseURL        string
	ValidatePath   string
	RequestTimeout time.Duration
	TokenCacheTTL  time.Duration
}

type RedirectConfig struct {
	DirectPlay       bool
	DirectPlayWeb    bool
	DirectPlayUsers  map[string]struct{} // user IDs or names; nil means apply DirectPlay to all
	ListenAddr       string
	PublicURL        string
	PlayTicketSecret string
	EphemeralSecret  bool
	PlayTicketTTL    time.Duration
	RoutePrefix      string
}

type Rule struct {
	Name         string `yaml:"name"`
	SourcePath   string `yaml:"source_path"`
	TargetPath   string `yaml:"target_path"`
	Flatten      *bool  `yaml:"flatten"`
	IncludeRegex string `yaml:"include_regex"`
	ExcludeRegex string `yaml:"exclude_regex"`
	URLMode      string `yaml:"url_mode"`
	CleanRemoved *bool  `yaml:"clean_removed"`
	Overwrite    *bool  `yaml:"overwrite"`

	includeRE *regexp.Regexp
	excludeRE *regexp.Regexp
}

type ruleFile struct {
	Defaults ruleDefaults `yaml:"defaults"`
	Rules    []Rule       `yaml:"rules"`
}

type ruleDefaults struct {
	URLMode      string `yaml:"url_mode"`
	CleanRemoved *bool  `yaml:"clean_removed"`
	Overwrite    *bool  `yaml:"overwrite"`
	Flatten      *bool  `yaml:"flatten"`
}

func Load() (Config, error) {
	cfg := Config{
		OpenList: OpenListConfig{
			BaseURL:            strings.TrimSpace(os.Getenv("OPENLIST_BASE_URL")),
			PublicURL:          strings.TrimSpace(os.Getenv("OPENLIST_PUBLIC_URL")),
			Token:              strings.TrimSpace(os.Getenv("OPENLIST_TOKEN")),
			Username:           strings.TrimSpace(os.Getenv("OPENLIST_USERNAME")),
			Password:           strings.TrimSpace(os.Getenv("OPENLIST_PASSWORD")),
			RequestTimeout:     getenvDuration("OPENLIST_REQUEST_TIMEOUT", 15*time.Second),
			Retry:              getenvInt("OPENLIST_RETRY", 3),
			RetryBackoff:       getenvDuration("OPENLIST_RETRY_BACKOFF", 2*time.Second),
			ListPerPage:        getenvInt("OPENLIST_LIST_PER_PAGE", 200),
			InsecureSkipVerify: getenvBool("OPENLIST_INSECURE_SKIP_VERIFY", false),
			DisableHTTP2:       getenvBool("OPENLIST_DISABLE_HTTP2", false),
		},
		Emby: EmbyConfig{
			BaseURL:        strings.TrimSpace(getenvString("EMBY_BASE_URL", defaultEmbyBaseURL)),
			ValidatePath:   defaultEmbyValidatePath,
			RequestTimeout: getenvDuration("EMBY_REQUEST_TIMEOUT", 10*time.Second),
			TokenCacheTTL:  getenvDuration("EMBY_TOKEN_CACHE_TTL", 30*time.Second),
		},
		Redirect: RedirectConfig{
			DirectPlay:       getenvBool("OPENLIST_DIRECT_PLAY", true),
			DirectPlayWeb:    getenvBool("OPENLIST_DIRECT_PLAY_WEB", true),
			DirectPlayUsers:  parseStringSet(getenvString("OPENLIST_DIRECT_PLAY_USERS", "")),
			ListenAddr:       defaultRedirectListenAddr,
			PublicURL:        strings.TrimSpace(getenvString("PUBLIC_URL", defaultRedirectPublicURL)),
			PlayTicketSecret: strings.TrimSpace(getenvString("PLAY_TICKET_SECRET", "")),
			PlayTicketTTL:    getenvDuration("PLAY_TICKET_TTL", 12*time.Hour),
			RoutePrefix:      defaultRedirectRoutePrefix,
		},
		Sync: SyncConfig{
			BaseDir:             strings.TrimSpace(getenvString("STRM_BASE_DIR", defaultBaseDir)),
			RuleFile:            strings.TrimSpace(getenvString("STRM_RULES_FILE", defaultRuleFile)),
			IndexDB:             strings.TrimSpace(getenvString("STRM_INDEX_DB", defaultIndexDB)),
			ScanInterval:        getenvDuration("STRM_SCAN_INTERVAL", 5*time.Minute),
			FullRescanInterval:  getenvDuration("STRM_FULL_RESCAN_INTERVAL", 24*time.Hour),
			MaxDirsPerCycle:     getenvInt("STRM_MAX_DIRS_PER_CYCLE", 200),
			MaxRequestsPerCycle: getenvInt("STRM_MAX_REQUESTS_PER_CYCLE", 1000),
			MinFileSize:         defaultMinFileSize,
			VideoExts:           parseExtSet(getenvString("STRM_VIDEO_EXTS", ".mp4,.mkv,.avi,.ts,.mov,.wmv,.flv,.mpg")),
			CleanRemoved:        getenvBool("STRM_CLEAN_REMOVED", true),
			Overwrite:           getenvBool("STRM_OVERWRITE", true),
			LogLevel:            strings.ToLower(getenvString("STRM_LOG_LEVEL", "info")),
		},
	}

	if cfg.OpenList.BaseURL == "" {
		return Config{}, errors.New("OPENLIST_BASE_URL is required")
	}
	if raw := strings.TrimSpace(os.Getenv("STRM_MIN_FILE_SIZE")); raw != "" {
		size, err := parseSizeBytes(raw)
		if err != nil {
			return Config{}, fmt.Errorf("invalid STRM_MIN_FILE_SIZE: %w", err)
		}
		cfg.Sync.MinFileSize = size
	}
	if isEnvSet("OPENLIST_DIRECT_LINK_PERMANENT") {
		return Config{}, errors.New("OPENLIST_DIRECT_LINK_PERMANENT has been removed: OpenList link expiry is now managed by OpenList itself")
	}
	if isEnvSet("REDIRECT_TARGET_MODE") {
		return Config{}, errors.New("REDIRECT_TARGET_MODE has been removed: emby-pro now always resolves OpenList download routes at playback time")
	}
	if cfg.OpenList.Token == "" && (cfg.OpenList.Username == "" || cfg.OpenList.Password == "") {
		return Config{}, errors.New("OPENLIST_TOKEN or OPENLIST_USERNAME/OPENLIST_PASSWORD is required")
	}
	if cfg.Sync.BaseDir == "" {
		cfg.Sync.BaseDir = defaultBaseDir
	}
	if !filepath.IsAbs(cfg.Sync.BaseDir) {
		cfg.Sync.BaseDir = filepath.Clean(filepath.Join(string(os.PathSeparator), cfg.Sync.BaseDir))
	}
	if cfg.Sync.RuleFile == "" {
		cfg.Sync.RuleFile = defaultRuleFile
	}
	if cfg.Sync.IndexDB == "" {
		cfg.Sync.IndexDB = defaultIndexDB
	}
	if cfg.Emby.ValidatePath == "" {
		cfg.Emby.ValidatePath = "/System/Info"
	}
	if !strings.HasPrefix(cfg.Emby.ValidatePath, "/") {
		cfg.Emby.ValidatePath = "/" + cfg.Emby.ValidatePath
	}

	envRules, err := buildEnvRules(cfg.Sync.BaseDir)
	if err != nil {
		return Config{}, err
	}

	fileRules, err := loadRuleFile(cfg.Sync.RuleFile, cfg.Sync.BaseDir)
	if err != nil {
		return Config{}, err
	}

	merged := make(map[string]Rule, len(envRules)+len(fileRules))
	for _, rule := range envRules {
		merged[rule.SourcePath] = rule
	}
	for _, rule := range fileRules {
		merged[rule.SourcePath] = rule
	}

	if len(merged) == 0 {
		return Config{}, errors.New("no rules found: set OPENLIST_PATHS or provide /config/strm-rules.yml")
	}

	cfg.Rules = make([]Rule, 0, len(merged))
	for _, rule := range merged {
		if err := finalizeRule(&rule, cfg.Sync); err != nil {
			return Config{}, err
		}
		cfg.Rules = append(cfg.Rules, rule)
	}

	sort.Slice(cfg.Rules, func(i, j int) bool {
		return cfg.Rules[i].SourcePath < cfg.Rules[j].SourcePath
	})
	if err := validateRuleNames(cfg.Rules); err != nil {
		return Config{}, err
	}
	if cfg.Redirect.PlayTicketSecret == "" {
		secret, err := randomSecret(32)
		if err != nil {
			return Config{}, fmt.Errorf("generate PLAY_TICKET_SECRET: %w", err)
		}
		cfg.Redirect.PlayTicketSecret = secret
		cfg.Redirect.EphemeralSecret = true
	}
	if cfg.Redirect.PlayTicketTTL <= 0 {
		return Config{}, errors.New("PLAY_TICKET_TTL must be greater than zero")
	}

	return cfg, nil
}

func (r Rule) ShouldKeep(relativePath string) bool {
	if r.includeRE != nil && !r.includeRE.MatchString(relativePath) {
		return false
	}
	if r.excludeRE != nil && r.excludeRE.MatchString(relativePath) {
		return false
	}
	return true
}

func (r Rule) CleanRemovedValue(defaultValue bool) bool {
	if r.CleanRemoved == nil {
		return defaultValue
	}
	return *r.CleanRemoved
}

func (r Rule) OverwriteValue(defaultValue bool) bool {
	if r.Overwrite == nil {
		return defaultValue
	}
	return *r.Overwrite
}

func (r Rule) FlattenValue() bool {
	return r.Flatten != nil && *r.Flatten
}

func buildEnvRules(baseDir string) ([]Rule, error) {
	raw := strings.TrimSpace(os.Getenv("OPENLIST_PATHS"))
	if raw == "" {
		return nil, nil
	}

	seen := make(map[string]struct{})
	parts := strings.Split(raw, ",")
	rules := make([]Rule, 0, len(parts))
	for _, part := range parts {
		sourcePath := pathutil.NormalizeSourcePath(part)
		if sourcePath == "" {
			continue
		}
		if _, ok := seen[sourcePath]; ok {
			continue
		}
		seen[sourcePath] = struct{}{}
		rules = append(rules, Rule{
			Name:       defaultRuleName(sourcePath),
			SourcePath: sourcePath,
			TargetPath: defaultTargetPath(baseDir, sourcePath),
		})
	}
	if len(rules) == 0 {
		return nil, errors.New("OPENLIST_PATHS is set but no valid source path was found")
	}
	return rules, nil
}

func loadRuleFile(ruleFilePath, baseDir string) ([]Rule, error) {
	content, err := os.ReadFile(ruleFilePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read rule file: %w", err)
	}

	var rf ruleFile
	if err := yaml.Unmarshal(content, &rf); err != nil {
		return nil, fmt.Errorf("parse rule file: %w", err)
	}

	rules := make([]Rule, 0, len(rf.Rules))
	for i, rule := range rf.Rules {
		if rule.SourcePath == "" {
			return nil, fmt.Errorf("rule %d source_path is required", i+1)
		}
		if strings.TrimSpace(rf.Defaults.URLMode) != "" {
			return nil, errors.New("defaults.url_mode has been removed; emby-pro now always generates system URLs and signs playback at runtime")
		}
		if strings.TrimSpace(rule.URLMode) != "" {
			return nil, fmt.Errorf("rule %d url_mode has been removed", i+1)
		}
		if rule.CleanRemoved == nil {
			rule.CleanRemoved = rf.Defaults.CleanRemoved
		}
		if rule.Overwrite == nil {
			rule.Overwrite = rf.Defaults.Overwrite
		}
		if rule.Flatten == nil {
			rule.Flatten = rf.Defaults.Flatten
		}
		if rule.TargetPath == "" {
			rule.TargetPath = defaultTargetPath(baseDir, pathutil.NormalizeSourcePath(rule.SourcePath))
		}
		rules = append(rules, rule)
	}

	return rules, nil
}

func finalizeRule(rule *Rule, syncCfg SyncConfig) error {
	rule.SourcePath = pathutil.NormalizeSourcePath(rule.SourcePath)
	if rule.SourcePath == "" {
		return errors.New("rule source_path cannot be empty")
	}
	if rule.Name == "" {
		rule.Name = defaultRuleName(rule.SourcePath)
	}
	if rule.TargetPath == "" {
		rule.TargetPath = defaultTargetPath(syncCfg.BaseDir, rule.SourcePath)
	}
	if rule.Flatten == nil {
		rule.Flatten = boolPtr(false)
	}
	if !filepath.IsAbs(rule.TargetPath) {
		rule.TargetPath = filepath.Join(syncCfg.BaseDir, rule.TargetPath)
	}
	rule.TargetPath = filepath.Clean(rule.TargetPath)

	includeRE, err := compileOptionalPattern(rule.IncludeRegex)
	if err != nil {
		return fmt.Errorf("rule %s include_regex: %w", rule.Name, err)
	}
	excludeRE, err := compileOptionalPattern(rule.ExcludeRegex)
	if err != nil {
		return fmt.Errorf("rule %s exclude_regex: %w", rule.Name, err)
	}
	rule.includeRE = includeRE
	rule.excludeRE = excludeRE
	return nil
}

func defaultTargetPath(baseDir, sourcePath string) string {
	trimmed := strings.TrimPrefix(sourcePath, "/")
	if trimmed == "" {
		return filepath.Clean(baseDir)
	}
	return filepath.Clean(filepath.Join(baseDir, filepath.FromSlash(trimmed)))
}

func defaultRuleName(sourcePath string) string {
	if sourcePath == "/" {
		return "root"
	}
	name := strings.Trim(sourcePath, "/")
	name = strings.ReplaceAll(name, "/", "-")
	if name == "" {
		return "root"
	}
	return name
}

func compileOptionalPattern(pattern string) (*regexp.Regexp, error) {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return nil, nil
	}
	if strings.HasPrefix(pattern, "/") && strings.Count(pattern, "/") >= 2 {
		lastSlash := strings.LastIndex(pattern, "/")
		if lastSlash > 0 {
			body := pattern[1:lastSlash]
			flags := pattern[lastSlash+1:]
			if strings.Contains(flags, "i") {
				body = "(?i)" + body
			}
			return regexp.Compile(body)
		}
	}
	return regexp.Compile(pattern)
}

func getenvString(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func getenvBool(key string, fallback bool) bool {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func getenvInt(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func getenvDuration(key string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	if num, err := strconv.Atoi(value); err == nil {
		return time.Duration(num) * time.Second
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func parseStringSet(raw string) map[string]struct{} {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	result := make(map[string]struct{})
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			result[part] = struct{}{}
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func parseExtSet(raw string) map[string]struct{} {
	result := make(map[string]struct{})
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(strings.ToLower(part))
		if part == "" {
			continue
		}
		if !strings.HasPrefix(part, ".") {
			part = "." + part
		}
		result[part] = struct{}{}
	}
	return result
}

func parseSizeBytes(raw string) (int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, errors.New("value cannot be empty")
	}

	split := 0
	for split < len(raw) && raw[split] >= '0' && raw[split] <= '9' {
		split++
	}
	if split == 0 {
		return 0, fmt.Errorf("invalid size %q", raw)
	}

	value, err := strconv.ParseInt(raw[:split], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse size %q: %w", raw, err)
	}

	suffix := strings.ToUpper(strings.TrimSpace(raw[split:]))
	multiplier := int64(1)
	switch suffix {
	case "", "B":
		multiplier = 1
	case "K", "KB", "KIB":
		multiplier = 1024
	case "M", "MB", "MIB":
		multiplier = 1024 * 1024
	case "G", "GB", "GIB":
		multiplier = 1024 * 1024 * 1024
	case "T", "TB", "TIB":
		multiplier = 1024 * 1024 * 1024 * 1024
	default:
		return 0, fmt.Errorf("unsupported size suffix %q", suffix)
	}

	if value > math.MaxInt64/multiplier {
		return 0, fmt.Errorf("size %q is too large", raw)
	}
	return value * multiplier, nil
}

func validateRuleNames(rules []Rule) error {
	seen := make(map[string]string, len(rules))
	for _, rule := range rules {
		if previous, ok := seen[rule.Name]; ok {
			return fmt.Errorf("duplicate rule name %q for source paths %s and %s", rule.Name, previous, rule.SourcePath)
		}
		seen[rule.Name] = rule.SourcePath
	}
	return nil
}

func boolPtr(value bool) *bool {
	return &value
}

func isEnvSet(key string) bool {
	_, ok := os.LookupEnv(key)
	return ok
}

func randomSecret(size int) (string, error) {
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
