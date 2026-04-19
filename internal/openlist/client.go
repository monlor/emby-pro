package openlist

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/monlor/emby-pro/internal/config"
	"golang.org/x/time/rate"
)

type Client struct {
	baseURL      *url.URL
	publicURL    *url.URL
	httpClient   *http.Client
	username     string
	password     string
	retry        int
	retryBackoff time.Duration
	limiter      *rate.Limiter

	mu    sync.Mutex
	token string
}

type ListResult struct {
	Total   int     `json:"total"`
	Content []Entry `json:"content"`
}

type Entry struct {
	Name     string    `json:"name"`
	Size     int64     `json:"size"`
	IsDir    bool      `json:"is_dir"`
	Modified time.Time `json:"modified"`
	Sign     string    `json:"sign"`
	RawURL   string    `json:"raw_url"`
}

type apiEnvelope struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

type loginResponse struct {
	Token string `json:"token"`
}

func NewClient(cfg config.OpenListConfig) (*Client, error) {
	baseURL, err := url.Parse(cfg.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse base url: %w", err)
	}
	publicURL := baseURL
	if cfg.PublicURL != "" {
		publicURL, err = url.Parse(cfg.PublicURL)
		if err != nil {
			return nil, fmt.Errorf("parse public url: %w", err)
		}
	}
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: cfg.InsecureSkipVerify,
		},
		ForceAttemptHTTP2: !cfg.DisableHTTP2,
	}
	var limiter *rate.Limiter
	if cfg.RateLimitQPS > 0 {
		limiter = rate.NewLimiter(rate.Limit(cfg.RateLimitQPS), max(1, cfg.RateLimitBurst))
	}
	return &Client{
		baseURL:   baseURL,
		publicURL: publicURL,
		httpClient: &http.Client{
			Timeout:   cfg.RequestTimeout,
			Transport: transport,
		},
		username:     cfg.Username,
		password:     cfg.Password,
		retry:        max(1, cfg.Retry),
		retryBackoff: cfg.RetryBackoff,
		limiter:      limiter,
		token:        cfg.Token,
	}, nil
}

func (c *Client) ListPage(ctx context.Context, dirPath string, pageNum, perPage int) (ListResult, error) {
	payload := map[string]any{
		"path":     dirPath,
		"password": "",
		"refresh":  false,
		"page":     pageNum,
		"per_page": perPage,
	}

	var result ListResult
	if err := c.requestJSON(ctx, http.MethodPost, "/api/fs/list", payload, &result, true); err != nil {
		return ListResult{}, err
	}
	return result, nil
}

func (c *Client) Get(ctx context.Context, filePath string) (Entry, error) {
	payload := map[string]any{
		"path":     filePath,
		"password": "",
		"refresh":  false,
	}

	var result Entry
	if err := c.requestJSON(ctx, http.MethodPost, "/api/fs/get", payload, &result, true); err != nil {
		return Entry{}, err
	}
	return result, nil
}

func (c *Client) DownloadURL(entry Entry, fullPath string) string {
	u := *c.publicURL
	u.RawQuery = ""
	u.Fragment = ""

	segments := strings.Split(strings.TrimPrefix(fullPath, "/"), "/")
	unescapedPath := strings.TrimPrefix(fullPath, "/")
	for i, segment := range segments {
		segments[i] = url.PathEscape(segment)
	}

	basePath := strings.TrimSuffix(c.publicURL.Path, "/")
	if basePath == "" {
		basePath = "/"
	}
	downloadPath := path.Join(basePath, "/d")
	if len(segments) > 0 && segments[0] != "" {
		downloadPath = path.Join(downloadPath, unescapedPath)
	}
	u.Path = downloadPath
	if len(segments) > 0 && segments[0] != "" {
		u.RawPath = path.Join(strings.TrimSuffix(c.publicURL.EscapedPath(), "/"), "/d", strings.Join(segments, "/"))
	}

	if entry.Sign != "" {
		query := u.Query()
		query.Set("sign", entry.Sign)
		u.RawQuery = query.Encode()
	}

	return u.String()
}

func (c *Client) RawURL(entry Entry, fullPath string) (string, error) {
	if entry.RawURL != "" {
		return entry.RawURL, nil
	}
	return "", fmt.Errorf("empty raw url for %s", fullPath)
}

func (c *Client) Fingerprint() string {
	sum := sha256.Sum256([]byte(c.baseURL.String()))
	return hex.EncodeToString(sum[:8])
}

func (c *Client) requestJSON(ctx context.Context, method, uri string, payload any, out any, allowRelogin bool) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}

	var lastErr error
	for attempt := 0; attempt < c.retry; attempt++ {
		if attempt > 0 {
			timer := time.NewTimer(time.Duration(attempt) * c.retryBackoff)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}

		lastErr = c.doRequest(ctx, method, uri, body, out, allowRelogin)
		if lastErr == nil {
			return nil
		}
	}

	return lastErr
}

func (c *Client) doRequest(ctx context.Context, method, uri string, body []byte, out any, allowRelogin bool) error {
	if c.limiter != nil {
		if err := c.limiter.Wait(ctx); err != nil {
			return fmt.Errorf("wait rate limit: %w", err)
		}
	}
	if err := c.ensureToken(ctx); err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, method, c.resolveURL(uri), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json;charset=utf-8")
	if token := c.getToken(); token != "" {
		req.Header.Set("Authorization", token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request %s failed: %w", uri, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read %s response: %w", uri, err)
	}

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		if allowRelogin && c.canLogin() {
			if err := c.refreshLogin(ctx); err != nil {
				return err
			}
			return c.doRequest(ctx, method, uri, body, out, false)
		}
	}

	if resp.StatusCode >= 400 {
		return fmt.Errorf("%s returned %s: %s", uri, resp.Status, string(respBody))
	}

	var envelope apiEnvelope
	if err := json.Unmarshal(respBody, &envelope); err != nil {
		return fmt.Errorf("decode %s response: %w", uri, err)
	}
	if envelope.Code != http.StatusOK {
		if (envelope.Code == http.StatusUnauthorized || envelope.Code == http.StatusForbidden) && allowRelogin && c.canLogin() {
			if err := c.refreshLogin(ctx); err != nil {
				return err
			}
			return c.doRequest(ctx, method, uri, body, out, false)
		}
		return fmt.Errorf("%s failed: code=%d message=%s", uri, envelope.Code, envelope.Message)
	}
	if out == nil {
		return nil
	}
	if len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return nil
	}
	if err := json.Unmarshal(envelope.Data, out); err != nil {
		return fmt.Errorf("decode %s data: %w", uri, err)
	}
	return nil
}

func (c *Client) ensureToken(ctx context.Context) error {
	if c.getToken() != "" {
		return nil
	}
	if !c.canLogin() {
		return fmt.Errorf("openlist token is empty and username/password is not configured")
	}
	return c.login(ctx)
}

func (c *Client) login(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.token != "" {
		return nil
	}

	payload := map[string]any{
		"username": c.username,
		"password": c.password,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.resolveURL("/api/auth/login"), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build login request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json;charset=utf-8")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("login request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read login response: %w", err)
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("login failed: %s", resp.Status)
	}

	var envelope apiEnvelope
	if err := json.Unmarshal(respBody, &envelope); err != nil {
		return fmt.Errorf("decode login response: %w", err)
	}
	if envelope.Code != http.StatusOK {
		return fmt.Errorf("login failed: code=%d message=%s", envelope.Code, envelope.Message)
	}

	var result loginResponse
	if err := json.Unmarshal(envelope.Data, &result); err != nil {
		return fmt.Errorf("decode login token: %w", err)
	}
	if result.Token == "" {
		return fmt.Errorf("login succeeded but token is empty")
	}
	c.token = result.Token
	return nil
}

func (c *Client) refreshLogin(ctx context.Context) error {
	c.mu.Lock()
	c.token = ""
	c.mu.Unlock()
	return c.login(ctx)
}

func (c *Client) canLogin() bool {
	return c.username != "" && c.password != ""
}

func (c *Client) getToken() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.token
}

func (c *Client) resolveURL(uri string) string {
	u := *c.baseURL
	basePath := strings.TrimSuffix(c.baseURL.Path, "/")
	if basePath == "" {
		basePath = "/"
	}
	u.Path = path.Join(basePath, uri)
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}
