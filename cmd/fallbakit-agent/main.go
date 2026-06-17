package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/fallbakit/fallbakit-agent/internal/observability"
	"github.com/fallbakit/fallbakit-agent/internal/tunnel"
	"github.com/fallbakit/fallbakit-agent/internal/update"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.yaml.in/yaml/v2"
)

// agentVersion is overridden at release time via -ldflags "-X main.agentVersion=...".
var agentVersion = "dev"

type agentConfig struct {
	APIKey          string        `yaml:"api_key"`
	BaseURL         string        `yaml:"base_url"`
	TunnelURL       string        `yaml:"tunnel_url"`
	LocalProvider   string        `yaml:"local_provider"`
	LocalBaseURL    string        `yaml:"local_base_url"`
	LocalAPIKey     string        `yaml:"local_api_key"`
	OllamaURL       string        `yaml:"ollama_url"`
	RunnerID        string        `yaml:"runner_id"`
	AgentID         string        `yaml:"agent_id"`
	LogFormat       string        `yaml:"log_format"`
	ServiceName     string        `yaml:"service_name"`
	OTELEndpoint    string        `yaml:"otel_endpoint"`
	MetricsAddr     string        `yaml:"metrics_addr"`
	MinBackoff      time.Duration `yaml:"min_backoff"`
	MaxBackoff      time.Duration `yaml:"max_backoff"`
	ConnectTimeout  time.Duration `yaml:"connect_timeout"`
	LocalTimeout    time.Duration `yaml:"local_timeout"`
	OllamaTimeout   time.Duration `yaml:"ollama_timeout"`
	UpdateURL       string        `yaml:"update_url"`
	UpdatePublicKey string        `yaml:"update_public_key"`
	UpdateCheck     bool          `yaml:"update_check"`
}

func main() {
	configPath := findConfigPath(os.Args[1:])
	fileConfig, err := loadAgentConfig(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config: %v\n", err)
		os.Exit(2)
	}
	defaultAgentID, _ := os.Hostname()
	if defaultAgentID == "" {
		defaultAgentID = "fallbakit-agent"
	}

	configFlag := flag.String("config", configPath, "optional YAML config file")
	apiKey := flag.String("api-key", envOrDefault("FALLBAKIT_RUNNER_API_KEY", defaultString(fileConfig.APIKey, os.Getenv("FALLBAKIT_API_KEY"))), "runner API key for hosted tunnel bootstrap")
	baseURL := flag.String("base-url", envOrDefault("FALLBAKIT_BASE_URL", defaultString(fileConfig.BaseURL, "http://localhost:8080")), "Fallbakit platform base URL")
	tunnelURL := flag.String("tunnel-url", envOrDefault("FALLBAKIT_TUNNEL_URL", fileConfig.TunnelURL), "optional explicit WebSocket tunnel URL")
	localProvider := flag.String("local-provider", envOrDefault("FALLBAKIT_LOCAL_PROVIDER", defaultString(fileConfig.LocalProvider, localProviderOllama)), "local runtime provider: ollama, omlx, or vllm")
	localBaseURL := flag.String("local-base-url", envOrDefault("FALLBAKIT_LOCAL_BASE_URL", fileConfig.LocalBaseURL), "local runtime base URL")
	localAPIKey := flag.String("local-api-key", envOrDefault("FALLBAKIT_LOCAL_API_KEY", fileConfig.LocalAPIKey), "optional local runtime API key forwarded only to the local runtime")
	ollama := flag.String("ollama", envOrDefault("FALLBAKIT_OLLAMA_URL", fileConfig.OllamaURL), "deprecated alias for the local Ollama base URL")
	runnerID := flag.String("runner-id", envOrDefault("FALLBAKIT_RUNNER_ID", fileConfig.RunnerID), "dashboard-generated runner ID for this local runtime connection")
	agentID := flag.String("agent-id", envOrDefault("FALLBAKIT_AGENT_ID", defaultString(fileConfig.AgentID, defaultAgentID)), "stable agent ID shown in tunnel metadata")
	logFormat := flag.String("log-format", envOrDefault("FALLBAKIT_LOG_FORMAT", defaultString(fileConfig.LogFormat, observability.FormatText)), "log format: json or text")
	serviceName := flag.String("service-name", envOrDefault("FALLBAKIT_SERVICE_NAME", defaultString(fileConfig.ServiceName, "fallbakit-agent")), "service name for logs, metrics, and traces")
	otelEndpoint := flag.String("otel-endpoint", envOrDefault("FALLBAKIT_OTEL_ENDPOINT", fileConfig.OTELEndpoint), "optional OTLP/HTTP trace endpoint host:port")
	metricsAddr := flag.String("metrics-addr", envOrDefault("FALLBAKIT_METRICS_ADDR", fileConfig.MetricsAddr), "optional HTTP listen address for agent metrics")
	minBackoff := flag.Duration("min-backoff", envDurationOrDefault("FALLBAKIT_MIN_BACKOFF", defaultDuration(fileConfig.MinBackoff, time.Second)), "minimum reconnect backoff")
	maxBackoff := flag.Duration("max-backoff", envDurationOrDefault("FALLBAKIT_MAX_BACKOFF", defaultDuration(fileConfig.MaxBackoff, 30*time.Second)), "maximum reconnect backoff")
	connectTimeout := flag.Duration("connect-timeout", envDurationOrDefault("FALLBAKIT_CONNECT_TIMEOUT", defaultDuration(fileConfig.ConnectTimeout, 10*time.Second)), "bootstrap HTTP timeout")
	localTimeout := flag.Duration("local-timeout", envDurationOrDefault("FALLBAKIT_LOCAL_TIMEOUT", envDurationOrDefault("FALLBAKIT_OLLAMA_TIMEOUT", defaultDuration(fileConfig.LocalTimeout, defaultDuration(fileConfig.OllamaTimeout, 3*time.Second)))), "local runtime readiness probe timeout")
	ollamaTimeout := flag.Duration("ollama-timeout", 0, "deprecated alias for -local-timeout")
	updateURL := flag.String("update-url", envOrDefault("FALLBAKIT_AGENT_UPDATE_URL", fileConfig.UpdateURL), "optional signed agent update manifest URL")
	updatePublicKey := flag.String("update-public-key", envOrDefault("FALLBAKIT_AGENT_UPDATE_PUBLIC_KEY", fileConfig.UpdatePublicKey), "base64 or hex Ed25519 update public key")
	updateCheck := flag.Bool("update-check", envBoolOrDefault("FALLBAKIT_AGENT_UPDATE_CHECK", fileConfig.UpdateCheck), "check, verify, and atomically stage a signed agent update before connecting")
	flag.Parse()
	_ = configFlag

	resolvedLocalProvider, err := resolveLocalProvider(*localProvider)
	if err != nil {
		fmt.Fprintf(os.Stderr, "resolve local provider: %v\n", err)
		os.Exit(2)
	}
	resolvedLocalBaseURL, err := resolveLocalBaseURL(resolvedLocalProvider, *localBaseURL, *ollama)
	if err != nil {
		fmt.Fprintf(os.Stderr, "resolve local base URL: %v\n", err)
		os.Exit(2)
	}
	resolvedLocalTimeout := *localTimeout
	if *ollamaTimeout > 0 && !flagWasSet("local-timeout") {
		resolvedLocalTimeout = *ollamaTimeout
	}

	logger := observability.ConfigureDefaultLogger(*serviceName, *logFormat)
	traceProvider, err := observability.InitTracing(context.Background(), observability.TraceConfig{ServiceName: *serviceName, Endpoint: *otelEndpoint})
	if err != nil {
		logger.Error("initialize tracing", "error", err)
		os.Exit(1)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := traceProvider.Shutdown(shutdownCtx); err != nil {
			logger.Warn("shutdown tracing", "error", err)
		}
	}()

	if _, err := url.ParseRequestURI(resolvedLocalBaseURL); err != nil {
		logger.Error("invalid local base URL", "error", err, "local_provider", resolvedLocalProvider, "local_base_url", resolvedLocalBaseURL)
		os.Exit(2)
	}

	if *apiKey == "" {
		flag.Usage()
		logger.Error("runner api-key is required for bootstrap")
		os.Exit(2)
	}
	if *runnerID == "" {
		flag.Usage()
		logger.Error("runner id is required for bootstrap")
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	status := &tunnel.AgentStatus{}
	if *metricsAddr != "" {
		go serveMetrics(ctx, *metricsAddr, resolvedLocalProvider, resolvedLocalBaseURL, resolvedLocalTimeout, status, logger)
	}

	if *updateCheck {
		if err := checkAndStageUpdate(ctx, *updateURL, *updatePublicKey); err != nil {
			logger.Warn("agent update check failed", "error", err)
		} else {
			logger.Info("agent update check completed")
		}
	}

	bootstrapClient := &tunnel.BootstrapClient{
		BaseURL:       *baseURL,
		APIKey:        *apiKey,
		TunnelURL:     *tunnelURL,
		RunnerID:      *runnerID,
		AgentID:       *agentID,
		AgentVersion:  agentVersion,
		LocalProvider: resolvedLocalProvider,
		LocalBaseURL:  resolvedLocalBaseURL,
		OllamaURL:     bootstrapOllamaURL(resolvedLocalProvider, resolvedLocalBaseURL),
		Labels: map[string]string{
			"local_provider": resolvedLocalProvider,
			"local_base_url": resolvedLocalBaseURL,
		},
		HTTPClient: &http.Client{Timeout: *connectTimeout},
	}

	logger.Info("fallbakit agent starting", "mode", "runner_bootstrap", "base_url", *baseURL, "tunnel_url_configured", *tunnelURL != "", "local_provider", resolvedLocalProvider, "local_base_url", resolvedLocalBaseURL, "runner_id", *runnerID, "agent_id", *agentID, "tracing_enabled", traceProvider.Enabled())
	err = tunnel.RunAgent(ctx, tunnel.AgentRunnerConfig{
		Endpoint:        *tunnelURL,
		OllamaBase:      resolvedLocalBaseURL,
		LocalAPIKey:     *localAPIKey,
		BootstrapClient: bootstrapClient,
		Status:          status,
		MinBackoff:      *minBackoff,
		MaxBackoff:      *maxBackoff,
	})
	if err != nil && ctx.Err() == nil {
		logger.Error("agent stopped", "error", err)
		os.Exit(1)
	}
}

func checkAndStageUpdate(ctx context.Context, manifestURL, publicKeyValue string) error {
	key, err := update.ParsePublicKey(publicKeyValue)
	if err != nil {
		return err
	}
	verified, err := (update.Client{PublicKey: key}).FetchAndVerify(ctx, manifestURL)
	if err != nil {
		return err
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	info, err := os.Stat(executable)
	if err != nil {
		return err
	}
	return update.AtomicReplace(executable, verified.Binary, info.Mode())
}

func serveMetrics(ctx context.Context, addr, localProvider, localBaseURL string, localTimeout time.Duration, status *tunnel.AgentStatus, logger *slog.Logger) {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		connected, _, _ := status.Snapshot()
		if !connected {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("tunnel disconnected\n"))
			return
		}
		if err := probeLocalProvider(r.Context(), localProvider, localBaseURL, localTimeout); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(localProvider + " unavailable\n"))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ready\n"))
	})
	server := &http.Server{Addr: addr, Handler: mux}
	errCh := make(chan error, 1)
	go func() {
		logger.Info("agent metrics server listening", "addr", addr)
		errCh <- server.ListenAndServe()
	}()
	select {
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			logger.Warn("agent metrics server stopped", "error", err)
		}
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			logger.Warn("agent metrics server shutdown failed", "error", err)
		}
	}
}

func loadAgentConfig(path string) (agentConfig, error) {
	if strings.TrimSpace(path) == "" {
		return agentConfig{}, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return agentConfig{}, err
	}
	var cfg agentConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return agentConfig{}, err
	}
	return cfg, nil
}

func findConfigPath(args []string) string {
	for i, arg := range args {
		if arg == "-config" && i+1 < len(args) {
			return args[i+1]
		}
		if value, ok := strings.CutPrefix(arg, "-config="); ok {
			return value
		}
	}
	return os.Getenv("FALLBAKIT_AGENT_CONFIG")
}

const (
	localProviderOllama = "ollama"
	localProviderOMLX   = "omlx"
	localProviderVLLM   = "vllm"
)

func resolveLocalProvider(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", localProviderOllama:
		return localProviderOllama, nil
	case localProviderOMLX:
		return localProviderOMLX, nil
	case localProviderVLLM:
		return localProviderVLLM, nil
	default:
		return "", fmt.Errorf("unsupported local provider %q", value)
	}
}

func resolveLocalBaseURL(provider, explicitBaseURL, ollamaAlias string) (string, error) {
	explicitBaseURL = strings.TrimSpace(explicitBaseURL)
	ollamaAlias = strings.TrimSpace(ollamaAlias)
	if explicitBaseURL != "" {
		return explicitBaseURL, nil
	}
	if provider == localProviderOllama {
		if ollamaAlias != "" {
			return ollamaAlias, nil
		}
		return "http://localhost:11434", nil
	}
	if ollamaAlias != "" {
		return "", fmt.Errorf("FALLBAKIT_OLLAMA_URL and -ollama are only supported with local provider %q; use FALLBAKIT_LOCAL_BASE_URL for %q", localProviderOllama, provider)
	}
	if provider == localProviderVLLM {
		return "http://localhost:8000", nil
	}
	return "http://localhost:8000", nil
}

func bootstrapOllamaURL(provider, localBaseURL string) string {
	if provider == localProviderOllama {
		return localBaseURL
	}
	return ""
}

func probeLocalProvider(ctx context.Context, provider, baseURL string, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	probePath, err := localProviderProbePath(provider)
	if err != nil {
		return err
	}
	endpoint, err := url.JoinPath(baseURL, probePath)
	if err != nil {
		return err
	}
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s probe status %d", provider, resp.StatusCode)
	}
	return nil
}

func localProviderProbePath(provider string) (string, error) {
	switch provider {
	case localProviderOllama:
		return "/api/tags", nil
	case localProviderOMLX, localProviderVLLM:
		return "/v1/models", nil
	default:
		return "", fmt.Errorf("unsupported local provider %q", provider)
	}
}

func flagWasSet(name string) bool {
	wasSet := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == name {
			wasSet = true
		}
	})
	return wasSet
}

func envOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func envBoolOrDefault(key string, fallback bool) bool {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value == "1" || value == "true" || value == "TRUE" || value == "yes"
}

func envDurationOrDefault(key string, fallback time.Duration) time.Duration {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func defaultString(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

func defaultDuration(value, fallback time.Duration) time.Duration {
	if value > 0 {
		return value
	}
	return fallback
}
