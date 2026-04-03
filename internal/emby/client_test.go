package emby

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/monlor/emby-pro/internal/config"
)

func TestResolveRequestURIDoesNotDuplicateBasePath(t *testing.T) {
	client, err := NewClient(config.EmbyConfig{
		BaseURL:        "https://media.example.com/emby",
		ValidatePath:   "/System/Info",
		RequestTimeout: 5 * time.Second,
		TokenCacheTTL:  time.Minute,
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	resolved := client.ResolveRequestURI("/emby/Items/6/PlaybackInfo?api_key=token")
	if got, want := resolved.String(), "https://media.example.com/emby/Items/6/PlaybackInfo?api_key=token"; got != want {
		t.Fatalf("resolved url = %s, want %s", got, want)
	}
}

func TestGetUserInfoUsesSessionsAndDeviceID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/Sessions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("api_key"); got != "token" {
			t.Fatalf("unexpected api_key: %s", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{
				"UserId":   "admin-id",
				"UserName": "admin",
				"DeviceId": "admin-device",
			},
			{
				"UserId":   "user-id",
				"UserName": "monlor",
				"DeviceId": "web-device",
			},
		})
	}))
	defer server.Close()

	client, err := NewClient(config.EmbyConfig{
		BaseURL:        server.URL,
		ValidatePath:   "/System/Info",
		RequestTimeout: 5 * time.Second,
		TokenCacheTTL:  time.Minute,
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	userInfo, err := client.GetUserInfo(context.Background(), "token", "web-device")
	if err != nil {
		t.Fatalf("GetUserInfo() error = %v", err)
	}
	if got, want := userInfo.ID, "user-id"; got != want {
		t.Fatalf("user id = %s, want %s", got, want)
	}
	if got, want := userInfo.Name, "monlor"; got != want {
		t.Fatalf("user name = %s, want %s", got, want)
	}
}
