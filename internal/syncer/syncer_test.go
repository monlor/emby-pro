package syncer

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/monlor/emby-pro/internal/config"
	"github.com/monlor/emby-pro/internal/index"
	"github.com/monlor/emby-pro/internal/openlist"
)

func TestRunOnceScansNewChildDirsInSameCycle(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/api/fs/list" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}

		var req struct {
			Path string `json:"path"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}

		var content []map[string]any
		switch req.Path {
		case "/media":
			content = []map[string]any{
				{
					"name":     "movies",
					"is_dir":   true,
					"size":     0,
					"modified": time.Now().Format(time.RFC3339),
				},
			}
		case "/media/movies":
			content = []map[string]any{
				{
					"name":     "demo.mp4",
					"is_dir":   false,
					"size":     123,
					"modified": time.Now().Format(time.RFC3339),
				},
			}
		default:
			content = []map[string]any{}
		}

		_ = json.NewEncoder(w).Encode(map[string]any{
			"code":    200,
			"message": "success",
			"data": map[string]any{
				"total":   len(content),
				"content": content,
			},
		})
	}))
	defer server.Close()

	tempDir := t.TempDir()
	store, err := index.Open(filepath.Join(tempDir, "index.db"))
	if err != nil {
		t.Fatalf("index.Open() error = %v", err)
	}
	defer store.Close()

	client, err := openlist.NewClient(config.OpenListConfig{
		BaseURL:        server.URL,
		Token:          "token",
		RequestTimeout: 5 * time.Second,
		Retry:          1,
		RetryBackoff:   time.Second,
		ListPerPage:    100,
	})
	if err != nil {
		t.Fatalf("openlist.NewClient() error = %v", err)
	}

	cfg := config.Config{
		OpenList: config.OpenListConfig{
			BaseURL:        server.URL,
			Token:          "token",
			RequestTimeout: 5 * time.Second,
			Retry:          1,
			RetryBackoff:   time.Second,
			ListPerPage:    100,
		},
		Redirect: config.RedirectConfig{
			PublicURL:        "http://127.0.0.1:18097",
			ListenAddr:       "127.0.0.1:18097",
			PlayTicketSecret: "test-secret",
			PlayTicketTTL:    12 * time.Hour,
		},
		Sync: config.SyncConfig{
			BaseDir:             filepath.Join(tempDir, "strm"),
			RuleFile:            filepath.Join(tempDir, "none.yml"),
			IndexDB:             filepath.Join(tempDir, "index.db"),
			ScanInterval:        time.Millisecond,
			FullRescanInterval:  time.Hour,
			MaxDirsPerCycle:     10,
			MaxRequestsPerCycle: 20,
			VideoExts: map[string]struct{}{
				".mp4": {},
			},
			CleanRemoved: true,
			Overwrite:    true,
			LogLevel:     "debug",
		},
		Rules: []config.Rule{
			{
				Name:       "media",
				SourcePath: "/media",
				TargetPath: filepath.Join(tempDir, "strm", "media"),
			},
		},
	}

	s := New(cfg, store, client)
	if err := s.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}

	var files []string
	err = filepath.Walk(filepath.Join(tempDir, "strm"), func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("filepath.Walk() error = %v", err)
	}

	if len(files) != 1 {
		t.Fatalf("expected 1 strm file, got %d (%v)", len(files), files)
	}
	content, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatalf("os.ReadFile() error = %v", err)
	}
	if got, want := string(content), "http://127.0.0.1:18097/strm/openlist/media/movies/demo.mp4"; got != want {
		t.Fatalf("strm content = %s, want %s", got, want)
	}
}

func TestRunOnceSkipsFilesSmallerThanMinFileSize(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/api/fs/list" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}

		_ = json.NewEncoder(w).Encode(map[string]any{
			"code":    200,
			"message": "success",
			"data": map[string]any{
				"total": 1,
				"content": []map[string]any{
					{
						"name":     "demo.mp4",
						"is_dir":   false,
						"size":     123,
						"modified": time.Now().Format(time.RFC3339),
					},
				},
			},
		})
	}))
	defer server.Close()

	tempDir := t.TempDir()
	store, err := index.Open(filepath.Join(tempDir, "index.db"))
	if err != nil {
		t.Fatalf("index.Open() error = %v", err)
	}
	defer store.Close()

	client, err := openlist.NewClient(config.OpenListConfig{
		BaseURL:        server.URL,
		Token:          "token",
		RequestTimeout: 5 * time.Second,
		Retry:          1,
		RetryBackoff:   time.Second,
		ListPerPage:    100,
	})
	if err != nil {
		t.Fatalf("openlist.NewClient() error = %v", err)
	}

	cfg := config.Config{
		OpenList: config.OpenListConfig{
			BaseURL:        server.URL,
			Token:          "token",
			RequestTimeout: 5 * time.Second,
			Retry:          1,
			RetryBackoff:   time.Second,
			ListPerPage:    100,
		},
		Redirect: config.RedirectConfig{
			PublicURL:        "http://127.0.0.1:18097",
			ListenAddr:       "127.0.0.1:18097",
			PlayTicketSecret: "test-secret",
			PlayTicketTTL:    12 * time.Hour,
		},
		Sync: config.SyncConfig{
			BaseDir:             filepath.Join(tempDir, "strm"),
			RuleFile:            filepath.Join(tempDir, "none.yml"),
			IndexDB:             filepath.Join(tempDir, "index.db"),
			ScanInterval:        time.Millisecond,
			FullRescanInterval:  time.Hour,
			MaxDirsPerCycle:     10,
			MaxRequestsPerCycle: 20,
			MinFileSize:         200,
			VideoExts: map[string]struct{}{
				".mp4": {},
			},
			CleanRemoved: true,
			Overwrite:    true,
			LogLevel:     "debug",
		},
		Rules: []config.Rule{
			{
				Name:       "media",
				SourcePath: "/media",
				TargetPath: filepath.Join(tempDir, "strm", "media"),
			},
		},
	}

	s := New(cfg, store, client)
	if err := s.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}

	targetFile := filepath.Join(tempDir, "strm", "media", "demo.strm")
	if _, err := os.Stat(targetFile); !os.IsNotExist(err) {
		t.Fatalf("expected no strm file for small source, got err=%v", err)
	}

	files, err := store.ListFilesByRule("media")
	if err != nil {
		t.Fatalf("ListFilesByRule() error = %v", err)
	}
	if len(files) != 0 {
		t.Fatalf("expected no tracked files, got %d", len(files))
	}
}

func TestRunOnceWritesMappedPublicSTRMPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/api/fs/list" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}

		var req struct {
			Path string `json:"path"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.Path != "/115pan_cookie" {
			t.Fatalf("list path = %s, want %s", req.Path, "/115pan_cookie")
		}

		_ = json.NewEncoder(w).Encode(map[string]any{
			"code":    200,
			"message": "success",
			"data": map[string]any{
				"total": 1,
				"content": []map[string]any{
					{
						"name":     "demo.mp4",
						"is_dir":   false,
						"size":     123,
						"modified": time.Now().Format(time.RFC3339),
					},
				},
			},
		})
	}))
	defer server.Close()

	tempDir := t.TempDir()
	store, err := index.Open(filepath.Join(tempDir, "index.db"))
	if err != nil {
		t.Fatalf("index.Open() error = %v", err)
	}
	defer store.Close()

	client, err := openlist.NewClient(config.OpenListConfig{
		BaseURL:        server.URL,
		Token:          "token",
		RequestTimeout: 5 * time.Second,
		Retry:          1,
		RetryBackoff:   time.Second,
		ListPerPage:    100,
	})
	if err != nil {
		t.Fatalf("openlist.NewClient() error = %v", err)
	}

	cfg := config.Config{
		OpenList: config.OpenListConfig{
			BaseURL:        server.URL,
			Token:          "token",
			RequestTimeout: 5 * time.Second,
			Retry:          1,
			RetryBackoff:   time.Second,
			ListPerPage:    100,
		},
		Redirect: config.RedirectConfig{
			PublicURL:        "http://127.0.0.1:18097",
			ListenAddr:       "127.0.0.1:18097",
			PathMappings:     []config.PathMapping{{SourcePrefix: "/115pan_cookie", PublicPrefix: "/115pan"}},
			PlayTicketSecret: "test-secret",
			PlayTicketTTL:    12 * time.Hour,
		},
		Sync: config.SyncConfig{
			BaseDir:             filepath.Join(tempDir, "strm"),
			RuleFile:            filepath.Join(tempDir, "none.yml"),
			IndexDB:             filepath.Join(tempDir, "index.db"),
			ScanInterval:        time.Millisecond,
			FullRescanInterval:  time.Hour,
			MaxDirsPerCycle:     10,
			MaxRequestsPerCycle: 20,
			VideoExts: map[string]struct{}{
				".mp4": {},
			},
			CleanRemoved: true,
			Overwrite:    true,
			LogLevel:     "debug",
		},
		Rules: []config.Rule{
			{
				Name:       "115pan-cookie",
				SourcePath: "/115pan_cookie",
				TargetPath: filepath.Join(tempDir, "strm", "115pan_cookie"),
			},
		},
	}

	s := New(cfg, store, client)
	if err := s.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}

	content, err := os.ReadFile(filepath.Join(tempDir, "strm", "115pan_cookie", "demo.strm"))
	if err != nil {
		t.Fatalf("os.ReadFile() error = %v", err)
	}
	if got, want := string(content), "http://127.0.0.1:18097/strm/openlist/115pan/demo.mp4"; got != want {
		t.Fatalf("strm content = %s, want %s", got, want)
	}
}

func TestResolveWriteConflictsPrefersLargestThenExtension(t *testing.T) {
	s := &Syncer{
		logger: log.New(io.Discard, "", 0),
	}
	writes, activeTargets := s.resolveWriteConflicts([]fileWrite{
		{
			sourcePath: "/media/demo.mp4",
			targetPath: "/strm/demo.strm",
			size:       100,
			name:       "demo.mp4",
		},
		{
			sourcePath: "/media/demo.mkv",
			targetPath: "/strm/demo.strm",
			size:       200,
			name:       "demo.mkv",
		},
		{
			sourcePath: "/media/other.mp4",
			targetPath: "/strm/other.strm",
			size:       50,
			name:       "other.mp4",
		},
	})

	if len(writes) != 2 {
		t.Fatalf("expected 2 writes, got %d", len(writes))
	}
	if writes[0].sourcePath != "/media/demo.mkv" {
		t.Fatalf("expected mkv to win, got %s", writes[0].sourcePath)
	}
	if _, ok := activeTargets["/strm/demo.strm"]; !ok {
		t.Fatalf("expected activeTargets to include demo.strm")
	}
}

func TestRunOnceDoesNotResolvePlayURLDuringSync(t *testing.T) {
	var listCalls int
	var getCalls int

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch r.URL.Path {
		case "/api/fs/list":
			listCalls++
			var req struct {
				Path string `json:"path"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode list request: %v", err)
			}
			if req.Path != "/media" {
				t.Fatalf("unexpected list path: %s", req.Path)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code":    200,
				"message": "success",
				"data": map[string]any{
					"total": 1,
					"content": []map[string]any{
						{
							"name":     "demo.mp4",
							"is_dir":   false,
							"size":     123,
							"modified": time.Now().Format(time.RFC3339),
						},
					},
				},
			})
		case "/api/fs/get":
			getCalls++
			t.Fatalf("sync should not call /api/fs/get, got path %s", r.URL.Path)
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	tempDir := t.TempDir()
	store, err := index.Open(filepath.Join(tempDir, "index.db"))
	if err != nil {
		t.Fatalf("index.Open() error = %v", err)
	}
	defer store.Close()

	client, err := openlist.NewClient(config.OpenListConfig{
		BaseURL:        server.URL,
		Token:          "token",
		RequestTimeout: 5 * time.Second,
		Retry:          1,
		RetryBackoff:   time.Second,
		ListPerPage:    100,
	})
	if err != nil {
		t.Fatalf("openlist.NewClient() error = %v", err)
	}

	cfg := config.Config{
		OpenList: config.OpenListConfig{
			BaseURL:        server.URL,
			Token:          "token",
			RequestTimeout: 5 * time.Second,
			Retry:          1,
			RetryBackoff:   time.Second,
			ListPerPage:    100,
		},
		Redirect: config.RedirectConfig{
			PublicURL:        "http://127.0.0.1:18097",
			ListenAddr:       "127.0.0.1:18097",
			PlayTicketSecret: "test-secret",
			PlayTicketTTL:    12 * time.Hour,
		},
		Sync: config.SyncConfig{
			BaseDir:             filepath.Join(tempDir, "strm"),
			RuleFile:            filepath.Join(tempDir, "none.yml"),
			IndexDB:             filepath.Join(tempDir, "index.db"),
			ScanInterval:        time.Millisecond,
			FullRescanInterval:  time.Hour,
			MaxDirsPerCycle:     10,
			MaxRequestsPerCycle: 20,
			VideoExts: map[string]struct{}{
				".mp4": {},
			},
			CleanRemoved: true,
			Overwrite:    true,
			LogLevel:     "debug",
		},
		Rules: []config.Rule{
			{
				Name:       "media",
				SourcePath: "/media",
				TargetPath: filepath.Join(tempDir, "strm", "media"),
			},
		},
	}

	s := New(cfg, store, client)
	if err := s.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}

	if listCalls == 0 {
		t.Fatalf("expected list to be called")
	}
	if getCalls != 0 {
		t.Fatalf("expected get to never be called, got %d", getCalls)
	}

	content, err := os.ReadFile(filepath.Join(tempDir, "strm", "media", "demo.strm"))
	if err != nil {
		t.Fatalf("os.ReadFile() error = %v", err)
	}
	if strings.Contains(string(content), "?") {
		t.Fatalf("expected stable system URL without query, got %s", string(content))
	}
}

func TestRunOnceAdoptsExistingSTRMWhenIndexIsMissing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/api/fs/list" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}

		_ = json.NewEncoder(w).Encode(map[string]any{
			"code":    200,
			"message": "success",
			"data": map[string]any{
				"total": 1,
				"content": []map[string]any{
					{
						"name":     "demo.mp4",
						"is_dir":   false,
						"size":     123,
						"modified": time.Now().Format(time.RFC3339),
					},
				},
			},
		})
	}))
	defer server.Close()

	tempDir := t.TempDir()
	store, err := index.Open(filepath.Join(tempDir, "index.db"))
	if err != nil {
		t.Fatalf("index.Open() error = %v", err)
	}
	defer store.Close()

	client, err := openlist.NewClient(config.OpenListConfig{
		BaseURL:        server.URL,
		Token:          "token",
		RequestTimeout: 5 * time.Second,
		Retry:          1,
		RetryBackoff:   time.Second,
		ListPerPage:    100,
	})
	if err != nil {
		t.Fatalf("openlist.NewClient() error = %v", err)
	}

	targetDir := filepath.Join(tempDir, "strm", "media")
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatalf("os.MkdirAll() error = %v", err)
	}
	targetFile := filepath.Join(targetDir, "demo.strm")
	if err := os.WriteFile(targetFile, []byte("stale-content"), 0o644); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	cfg := config.Config{
		OpenList: config.OpenListConfig{
			BaseURL:        server.URL,
			Token:          "token",
			RequestTimeout: 5 * time.Second,
			Retry:          1,
			RetryBackoff:   time.Second,
			ListPerPage:    100,
		},
		Redirect: config.RedirectConfig{
			PublicURL:        "http://127.0.0.1:18097",
			ListenAddr:       "127.0.0.1:18097",
			PlayTicketSecret: "test-secret",
			PlayTicketTTL:    12 * time.Hour,
		},
		Sync: config.SyncConfig{
			BaseDir:             filepath.Join(tempDir, "strm"),
			RuleFile:            filepath.Join(tempDir, "none.yml"),
			IndexDB:             filepath.Join(tempDir, "index.db"),
			ScanInterval:        time.Millisecond,
			FullRescanInterval:  time.Hour,
			MaxDirsPerCycle:     10,
			MaxRequestsPerCycle: 20,
			VideoExts: map[string]struct{}{
				".mp4": {},
			},
			CleanRemoved: true,
			Overwrite:    true,
			LogLevel:     "debug",
		},
		Rules: []config.Rule{
			{
				Name:       "media",
				SourcePath: "/media",
				TargetPath: targetDir,
			},
		},
	}

	s := New(cfg, store, client)
	if err := s.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}

	content, err := os.ReadFile(targetFile)
	if err != nil {
		t.Fatalf("os.ReadFile() error = %v", err)
	}
	if got, want := string(content), "http://127.0.0.1:18097/strm/openlist/media/demo.mp4"; got != want {
		t.Fatalf("expected existing strm to be adopted and rewritten, got %s want %s", got, want)
	}

	files, err := store.ListFilesByRule("media")
	if err != nil {
		t.Fatalf("ListFilesByRule() error = %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 tracked file, got %d", len(files))
	}
}

func TestRunOnceSkipsUnchangedTrackedFileWhenOverwriteDisabled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/api/fs/list" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}

		var req struct {
			Path string `json:"path"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.Path != "/media" {
			t.Fatalf("unexpected list path: %s", req.Path)
		}

		_ = json.NewEncoder(w).Encode(map[string]any{
			"code":    200,
			"message": "success",
			"data": map[string]any{
				"total": 1,
				"content": []map[string]any{
					{
						"name":     "demo.mp4",
						"is_dir":   false,
						"size":     123,
						"modified": time.Now().Format(time.RFC3339),
					},
				},
			},
		})
	}))
	defer server.Close()

	tempDir := t.TempDir()
	store, err := index.Open(filepath.Join(tempDir, "index.db"))
	if err != nil {
		t.Fatalf("index.Open() error = %v", err)
	}
	defer store.Close()

	client, err := openlist.NewClient(config.OpenListConfig{
		BaseURL:        server.URL,
		Token:          "token",
		RequestTimeout: 5 * time.Second,
		Retry:          1,
		RetryBackoff:   time.Second,
		ListPerPage:    100,
	})
	if err != nil {
		t.Fatalf("openlist.NewClient() error = %v", err)
	}

	targetDir := filepath.Join(tempDir, "strm", "media")
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatalf("os.MkdirAll() error = %v", err)
	}
	targetFile := filepath.Join(targetDir, "demo.strm")
	expectedContent := "http://127.0.0.1:18097/strm/openlist/media/demo.mp4"
	if err := os.WriteFile(targetFile, []byte(expectedContent), 0o644); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	cfg := config.Config{
		OpenList: config.OpenListConfig{
			BaseURL:        server.URL,
			Token:          "token",
			RequestTimeout: 5 * time.Second,
			Retry:          1,
			RetryBackoff:   time.Second,
			ListPerPage:    100,
		},
		Redirect: config.RedirectConfig{
			PublicURL:        "http://127.0.0.1:18097",
			ListenAddr:       "127.0.0.1:18097",
			PlayTicketSecret: "test-secret",
			PlayTicketTTL:    12 * time.Hour,
		},
		Sync: config.SyncConfig{
			BaseDir:             filepath.Join(tempDir, "strm"),
			RuleFile:            filepath.Join(tempDir, "none.yml"),
			IndexDB:             filepath.Join(tempDir, "index.db"),
			ScanInterval:        time.Millisecond,
			FullRescanInterval:  time.Hour,
			MaxDirsPerCycle:     10,
			MaxRequestsPerCycle: 20,
			VideoExts: map[string]struct{}{
				".mp4": {},
			},
			CleanRemoved: true,
			Overwrite:    false,
			LogLevel:     "debug",
		},
		Rules: []config.Rule{
			{
				Name:       "media",
				SourcePath: "/media",
				TargetPath: targetDir,
			},
		},
	}

	s := New(cfg, store, client)
	if err := s.RunOnce(context.Background()); err != nil {
		t.Fatalf("first RunOnce() error = %v", err)
	}

	oldTime := time.Unix(100, 0)
	if err := os.Chtimes(targetFile, oldTime, oldTime); err != nil {
		t.Fatalf("os.Chtimes() error = %v", err)
	}
	time.Sleep(10 * time.Millisecond)

	if err := s.RunOnce(context.Background()); err != nil {
		t.Fatalf("second RunOnce() error = %v", err)
	}

	infoAfter, err := os.Stat(targetFile)
	if err != nil {
		t.Fatalf("os.Stat() error = %v", err)
	}
	if !infoAfter.ModTime().Equal(oldTime) {
		t.Fatalf("expected unchanged tracked file to not be rewritten when overwrite=false")
	}
}

func TestRunOnceForceRewritesTrackedFileWhenOverwriteEnabled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/api/fs/list" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}

		var req struct {
			Path string `json:"path"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.Path != "/media" {
			t.Fatalf("unexpected list path: %s", req.Path)
		}

		_ = json.NewEncoder(w).Encode(map[string]any{
			"code":    200,
			"message": "success",
			"data": map[string]any{
				"total": 1,
				"content": []map[string]any{
					{
						"name":     "demo.mp4",
						"is_dir":   false,
						"size":     123,
						"modified": time.Now().Format(time.RFC3339),
					},
				},
			},
		})
	}))
	defer server.Close()

	tempDir := t.TempDir()
	store, err := index.Open(filepath.Join(tempDir, "index.db"))
	if err != nil {
		t.Fatalf("index.Open() error = %v", err)
	}
	defer store.Close()

	client, err := openlist.NewClient(config.OpenListConfig{
		BaseURL:        server.URL,
		Token:          "token",
		RequestTimeout: 5 * time.Second,
		Retry:          1,
		RetryBackoff:   time.Second,
		ListPerPage:    100,
	})
	if err != nil {
		t.Fatalf("openlist.NewClient() error = %v", err)
	}

	targetDir := filepath.Join(tempDir, "strm", "media")
	cfg := config.Config{
		OpenList: config.OpenListConfig{
			BaseURL:        server.URL,
			Token:          "token",
			RequestTimeout: 5 * time.Second,
			Retry:          1,
			RetryBackoff:   time.Second,
			ListPerPage:    100,
		},
		Redirect: config.RedirectConfig{
			PublicURL:        "http://127.0.0.1:18097",
			ListenAddr:       "127.0.0.1:18097",
			PlayTicketSecret: "test-secret",
			PlayTicketTTL:    12 * time.Hour,
		},
		Sync: config.SyncConfig{
			BaseDir:             filepath.Join(tempDir, "strm"),
			RuleFile:            filepath.Join(tempDir, "none.yml"),
			IndexDB:             filepath.Join(tempDir, "index.db"),
			ScanInterval:        time.Millisecond,
			FullRescanInterval:  time.Hour,
			MaxDirsPerCycle:     10,
			MaxRequestsPerCycle: 20,
			VideoExts: map[string]struct{}{
				".mp4": {},
			},
			CleanRemoved: true,
			Overwrite:    true,
			LogLevel:     "debug",
		},
		Rules: []config.Rule{
			{
				Name:       "media",
				SourcePath: "/media",
				TargetPath: targetDir,
			},
		},
	}

	s := New(cfg, store, client)
	if err := s.RunOnce(context.Background()); err != nil {
		t.Fatalf("first RunOnce() error = %v", err)
	}

	targetFile := filepath.Join(targetDir, "demo.strm")
	oldTime := time.Unix(100, 0)
	if err := os.Chtimes(targetFile, oldTime, oldTime); err != nil {
		t.Fatalf("os.Chtimes() error = %v", err)
	}
	time.Sleep(10 * time.Millisecond)

	if err := s.RunOnce(context.Background()); err != nil {
		t.Fatalf("second RunOnce() error = %v", err)
	}

	infoAfter, err := os.Stat(targetFile)
	if err != nil {
		t.Fatalf("os.Stat() error = %v", err)
	}
	if !infoAfter.ModTime().After(oldTime) {
		t.Fatalf("expected tracked file to be force rewritten when overwrite=true")
	}
}
