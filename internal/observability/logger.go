package observability

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync/atomic"
)

const (
	FormatJSON = "json"
	FormatText = "text"
)

var serviceName atomic.Value

func init() {
	serviceName.Store("fallbakit")
}

type RedactingHandler struct {
	next slog.Handler
}

func NewRedactingHandler(next slog.Handler) *RedactingHandler {
	return &RedactingHandler{next: next}
}

func (h *RedactingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *RedactingHandler) Handle(ctx context.Context, record slog.Record) error {
	out := slog.NewRecord(record.Time, record.Level, record.Message, record.PC)
	record.Attrs(func(attr slog.Attr) bool {
		out.AddAttrs(redactAttr(attr))
		return true
	})
	return h.next.Handle(ctx, out)
}

func (h *RedactingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	redacted := make([]slog.Attr, 0, len(attrs))
	for _, attr := range attrs {
		redacted = append(redacted, redactAttr(attr))
	}
	return &RedactingHandler{next: h.next.WithAttrs(redacted)}
}

func (h *RedactingHandler) WithGroup(name string) slog.Handler {
	return &RedactingHandler{next: h.next.WithGroup(name)}
}

func redactAttr(attr slog.Attr) slog.Attr {
	if IsSensitiveKey(attr.Key) {
		return slog.String(attr.Key, Redacted)
	}
	if attr.Value.Kind() == slog.KindGroup {
		children := attr.Value.Group()
		redacted := make([]slog.Attr, 0, len(children))
		for _, child := range children {
			redacted = append(redacted, redactAttr(child))
		}
		return slog.Group(attr.Key, attrsToAny(redacted)...)
	}
	return attr
}

func attrsToAny(attrs []slog.Attr) []any {
	values := make([]any, 0, len(attrs))
	for _, attr := range attrs {
		values = append(values, attr)
	}
	return values
}

func NewLogger(service, format string, out io.Writer) *slog.Logger {
	if out == nil {
		out = os.Stdout
	}
	if service == "" {
		service = CurrentService()
	}
	if service == "" {
		service = "fallbakit"
	}
	serviceName.Store(service)

	options := &slog.HandlerOptions{Level: slog.LevelInfo}
	format = strings.ToLower(strings.TrimSpace(format))
	var handler slog.Handler
	if format == FormatText {
		handler = slog.NewTextHandler(out, options)
	} else {
		handler = slog.NewJSONHandler(out, options)
	}
	return slog.New(NewRedactingHandler(handler)).With("service", service)
}

func ConfigureDefaultLogger(service, format string) *slog.Logger {
	logger := NewLogger(service, format, os.Stdout)
	slog.SetDefault(logger)
	return logger
}

func CurrentService() string {
	value, _ := serviceName.Load().(string)
	if value == "" {
		return "fallbakit"
	}
	return value
}
