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
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/monlor/emby-pro/internal/config"
	"github.com/monlor/emby-pro/internal/emby"
	"github.com/monlor/emby-pro/internal/httpx"
	"github.com/monlor/emby-pro/internal/openlist"
	"github.com/monlor/emby-pro/internal/pathutil"
)

const mediaPathCacheTTL = 30 * time.Minute

type tokenCacheEntry struct {
	expiry   time.Time
	userInfo *emby.UserInfo
}

type mediaPathCacheEntry struct {
	pathValue string
	expiry    time.Time
}

type Server struct {
	cfg        config.RedirectConfig
	client     *openlist.Client
	embyClient *emby.Client
	logger     *log.Logger
	builder    *Builder

	cacheMu    sync.Mutex
	tokenCache map[string]tokenCacheEntry
	tokenTTL   time.Duration

	mediaPathCacheMu sync.Mutex
	mediaPathCache   map[string]mediaPathCacheEntry

	proxy *httputil.ReverseProxy
}

var embyTokenPattern = regexp.MustCompile(`(?i)token="?([^", ]+)"?`)
var embyDeviceIDPattern = regexp.MustCompile(`(?i)deviceid="?([^", ]+)"?`)
var embyClientPattern = regexp.MustCompile(`(?i)client="?([^",]+)"?`)
var playbackInfoPathPattern = regexp.MustCompile(`(?i)^(?:/emby)?/Items/([^/]+)/PlaybackInfo$`)
var videoStreamPathPattern = regexp.MustCompile(`(?i)^(?:/emby)?/videos/([^/]+)/(?:stream(?:/.*)?|stream\.[^/]+|original(?:/.*)?|original\.[^/]+)$`)
var itemDownloadPathPattern = regexp.MustCompile(`(?i)^(?:/emby)?/Items/([^/]+)/Download(?:/.*)?$`)

func NewServer(cfg config.RedirectConfig, client *openlist.Client, embyClient *emby.Client, logger *log.Logger, tokenTTL time.Duration) *Server {
	if tokenTTL <= 0 {
		tokenTTL = 30 * time.Second
	}
	proxy := httputil.NewSingleHostReverseProxy(embyClient.BaseURL())
	proxy.Director = func(req *http.Request) {
		target := embyClient.BaseURL()
		req.URL.Scheme = target.Scheme
		req.URL.Host = target.Host
		resolved := embyClient.ResolveRequestURI(req.URL.RequestURI())
		req.URL.Path = resolved.Path
		req.URL.RawPath = resolved.RawPath
		req.URL.RawQuery = resolved.RawQuery
		req.URL.Fragment = resolved.Fragment
		req.Host = target.Host
	}
	serverCfg := config.RedirectConfig{
		DirectPlay:       cfg.DirectPlay,
		DirectPlayWeb:    cfg.DirectPlayWeb,
		DirectPlayUsers:  cfg.DirectPlayUsers,
		ListenAddr:       cfg.ListenAddr,
		PublicURL:        cfg.PublicURL,
		PlayTicketSecret: cfg.PlayTicketSecret,
		PlayTicketTTL:    cfg.PlayTicketTTL,
		RoutePrefix:      defaultRoutePrefix(cfg.RoutePrefix),
	}
	return &Server{
		cfg:            serverCfg,
		client:         client,
		embyClient:     embyClient,
		logger:         logger,
		builder:        NewBuilder(serverCfg),
		tokenCache:     make(map[string]tokenCacheEntry),
		tokenTTL:       tokenTTL,
		mediaPathCache: make(map[string]mediaPathCacheEntry),
		proxy:          proxy,
	}
}

func (s *Server) Run(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.HandleFunc(s.cfg.RoutePrefix+"/", s.handleSTRM)
	mux.HandleFunc("/", s.handleProxyEmby)

	httpServer := &http.Server{
		Addr:              s.cfg.ListenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
	}()

	s.logger.Printf("[INFO] redirect server listening on %s", s.cfg.ListenAddr)
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

func (s *Server) handleProxyEmby(w http.ResponseWriter, r *http.Request) {
	if provider, _, ok := s.sourcePathFromRoute(r); ok && provider == openListProvider {
		s.handleSTRM(w, r)
		return
	}

	if playbackInfoPathPattern.MatchString(r.URL.Path) {
		s.handlePlaybackInfo(w, r)
		return
	}

	if s.isDirectPlayEnabled(r) && (videoStreamPathPattern.MatchString(r.URL.Path) || itemDownloadPathPattern.MatchString(r.URL.Path)) {
		s.handleMediaRequest(w, r)
		return
	}

	s.proxy.ServeHTTP(w, r)
}

func (s *Server) isDirectPlayEnabled(r *http.Request) bool {
	if !s.cfg.DirectPlay {
		return false
	}
	if isWebClientRequest(r) && !s.cfg.DirectPlayWeb {
		return false
	}
	if len(s.cfg.DirectPlayUsers) == 0 {
		return true
	}

	token := extractEmbyToken(r)
	if token == "" {
		return false
	}

	userInfo := s.getCachedUserInfo(token)
	if userInfo == nil {
		info, err := s.embyClient.GetUserInfo(r.Context(), token, extractEmbyDeviceID(r))
		if err != nil {
			s.logger.Printf("[WARN] get user info for direct play check: %v", err)
			return false
		}
		s.cacheTokenWithUser(token, info)
		userInfo = info
	}

	_, byID := s.cfg.DirectPlayUsers[userInfo.ID]
	_, byName := s.cfg.DirectPlayUsers[userInfo.Name]
	return byID || byName
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
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read request body", http.StatusBadRequest)
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(bodyBytes))

	resp, respBody, err := s.embyClient.RawRequest(r.Context(), r.Method, r.URL.RequestURI(), r.Header, bodyBytes)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	mediaInfo, err := s.maybeRewritePlaybackInfo(r, respBody, s.isDirectPlayEnabled(r))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	httpx.CopyHeaders(w.Header(), resp.Header)
	w.Header().Del("Content-Length")
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(mediaInfo)
}

func (s *Server) handleMediaRequest(w http.ResponseWriter, r *http.Request) {
	token := extractEmbyToken(r)
	if token == "" {
		s.proxy.ServeHTTP(w, r)
		return
	}

	itemID := extractItemID(r.URL.Path)
	mediaSourceID := mediaSourceIDFromRequest(r)
	if itemID == "" || mediaSourceID == "" {
		s.proxy.ServeHTTP(w, r)
		return
	}

	cacheKey := itemID + ":" + mediaSourceID
	if pathValue, ok := s.getCachedMediaPath(cacheKey); ok {
		targetSource, directRemote, managed := s.resolveManagedMediaPath(pathValue)
		if managed && targetSource != "" {
			s.redirectToTarget(w, r, openListProvider, targetSource)
			return
		}
		if directRemote != "" {
			http.Redirect(w, r, directRemote, http.StatusFound)
			return
		}
	}

	playbackInfoURI := s.playbackInfoURI(r.URL.Path, itemID, mediaSourceID, token)
	info, err := s.embyClient.FetchPlaybackInfo(r.Context(), playbackInfoURI, token)
	if err != nil {
		s.proxy.ServeHTTP(w, r)
		return
	}

	mediaSource := findMediaSource(info, mediaSourceID)
	if mediaSource == nil {
		s.proxy.ServeHTTP(w, r)
		return
	}

	pathValue, _ := mediaSource["Path"].(string)
	targetSource, directRemote, ok := s.resolveManagedMediaPath(pathValue)
	if ok && targetSource != "" {
		s.redirectToTarget(w, r, openListProvider, targetSource)
		return
	}
	if directRemote != "" {
		http.Redirect(w, r, directRemote, http.StatusFound)
		return
	}

	s.proxy.ServeHTTP(w, r)
}

func (s *Server) redirectToTarget(w http.ResponseWriter, r *http.Request, provider, sourcePath string) {
	if provider != openListProvider {
		http.NotFound(w, r)
		return
	}

	entry, err := s.client.Get(r.Context(), sourcePath)
	if err != nil {
		http.Error(w, fmt.Sprintf("resolve source path: %v", err), http.StatusBadGateway)
		return
	}

	http.Redirect(w, r, s.client.DownloadURL(entry, sourcePath), http.StatusFound)
}

func (s *Server) authorizePlayTicket(r *http.Request, provider, sourcePath string) (int, error) {
	token := strings.TrimSpace(r.URL.Query().Get(playTicketParam))
	if token == "" {
		if isLoopbackRequest(r) {
			return 0, nil
		}
		return http.StatusForbidden, fmt.Errorf("missing play ticket")
	}

	claims, err := decodePlayTicket([]byte(s.cfg.PlayTicketSecret), token, time.Now())
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
	if len(body) == 0 {
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

		s.cacheMediaPath(itemID+":"+mediaSourceID, pathValue)
		source["Path"] = ticketURL
		rewritten = true

		if !directPlay {
			continue
		}

		directURL, err := s.builder.BuildRelativePlayTicket(managedSource, now, s.cfg.PlayTicketTTL)
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

func (s *Server) playbackInfoURI(pathValue, itemID, mediaSourceID, token string) string {
	prefix := requestPathPrefix(pathValue)
	query := url.Values{}
	query.Set("MediaSourceId", mediaSourceID)
	query.Set("api_key", token)
	return fmt.Sprintf("%s/Items/%s/PlaybackInfo?%s", prefix, itemID, query.Encode())
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

func (s *Server) cacheMediaPath(key, pathValue string) {
	s.mediaPathCacheMu.Lock()
	defer s.mediaPathCacheMu.Unlock()
	s.mediaPathCache[key] = mediaPathCacheEntry{
		pathValue: pathValue,
		expiry:    time.Now().Add(mediaPathCacheTTL),
	}
}

func (s *Server) getCachedMediaPath(key string) (string, bool) {
	s.mediaPathCacheMu.Lock()
	defer s.mediaPathCacheMu.Unlock()
	entry, ok := s.mediaPathCache[key]
	if !ok {
		return "", false
	}
	if time.Now().After(entry.expiry) {
		delete(s.mediaPathCache, key)
		return "", false
	}
	return entry.pathValue, true
}

func (s *Server) sourcePathFromRoute(r *http.Request) (string, string, bool) {
	return s.sourcePathFromEscapedPath(r.URL.EscapedPath())
}

func (s *Server) sourcePathFromEscapedPath(escapedPath string) (string, string, bool) {
	prefix := strings.TrimSuffix(s.cfg.RoutePrefix, "/") + "/"
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
	return parts[0], pathutil.NormalizeSourcePath(decoded), true
}

func (s *Server) buildPlayTicketURL(r *http.Request, sourcePath string, now time.Time) (string, error) {
	publicURL := s.publicURLFromRequest(r)
	if publicURL == "" {
		return s.builder.BuildPlayTicket(sourcePath, now, s.cfg.PlayTicketTTL)
	}
	return s.builder.BuildPlayTicketForPublicURL(publicURL, sourcePath, now, s.cfg.PlayTicketTTL)
}

func (s *Server) publicURLFromRequest(r *http.Request) string {
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
		return strings.TrimSpace(s.cfg.PublicURL)
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
	if matches := videoStreamPathPattern.FindStringSubmatch(pathValue); len(matches) == 2 {
		return matches[1]
	}
	if matches := itemDownloadPathPattern.FindStringSubmatch(pathValue); len(matches) == 2 {
		return matches[1]
	}
	return ""
}

func mediaSourceIDFromRequest(r *http.Request) string {
	return strings.TrimSpace(firstNonEmpty(
		r.URL.Query().Get("MediaSourceId"),
		r.URL.Query().Get("mediaSourceId"),
	))
}

func findMediaSource(payload map[string]any, mediaSourceID string) map[string]any {
	sources, ok := payload["MediaSources"].([]any)
	if !ok {
		return nil
	}
	for _, rawSource := range sources {
		source, ok := rawSource.(map[string]any)
		if !ok {
			continue
		}
		if sourceID, _ := source["Id"].(string); sourceID == mediaSourceID {
			return source
		}
	}
	if len(sources) == 0 {
		return nil
	}
	source, _ := sources[0].(map[string]any)
	return source
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
