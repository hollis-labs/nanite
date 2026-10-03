package pluginapi

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/hollis-labs/plugin-sdk/manifest"
	sdkprocess "github.com/hollis-labs/plugin-sdk/subprocess"
)

// CapabilityContextAlwaysShip requests reviewed persistent user-context placement.
const CapabilityContextAlwaysShip = "context.always_ship"

// AlwaysShipProtocol identifies the private always-ship fetch wire format.
const AlwaysShipProtocol = 1

// MaxAlwaysShipSources bounds declared sources per owner.
const MaxAlwaysShipSources = 4

// MaxAlwaysShipBodyBytes bounds decoded body bytes across an owner's sources.
const MaxAlwaysShipBodyBytes = 6000

// MaxAlwaysShipWireBytes bounds each encoded fetch request or response.
const MaxAlwaysShipWireBytes = 64 << 10

// AlwaysShipFetchPath is a private host-to-child http/handle operation over
// owned stdio, not a public HTTP route or a plugin-to-host authority surface.
const AlwaysShipFetchPath = "/__nanite/context/always-ship/fetch"

var alwaysShipTitleSeparators = regexp.MustCompile(`[ ._-]+`)

func normalizedAlwaysShipTitle(title string) string {
	return alwaysShipTitleSeparators.ReplaceAllString(strings.ToLower(title), " ")
}

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
	if !alwaysShipTitle.MatchString(source.Title) || normalizedAlwaysShipTitle(source.Title) == "session context" {
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
		return fmt.Errorf("pluginapi: invalid always-ship scope sources or byte cap")
	}
	if err := (ContextScope{SourceIDs: scope.SourceIDs, SessionIDs: scope.SessionIDs, AllSessions: scope.AllSessions}).Validate(); err != nil {
		detail := strings.ReplaceAll(err.Error(), "context scope", "always-ship scope")
		return fmt.Errorf("%s", strings.ReplaceAll(detail, "context ", "always-ship scope "))
	}
	return nil
}

func DecodeAlwaysShipScope(raw json.RawMessage) (AlwaysShipScope, error) {
	var scope AlwaysShipScope
	if len(raw) == 0 || len(raw) > maxQueryScopeBytes {
		return scope, fmt.Errorf("pluginapi: always-ship scope absent or oversized")
	}
	if err := manifest.DecodeExtension(raw, &scope); err != nil {
		return AlwaysShipScope{}, fmt.Errorf("pluginapi: invalid always-ship scope: %w", err)
	}
	if err := scope.Validate(); err != nil {
		return AlwaysShipScope{}, err
	}
	return scope, nil
}

// Allows checks source membership and a valid session ID against declared scope.
// It does not establish host approval or grant lifetime.
func (scope AlwaysShipScope) Allows(sourceID, sessionID string) bool {
	return slices.Contains(scope.SourceIDs, sourceID) && validQuerySession(sessionID) && (scope.AllSessions || slices.Contains(scope.SessionIDs, sessionID))
}

// AlwaysShipScopeFor validates declared authority, not a runtime grant. Pass
// requests and tools from the same validated shared manifest that owns block.
// Hosts must separately enforce accepted approval, session scope, lifetime and
// contextual title reservations (e.g. Session Documents while core owns it).
// A list tool must support a current-session inventory call without arguments.
// Its read effect is self-declared; host review must show the tool and its effect.
// Availability and execution still depend on ordinary agent_tools grants.
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
	declaredTools := make(map[string]manifest.Tool, len(tools))
	for _, tool := range tools {
		if _, duplicate := declaredTools[tool.Name]; duplicate {
			return AlwaysShipScope{}, fmt.Errorf("pluginapi: duplicate always-ship manifest tool")
		}
		declaredTools[tool.Name] = tool
	}
	for _, source := range block.Registers.AlwaysShipSources {
		tool, declared := declaredTools[source.ListTool]
		if !slices.Contains(scope.SourceIDs, source.ID) || !declared || tool.Effect != ToolEffectRead {
			return AlwaysShipScope{}, fmt.Errorf("pluginapi: always-ship source requires reviewed scope and a declared read list tool")
		}
		var schema struct {
			Required []string `json:"required"`
		}
		if err := json.Unmarshal(tool.InputSchema, &schema); err != nil || len(schema.Required) != 0 {
			return AlwaysShipScope{}, fmt.Errorf("pluginapi: always-ship list tool must be callable without arguments")
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
	if len(result.AgentID) > 128 || len(result.Intent) > 64 || !utf8.ValidString(result.AgentID) || !utf8.ValidString(result.Intent) || strings.ContainsFunc(result.AgentID, unicode.IsControl) || strings.ContainsFunc(result.Intent, unicode.IsControl) {
		return AlwaysShipRequest{}, fmt.Errorf("pluginapi: invalid always-ship routing hints")
	}
	return result, nil
}

// DecodeAlwaysShipResponse checks the assigned body-byte allowance as well as
// the wire ceiling. maxBytes may be zero to accept only an empty body. Unknown,
// duplicate, missing/null body and malformed text are refused, never truncated.
// Body rejects NUL and invalid UTF-8 only; other controls (including ESC and
// CRLF) and headings remain intact. Reviewed body content is not sanitized.
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
