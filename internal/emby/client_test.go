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
		switch r.URL.Path {
		case "/Sessions":
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
		case "/Users/user-id":
			if got := r.URL.Query().Get("api_key"); got != "token" {
				t.Fatalf("unexpected api_key: %s", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"Id":   "user-id",
				"Name": "monlor",
				"Policy": map[string]any{
					"IsAdministrator": false,
				},
			})
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
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

func TestGetUserInfoUsesCurrentUserWhenAvailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/Sessions":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"UserId": "admin-id", "UserName": "admin", "DeviceId": "device-1"},
			})
		case "/Users/admin-id":
			if got := r.URL.Query().Get("api_key"); got != "token" {
				t.Fatalf("unexpected api_key: %s", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"Id":   "admin-id",
				"Name": "admin",
				"Policy": map[string]any{
					"IsAdministrator": true,
				},
			})
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
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

	userInfo, err := client.GetUserInfo(context.Background(), "token", "")
	if err != nil {
		t.Fatalf("GetUserInfo() error = %v", err)
	}
	if !userInfo.IsAdmin {
		t.Fatalf("expected admin user")
	}
}

func TestListUsers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/Users" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("api_key"); got != "token" {
			t.Fatalf("unexpected api_key: %s", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{
				"Id":   "admin-id",
				"Name": "admin",
				"Policy": map[string]any{
					"IsAdministrator": true,
				},
			},
			{
				"Id":   "user-id",
				"Name": "alice",
				"Policy": map[string]any{
					"IsAdministrator": false,
				},
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

	users, err := client.ListUsers(context.Background(), "token")
	if err != nil {
		t.Fatalf("ListUsers() error = %v", err)
	}
	if len(users) != 2 {
		t.Fatalf("expected 2 users, got %d", len(users))
	}
	if users[0].ID != "admin-id" || !users[0].IsAdmin {
		t.Fatalf("unexpected first user: %+v", users[0])
	}
	if users[1].Name != "alice" || users[1].IsAdmin {
		t.Fatalf("unexpected second user: %+v", users[1])
	}
}
