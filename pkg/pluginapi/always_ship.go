package pluginapi

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/hollis-labs/plugin-sdk/manifest"
	sdkprocess "github.com/hollis-labs/plugin-sdk/subprocess"
)

const CapabilityContextAlwaysShip = "context.always_ship"
const AlwaysShipProtocol = 1
const MaxAlwaysShipSources = 4
const MaxAlwaysShipBodyBytes = 6000
const MaxAlwaysShipWireBytes = 64 << 10

// AlwaysShipFetchPath is a private host-to-child http/handle operation over
// owned stdio, not a public HTTP route or a plugin-to-host authority surface.
const AlwaysShipFetchPath = "/__nanite/context/always-ship/fetch"

var alwaysShipTitle = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9 ._-]{0,62}[A-Za-z0-9])?$`)

// AlwaysShipSource contributes a plugin-rendered body to the non-compactable
// user-context slot. The host adds "## Title\n" and enforces the shared budget.
// ListTool names a read-effect tool in the SAME shared manifest, using its
// public manifest name. This declaration never grants agent access to that tool.
type AlwaysShipSource struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	ListTool string `json:"list_tool"`
}

func (source AlwaysShipSource) Validate() error {
	if !slug.MatchString(source.ID) || len(source.ID) > 64 {
		return fmt.Errorf("pluginapi: invalid always-ship source ID")
	}
	if !alwaysShipTitle.MatchString(source.Title) || source.Title == "Session Context" {
		return fmt.Errorf("pluginapi: invalid or reserved always-ship title")
	}
	if !agentToolName.MatchString(source.ListTool) {
		return fmt.Errorf("pluginapi: invalid always-ship list tool")
	}
	return nil
}

// AlwaysShipScope is context.always_ship metadata. MaxBytes is a body-byte cap
// shared by ALL sources of this owner, not a token count or a per-source cap.
// The host may assign less space and must also charge headings, separators and
// fallbacks to the existing user-context token ceiling. No query text is shared.
type AlwaysShipScope struct {
	SourceIDs   []string `json:"source_ids"`
	SessionIDs  []string `json:"session_ids,omitempty"`
	AllSessions bool     `json:"all_sessions,omitempty"`
	MaxBytes    int      `json:"max_bytes"`
}

func (scope AlwaysShipScope) Validate() error {
	if len(scope.SourceIDs) == 0 || len(scope.SourceIDs) > MaxAlwaysShipSources || scope.MaxBytes <= 0 || scope.MaxBytes > MaxAlwaysShipBodyBytes {
		return fmt.Errorf("pluginapi: invalid always-ship sources or byte cap")
	}
	return (ContextScope{SourceIDs: scope.SourceIDs, SessionIDs: scope.SessionIDs, AllSessions: scope.AllSessions}).Validate()
}

func DecodeAlwaysShipScope(raw json.RawMessage) (AlwaysShipScope, error) {
	var scope AlwaysShipScope
	if len(raw) == 0 || len(raw) > maxQueryScopeBytes {
		return scope, fmt.Errorf("pluginapi: always-ship scope absent or oversized")
	}
	if err := manifest.DecodeExtension(raw, &scope); err != nil {
		return AlwaysShipScope{}, fmt.Errorf("pluginapi: invalid always-ship scope: %w", err)
	}
	return scope, scope.Validate()
}

func (scope AlwaysShipScope) Allows(sourceID, sessionID string) bool {
	return slices.Contains(scope.SourceIDs, sourceID) && validQuerySession(sessionID) && (scope.AllSessions || slices.Contains(scope.SessionIDs, sessionID))
}

// AlwaysShipScopeFor validates declared authority, not a runtime grant. Pass
// requests and tools from the same validated shared manifest that owns block.
// Hosts must separately enforce accepted approval, session scope, lifetime and
// contextual title reservations (e.g. Session Documents while core owns it).
// A list tool must support a current-session inventory call; availability and
// execution still depend on the caller's ordinary agent_tools grants.
func AlwaysShipScopeFor(block Block, requests []sdkprocess.CapabilityRequest, tools []manifest.Tool) (AlwaysShipScope, error) {
	var scope AlwaysShipScope
	found := false
	for _, request := range requests {
		if request.Name != CapabilityContextAlwaysShip {
			continue
		}
		if found || request.Optional {
			return AlwaysShipScope{}, fmt.Errorf("pluginapi: context.always_ship requires one non-optional capability")
		}
		var err error
		scope, err = DecodeAlwaysShipScope(request.Metadata)
		if err != nil {
			return AlwaysShipScope{}, err
		}
		found = true
	}
	if len(block.Registers.AlwaysShipSources) == 0 {
		if found {
			return AlwaysShipScope{}, fmt.Errorf("pluginapi: always-ship scope has no declared sources")
		}
		return scope, nil
	}
	if err := block.Validate(); err != nil {
		return AlwaysShipScope{}, err
	}
	if !found || len(scope.SourceIDs) != len(block.Registers.AlwaysShipSources) {
		return AlwaysShipScope{}, fmt.Errorf("pluginapi: always-ship sources require matching reviewed scope")
	}
	if err := ValidateAgentTools(tools); err != nil {
		return AlwaysShipScope{}, err
	}
	toolEffects := make(map[string]string, len(tools))
	for _, tool := range tools {
		if _, duplicate := toolEffects[tool.Name]; duplicate {
			return AlwaysShipScope{}, fmt.Errorf("pluginapi: duplicate always-ship manifest tool")
		}
		toolEffects[tool.Name] = tool.Effect
	}
	for _, source := range block.Registers.AlwaysShipSources {
		if !slices.Contains(scope.SourceIDs, source.ID) || toolEffects[source.ListTool] != ToolEffectRead {
			return AlwaysShipScope{}, fmt.Errorf("pluginapi: always-ship source requires reviewed scope and a declared read list tool")
		}
	}
	return scope, nil
}

type AlwaysShipRequest struct {
	Protocol  int    `json:"protocol"`
	SourceID  string `json:"source_id"`
	SessionID string `json:"session_id"`
	AgentID   string `json:"agent_id,omitempty"`
	Intent    string `json:"intent"`
	MaxBytes  int    `json:"max_bytes"`
}

// AlwaysShipResponse carries only the plugin-rendered section body. Empty is
// explicit success. A partial body must itself explain omitted content and how
// to list/read it; the host cannot infer item completeness from opaque text.
type AlwaysShipResponse struct {
	Protocol int    `json:"protocol"`
	Body     string `json:"body"`
}

// DecodeAlwaysShipRequest validates bounded wire input; it grants no scope.
// AgentID is an optional host routing hint, never a credential. User query text
// and keywords have no fields here and are rejected by the strict decoder.
func DecodeAlwaysShipRequest(request *sdkprocess.HTTPRequest) (AlwaysShipRequest, error) {
	var result AlwaysShipRequest
	if request == nil || request.Method != "POST" || request.Path != AlwaysShipFetchPath || len(request.Body) > MaxAlwaysShipWireBytes || !utf8.Valid(request.Body) {
		return result, fmt.Errorf("pluginapi: invalid always-ship request")
	}
	if err := manifest.DecodeExtension(request.Body, &result); err != nil {
		return result, err
	}
	if result.Protocol != AlwaysShipProtocol || !slug.MatchString(result.SourceID) || len(result.SourceID) > 64 || !validQuerySession(result.SessionID) || result.SessionID != request.SessionID || result.MaxBytes <= 0 || result.MaxBytes > MaxAlwaysShipBodyBytes {
		return AlwaysShipRequest{}, fmt.Errorf("pluginapi: invalid always-ship request fields")
	}
	if len(result.AgentID) > 128 || len(result.Intent) > 64 || !utf8.ValidString(result.AgentID) || !utf8.ValidString(result.Intent) || strings.ContainsRune(result.AgentID, 0) || strings.ContainsRune(result.Intent, 0) {
		return AlwaysShipRequest{}, fmt.Errorf("pluginapi: invalid always-ship routing hints")
	}
	return result, nil
}

// DecodeAlwaysShipResponse checks the assigned body-byte allowance as well as
// the wire ceiling. maxBytes may be zero to accept only an empty body. Unknown,
// duplicate, missing/null body and malformed text are refused, never truncated.
func DecodeAlwaysShipResponse(raw []byte, maxBytes int) (AlwaysShipResponse, error) {
	var wire struct {
		Protocol int     `json:"protocol"`
		Body     *string `json:"body"`
	}
	if maxBytes < 0 || maxBytes > MaxAlwaysShipBodyBytes || len(raw) > MaxAlwaysShipWireBytes || !utf8.Valid(raw) {
		return AlwaysShipResponse{}, fmt.Errorf("pluginapi: always-ship response exceeds limits or is invalid UTF-8")
	}
	if err := manifest.DecodeExtension(raw, &wire); err != nil {
		return AlwaysShipResponse{}, err
	}
	if wire.Protocol != AlwaysShipProtocol || wire.Body == nil || len(*wire.Body) > maxBytes || !utf8.ValidString(*wire.Body) || strings.ContainsRune(*wire.Body, 0) {
		return AlwaysShipResponse{}, fmt.Errorf("pluginapi: invalid always-ship response")
	}
	return AlwaysShipResponse{Protocol: wire.Protocol, Body: *wire.Body}, nil
}
