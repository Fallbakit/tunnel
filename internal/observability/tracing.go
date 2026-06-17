package observability

import (
	"context"
	"errors"
	"os"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.37.0"
	"go.opentelemetry.io/otel/trace"
)

type TraceConfig struct {
	ServiceName string
	Endpoint    string
}

type TraceProvider struct {
	provider *sdktrace.TracerProvider
	enabled  bool
}

func InitTracing(ctx context.Context, cfg TraceConfig) (*TraceProvider, error) {
	endpoint := strings.TrimSpace(cfg.Endpoint)
	if endpoint == "" {
		endpoint = strings.TrimSpace(os.Getenv("FALLBAKIT_OTEL_ENDPOINT"))
	}
	if endpoint == "" {
		otel.SetTracerProvider(trace.NewNoopTracerProvider())
		return &TraceProvider{}, nil
	}
	service := cfg.ServiceName
	if service == "" {
		service = CurrentService()
	}
	exporter, err := otlptracehttp.New(ctx,
		otlptracehttp.WithEndpoint(endpoint),
		otlptracehttp.WithInsecure(),
	)
	if err != nil {
		return nil, err
	}
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName(service),
		)),
	)
	otel.SetTracerProvider(provider)
	return &TraceProvider{provider: provider, enabled: true}, nil
}

func (p *TraceProvider) Enabled() bool {
	return p != nil && p.enabled
}

func (p *TraceProvider) Shutdown(ctx context.Context) error {
	if p == nil || p.provider == nil {
		return nil
	}
	err := p.provider.Shutdown(ctx)
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

func Tracer(name string) trace.Tracer {
	if name == "" {
		name = CurrentService()
	}
	return otel.Tracer(name)
}
