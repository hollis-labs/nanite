package station

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"golang.org/x/net/http/httpguts"
)

type headersKey struct{}

// CopyHeaders validates non-credential, application correlation headers and
// returns an owned, canonical copy. Reserved transport and identity fields
// remain under the transport's control.
func CopyHeaders(headers map[string]string) (map[string]string, error) {
	out := make(map[string]string, len(headers))
	for name, value := range headers {
		key := strings.ToLower(name)
		reserved := key == "authorization" || key == "cookie" || key == "host" || key == "connection" || key == "transfer-encoding" || key == "te" || key == "trailer" || key == "upgrade" || key == "accept" || key == "user-agent" || key == "x-api-key" || key == "x-station-session" || key == "x-nanite-principal" || key == "x-nanite-conversation" || key == "x-nanite-auth-server" || key == "x-run-token"
		for _, prefix := range []string{"content-", "proxy-", "x-forwarded-", "anthropic-", "openai-", "mcp-", "x-stainless-", "x-warden-"} {
			reserved = reserved || strings.HasPrefix(key, prefix)
		}
		if reserved || !httpguts.ValidHeaderFieldName(name) || !httpguts.ValidHeaderFieldValue(value) {
			return nil, fmt.Errorf("turn header %q is reserved or invalid", name)
		}
		canonical := http.CanonicalHeaderKey(name)
		if _, exists := out[canonical]; exists {
			return nil, fmt.Errorf("duplicate turn header %q", name)
		}
		out[canonical] = value
	}
	return out, nil
}

// WithHeaders attaches an already validated, owned header map to a request scope.
func WithHeaders(ctx context.Context, headers map[string]string) context.Context {
	return context.WithValue(ctx, headersKey{}, headers)
}

// Headers returns the request scope's immutable headers.
func Headers(ctx context.Context) map[string]string {
	h, _ := ctx.Value(headersKey{}).(map[string]string)
	return h
}
