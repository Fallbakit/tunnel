package tunnel

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestTokenSignerRoundTripAndExpiry(t *testing.T) {
	signer, err := NewTokenSigner("test-secret")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC)
	signer.SetNowForTest(func() time.Time { return now })

	token, expiresAt, err := signer.Sign(SessionClaims{
		UserID:    "user_1",
		AccountID: "account_1",
		RunnerID:  "runner_1",
	}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if !expiresAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("expiresAt = %s, want %s", expiresAt, now.Add(time.Minute))
	}

	claims, err := signer.Verify(token)
	if err != nil {
		t.Fatal(err)
	}
	if claims.UserID != "user_1" || claims.AccountID != "account_1" || claims.RunnerID != "runner_1" {
		t.Fatalf("claims = %+v", claims)
	}

	signer.SetNowForTest(func() time.Time { return now.Add(2 * time.Minute) })
	if _, err := signer.Verify(token); err != ErrExpiredSessionToken {
		t.Fatalf("Verify expired error = %v, want %v", err, ErrExpiredSessionToken)
	}
}

func TestTokenSignerRejectsTampering(t *testing.T) {
	signer, err := NewTokenSigner("test-secret")
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := signer.Sign(SessionClaims{UserID: "user_1", AccountID: "account_1"}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	token += "x"
	if _, err := signer.Verify(token); err != ErrInvalidSessionToken {
		t.Fatalf("Verify tampered error = %v, want %v", err, ErrInvalidSessionToken)
	}
}

func TestDeriveTunnelURL(t *testing.T) {
	got, err := DeriveTunnelURL("https://api.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if got != "wss://api.example.com/tunnel" {
		t.Fatalf("DeriveTunnelURL = %q", got)
	}
}

func TestBootstrapClientPrefersExplicitTunnelURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/runners/bootstrap" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tunnel_url":"ws://localhost:8443/tunnel","session_token":"session-token"}`))
	}))
	defer server.Close()

	client := &BootstrapClient{
		BaseURL:   server.URL,
		APIKey:    "test-key",
		TunnelURL: "ws://host.docker.internal:8443/tunnel",
	}

	resp, err := client.Bootstrap(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if resp.TunnelURL != "ws://host.docker.internal:8443/tunnel" {
		t.Fatalf("TunnelURL = %q", resp.TunnelURL)
	}
}
