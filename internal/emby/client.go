package emby

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/monlor/emby-pro/internal/config"
	"github.com/monlor/emby-pro/internal/httpx"
)

type Client struct {
	baseURL      *url.URL
	validatePath string
	httpClient   *http.Client
}

func NewClient(cfg config.EmbyConfig) (*Client, error) {
	baseURL, err := url.Parse(cfg.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse emby base url: %w", err)
	}
	return &Client{
		baseURL:      baseURL,
		validatePath: cfg.ValidatePath,
		httpClient: &http.Client{
			Timeout: cfg.RequestTimeout,
		},
	}, nil
}

type UserInfo struct {
	ID   string
	Name string
}

type SessionInfo struct {
	UserID   string `json:"UserId"`
	UserName string `json:"UserName"`
	DeviceID string `json:"DeviceId"`
}

func (c *Client) GetUserInfo(ctx context.Context, token, deviceID string) (*UserInfo, error) {
	sessionsURL := c.resolveURL("/Sessions")
	q := sessionsURL.Query()
	q.Set("api_key", token)
	sessionsURL.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sessionsURL.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build emby user info request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("emby user info request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("emby user info failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var sessions []SessionInfo
	if err := json.NewDecoder(resp.Body).Decode(&sessions); err != nil {
		return nil, fmt.Errorf("decode emby sessions: %w", err)
	}

	deviceID = strings.TrimSpace(deviceID)
	if deviceID != "" {
		for _, session := range sessions {
			if session.DeviceID == deviceID && session.UserID != "" && session.UserName != "" {
				return &UserInfo{ID: session.UserID, Name: session.UserName}, nil
			}
		}
	}

	users := make(map[string]UserInfo)
	for _, session := range sessions {
		if session.UserID == "" || session.UserName == "" {
			continue
		}
		users[session.UserID] = UserInfo{ID: session.UserID, Name: session.UserName}
	}
	if len(users) == 1 {
		for _, user := range users {
			return &user, nil
		}
	}
	if len(users) == 0 {
		return nil, fmt.Errorf("emby user info failed: no user session found")
	}
	return nil, fmt.Errorf("emby user info failed: multiple user sessions visible; device id required")
}

func (c *Client) ValidateToken(ctx context.Context, token string) error {
	if strings.TrimSpace(token) == "" {
		return fmt.Errorf("empty emby token")
	}

	validateURL := c.resolveURL(c.validatePath)
	query := validateURL.Query()
	query.Set("api_key", token)
	validateURL.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, validateURL.String(), nil)
	if err != nil {
		return fmt.Errorf("build emby validation request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("emby validation request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		return nil
	}

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	return fmt.Errorf("emby validation failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
}

func (c *Client) BaseURL() *url.URL {
	u := *c.baseURL
	return &u
}

func (c *Client) RawRequest(ctx context.Context, method, requestURI string, headers http.Header, body []byte) (*http.Response, []byte, error) {
	targetURL := c.ResolveRequestURI(requestURI)
	req, err := http.NewRequestWithContext(ctx, method, targetURL.String(), bytes.NewReader(body))
	if err != nil {
		return nil, nil, fmt.Errorf("build emby request: %w", err)
	}

	httpx.CopyHeaders(req.Header, headers)
	req.Header.Del("Accept-Encoding")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("emby request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("read emby response: %w", err)
	}

	respCopy := new(http.Response)
	*respCopy = *resp
	respCopy.Header = resp.Header.Clone()
	respCopy.Body = io.NopCloser(bytes.NewReader(respBody))
	return respCopy, respBody, nil
}

func (c *Client) FetchPlaybackInfo(ctx context.Context, requestURI, token string) (map[string]any, error) {
	resp, body, err := c.RawRequest(ctx, http.MethodGet, requestURI, http.Header{
		"X-Emby-Token": []string{token},
	}, nil)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("playback info request failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode playback info: %w", err)
	}
	return payload, nil
}

func (c *Client) ResolveRequestURI(requestURI string) *url.URL {
	return c.resolveRequestURI(requestURI)
}

func (c *Client) resolveURL(uri string) *url.URL {
	u := *c.baseURL
	basePath := strings.TrimSuffix(c.baseURL.Path, "/")
	if basePath == "" {
		basePath = "/"
	}
	u.Path = path.Join(basePath, uri)
	u.RawQuery = ""
	u.Fragment = ""
	return &u
}

func (c *Client) resolveRequestURI(requestURI string) *url.URL {
	u := *c.baseURL
	basePath := strings.TrimSuffix(c.baseURL.Path, "/")
	if basePath == "" {
		basePath = "/"
	}

	if parsed, err := url.Parse(requestURI); err == nil {
		reqPath := parsed.Path
		if basePath != "/" && strings.HasPrefix(reqPath, basePath+"/") {
			u.Path = reqPath
		} else if reqPath == basePath {
			u.Path = reqPath
		} else {
			u.Path = path.Join(basePath, reqPath)
		}
		u.RawPath = ""
		u.RawQuery = parsed.RawQuery
		u.Fragment = parsed.Fragment
		return &u
	}

	u.Path = path.Join(basePath, requestURI)
	u.RawQuery = ""
	u.Fragment = ""
	return &u
}
