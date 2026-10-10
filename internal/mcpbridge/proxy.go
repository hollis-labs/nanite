package mcpbridge

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/hollis-labs/nanite/internal/mcp"
	"github.com/hollis-labs/nanite/internal/plugin/subprocess"
)

// Proxy is an internal candidate transport to one running execution owner. Its
// credential must come from the trusted host's bootstrap; it reads no ambient
// token, opens no datastore and spawns no plugin host.
type Proxy struct {
	endpoint   string
	credential string
	limits     TransportLimits
	client     *http.Client
}

func (Proxy) Format(s fmt.State, _ rune) { _, _ = fmt.Fprint(s, "[MCP proxy credential redacted]") }

func NewProxy(endpoint, credential string, limits TransportLimits) (*Proxy, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Opaque != "" {
		return nil, errors.New("invalid MCP proxy loopback endpoint")
	}
	ip := net.ParseIP(parsed.Hostname())
	if ip == nil || !ip.IsLoopback() || parsed.Port() == "" {
		return nil, errors.New("MCP proxy endpoint requires a literal loopback IP and port")
	}
	if len(credential) != 43 {
		return nil, ErrUnauthenticated
	}
	if raw, err := base64.RawURLEncoding.Strict().DecodeString(credential); err != nil || len(raw) != 32 {
		return nil, ErrUnauthenticated
	}
	if limits.MaxRequestBytes <= 0 || limits.MaxResponseBytes <= 0 || limits.MaxCallDuration <= 0 {
		return nil, errors.New("explicit positive MCP proxy transport limits are required")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	// Environment proxy settings must not redirect a scoped host credential.
	transport.Proxy = nil
	return &Proxy{endpoint: strings.TrimRight(endpoint, "/"), credential: credential, limits: limits, client: &http.Client{Transport: transport, Timeout: limits.MaxCallDuration, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func (p *Proxy) Close() { p.client.CloseIdleConnections() }

func (p *Proxy) List(ctx context.Context) (Catalog, error) {
	var catalog Catalog
	if err := p.request(ctx, "list", ListRequest{Version: CandidateVersion}, &catalog); err != nil {
		return Catalog{}, err
	}
	if catalog.Version != CandidateVersion || catalog.Revision == "" || catalog.Tools == nil {
		return Catalog{}, ErrTargetUnavailable
	}
	for _, tool := range catalog.Tools {
		if tool.Name == "" || len(tool.Name) > 64 || tool.Binding == "" || len(tool.Binding) > 1024 {
			return Catalog{}, ErrTargetUnavailable
		}
	}
	return catalog, nil
}

// Call forwards the exact supplied catalog binding and operation key once.
// A stale binding requires explicit fresh discovery; mutations are never retried.
func (p *Proxy) Call(ctx context.Context, request CallRequest) (*mcp.ToolResult, error) {
	if request.Version != CandidateVersion || !validRequestID(request.RequestID) || request.Name == "" || request.Binding == "" {
		return nil, errors.New("invalid MCP proxy call")
	}
	var result mcp.ToolResult
	if err := p.request(ctx, "call", request, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (p *Proxy) Cancel(ctx context.Context, requestID string) (bool, error) {
	if !validRequestID(requestID) {
		return false, errors.New("invalid MCP proxy cancellation")
	}
	var result struct {
		Version  int  `json:"version"`
		Canceled bool `json:"canceled"`
	}
	if err := p.request(ctx, "cancel", CancelRequest{Version: CandidateVersion, RequestID: requestID}, &result); err != nil {
		return false, err
	}
	if result.Version != CandidateVersion {
		return false, ErrTargetUnavailable
	}
	return result.Canceled, nil
}

type RemoteError struct {
	Code   string
	Status int
}

func (e *RemoteError) Error() string { return "MCP proxy refused: " + e.Code }
func (e *RemoteError) Is(target error) bool {
	switch e.Code {
	case "unauthenticated":
		return target == ErrUnauthenticated
	case "authority_unavailable":
		return target == ErrAuthorityUnavailable
	case "target_unavailable":
		return target == ErrTargetUnavailable
	case "forbidden":
		return target == ErrForbidden
	case "conflict":
		return target == ErrConflict
	case "overloaded":
		return target == ErrCapacity
	case "unknown_outcome":
		return target == ErrUnknownOutcome
	case "deadline_exceeded":
		return target == context.DeadlineExceeded
	case "canceled":
		return target == context.Canceled
	case "stale_binding":
		return target == subprocess.ErrStaleBinding
	}
	return false
}

func (p *Proxy) request(ctx context.Context, method string, input, output any) error {
	body, err := json.Marshal(input)
	if err != nil {
		return errors.New("invalid MCP proxy request")
	}
	if int64(len(body)) > p.limits.MaxRequestBytes {
		return errors.New("MCP proxy request limit exceeded")
	}
	ctx, cancel := context.WithTimeout(ctx, p.limits.MaxCallDuration)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint+"/api/mcp/v1/"+method, bytes.NewReader(body))
	if err != nil {
		return errors.New("invalid MCP proxy endpoint")
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+p.credential)
	response, err := p.client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrTargetUnavailable
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, int64(p.limits.MaxResponseBytes)+1))
	if err != nil || len(raw) > p.limits.MaxResponseBytes {
		return errors.New("MCP proxy response limit exceeded")
	}
	if response.StatusCode != http.StatusOK {
		var refusal struct {
			Code string `json:"error"`
		}
		if json.Unmarshal(raw, &refusal) != nil {
			return ErrTargetUnavailable
		}
		switch refusal.Code {
		case "unauthenticated", "authority_unavailable", "target_unavailable", "forbidden", "conflict", "overloaded", "unknown_outcome", "deadline_exceeded", "canceled", "stale_binding", "invalid_request", "response_limit", "unknown_method", "method_not_allowed":
			return &RemoteError{Code: refusal.Code, Status: response.StatusCode}
		default:
			return ErrTargetUnavailable
		}
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return ErrTargetUnavailable
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(output) != nil {
		return ErrTargetUnavailable
	}
	if decoder.Decode(new(any)) != io.EOF {
		return ErrTargetUnavailable
	}
	return nil
}
