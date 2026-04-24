package redirect

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/monlor/emby-pro/internal/config"
	"github.com/monlor/emby-pro/internal/emby"
	"github.com/monlor/emby-pro/internal/openlist"
)

func TestHandleSTRMRequiresValidPlayTicket(t *testing.T) {
	openlistServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/fs/get":
			var req struct {
				Path string `json:"path"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode openlist request: %v", err)
			}
			if req.Path != "/media/demo.mp4" {
				t.Fatalf("unexpected openlist path: %s", req.Path)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code":    200,
				"message": "success",
				"data": map[string]any{
					"name":   "demo.mp4",
					"is_dir": false,
					"sign":   "demo-sign",
				},
			})
		default:
			t.Fatalf("unexpected openlist path: %s", r.URL.Path)
		}
	}))
	defer openlistServer.Close()

	embyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ServerName":"emby"}`))
	}))
	defer embyServer.Close()

	server := newTestServer(t, openlistServer.URL, embyServer.URL, config.RedirectConfig{
		ListenAddr:       ":8097",
		PublicURL:        "https://emby.example.com",
		PlayTicketSecret: "test-secret",
		RoutePrefix:      "/strm",
		PlayTicketTTL:    12 * time.Hour,
	})

	req := httptest.NewRequest(http.MethodGet, "/strm/openlist/media/demo.mp4", nil)
	rec := httptest.NewRecorder()

	server.handleSTRM(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 without ticket, got %d body=%s", rec.Code, rec.Body.String())
	}

	ticketURL, err := server.builder.BuildPlayTicket("/media/demo.mp4", time.Now(), time.Hour)
	if err != nil {
		t.Fatalf("BuildPlayTicket() error = %v", err)
	}
	parsed, err := url.Parse(ticketURL)
	if err != nil {
		t.Fatalf("url.Parse() error = %v", err)
	}

	req = httptest.NewRequest(http.MethodGet, parsed.RequestURI(), nil)
	rec = httptest.NewRecorder()
	server.handleSTRM(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d body=%s", rec.Code, rec.Body.String())
	}
	if got, want := rec.Header().Get("Location"), openlistServer.URL+"/d/media/demo.mp4?sign=demo-sign"; got != want {
		t.Fatalf("redirect location = %s, want %s", got, want)
	}
}

func TestHandleSTRMRejectsExpiredPlayTicket(t *testing.T) {
	embyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer embyServer.Close()

	openlistClient, err := openlist.NewClient(config.OpenListConfig{
		BaseURL:        "http://openlist.invalid",
		Token:          "token",
		RequestTimeout: 5 * time.Second,
		Retry:          1,
		RetryBackoff:   time.Second,
		ListPerPage:    100,
	})
	if err != nil {
		t.Fatalf("openlist.NewClient() error = %v", err)
	}

	embyClient, err := emby.NewClient(config.EmbyConfig{
		BaseURL:        embyServer.URL,
		ValidatePath:   "/System/Info",
		RequestTimeout: 5 * time.Second,
		TokenCacheTTL:  time.Minute,
	})
	if err != nil {
		t.Fatalf("emby.NewClient() error = %v", err)
	}

	server := NewServer(config.RedirectConfig{
		ListenAddr:       ":8097",
		PublicURL:        "http://127.0.0.1:8097",
		PlayTicketSecret: "test-secret",
		RoutePrefix:      "/strm",
		PlayTicketTTL:    time.Hour,
	}, openlistClient, embyClient, AdminCallbacks{}, log.New(io.Discard, "", 0), time.Minute)

	token, err := encodePlayTicket([]byte("test-secret"), playTicketClaims{
		Provider:   openListProvider,
		SourcePath: "/media/demo.mp4",
		ExpiresAt:  time.Now().Add(-time.Minute).Unix(),
	})
	if err != nil {
		t.Fatalf("encodePlayTicket() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/strm/openlist/media/demo.mp4?t="+url.QueryEscape(token), nil)
	rec := httptest.NewRecorder()
	server.handleSTRM(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for expired ticket, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleSTRMAllowsLoopbackWithoutPlayTicket(t *testing.T) {
	openlistServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/api/fs/get" {
			t.Fatalf("unexpected openlist path: %s", r.URL.Path)
		}
		var req struct {
			Path string `json:"path"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode openlist request: %v", err)
		}
		if req.Path != "/media/demo.mp4" {
			t.Fatalf("unexpected openlist path: %s", req.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code":    200,
			"message": "success",
			"data": map[string]any{
				"name":   "demo.mp4",
				"is_dir": false,
				"sign":   "demo-sign",
			},
		})
	}))
	defer openlistServer.Close()

	embyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer embyServer.Close()

	server := newTestServer(t, openlistServer.URL, embyServer.URL, config.RedirectConfig{
		ListenAddr:       ":8097",
		PublicURL:        "http://127.0.0.1:8097",
		PlayTicketSecret: "test-secret",
		RoutePrefix:      "/strm",
		PlayTicketTTL:    time.Hour,
	})

	req := httptest.NewRequest(http.MethodGet, "/strm/openlist/media/demo.mp4", nil)
	req.RemoteAddr = "127.0.0.1:43210"
	rec := httptest.NewRecorder()
	server.handleSTRM(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d body=%s", rec.Code, rec.Body.String())
	}
	if got, want := rec.Header().Get("Location"), openlistServer.URL+"/d/media/demo.mp4?sign=demo-sign"; got != want {
		t.Fatalf("redirect location = %s, want %s", got, want)
	}
}

func TestHandleSTRMUsesOpenListPublicURLForRedirect(t *testing.T) {
	openlistServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/api/fs/get" {
			t.Fatalf("unexpected openlist path: %s", r.URL.Path)
		}
		var req struct {
			Path string `json:"path"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode openlist request: %v", err)
		}
		if req.Path != "/media/demo.mp4" {
			t.Fatalf("unexpected openlist path: %s", req.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code":    200,
			"message": "success",
			"data": map[string]any{
				"name":   "demo.mp4",
				"is_dir": false,
				"sign":   "demo-sign",
			},
		})
	}))
	defer openlistServer.Close()

	embyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer embyServer.Close()

	openlistClient, err := openlist.NewClient(config.OpenListConfig{
		BaseURL:        openlistServer.URL,
		PublicURL:      "https://list.example.com/share",
		Token:          "token",
		RequestTimeout: 5 * time.Second,
		Retry:          1,
		RetryBackoff:   time.Second,
		ListPerPage:    100,
	})
	if err != nil {
		t.Fatalf("openlist.NewClient() error = %v", err)
	}

	embyClient, err := emby.NewClient(config.EmbyConfig{
		BaseURL:        embyServer.URL,
		ValidatePath:   "/System/Info",
		RequestTimeout: 5 * time.Second,
		TokenCacheTTL:  time.Minute,
	})
	if err != nil {
		t.Fatalf("emby.NewClient() error = %v", err)
	}

	server := NewServer(config.RedirectConfig{
		ListenAddr:       ":8097",
		PublicURL:        "https://emby.example.com",
		PlayTicketSecret: "test-secret",
		RoutePrefix:      "/strm",
		PlayTicketTTL:    time.Hour,
	}, openlistClient, embyClient, AdminCallbacks{}, log.New(io.Discard, "", 0), time.Minute)

	req := httptest.NewRequest(http.MethodGet, "/strm/openlist/media/demo.mp4", nil)
	req.RemoteAddr = "127.0.0.1:43210"
	rec := httptest.NewRecorder()
	server.handleSTRM(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d body=%s", rec.Code, rec.Body.String())
	}
	if got, want := rec.Header().Get("Location"), "https://list.example.com/share/d/media/demo.mp4?sign=demo-sign"; got != want {
		t.Fatalf("redirect location = %s, want %s", got, want)
	}
}

func TestHandleSTRMMapsPublicRouteBackToSourcePath(t *testing.T) {
	openlistServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/api/fs/get" {
			t.Fatalf("unexpected openlist path: %s", r.URL.Path)
		}
		var req struct {
			Path string `json:"path"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode openlist request: %v", err)
		}
		if req.Path != "/115pan_cookie/movies/demo.mp4" {
			t.Fatalf("unexpected openlist source path: %s", req.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code":    200,
			"message": "success",
			"data": map[string]any{
				"name":   "demo.mp4",
				"is_dir": false,
				"sign":   "demo-sign",
			},
		})
	}))
	defer openlistServer.Close()

	embyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer embyServer.Close()

	server := newTestServer(t, openlistServer.URL, embyServer.URL, config.RedirectConfig{
		ListenAddr:       ":8097",
		PublicURL:        "https://emby.example.com",
		PlayTicketSecret: "test-secret",
		RoutePrefix:      "/strm",
		PlayTicketTTL:    time.Hour,
	})

	ticketURL, err := server.builder.BuildPlayTicket("/115pan_cookie/movies/demo.mp4", time.Now(), time.Hour)
	if err != nil {
		t.Fatalf("BuildPlayTicket() error = %v", err)
	}
	parsed, err := url.Parse(ticketURL)
	if err != nil {
		t.Fatalf("url.Parse() error = %v", err)
	}
	if got, want := parsed.Path, "/strm/openlist/115pan_cookie/movies/demo.mp4"; got != want {
		t.Fatalf("ticket path = %s, want %s", got, want)
	}

	req := httptest.NewRequest(http.MethodGet, parsed.RequestURI(), nil)
	rec := httptest.NewRecorder()
	server.handleSTRM(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d body=%s", rec.Code, rec.Body.String())
	}
	if got, want := rec.Header().Get("Location"), openlistServer.URL+"/d/115pan_cookie/movies/demo.mp4?sign=demo-sign"; got != want {
		t.Fatalf("redirect location = %s, want %s", got, want)
	}
}

func TestHandlePlaybackInfoRewritesManagedPathToTicket(t *testing.T) {
	builder := NewBuilder(config.RedirectConfig{
		PublicURL:        "http://127.0.0.1:8097",
		PlayTicketSecret: "test-secret",
		RoutePrefix:      "/strm",
	})
	stablePath, err := builder.Build("/115pan_cookie/测试 demo.mp4")
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	embyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/Items/6/PlaybackInfo" {
			t.Fatalf("unexpected emby path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"MediaSources": []map[string]any{
				{
					"Id":                   "mediasource_6",
					"Path":                 stablePath,
					"Container":            "mkv",
					"DirectStreamUrl":      "/videos/6/original.mkv?api_key=token",
					"TranscodingUrl":       "/videos/6/master.m3u8?api_key=token",
					"SupportsDirectPlay":   false,
					"SupportsDirectStream": false,
					"SupportsTranscoding":  true,
				},
			},
			"PlaySessionId": "play-session",
		})
	}))
	defer embyServer.Close()

	server := newTestServer(t, "http://openlist.invalid", embyServer.URL, config.RedirectConfig{
		DirectPlay:       true,
		ListenAddr:       ":8097",
		PublicURL:        "http://127.0.0.1:8097",
		PlayTicketSecret: "test-secret",
		RoutePrefix:      "/strm",
		PlayTicketTTL:    12 * time.Hour,
	})

	req := httptest.NewRequest(http.MethodPost, "/Items/6/PlaybackInfo?api_key=valid-token&MediaSourceId=mediasource_6", io.NopCloser(strings.NewReader(`{}`)))
	req.Host = "127.0.0.1:8097"
	rec := httptest.NewRecorder()

	server.handlePlaybackInfo(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	source := payload["MediaSources"].([]any)[0].(map[string]any)

	pathValue, _ := source["Path"].(string)
	parsedPath, err := url.Parse(pathValue)
	if err != nil {
		t.Fatalf("url.Parse(path) error = %v", err)
	}
	if got, want := parsedPath.Path, "/strm/openlist/115pan_cookie/测试 demo.mp4"; got != want {
		t.Fatalf("rewritten path = %s, want %s", got, want)
	}
	if parsedPath.Query().Get(playTicketParam) == "" {
		t.Fatalf("expected play ticket in rewritten path, got %s", pathValue)
	}

	directStreamURL, _ := source["DirectStreamUrl"].(string)
	parsedDirect, err := url.Parse(directStreamURL)
	if err != nil {
		t.Fatalf("url.Parse(direct stream) error = %v", err)
	}
	if parsedDirect.Scheme != "" || parsedDirect.Host != "" {
		t.Fatalf("expected relative DirectStreamUrl, got %s", directStreamURL)
	}
	if got, want := parsedDirect.Path, "/strm/openlist/115pan_cookie/测试 demo.mp4"; got != want {
		t.Fatalf("unexpected DirectStreamUrl path: %s want %s", got, want)
	}
	if parsedDirect.Query().Get(playTicketParam) == "" {
		t.Fatalf("unexpected DirectStreamUrl: %s", directStreamURL)
	}
	if _, ok := source["TranscodingUrl"]; ok {
		t.Fatalf("expected TranscodingUrl to be removed")
	}
	if source["SupportsTranscoding"] != false {
		t.Fatalf("expected SupportsTranscoding=false, got %v", source["SupportsTranscoding"])
	}
}

func TestHandlePlaybackInfoFastPathBuildsFromItemMetadata(t *testing.T) {
	builder := NewBuilder(config.RedirectConfig{
		PublicURL:        "http://127.0.0.1:8097",
		PlayTicketSecret: "test-secret",
		RoutePrefix:      "/strm",
	})
	stablePath, err := builder.Build("/media/demo.mp4")
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	embyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/Items/6/PlaybackInfo" {
			t.Fatalf("fast path should not call PlaybackInfo")
		}
		if r.URL.Path != "/Users/user-id/Items/6" {
			t.Fatalf("unexpected emby path: %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("Fields"); got == "" {
			t.Fatalf("expected Fields query, got empty")
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"Chapters": []map[string]any{
				{"StartPositionTicks": 0, "Name": "Chapter 1"},
			},
			"MediaSources": []map[string]any{
				{
					"Id":                   "mediasource_6",
					"Path":                 stablePath,
					"Container":            "mkv",
					"Protocol":             "Http",
					"SupportsDirectPlay":   true,
					"SupportsDirectStream": true,
					"SupportsTranscoding":  true,
					"MediaStreams": []map[string]any{
						{"Index": 0, "Type": "Video"},
					},
				},
			},
			"RunTimeTicks": 123456789,
		})
	}))
	defer embyServer.Close()

	server := newTestServer(t, "http://openlist.invalid", embyServer.URL, config.RedirectConfig{
		DirectPlay:       true,
		FastPlaybackInfo: true,
		ListenAddr:       ":8097",
		PublicURL:        "http://127.0.0.1:8097",
		PlayTicketSecret: "test-secret",
		RoutePrefix:      "/strm",
		PlayTicketTTL:    12 * time.Hour,
	})

	req := httptest.NewRequest(http.MethodPost, "/Items/6/PlaybackInfo?api_key=valid-token&UserId=user-id&MediaSourceId=mediasource_6", io.NopCloser(strings.NewReader(`{}`)))
	rec := httptest.NewRecorder()

	server.handlePlaybackInfo(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	playSessionID, _ := payload["PlaySessionId"].(string)
	if playSessionID == "" {
		t.Fatalf("expected fast path to include non-empty PlaySessionId")
	}
	source := payload["MediaSources"].([]any)[0].(map[string]any)
	pathValue, _ := source["Path"].(string)
	parsedPath, err := url.Parse(pathValue)
	if err != nil {
		t.Fatalf("url.Parse(path) error = %v", err)
	}
	if got, want := parsedPath.Path, "/strm/openlist/media/demo.mp4"; got != want {
		t.Fatalf("unexpected path: %s want %s", got, want)
	}
	if parsedPath.Query().Get(playTicketParam) == "" {
		t.Fatalf("expected play ticket in path: %s", pathValue)
	}
	chapters, _ := source["Chapters"].([]any)
	if len(chapters) != 1 {
		t.Fatalf("expected chapters to be copied onto source, got %d", len(chapters))
	}
}

func TestHandlePlaybackInfoFastPathFallsBackWhenDirectPlayDisabled(t *testing.T) {
	builder := NewBuilder(config.RedirectConfig{
		PublicURL:        "http://127.0.0.1:8097",
		PlayTicketSecret: "test-secret",
		RoutePrefix:      "/strm",
	})
	stablePath, err := builder.Build("/media/demo.mp4")
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	var playbackInfoCalls int
	embyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/Items/6/PlaybackInfo" {
			t.Fatalf("unexpected emby path: %s", r.URL.Path)
		}
		playbackInfoCalls++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"MediaSources": []map[string]any{
				{
					"Id":                   "mediasource_6",
					"Path":                 stablePath,
					"Container":            "mkv",
					"SupportsDirectPlay":   false,
					"SupportsDirectStream": false,
					"SupportsTranscoding":  true,
					"TranscodingUrl":       "/videos/6/master.m3u8?api_key=token",
				},
			},
			"PlaySessionId": "play-session",
		})
	}))
	defer embyServer.Close()

	server := newTestServer(t, "http://openlist.invalid", embyServer.URL, config.RedirectConfig{
		DirectPlay:       false,
		FastPlaybackInfo: true,
		ListenAddr:       ":8097",
		PublicURL:        "http://127.0.0.1:8097",
		PlayTicketSecret: "test-secret",
		RoutePrefix:      "/strm",
		PlayTicketTTL:    12 * time.Hour,
	})

	req := httptest.NewRequest(http.MethodPost, "/Items/6/PlaybackInfo?api_key=valid-token&UserId=user-id&MediaSourceId=mediasource_6", io.NopCloser(strings.NewReader(`{}`)))
	rec := httptest.NewRecorder()

	server.handlePlaybackInfo(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if playbackInfoCalls != 1 {
		t.Fatalf("expected fallback PlaybackInfo call, got %d", playbackInfoCalls)
	}
}

func TestHandlePlaybackInfoFastPathRespectsWebDirectPlayFlag(t *testing.T) {
	builder := NewBuilder(config.RedirectConfig{
		PublicURL:        "http://127.0.0.1:8097",
		PlayTicketSecret: "test-secret",
		RoutePrefix:      "/strm",
	})
	stablePath, err := builder.Build("/media/demo.mp4")
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	var itemCalls int
	var playbackInfoCalls int
	embyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/Users/user-id/Items/6":
			itemCalls++
			t.Fatalf("fast path should not fetch item metadata when web direct play is disabled")
		case "/Items/6/PlaybackInfo":
			playbackInfoCalls++
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"MediaSources": []map[string]any{
					{
						"Id":                   "mediasource_6",
						"Path":                 stablePath,
						"Container":            "mkv",
						"Protocol":             "Http",
						"SupportsDirectPlay":   false,
						"SupportsDirectStream": false,
						"SupportsTranscoding":  true,
						"TranscodingUrl":       "/videos/6/master.m3u8?api_key=token",
					},
				},
				"PlaySessionId": "play-session",
			})
		default:
			t.Fatalf("unexpected emby path: %s", r.URL.Path)
		}
	}))
	defer embyServer.Close()

	server := newTestServer(t, "http://openlist.invalid", embyServer.URL, config.RedirectConfig{
		DirectPlay:       true,
		DirectPlayWeb:    false,
		FastPlaybackInfo: true,
		ListenAddr:       ":8097",
		PublicURL:        "http://127.0.0.1:8097",
		PlayTicketSecret: "test-secret",
		RoutePrefix:      "/strm",
		PlayTicketTTL:    12 * time.Hour,
	})

	req := httptest.NewRequest(http.MethodPost, "/Items/6/PlaybackInfo?api_key=valid-token&UserId=user-id&MediaSourceId=mediasource_6&X-Emby-Client=Emby+Web", io.NopCloser(strings.NewReader(`{}`)))
	rec := httptest.NewRecorder()

	server.handlePlaybackInfo(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if itemCalls != 0 {
		t.Fatalf("expected no item metadata calls, got %d", itemCalls)
	}
	if playbackInfoCalls != 1 {
		t.Fatalf("expected one PlaybackInfo call, got %d", playbackInfoCalls)
	}

	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	source := payload["MediaSources"].([]any)[0].(map[string]any)
	pathValue, _ := source["Path"].(string)
	if pathValue != stablePath {
		t.Fatalf("expected path to remain upstream when web direct play disabled, got %s", pathValue)
	}
	if _, ok := source["DirectStreamUrl"]; ok {
		t.Fatalf("expected no direct stream rewrite when web direct play disabled")
	}
}

func TestHandlePlaybackInfoPrefersRequestDerivedPublicURL(t *testing.T) {
	builder := NewBuilder(config.RedirectConfig{
		PublicURL:        "http://127.0.0.1:8097",
		PlayTicketSecret: "test-secret",
		RoutePrefix:      "/strm",
	})
	stablePath, err := builder.Build("/media/demo.mp4")
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	embyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/emby/Items/6/PlaybackInfo" {
			t.Fatalf("unexpected emby path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"MediaSources": []map[string]any{
				{
					"Id":                   "mediasource_6",
					"Path":                 stablePath,
					"Container":            "mkv",
					"DirectStreamUrl":      "/videos/6/original.mkv?api_key=token",
					"TranscodingUrl":       "/videos/6/master.m3u8?api_key=token",
					"SupportsDirectPlay":   false,
					"SupportsDirectStream": false,
					"SupportsTranscoding":  true,
				},
			},
			"PlaySessionId": "play-session",
		})
	}))
	defer embyServer.Close()

	server := newTestServer(t, "http://openlist.invalid", embyServer.URL, config.RedirectConfig{
		DirectPlay:       true,
		ListenAddr:       ":8097",
		PublicURL:        "http://127.0.0.1:8097",
		PlayTicketSecret: "test-secret",
		RoutePrefix:      "/strm",
		PlayTicketTTL:    12 * time.Hour,
	})

	req := httptest.NewRequest(http.MethodPost, "/emby/Items/6/PlaybackInfo?api_key=valid-token&MediaSourceId=mediasource_6", io.NopCloser(strings.NewReader(`{}`)))
	req.Host = "media.example.com"
	req.Header.Set("X-Forwarded-Proto", "https")
	rec := httptest.NewRecorder()

	server.handlePlaybackInfo(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	source := payload["MediaSources"].([]any)[0].(map[string]any)
	pathValue, _ := source["Path"].(string)
	parsedPath, err := url.Parse(pathValue)
	if err != nil {
		t.Fatalf("url.Parse(path) error = %v", err)
	}
	if got, want := parsedPath.Scheme, "https"; got != want {
		t.Fatalf("rewritten scheme = %s, want %s", got, want)
	}
	if got, want := parsedPath.Host, "media.example.com"; got != want {
		t.Fatalf("rewritten host = %s, want %s", got, want)
	}
	if got, want := parsedPath.Path, "/emby/strm/openlist/media/demo.mp4"; got != want {
		t.Fatalf("rewritten path = %s, want %s", got, want)
	}
}

func TestHandleProxyEmbyPreservesTranscodingWhenDirectPlayDisabled(t *testing.T) {
	builder := NewBuilder(config.RedirectConfig{
		PublicURL:        "http://127.0.0.1:8097",
		PlayTicketSecret: "test-secret",
		RoutePrefix:      "/strm",
	})
	stablePath, err := builder.Build("/media/demo.mp4")
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	embyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/Items/6/PlaybackInfo" {
			t.Fatalf("unexpected emby path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"MediaSources": []map[string]any{
				{
					"Id":                   "mediasource_6",
					"Path":                 stablePath,
					"Container":            "mkv",
					"DirectStreamUrl":      "/videos/6/original.mkv?api_key=token",
					"TranscodingUrl":       "/videos/6/master.m3u8?api_key=token",
					"SupportsDirectPlay":   false,
					"SupportsDirectStream": false,
					"SupportsTranscoding":  true,
				},
			},
		})
	}))
	defer embyServer.Close()

	server := newTestServer(t, "http://openlist.invalid", embyServer.URL, config.RedirectConfig{
		DirectPlay:       false,
		ListenAddr:       ":8097",
		PublicURL:        "http://127.0.0.1:8097",
		PlayTicketSecret: "test-secret",
		RoutePrefix:      "/strm",
		PlayTicketTTL:    12 * time.Hour,
	})

	req := httptest.NewRequest(http.MethodPost, "/Items/6/PlaybackInfo?api_key=valid-token&MediaSourceId=mediasource_6", io.NopCloser(strings.NewReader(`{}`)))
	rec := httptest.NewRecorder()
	server.handleProxyEmby(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	source := payload["MediaSources"].([]any)[0].(map[string]any)

	pathValue, _ := source["Path"].(string)
	parsedPath, err := url.Parse(pathValue)
	if err != nil {
		t.Fatalf("url.Parse(path) error = %v", err)
	}
	if got, want := parsedPath.Path, "/strm/openlist/media/demo.mp4"; got != want {
		t.Fatalf("path = %s, want %s", got, want)
	}
	if parsedPath.Query().Get(playTicketParam) != "" {
		t.Fatalf("expected path to stay unchanged, got %s", pathValue)
	}

	directStreamURL, _ := source["DirectStreamUrl"].(string)
	if got, want := directStreamURL, "/videos/6/original.mkv?api_key=token"; got != want {
		t.Fatalf("DirectStreamUrl = %s, want %s", got, want)
	}
	if _, ok := source["TranscodingUrl"]; !ok {
		t.Fatalf("expected TranscodingUrl to be preserved")
	}
	if source["SupportsTranscoding"] != true {
		t.Fatalf("expected SupportsTranscoding=true, got %v", source["SupportsTranscoding"])
	}
}

func TestHandlePlaybackInfoPreservesTranscodingForWebWhenWebDirectPlayDisabled(t *testing.T) {
	builder := NewBuilder(config.RedirectConfig{
		PublicURL:        "http://127.0.0.1:8097",
		PlayTicketSecret: "test-secret",
		RoutePrefix:      "/strm",
	})
	stablePath, err := builder.Build("/media/demo.mp4")
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	embyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/Items/6/PlaybackInfo" {
			t.Fatalf("unexpected emby path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"MediaSources": []map[string]any{
				{
					"Id":                   "mediasource_6",
					"Path":                 stablePath,
					"Container":            "mkv",
					"DirectStreamUrl":      "/videos/6/original.mkv?api_key=token",
					"TranscodingUrl":       "/videos/6/master.m3u8?api_key=token",
					"SupportsDirectPlay":   false,
					"SupportsDirectStream": false,
					"SupportsTranscoding":  true,
				},
			},
		})
	}))
	defer embyServer.Close()

	server := newTestServer(t, "http://openlist.invalid", embyServer.URL, config.RedirectConfig{
		DirectPlay:       true,
		DirectPlayWeb:    false,
		ListenAddr:       ":8097",
		PublicURL:        "http://127.0.0.1:8097",
		PlayTicketSecret: "test-secret",
		RoutePrefix:      "/strm",
		PlayTicketTTL:    12 * time.Hour,
	})

	req := httptest.NewRequest(http.MethodPost, "/Items/6/PlaybackInfo?api_key=valid-token&MediaSourceId=mediasource_6", io.NopCloser(strings.NewReader(`{}`)))
	req.Header.Set("X-Emby-Client", "Emby Web")
	rec := httptest.NewRecorder()
	server.handleProxyEmby(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	source := payload["MediaSources"].([]any)[0].(map[string]any)

	pathValue, _ := source["Path"].(string)
	parsedPath, err := url.Parse(pathValue)
	if err != nil {
		t.Fatalf("url.Parse(path) error = %v", err)
	}
	if got, want := parsedPath.Path, "/strm/openlist/media/demo.mp4"; got != want {
		t.Fatalf("path = %s, want %s", got, want)
	}
	if parsedPath.Query().Get(playTicketParam) != "" {
		t.Fatalf("expected path to stay unchanged for web, got %s", pathValue)
	}

	directStreamURL, _ := source["DirectStreamUrl"].(string)
	if got, want := directStreamURL, "/videos/6/original.mkv?api_key=token"; got != want {
		t.Fatalf("DirectStreamUrl = %s, want %s", got, want)
	}
	if _, ok := source["TranscodingUrl"]; !ok {
		t.Fatalf("expected TranscodingUrl to be preserved for web")
	}
	if source["SupportsTranscoding"] != true {
		t.Fatalf("expected SupportsTranscoding=true, got %v", source["SupportsTranscoding"])
	}
}

func TestHandlePlaybackInfoRewritesDirectStreamForWebWhenEnabled(t *testing.T) {
	builder := NewBuilder(config.RedirectConfig{
		PublicURL:        "http://127.0.0.1:8097",
		PlayTicketSecret: "test-secret",
		RoutePrefix:      "/strm",
	})
	stablePath, err := builder.Build("/media/demo.mp4")
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	embyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/Items/6/PlaybackInfo" {
			t.Fatalf("unexpected emby path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"MediaSources": []map[string]any{
				{
					"Id":                   "mediasource_6",
					"Path":                 stablePath,
					"Container":            "mkv",
					"DirectStreamUrl":      "/videos/6/original.mkv?api_key=token",
					"TranscodingUrl":       "/videos/6/master.m3u8?api_key=token",
					"SupportsDirectPlay":   false,
					"SupportsDirectStream": false,
					"SupportsTranscoding":  true,
				},
			},
		})
	}))
	defer embyServer.Close()

	server := newTestServer(t, "http://openlist.invalid", embyServer.URL, config.RedirectConfig{
		DirectPlay:       true,
		DirectPlayWeb:    true,
		ListenAddr:       ":8097",
		PublicURL:        "http://127.0.0.1:8097",
		PlayTicketSecret: "test-secret",
		RoutePrefix:      "/strm",
		PlayTicketTTL:    12 * time.Hour,
	})

	req := httptest.NewRequest(http.MethodPost, "/Items/6/PlaybackInfo?api_key=valid-token&MediaSourceId=mediasource_6", io.NopCloser(strings.NewReader(`{}`)))
	req.Header.Set("X-Emby-Client", "Emby Web")
	rec := httptest.NewRecorder()
	server.handlePlaybackInfo(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	source := payload["MediaSources"].([]any)[0].(map[string]any)

	directStreamURL, _ := source["DirectStreamUrl"].(string)
	parsedDirect, err := url.Parse(directStreamURL)
	if err != nil {
		t.Fatalf("url.Parse(direct stream) error = %v", err)
	}
	if got, want := parsedDirect.Path, "/strm/openlist/media/demo.mp4"; got != want {
		t.Fatalf("unexpected DirectStreamUrl path: %s want %s", got, want)
	}
	if parsedDirect.Query().Get(playTicketParam) == "" {
		t.Fatalf("unexpected DirectStreamUrl: %s", directStreamURL)
	}
	if _, ok := source["TranscodingUrl"]; ok {
		t.Fatalf("expected TranscodingUrl to be removed")
	}
	if source["SupportsTranscoding"] != false {
		t.Fatalf("expected SupportsTranscoding=false, got %v", source["SupportsTranscoding"])
	}
}

func TestIsWebClientRequest(t *testing.T) {
	cases := []struct {
		name    string
		header  http.Header
		wantWeb bool
	}{
		{
			name:    "emby client header",
			header:  http.Header{"X-Emby-Client": []string{"Emby Web"}},
			wantWeb: true,
		},
		{
			name:    "authorization header",
			header:  http.Header{"Authorization": []string{`MediaBrowser Client="Emby Web", Device="Chrome", DeviceId="web-device"`}},
			wantWeb: true,
		},
		{
			name:    "native client",
			header:  http.Header{"X-Emby-Client": []string{"Emby Android"}},
			wantWeb: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Header = tc.header
			if got := isWebClientRequest(req); got != tc.wantWeb {
				t.Fatalf("isWebClientRequest() = %v, want %v", got, tc.wantWeb)
			}
		})
	}
}

func TestHandleProxyEmbyAcceptsManagedPathWithEmbyPrefix(t *testing.T) {
	openlistServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/api/fs/get" {
			t.Fatalf("unexpected openlist path: %s", r.URL.Path)
		}
		var req struct {
			Path string `json:"path"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode openlist request: %v", err)
		}
		if req.Path != "/media/demo.mp4" {
			t.Fatalf("unexpected source path: %s", req.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code":    200,
			"message": "success",
			"data": map[string]any{
				"name":   "demo.mp4",
				"is_dir": false,
				"sign":   "demo-sign",
			},
		})
	}))
	defer openlistServer.Close()

	embyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer embyServer.Close()

	server := newTestServer(t, openlistServer.URL, embyServer.URL, config.RedirectConfig{
		ListenAddr:       ":8097",
		PublicURL:        "http://127.0.0.1:8097",
		PlayTicketSecret: "test-secret",
		RoutePrefix:      "/strm",
		PlayTicketTTL:    12 * time.Hour,
	})

	token, err := encodePlayTicket([]byte("test-secret"), playTicketClaims{
		Provider:   openListProvider,
		SourcePath: "/media/demo.mp4",
		ExpiresAt:  time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatalf("encodePlayTicket() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/emby/strm/openlist/media/demo.mp4?t="+url.QueryEscape(token), nil)
	rec := httptest.NewRecorder()
	server.handleProxyEmby(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d body=%s", rec.Code, rec.Body.String())
	}
	if got, want := rec.Header().Get("Location"), openlistServer.URL+"/d/media/demo.mp4?sign=demo-sign"; got != want {
		t.Fatalf("redirect location = %s, want %s", got, want)
	}
}

func TestHandlePlaybackInfoRewritesMediaSourcesWithEmbyPrefix(t *testing.T) {
	builder := NewBuilder(config.RedirectConfig{
		PublicURL:        "http://127.0.0.1:8097",
		PlayTicketSecret: "test-secret",
		RoutePrefix:      "/strm",
	})
	stablePath, err := builder.Build("/media/demo.mp4")
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	embyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/emby/Items/6/PlaybackInfo" {
			t.Fatalf("unexpected emby path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"MediaSources": []map[string]any{
				{
					"Id":                   "mediasource_6",
					"Path":                 stablePath,
					"Container":            "mkv",
					"DirectStreamUrl":      "/videos/6/original.mkv?api_key=token",
					"TranscodingUrl":       "/videos/6/master.m3u8?api_key=token",
					"SupportsDirectPlay":   false,
					"SupportsDirectStream": false,
					"SupportsTranscoding":  true,
				},
			},
			"PlaySessionId": "play-session",
		})
	}))
	defer embyServer.Close()

	server := newTestServer(t, "http://openlist.invalid", embyServer.URL, config.RedirectConfig{
		DirectPlay:       true,
		ListenAddr:       ":8097",
		PublicURL:        "http://127.0.0.1:8097",
		PlayTicketSecret: "test-secret",
		RoutePrefix:      "/strm",
		PlayTicketTTL:    12 * time.Hour,
	})

	req := httptest.NewRequest(http.MethodPost, "/emby/Items/6/PlaybackInfo?api_key=valid-token&MediaSourceId=mediasource_6", io.NopCloser(strings.NewReader(`{}`)))
	req.Host = "127.0.0.1:8097"
	rec := httptest.NewRecorder()
	server.handlePlaybackInfo(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	source := payload["MediaSources"].([]any)[0].(map[string]any)

	directStreamURL, _ := source["DirectStreamUrl"].(string)
	parsedDirect, err := url.Parse(directStreamURL)
	if err != nil {
		t.Fatalf("url.Parse(direct stream) error = %v", err)
	}
	if parsedDirect.Scheme != "" || parsedDirect.Host != "" {
		t.Fatalf("expected relative DirectStreamUrl, got %s", directStreamURL)
	}
	if got, want := parsedDirect.Path, "/strm/openlist/media/demo.mp4"; got != want {
		t.Fatalf("unexpected DirectStreamUrl path: %s want %s", got, want)
	}
	if parsedDirect.Query().Get(playTicketParam) == "" {
		t.Fatalf("unexpected DirectStreamUrl: %s", directStreamURL)
	}
}

func newTestServer(t *testing.T, openlistBaseURL, embyBaseURL string, redirectCfg config.RedirectConfig) *Server {
	t.Helper()

	openlistClient, err := openlist.NewClient(config.OpenListConfig{
		BaseURL:        openlistBaseURL,
		Token:          "token",
		RequestTimeout: 5 * time.Second,
		Retry:          1,
		RetryBackoff:   time.Second,
		ListPerPage:    100,
	})
	if err != nil {
		t.Fatalf("openlist.NewClient() error = %v", err)
	}

	embyClient, err := emby.NewClient(config.EmbyConfig{
		BaseURL:        embyBaseURL,
		ValidatePath:   "/System/Info",
		RequestTimeout: 5 * time.Second,
		TokenCacheTTL:  time.Minute,
	})
	if err != nil {
		t.Fatalf("emby.NewClient() error = %v", err)
	}

	return NewServer(redirectCfg, openlistClient, embyClient, AdminCallbacks{}, log.New(io.Discard, "", 0), time.Minute)
}
