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
	defaultConfigFile         = "/config/app.yml"
	defaultIndexDB            = "/config/strm-index.db"
	defaultBaseDir            = "/strm"
	defaultEmbyBaseURL        = "http://127.0.0.1:8096"
	defaultEmbyValidatePath   = "/System/Info"
	defaultOpenListBaseURL    = "http://127.0.0.1:5244"
	defaultRedirectListenAddr = ":28096"
	defaultRedirectPublicURL  = "http://127.0.0.1:28096"
	defaultRoutePrefix        = "/strm"
	defaultMinFileSize        = 15 * 1024 * 1024
)

type Config struct {
	OpenList OpenListConfig `yaml:"openlist" json:"openlist"`
	Emby     EmbyConfig     `yaml:"emby" json:"emby"`
	Redirect RedirectConfig `yaml:"redirect" json:"redirect"`
	Sync     SyncConfig     `yaml:"sync" json:"sync"`
	Rules    []Rule         `yaml:"rules" json:"rules"`
}

type OpenListConfig struct {
	BaseURL            string        `yaml:"base_url" json:"base_url"`
	PublicURL          string        `yaml:"public_url" json:"public_url"`
	Token              string        `yaml:"token" json:"token"`
	Username           string        `yaml:"username" json:"username"`
	Password           string        `yaml:"password" json:"password"`
	RequestTimeout     time.Duration `yaml:"request_timeout" json:"request_timeout"`
	Retry              int           `yaml:"retry" json:"retry"`
	RetryBackoff       time.Duration `yaml:"retry_backoff" json:"retry_backoff"`
	ListPerPage        int           `yaml:"list_per_page" json:"list_per_page"`
	RateLimitQPS       float64       `yaml:"rate_limit_qps" json:"rate_limit_qps"`
	RateLimitBurst     int           `yaml:"rate_limit_burst" json:"rate_limit_burst"`
	InsecureSkipVerify bool          `yaml:"insecure_skip_verify" json:"insecure_skip_verify"`
	DisableHTTP2       bool          `yaml:"disable_http2" json:"disable_http2"`
}

type SyncConfig struct {
	BaseDir             string              `yaml:"base_dir" json:"base_dir"`
	RuleFile            string              `yaml:"-" json:"-"`
	IndexDB             string              `yaml:"index_db" json:"index_db"`
	FullRescanInterval  time.Duration       `yaml:"full_rescan_interval" json:"full_rescan_interval"`
	MaxDirsPerCycle     int                 `yaml:"max_dirs_per_cycle" json:"max_dirs_per_cycle"`
	MaxRequestsPerCycle int                 `yaml:"max_requests_per_cycle" json:"max_requests_per_cycle"`
	MinFileSize         int64               `yaml:"min_file_size" json:"min_file_size"`
	VideoExtsRaw        []string            `yaml:"video_exts" json:"video_exts"`
	CleanRemoved        bool                `yaml:"clean_removed" json:"clean_removed"`
	Overwrite           bool                `yaml:"overwrite" json:"overwrite"`
	LogLevel            string              `yaml:"log_level" json:"log_level"`
	HotInterval         time.Duration       `yaml:"hot_interval" json:"hot_interval"`
	WarmInterval        time.Duration       `yaml:"warm_interval" json:"warm_interval"`
	ColdInterval        time.Duration       `yaml:"cold_interval" json:"cold_interval"`
	HotJitter           time.Duration       `yaml:"hot_jitter" json:"hot_jitter"`
	WarmJitter          time.Duration       `yaml:"warm_jitter" json:"warm_jitter"`
	ColdJitter          time.Duration       `yaml:"cold_jitter" json:"cold_jitter"`
	UnchangedToWarm     int                 `yaml:"unchanged_to_warm" json:"unchanged_to_warm"`
	UnchangedToCold     int                 `yaml:"unchanged_to_cold" json:"unchanged_to_cold"`
	FailureBackoffMax   time.Duration       `yaml:"failure_backoff_max" json:"failure_backoff_max"`
	RuleCooldown        time.Duration       `yaml:"rule_cooldown" json:"rule_cooldown"`
	VideoExts           map[string]struct{} `yaml:"-" json:"-"`
}

type EmbyConfig struct {
	BaseURL        string        `yaml:"base_url" json:"base_url"`
	ValidatePath   string        `yaml:"validate_path" json:"validate_path"`
	RequestTimeout time.Duration `yaml:"request_timeout" json:"request_timeout"`
	TokenCacheTTL  time.Duration `yaml:"token_cache_ttl" json:"token_cache_ttl"`
}

type RedirectConfig struct {
	DirectPlay        bool                `yaml:"direct_play" json:"direct_play"`
	DirectPlayWeb     bool                `yaml:"direct_play_web" json:"direct_play_web"`
	FastPlaybackInfo  bool                `yaml:"fast_playback_info" json:"fast_playback_info"`
	DirectPlayUsers   []string            `yaml:"direct_play_users" json:"direct_play_users"`
	ListenAddr        string              `yaml:"listen_addr" json:"listen_addr"`
	PublicURL         string              `yaml:"public_url" json:"public_url"`
	PlayTicketSecret  string              `yaml:"play_ticket_secret" json:"play_ticket_secret"`
	EphemeralSecret   bool                `yaml:"-" json:"ephemeral_secret"`
	PlayTicketTTL     time.Duration       `yaml:"play_ticket_ttl" json:"play_ticket_ttl"`
	RoutePrefix       string              `yaml:"route_prefix" json:"route_prefix"`
	DirectPlayUserSet map[string]struct{} `yaml:"-" json:"-"`
}

type Rule struct {
	Name         string `yaml:"name" json:"name"`
	SourcePath   string `yaml:"source_path" json:"source_path"`
	TargetPath   string `yaml:"target_path" json:"target_path"`
	Flatten      *bool  `yaml:"flatten,omitempty" json:"flatten,omitempty"`
	IncludeRegex string `yaml:"include_regex,omitempty" json:"include_regex,omitempty"`
	ExcludeRegex string `yaml:"exclude_regex,omitempty" json:"exclude_regex,omitempty"`
	CleanRemoved *bool  `yaml:"clean_removed,omitempty" json:"clean_removed,omitempty"`
	Overwrite    *bool  `yaml:"overwrite,omitempty" json:"overwrite,omitempty"`

	includeRE *regexp.Regexp
	excludeRE *regexp.Regexp
}

func Default() Config {
	return Config{
		OpenList: OpenListConfig{
			BaseURL:        defaultOpenListBaseURL,
			RequestTimeout: 15 * time.Second,
			Retry:          3,
			RetryBackoff:   2 * time.Second,
			ListPerPage:    200,
			RateLimitQPS:   0,
			RateLimitBurst: 1,
		},
		Emby: EmbyConfig{
			BaseURL:        defaultEmbyBaseURL,
			ValidatePath:   defaultEmbyValidatePath,
			RequestTimeout: 15 * time.Second,
			TokenCacheTTL:  30 * time.Second,
		},
		Redirect: RedirectConfig{
			DirectPlay:       true,
			DirectPlayWeb:    false,
			FastPlaybackInfo: false,
			ListenAddr:       defaultRedirectListenAddr,
			PublicURL:        defaultRedirectPublicURL,
			PlayTicketTTL:    12 * time.Hour,
			RoutePrefix:      defaultRoutePrefix,
		},
		Sync: SyncConfig{
			BaseDir:             defaultBaseDir,
			IndexDB:             defaultIndexDB,
			FullRescanInterval:  24 * time.Hour,
			MaxDirsPerCycle:     200,
			MaxRequestsPerCycle: 1000,
			MinFileSize:         defaultMinFileSize,
			VideoExtsRaw:        []string{".mp4", ".mkv", ".avi", ".ts", ".mov", ".wmv", ".flv", ".mpg"},
			CleanRemoved:        true,
			Overwrite:           false,
			LogLevel:            "info",
			HotInterval:         30 * time.Minute,
			WarmInterval:        6 * time.Hour,
			ColdInterval:        24 * time.Hour,
			HotJitter:           10 * time.Minute,
			WarmJitter:          time.Hour,
			ColdJitter:          4 * time.Hour,
			UnchangedToWarm:     3,
			UnchangedToCold:     7,
			FailureBackoffMax:   24 * time.Hour,
			RuleCooldown:        6 * time.Hour,
		},
		Rules: []Rule{},
	}
}

func EnsureConfigFile(path string) (bool, error) {
	if strings.TrimSpace(path) == "" {
		path = defaultConfigFile
	}
	if _, err := os.Stat(path); err == nil {
		return false, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	if err := SaveToFile(path, Default()); err != nil {
		return false, err
	}
	return true, nil
}

func Load() (Config, error) {
	return LoadFromFile(defaultConfigFile)
}

func LoadFromFile(path string) (Config, error) {
	if strings.TrimSpace(path) == "" {
		path = defaultConfigFile
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config file: %w", err)
	}
	cfg := Default()
	if err := yaml.Unmarshal(content, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse config file: %w", err)
	}
	return Normalize(cfg), nil
}

func SaveToFile(path string, cfg Config) error {
	if strings.TrimSpace(path) == "" {
		path = defaultConfigFile
	}
	cfg = Normalize(cfg)
	body, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config file: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return writeFileAtomic(path, body)
}

func Normalize(cfg Config) Config {
	defaults := Default()

	if strings.TrimSpace(cfg.OpenList.BaseURL) == "" {
		cfg.OpenList.BaseURL = defaults.OpenList.BaseURL
	}
	cfg.OpenList.PublicURL = strings.TrimSpace(cfg.OpenList.PublicURL)
	cfg.OpenList.Token = strings.TrimSpace(cfg.OpenList.Token)
	cfg.OpenList.Username = strings.TrimSpace(cfg.OpenList.Username)
	cfg.OpenList.Password = strings.TrimSpace(cfg.OpenList.Password)
	if cfg.OpenList.RequestTimeout <= 0 {
		cfg.OpenList.RequestTimeout = defaults.OpenList.RequestTimeout
	}
	if cfg.OpenList.Retry <= 0 {
		cfg.OpenList.Retry = defaults.OpenList.Retry
	}
	if cfg.OpenList.RetryBackoff <= 0 {
		cfg.OpenList.RetryBackoff = defaults.OpenList.RetryBackoff
	}
	if cfg.OpenList.ListPerPage <= 0 {
		cfg.OpenList.ListPerPage = defaults.OpenList.ListPerPage
	}
	if cfg.OpenList.RateLimitBurst <= 0 {
		cfg.OpenList.RateLimitBurst = defaults.OpenList.RateLimitBurst
	}

	if strings.TrimSpace(cfg.Emby.BaseURL) == "" {
		cfg.Emby.BaseURL = defaults.Emby.BaseURL
	}
	if strings.TrimSpace(cfg.Emby.ValidatePath) == "" {
		cfg.Emby.ValidatePath = defaults.Emby.ValidatePath
	}
	if !strings.HasPrefix(cfg.Emby.ValidatePath, "/") {
		cfg.Emby.ValidatePath = "/" + cfg.Emby.ValidatePath
	}
	if cfg.Emby.RequestTimeout <= 0 {
		cfg.Emby.RequestTimeout = defaults.Emby.RequestTimeout
	}
	if cfg.Emby.TokenCacheTTL <= 0 {
		cfg.Emby.TokenCacheTTL = defaults.Emby.TokenCacheTTL
	}

	if strings.TrimSpace(cfg.Redirect.ListenAddr) == "" {
		cfg.Redirect.ListenAddr = defaults.Redirect.ListenAddr
	}
	if strings.TrimSpace(cfg.Redirect.PublicURL) == "" {
		cfg.Redirect.PublicURL = defaults.Redirect.PublicURL
	}
	if strings.TrimSpace(cfg.Redirect.RoutePrefix) == "" {
		cfg.Redirect.RoutePrefix = defaults.Redirect.RoutePrefix
	}
	if !strings.HasPrefix(cfg.Redirect.RoutePrefix, "/") {
		cfg.Redirect.RoutePrefix = "/" + cfg.Redirect.RoutePrefix
	}
	if cfg.Redirect.PlayTicketTTL <= 0 {
		cfg.Redirect.PlayTicketTTL = defaults.Redirect.PlayTicketTTL
	}
	cfg.Redirect.DirectPlayUserSet = parseStringSet(cfg.Redirect.DirectPlayUsers)

	if strings.TrimSpace(cfg.Sync.BaseDir) == "" {
		cfg.Sync.BaseDir = defaults.Sync.BaseDir
	}
	if !filepath.IsAbs(cfg.Sync.BaseDir) {
		cfg.Sync.BaseDir = filepath.Clean(filepath.Join(string(os.PathSeparator), cfg.Sync.BaseDir))
	}
	if strings.TrimSpace(cfg.Sync.IndexDB) == "" {
		cfg.Sync.IndexDB = defaults.Sync.IndexDB
	}
	if cfg.Sync.FullRescanInterval <= 0 {
		cfg.Sync.FullRescanInterval = defaults.Sync.FullRescanInterval
	}
	if cfg.Sync.MaxDirsPerCycle <= 0 {
		cfg.Sync.MaxDirsPerCycle = defaults.Sync.MaxDirsPerCycle
	}
	if cfg.Sync.MaxRequestsPerCycle <= 0 {
		cfg.Sync.MaxRequestsPerCycle = defaults.Sync.MaxRequestsPerCycle
	}
	if cfg.Sync.MinFileSize <= 0 {
		cfg.Sync.MinFileSize = defaults.Sync.MinFileSize
	}
	if len(cfg.Sync.VideoExtsRaw) == 0 {
		cfg.Sync.VideoExtsRaw = append([]string(nil), defaults.Sync.VideoExtsRaw...)
	}
	cfg.Sync.VideoExts = parseExtSet(cfg.Sync.VideoExtsRaw)
	if strings.TrimSpace(cfg.Sync.LogLevel) == "" {
		cfg.Sync.LogLevel = defaults.Sync.LogLevel
	}
	cfg.Sync.LogLevel = strings.ToLower(strings.TrimSpace(cfg.Sync.LogLevel))
	if cfg.Sync.HotInterval <= 0 {
		cfg.Sync.HotInterval = defaults.Sync.HotInterval
	}
	if cfg.Sync.WarmInterval <= 0 {
		cfg.Sync.WarmInterval = defaults.Sync.WarmInterval
	}
	if cfg.Sync.ColdInterval <= 0 {
		cfg.Sync.ColdInterval = defaults.Sync.ColdInterval
	}
	if cfg.Sync.HotJitter < 0 {
		cfg.Sync.HotJitter = defaults.Sync.HotJitter
	}
	if cfg.Sync.WarmJitter < 0 {
		cfg.Sync.WarmJitter = defaults.Sync.WarmJitter
	}
	if cfg.Sync.ColdJitter < 0 {
		cfg.Sync.ColdJitter = defaults.Sync.ColdJitter
	}
	if cfg.Sync.UnchangedToWarm <= 0 {
		cfg.Sync.UnchangedToWarm = defaults.Sync.UnchangedToWarm
	}
	if cfg.Sync.UnchangedToCold <= 0 {
		cfg.Sync.UnchangedToCold = defaults.Sync.UnchangedToCold
	}
	if cfg.Sync.FailureBackoffMax <= 0 {
		cfg.Sync.FailureBackoffMax = defaults.Sync.FailureBackoffMax
	}
	if cfg.Sync.RuleCooldown <= 0 {
		cfg.Sync.RuleCooldown = defaults.Sync.RuleCooldown
	}

	for i := range cfg.Rules {
		if cfg.Rules[i].Flatten == nil {
			cfg.Rules[i].Flatten = boolPtr(false)
		}
	}

	return cfg
}

func Validate(cfg Config) (Config, error) {
	cfg = Normalize(cfg)

	if strings.TrimSpace(cfg.OpenList.BaseURL) == "" {
		return Config{}, errors.New("openlist.base_url is required")
	}
	if cfg.OpenList.Token == "" && (cfg.OpenList.Username == "" || cfg.OpenList.Password == "") {
		return Config{}, errors.New("openlist.token or openlist.username/openlist.password is required")
	}
	if cfg.OpenList.RateLimitQPS < 0 {
		return Config{}, errors.New("openlist.rate_limit_qps must be greater than or equal to zero")
	}

	if cfg.Redirect.PlayTicketTTL <= 0 {
		return Config{}, errors.New("redirect.play_ticket_ttl must be greater than zero")
	}
	if cfg.Sync.FullRescanInterval <= 0 {
		return Config{}, errors.New("sync.full_rescan_interval must be greater than zero")
	}
	if cfg.Sync.HotInterval <= 0 || cfg.Sync.WarmInterval <= 0 || cfg.Sync.ColdInterval <= 0 {
		return Config{}, errors.New("adaptive sync intervals must be greater than zero")
	}
	if cfg.Sync.HotJitter < 0 || cfg.Sync.WarmJitter < 0 || cfg.Sync.ColdJitter < 0 {
		return Config{}, errors.New("adaptive sync jitter must be greater than or equal to zero")
	}
	if cfg.Sync.UnchangedToWarm <= 0 || cfg.Sync.UnchangedToCold < cfg.Sync.UnchangedToWarm {
		return Config{}, errors.New("adaptive sync unchanged thresholds are invalid")
	}
	if cfg.Sync.FailureBackoffMax <= 0 {
		return Config{}, errors.New("sync.failure_backoff_max must be greater than zero")
	}
	if cfg.Sync.RuleCooldown <= 0 {
		return Config{}, errors.New("sync.rule_cooldown must be greater than zero")
	}
	if len(cfg.Rules) == 0 {
		return Config{}, errors.New("at least one rule is required")
	}

	normalizedRules := make([]Rule, 0, len(cfg.Rules))
	for _, rule := range cfg.Rules {
		if err := finalizeRule(&rule, cfg.Sync); err != nil {
			return Config{}, err
		}
		normalizedRules = append(normalizedRules, rule)
	}
	sort.Slice(normalizedRules, func(i, j int) bool {
		return normalizedRules[i].SourcePath < normalizedRules[j].SourcePath
	})
	if err := validateRuleNames(normalizedRules); err != nil {
		return Config{}, err
	}
	cfg.Rules = normalizedRules

	if cfg.Redirect.PlayTicketSecret == "" {
		secret, err := randomSecret(32)
		if err != nil {
			return Config{}, fmt.Errorf("generate play ticket secret: %w", err)
		}
		cfg.Redirect.PlayTicketSecret = secret
		cfg.Redirect.EphemeralSecret = true
	}

	cfg.Redirect.DirectPlayUserSet = parseStringSet(cfg.Redirect.DirectPlayUsers)

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

func parseStringSet(values []string) map[string]struct{} {
	if len(values) == 0 {
		return nil
	}
	result := make(map[string]struct{})
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			result[value] = struct{}{}
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func parseExtSet(values []string) map[string]struct{} {
	result := make(map[string]struct{})
	for _, part := range values {
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

func writeFileAtomic(target string, content []byte) error {
	tempFile, err := os.CreateTemp(filepath.Dir(target), ".emby-pro-*")
	if err != nil {
		return err
	}
	tempPath := tempFile.Name()
	defer os.Remove(tempPath)

	if _, err := tempFile.Write(content); err != nil {
		tempFile.Close()
		return err
	}
	if err := tempFile.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, target)
}

func boolPtr(value bool) *bool {
	return &value
}

func randomSecret(size int) (string, error) {
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
