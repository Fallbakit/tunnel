package observability

import (
	"net/http"
	"strings"
)

const Redacted = "[REDACTED]"

var sensitiveFragments = []string{
	"authorization",
	"api-key",
	"api_key",
	"apikey",
	"bearer",
	"client-secret",
	"client_secret",
	"credential",
	"key",
	"mtls",
	"password",
	"private",
	"prompt",
	"provider",
	"response",
	"secret",
	"signature",
	"token",
}

func RedactField(key string, value any) any {
	if IsSensitiveKey(key) {
		return Redacted
	}
	return value
}

func RedactString(key, value string) string {
	if IsSensitiveKey(key) {
		return Redacted
	}
	return value
}

func IsSensitiveKey(key string) bool {
	normalized := strings.ToLower(strings.TrimSpace(key))
	for _, fragment := range sensitiveFragments {
		if strings.Contains(normalized, fragment) {
			return true
		}
	}
	return false
}

func RedactHeaders(headers http.Header) map[string]string {
	redacted := make(map[string]string, len(headers))
	for key, values := range headers {
		value := strings.Join(values, ",")
		redacted[key] = RedactString(key, value)
	}
	return redacted
}
