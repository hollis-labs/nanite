package station

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	"golang.org/x/net/http/httpguts"
)

type headersKey struct{}
type headerScope struct {
	values   map[string]string
	shadowed sync.Map
}

// CopyHeaders validates non-credential, application correlation headers and
// returns an owned, canonical copy. Reserved transport and identity fields
// remain under the transport's control.
func CopyHeaders(headers map[string]string) (map[string]string, error) {
	if len(headers) > 16 {
		return nil, fmt.Errorf("turn headers exceed the limit of 16")
	}
	out := make(map[string]string, len(headers))
	for name, value := range headers {
		key := strings.ToLower(name)
		reserved := key == "authorization" || key == "cookie" || key == "host" || key == "connection" || key == "transfer-encoding" || key == "te" || key == "trailer" || key == "upgrade" || key == "accept" || key == "user-agent" || key == "x-api-key" || key == "x-station-session" || key == "x-nanite-principal" || key == "x-nanite-conversation" || key == "x-nanite-auth-server" || key == "x-run-token"
		for _, prefix := range []string{"content-", "proxy-", "x-forwarded-", "anthropic-", "openai-", "mcp-", "x-stainless-", "x-warden-"} {
			reserved = reserved || strings.HasPrefix(key, prefix)
		}
		for _, word := range []string{"auth", "token", "key", "secret", "cookie", "session", "password", "credential"} {
			reserved = reserved || strings.Contains(key, word)
		}
		printable := len(value) <= 256
		for i := range len(value) {
			printable = printable && value[i] >= 0x20 && value[i] <= 0x7e
		}
		if reserved || !httpguts.ValidHeaderFieldName(name) || !printable {
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
	return context.WithValue(ctx, headersKey{}, &headerScope{values: headers})
}

// Headers returns the request scope's immutable headers.
func Headers(ctx context.Context) map[string]string {
	h, _ := ctx.Value(headersKey{}).(*headerScope)
	if h == nil {
		return nil
	}
	return h.values
}

// ConfiguredHeaders overlays host configuration on turn correlation metadata.
// Shadow warnings name only the header, at most once per header scope.
func ConfiguredHeaders(ctx context.Context, configured map[string]string, logger *slog.Logger) map[string]string {
	out := make(map[string]string, len(configured)+len(Headers(ctx)))
	for k, v := range Headers(ctx) {
		out[http.CanonicalHeaderKey(k)] = v
	}
	for k, v := range configured {
		name := http.CanonicalHeaderKey(k)
		if _, shadowed := out[name]; shadowed {
			h, _ := ctx.Value(headersKey{}).(*headerScope)
			if h != nil {
				if _, loaded := h.shadowed.LoadOrStore(name, true); !loaded {
					if logger == nil {
						logger = slog.Default()
					}
					logger.WarnContext(ctx, "turn header shadowed by host configuration", "header", name)
				}
			}
		}
		out[name] = v
	}
	return out
}
