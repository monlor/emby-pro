package openlist

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/monlor/emby-pro/internal/config"
)

func TestDownloadURL(t *testing.T) {
	baseURL, err := url.Parse("http://openlist:5244/base")
	if err != nil {
		t.Fatalf("url.Parse() error = %v", err)
	}

	client := &Client{baseURL: baseURL, publicURL: baseURL}
	got := client.DownloadURL(Entry{Sign: "abc"}, "/movies/电影.mkv")
	want := "http://openlist:5244/base/d/movies/%E7%94%B5%E5%BD%B1.mkv?sign=abc"
	if got != want {
		t.Fatalf("DownloadURL() = %s, want %s", got, want)
	}
}

func TestDownloadURLUsesPublicURLWhenConfigured(t *testing.T) {
	baseURL, err := url.Parse("http://openlist:5244/base")
	if err != nil {
		t.Fatalf("url.Parse(baseURL) error = %v", err)
	}
	publicURL, err := url.Parse("https://list.example.com/share")
	if err != nil {
		t.Fatalf("url.Parse(publicURL) error = %v", err)
	}

	client := &Client{baseURL: baseURL, publicURL: publicURL}
	got := client.DownloadURL(Entry{Sign: "abc"}, "/movies/电影.mkv")
	want := "https://list.example.com/share/d/movies/%E7%94%B5%E5%BD%B1.mkv?sign=abc"
	if got != want {
		t.Fatalf("DownloadURL() = %s, want %s", got, want)
	}
}

func TestListPageAppliesRateLimit(t *testing.T) {
	var callTimes []time.Time
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callTimes = append(callTimes, time.Now())
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{
			"code":    200,
			"message": "success",
			"data": map[string]any{
				"total":   0,
				"content": []map[string]any{},
			},
		}); err != nil {
			t.Fatalf("encode response: %v", err)
		}
	}))
	defer server.Close()

	client, err := NewClient(config.OpenListConfig{
		BaseURL:        server.URL,
		Token:          "token",
		RequestTimeout: 2 * time.Second,
		Retry:          1,
		RetryBackoff:   time.Millisecond,
		ListPerPage:    200,
		RateLimitQPS:   5,
		RateLimitBurst: 1,
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	ctx := context.Background()
	if _, err := client.ListPage(ctx, "/media", 1, 200); err != nil {
		t.Fatalf("first ListPage() error = %v", err)
	}
	if _, err := client.ListPage(ctx, "/media", 2, 200); err != nil {
		t.Fatalf("second ListPage() error = %v", err)
	}

	if len(callTimes) != 2 {
		t.Fatalf("expected 2 calls, got %d", len(callTimes))
	}
	if delta := callTimes[1].Sub(callTimes[0]); delta < 150*time.Millisecond {
		t.Fatalf("expected rate limiter to delay second request, got %s", delta)
	}
}

func TestNewUnlimitedClientDisablesRateLimit(t *testing.T) {
	var callTimes []time.Time
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callTimes = append(callTimes, time.Now())
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{
			"code":    200,
			"message": "success",
			"data": map[string]any{
				"total":   0,
				"content": []map[string]any{},
			},
		}); err != nil {
			t.Fatalf("encode response: %v", err)
		}
	}))
	defer server.Close()

	client, err := NewUnlimitedClient(config.OpenListConfig{
		BaseURL:        server.URL,
		Token:          "token",
		RequestTimeout: 2 * time.Second,
		Retry:          1,
		RetryBackoff:   time.Millisecond,
		ListPerPage:    200,
		RateLimitQPS:   0.2,
		RateLimitBurst: 1,
	})
	if err != nil {
		t.Fatalf("NewUnlimitedClient() error = %v", err)
	}

	ctx := context.Background()
	if _, err := client.Get(ctx, "/media/demo.mp4"); err != nil {
		t.Fatalf("first Get() error = %v", err)
	}
	if _, err := client.Get(ctx, "/media/demo.mp4"); err != nil {
		t.Fatalf("second Get() error = %v", err)
	}

	if len(callTimes) != 2 {
		t.Fatalf("expected 2 calls, got %d", len(callTimes))
	}
	if delta := callTimes[1].Sub(callTimes[0]); delta > 100*time.Millisecond {
		t.Fatalf("expected unlimited client to avoid rate limiter delay, got %s", delta)
	}
}
