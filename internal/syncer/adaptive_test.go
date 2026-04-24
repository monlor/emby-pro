package syncer

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/monlor/emby-pro/internal/config"
	"github.com/monlor/emby-pro/internal/index"
	"github.com/monlor/emby-pro/internal/openlist"
	"github.com/monlor/emby-pro/internal/redirect"
)

func newAdaptiveRunOnceConfig(tempDir, serverURL string) config.Config {
	syncCfg := testSyncConfig(tempDir)
	syncCfg.HotInterval = time.Second
	syncCfg.WarmInterval = 2 * time.Second
	syncCfg.ColdInterval = 4 * time.Second
	syncCfg.HotJitter = 0
	syncCfg.WarmJitter = 0
	syncCfg.ColdJitter = 0
	syncCfg.UnchangedToWarm = 1
	syncCfg.UnchangedToCold = 2
	syncCfg.RuleCooldown = 2 * time.Second

	return config.Config{
		OpenList: config.OpenListConfig{
			BaseURL:        serverURL,
			Token:          "token",
			RequestTimeout: 5 * time.Second,
			Retry:          1,
			RetryBackoff:   time.Millisecond,
			ListPerPage:    100,
		},
		Redirect: config.RedirectConfig{
			PublicURL:        "http://127.0.0.1:18097",
			ListenAddr:       "127.0.0.1:18097",
			PlayTicketSecret: "test-secret",
			PlayTicketTTL:    12 * time.Hour,
		},
		Sync: syncCfg,
		Rules: []config.Rule{
			{Name: "media", SourcePath: "/media", TargetPath: filepath.Join(tempDir, "strm", "media")},
		},
	}
}

func newAdaptiveTestStoreAndClient(t *testing.T, tempDir, serverURL string) (*index.Store, *openlist.Client) {
	t.Helper()

	store, err := index.Open(filepath.Join(tempDir, "index.db"))
	if err != nil {
		t.Fatalf("index.Open() error = %v", err)
	}

	client, err := openlist.NewClient(config.OpenListConfig{
		BaseURL:        serverURL,
		Token:          "token",
		RequestTimeout: 5 * time.Second,
		Retry:          1,
		RetryBackoff:   time.Millisecond,
		ListPerPage:    100,
	})
	if err != nil {
		store.Close()
		t.Fatalf("openlist.NewClient() error = %v", err)
	}

	return store, client
}

func newSequencedListServer(t *testing.T, sequences ...[]map[string]any) (*httptest.Server, func(int)) {
	t.Helper()

	var mu sync.Mutex
	current := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		entries := sequences[current]
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{
			"code":    200,
			"message": "success",
			"data": map[string]any{
				"total":   len(entries),
				"content": entries,
			},
		}); err != nil {
			t.Fatalf("encode response: %v", err)
		}
	}))

	setSequence := func(idx int) {
		mu.Lock()
		defer mu.Unlock()
		current = idx
	}
	return server, setSequence
}

func forceRootDirDueNow(t *testing.T, store *index.Store) {
	t.Helper()

	dir, err := store.GetDir("media", "/media")
	if err != nil {
		t.Fatalf("GetDir() error = %v", err)
	}
	now := time.Now()
	if err := store.MarkDirScannedSuccess("media", "/media", now, dir.LastRemoteMtime, dir.LastEntryCount, dir.LastResult, dir.UnchangedStreak, now); err != nil {
		t.Fatalf("MarkDirScannedSuccess() error = %v", err)
	}
}

func TestRunOnceAdaptiveScheduleConvergesForUnchangedDir(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
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
						"modified": time.Unix(1_700_000_000, 0).Format(time.RFC3339),
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
		RetryBackoff:   time.Millisecond,
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
			RetryBackoff:   time.Millisecond,
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
			FullRescanInterval:  time.Hour,
			MaxDirsPerCycle:     10,
			MaxRequestsPerCycle: 20,
			VideoExts:           map[string]struct{}{".mp4": {}},
			CleanRemoved:        true,
			Overwrite:           true,
			LogLevel:            "debug",
			HotInterval:         time.Second,
			WarmInterval:        2 * time.Second,
			ColdInterval:        4 * time.Second,
			HotJitter:           0,
			WarmJitter:          0,
			ColdJitter:          0,
			UnchangedToWarm:     1,
			UnchangedToCold:     2,
			FailureBackoffMax:   24 * time.Hour,
			RuleCooldown:        6 * time.Second,
		},
		Rules: []config.Rule{
			{Name: "media", SourcePath: "/media", TargetPath: filepath.Join(tempDir, "strm", "media")},
		},
	}

	s := New(cfg, store, client)
	if err := s.RunOnce(context.Background()); err != nil {
		t.Fatalf("first RunOnce() error = %v", err)
	}
	time.Sleep(1100 * time.Millisecond)
	if err := s.RunOnce(context.Background()); err != nil {
		t.Fatalf("second RunOnce() error = %v", err)
	}
	time.Sleep(2100 * time.Millisecond)
	if err := s.RunOnce(context.Background()); err != nil {
		t.Fatalf("third RunOnce() error = %v", err)
	}

	dir, err := store.GetDir("media", "/media")
	if err != nil {
		t.Fatalf("GetDir() error = %v", err)
	}
	if dir.UnchangedStreak != 2 {
		t.Fatalf("unchanged streak = %d, want 2", dir.UnchangedStreak)
	}
	if dir.LastResult != dirResultUnchanged {
		t.Fatalf("last result = %s, want %s", dir.LastResult, dirResultUnchanged)
	}
	if delta := dir.NextScanAt.Sub(dir.LastSuccessAt); delta != 4*time.Second {
		t.Fatalf("next scan delta = %s, want %s", delta, 4*time.Second)
	}
}

func TestRunOnceWindControlActivatesRuleCooldown(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "blocked", http.StatusTooManyRequests)
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
		RetryBackoff:   time.Millisecond,
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
			RetryBackoff:   time.Millisecond,
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
			FullRescanInterval:  time.Hour,
			MaxDirsPerCycle:     10,
			MaxRequestsPerCycle: 20,
			VideoExts:           map[string]struct{}{".mp4": {}},
			CleanRemoved:        true,
			Overwrite:           true,
			LogLevel:            "debug",
			HotInterval:         time.Second,
			WarmInterval:        2 * time.Second,
			ColdInterval:        4 * time.Second,
			HotJitter:           0,
			WarmJitter:          0,
			ColdJitter:          0,
			UnchangedToWarm:     1,
			UnchangedToCold:     2,
			FailureBackoffMax:   24 * time.Hour,
			RuleCooldown:        2 * time.Second,
		},
		Rules: []config.Rule{
			{Name: "media", SourcePath: "/media", TargetPath: filepath.Join(tempDir, "strm", "media")},
		},
	}

	s := New(cfg, store, client)
	if err := s.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}

	states, err := store.ListRuleStates()
	if err != nil {
		t.Fatalf("ListRuleStates() error = %v", err)
	}
	if len(states) != 1 || !states[0].CooldownUntil.After(time.Now()) {
		t.Fatalf("expected active rule cooldown, got %+v", states)
	}

	dirs, err := store.DueDirs(10, time.Now())
	if err != nil {
		t.Fatalf("DueDirs() error = %v", err)
	}
	if len(dirs) != 0 {
		t.Fatalf("expected cooldown to suppress due dirs, got %d", len(dirs))
	}
}

func TestRunOnce403OnlyBacksOffSingleDir(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
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
		RetryBackoff:   time.Millisecond,
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
			RetryBackoff:   time.Millisecond,
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
			FullRescanInterval:  time.Hour,
			MaxDirsPerCycle:     10,
			MaxRequestsPerCycle: 20,
			VideoExts:           map[string]struct{}{".mp4": {}},
			CleanRemoved:        true,
			Overwrite:           true,
			LogLevel:            "debug",
			HotInterval:         time.Second,
			WarmInterval:        2 * time.Second,
			ColdInterval:        4 * time.Second,
			HotJitter:           0,
			WarmJitter:          0,
			ColdJitter:          0,
			UnchangedToWarm:     1,
			UnchangedToCold:     2,
			FailureBackoffMax:   24 * time.Hour,
			RuleCooldown:        2 * time.Second,
		},
		Rules: []config.Rule{
			{Name: "media", SourcePath: "/media", TargetPath: filepath.Join(tempDir, "strm", "media")},
		},
	}

	s := New(cfg, store, client)
	if err := s.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}

	states, err := store.ListRuleStates()
	if err != nil {
		t.Fatalf("ListRuleStates() error = %v", err)
	}
	if len(states) != 1 || !states[0].CooldownUntil.IsZero() {
		t.Fatalf("expected no rule cooldown, got %+v", states)
	}

	dir, err := store.GetDir("media", "/media")
	if err != nil {
		t.Fatalf("GetDir() error = %v", err)
	}
	if !dir.NextScanAt.After(time.Now()) {
		t.Fatalf("expected directory backoff, got %s", dir.NextScanAt)
	}
}

func TestRunOnceRenameKeepsHotWhenTrackedOutputsChange(t *testing.T) {
	firstEntries := []map[string]any{
		{
			"name":     "a.mp4",
			"is_dir":   false,
			"size":     123,
			"modified": time.Unix(1_700_000_000, 0).Format(time.RFC3339),
		},
	}
	secondEntries := []map[string]any{
		{
			"name":     "b.mp4",
			"is_dir":   false,
			"size":     123,
			"modified": time.Unix(1_700_000_000, 0).Format(time.RFC3339),
		},
	}
	server, setSequence := newSequencedListServer(t, firstEntries, secondEntries)
	defer server.Close()

	tempDir := t.TempDir()
	store, client := newAdaptiveTestStoreAndClient(t, tempDir, server.URL)
	defer store.Close()

	s := New(newAdaptiveRunOnceConfig(tempDir, server.URL), store, client)
	if err := s.RunOnce(context.Background()); err != nil {
		t.Fatalf("first RunOnce() error = %v", err)
	}

	forceRootDirDueNow(t, store)
	setSequence(1)
	if err := s.RunOnce(context.Background()); err != nil {
		t.Fatalf("second RunOnce() error = %v", err)
	}

	dir, err := store.GetDir("media", "/media")
	if err != nil {
		t.Fatalf("GetDir() error = %v", err)
	}
	if dir.LastResult != dirResultChanged {
		t.Fatalf("last result = %s, want %s", dir.LastResult, dirResultChanged)
	}
	if dir.UnchangedStreak != 0 {
		t.Fatalf("unchanged streak = %d, want 0", dir.UnchangedStreak)
	}
	if delta := dir.NextScanAt.Sub(dir.LastSuccessAt); delta != time.Second {
		t.Fatalf("next scan delta = %s, want %s", delta, time.Second)
	}

	files, err := store.ListFilesByParent("media", "/media")
	if err != nil {
		t.Fatalf("ListFilesByParent() error = %v", err)
	}
	if len(files) != 1 || files[0].SourcePath != "/media/b.mp4" {
		t.Fatalf("tracked files = %+v, want only /media/b.mp4", files)
	}
}

func TestRunOncePathMappingChangeKeepsHotWhenSTRMContentChanges(t *testing.T) {
	entries := []map[string]any{
		{
			"name":     "demo.mp4",
			"is_dir":   false,
			"size":     123,
			"modified": time.Unix(1_700_000_000, 0).Format(time.RFC3339),
		},
	}
	server, _ := newSequencedListServer(t, entries)
	defer server.Close()

	tempDir := t.TempDir()
	store, client := newAdaptiveTestStoreAndClient(t, tempDir, server.URL)
	defer store.Close()

	s := New(newAdaptiveRunOnceConfig(tempDir, server.URL), store, client)
	if err := s.RunOnce(context.Background()); err != nil {
		t.Fatalf("first RunOnce() error = %v", err)
	}

	targetPath := filepath.Join(tempDir, "strm", "media", "demo.strm")
	before, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("ReadFile(before) error = %v", err)
	}

	forceRootDirDueNow(t, store)
	s.cfg.Redirect.PublicURL = "http://127.0.0.1:19097"
	s.redir = redirect.NewBuilder(s.cfg.Redirect)
	if err := s.RunOnce(context.Background()); err != nil {
		t.Fatalf("second RunOnce() error = %v", err)
	}

	dir, err := store.GetDir("media", "/media")
	if err != nil {
		t.Fatalf("GetDir() error = %v", err)
	}
	if dir.LastResult != dirResultChanged {
		t.Fatalf("last result = %s, want %s", dir.LastResult, dirResultChanged)
	}
	if dir.UnchangedStreak != 0 {
		t.Fatalf("unchanged streak = %d, want 0", dir.UnchangedStreak)
	}

	after, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("ReadFile(after) error = %v", err)
	}
	if string(before) == string(after) {
		t.Fatalf("expected .strm content to change after redirect public url update")
	}
}

func TestRunOnceIgnoresNoiseEntriesForAdaptiveScheduling(t *testing.T) {
	firstEntries := []map[string]any{
		{
			"name":     "demo.mp4",
			"is_dir":   false,
			"size":     123,
			"modified": time.Unix(1_700_000_000, 0).Format(time.RFC3339),
		},
	}
	secondEntries := []map[string]any{
		{
			"name":     "demo.mp4",
			"is_dir":   false,
			"size":     123,
			"modified": time.Unix(1_700_000_000, 0).Format(time.RFC3339),
		},
		{
			"name":     "cover.jpg",
			"is_dir":   false,
			"size":     456,
			"modified": time.Unix(1_700_000_100, 0).Format(time.RFC3339),
		},
	}
	server, setSequence := newSequencedListServer(t, firstEntries, secondEntries)
	defer server.Close()

	tempDir := t.TempDir()
	store, client := newAdaptiveTestStoreAndClient(t, tempDir, server.URL)
	defer store.Close()

	s := New(newAdaptiveRunOnceConfig(tempDir, server.URL), store, client)
	if err := s.RunOnce(context.Background()); err != nil {
		t.Fatalf("first RunOnce() error = %v", err)
	}

	forceRootDirDueNow(t, store)
	setSequence(1)
	if err := s.RunOnce(context.Background()); err != nil {
		t.Fatalf("second RunOnce() error = %v", err)
	}

	dir, err := store.GetDir("media", "/media")
	if err != nil {
		t.Fatalf("GetDir() error = %v", err)
	}
	if dir.LastResult != dirResultUnchanged {
		t.Fatalf("last result = %s, want %s", dir.LastResult, dirResultUnchanged)
	}
	if dir.UnchangedStreak != 1 {
		t.Fatalf("unchanged streak = %d, want 1", dir.UnchangedStreak)
	}
	if delta := dir.NextScanAt.Sub(dir.LastSuccessAt); delta != 2*time.Second {
		t.Fatalf("next scan delta = %s, want %s", delta, 2*time.Second)
	}
}

func TestNextWakeAtPrefersNearestEligibleDirectory(t *testing.T) {
	tempDir := t.TempDir()
	store, err := index.Open(filepath.Join(tempDir, "index.db"))
	if err != nil {
		t.Fatalf("index.Open() error = %v", err)
	}
	defer store.Close()

	now := time.Now()
	rule := config.Rule{Name: "media", SourcePath: "/media", TargetPath: filepath.Join(tempDir, "strm", "media")}
	if err := store.EnsureRule(rule, now); err != nil {
		t.Fatalf("EnsureRule() error = %v", err)
	}
	if err := store.ScheduleFullRescan(rule.Name, now); err != nil {
		t.Fatalf("ScheduleFullRescan() error = %v", err)
	}
	nextDirAt := now.Add(5 * time.Second)
	if err := store.MarkDirScannedSuccess(rule.Name, rule.SourcePath, nextDirAt, now, 0, dirResultChanged, 0, now); err != nil {
		t.Fatalf("MarkDirScannedSuccess() error = %v", err)
	}

	s := New(config.Config{
		Sync: config.SyncConfig{
			FullRescanInterval: time.Hour,
		},
	}, store, nil)

	got, err := s.nextWakeAt(now)
	if err != nil {
		t.Fatalf("nextWakeAt() error = %v", err)
	}
	want := nextDirAt.Truncate(time.Second)
	if !got.Equal(want) {
		t.Fatalf("nextWakeAt() = %s, want %s", got, want)
	}
}
