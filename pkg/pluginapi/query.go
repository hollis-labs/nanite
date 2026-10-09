package pluginapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/manifest"
)

// QueryProtocol versions the read-only query wire independently of UI registration.
const QueryProtocol = 1

const CapabilityReadOnlyQuery = "readonly.query"

type QueryResource string

const (
	QuerySessions          QueryResource = "sessions"
	QueryUsage             QueryResource = "usage"
	QueryExecutionMetrics  QueryResource = "execution_metrics"
	QueryContextSlots      QueryResource = "context_slots"
	QueryMessageReferences QueryResource = "message_refs"
	QueryDataExports       QueryResource = "data_exports"
)

const MaxQueryLimit = 100
const maxQueryScopeBytes = 16 << 10

// MaxQueryResponseBytes bounds a complete query response, including its data.
const MaxQueryResponseBytes = 1 << 20

// QueryScope is readonly.query capability metadata. Session IDs name an explicit
// allowlist; AllSessions instead permits reads in the host's current workspace.
// IncludeContent additionally permits raw context-slot text, including sensitive
// instructions. Without it, context slots expose accounting metadata only.
type QueryScope struct {
	Resources      []QueryResource `json:"resources"`
	SessionIDs     []string        `json:"session_ids,omitempty"`
	AllSessions    bool            `json:"all_sessions,omitempty"`
	IncludeContent bool            `json:"include_content,omitempty"`
}

func knownQueryResource(resource QueryResource) bool {
	switch resource {
	case QuerySessions, QueryUsage, QueryExecutionMetrics, QueryContextSlots, QueryMessageReferences, QueryDataExports:
		return true
	default:
		return false
	}
}

func validQuerySession(id string) bool {
	if len(id) == 0 || len(id) > 128 {
		return false
	}
	for _, ch := range id {
		if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '.' || ch == '-' || ch == '_') {
			return false
		}
	}
	return true
}

func (scope QueryScope) Validate() error {
	if len(scope.Resources) == 0 || len(scope.Resources) > 6 {
		return fmt.Errorf("pluginapi: query scope requires one to six resources")
	}
	seen := make(map[QueryResource]bool)
	for _, resource := range scope.Resources {
		if !knownQueryResource(resource) || seen[resource] {
			return fmt.Errorf("pluginapi: unknown or duplicate query resource")
		}
		seen[resource] = true
	}
	if scope.AllSessions == (len(scope.SessionIDs) != 0) || len(scope.SessionIDs) > 64 {
		return fmt.Errorf("pluginapi: query scope requires session_ids or all_sessions exclusively")
	}
	ids := make(map[string]bool)
	for _, id := range scope.SessionIDs {
		if !validQuerySession(id) || ids[id] {
			return fmt.Errorf("pluginapi: invalid or duplicate query session")
		}
		ids[id] = true
	}
	if seen[QueryDataExports] && !scope.AllSessions {
		return fmt.Errorf("pluginapi: data exports require explicit workspace scope")
	}
	if scope.IncludeContent && !seen[QueryContextSlots] {
		return fmt.Errorf("pluginapi: include_content requires context_slots")
	}
	return nil
}

func DecodeQueryScope(raw json.RawMessage) (QueryScope, error) {
	var scope QueryScope
	if len(raw) == 0 || len(raw) > maxQueryScopeBytes {
		return scope, fmt.Errorf("pluginapi: query scope is absent or oversized")
	}
	if err := manifest.DecodeExtension(raw, &scope); err != nil {
		return QueryScope{}, fmt.Errorf("pluginapi: invalid query scope: %w", err)
	}
	return scope, scope.Validate()
}

// Allows checks resource and session narrowing. An empty session is only valid
// for listing permitted session metadata; all other resources require one ID.
func (scope QueryScope) Allows(resource QueryResource, sessionID string) bool {
	if !knownQueryResource(resource) || !slices.Contains(scope.Resources, resource) {
		return false
	}
	if resource == QueryDataExports {
		return sessionID == "" && scope.AllSessions
	}
	if sessionID == "" {
		return resource == QuerySessions
	}
	return validQuerySession(sessionID) && (scope.AllSessions || slices.Contains(scope.SessionIDs, sessionID))
}

// QueryGrant is carried in plugin/init Identity under nanite_host_query. Its
// token belongs to this subprocess connection, is not persisted, and must not
// be forwarded to a browser or written to logs. Unload revokes the credential.
type QueryGrant struct {
	Protocol int        `json:"protocol"`
	PluginID string     `json:"plugin_id"`
	HostURL  string     `json:"host_url"`
	Token    string     `json:"token"`
	Scope    QueryScope `json:"scope"`
}

var ErrQueryNotGranted = errors.New("pluginapi: read-only queries were not granted")

func QueryGrantFromIdentity(identity json.RawMessage) (QueryGrant, error) {
	if len(identity) == 0 {
		return QueryGrant{}, ErrQueryNotGranted
	}
	if len(identity) > maxQueryScopeBytes {
		return QueryGrant{}, fmt.Errorf("pluginapi: query identity is oversized")
	}
	var claims hostIdentity
	if err := manifest.DecodeExtension(identity, &claims); err != nil {
		return QueryGrant{}, fmt.Errorf("pluginapi: invalid host identity")
	}
	raw := claims.HostQuery
	if len(raw) == 0 {
		return QueryGrant{}, ErrQueryNotGranted
	}
	var grant QueryGrant
	if err := manifest.DecodeExtension(raw, &grant); err != nil {
		return QueryGrant{}, fmt.Errorf("pluginapi: invalid query grant")
	}
	return grant, grant.Validate()
}

func (grant QueryGrant) Validate() error {
	if grant.Protocol != QueryProtocol || strings.TrimSpace(grant.PluginID) == "" || len(grant.PluginID) > 63 || len(grant.HostURL) > 1024 {
		return fmt.Errorf("pluginapi: invalid query grant identity or protocol")
	}
	token, err := base64.RawURLEncoding.DecodeString(grant.Token)
	if err != nil || len(token) != 32 {
		return fmt.Errorf("pluginapi: invalid query credential")
	}
	if _, hostErr := loopbackHostURL(grant.HostURL); hostErr != nil {
		return hostErr
	}
	return grant.Scope.Validate()
}

type QueryRequest struct {
	Resource  QueryResource
	SessionID string
	MessageID string // optional narrowing for message_refs only
	Limit     int    // zero uses the host default; otherwise 1–100
}

type QueryResponse struct {
	Protocol  int             `json:"protocol"`
	Resource  QueryResource   `json:"resource"`
	SessionID string          `json:"session_id,omitempty"`
	Data      json.RawMessage `json:"data"`
}

// QueryClient performs only fixed read operations against an explicit loopback
// host. It refuses redirects, ignores environment proxies, bounds responses,
// and never retries. A credential grants no arbitrary SQL or tool execution.
type QueryClient struct {
	grant    QueryGrant
	endpoint string
	http     *http.Client
}

func NewQueryClient(grant QueryGrant, transport http.RoundTripper) (*QueryClient, error) {
	if err := grant.Validate(); err != nil {
		return nil, err
	}
	client, err := NewClient(grant.HostURL, transport)
	if err != nil {
		return nil, err
	}
	grant.Scope.Resources = slices.Clone(grant.Scope.Resources)
	grant.Scope.SessionIDs = slices.Clone(grant.Scope.SessionIDs)
	httpClient := *client.http
	httpClient.Timeout = 30 * time.Second
	return &QueryClient{grant: grant, endpoint: strings.TrimSuffix(grant.HostURL, "/") + "/api/plugin-host/query/", http: &httpClient}, nil
}

func (client *QueryClient) Query(ctx context.Context, query QueryRequest) (QueryResponse, error) {
	if !client.grant.Scope.Allows(query.Resource, query.SessionID) || query.Limit < 0 || query.Limit > MaxQueryLimit {
		return QueryResponse{}, fmt.Errorf("pluginapi: query exceeds the granted scope or limit")
	}
	if query.MessageID != "" && (query.Resource != QueryMessageReferences || !validQuerySession(query.MessageID)) {
		return QueryResponse{}, fmt.Errorf("pluginapi: invalid message reference query")
	}
	parameters := url.Values{}
	if query.SessionID != "" {
		parameters.Set("session_id", query.SessionID)
	}
	if query.MessageID != "" {
		parameters.Set("message_id", query.MessageID)
	}
	if query.Limit != 0 {
		parameters.Set("limit", strconv.Itoa(query.Limit))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, client.endpoint+string(query.Resource)+"?"+parameters.Encode(), nil)
	if err != nil {
		return QueryResponse{}, err
	}
	req.Header.Set("Authorization", "Bearer "+client.grant.Token)
	resp, err := client.http.Do(req)
	if err != nil {
		return QueryResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return QueryResponse{}, fmt.Errorf("pluginapi: query host returned HTTP %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, MaxQueryResponseBytes+1))
	if err != nil {
		return QueryResponse{}, err
	}
	if len(raw) > MaxQueryResponseBytes {
		return QueryResponse{}, fmt.Errorf("pluginapi: query response is oversized")
	}
	var result QueryResponse
	if err := manifest.DecodeExtension(raw, &result); err != nil {
		return QueryResponse{}, fmt.Errorf("pluginapi: invalid query response")
	}
	if result.Protocol != QueryProtocol || result.Resource != query.Resource || result.SessionID != query.SessionID || result.Data == nil {
		return QueryResponse{}, fmt.Errorf("pluginapi: query response does not match the request")
	}
	return result, nil
}
