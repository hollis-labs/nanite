package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/mcp"
	recoverpkg "github.com/hollis-labs/nanite/internal/recover"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/toolclient"
)

// ToolSelection holds the result of tool selection, including progressive
// discovery metadata. Mirrors chat.toolSelection but is owned by the service layer.
type ToolSelection struct {
	Tools         []provider.ToolDefinition // tools to send to the LLM
	Catalog       string                    // non-empty when progressive discovery is active
	Progressive   bool                      // true when using progressive discovery
	OverrideBlock string                    // markdown "## Tool Overrides" section composed from per-tool Hints (go-toolbroker); empty when no tool in the final selection has enrichment
}

// ToolResult holds the outcome of a single tool execution.
type ToolResult struct {
	Output  string // the raw result text
	IsError bool   // true if the tool call failed
}

// ToolService encapsulates tool selection, execution, and progressive discovery.
// It unifies the two duplicated execution branches (ToolClient path and
// MCPManager fallback path) from the old Engine into a single Execute method.
type ToolService interface {
	// SelectForAgent returns the tool set for an agent, applying intent
	// extraction, permission filtering, allowlist filtering, and progressive
	// discovery when the tool count exceeds the threshold.
	//
	// windowSize is the per-session context window in tokens (from models.dev /
	// user settings). Pass 0 when the model is unknown — the broker falls back
	// to DefaultContextWindowTokens so behaviour is preserved.
	SelectForAgent(ctx context.Context, sessionID, agentID, userMessage, workspaceID string, windowSize int) (*ToolSelection, error)

	// Execute runs a tool call, routing through ToolClient (with permission
	// checks) when available, falling back to direct MCPManager execution.
	Execute(ctx context.Context, agentID, toolName string, input map[string]any) (*ToolResult, error)

	// HandleRequestTools processes a request_tools meta-tool call for
	// progressive discovery. Returns newly-discovered tool definitions and
	// a human-readable summary string.
	HandleRequestTools(ctx context.Context, input map[string]any) ([]provider.ToolDefinition, string, error)

	// ListSummaries returns lightweight name+description pairs for all
	// registered tools (no full schemas).
	ListSummaries() []toolclient.ToolSummary

	// GetToolMeta returns safety metadata for a tool. Returns false if the
	// tool is not found in the registry. Used by the permission engine.
	GetToolMeta(toolName string) (ToolMetaInfo, bool)

	// GetToolSchema returns the InputSchema for a named tool, or nil if the
	// tool has no schema or is not found. Used by arg validation at execute time.
	GetToolSchema(toolName string) map[string]any
}

// ToolMetaInfo carries safety metadata for a tool, used by the permission
// engine and the parallel tool executor.
type ToolMetaInfo struct {
	IsReadOnly        bool
	IsDestructive     bool
	IsConcurrencySafe bool // safe to run in parallel with other tools
	MaxIterations     int  // 0 = no per-tool limit
}

// ProgressiveDiscoveryThreshold is the MCP tool count above which
// progressive discovery is activated.
const ProgressiveDiscoveryThreshold = 10

// BrokerDecisionLogger logs tool selection decisions for debugging.
type BrokerDecisionLogger interface {
	LogBrokerDecision(sessionID, intent, layerReached string, selectedTools []string, signals string) error
}

// BrokerDecisionExLogger is the Phase 5 / D3 (CW-20260419-0011) extension.
// Implementations record every request_tools call (intent + outcome +
// consecutive_empty + total_calls + reflection_query). *store.Store
// satisfies it via LogBrokerDecisionEx — the adapter lives here so the
// service layer doesn't depend on the store package's record shape.
type BrokerDecisionExLogger interface {
	BrokerDecisionLogger
	LogBrokerDecisionEx(e store.BrokerDecisionEntry) error
}

// toolServiceImpl is the concrete implementation of ToolService.
type toolServiceImpl struct {
	toolClient     *toolclient.ToolClient
	mcpManager     *mcp.Manager
	agents         AgentReader
	decisionLogger BrokerDecisionLogger

	// repairConfig wires the C2 LLM-augmented repair pipeline
	// (CW-20260429-0008). Nil-safe: when unset (or when the cost gates
	// reject the call) Execute returns the C1 structured envelope
	// directly and never spends a Haiku call.
	repairConfig *RepairConfig

	// transportHook lets tests replace the transport hop with a stub.
	// Production code leaves it nil; the default callTransport then
	// dispatches to s.toolClient or s.mcpManager.
	transportHook func(ctx context.Context, agentID, toolName string, input map[string]any) (string, error)
}

// RepairConfig holds the wiring for the C2 LLM repair pipeline. It is
// injected by the container; nil-safe.
//
// The struct is intentionally tiny — the orchestration logic lives in
// Execute() and the LLM primitive in internal/recover. Callers that
// want to disable repair entirely can leave repairConfig nil OR set
// NANITE_AUTO_REPAIR=false in the environment OR persist
// auto_repair_pref="never" in user_settings.
type RepairConfig struct {
	// Provider is the LLM provider used for repair calls. Typically
	// the same provider as the user's default chat provider, resolved
	// via *provider.Registry at container build.
	Provider provider.Provider

	// Model is the repair model name (default DefaultRepairModel
	// from internal/recover when empty).
	Model string

	// Timeout bounds a single repair LLM call. Default
	// recoverpkg.DefaultRepairTimeout when zero.
	Timeout time.Duration

	// SettingsReader returns the current user settings so the
	// auto_repair_pref gate can be checked at call time. Nil-safe —
	// when nil the gate is "always".
	SettingsReader UserSettingsReader
}

// UserSettingsReader is the narrow surface RepairConfig needs to read
// the auto_repair_pref column. *store.Store satisfies it.
type UserSettingsReader interface {
	GetUserSettings() (*store.UserSettings, error)
}

// NewToolService creates a ToolService. Both toolClient and mcpManager may be
// nil — Execute will return an error if neither is available.
func NewToolService(tc *toolclient.ToolClient, mcpMgr *mcp.Manager, agents AgentReader) ToolService {
	return &toolServiceImpl{
		toolClient: tc,
		mcpManager: mcpMgr,
		agents:     agents,
	}
}

// SetDecisionLogger attaches a broker decision logger (typically *store.Store).
func (s *toolServiceImpl) SetDecisionLogger(dl BrokerDecisionLogger) {
	s.decisionLogger = dl
}

// LogRequestToolsCall persists a single request_tools meta-tool call to
// broker_decisions. Called by the chat service via the brokerCallPersister
// interface. Routes through the BrokerDecisionExLogger when available
// (so the new fields land), falling back to the legacy LogBrokerDecision
// when the attached logger is the older shape.
func (s *toolServiceImpl) LogRequestToolsCall(
	sessionID, intent, outcome string,
	consecutiveEmpty, totalCalls, loadedCount int,
	reflectionQuery string,
) {
	if s.decisionLogger == nil || sessionID == "" {
		return
	}
	if ex, ok := s.decisionLogger.(BrokerDecisionExLogger); ok {
		err := ex.LogBrokerDecisionEx(store.BrokerDecisionEntry{
			SessionID:        sessionID,
			Intent:           intent,
			LayerReached:     "request_tools",
			Outcome:          outcome,
			ConsecutiveEmpty: consecutiveEmpty,
			TotalCalls:       totalCalls,
			LoadedCount:      loadedCount,
			ReflectionQuery:  reflectionQuery,
		})
		if err != nil {
			slog.Warn("service/tool: failed to persist request_tools call (ex)", "err", err)
		}
		return
	}
	// Legacy fallback — at least the row lands so debug panels see it.
	if err := s.decisionLogger.LogBrokerDecision(sessionID, intent, "request_tools", nil, outcome); err != nil {
		slog.Warn("service/tool: failed to persist request_tools call", "err", err)
	}
}

// SetRepairConfig attaches the C2 repair pipeline wiring. Nil-safe —
// pass nil to disable repair entirely (the env-var and user-pref gates
// also disable it independently).
func (s *toolServiceImpl) SetRepairConfig(rc *RepairConfig) {
	s.repairConfig = rc
}

// SelectForAgent implements ToolService.
func (s *toolServiceImpl) SelectForAgent(ctx context.Context, sessionID, agentID, userMessage, workspaceID string, windowSize int) (*ToolSelection, error) {
	intent, hints := extractIntent(userMessage)
	slog.Debug("service/tool: extracted intent", "intent", intent, "hints", hints)

	// Collect tools via broker selection.
	var allTools []provider.ToolDefinition
	var overrideBlock string
	seen := map[string]bool{} // dedup: Anthropic API rejects duplicate tool names

	if s.toolClient != nil {
		res, err := s.toolClient.SelectToolsAsProvider(ctx, intent, hints, workspaceID, agentID, windowSize)
		if err != nil {
			slog.Warn("service/tool: broker selection failed — falling back to MCP manager", "err", err)
		} else {
			overrideBlock = res.OverrideBlock
			for _, t := range res.Tools {
				if !seen[t.Name] {
					seen[t.Name] = true
					allTools = append(allTools, t)
				}
			}
		}
		// Phase 5 / D3 (CW-20260419-0011): when the toolclient has skills
		// or a memory recaller wired, run the reasoning-augmented signal
		// pass to capture diagnostic state (logged + persisted). The
		// signal pass returns the same tool universe as SelectToolsAsProvider
		// for the time being — it informs ranking, not surface composition,
		// because the agent permission filter downstream of this path
		// expects provider.ToolDefinition output. Future work: pass the
		// augmented order into provider conversion so the LLM receives
		// skills-prioritised tools first. (See follow-ups in the ADR-003
		// "Limitations" section.)
		if s.toolClient.MemoryRecaller() != nil || len(s.toolClient.Skills()) > 0 {
			if _, _, signals, err := s.toolClient.SelectToolsAugmented(ctx, intent, hints, workspaceID, agentID, windowSize); err == nil {
				s.logDecisionWithSignals(sessionID, intent, "augmented", allTools, signals)
			}
		}
	}

	// If no MCP tools from the broker, try direct discovery from agent's configured servers.
	mcpCount := countMCPOriginTools(s.toolClient, allTools)
	if mcpCount == 0 && s.mcpManager != nil && s.agents != nil {
		agent, err := s.agents.GetAgent(agentID)
		if err == nil {
			allTools, seen = s.discoverAgentMCPTools(ctx, agent.MCPServers, allTools, seen)
		}
	}

	// Apply agent tools allowlist (schema v2).
	if s.agents != nil {
		if agent, err := s.agents.GetAgent(agentID); err == nil {
			allTools = filterToolsByAllowlist(allTools, agent.Tools)
		}
	}

	if len(allTools) == 0 {
		slog.Warn("service/tool: 0 tools for agent — proceeding without tools", "agent", agentID)
	} else {
		slog.Info("service/tool: selected tools for agent", "count", len(allTools), "agent", agentID)
	}

	// Check if progressive discovery should be used. With internalization
	// (ADR-002) the agent-facing surface is uniform; we identify MCP-origin
	// tools by asking the toolclient which names are NOT registered as
	// builtins. The `mcp__` prefix is no longer emitted on the agent surface.
	mcpToolCount := countMCPOriginTools(s.toolClient, allTools)
	if mcpToolCount > ProgressiveDiscoveryThreshold && s.toolClient != nil {
		summaries := s.toolClient.ListToolSummaries()
		catalog := chat.BuildToolCatalog(summaries)

		// Keep builtin tools alongside request_tools meta-tool.
		builtinTools := []provider.ToolDefinition{toolclient.RequestToolsMetaTool()}
		for _, t := range allTools {
			if s.toolClient.IsBuiltinTool(t.Name) {
				builtinTools = append(builtinTools, t)
			}
		}

		slog.Info("service/tool: progressive discovery active",
			"mcp_tools", mcpToolCount, "builtins", len(builtinTools)-1, "catalog_entries", len(summaries))

		s.logDecision(sessionID, intent, "progressive", builtinTools)
		return &ToolSelection{
			Tools:       builtinTools,
			Catalog:     catalog,
			Progressive: true,
		}, nil
	}

	layer := "broker"
	if len(allTools) == 0 {
		layer = "empty"
	}
	s.logDecision(sessionID, intent, layer, allTools)
	return &ToolSelection{Tools: allTools, OverrideBlock: overrideBlock}, nil
}

// logDecision persists the broker selection decision for the debug panel.
func (s *toolServiceImpl) logDecision(sessionID, intent, layer string, tools []provider.ToolDefinition) {
	s.logDecisionWithSignals(sessionID, intent, layer, tools, "")
}

// logDecisionWithSignals is logDecision plus the diagnostic signals JSON.
// Phase 5 / D3 routes the reasoning-augmented selection signals here.
func (s *toolServiceImpl) logDecisionWithSignals(sessionID, intent, layer string, tools []provider.ToolDefinition, signals string) {
	if s.decisionLogger == nil || sessionID == "" {
		return
	}
	names := make([]string, len(tools))
	for i, t := range tools {
		names[i] = t.Name
	}
	if err := s.decisionLogger.LogBrokerDecision(sessionID, intent, layer, names, signals); err != nil {
		slog.Warn("service/tool: failed to log broker decision", "err", err)
	}
}

// Execute implements ToolService. Unified execution path: ToolClient (with
// permissions) → MCPManager fallback → error.
//
// Errors returned by the underlying tool transport are passed through the
// recover taxonomy (CW-20260429-0007 / C1, layer 3 of the
// self_healing_tool_surface_lens). Recoverable errors are tagged, logged
// at INFO with structured fields, and surfaced to the agent as a JSON
// envelope so even without C2's auto-repair the agent has actionable
// feedback (kind, reason, suggestion, schema_uri, path). Non-recoverable
// errors flow through unchanged with the same `Error: <prose>` shape they
// always had.
//
// CW-20260429-0008 (C2): when a recoverable error is detected and the
// cost gates pass, the harness dispatches a Haiku-class LLM to reshape
// the args, retries the tool ONCE, and either returns the success
// result wrapped with a `repair_note` (so the agent learns) or, on
// retry failure, returns the ORIGINAL C1 envelope unchanged.
//
// Iteration cap is hard at 1: there is no nested repair on retry
// failure. The repair pipeline is bypassed entirely when:
//   - NANITE_AUTO_REPAIR=false in the environment, OR
//   - user_settings.auto_repair_pref = "never", OR
//   - no RepairConfig has been wired on the service.
func (s *toolServiceImpl) Execute(ctx context.Context, agentID, toolName string, input map[string]any) (*ToolResult, error) {
	output, callErr := s.callTransport(ctx, agentID, toolName, input)
	if callErr == nil {
		return &ToolResult{Output: output}, nil
	}
	if errors.Is(callErr, errNoTransport) {
		return &ToolResult{
			Output:  "Error: no tool client or MCP manager configured",
			IsError: true,
		}, nil
	}

	// On a recoverable error, attempt the C2 LLM repair pipeline before
	// surfacing the C1 envelope. attemptRepair returns the C1 envelope
	// itself when the gates reject, when no repair was possible, or when
	// the retry failed.
	return s.attemptRepair(ctx, agentID, toolName, input, callErr), nil
}

// errNoTransport is returned by callTransport when neither the
// toolclient nor the MCP manager is configured. Sentinel — never
// surfaced to the agent.
var errNoTransport = errors.New("no tool transport")

// callTransport is the single transport hop. Returns (raw output, nil)
// on success; (zero, error) on transport error; (zero, errNoTransport)
// when neither route is wired.
func (s *toolServiceImpl) callTransport(ctx context.Context, agentID, toolName string, input map[string]any) (string, error) {
	if s.transportHook != nil {
		return s.transportHook(ctx, agentID, toolName, input)
	}
	if s.toolClient != nil {
		return s.toolClient.CallTool(ctx, agentID, toolName, input)
	}
	if s.mcpManager != nil {
		return s.mcpManager.ExecuteTool(ctx, toolName, input)
	}
	return "", errNoTransport
}

// attemptRepair is the C2 orchestration for a single recoverable error.
// It returns the ToolResult the agent will see — either the repaired
// success (wrapped with repair_note), the C1 envelope on bypass /
// failure, or a missing-required envelope when the LLM declined to
// fabricate values.
//
// IMPORTANT: iteration cap = 1. If the retry fails we return the
// ORIGINAL C1 envelope (no nested repair, no compounded errors).
func (s *toolServiceImpl) attemptRepair(ctx context.Context, agentID, toolName string, input map[string]any, origErr error) *ToolResult {
	kind := recoverpkg.Classify(origErr)
	if !kind.IsRecoverable() {
		return &ToolResult{Output: fmt.Sprintf("Error: %v", origErr), IsError: true}
	}
	wrapped := recoverpkg.Wrap(origErr, toolName, input)
	var rec *recoverpkg.RecoverableError
	if !errors.As(wrapped, &rec) || rec == nil {
		return &ToolResult{Output: fmt.Sprintf("Error: %v", origErr), IsError: true}
	}

	slog.Info("recoverable tool error classified",
		"kind", rec.Kind.String(),
		"tool", rec.ToolName,
		"path", rec.ErrorPath,
		"reason", rec.ErrorReason,
		"schema_uri", rec.SchemaURI,
	)

	// Gate 1: env var bypass (operator-level kill switch).
	if !autoRepairEnvEnabled() {
		return &ToolResult{Output: buildAgentErrorEnvelope(rec), IsError: true}
	}
	// Gate 2: repair pipeline must be wired.
	if s.repairConfig == nil || s.repairConfig.Provider == nil {
		return &ToolResult{Output: buildAgentErrorEnvelope(rec), IsError: true}
	}
	// Gate 3: user pref (auto_repair_pref). "never" disables.
	if s.repairConfig.SettingsReader != nil {
		us, err := s.repairConfig.SettingsReader.GetUserSettings()
		if err == nil && us != nil && us.AutoRepairPref == "never" {
			return &ToolResult{Output: buildAgentErrorEnvelope(rec), IsError: true}
		}
	}

	// Run the repair LLM call. Measure elapsed time around the call so
	// we get useful telemetry on the failure path too — timeouts and
	// transport errors are exactly when latency tells us something.
	repairStart := time.Now()
	outcome, repairErr := recoverpkg.Repair(ctx, rec, recoverpkg.RepairOptions{
		Provider:       s.repairConfig.Provider,
		Model:          s.repairConfig.Model,
		Timeout:        s.repairConfig.Timeout,
		SchemaProvider: s,
	})
	if repairErr != nil {
		slog.Info("auto_repair attempt",
			"kind", rec.Kind.String(),
			"tool", rec.ToolName,
			"repair_success", false,
			"repair_latency_ms", time.Since(repairStart).Milliseconds(),
			"retry_success", false,
			"err", repairErr.Error(),
		)
		// Timeout / transport / parse failure → fall through to C1.
		return &ToolResult{Output: buildAgentErrorEnvelope(rec), IsError: true}
	}

	// Missing-required path: return a structural envelope explicitly
	// listing the missing fields. No retry.
	if !outcome.HasRepair() {
		slog.Info("auto_repair attempt",
			"kind", rec.Kind.String(),
			"tool", rec.ToolName,
			"repair_success", false,
			"repair_latency_ms", outcome.LatencyMS,
			"retry_success", false,
			"missing_required", outcome.MissingRequired,
		)
		return &ToolResult{Output: buildMissingRequiredEnvelope(rec, outcome), IsError: true}
	}

	// Single retry — iteration cap = 1, hard.
	output, retryErr := s.callTransport(ctx, agentID, toolName, outcome.RepairedArgs)
	if retryErr != nil {
		slog.Info("auto_repair attempt",
			"kind", rec.Kind.String(),
			"tool", rec.ToolName,
			"repair_success", true,
			"repair_latency_ms", outcome.LatencyMS,
			"retry_success", false,
			"retry_err", retryErr.Error(),
		)
		// Compound-error rule: surface the ORIGINAL C1 envelope, not
		// a fresh classification of the retry error. The agent already
		// received a structural-error fingerprint on the first call;
		// reclassifying the retry would muddy the signal.
		return &ToolResult{Output: buildAgentErrorEnvelope(rec), IsError: true}
	}

	slog.Info("auto_repair attempt",
		"kind", rec.Kind.String(),
		"tool", rec.ToolName,
		"repair_success", true,
		"repair_latency_ms", outcome.LatencyMS,
		"retry_success", true,
	)
	return &ToolResult{Output: wrapWithRepairNote(output, rec, input, outcome), IsError: false}
}

// autoRepairEnvEnabled returns true unless NANITE_AUTO_REPAIR is set
// to a falsy value ("0", "false", "no", "off", case-insensitive). The
// default — when the env var is unset or set to anything else — is
// "enabled". This matches the ticket's "default always" behaviour.
func autoRepairEnvEnabled() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("NANITE_AUTO_REPAIR")))
	switch v {
	case "0", "false", "no", "off":
		return false
	}
	return true
}

// classifyAndFormatToolError runs the C1 recover.Classify pipeline over a
// tool-transport error and produces the agent-facing ToolResult.
//
// On a recoverable kind it:
//   - logs an INFO "recoverable tool error classified" entry with the
//     structured fields (kind, tool, path, reason, schema_uri),
//   - returns a ToolResult whose Output is the JSON envelope shape the
//     agent reads to choose its next call.
//
// On KindNone it preserves the legacy `Error: <prose>` output verbatim
// so the byte-stable contract that existing agents (and tests) expect
// is not broken by the classification layer.
func classifyAndFormatToolError(err error, toolName string, input map[string]any) *ToolResult {
	kind := recoverpkg.Classify(err)
	if !kind.IsRecoverable() {
		return &ToolResult{Output: fmt.Sprintf("Error: %v", err), IsError: true}
	}

	wrapped := recoverpkg.Wrap(err, toolName, input)
	var rec *recoverpkg.RecoverableError
	if !errors.As(wrapped, &rec) || rec == nil {
		// Defensive: Wrap returned a recoverable kind from Classify but
		// did not produce the expected wrapper. Fall back to the prose
		// shape rather than dropping the error.
		return &ToolResult{Output: fmt.Sprintf("Error: %v", err), IsError: true}
	}

	slog.Info("recoverable tool error classified",
		"kind", rec.Kind.String(),
		"tool", rec.ToolName,
		"path", rec.ErrorPath,
		"reason", rec.ErrorReason,
		"schema_uri", rec.SchemaURI,
	)

	envelope := buildAgentErrorEnvelope(rec)
	return &ToolResult{Output: envelope, IsError: true}
}

// buildAgentErrorEnvelope renders the agent-facing JSON shape for a
// classified recoverable error. The shape is intentionally conservative
// — kind / reason / suggestion / schema_uri / path / tool — because C2's
// auto-repair pass and the future Vanta learning hint both key off this
// payload.
//
// On marshalling failure (which would be a programmer bug since all
// fields are JSON-friendly) the function falls back to the rec.Error()
// string — the agent still gets the kind tag and reason.
func buildAgentErrorEnvelope(rec *recoverpkg.RecoverableError) string {
	payload := map[string]any{
		"recoverable_error": true,
		"kind":              rec.Kind.String(),
		"tool":              rec.ToolName,
	}
	if rec.ErrorReason != "" {
		payload["reason"] = rec.ErrorReason
	}
	if rec.Suggestion != "" {
		payload["suggestion"] = rec.Suggestion
	}
	if rec.ErrorPath != "" {
		payload["path"] = rec.ErrorPath
	}
	if rec.SchemaURI != "" {
		payload["schema_uri"] = rec.SchemaURI
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		// Should be unreachable — every value is a string or bool — but
		// keep a sane fallback rather than panicking on the LLM-facing
		// path.
		return "Error: " + rec.Error()
	}
	return string(raw)
}

// buildMissingRequiredEnvelope renders the agent-facing JSON shape when
// the C2 repair LLM declined to fabricate a value for a required field.
// The shape extends the C1 envelope with `missing_required` (the field
// list) and `lesson_hint` (an explainer the agent can persist via
// nanite_remember once D1 ships). repaired_args is intentionally absent
// — the contract is "no fabrication".
func buildMissingRequiredEnvelope(rec *recoverpkg.RecoverableError, outcome *recoverpkg.RepairOutcome) string {
	payload := map[string]any{
		"recoverable_error": true,
		"kind":              rec.Kind.String(),
		"tool":              rec.ToolName,
		"missing_required":  outcome.MissingRequired,
	}
	if outcome.LessonHint != "" {
		payload["lesson_hint"] = outcome.LessonHint
	}
	if rec.ErrorReason != "" {
		payload["reason"] = rec.ErrorReason
	}
	if rec.ErrorPath != "" {
		payload["path"] = rec.ErrorPath
	}
	if rec.SchemaURI != "" {
		payload["schema_uri"] = rec.SchemaURI
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return buildAgentErrorEnvelope(rec)
	}
	return string(raw)
}

// wrapWithRepairNote prepends the original tool result with a
// `repair_note` block so the calling agent sees that its input was
// reshaped on its behalf. The shape is:
//
//	{"repair_note": {original_args, repaired_args, lesson_hint, kind, path, tool}, "result": <orig>}
//
// We embed the raw tool result as a JSON value when it parses as JSON;
// otherwise we surface it as a string under `result_text`. This keeps
// the JSON envelope self-describing even for tools that return prose.
//
// Note: the calling agent's system prompt (see migration 047) tells it
// to read repair_note.lesson_hint and persist via nanite_remember when
// that tool is available.
func wrapWithRepairNote(toolOutput string, rec *recoverpkg.RecoverableError, originalArgs map[string]any, outcome *recoverpkg.RepairOutcome) string {
	note := map[string]any{
		"original_args": originalArgs,
		"repaired_args": outcome.RepairedArgs,
		"lesson_hint":   outcome.LessonHint,
		"kind":          rec.Kind.String(),
		"tool":          rec.ToolName,
	}
	if rec.ErrorPath != "" {
		note["path"] = rec.ErrorPath
	}
	payload := map[string]any{
		"repair_note": note,
	}
	// Try to embed the tool result as a JSON value; if it doesn't
	// parse, fall back to a string field.
	var resultJSON any
	if err := json.Unmarshal([]byte(toolOutput), &resultJSON); err == nil {
		payload["result"] = resultJSON
	} else {
		payload["result_text"] = toolOutput
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		// Defensive: fall back to the raw tool output — the agent
		// will at least see the success result, just without the
		// learning signal.
		return toolOutput
	}
	return string(raw)
}

// HandleRequestTools implements ToolService.
func (s *toolServiceImpl) HandleRequestTools(_ context.Context, input map[string]any) ([]provider.ToolDefinition, string, error) {
	if s.toolClient == nil {
		return nil, "No tool client configured.", fmt.Errorf("no tool client configured")
	}
	tools, summary := s.toolClient.HandleRequestTools(input)
	return tools, summary, nil
}

// ListSummaries implements ToolService.
func (s *toolServiceImpl) ListSummaries() []toolclient.ToolSummary {
	if s.toolClient == nil {
		return nil
	}
	return s.toolClient.ListToolSummaries()
}

// GetToolMeta implements ToolService. Returns safety metadata for a tool.
// Uses name-based heuristics consistent with internal/tool/adapt.go patterns.
// Concurrency safety: read/search/fetch tools are safe; write/edit/bash are not.
func (s *toolServiceImpl) GetToolMeta(toolName string) (ToolMetaInfo, bool) {
	meta := ToolMetaInfo{}

	// Read-only tools.
	switch {
	case strings.HasSuffix(toolName, "_read") || strings.HasSuffix(toolName, "_glob") ||
		strings.HasSuffix(toolName, "_grep") || strings.HasSuffix(toolName, "_search") ||
		strings.HasSuffix(toolName, "_list") || strings.HasSuffix(toolName, "_get"):
		meta.IsReadOnly = true
	case strings.Contains(toolName, "web_fetch") || strings.Contains(toolName, "web_search"):
		meta.IsReadOnly = true
	}

	// Destructive tools.
	switch {
	case strings.Contains(toolName, "delete") || strings.Contains(toolName, "remove"):
		meta.IsDestructive = true
	case strings.Contains(toolName, "shell") || strings.Contains(toolName, "bash"):
		meta.IsDestructive = true // shell commands may be destructive
	case strings.Contains(toolName, "drop") || strings.Contains(toolName, "reset"):
		meta.IsDestructive = true
	}

	// Concurrency safety — mirrors internal/tool/adapt.go:inferSafetyOptions.
	// Read-only tools are concurrent-safe; write/edit/bash/destructive are not.
	switch {
	case meta.IsReadOnly:
		meta.IsConcurrencySafe = true
	case strings.Contains(toolName, "json_parse") || strings.Contains(toolName, "datetime") ||
		strings.Contains(toolName, "hash") || strings.Contains(toolName, "uuid"):
		meta.IsConcurrencySafe = true // pure utility tools
	default:
		meta.IsConcurrencySafe = false
	}

	return meta, true
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

// discoverAgentMCPTools performs direct MCP discovery from an agent's
// configured server list. Returns the updated tool slice and seen map.
// Tool names are uniform (ADR-002); collisions are resolved by the
// caller's broker / Manager layer, not here.
func (s *toolServiceImpl) discoverAgentMCPTools(
	ctx context.Context,
	mcpServersJSON string,
	allTools []provider.ToolDefinition,
	seen map[string]bool,
) ([]provider.ToolDefinition, map[string]bool) {
	var servers []string
	// Silently ignore bad JSON — matches existing engine behaviour.
	_ = parseJSONStrings(mcpServersJSON, &servers)

	beforeCount := countMCPOriginTools(s.toolClient, allTools)
	for _, srv := range servers {
		srvTools, err := s.mcpManager.DiscoverServerTools(ctx, srv)
		if err != nil {
			continue
		}
		for _, t := range srvTools {
			// Use the canonical uniform name (no `mcp__server__` prefix).
			name := mcp.UniformToolName(srv, t.Name)
			if name == "" || seen[name] {
				continue
			}
			seen[name] = true
			allTools = append(allTools, provider.ToolDefinition{
				Name:        name,
				Description: t.Description,
				InputSchema: t.InputSchema,
			})
		}
	}
	afterCount := countMCPOriginTools(s.toolClient, allTools)
	if afterCount > beforeCount {
		slog.Info("service/tool: direct MCP discovery added tools from configured servers", "added", afterCount-beforeCount)
	}
	return allTools, seen
}

// countMCPOriginTools counts tools that did NOT come from the builtin
// registry — i.e. those that originated from an MCP server. With ADR-002
// the agent-facing surface is uniform; we no longer have a name-prefix
// signal to count by, so we ask the toolclient to classify each name.
func countMCPOriginTools(tc *toolclient.ToolClient, tools []provider.ToolDefinition) int {
	if tc == nil {
		// Without a toolclient we cannot distinguish; treat all as MCP-origin
		// to preserve the historical behaviour of triggering progressive
		// discovery when the manager exposes a large tool surface.
		return len(tools)
	}
	n := 0
	for _, t := range tools {
		if !tc.IsBuiltinTool(t.Name) {
			n++
		}
	}
	return n
}

// filterToolsByAllowlist removes tools not in the agent's tools allowlist.
// An empty or "[]" allowlist means no filtering.
func filterToolsByAllowlist(tools []provider.ToolDefinition, allowlistJSON string) []provider.ToolDefinition {
	if allowlistJSON == "" || allowlistJSON == "[]" {
		return tools
	}
	var allowlist []string
	if err := parseJSONStrings(allowlistJSON, &allowlist); err != nil || len(allowlist) == 0 {
		return tools
	}
	filtered := make([]provider.ToolDefinition, 0, len(tools))
	for _, t := range tools {
		for _, pattern := range allowlist {
			if toolclient.MatchPattern(pattern, t.Name) {
				filtered = append(filtered, t)
				break
			}
		}
	}
	slog.Debug("service/tool: allowlist filtered", "before", len(tools), "after", len(filtered))
	return filtered
}

// extractIntent derives an intent string and keyword hints from a user message.
// Mirrors chat.ExtractIntent logic — placed here so the service layer doesn't
// depend on the chat package.
func extractIntent(userMessage string) (intent string, hints []string) {
	msg := strings.ToLower(userMessage)
	for _, ch := range []string{",", ".", "!", "?", ";", ":", "'", "\"", "(", ")", "[", "]", "{", "}", "\n", "\t"} {
		msg = strings.ReplaceAll(msg, ch, " ")
	}

	words := strings.Fields(msg)
	seen := make(map[string]bool)
	var keywords []string

	for _, w := range words {
		if len(w) < 3 {
			continue
		}
		if intentStopWords[w] {
			continue
		}
		if seen[w] {
			continue
		}
		seen[w] = true
		keywords = append(keywords, w)
		if len(keywords) >= 10 {
			break
		}
	}

	if len(keywords) == 0 {
		return "general", nil
	}

	intentWords := keywords
	if len(intentWords) > 3 {
		intentWords = intentWords[:3]
	}
	return strings.Join(intentWords, " "), keywords
}

// intentStopWords mirrors the set from chat.ExtractIntent.
var intentStopWords = map[string]bool{
	"the": true, "and": true, "for": true, "with": true,
	"that": true, "this": true, "from": true, "have": true,
	"has": true, "not": true, "but": true, "are": true,
	"was": true, "were": true, "been": true, "can": true,
	"could": true, "would": true, "should": true, "will": true,
	"does": true, "did": true, "its": true, "they": true,
	"them": true, "their": true, "there": true, "what": true,
	"when": true, "where": true, "which": true, "who": true,
	"how": true, "all": true, "each": true, "every": true,
	"any": true, "some": true, "more": true, "most": true,
	"also": true, "than": true, "then": true, "just": true,
	"about": true, "into": true, "over": true, "such": true,
	"please": true, "want": true, "need": true, "like": true,
	"know": true, "think": true, "make": true, "use": true,
	"using": true, "help": true, "show": true, "tell": true,
}

// GetToolSchema implements ToolService.
func (s *toolServiceImpl) GetToolSchema(toolName string) map[string]any {
	if s.toolClient != nil {
		for _, t := range s.toolClient.ListTools() {
			if t.Name == toolName {
				return t.InputSchema
			}
		}
	}
	return nil
}

// parseJSONStrings is a tiny helper to unmarshal a JSON string array.
func parseJSONStrings(raw string, out *[]string) error {
	if raw == "" {
		return nil
	}
	return json.Unmarshal([]byte(raw), out)
}
