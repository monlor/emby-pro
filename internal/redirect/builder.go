package redirect

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/monlor/emby-pro/internal/config"
	"github.com/monlor/emby-pro/internal/pathutil"
)

const (
	openListProvider = "openlist"
	playTicketParam  = "t"
)

type playTicketClaims struct {
	Provider   string `json:"provider"`
	SourcePath string `json:"source_path"`
	ExpiresAt  int64  `json:"expires_at"`
}

type Builder struct {
	publicURL   *url.URL
	secret      []byte
	routePrefix string
}

func NewBuilder(cfg config.RedirectConfig) *Builder {
	if cfg.PublicURL == "" {
		return nil
	}
	u, err := url.Parse(cfg.PublicURL)
	if err != nil {
		return nil
	}
	return &Builder{
		publicURL:   u,
		secret:      []byte(cfg.PlayTicketSecret),
		routePrefix: defaultRoutePrefix(cfg.RoutePrefix),
	}
}

func (b *Builder) Build(sourcePath string) (string, error) {
	if b == nil {
		return "", fmt.Errorf("redirect builder is not configured")
	}
	sourcePath = pathutil.NormalizeSourcePath(sourcePath)
	if sourcePath == "" {
		return "", fmt.Errorf("source path is empty")
	}
	u := b.buildManagedURL(openListProvider, sourcePath)
	return u.String(), nil
}

func (b *Builder) BuildPlayTicket(sourcePath string, now time.Time, ttl time.Duration) (string, error) {
	if b == nil {
		return "", fmt.Errorf("redirect builder is not configured")
	}
	return b.buildPlayTicketForPublicURL(b.publicURL, sourcePath, now, ttl)
}

func (b *Builder) BuildRelativePlayTicket(sourcePath string, now time.Time, ttl time.Duration) (string, error) {
	if b == nil {
		return "", fmt.Errorf("redirect builder is not configured")
	}
	relativeBase := &url.URL{Path: "/"}
	return b.buildPlayTicketForPublicURL(relativeBase, sourcePath, now, ttl)
}

func (b *Builder) BuildPlayTicketForPublicURL(publicURL string, sourcePath string, now time.Time, ttl time.Duration) (string, error) {
	if b == nil {
		return "", fmt.Errorf("redirect builder is not configured")
	}
	override, err := url.Parse(strings.TrimSpace(publicURL))
	if err != nil {
		return "", fmt.Errorf("parse public url: %w", err)
	}
	return b.buildPlayTicketForPublicURL(override, sourcePath, now, ttl)
}

func (b *Builder) buildPlayTicketForPublicURL(publicURL *url.URL, sourcePath string, now time.Time, ttl time.Duration) (string, error) {
	if publicURL == nil {
		return "", fmt.Errorf("redirect public url is not configured")
	}
	if len(b.secret) == 0 {
		return "", fmt.Errorf("redirect secret is not configured")
	}
	if ttl <= 0 {
		return "", fmt.Errorf("play ticket ttl must be greater than zero")
	}

	sourcePath = pathutil.NormalizeSourcePath(sourcePath)
	if sourcePath == "" {
		return "", fmt.Errorf("source path is empty")
	}

	claims := playTicketClaims{
		Provider:   openListProvider,
		SourcePath: sourcePath,
		ExpiresAt:  now.Add(ttl).Unix(),
	}
	token, err := encodePlayTicket(b.secret, claims)
	if err != nil {
		return "", err
	}

	u := b.buildManagedURLForPublicURL(publicURL, openListProvider, sourcePath)
	query := u.Query()
	query.Set(playTicketParam, token)
	u.RawQuery = query.Encode()
	return u.String(), nil
}

func encodePlayTicket(secret []byte, claims playTicketClaims) (string, error) {
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("marshal play ticket: %w", err)
	}
	payloadEncoded := base64.RawURLEncoding.EncodeToString(payload)
	signature := sign(secret, payloadEncoded)
	return payloadEncoded + "." + signature, nil
}

func decodePlayTicket(secret []byte, token string, now time.Time) (playTicketClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return playTicketClaims{}, fmt.Errorf("invalid play ticket format")
	}
	payloadEncoded := parts[0]
	if !validSignature(secret, payloadEncoded, parts[1]) {
		return playTicketClaims{}, fmt.Errorf("invalid play ticket signature")
	}

	payload, err := base64.RawURLEncoding.DecodeString(payloadEncoded)
	if err != nil {
		return playTicketClaims{}, fmt.Errorf("decode play ticket payload: %w", err)
	}

	var claims playTicketClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return playTicketClaims{}, fmt.Errorf("decode play ticket claims: %w", err)
	}
	if claims.Provider == "" || claims.SourcePath == "" {
		return playTicketClaims{}, fmt.Errorf("invalid play ticket claims")
	}
	if now.Unix() > claims.ExpiresAt {
		return playTicketClaims{}, fmt.Errorf("play ticket expired")
	}
	return claims, nil
}

func (b *Builder) buildManagedURL(provider, sourcePath string) url.URL {
	return b.buildManagedURLForPublicURL(b.publicURL, provider, sourcePath)
}

func (b *Builder) buildManagedURLForPublicURL(publicURL *url.URL, provider, sourcePath string) url.URL {
	u := *publicURL
	basePath := strings.TrimSuffix(publicURL.Path, "/")
	if basePath == "" {
		basePath = "/"
	}

	segs := strings.Split(strings.TrimPrefix(sourcePath, "/"), "/")
	escapedSegs := make([]string, 0, len(segs))
	for _, seg := range segs {
		if seg == "" {
			continue
		}
		escapedSegs = append(escapedSegs, url.PathEscape(seg))
	}

	u.Path = path.Join(basePath, b.routePrefix, provider, sourcePath)
	rawSegments := append([]string{strings.TrimSuffix(publicURL.EscapedPath(), "/"), b.routePrefix, provider}, escapedSegs...)
	u.RawPath = path.Join(rawSegments...)
	u.RawQuery = ""
	return u
}

func sign(secret []byte, value string) string {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(value))
	return hex.EncodeToString(mac.Sum(nil))
}

func validSignature(secret []byte, value, actual string) bool {
	expected := sign(secret, value)
	expectedBytes, err1 := hex.DecodeString(expected)
	actualBytes, err2 := hex.DecodeString(actual)
	if err1 != nil || err2 != nil {
		return false
	}
	return hmac.Equal(expectedBytes, actualBytes)
}

func defaultRoutePrefix(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "/strm"
	}
	return raw
}
