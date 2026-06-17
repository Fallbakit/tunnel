package tunnel

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	DefaultBootstrapTokenTTL = 15 * time.Minute
	defaultTokenIssuer       = "fallbakit"
)

var (
	ErrBootstrapUnavailable = errors.New("tunnel bootstrap is not configured")
	ErrInvalidSessionToken  = errors.New("invalid tunnel session token")
	ErrExpiredSessionToken  = errors.New("expired tunnel session token")
)

type BootstrapRequest struct {
	RunnerID      string            `json:"runner_id"`
	AgentID       string            `json:"agent_id"`
	AgentVersion  string            `json:"agent_version"`
	Hostname      string            `json:"hostname"`
	LocalProvider string            `json:"local_provider,omitempty"`
	LocalBaseURL  string            `json:"local_base_url,omitempty"`
	OllamaURL     string            `json:"ollama_url"`
	Labels        map[string]string `json:"labels,omitempty"`
}

type BootstrapResponse struct {
	TunnelURL    string    `json:"tunnel_url"`
	SessionToken string    `json:"session_token"`
	ExpiresAt    time.Time `json:"expires_at"`
	AccountID    string    `json:"account_id"`
	RunnerID     string    `json:"runner_id"`
	RunnerLimit  int       `json:"runner_limit"`
}

type SessionClaims struct {
	Issuer       string `json:"iss"`
	UserID       string `json:"user_id"`
	AccountID    string `json:"account_id"`
	RunnerKeyID  string `json:"runner_key_id,omitempty"`
	RunnerID     string `json:"runner_id"`
	AgentID      string `json:"agent_id,omitempty"`
	AgentVersion string `json:"agent_version,omitempty"`
	ExpiresAt    int64  `json:"exp"`
	IssuedAt     int64  `json:"iat"`
}

func (c SessionClaims) Identity() Identity {
	return Identity{UserID: c.UserID, AccountID: c.AccountID, RunnerID: c.RunnerID}
}

type TokenSigner struct {
	secret []byte
	issuer string
	now    func() time.Time
}

func NewTokenSigner(secret string) (*TokenSigner, error) {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return nil, ErrBootstrapUnavailable
	}
	return &TokenSigner{
		secret: []byte(secret),
		issuer: defaultTokenIssuer,
		now:    time.Now,
	}, nil
}

func (s *TokenSigner) SetNowForTest(now func() time.Time) {
	if now == nil {
		now = time.Now
	}
	s.now = now
}

func (s *TokenSigner) Sign(claims SessionClaims, ttl time.Duration) (string, time.Time, error) {
	if s == nil || len(s.secret) == 0 {
		return "", time.Time{}, ErrBootstrapUnavailable
	}
	if !claims.Identity().Valid() {
		return "", time.Time{}, ErrInvalidIdentity
	}
	if ttl <= 0 {
		ttl = DefaultBootstrapTokenTTL
	}
	now := s.now().UTC()
	claims.Issuer = s.issuer
	claims.IssuedAt = now.Unix()
	claims.ExpiresAt = now.Add(ttl).Unix()
	if claims.RunnerID == "" {
		claims.RunnerID = defaultRunnerID
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", time.Time{}, err
	}
	payloadPart := base64.RawURLEncoding.EncodeToString(payload)
	sig := s.sign(payloadPart)
	return payloadPart + "." + sig, time.Unix(claims.ExpiresAt, 0).UTC(), nil
}

func (s *TokenSigner) Verify(token string) (SessionClaims, error) {
	if s == nil || len(s.secret) == 0 {
		return SessionClaims{}, ErrBootstrapUnavailable
	}
	payloadPart, sigPart, ok := strings.Cut(strings.TrimSpace(token), ".")
	if !ok || payloadPart == "" || sigPart == "" {
		return SessionClaims{}, ErrInvalidSessionToken
	}
	expected := s.sign(payloadPart)
	if subtle.ConstantTimeCompare([]byte(expected), []byte(sigPart)) != 1 {
		return SessionClaims{}, ErrInvalidSessionToken
	}
	payload, err := base64.RawURLEncoding.DecodeString(payloadPart)
	if err != nil {
		return SessionClaims{}, ErrInvalidSessionToken
	}
	var claims SessionClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return SessionClaims{}, ErrInvalidSessionToken
	}
	if claims.Issuer != s.issuer || !claims.Identity().Valid() {
		return SessionClaims{}, ErrInvalidSessionToken
	}
	if claims.ExpiresAt <= s.now().UTC().Unix() {
		return SessionClaims{}, ErrExpiredSessionToken
	}
	if claims.RunnerID == "" {
		claims.RunnerID = defaultRunnerID
	}
	return claims, nil
}

func (s *TokenSigner) sign(payloadPart string) string {
	mac := hmac.New(sha256.New, s.secret)
	_, _ = mac.Write([]byte(payloadPart))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

type BootstrapRunner struct {
	UserID      string
	AccountID   string
	RunnerID    string
	RunnerKeyID string
}

type BootstrapService struct {
	Signer    *TokenSigner
	TunnelURL string
	TTL       time.Duration
}

func (s *BootstrapService) Issue(runner BootstrapRunner, req BootstrapRequest, runnerLimit int) (BootstrapResponse, error) {
	if s == nil || s.Signer == nil {
		return BootstrapResponse{}, ErrBootstrapUnavailable
	}
	runnerID := strings.TrimSpace(runner.RunnerID)
	if runnerID == "" {
		runnerID = strings.TrimSpace(req.RunnerID)
	}
	if runnerID == "" {
		runnerID = defaultRunnerID
	}
	identity := Identity{UserID: runner.UserID, AccountID: runner.AccountID, RunnerID: runnerID}
	if !identity.Valid() {
		return BootstrapResponse{}, ErrInvalidIdentity
	}
	claims := SessionClaims{
		UserID:       runner.UserID,
		AccountID:    runner.AccountID,
		RunnerKeyID:  runner.RunnerKeyID,
		RunnerID:     runnerID,
		AgentID:      strings.TrimSpace(req.AgentID),
		AgentVersion: strings.TrimSpace(req.AgentVersion),
	}
	token, expiresAt, err := s.Signer.Sign(claims, s.TTL)
	if err != nil {
		return BootstrapResponse{}, err
	}
	return BootstrapResponse{
		TunnelURL:    strings.TrimSpace(s.TunnelURL),
		SessionToken: token,
		ExpiresAt:    expiresAt,
		AccountID:    runner.AccountID,
		RunnerID:     runnerID,
		RunnerLimit:  runnerLimit,
	}, nil
}

type BootstrapClient struct {
	BaseURL       string
	APIKey        string
	TunnelURL     string
	RunnerID      string
	AgentID       string
	AgentVersion  string
	Hostname      string
	LocalProvider string
	LocalBaseURL  string
	OllamaURL     string
	Labels        map[string]string
	HTTPClient    *http.Client
}

func (c *BootstrapClient) Bootstrap(ctx context.Context) (BootstrapResponse, error) {
	if c == nil {
		return BootstrapResponse{}, ErrBootstrapUnavailable
	}
	if strings.TrimSpace(c.APIKey) == "" {
		return BootstrapResponse{}, errors.New("api key is required for tunnel bootstrap")
	}
	baseURL := strings.TrimSpace(c.BaseURL)
	if baseURL == "" {
		return BootstrapResponse{}, errors.New("base URL is required for tunnel bootstrap")
	}
	endpoint, err := url.JoinPath(baseURL, "/v1/runners/bootstrap")
	if err != nil {
		return BootstrapResponse{}, err
	}
	hostname := strings.TrimSpace(c.Hostname)
	if hostname == "" {
		hostname, _ = os.Hostname()
	}
	payload := BootstrapRequest{
		RunnerID:      c.RunnerID,
		AgentID:       c.AgentID,
		AgentVersion:  c.AgentVersion,
		Hostname:      hostname,
		LocalProvider: c.LocalProvider,
		LocalBaseURL:  c.LocalBaseURL,
		OllamaURL:     c.OllamaURL,
		Labels:        c.Labels,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return BootstrapResponse{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(body)))
	if err != nil {
		return BootstrapResponse{}, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
	httpReq.Header.Set("Content-Type", "application/json")
	client := c.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return BootstrapResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return BootstrapResponse{}, fmt.Errorf("tunnel bootstrap failed with status %d", resp.StatusCode)
	}
	var out BootstrapResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return BootstrapResponse{}, err
	}
	if out.SessionToken == "" {
		return BootstrapResponse{}, errors.New("tunnel bootstrap response missing session token")
	}
	explicitTunnelURL := strings.TrimSpace(c.TunnelURL)
	if explicitTunnelURL != "" {
		out.TunnelURL = explicitTunnelURL
	}
	if out.TunnelURL == "" {
		out.TunnelURL, err = DeriveTunnelURL(baseURL)
		if err != nil {
			return BootstrapResponse{}, err
		}
	}
	return out, nil
}

func DeriveTunnelURL(baseURL string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return "", err
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", errors.New("base URL must include scheme and host")
	}
	switch parsed.Scheme {
	case "https":
		parsed.Scheme = "wss"
	case "http":
		parsed.Scheme = "ws"
	default:
		return "", fmt.Errorf("unsupported base URL scheme %q", parsed.Scheme)
	}
	parsed.Path = "/tunnel"
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}
