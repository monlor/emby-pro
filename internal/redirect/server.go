package redirect

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/monlor/emby-pro/internal/config"
	"github.com/monlor/emby-pro/internal/emby"
	"github.com/monlor/emby-pro/internal/httpx"
	"github.com/monlor/emby-pro/internal/index"
	"github.com/monlor/emby-pro/internal/openlist"
	"github.com/monlor/emby-pro/internal/pathutil"
)

type tokenCacheEntry struct {
	expiry   time.Time
	userInfo *emby.UserInfo
}

type runtimeState struct {
	cfg        config.RedirectConfig
	client     *openlist.Client
	embyClient *emby.Client
	builder    *Builder
}

type AdminRescanResult struct {
	RuleName  string `json:"rule_name"`
	Scheduled bool   `json:"scheduled"`
	Pending   bool   `json:"pending"`
	NotFound  bool   `json:"not_found"`
}

type AdminCallbacks struct {
	CurrentConfig          func() config.Config
	RuntimeError           func() string
	RecentLogs             func() []string
	ListRuleStates         func() ([]index.RuleState, error)
	ListExistingTargetDirs func() ([]string, error)
	SaveConfig             func(config.Config) (config.Config, error)
	RequestFullRescan      func(string) (AdminRescanResult, error)
	RequestFullRescanAll   func() ([]AdminRescanResult, error)
	ForceRewriteRule       func(string) (AdminRescanResult, error)
}

type Server struct {
	logger *log.Logger
	admin  AdminCallbacks

	runtimeMu sync.RWMutex
	runtime   runtimeState
	builder   *Builder

	cacheMu    sync.Mutex
	tokenCache map[string]tokenCacheEntry
	tokenTTL   time.Duration

	proxy *httputil.ReverseProxy
}

var embyTokenPattern = regexp.MustCompile(`(?i)token="?([^", ]+)"?`)
var embyDeviceIDPattern = regexp.MustCompile(`(?i)deviceid="?([^", ]+)"?`)
var embyClientPattern = regexp.MustCompile(`(?i)client="?([^",]+)"?`)
var playbackInfoPathPattern = regexp.MustCompile(`(?i)^(?:/emby)?/Items/([^/]+)/PlaybackInfo$`)

type adminConfigResponse struct {
	Config             adminConfigPayload           `json:"config"`
	RuleStates         []index.RuleState            `json:"rule_states"`
	RuntimeError       string                       `json:"runtime_error,omitempty"`
	User               adminUserPayload             `json:"user"`
	EmbyUsers          []adminSelectableUserPayload `json:"emby_users"`
	ExistingTargetDirs []string                     `json:"existing_target_dirs"`
	Logs               []string                     `json:"logs"`
}

type adminStatusResponse struct {
	RuleStates   []index.RuleState `json:"rule_states"`
	RuntimeError string            `json:"runtime_error,omitempty"`
	User         adminUserPayload  `json:"user"`
	Logs         []string          `json:"logs"`
}

type adminUserPayload struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	IsAdmin bool   `json:"is_admin"`
}

type adminSelectableUserPayload struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	IsAdmin bool   `json:"is_admin"`
}

type adminOpenListDirPayload struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type adminConfigPayload struct {
	OpenList adminOpenListPayload `json:"openlist"`
	Emby     adminEmbyPayload     `json:"emby"`
	Redirect adminRedirectPayload `json:"redirect"`
	Sync     adminSyncPayload     `json:"sync"`
	Rules    []adminRulePayload   `json:"rules"`
}

type adminOpenListPayload struct {
	BaseURL            string  `json:"base_url"`
	PublicURL          string  `json:"public_url"`
	Token              string  `json:"token"`
	Username           string  `json:"username"`
	Password           string  `json:"password"`
	RequestTimeout     string  `json:"request_timeout"`
	Retry              int     `json:"retry"`
	RetryBackoff       string  `json:"retry_backoff"`
	ListPerPage        int     `json:"list_per_page"`
	RateLimitQPS       float64 `json:"rate_limit_qps"`
	RateLimitBurst     int     `json:"rate_limit_burst"`
	InsecureSkipVerify bool    `json:"insecure_skip_verify"`
	DisableHTTP2       bool    `json:"disable_http2"`
}

type adminEmbyPayload struct {
	BaseURL        string `json:"base_url"`
	ValidatePath   string `json:"validate_path"`
	RequestTimeout string `json:"request_timeout"`
	TokenCacheTTL  string `json:"token_cache_ttl"`
}

type adminRedirectPayload struct {
	DirectPlay       bool     `json:"direct_play"`
	DirectPlayWeb    bool     `json:"direct_play_web"`
	FastPlaybackInfo bool     `json:"fast_playback_info"`
	DirectPlayUsers  []string `json:"direct_play_users"`
	PublicURL        string   `json:"public_url"`
	PlayTicketSecret string   `json:"play_ticket_secret"`
	PlayTicketTTL    string   `json:"play_ticket_ttl"`
	RoutePrefix      string   `json:"route_prefix"`
}

type adminSyncPayload struct {
	BaseDir             string   `json:"base_dir"`
	IndexDB             string   `json:"index_db"`
	FullRescanInterval  string   `json:"full_rescan_interval"`
	MaxDirsPerCycle     int      `json:"max_dirs_per_cycle"`
	MaxRequestsPerCycle int      `json:"max_requests_per_cycle"`
	MinFileSize         int64    `json:"min_file_size"`
	VideoExts           []string `json:"video_exts"`
	CleanRemoved        bool     `json:"clean_removed"`
	Overwrite           bool     `json:"overwrite"`
	LogLevel            string   `json:"log_level"`
	HotInterval         string   `json:"hot_interval"`
	WarmInterval        string   `json:"warm_interval"`
	ColdInterval        string   `json:"cold_interval"`
	HotJitter           string   `json:"hot_jitter"`
	WarmJitter          string   `json:"warm_jitter"`
	ColdJitter          string   `json:"cold_jitter"`
	UnchangedToWarm     int      `json:"unchanged_to_warm"`
	UnchangedToCold     int      `json:"unchanged_to_cold"`
	FailureBackoffMax   string   `json:"failure_backoff_max"`
	RuleCooldown        string   `json:"rule_cooldown"`
}

type adminRulePayload struct {
	Name         string `json:"name"`
	SourcePath   string `json:"source_path"`
	TargetPath   string `json:"target_path"`
	Flatten      bool   `json:"flatten"`
	IncludeRegex string `json:"include_regex"`
	ExcludeRegex string `json:"exclude_regex"`
}

func NewServer(cfg config.RedirectConfig, client *openlist.Client, embyClient *emby.Client, admin AdminCallbacks, logger *log.Logger, tokenTTL time.Duration) *Server {
	if tokenTTL <= 0 {
		tokenTTL = 30 * time.Second
	}
	serverCfg := config.RedirectConfig{
		DirectPlay:        cfg.DirectPlay,
		DirectPlayWeb:     cfg.DirectPlayWeb,
		FastPlaybackInfo:  cfg.FastPlaybackInfo,
		DirectPlayUsers:   append([]string(nil), cfg.DirectPlayUsers...),
		DirectPlayUserSet: cfg.DirectPlayUserSet,
		ListenAddr:        cfg.ListenAddr,
		PublicURL:         cfg.PublicURL,
		PlayTicketSecret:  cfg.PlayTicketSecret,
		PlayTicketTTL:     cfg.PlayTicketTTL,
		RoutePrefix:       defaultRoutePrefix(cfg.RoutePrefix),
	}
	server := &Server{
		logger:     logger,
		admin:      admin,
		tokenCache: make(map[string]tokenCacheEntry),
		tokenTTL:   tokenTTL,
	}
	proxy := httputil.NewSingleHostReverseProxy(embyClient.BaseURL())
	proxy.Director = func(req *http.Request) {
		snapshot := server.snapshot()
		target := snapshot.embyClient.BaseURL()
		req.URL.Scheme = target.Scheme
		req.URL.Host = target.Host
		resolved := snapshot.embyClient.ResolveRequestURI(req.URL.RequestURI())
		req.URL.Path = resolved.Path
		req.URL.RawPath = resolved.RawPath
		req.URL.RawQuery = resolved.RawQuery
		req.URL.Fragment = resolved.Fragment
		req.Host = target.Host
	}
	server.proxy = proxy
	server.UpdateRuntime(serverCfg, client, embyClient)
	return server
}

func (s *Server) UpdateRuntime(cfg config.RedirectConfig, client *openlist.Client, embyClient *emby.Client) {
	s.runtimeMu.Lock()
	defer s.runtimeMu.Unlock()
	builder := NewBuilder(cfg)
	s.runtime = runtimeState{
		cfg:        cfg,
		client:     client,
		embyClient: embyClient,
		builder:    builder,
	}
	s.builder = builder
}

func (s *Server) snapshot() runtimeState {
	s.runtimeMu.RLock()
	defer s.runtimeMu.RUnlock()
	return s.runtime
}

func (s *Server) Run(ctx context.Context) error {
	snapshot := s.snapshot()
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.HandleFunc("/admin", s.handleAdminPage)
	mux.HandleFunc("/admin/", s.handleAdminPage)
	mux.HandleFunc("/admin/api/config", s.handleAdminConfig)
	mux.HandleFunc("/admin/api/openlist-dirs", s.handleAdminOpenListDirs)
	mux.HandleFunc("/admin/api/status", s.handleAdminStatus)
	mux.HandleFunc("/admin/api/full-scan", s.handleAdminFullScan)
	mux.HandleFunc("/admin/api/rescan", s.handleAdminRescan)
	mux.HandleFunc("/admin/api/rescan/", s.handleAdminRescan)
	mux.HandleFunc(snapshot.cfg.RoutePrefix+"/", s.handleSTRM)
	mux.HandleFunc("/", s.handleProxyEmby)

	httpServer := &http.Server{
		Addr:              snapshot.cfg.ListenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
	}()

	s.logger.Printf("[INFO] redirect server listening on %s", snapshot.cfg.ListenAddr)
	err := httpServer.ListenAndServe()
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (s *Server) handleAdminPage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, adminPageHTML)
}

func (s *Server) handleAdminConfig(w http.ResponseWriter, r *http.Request) {
	user, status, err := s.requireAdmin(r)
	if err != nil {
		http.Error(w, err.Error(), status)
		return
	}
	if s.admin.CurrentConfig == nil || s.admin.SaveConfig == nil || s.admin.ListRuleStates == nil || s.admin.RuntimeError == nil || s.admin.RecentLogs == nil {
		http.Error(w, "admin controller unavailable", http.StatusServiceUnavailable)
		return
	}

	switch r.Method {
	case http.MethodGet:
		resp, err := s.adminSnapshot(r, user)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, resp)
	case http.MethodPost:
		var payload adminConfigPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, "invalid json body", http.StatusBadRequest)
			return
		}
		cfg, err := payload.toConfig()
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if _, err := s.admin.SaveConfig(cfg); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		resp, err := s.adminSnapshot(r, user)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, resp)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleAdminOpenListDirs(w http.ResponseWriter, r *http.Request) {
	if _, status, err := s.requireAdmin(r); err != nil {
		http.Error(w, err.Error(), status)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	snapshot := s.snapshot()
	dirPath := pathutil.NormalizeSourcePath(strings.TrimSpace(r.URL.Query().Get("path")))
	if dirPath == "" {
		dirPath = "/"
	}

	dirs, err := s.listOpenListDirs(r.Context(), snapshot.client, dirPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"path": dirPath,
		"dirs": dirs,
	})
}

func (s *Server) handleAdminStatus(w http.ResponseWriter, r *http.Request) {
	user, status, err := s.requireAdmin(r)
	if err != nil {
		http.Error(w, err.Error(), status)
		return
	}
	if s.admin.ListRuleStates == nil || s.admin.RuntimeError == nil || s.admin.RecentLogs == nil {
		http.Error(w, "admin controller unavailable", http.StatusServiceUnavailable)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	resp, err := s.adminStatus(user)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleAdminRescan(w http.ResponseWriter, r *http.Request) {
	if _, status, err := s.requireAdmin(r); err != nil {
		http.Error(w, err.Error(), status)
		return
	}
	if s.admin.RequestFullRescan == nil || s.admin.RequestFullRescanAll == nil {
		http.Error(w, "admin controller unavailable", http.StatusServiceUnavailable)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ruleName := strings.TrimPrefix(r.URL.Path, "/admin/api/rescan")
	ruleName = strings.Trim(ruleName, "/")
	if ruleName == "" {
		results, err := s.admin.RequestFullRescanAll()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"results": results})
		return
	}

	result, err := s.admin.RequestFullRescan(ruleName)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if result.NotFound {
		http.Error(w, "rule not found", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleAdminFullScan(w http.ResponseWriter, r *http.Request) {
	if _, status, err := s.requireAdmin(r); err != nil {
		http.Error(w, err.Error(), status)
		return
	}
	if s.admin.ForceRewriteRule == nil {
		http.Error(w, "admin controller unavailable", http.StatusServiceUnavailable)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ruleName := strings.TrimSpace(r.URL.Query().Get("rule"))
	if ruleName == "" {
		http.Error(w, "missing rule", http.StatusBadRequest)
		return
	}
	result, err := s.admin.ForceRewriteRule(ruleName)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if result.NotFound {
		http.Error(w, "rule not found", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) requireAdmin(r *http.Request) (*emby.UserInfo, int, error) {
	snapshot := s.snapshot()
	token := extractEmbyToken(r)
	if token == "" {
		return nil, http.StatusUnauthorized, fmt.Errorf("missing emby token")
	}

	userInfo := s.getCachedUserInfo(token)
	if userInfo == nil || !userInfo.IsAdmin {
		info, err := snapshot.embyClient.GetUserInfo(r.Context(), token, extractEmbyDeviceID(r))
		if err != nil {
			return nil, http.StatusUnauthorized, err
		}
		s.cacheTokenWithUser(token, info)
		userInfo = info
	}
	if !userInfo.IsAdmin {
		return nil, http.StatusForbidden, fmt.Errorf("emby admin required")
	}
	return userInfo, http.StatusOK, nil
}

func (s *Server) adminSnapshot(r *http.Request, user *emby.UserInfo) (adminConfigResponse, error) {
	cfg := s.admin.CurrentConfig()
	status, err := s.adminStatus(user)
	if err != nil {
		return adminConfigResponse{}, err
	}
	resp := adminConfigResponse{
		Config:             newAdminConfigPayload(cfg),
		RuleStates:         status.RuleStates,
		RuntimeError:       status.RuntimeError,
		User:               status.User,
		Logs:               status.Logs,
		ExistingTargetDirs: []string{},
		EmbyUsers:          []adminSelectableUserPayload{},
	}
	if s.admin.ListExistingTargetDirs != nil {
		dirs, err := s.admin.ListExistingTargetDirs()
		if err != nil {
			s.logger.Printf("[WARN] list existing target dirs for admin page: %v", err)
		} else {
			resp.ExistingTargetDirs = dirs
		}
	}
	token := extractEmbyToken(r)
	if token != "" {
		users, err := s.snapshot().embyClient.ListUsers(r.Context(), token)
		if err != nil {
			s.logger.Printf("[WARN] list emby users for admin page: %v", err)
		} else {
			resp.EmbyUsers = make([]adminSelectableUserPayload, 0, len(users))
			for _, item := range users {
				resp.EmbyUsers = append(resp.EmbyUsers, adminSelectableUserPayload{
					ID:      item.ID,
					Name:    item.Name,
					IsAdmin: item.IsAdmin,
				})
			}
		}
	}
	return resp, nil
}

func (s *Server) adminStatus(user *emby.UserInfo) (adminStatusResponse, error) {
	states, err := s.admin.ListRuleStates()
	if err != nil {
		return adminStatusResponse{}, err
	}
	resp := adminStatusResponse{
		RuleStates:   states,
		RuntimeError: s.admin.RuntimeError(),
		Logs:         s.admin.RecentLogs(),
	}
	if user != nil {
		resp.User = adminUserPayload{
			ID:      user.ID,
			Name:    user.Name,
			IsAdmin: user.IsAdmin,
		}
	}
	return resp, nil
}

func (s *Server) listOpenListDirs(ctx context.Context, client *openlist.Client, dirPath string) ([]adminOpenListDirPayload, error) {
	if client == nil {
		return nil, fmt.Errorf("openlist client unavailable")
	}
	pageNum := 1
	perPage := 200
	dirs := make([]adminOpenListDirPayload, 0, 32)
	for {
		page, err := client.ListPage(ctx, dirPath, pageNum, perPage)
		if err != nil {
			return nil, err
		}
		if len(page.Content) == 0 {
			break
		}
		for _, entry := range page.Content {
			if !entry.IsDir {
				continue
			}
			childPath := pathutil.NormalizeSourcePath(path.Join(dirPath, entry.Name))
			if childPath == "" {
				continue
			}
			dirs = append(dirs, adminOpenListDirPayload{
				Name: entry.Name,
				Path: childPath,
			})
		}
		if len(page.Content) < perPage || len(page.Content) >= page.Total || page.Total <= len(dirs) {
			break
		}
		pageNum++
	}
	sort.Slice(dirs, func(i, j int) bool {
		return dirs[i].Name < dirs[j].Name
	})
	return dirs, nil
}

func (s *Server) handleProxyEmby(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/admin") {
		http.NotFound(w, r)
		return
	}

	if provider, _, ok := s.sourcePathFromRoute(r); ok && provider == openListProvider {
		s.handleSTRM(w, r)
		return
	}

	if playbackInfoPathPattern.MatchString(r.URL.Path) {
		s.handlePlaybackInfo(w, r)
		return
	}

	s.proxy.ServeHTTP(w, r)
}

func (s *Server) isDirectPlayEnabled(r *http.Request) bool {
	snapshot := s.snapshot()
	if !snapshot.cfg.DirectPlay {
		return false
	}
	if isWebClientRequest(r) && !snapshot.cfg.DirectPlayWeb {
		return false
	}
	if len(snapshot.cfg.DirectPlayUsers) == 0 {
		return true
	}

	token := extractEmbyToken(r)
	if token == "" {
		return false
	}

	userInfo := s.getCachedUserInfo(token)
	if userInfo == nil {
		info, err := snapshot.embyClient.GetUserInfo(r.Context(), token, extractEmbyDeviceID(r))
		if err != nil {
			s.logger.Printf("[WARN] get user info for direct play check: %v", err)
			return false
		}
		s.cacheTokenWithUser(token, info)
		userInfo = info
	}

	_, byID := snapshot.cfg.DirectPlayUserSet[userInfo.ID]
	_, byName := snapshot.cfg.DirectPlayUserSet[userInfo.Name]
	return byID || byName
}

func (s *Server) isFastPlaybackInfoEnabled(r *http.Request) bool {
	return s.isDirectPlayEnabled(r)
}

func (s *Server) handleSTRM(w http.ResponseWriter, r *http.Request) {
	provider, sourcePath, ok := s.sourcePathFromRoute(r)
	if !ok || provider != openListProvider {
		http.NotFound(w, r)
		return
	}

	if status, err := s.authorizePlayTicket(r, provider, sourcePath); err != nil {
		http.Error(w, err.Error(), status)
		return
	}

	s.redirectToTarget(w, r, provider, sourcePath)
}

func (s *Server) handlePlaybackInfo(w http.ResponseWriter, r *http.Request) {
	snapshot := s.snapshot()
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read request body", http.StatusBadRequest)
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(bodyBytes))

	directPlay := s.isDirectPlayEnabled(r)
	if directPlay && snapshot.cfg.FastPlaybackInfo && s.isFastPlaybackInfoEnabled(r) {
		fastBody, handled, err := s.buildFastPlaybackInfo(r)
		if err != nil {
			s.logger.Printf("[WARN] fast playbackinfo fallback: %v", err)
		} else if handled {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(fastBody)
			return
		}
	}

	resp, respBody, err := snapshot.embyClient.RawRequest(r.Context(), r.Method, r.URL.RequestURI(), r.Header, bodyBytes)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	mediaInfo, err := s.maybeRewritePlaybackInfo(r, respBody, directPlay)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	httpx.CopyHeaders(w.Header(), resp.Header)
	w.Header().Del("Content-Length")
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(mediaInfo)
}

func (s *Server) buildFastPlaybackInfo(r *http.Request) ([]byte, bool, error) {
	snapshot := s.snapshot()
	token := extractEmbyToken(r)
	if token == "" {
		return nil, false, nil
	}

	itemID := extractItemID(r.URL.Path)
	if itemID == "" {
		return nil, false, nil
	}

	itemRequestURI := buildItemRequestURI(r, itemID)
	itemPayload, err := snapshot.embyClient.FetchItem(r.Context(), itemRequestURI, token)
	if err != nil {
		return nil, false, err
	}

	sources, ok := itemPayload["MediaSources"].([]any)
	if !ok || len(sources) == 0 {
		return nil, false, fmt.Errorf("item payload missing mediasources")
	}

	filteredSources := filterMediaSourcesByID(sources, strings.TrimSpace(r.URL.Query().Get("MediaSourceId")))
	if len(filteredSources) == 0 {
		return nil, false, fmt.Errorf("no mediasource matched request")
	}

	chapters, _ := itemPayload["Chapters"].([]any)
	managed := false
	for _, rawSource := range filteredSources {
		source, ok := rawSource.(map[string]any)
		if !ok {
			continue
		}
		if len(chapters) > 0 {
			if _, hasChapters := source["Chapters"]; !hasChapters {
				source["Chapters"] = chapters
			}
		}
		pathValue, _ := source["Path"].(string)
		managedSource, _, ok := s.resolveManagedMediaPath(pathValue)
		if ok && managedSource != "" {
			managed = true
		}
	}
	if !managed {
		return nil, false, nil
	}

	payload := map[string]any{
		"MediaSources":  filteredSources,
		"PlaySessionId": uuid.NewString(),
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, false, err
	}

	rewritten, err := s.maybeRewritePlaybackInfo(r, body, true)
	if err != nil {
		return nil, false, err
	}
	return rewritten, true, nil
}

func (s *Server) redirectToTarget(w http.ResponseWriter, r *http.Request, provider, sourcePath string) {
	snapshot := s.snapshot()
	if provider != openListProvider {
		http.NotFound(w, r)
		return
	}

	entry, err := snapshot.client.Get(r.Context(), sourcePath)
	if err != nil {
		http.Error(w, fmt.Sprintf("resolve source path: %v", err), http.StatusBadGateway)
		return
	}

	http.Redirect(w, r, snapshot.client.DownloadURL(entry, sourcePath), http.StatusFound)
}

func (s *Server) authorizePlayTicket(r *http.Request, provider, sourcePath string) (int, error) {
	snapshot := s.snapshot()
	token := strings.TrimSpace(r.URL.Query().Get(playTicketParam))
	if token == "" {
		if isLoopbackRequest(r) {
			return 0, nil
		}
		return http.StatusForbidden, fmt.Errorf("missing play ticket")
	}

	claims, err := decodePlayTicket([]byte(snapshot.cfg.PlayTicketSecret), token, time.Now())
	if err != nil {
		return http.StatusForbidden, err
	}
	if claims.Provider != provider || claims.SourcePath != sourcePath {
		return http.StatusForbidden, fmt.Errorf("play ticket does not match route")
	}
	return 0, nil
}

func isLoopbackRequest(r *http.Request) bool {
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err != nil {
		host = strings.TrimSpace(r.RemoteAddr)
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (s *Server) maybeRewritePlaybackInfo(r *http.Request, body []byte, directPlay bool) ([]byte, error) {
	snapshot := s.snapshot()
	if len(body) == 0 {
		return body, nil
	}
	if !directPlay {
		return body, nil
	}

	token := extractEmbyToken(r)
	if token == "" {
		return body, nil
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return body, nil
	}

	itemID := extractItemID(r.URL.Path)
	if itemID == "" {
		return body, nil
	}

	sources, ok := payload["MediaSources"].([]any)
	if !ok || len(sources) == 0 {
		return body, nil
	}

	rewritten := false
	now := time.Now()
	for _, rawSource := range sources {
		source, ok := rawSource.(map[string]any)
		if !ok {
			continue
		}

		pathValue, _ := source["Path"].(string)
		managedSource, _, ok := s.resolveManagedMediaPath(pathValue)
		if !ok || managedSource == "" {
			continue
		}

		mediaSourceID, _ := source["Id"].(string)
		if mediaSourceID == "" {
			continue
		}

		ticketURL, err := s.buildPlayTicketURL(r, managedSource, now)
		if err != nil {
			return nil, err
		}

		source["Path"] = ticketURL
		rewritten = true

		directURL, err := snapshot.builder.BuildRelativePlayTicket(managedSource, now, snapshot.cfg.PlayTicketTTL)
		if err != nil {
			return nil, err
		}
		source["SupportsDirectPlay"] = true
		source["SupportsDirectStream"] = true
		source["SupportsTranscoding"] = false
		// Emby prepends its own site prefix when consuming DirectStreamUrl, so this
		// must stay as a /strm-relative route instead of including /emby.
		source["DirectStreamUrl"] = directURL
		source["AddApiKeyToDirectStreamUrl"] = false
		delete(source, "TranscodingUrl")
		delete(source, "TranscodingSubProtocol")
		delete(source, "TranscodingContainer")
	}

	if !rewritten {
		return body, nil
	}
	return json.Marshal(payload)
}

func buildItemRequestURI(r *http.Request, itemID string) string {
	itemID = strings.TrimSpace(itemID)
	if itemID == "" {
		return ""
	}

	prefix := requestPathPrefix(r.URL.Path)
	fields := "MediaSources,Chapters,Path,MediaStreams,RunTimeTicks"
	userID := strings.TrimSpace(r.URL.Query().Get("UserId"))
	if userID != "" {
		return fmt.Sprintf("%s/Users/%s/Items/%s?Fields=%s", prefix, url.PathEscape(userID), url.PathEscape(itemID), url.QueryEscape(fields))
	}
	return fmt.Sprintf("%s/Items/%s?Fields=%s", prefix, url.PathEscape(itemID), url.QueryEscape(fields))
}

func filterMediaSourcesByID(sources []any, mediaSourceID string) []any {
	if mediaSourceID == "" {
		return sources
	}

	filtered := make([]any, 0, 1)
	for _, rawSource := range sources {
		source, ok := rawSource.(map[string]any)
		if !ok {
			continue
		}
		sourceID, _ := source["Id"].(string)
		if strings.EqualFold(sourceID, mediaSourceID) {
			filtered = append(filtered, source)
		}
	}
	return filtered
}

func (s *Server) resolveManagedMediaPath(pathValue string) (managedSource string, directRemote string, ok bool) {
	if strings.TrimSpace(pathValue) == "" {
		return "", "", false
	}

	parsed, err := url.Parse(pathValue)
	if err != nil {
		return "", "", false
	}
	if parsed.Scheme == "" {
		return "", "", false
	}

	provider, sourcePath, routeOK := s.sourcePathFromEscapedPath(parsed.EscapedPath())
	if routeOK && provider == openListProvider {
		return sourcePath, "", true
	}

	return "", pathValue, true
}

func (s *Server) isTokenCached(token string) bool {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()

	entry, ok := s.tokenCache[token]
	if !ok {
		return false
	}
	if time.Now().After(entry.expiry) {
		delete(s.tokenCache, token)
		return false
	}
	return true
}

func (s *Server) cacheToken(token string) {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	entry := s.tokenCache[token]
	entry.expiry = time.Now().Add(s.tokenTTL)
	s.tokenCache[token] = entry
}

func (s *Server) cacheTokenWithUser(token string, userInfo *emby.UserInfo) {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	s.tokenCache[token] = tokenCacheEntry{
		expiry:   time.Now().Add(s.tokenTTL),
		userInfo: userInfo,
	}
}

func (s *Server) getCachedUserInfo(token string) *emby.UserInfo {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	entry, ok := s.tokenCache[token]
	if !ok || time.Now().After(entry.expiry) {
		return nil
	}
	return entry.userInfo
}

func (s *Server) sourcePathFromRoute(r *http.Request) (string, string, bool) {
	return s.sourcePathFromEscapedPath(r.URL.EscapedPath())
}

func (s *Server) sourcePathFromEscapedPath(escapedPath string) (string, string, bool) {
	snapshot := s.snapshot()
	prefix := strings.TrimSuffix(snapshot.cfg.RoutePrefix, "/") + "/"
	idx := strings.Index(escapedPath, prefix)
	if idx < 0 {
		return "", "", false
	}

	trimmed := escapedPath[idx+len(prefix):]
	parts := strings.SplitN(trimmed, "/", 2)
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" {
		return "", "", false
	}

	decoded, err := url.PathUnescape(parts[1])
	if err != nil {
		return "", "", false
	}
	publicPath := pathutil.NormalizeSourcePath(decoded)
	return parts[0], publicPath, true
}

func (s *Server) buildPlayTicketURL(r *http.Request, sourcePath string, now time.Time) (string, error) {
	snapshot := s.snapshot()
	publicURL := s.publicURLFromRequest(r)
	if publicURL == "" {
		return snapshot.builder.BuildPlayTicket(sourcePath, now, snapshot.cfg.PlayTicketTTL)
	}
	return snapshot.builder.BuildPlayTicketForPublicURL(publicURL, sourcePath, now, snapshot.cfg.PlayTicketTTL)
}

func (s *Server) publicURLFromRequest(r *http.Request) string {
	snapshot := s.snapshot()
	scheme := firstNonEmpty(
		forwardedHeaderValue(r.Header.Get("Forwarded"), "proto"),
		firstForwardedValue(r.Header.Get("X-Forwarded-Proto")),
	)
	if scheme == "" {
		if r.TLS != nil {
			scheme = "https"
		} else {
			scheme = "http"
		}
	}

	host := firstNonEmpty(
		forwardedHeaderValue(r.Header.Get("Forwarded"), "host"),
		firstForwardedValue(r.Header.Get("X-Forwarded-Host")),
		strings.TrimSpace(r.Host),
		r.URL.Host,
	)
	if host == "" {
		return strings.TrimSpace(snapshot.cfg.PublicURL)
	}

	prefix := normalizeForwardedPrefix(firstNonEmpty(
		firstForwardedValue(r.Header.Get("X-Forwarded-Prefix")),
		requestPathPrefix(r.URL.Path),
	))

	u := url.URL{
		Scheme: scheme,
		Host:   host,
		Path:   prefix,
	}
	return u.String()
}

func newAdminConfigPayload(cfg config.Config) adminConfigPayload {
	rules := make([]adminRulePayload, 0, len(cfg.Rules))
	for _, rule := range cfg.Rules {
		rules = append(rules, adminRulePayload{
			Name:         rule.Name,
			SourcePath:   rule.SourcePath,
			TargetPath:   rule.TargetPath,
			Flatten:      rule.FlattenValue(),
			IncludeRegex: rule.IncludeRegex,
			ExcludeRegex: rule.ExcludeRegex,
		})
	}

	return adminConfigPayload{
		OpenList: adminOpenListPayload{
			BaseURL:            cfg.OpenList.BaseURL,
			PublicURL:          cfg.OpenList.PublicURL,
			Token:              cfg.OpenList.Token,
			Username:           cfg.OpenList.Username,
			Password:           cfg.OpenList.Password,
			RequestTimeout:     cfg.OpenList.RequestTimeout.String(),
			Retry:              cfg.OpenList.Retry,
			RetryBackoff:       cfg.OpenList.RetryBackoff.String(),
			ListPerPage:        cfg.OpenList.ListPerPage,
			RateLimitQPS:       cfg.OpenList.RateLimitQPS,
			RateLimitBurst:     cfg.OpenList.RateLimitBurst,
			InsecureSkipVerify: cfg.OpenList.InsecureSkipVerify,
			DisableHTTP2:       cfg.OpenList.DisableHTTP2,
		},
		Emby: adminEmbyPayload{
			BaseURL:        cfg.Emby.BaseURL,
			ValidatePath:   cfg.Emby.ValidatePath,
			RequestTimeout: cfg.Emby.RequestTimeout.String(),
			TokenCacheTTL:  cfg.Emby.TokenCacheTTL.String(),
		},
		Redirect: adminRedirectPayload{
			DirectPlay:       cfg.Redirect.DirectPlay,
			DirectPlayWeb:    cfg.Redirect.DirectPlayWeb,
			FastPlaybackInfo: cfg.Redirect.FastPlaybackInfo,
			DirectPlayUsers:  append([]string(nil), cfg.Redirect.DirectPlayUsers...),
			PublicURL:        cfg.Redirect.PublicURL,
			PlayTicketSecret: cfg.Redirect.PlayTicketSecret,
			PlayTicketTTL:    cfg.Redirect.PlayTicketTTL.String(),
			RoutePrefix:      cfg.Redirect.RoutePrefix,
		},
		Sync: adminSyncPayload{
			BaseDir:             cfg.Sync.BaseDir,
			IndexDB:             cfg.Sync.IndexDB,
			FullRescanInterval:  cfg.Sync.FullRescanInterval.String(),
			MaxDirsPerCycle:     cfg.Sync.MaxDirsPerCycle,
			MaxRequestsPerCycle: cfg.Sync.MaxRequestsPerCycle,
			MinFileSize:         cfg.Sync.MinFileSize,
			VideoExts:           append([]string(nil), cfg.Sync.VideoExtsRaw...),
			CleanRemoved:        cfg.Sync.CleanRemoved,
			Overwrite:           cfg.Sync.Overwrite,
			LogLevel:            cfg.Sync.LogLevel,
			HotInterval:         cfg.Sync.HotInterval.String(),
			WarmInterval:        cfg.Sync.WarmInterval.String(),
			ColdInterval:        cfg.Sync.ColdInterval.String(),
			HotJitter:           cfg.Sync.HotJitter.String(),
			WarmJitter:          cfg.Sync.WarmJitter.String(),
			ColdJitter:          cfg.Sync.ColdJitter.String(),
			UnchangedToWarm:     cfg.Sync.UnchangedToWarm,
			UnchangedToCold:     cfg.Sync.UnchangedToCold,
			FailureBackoffMax:   cfg.Sync.FailureBackoffMax.String(),
			RuleCooldown:        cfg.Sync.RuleCooldown.String(),
		},
		Rules: rules,
	}
}

func (p adminConfigPayload) toConfig() (config.Config, error) {
	cfg := config.Default()
	cfg.OpenList = config.OpenListConfig{
		BaseURL:            strings.TrimSpace(p.OpenList.BaseURL),
		PublicURL:          strings.TrimSpace(p.OpenList.PublicURL),
		Token:              strings.TrimSpace(p.OpenList.Token),
		Username:           strings.TrimSpace(p.OpenList.Username),
		Password:           strings.TrimSpace(p.OpenList.Password),
		Retry:              p.OpenList.Retry,
		ListPerPage:        p.OpenList.ListPerPage,
		RateLimitQPS:       p.OpenList.RateLimitQPS,
		RateLimitBurst:     p.OpenList.RateLimitBurst,
		InsecureSkipVerify: p.OpenList.InsecureSkipVerify,
		DisableHTTP2:       p.OpenList.DisableHTTP2,
	}
	cfg.Emby = config.EmbyConfig{
		BaseURL:      strings.TrimSpace(p.Emby.BaseURL),
		ValidatePath: strings.TrimSpace(p.Emby.ValidatePath),
	}
	cfg.Redirect = config.RedirectConfig{
		DirectPlay:       p.Redirect.DirectPlay,
		DirectPlayWeb:    p.Redirect.DirectPlayWeb,
		FastPlaybackInfo: p.Redirect.FastPlaybackInfo,
		DirectPlayUsers:  append([]string(nil), p.Redirect.DirectPlayUsers...),
		PublicURL:        strings.TrimSpace(p.Redirect.PublicURL),
		PlayTicketSecret: strings.TrimSpace(p.Redirect.PlayTicketSecret),
		RoutePrefix:      strings.TrimSpace(p.Redirect.RoutePrefix),
	}
	cfg.Sync = config.SyncConfig{
		BaseDir:             strings.TrimSpace(p.Sync.BaseDir),
		IndexDB:             strings.TrimSpace(p.Sync.IndexDB),
		MaxDirsPerCycle:     p.Sync.MaxDirsPerCycle,
		MaxRequestsPerCycle: p.Sync.MaxRequestsPerCycle,
		MinFileSize:         p.Sync.MinFileSize,
		VideoExtsRaw:        append([]string(nil), p.Sync.VideoExts...),
		CleanRemoved:        p.Sync.CleanRemoved,
		Overwrite:           p.Sync.Overwrite,
		LogLevel:            strings.TrimSpace(p.Sync.LogLevel),
		UnchangedToWarm:     p.Sync.UnchangedToWarm,
		UnchangedToCold:     p.Sync.UnchangedToCold,
	}

	var err error
	if cfg.OpenList.RequestTimeout, err = parseDurationField("openlist.request_timeout", p.OpenList.RequestTimeout); err != nil {
		return config.Config{}, err
	}
	if cfg.OpenList.RetryBackoff, err = parseDurationField("openlist.retry_backoff", p.OpenList.RetryBackoff); err != nil {
		return config.Config{}, err
	}
	if cfg.Emby.RequestTimeout, err = parseDurationField("emby.request_timeout", p.Emby.RequestTimeout); err != nil {
		return config.Config{}, err
	}
	if cfg.Emby.TokenCacheTTL, err = parseDurationField("emby.token_cache_ttl", p.Emby.TokenCacheTTL); err != nil {
		return config.Config{}, err
	}
	if cfg.Redirect.PlayTicketTTL, err = parseDurationField("redirect.play_ticket_ttl", p.Redirect.PlayTicketTTL); err != nil {
		return config.Config{}, err
	}
	if cfg.Sync.FullRescanInterval, err = parseDurationField("sync.full_rescan_interval", p.Sync.FullRescanInterval); err != nil {
		return config.Config{}, err
	}
	if cfg.Sync.HotInterval, err = parseDurationField("sync.hot_interval", p.Sync.HotInterval); err != nil {
		return config.Config{}, err
	}
	if cfg.Sync.WarmInterval, err = parseDurationField("sync.warm_interval", p.Sync.WarmInterval); err != nil {
		return config.Config{}, err
	}
	if cfg.Sync.ColdInterval, err = parseDurationField("sync.cold_interval", p.Sync.ColdInterval); err != nil {
		return config.Config{}, err
	}
	if cfg.Sync.HotJitter, err = parseDurationField("sync.hot_jitter", p.Sync.HotJitter); err != nil {
		return config.Config{}, err
	}
	if cfg.Sync.WarmJitter, err = parseDurationField("sync.warm_jitter", p.Sync.WarmJitter); err != nil {
		return config.Config{}, err
	}
	if cfg.Sync.ColdJitter, err = parseDurationField("sync.cold_jitter", p.Sync.ColdJitter); err != nil {
		return config.Config{}, err
	}
	if cfg.Sync.FailureBackoffMax, err = parseDurationField("sync.failure_backoff_max", p.Sync.FailureBackoffMax); err != nil {
		return config.Config{}, err
	}
	if cfg.Sync.RuleCooldown, err = parseDurationField("sync.rule_cooldown", p.Sync.RuleCooldown); err != nil {
		return config.Config{}, err
	}

	cfg.Rules = make([]config.Rule, 0, len(p.Rules))
	for _, rule := range p.Rules {
		cfg.Rules = append(cfg.Rules, config.Rule{
			Name:         strings.TrimSpace(rule.Name),
			SourcePath:   strings.TrimSpace(rule.SourcePath),
			TargetPath:   strings.TrimSpace(rule.TargetPath),
			Flatten:      boolPtr(rule.Flatten),
			IncludeRegex: strings.TrimSpace(rule.IncludeRegex),
			ExcludeRegex: strings.TrimSpace(rule.ExcludeRegex),
		})
	}
	return cfg, nil
}

func parseDurationField(name, raw string) (time.Duration, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	value, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid %s: %w", name, err)
	}
	return value, nil
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func extractEmbyToken(r *http.Request) string {
	candidates := []string{
		r.URL.Query().Get("api_key"),
		r.URL.Query().Get("X-Emby-Token"),
		r.URL.Query().Get("x-emby-token"),
		r.URL.Query().Get("X-MediaBrowser-Token"),
		r.URL.Query().Get("x-mediabrowser-token"),
		r.Header.Get("X-Emby-Token"),
		r.Header.Get("X-MediaBrowser-Token"),
	}
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate != "" {
			return candidate
		}
	}

	headers := []string{
		r.Header.Get("Authorization"),
		r.Header.Get("X-Emby-Authorization"),
	}
	for _, header := range headers {
		if header == "" {
			continue
		}
		matches := embyTokenPattern.FindStringSubmatch(header)
		if len(matches) == 2 {
			return matches[1]
		}
	}

	return ""
}

func extractEmbyDeviceID(r *http.Request) string {
	candidates := []string{
		r.URL.Query().Get("DeviceId"),
		r.URL.Query().Get("deviceId"),
		r.URL.Query().Get("X-Emby-Device-Id"),
		r.URL.Query().Get("x-emby-device-id"),
		r.Header.Get("X-Emby-Device-Id"),
		r.Header.Get("X-MediaBrowser-Device-Id"),
	}
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate != "" {
			return candidate
		}
	}

	headers := []string{
		r.Header.Get("Authorization"),
		r.Header.Get("X-Emby-Authorization"),
	}
	for _, header := range headers {
		if header == "" {
			continue
		}
		matches := embyDeviceIDPattern.FindStringSubmatch(header)
		if len(matches) == 2 {
			return matches[1]
		}
	}

	return ""
}

func boolPtr(value bool) *bool {
	return &value
}

const adminPageHTML = `<!doctype html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width,initial-scale=1">
  <title>emby-pro admin</title>
  <style>
    :root { color-scheme: light; font-family: ui-sans-serif, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; }
    body { margin: 0; background: #f5f7fb; color: #162033; }
    .wrap { max-width: 1200px; margin: 0 auto; padding: 24px; }
    h1, h2, h3 { margin: 0 0 12px; }
    .grid { display: grid; gap: 16px; grid-template-columns: repeat(auto-fit, minmax(240px, 1fr)); }
    .card { background: #fff; border-radius: 14px; padding: 18px; box-shadow: 0 8px 24px rgba(15, 23, 42, 0.08); margin-bottom: 16px; }
    label { display: block; font-size: 13px; font-weight: 600; margin-bottom: 6px; }
    input, textarea, select { width: 100%; box-sizing: border-box; padding: 10px 12px; border: 1px solid #cfd7e6; border-radius: 10px; font: inherit; }
    textarea { min-height: 84px; resize: vertical; }
    table { width: 100%; border-collapse: collapse; }
    th, td { padding: 8px; border-bottom: 1px solid #e5e9f2; text-align: left; vertical-align: top; }
    button { border: 0; background: #1c63ff; color: #fff; padding: 10px 14px; border-radius: 10px; cursor: pointer; font: inherit; }
    button.secondary { background: #e8eefc; color: #234092; }
    button.danger { background: #d9485f; }
    .actions { display: flex; gap: 10px; flex-wrap: wrap; margin-top: 12px; }
    .muted { color: #66758c; font-size: 13px; }
    .status { margin-bottom: 16px; padding: 12px 14px; border-radius: 10px; background: #eaf1ff; }
    .error { background: #ffe8eb; color: #8c2332; }
    .hint { display: inline-flex; align-items: center; justify-content: center; width: 16px; height: 16px; margin-left: 4px; border-radius: 999px; background: #dce7ff; color: #20449a; font-size: 11px; font-weight: 700; cursor: help; }
    .section-note { margin: 6px 0 0; color: #66758c; font-size: 13px; }
    .inline-note { margin-top: 8px; color: #66758c; font-size: 12px; }
    details.advanced { margin-top: 14px; }
    details.advanced summary { cursor: pointer; color: #234092; font-weight: 700; }
    .subgrid { margin-top: 12px; }
    .choice-list { display: grid; gap: 10px; grid-template-columns: repeat(auto-fit, minmax(220px, 1fr)); margin-top: 12px; }
    .choice-item { display: flex; align-items: center; gap: 8px; margin: 0; padding: 10px 12px; border: 1px solid #d9e2f3; border-radius: 10px; background: #f8fbff; }
    .choice-item input { width: auto; margin: 0; }
    .choice-meta { color: #66758c; font-size: 12px; }
    .tree-wrap { border: 1px solid #e5e9f2; border-radius: 12px; background: #fbfcff; padding: 12px; min-height: 120px; position: relative; }
    .tree-root, .tree-root ul { list-style: none; margin: 0; padding-left: 18px; }
    .tree-root { padding-left: 0; }
    .tree-node { display: inline-flex; align-items: center; gap: 8px; padding: 6px 8px; border-radius: 8px; cursor: pointer; user-select: none; }
    .tree-node:hover { background: #eef4ff; }
    .tree-node-folder::before { content: "▸"; color: #66758c; font-size: 12px; width: 10px; }
    .tree-item-expanded > .tree-node-folder::before { content: "▾"; }
    .tree-item-collapsed > ul { display: none; }
    .tree-node-leaf::before { content: "•"; color: #9aa6bc; font-size: 12px; width: 10px; }
    .tree-meta { color: #66758c; font-size: 12px; }
    .context-menu { position: fixed; z-index: 1000; min-width: 180px; background: #fff; border: 1px solid #d9e2f3; border-radius: 12px; box-shadow: 0 12px 28px rgba(15, 23, 42, 0.16); padding: 6px; display: none; }
    .context-menu button { width: 100%; text-align: left; background: transparent; color: #162033; padding: 10px 12px; border-radius: 8px; }
    .context-menu button:hover { background: #eef4ff; }
    .input-with-button { display: grid; grid-template-columns: 1fr auto; gap: 8px; align-items: start; }
    .dialog-backdrop { position: fixed; inset: 0; background: rgba(15, 23, 42, 0.35); display: none; align-items: center; justify-content: center; z-index: 1100; }
    .dialog { width: min(720px, calc(100vw - 32px)); max-height: calc(100vh - 48px); background: #fff; border-radius: 16px; box-shadow: 0 18px 48px rgba(15, 23, 42, 0.2); padding: 18px; display: flex; flex-direction: column; gap: 12px; }
    .dialog-head { display: flex; align-items: center; justify-content: space-between; gap: 12px; }
    .dialog-path { padding: 10px 12px; border-radius: 10px; background: #f3f7ff; color: #234092; font: 13px/1.4 ui-monospace, SFMono-Regular, Menlo, monospace; }
    .dialog-list { border: 1px solid #e5e9f2; border-radius: 12px; overflow: auto; min-height: 220px; max-height: 420px; }
    .dialog-row { display: flex; align-items: center; justify-content: space-between; gap: 12px; padding: 10px 12px; border-bottom: 1px solid #eef2f7; cursor: pointer; }
    .dialog-row:last-child { border-bottom: 0; }
    .dialog-row:hover { background: #f8fbff; }
    .dialog-row-name { font-weight: 600; }
    pre.logbox { margin: 0; padding: 12px; border-radius: 10px; background: #0f172a; color: #d6e2ff; min-height: 180px; max-height: 360px; overflow: auto; font: 12px/1.5 ui-monospace, SFMono-Regular, Menlo, monospace; }
  </style>
</head>
<body>
  <div class="wrap">
    <h1>emby-pro 管理页</h1>
    <p class="muted">保存后立即热更新；当前 strm 目录结构支持右键快速触发所属规则更新。</p>
    <div id="userInfo" class="status" style="display:none"></div>
    <div id="message" class="status" style="display:none"></div>
    <div id="runtimeError" class="status error" style="display:none"></div>
    <form id="configForm">
      <div class="card">
        <h2>OpenList 配置</h2>
        <p class="section-note">这里填写 OpenList 的访问地址和认证信息，服务端会用它来扫描目录和获取直链。</p>
        <div class="grid">
          <div><label>服务端地址 <span class="hint" title="emby-pro 容器访问 OpenList API 时使用的地址，通常填内网地址。">?</span></label><input id="ol_base_url"></div>
          <div><label>访问令牌 <span class="hint" title="OpenList token。已填写时优先使用，不再用用户名密码登录。">?</span></label><input id="ol_token" type="password"></div>
          <div><label>用户名</label><input id="ol_username"></div>
          <div><label>密码</label><input id="ol_password" type="password"></div>
        </div>
        <details class="advanced">
          <summary>高级设置</summary>
          <div class="grid subgrid">
            <div><label>外部地址 <span class="hint" title="客户端最终访问下载链接时使用的地址。若不填，则回退到服务端地址。">?</span></label><input id="ol_public_url"></div>
            <div><label>请求超时 <span class="hint" title="每次请求 OpenList 最多等待多久，示例：15s。">?</span></label><input id="ol_request_timeout"></div>
            <div><label>重试次数 <span class="hint" title="请求失败后额外重试的次数。">?</span></label><input id="ol_retry" type="number"></div>
            <div><label>重试间隔 <span class="hint" title="重试前等待多久，示例：2s。">?</span></label><input id="ol_retry_backoff"></div>
            <div><label>每页条数 <span class="hint" title="扫描目录时每次向 OpenList 请求的分页大小。">?</span></label><input id="ol_list_per_page" type="number"></div>
            <div><label>限速 QPS <span class="hint" title="strm 扫描链路额外限速，每秒最多多少次请求；0 表示不额外限速。">?</span></label><input id="ol_rate_limit_qps" type="number" step="0.1"></div>
            <div><label>限速突发值 <span class="hint" title="令牌桶一次最多允许突发多少个请求。">?</span></label><input id="ol_rate_limit_burst" type="number"></div>
          </div>
        </details>
      </div>
      <div class="card">
        <h2>Emby 与播放代理</h2>
        <p class="section-note">这里控制 Emby 上游连接和 /strm 播放代理行为。</p>
        <div class="grid">
          <div><label>Emby 地址 <span class="hint" title="emby-pro 回源 Emby API 时使用的地址，通常填容器内或本机可访问地址。">?</span></label><input id="emby_base_url"></div>
          <div><label>代理外部地址 <span class="hint" title="生成稳定 strm 地址和播放票据时使用的外部访问地址。">?</span></label><input id="redirect_public_url"></div>
        </div>
        <div class="actions">
          <label><input id="redirect_direct_play" type="checkbox"> 启用直链播放 <span class="hint" title="总开关。开启后，emby-pro 会把可直链的媒体改写到 /strm 入口。">?</span></label>
          <label><input id="redirect_direct_play_web" type="checkbox"> 浏览器启用直链 <span class="hint" title="默认关闭。开启后 Emby Web 也会走直链，可能遇到浏览器跨域限制。">?</span></label>
          <label><input id="redirect_fast_playback_info" type="checkbox"> 快速生成播放信息 <span class="hint" title="允许直接基于媒体信息构造最小播放信息，减少一次回源 Emby。">?</span></label>
        </div>
        <div class="card" style="margin-top:16px; margin-bottom:0; box-shadow:none; border:1px solid #e5e9f2;">
          <h3>直链用户</h3>
          <p class="section-note">未选择任何用户时，默认所有 Emby 用户都走直链；这里勾选后只对所选用户生效。</p>
          <div id="directPlayUsers" class="choice-list"></div>
          <div id="directPlayUsersHint" class="inline-note"></div>
        </div>
        <details class="advanced">
          <summary>高级设置</summary>
          <div class="grid subgrid">
            <div><label>校验路径 <span class="hint" title="用于 Emby token 校验的接口路径，通常保持默认。">?</span></label><input id="emby_validate_path"></div>
            <div><label>Emby 超时</label><input id="emby_request_timeout"></div>
            <div><label>令牌缓存时长 <span class="hint" title="同一个 Emby token 的用户信息缓存多久，示例：30s。">?</span></label><input id="emby_token_cache_ttl"></div>
            <div><label>播放票据时长 <span class="hint" title="播放票据有效期，示例：12h。">?</span></label><input id="redirect_play_ticket_ttl"></div>
            <div><label>播放票据密钥 <span class="hint" title="用于签发播放票据。为空时会在运行时临时生成，重启后旧票据失效。">?</span></label><input id="redirect_play_ticket_secret"></div>
            <div><label>播放路由前缀 <span class="hint" title="strm 链接暴露出来的 URL 前缀，通常保持 /strm。">?</span></label><input id="redirect_route_prefix"></div>
          </div>
        </details>
      </div>
      <div class="card">
        <h2>同步扫描</h2>
        <p class="section-note">默认只保留常用项，调度和限流参数放到高级设置里。</p>
        <div class="grid">
          <div><label>strm 根目录 <span class="hint" title="所有规则的 strm 输出根目录。">?</span></label><input id="sync_base_dir"></div>
          <div><label>索引数据库 <span class="hint" title="保存扫描状态、目录计划和重扫记录的 SQLite 文件。">?</span></label><input id="sync_index_db"></div>
          <div><label>全量重扫周期 <span class="hint" title="多久自动请求一次全量校准扫描，示例：24h。">?</span></label><input id="sync_full_rescan_interval"></div>
          <div><label>最小文件大小（MB） <span class="hint" title="小于这个大小的视频不会生成 strm。管理页按 MB 输入，保存时会自动换算成字节。">?</span></label><input id="sync_min_file_size" type="number" step="1" min="0"></div>
          <div><label>视频扩展名（逗号分隔） <span class="hint" title="只有这些扩展名才会被识别为视频并生成 strm。">?</span></label><input id="sync_video_exts"></div>
          <div><label>日志级别 <span class="hint" title="支持 info 或 debug。">?</span></label><input id="sync_log_level"></div>
        </div>
        <div class="actions">
          <label><input id="sync_clean_removed" type="checkbox"> 自动清理已删除文件 <span class="hint" title="OpenList 里消失的文件，会把本地对应 strm 一起删除。">?</span></label>
          <label><input id="sync_overwrite" type="checkbox"> 扫描命中时总是重写 <span class="hint" title="开启后，每次扫到命中的文件都重写 strm；关闭后仅在内容变化时重写。">?</span></label>
        </div>
        <details class="advanced">
          <summary>高级设置</summary>
          <div class="grid subgrid">
            <div><label>每轮最多目录数 <span class="hint" title="单次循环最多处理多少个目录。">?</span></label><input id="sync_max_dirs_per_cycle" type="number"></div>
            <div><label>每轮最多请求数 <span class="hint" title="单次循环最多向 OpenList 发多少次请求。">?</span></label><input id="sync_max_requests_per_cycle" type="number"></div>
            <div><label>高频周期 <span class="hint" title="刚变化或较热目录的扫描周期。">?</span></label><input id="sync_hot_interval"></div>
            <div><label>中频周期 <span class="hint" title="相对稳定目录的扫描周期。">?</span></label><input id="sync_warm_interval"></div>
            <div><label>低频周期 <span class="hint" title="长期不变目录的扫描周期。">?</span></label><input id="sync_cold_interval"></div>
            <div><label>高频抖动</label><input id="sync_hot_jitter"></div>
            <div><label>中频抖动</label><input id="sync_warm_jitter"></div>
            <div><label>低频抖动</label><input id="sync_cold_jitter"></div>
            <div><label>进入中频阈值 <span class="hint" title="目录连续多少次无变化后，从高频降到中频。">?</span></label><input id="sync_unchanged_to_warm" type="number"></div>
            <div><label>进入低频阈值 <span class="hint" title="目录连续多少次无变化后，从中频降到低频。">?</span></label><input id="sync_unchanged_to_cold" type="number"></div>
            <div><label>失败最大退避 <span class="hint" title="扫描失败后，最多退避多久再重试。">?</span></label><input id="sync_failure_backoff_max"></div>
            <div><label>规则冷却时长 <span class="hint" title="遇到 429 或风控时，这条规则暂停多久再继续扫。">?</span></label><input id="sync_rule_cooldown"></div>
          </div>
          <div class="actions">
            <label><input id="ol_insecure_skip_verify" type="checkbox"> 跳过 TLS 校验 <span class="hint" title="只有在 OpenList 使用自签名证书时才建议开启。">?</span></label>
            <label><input id="ol_disable_http2" type="checkbox"> 禁用 HTTP/2 <span class="hint" title="用于兼容少数代理或网络环境异常的情况。">?</span></label>
          </div>
        </details>
      </div>
      <div class="card">
        <h2>规则</h2>
        <p class="section-note">目标目录填写相对路径即可，系统会自动拼接到 strm 根目录下面。</p>
        <table>
          <thead><tr><th>名称</th><th>源目录</th><th>目标目录</th><th>包含规则</th><th>排除规则</th><th>选项</th><th></th></tr></thead>
          <tbody id="rulesBody"></tbody>
        </table>
        <div class="actions"><button type="button" class="secondary" id="addRule">新增规则</button></div>
      </div>
      <div class="card">
        <h2>strm 目录结构</h2>
        <p class="section-note">展示当前 strm 目录。右键目录可快速更新所属规则。</p>
        <div id="strmTree" class="tree-wrap"></div>
      </div>
      <div class="card">
        <h2>扫描日志</h2>
        <p class="section-note">这里展示最近的同步器日志。点规则扫描或全量扫描后，可以在这里确认是否真正开始处理。</p>
        <pre id="logsBox" class="logbox">暂无日志</pre>
      </div>
      <div class="actions"><button type="submit">保存配置</button></div>
    </form>
  </div>
  <div id="treeContextMenu" class="context-menu">
    <button type="button" id="treeContextScan">更新所属规则</button>
  </div>
  <div id="openlistDialogBackdrop" class="dialog-backdrop">
    <div class="dialog">
      <div class="dialog-head">
        <h3>选择 OpenList 目录</h3>
        <button type="button" class="secondary" id="openlistDialogClose">关闭</button>
      </div>
      <div id="openlistDialogPath" class="dialog-path">/</div>
      <div class="actions">
        <button type="button" class="secondary" id="openlistDialogUp">返回上级</button>
        <button type="button" class="secondary" id="openlistDialogSelectCurrent">使用当前目录</button>
      </div>
      <div id="openlistDialogList" class="dialog-list"></div>
    </div>
  </div>
  <script>
    let latest = null;
    let workingToken = '';
    let treeContextPath = '';
    let openListDialogPath = '/';
    let openListDialogRow = null;
    function listCandidateTokens() {
      const tokens = [];
      try {
        const raw = localStorage.getItem('servercredentials3');
        if (!raw) return tokens;
        const parsed = JSON.parse(raw);
        const servers = parsed?.Servers || [];
        const origin = window.location.origin;
        const host = window.location.host;
        for (const server of servers) {
          const candidates = [
            server.ManualAddress,
            server.LocalAddress,
            server.RemoteAddress
          ].filter(Boolean);
          const matched = candidates.some(value => {
            try {
              const url = new URL(value, origin);
              return url.origin === origin || url.host === host;
            } catch (_) {
              return false;
            }
          });
          if (!matched) continue;
          if (server.AccessToken) tokens.push(server.AccessToken);
          for (const user of (server.Users || [])) {
            if (user && user.AccessToken) tokens.push(user.AccessToken);
          }
        }
      } catch (_) {}
      return [...new Set(tokens.filter(Boolean))];
    }
    async function authedFetch(url, options = {}) {
      const candidates = workingToken ? [workingToken] : listCandidateTokens();
      if (!candidates.length) throw new Error('未找到 Emby 登录信息，请先在当前地址登录 Emby。');
      let lastError = '';
      for (const token of candidates) {
        const resp = await fetch(url, {
          ...options,
          credentials: 'same-origin',
          headers: { ...(options.headers || {}), 'X-Emby-Token': token }
        });
        if (resp.ok) {
          workingToken = token;
          return resp;
        }
        const text = await resp.text();
        if (resp.status === 401) {
          lastError = '当前页面缓存的 Emby token 已过期，请重新登录 Emby。';
          continue;
        }
        if (resp.status === 403) {
          lastError = text || '当前账号不是 Emby 管理员。';
          continue;
        }
        throw new Error(text || ('请求失败: ' + resp.status));
      }
      throw new Error(lastError || '没有找到可用的 Emby 管理员 token。');
    }
    function showMessage(text, error = false) {
      const box = document.getElementById('message');
      box.style.display = text ? 'block' : 'none';
      box.textContent = text || '';
      box.className = error ? 'status error' : 'status';
    }
    function renderUser(user) {
      const box = document.getElementById('userInfo');
      if (!user || !user.name) {
        box.style.display = 'none';
        box.textContent = '';
        return;
      }
      box.style.display = 'block';
      box.className = user.is_admin ? 'status' : 'status error';
      box.textContent = '当前用户: ' + user.name + ' | ID: ' + user.id + ' | 管理员: ' + (user.is_admin ? '是' : '否');
    }
    function renderRuntimeError(text) {
      const box = document.getElementById('runtimeError');
      box.style.display = text ? 'block' : 'none';
      box.textContent = text || '';
    }
    function renderLogs(logs) {
      const box = document.getElementById('logsBox');
      const content = (logs || []).length ? logs.join('\n') : '暂无日志';
      box.textContent = content;
      box.scrollTop = box.scrollHeight;
    }
    function v(id, value) { document.getElementById(id).value = value || ''; }
    function c(id, value) { document.getElementById(id).checked = !!value; }
    function splitCSV(value) { return (value || '').split(',').map(v => v.trim()).filter(Boolean); }
    function trimSlashes(value) { return (value || '').replace(/^\/+|\/+$/g, ''); }
    function normalizeSourcePath(value) {
      const trimmed = (value || '').trim();
      if (!trimmed) return '';
      return '/' + trimSlashes(trimmed);
    }
    function bytesToMB(value) {
      const bytes = Number(value || 0);
      if (!Number.isFinite(bytes) || bytes <= 0) return '';
      return String(Math.round(bytes / (1024 * 1024)));
    }
    function mbToBytes(value) {
      const mb = Number(value || 0);
      if (!Number.isFinite(mb) || mb <= 0) return 0;
      return Math.round(mb * 1024 * 1024);
    }
    function normalizedRule(rule = {}) {
      return {
        name: (rule.name || '').trim(),
        source_path: (rule.source_path || '').trim(),
        target_path: (rule.target_path || '').trim(),
        include_regex: (rule.include_regex || '').trim(),
        exclude_regex: (rule.exclude_regex || '').trim(),
        flatten: !!rule.flatten
      };
    }
    function selectedDirectPlayUsers() {
      return [...document.querySelectorAll('input[name="redirect_direct_play_users"]:checked')].map(input => input.value);
    }
    function renderDirectPlayUsers(selectedUsers, users) {
      const selectedSet = new Set((selectedUsers || []).map(value => value.trim()).filter(Boolean));
      const container = document.getElementById('directPlayUsers');
      const hint = document.getElementById('directPlayUsersHint');
      container.innerHTML = '';
      const merged = new Map();
      for (const user of (users || [])) {
        if (!user || !user.id || !user.name) continue;
        merged.set(user.id, {
          value: user.id,
          label: user.name,
          checked: selectedSet.has(user.id) || selectedSet.has(user.name),
          meta: user.is_admin ? '管理员' : '普通用户'
        });
      }
      for (const value of selectedSet) {
        const exists = [...merged.values()].some(item => item.value === value || item.label === value);
        if (exists) continue;
        merged.set('legacy:' + value, {
          value: value,
          label: value,
          checked: true,
          meta: '现有配置值'
        });
      }
      const items = [...merged.values()].sort((a, b) => a.label.localeCompare(b.label, 'zh-CN'));
      if (!items.length) {
        hint.textContent = '暂未读取到 Emby 用户列表，留空时仍默认所有用户走直链。';
        return;
      }
      for (const item of items) {
        const label = document.createElement('label');
        label.className = 'choice-item';
        label.innerHTML = '<input type="checkbox" name="redirect_direct_play_users"><span><strong></strong><div class="choice-meta"></div></span>';
        const input = label.querySelector('input');
        input.value = item.value;
        input.checked = !!item.checked;
        label.querySelector('strong').textContent = item.label;
        label.querySelector('.choice-meta').textContent = item.meta;
        container.appendChild(label);
      }
      hint.textContent = selectedSet.size ? '当前已限制为仅所选用户走直链。' : '当前未限制用户，默认所有用户都走直链。';
    }
    function buildTree(dirs = []) {
      const root = { name: '/', path: '', children: new Map() };
      for (const dir of (dirs || [])) {
        const parts = trimSlashes(dir).split('/').filter(Boolean);
        let node = root;
        let currentPath = '';
        for (const part of parts) {
          currentPath = currentPath ? currentPath + '/' + part : part;
          if (!node.children.has(part)) {
            node.children.set(part, { name: part, path: currentPath, children: new Map() });
          }
          node = node.children.get(part);
        }
      }
      return root;
    }
    function renderTree(data) {
      const container = document.getElementById('strmTree');
      container.innerHTML = '';
      const dirs = data?.existing_target_dirs || [];
      if (!dirs.length) {
        container.textContent = '当前没有发现已生成的 strm 目录。';
        return;
      }
      const tree = buildTree(dirs);
      const rootList = document.createElement('ul');
      rootList.className = 'tree-root';
      for (const child of [...tree.children.values()].sort((a, b) => a.name.localeCompare(b.name, 'zh-CN'))) {
        rootList.appendChild(renderTreeNode(child, data));
      }
      container.appendChild(rootList);
    }
    function renderTreeNode(node, data) {
      const item = document.createElement('li');
      const hasChildren = node.children.size > 0;
      item.className = hasChildren ? 'tree-item-collapsed' : '';
      const row = document.createElement('div');
      row.className = 'tree-node tree-node-folder';
      row.dataset.path = node.path;
      const matchedRule = findRuleByTargetDir(node.path, data?.config);
      row.innerHTML = '<span></span><span class="tree-meta"></span>';
      row.querySelector('span').textContent = node.name;
      row.querySelector('.tree-meta').textContent = matchedRule ? ('规则: ' + matchedRule.name) : '无直接规则';
      row.addEventListener('click', () => {
        if (!hasChildren) return;
        item.classList.toggle('tree-item-collapsed');
        item.classList.toggle('tree-item-expanded');
      });
      row.addEventListener('contextmenu', ev => {
        ev.preventDefault();
        openTreeContextMenu(ev.clientX, ev.clientY, node.path);
      });
      item.appendChild(row);
      if (hasChildren) {
        const sub = document.createElement('ul');
        for (const child of [...node.children.values()].sort((a, b) => a.name.localeCompare(b.name, 'zh-CN'))) {
          sub.appendChild(renderTreeNode(child, data));
        }
        item.appendChild(sub);
      }
      return item;
    }
    function findRuleByTargetDir(dirPath, cfg) {
      const normalizedDir = trimSlashes(dirPath);
      const rules = cfg?.rules || [];
      let matched = null;
      let longest = -1;
      for (const rule of rules) {
        const relativeTarget = trimSlashes(toRelativeTargetPath(rule.target_path, cfg?.sync?.base_dir || ''));
        if (!relativeTarget) continue;
        const exact = normalizedDir === relativeTarget;
        const nested = normalizedDir.startsWith(relativeTarget + '/');
        if (!exact && !nested) continue;
        if (relativeTarget.length > longest) {
          longest = relativeTarget.length;
          matched = rule;
        }
      }
      return matched;
    }
    function openTreeContextMenu(x, y, dirPath) {
      treeContextPath = dirPath;
      const menu = document.getElementById('treeContextMenu');
      menu.style.display = 'block';
      menu.style.left = x + 'px';
      menu.style.top = y + 'px';
      const rule = findRuleByTargetDir(dirPath, latest?.config);
      document.getElementById('treeContextScan').disabled = !rule;
    }
    function closeTreeContextMenu() {
      const menu = document.getElementById('treeContextMenu');
      menu.style.display = 'none';
      treeContextPath = '';
    }
    function toRelativeTargetPath(targetPath, baseDir) {
      const normalizedBase = trimSlashes(baseDir);
      const normalizedTarget = trimSlashes(targetPath);
      if (!normalizedTarget) return '';
      if (!normalizedBase) return normalizedTarget;
      if (normalizedTarget === normalizedBase) return '';
      if (normalizedTarget.startsWith(normalizedBase + '/')) return normalizedTarget.slice(normalizedBase.length + 1);
      return normalizedTarget;
    }
    function toAbsoluteTargetPath(relativeTargetPath, baseDir) {
      const rel = trimSlashes(relativeTargetPath);
      const base = trimSlashes(baseDir);
      if (!base && !rel) return '';
      if (!base) return '/' + rel;
      if (!rel) return '/' + base;
      return '/' + base + '/' + rel;
    }
    function maybeSyncTargetPathFromSource(tr) {
      const sourceInput = tr.querySelector('[data-k="source_path"]');
      const targetInput = tr.querySelector('[data-k="target_path"]');
      if (!sourceInput || !targetInput) return;
      if ((targetInput.value || '').trim() !== '') return;
      targetInput.value = trimSlashes(sourceInput.value);
    }
    async function loadOpenListDirs(dirPath) {
      const resp = await authedFetch('/admin/api/openlist-dirs?path=' + encodeURIComponent(dirPath));
      return await resp.json();
    }
    function closeOpenListDialog() {
      document.getElementById('openlistDialogBackdrop').style.display = 'none';
      openListDialogRow = null;
    }
    function applyOpenListDirSelection(pathValue) {
      if (!openListDialogRow) return;
      const sourceInput = openListDialogRow.querySelector('[data-k="source_path"]');
      if (!sourceInput) return;
      sourceInput.value = normalizeSourcePath(pathValue);
      maybeSyncTargetPathFromSource(openListDialogRow);
      closeOpenListDialog();
    }
    async function renderOpenListDialog(dirPath) {
      const data = await loadOpenListDirs(dirPath);
      openListDialogPath = data.path || '/';
      document.getElementById('openlistDialogPath').textContent = openListDialogPath;
      const list = document.getElementById('openlistDialogList');
      list.innerHTML = '';
      const dirs = data.dirs || [];
      if (!dirs.length) {
        const empty = document.createElement('div');
        empty.className = 'dialog-row';
        empty.textContent = '当前目录下没有子目录';
        list.appendChild(empty);
        return;
      }
      for (const dir of dirs) {
        const row = document.createElement('div');
        row.className = 'dialog-row';
        row.innerHTML = '<div><div class="dialog-row-name"></div><div class="choice-meta"></div></div><button type="button" class="secondary">进入</button>';
        row.querySelector('.dialog-row-name').textContent = dir.name;
        row.querySelector('.choice-meta').textContent = dir.path;
        row.addEventListener('dblclick', () => applyOpenListDirSelection(dir.path));
        row.querySelector('button').addEventListener('click', ev => {
          ev.stopPropagation();
          renderOpenListDialog(dir.path).catch(err => showMessage(err.message, true));
        });
        row.addEventListener('click', () => applyOpenListDirSelection(dir.path));
        list.appendChild(row);
      }
    }
    async function openOpenListDialog(tr) {
      openListDialogRow = tr;
      const sourceInput = tr.querySelector('[data-k="source_path"]');
      const startPath = normalizeSourcePath(sourceInput ? sourceInput.value : '') || '/';
      document.getElementById('openlistDialogBackdrop').style.display = 'flex';
      await renderOpenListDialog(startPath);
    }
    function renderRuleRow(rule = {}) {
      rule = { clean_removed: true, ...rule };
      const tr = document.createElement('tr');
      tr.innerHTML = '<td><input data-k="name"></td><td><div class="input-with-button"><input data-k="source_path" placeholder="/movies"><button type="button" class="secondary" data-action="pick-source">选择</button></div><div class="section-note">支持直接输入，也可以从 OpenList 目录中选择。</div></td><td><input data-k="target_path" placeholder="例如：电影/港台"><div class="section-note">为空时会自动跟随源目录路径。</div></td><td><input data-k="include_regex"></td><td><input data-k="exclude_regex"></td><td><label><input data-k="flatten" type="checkbox"> 扁平 <span class="hint" title="开启后，这条规则下生成的 strm 文件都会直接放到目标目录，不保留 OpenList 原来的子目录层级。">?</span></label></td><td><button type="button" class="secondary" data-action="scan">扫描</button> <button type="button" class="secondary" data-action="force">全量扫描并强制重写</button> <button type="button" class="danger" data-action="delete">删除</button></td>';
      for (const [key, value] of Object.entries(rule)) {
        const el = tr.querySelector('[data-k="' + key + '"]');
        if (!el) continue;
        if (el.type === 'checkbox') el.checked = !!value; else el.value = value || '';
      }
      tr.querySelector('[data-action="pick-source"]').onclick = () => openOpenListDialog(tr).catch(err => showMessage(err.message, true));
      tr.querySelector('[data-k="source_path"]').addEventListener('change', () => maybeSyncTargetPathFromSource(tr));
      tr.querySelector('[data-action="delete"]').onclick = () => tr.remove();
      tr.querySelector('[data-action="scan"]').onclick = () => {
        const name = tr.querySelector('[data-k="name"]').value.trim();
        if (!name) {
          showMessage('请先填写并保存规则名称后再触发扫描。', true);
          return;
        }
        rescanRule(name).catch(err => showMessage(err.message, true));
      };
      tr.querySelector('[data-action="force"]').onclick = () => {
        const name = tr.querySelector('[data-k="name"]').value.trim();
        if (!name) {
          showMessage('请先填写并保存规则名称后再触发全量扫描。', true);
          return;
        }
        forceFullScan(name).catch(err => showMessage(err.message, true));
      };
      return tr;
    }
    function readRules() {
      const baseDir = document.getElementById('sync_base_dir').value.trim();
      return [...document.querySelectorAll('#rulesBody tr')].map(tr => {
        const pick = key => tr.querySelector('[data-k="' + key + '"]');
        return normalizedRule({
          name: pick('name').value,
          source_path: normalizeSourcePath(pick('source_path').value),
          target_path: toAbsoluteTargetPath(pick('target_path').value.trim(), baseDir),
          include_regex: pick('include_regex').value,
          exclude_regex: pick('exclude_regex').value,
          flatten: pick('flatten').checked
        });
      }).filter(rule => rule.name || rule.source_path || rule.target_path);
    }
    function render(data) {
      latest = data;
      renderStatus(data);
      const cfg = data.config;
      renderDirectPlayUsers(cfg.redirect.direct_play_users || [], data.emby_users || []);
      renderTree(data);
      v('ol_base_url', cfg.openlist.base_url);
      v('ol_public_url', cfg.openlist.public_url);
      v('ol_token', cfg.openlist.token);
      v('ol_username', cfg.openlist.username);
      v('ol_password', cfg.openlist.password);
      v('ol_request_timeout', cfg.openlist.request_timeout);
      v('ol_retry', cfg.openlist.retry);
      v('ol_retry_backoff', cfg.openlist.retry_backoff);
      v('ol_list_per_page', cfg.openlist.list_per_page);
      v('ol_rate_limit_qps', cfg.openlist.rate_limit_qps);
      v('ol_rate_limit_burst', cfg.openlist.rate_limit_burst);
      c('ol_insecure_skip_verify', cfg.openlist.insecure_skip_verify);
      c('ol_disable_http2', cfg.openlist.disable_http2);
      v('emby_base_url', cfg.emby.base_url);
      v('emby_validate_path', cfg.emby.validate_path);
      v('emby_request_timeout', cfg.emby.request_timeout);
      v('emby_token_cache_ttl', cfg.emby.token_cache_ttl);
      v('redirect_public_url', cfg.redirect.public_url);
      v('redirect_play_ticket_ttl', cfg.redirect.play_ticket_ttl);
      v('redirect_play_ticket_secret', cfg.redirect.play_ticket_secret);
      v('redirect_route_prefix', cfg.redirect.route_prefix);
      c('redirect_direct_play', cfg.redirect.direct_play);
      c('redirect_direct_play_web', cfg.redirect.direct_play_web);
      c('redirect_fast_playback_info', cfg.redirect.fast_playback_info);
      v('sync_base_dir', cfg.sync.base_dir);
      v('sync_index_db', cfg.sync.index_db);
      v('sync_full_rescan_interval', cfg.sync.full_rescan_interval);
      v('sync_max_dirs_per_cycle', cfg.sync.max_dirs_per_cycle);
      v('sync_max_requests_per_cycle', cfg.sync.max_requests_per_cycle);
      v('sync_min_file_size', bytesToMB(cfg.sync.min_file_size));
      v('sync_video_exts', (cfg.sync.video_exts || []).join(', '));
      v('sync_log_level', cfg.sync.log_level);
      v('sync_hot_interval', cfg.sync.hot_interval);
      v('sync_warm_interval', cfg.sync.warm_interval);
      v('sync_cold_interval', cfg.sync.cold_interval);
      v('sync_hot_jitter', cfg.sync.hot_jitter);
      v('sync_warm_jitter', cfg.sync.warm_jitter);
      v('sync_cold_jitter', cfg.sync.cold_jitter);
      v('sync_unchanged_to_warm', cfg.sync.unchanged_to_warm);
      v('sync_unchanged_to_cold', cfg.sync.unchanged_to_cold);
      v('sync_failure_backoff_max', cfg.sync.failure_backoff_max);
      v('sync_rule_cooldown', cfg.sync.rule_cooldown);
      c('sync_clean_removed', cfg.sync.clean_removed);
      c('sync_overwrite', cfg.sync.overwrite);
      const rulesBody = document.getElementById('rulesBody');
      rulesBody.innerHTML = '';
      (cfg.rules || []).forEach(rule => rulesBody.appendChild(renderRuleRow({
        ...rule,
        target_path: toRelativeTargetPath(rule.target_path, cfg.sync.base_dir)
      })));
    }
    function renderStatus(data) {
      renderUser(data.user);
      renderRuntimeError(data.runtime_error);
      renderLogs(data.logs);
    }
    async function load() {
      const resp = await authedFetch('/admin/api/config');
      render(await resp.json());
    }
    async function refreshStatus() {
      const resp = await authedFetch('/admin/api/status');
      renderStatus(await resp.json());
    }
    async function save(ev) {
      ev.preventDefault();
      const payload = {
        openlist: {
          base_url: document.getElementById('ol_base_url').value.trim(),
          public_url: document.getElementById('ol_public_url').value.trim(),
          token: document.getElementById('ol_token').value.trim(),
          username: document.getElementById('ol_username').value.trim(),
          password: document.getElementById('ol_password').value,
          request_timeout: document.getElementById('ol_request_timeout').value.trim(),
          retry: Number(document.getElementById('ol_retry').value || 0),
          retry_backoff: document.getElementById('ol_retry_backoff').value.trim(),
          list_per_page: Number(document.getElementById('ol_list_per_page').value || 0),
          rate_limit_qps: Number(document.getElementById('ol_rate_limit_qps').value || 0),
          rate_limit_burst: Number(document.getElementById('ol_rate_limit_burst').value || 0),
          insecure_skip_verify: document.getElementById('ol_insecure_skip_verify').checked,
          disable_http2: document.getElementById('ol_disable_http2').checked
        },
        emby: {
          base_url: document.getElementById('emby_base_url').value.trim(),
          validate_path: document.getElementById('emby_validate_path').value.trim(),
          request_timeout: document.getElementById('emby_request_timeout').value.trim(),
          token_cache_ttl: document.getElementById('emby_token_cache_ttl').value.trim()
        },
        redirect: {
          direct_play: document.getElementById('redirect_direct_play').checked,
          direct_play_web: document.getElementById('redirect_direct_play_web').checked,
          fast_playback_info: document.getElementById('redirect_fast_playback_info').checked,
          direct_play_users: selectedDirectPlayUsers(),
          public_url: document.getElementById('redirect_public_url').value.trim(),
          play_ticket_secret: document.getElementById('redirect_play_ticket_secret').value.trim(),
          play_ticket_ttl: document.getElementById('redirect_play_ticket_ttl').value.trim(),
          route_prefix: document.getElementById('redirect_route_prefix').value.trim()
        },
        sync: {
          base_dir: document.getElementById('sync_base_dir').value.trim(),
          index_db: document.getElementById('sync_index_db').value.trim(),
          full_rescan_interval: document.getElementById('sync_full_rescan_interval').value.trim(),
          max_dirs_per_cycle: Number(document.getElementById('sync_max_dirs_per_cycle').value || 0),
          max_requests_per_cycle: Number(document.getElementById('sync_max_requests_per_cycle').value || 0),
          min_file_size: mbToBytes(document.getElementById('sync_min_file_size').value),
          video_exts: splitCSV(document.getElementById('sync_video_exts').value),
          clean_removed: document.getElementById('sync_clean_removed').checked,
          overwrite: document.getElementById('sync_overwrite').checked,
          log_level: document.getElementById('sync_log_level').value.trim(),
          hot_interval: document.getElementById('sync_hot_interval').value.trim(),
          warm_interval: document.getElementById('sync_warm_interval').value.trim(),
          cold_interval: document.getElementById('sync_cold_interval').value.trim(),
          hot_jitter: document.getElementById('sync_hot_jitter').value.trim(),
          warm_jitter: document.getElementById('sync_warm_jitter').value.trim(),
          cold_jitter: document.getElementById('sync_cold_jitter').value.trim(),
          unchanged_to_warm: Number(document.getElementById('sync_unchanged_to_warm').value || 0),
          unchanged_to_cold: Number(document.getElementById('sync_unchanged_to_cold').value || 0),
          failure_backoff_max: document.getElementById('sync_failure_backoff_max').value.trim(),
          rule_cooldown: document.getElementById('sync_rule_cooldown').value.trim()
        },
        rules: readRules()
      };
      const resp = await authedFetch('/admin/api/config', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(payload) });
      const data = await resp.json();
      render(data);
      showMessage('配置已保存并热更新');
    }
    async function rescanRule(rule, silent = false) {
      const resp = await authedFetch('/admin/api/rescan/' + encodeURIComponent(rule), { method: 'POST' });
      const result = await resp.json();
      await refreshStatus();
      if (!silent) {
        showMessage(result.pending ? ('规则已在扫描中，已排队等待再次扫描：' + rule) : ('已请求立即扫描规则：' + rule));
      }
    }
    async function rescanAll() {
      const resp = await authedFetch('/admin/api/rescan', { method: 'POST' });
      await resp.json();
      await refreshStatus();
      showMessage('已请求刷新全部规则');
    }
    async function forceFullScan(rule) {
      const resp = await authedFetch('/admin/api/full-scan?rule=' + encodeURIComponent(rule), { method: 'POST' });
      await resp.json();
      await refreshStatus();
      showMessage('已请求规则全量扫描并强制重写：' + rule);
    }
    async function scanTreeDir() {
      const rule = findRuleByTargetDir(treeContextPath, latest?.config);
      closeTreeContextMenu();
      if (!rule || !rule.name) {
        showMessage('这个目录没有匹配到可更新的规则。', true);
        return;
      }
      await rescanRule(rule.name, true);
      showMessage('已请求更新目录 ' + treeContextPath + ' 所属规则：' + rule.name);
    }
    document.getElementById('configForm').addEventListener('submit', ev => save(ev).catch(err => showMessage(err.message, true)));
    document.getElementById('addRule').addEventListener('click', () => document.getElementById('rulesBody').appendChild(renderRuleRow()));
    document.getElementById('treeContextScan').addEventListener('click', () => scanTreeDir().catch(err => showMessage(err.message, true)));
    document.getElementById('openlistDialogClose').addEventListener('click', () => closeOpenListDialog());
    document.getElementById('openlistDialogSelectCurrent').addEventListener('click', () => applyOpenListDirSelection(openListDialogPath));
    document.getElementById('openlistDialogUp').addEventListener('click', () => {
      const current = normalizeSourcePath(openListDialogPath) || '/';
      const parent = current === '/' ? '/' : normalizeSourcePath(current.split('/').slice(0, -1).join('/')) || '/';
      renderOpenListDialog(parent).catch(err => showMessage(err.message, true));
    });
    document.getElementById('openlistDialogBackdrop').addEventListener('click', ev => {
      if (ev.target === ev.currentTarget) closeOpenListDialog();
    });
    document.addEventListener('click', () => closeTreeContextMenu());
    document.addEventListener('scroll', () => closeTreeContextMenu(), true);
    window.addEventListener('resize', () => closeTreeContextMenu());
    load().catch(err => showMessage(err.message, true));
    setInterval(() => refreshStatus().catch(() => {}), 4000);
  </script>
</body>
</html>`

func extractEmbyClient(r *http.Request) string {
	candidates := []string{
		r.URL.Query().Get("Client"),
		r.URL.Query().Get("client"),
		r.URL.Query().Get("X-Emby-Client"),
		r.URL.Query().Get("x-emby-client"),
		r.URL.Query().Get("X-MediaBrowser-Client"),
		r.URL.Query().Get("x-mediabrowser-client"),
		r.Header.Get("X-Emby-Client"),
		r.Header.Get("X-MediaBrowser-Client"),
	}
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate != "" {
			return candidate
		}
	}

	headers := []string{
		r.Header.Get("Authorization"),
		r.Header.Get("X-Emby-Authorization"),
	}
	for _, header := range headers {
		if header == "" {
			continue
		}
		matches := embyClientPattern.FindStringSubmatch(header)
		if len(matches) == 2 {
			return strings.TrimSpace(matches[1])
		}
	}

	return ""
}

func isWebClientRequest(r *http.Request) bool {
	client := normalizeClientName(extractEmbyClient(r))
	if client == "" {
		return false
	}
	for _, token := range strings.Fields(client) {
		if token == "web" {
			return true
		}
	}
	return false
}

func normalizeClientName(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return ""
	}

	fields := strings.FieldsFunc(value, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
	return strings.Join(fields, " ")
}

func extractItemID(pathValue string) string {
	if matches := playbackInfoPathPattern.FindStringSubmatch(pathValue); len(matches) == 2 {
		return matches[1]
	}
	return ""
}

func requestPathPrefix(pathValue string) string {
	if strings.HasPrefix(strings.ToLower(pathValue), "/emby/") {
		return "/emby"
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func firstForwardedValue(value string) string {
	if value == "" {
		return ""
	}
	part := strings.Split(value, ",")[0]
	return strings.TrimSpace(part)
}

func forwardedHeaderValue(raw, key string) string {
	if raw == "" || key == "" {
		return ""
	}

	for _, section := range strings.Split(raw, ",") {
		for _, part := range strings.Split(section, ";") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			name, value, ok := strings.Cut(part, "=")
			if !ok || !strings.EqualFold(strings.TrimSpace(name), key) {
				continue
			}
			return strings.Trim(strings.TrimSpace(value), "\"")
		}
	}
	return ""
}

func normalizeForwardedPrefix(prefix string) string {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" || prefix == "/" {
		return ""
	}
	if !strings.HasPrefix(prefix, "/") {
		prefix = "/" + prefix
	}
	return strings.TrimSuffix(prefix, "/")
}
