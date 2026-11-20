package mcpclient

import (
	"bytes"
	"context"
	"io"
	"maps"
	"net/http"
	"time"

	"github.com/hollis-labs/nanite/internal/provider/station"
	"github.com/hollis-labs/nanite/internal/warden"
)

type legacySSEKey struct{}

func (r *registration) transportContext(ctx context.Context) context.Context {
	if !r.legacySSE {
		return ctx
	}
	return context.WithValue(ctx, legacySSEKey{}, true)
}

// authMarker is a header set in a server's resolved configuration when
// identity.yaml gives it Warden auth or correlation. The transport removes it
// and sets the real headers per request; it is never sent.
const authMarker = "X-Nanite-Auth-Server"

// Claimed correlation headers (identity.yaml's mcp correlation). They are
// what the agent says, not verified ids.
const (
	PrincipalHeader    = "X-Nanite-Principal"
	ConversationHeader = "X-Nanite-Conversation"
)

func (c *Client) httpClient(headers map[string]string, timeoutSeconds int) *http.Client {
	t := headerTransport{headers: headers}
	if server, ok := headers[authMarker]; ok && c.auth != nil {
		t.headers = maps.Clone(headers)
		delete(t.headers, authMarker)
		id := c.auth.Servers[server]
		t.principal = c.auth.Principal
		t.correlation = id.Correlation
		if id.Audience != "" && c.auth.Warden != nil {
			t.bearer = c.auth.Warden.BearerFor(id.Audience, id.Scopes, "MCP server "+server)
		}
	}
	return &http.Client{Timeout: time.Duration(timeoutSeconds) * time.Second, Transport: t}
}

type headerTransport struct {
	headers     map[string]string
	bearer      *warden.Bearer
	principal   string
	correlation bool
}

func (t headerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	for k, v := range t.headers {
		req.Header.Set(k, v)
	}
	if t.correlation {
		req.Header.Set(PrincipalHeader, t.principal)
		if id, ok := station.SessionID(req.Context()); ok {
			req.Header.Set(ConversationHeader, id)
		}
	}
	if t.bearer != nil {
		return station.SendWithBearer(req, t.bearer, t.send)
	}
	return t.send(req)
}

func (t headerTransport) send(req *http.Request) (*http.Response, error) {
	if legacy, _ := req.Context().Value(legacySSEKey{}).(bool); !legacy || req.Method != http.MethodGet {
		return http.DefaultTransport.RoundTrip(req)
	}
	// The SDK's legacy SSE GET stays open for the entire connection. Keep it
	// tied to discovery until the endpoint event arrives, then let the SDK
	// own its lifetime through response Body.Close.
	original := req.Context()
	stream, cancel := context.WithCancel(context.WithoutCancel(original))
	stop := context.AfterFunc(original, cancel)
	resp, err := http.DefaultTransport.RoundTrip(req.WithContext(stream))
	if err != nil {
		stop()
		cancel()
		return nil, err
	}
	resp.Body = &sseBody{ReadCloser: resp.Body, stop: stop, cancel: cancel}
	return resp, nil
}

type sseBody struct {
	io.ReadCloser
	stop     func() bool
	cancel   context.CancelFunc
	ready    bool
	line     []byte
	endpoint bool
}

func (b *sseBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if !b.ready && n > 0 {
		b.watchEndpoint(p[:n])
	}
	return n, err
}

func (b *sseBody) watchEndpoint(chunk []byte) {
	for len(chunk) > 0 {
		line, rest, ended := bytes.Cut(chunk, []byte("\n"))
		if len(b.line)+len(line) > maxResponseBytes {
			// The decoder will reject an oversized event. Keep the timeout armed.
			return
		}
		b.line = append(b.line, line...)
		if !ended {
			return
		}
		trimmed := bytes.TrimSpace(b.line)
		if name, ok := bytes.CutPrefix(trimmed, []byte("event:")); ok {
			b.endpoint = bytes.Equal(bytes.TrimSpace(name), []byte("endpoint"))
		}
		if len(trimmed) == 0 && b.endpoint {
			b.ready = true
			b.stop()
			b.line = nil
			return
		}
		b.line = b.line[:0]
		chunk = rest
	}
}

func (b *sseBody) Close() error {
	b.stop()
	b.cancel()
	return b.ReadCloser.Close()
}
