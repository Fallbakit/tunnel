package tunnel

import (
	"context"
	"crypto/tls"
	"errors"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"sync"
	"time"

	"github.com/hashicorp/yamux"
)

type AgentRunnerConfig struct {
	Endpoint        string
	OllamaBase      string
	LocalBaseURL    string
	LocalAPIKey     string
	TLSConfig       *tls.Config
	BootstrapClient *BootstrapClient
	Status          *AgentStatus
	MinBackoff      time.Duration
	MaxBackoff      time.Duration
	Logger          *slog.Logger
}

type Dialer func(context.Context, string, *tls.Config) (*yamux.Session, error)
type AuthDialer func(context.Context, string, *tls.Config, http.Header) (*yamux.Session, error)

type AgentStatus struct {
	mu          sync.RWMutex
	connected   bool
	lastError   string
	connectedAt time.Time
}

func (s *AgentStatus) SetConnected(connected bool, err error) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.connected = connected
	if connected {
		s.connectedAt = time.Now().UTC()
		s.lastError = ""
		return
	}
	if err != nil {
		s.lastError = err.Error()
	}
}

func (s *AgentStatus) Snapshot() (bool, time.Time, string) {
	if s == nil {
		return false, time.Time{}, ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.connected, s.connectedAt, s.lastError
}

func RunAgent(ctx context.Context, cfg AgentRunnerConfig) error {
	return RunAgentWithAuthDialer(ctx, cfg, DialWithHeaders)
}

func RunAgentWithDialer(ctx context.Context, cfg AgentRunnerConfig, dialer Dialer) error {
	if dialer == nil {
		return errors.New("dialer is required")
	}
	return RunAgentWithAuthDialer(ctx, cfg, func(ctx context.Context, endpoint string, tlsConfig *tls.Config, _ http.Header) (*yamux.Session, error) {
		return dialer(ctx, endpoint, tlsConfig)
	})
}

func RunAgentWithAuthDialer(ctx context.Context, cfg AgentRunnerConfig, dialer AuthDialer) error {
	if cfg.Endpoint == "" {
		if cfg.BootstrapClient == nil {
			return errors.New("endpoint or bootstrap configuration is required")
		}
	}
	localBaseURL := cfg.LocalBaseURL
	if localBaseURL == "" {
		localBaseURL = cfg.OllamaBase
	}
	if localBaseURL == "" {
		return errors.New("local runtime base URL is required")
	}
	if dialer == nil {
		return errors.New("dialer is required")
	}

	minBackoff := cfg.MinBackoff
	if minBackoff <= 0 {
		minBackoff = time.Second
	}
	maxBackoff := cfg.MaxBackoff
	if maxBackoff <= 0 {
		maxBackoff = 30 * time.Second
	}
	if maxBackoff < minBackoff {
		maxBackoff = minBackoff
	}

	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	backoff := minBackoff
	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		endpoint := cfg.Endpoint
		headers := http.Header{}
		if cfg.BootstrapClient != nil {
			bootstrap, err := cfg.BootstrapClient.Bootstrap(ctx)
			if err != nil {
				cfg.Status.SetConnected(false, err)
				logger.Warn("tunnel bootstrap failed, retrying", "error", err, "base_url", cfg.BootstrapClient.BaseURL)
				if sleepErr := sleepContext(ctx, jitter(backoff)); sleepErr != nil {
					return sleepErr
				}
				backoff = nextBackoff(backoff, maxBackoff)
				continue
			}
			endpoint = bootstrap.TunnelURL
			headers.Set("Authorization", "Bearer "+bootstrap.SessionToken)
		}

		session, err := dialer(ctx, endpoint, cfg.TLSConfig, headers)
		if err != nil {
			cfg.Status.SetConnected(false, err)
			logger.Warn("tunnel dial failed, retrying", "error", err, "endpoint", endpoint)
			if sleepErr := sleepContext(ctx, jitter(backoff)); sleepErr != nil {
				return sleepErr
			}
			backoff = nextBackoff(backoff, maxBackoff)
			continue
		}

		backoff = minBackoff
		cfg.Status.SetConnected(true, nil)
		logger.Info("tunnel connected", "endpoint", endpoint)
		agent, err := NewAgentWithLocalAPIKey(session, localBaseURL, cfg.LocalAPIKey)
		if err != nil {
			_ = session.Close()
			return err
		}

		err = agent.Serve(ctx)
		_ = session.Close()
		cfg.Status.SetConnected(false, err)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			logger.Warn("tunnel disconnected, reconnecting", "error", err)
			if sleepErr := sleepContext(ctx, jitter(backoff)); sleepErr != nil {
				return sleepErr
			}
			backoff = nextBackoff(backoff, maxBackoff)
		}
	}
}

func jitter(d time.Duration) time.Duration {
	if d <= 0 {
		return d
	}
	half := d / 2
	return half + time.Duration(rand.Int64N(int64(d-half)+1))
}

func nextBackoff(current, max time.Duration) time.Duration {
	next := current * 2
	if next > max {
		return max
	}
	return next
}

func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
