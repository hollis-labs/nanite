package pluginapi

import (
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"unicode/utf8"

	"github.com/hollis-labs/plugin-sdk/manifest"
	sdkprocess "github.com/hollis-labs/plugin-sdk/subprocess"
)

const ContextProtocol = 1
const CapabilityContextSource = "context.source"
const MaxContextSources = 16
const MaxContextItems = 128
const MaxContextBytes = 1 << 20

// ContextFetchPath is a private host-to-child SDK http/handle path. It is not a
// browser or loopback API route and does not require a public http_routes entry.
const ContextFetchPath = "/__nanite/context/fetch"

// ContextSource declares a retrieval contribution to the existing dynamic
// context slot. Plugins cannot select prompt slots or cached instructions.
type ContextSource struct {
	ID string `json:"id"`
}

// ContextScope is context.source capability metadata. SourceIDs must name all
// declared sources. SessionIDs or AllSessions controls where retrieval runs.
// IncludeQuery separately permits user text and extracted keywords. Agent IDs
// and the intent category accompany permitted calls; project paths do not.
type ContextScope struct {
	SourceIDs    []string `json:"source_ids"`
	SessionIDs   []string `json:"session_ids,omitempty"`
	AllSessions  bool     `json:"all_sessions,omitempty"`
	IncludeQuery bool     `json:"include_query,omitempty"`
}

func (scope ContextScope) Validate() error {
	if len(scope.SourceIDs) == 0 || len(scope.SourceIDs) > MaxContextSources {
		return fmt.Errorf("pluginapi: context scope requires source IDs")
	}
	seen := map[string]bool{}
	for _, id := range scope.SourceIDs {
		if !slug.MatchString(id) || len(id) > 64 || seen[id] {
			return fmt.Errorf("pluginapi: invalid or duplicate context source")
		}
		seen[id] = true
	}
	if scope.AllSessions == (len(scope.SessionIDs) != 0) || len(scope.SessionIDs) > 64 {
		return fmt.Errorf("pluginapi: context scope requires session_ids or all_sessions exclusively")
	}
	seen = map[string]bool{}
	for _, id := range scope.SessionIDs {
		if !validQuerySession(id) || seen[id] {
			return fmt.Errorf("pluginapi: invalid or duplicate context session")
		}
		seen[id] = true
	}
	return nil
}

func DecodeContextScope(raw json.RawMessage) (ContextScope, error) {
	var scope ContextScope
	if len(raw) == 0 || len(raw) > maxQueryScopeBytes {
		return scope, fmt.Errorf("pluginapi: context scope is absent or oversized")
	}
	if err := manifest.DecodeExtension(raw, &scope); err != nil {
		return ContextScope{}, fmt.Errorf("pluginapi: invalid context scope: %w", err)
	}
	return scope, scope.Validate()
}

func (scope ContextScope) Allows(sourceID, sessionID string) bool {
	return slices.Contains(scope.SourceIDs, sourceID) && validQuerySession(sessionID) && (scope.AllSessions || slices.Contains(scope.SessionIDs, sessionID))
}

// ContextScopeFor validates the manifest's retrieval authority before approval.
// Declared contributions require one non-optional context.source capability;
// no dynamic or undeclared source can acquire this authority.
func ContextScopeFor(block Block, requests []sdkprocess.CapabilityRequest) (ContextScope, error) {
	var scope ContextScope
	found := false
	for _, request := range requests {
		if request.Name != CapabilityContextSource {
			continue
		}
		if found || request.Optional {
			return scope, fmt.Errorf("pluginapi: context.source requires one non-optional capability")
		}
		var err error
		scope, err = DecodeContextScope(request.Metadata)
		if err != nil {
			return ContextScope{}, err
		}
		found = true
	}
	if len(block.Registers.ContextSources) == 0 {
		if found {
			return ContextScope{}, fmt.Errorf("pluginapi: context scope has no declared sources")
		}
		return scope, nil
	}
	if err := block.Validate(); err != nil {
		return ContextScope{}, err
	}
	if !found || len(scope.SourceIDs) != len(block.Registers.ContextSources) {
		return ContextScope{}, fmt.Errorf("pluginapi: context sources require matching reviewed scope")
	}
	for _, source := range block.Registers.ContextSources {
		if !slices.Contains(scope.SourceIDs, source.ID) {
			return ContextScope{}, fmt.Errorf("pluginapi: undeclared context scope source")
		}
	}
	return scope, nil
}

type ContextRequest struct {
	Protocol    int      `json:"protocol"`
	SourceID    string   `json:"source_id"`
	SessionID   string   `json:"session_id"`
	AgentID     string   `json:"agent_id,omitempty"`
	Intent      string   `json:"intent"`
	TokenBudget int      `json:"token_budget"`
	QueryText   string   `json:"query_text,omitempty"`
	Keywords    []string `json:"keywords,omitempty"`
}

type ContextResponse struct {
	Protocol int           `json:"protocol"`
	Items    []ContextItem `json:"items"`
}

type ContextItem struct {
	Key       string  `json:"key"`
	Content   string  `json:"content"`
	Relevance float64 `json:"relevance"`
}

// DecodeContextRequest lets an SDK HTTPHandler dispatch this private operation.
// The host checks scopes; this decoder checks the bounded wire shape.
func DecodeContextRequest(request *sdkprocess.HTTPRequest) (ContextRequest, error) {
	var result ContextRequest
	if request == nil || request.Method != "POST" || request.Path != ContextFetchPath || len(request.Body) > MaxContextBytes {
		return result, fmt.Errorf("pluginapi: invalid context request")
	}
	if err := manifest.DecodeExtension(request.Body, &result); err != nil {
		return result, err
	}
	if result.Protocol != ContextProtocol || !slug.MatchString(result.SourceID) || len(result.SourceID) > 64 || !validQuerySession(result.SessionID) || result.SessionID != request.SessionID || result.TokenBudget <= 0 || result.TokenBudget > 50000 {
		return ContextRequest{}, fmt.Errorf("pluginapi: invalid context request fields")
	}
	return result, nil
}

// DecodeContextResponse refuses malformed, oversized or ambiguous output. The
// host supplies source ownership and token estimates; a child cannot claim them.
func DecodeContextResponse(raw []byte) (ContextResponse, error) {
	var result ContextResponse
	if len(raw) > MaxContextBytes {
		return result, fmt.Errorf("pluginapi: context response exceeds limit")
	}
	if err := manifest.DecodeExtension(raw, &result); err != nil {
		return result, err
	}
	if result.Protocol != ContextProtocol || result.Items == nil || len(result.Items) > MaxContextItems {
		return ContextResponse{}, fmt.Errorf("pluginapi: invalid context response")
	}
	seen := map[string]bool{}
	for _, item := range result.Items {
		if item.Key == "" || len(item.Key) > 256 || seen[item.Key] || !utf8.ValidString(item.Key) || item.Content == "" || !utf8.ValidString(item.Content) || math.IsNaN(item.Relevance) || math.IsInf(item.Relevance, 0) || item.Relevance < 0 || item.Relevance > 1 {
			return ContextResponse{}, fmt.Errorf("pluginapi: invalid context item")
		}
		seen[item.Key] = true
	}
	return result, nil
}
