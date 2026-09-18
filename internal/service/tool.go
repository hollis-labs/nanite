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

	llmcontracts "github.com/hollis-labs/go-llm-contracts"
	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/describer"
	"github.com/hollis-labs/nanite/internal/dispatch"
	"github.com/hollis-labs/nanite/internal/mcp"
	recoverpkg "github.com/hollis-labs/nanite/internal/recover"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/toolclient"
)

// chatRoleAgentSlug is the canonical slug of the chat-role agent profile
// (internal/agent/builtin/default.md). Surface filtering keys off this
// slug — Worker, Planner, executor, hint-selector and mux-orchestrator
// profiles use distinct slugs and bypass the chat-surface filter.
const chatRoleAgentSlug = "default"

// ToolSelection holds the result of tool selection, including progressive
// discovery metadata. Mirrors chat.toolSelection but is owned by the service layer.
type ToolSelection struct {
	Tools       []llmtypes.ToolDefinition // tools to send to the LLM
	Catalog     string                    // non-empty when progressive discovery is active
	Progressive bool                      // true when using progressive discovery
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
	// to DefaultContextWindowTokens so behavior is preserved.
	SelectForAgent(ctx context.Context, sessionID, agentID, userMessage, workspaceID string, windowSize int) (*ToolSelection, error)

	// Execute runs a tool call, routing through ToolClient (with permission
	// checks) when available, falling back to direct MCPManager execution.
	Execute(ctx context.Context, agentID, toolName string, input map[string]any) (*ToolResult, error)

	// HandleRequestTools processes a request_tools meta-tool call for
	// progressive discovery. Returns newly-discovered tool definitions and
	// a human-readable summary string.
	HandleRequestTools(ctx context.Context, agentID string, input map[string]any) ([]llmtypes.ToolDefinition, string, error)

	// ListSummaries returns lightweight name+description pairs for all
	// registered tools (no full schemas).
	ListSummaries() []toolclient.ToolSummary

	// GetToolMeta returns safety metadata for a tool. Returns false if the
	// tool is not found in the registry. Used by the permission engine.
	//
	// ctx is used to read the tool's declared concurrency-safety
	// classification from the known_tools catalog (TASKS/phase-4/07-tool-
	// concurrency-safety-classification.md) -- see the implementation's
	// doc comment for the full account.
	GetToolMeta(ctx context.Context, toolName string) (ToolMetaInfo, bool)

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

// toolServiceImpl is the concrete implementation of ToolService.
type toolServiceImpl struct {
	toolClient *toolclient.ToolClient
	mcpManager *mcp.Manager
	agents     AgentReader

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
	Provider llmcontracts.Provider

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
	GetUserSettings(ctx context.Context) (*store.UserSettings, error)
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

	// Collect tools via catalog selection.
	var allTools []llmtypes.ToolDefinition
	seen := map[string]bool{} // dedup: Anthropic API rejects duplicate tool names

	if s.toolClient != nil {
		res, err := s.toolClient.SelectToolsAsProvider(ctx, intent, hints, workspaceID, agentID)
		if err != nil {
			slog.Warn("service/tool: catalog selection failed — falling back to MCP manager", "err", err)
		} else {
			for _, t := range res.Tools {
				if !seen[t.Name] {
					seen[t.Name] = true
					allTools = append(allTools, t)
				}
			}
		}
	}

	// If no MCP tools from the broker, try direct discovery from agent's configured servers.
	mcpCount := countMCPOriginTools(s.toolClient, allTools)
	if mcpCount == 0 && s.mcpManager != nil && s.agents != nil {
		agent, err := s.agents.GetAgent(ctx, agentID)
		if err == nil {
			allTools, seen = s.discoverAgentMCPTools(ctx, agent.MCPServers, allTools, seen)
		}
	}

	// Resolve the agent's real agent_profiles row once, if any -- used by
	// the agent_tools grant filter below, dispatch-allowlist parsing, and
	// the chat-surface filter. Every agent (including the compiled-in
	// builtin profiles) is a real agent_profiles row with a real ID by the
	// time any selection runs (TASKS/adhoc/01-eliminate-file-based-agent-
	// runtime.md), so a miss (err != nil) here means agentID itself does
	// not resolve to a known agent, not "this population needs a
	// different permission model."
	var callerSlug string
	var callerDispatchAllowlist []string
	var dbAgent *store.AgentProfile
	if s.agents != nil {
		if agent, err := s.agents.GetAgent(ctx, agentID); err == nil {
			dbAgent = agent
		}
	}

	// agent_tools (through known_tools) is the sole roster-membership
	// gate, unconditionally, for every agent -- the FK-based replacement
	// for the schema-v2 tools allowlist this used to read
	// (filterToolsByAllowlist(allTools, agent.Tools), retired from this
	// path by TASKS/phase-4/05) and, as of
	// TASKS/adhoc/02-remove-tool-permissions-collapse-to-agent-tools.md,
	// for the legacy tool_permissions/PermissionResolver fallback that
	// used to run here for agentIDs with no real agent_profiles row (only
	// ever file-based agents, eliminated by TASKS/adhoc/01). Called
	// directly against agentID rather than gated on dbAgent != nil --
	// filterToolsByAgentTools' own store lookup degrades to "no grants" for
	// a genuinely unknown agentID, which is the correct fail-closed
	// outcome. This also closes the gap for tools that land in allTools
	// from outside SelectToolsAsProvider's own gate (e.g. the
	// discoverAgentMCPTools fallback above).
	allTools = filterToolsByAgentTools(ctx, s.agentToolsStore(), agentID, allTools)

	// Apply the chat-role surface filter. Only applies when the agent is
	// the chat-role profile (slug "default"). Worker / Planner / executor
	// / hint-selector / mux-orchestrator profiles bypass it — they have
	// their own surface decisions per
	// decisions.nanite.architecture.role_profile_seeding.
	//
	// Phase 2 graduation per executor-handoff design (CW-20260429-0033 / B4):
	// the four lens primitives (tool_describe, tool_validate, lesson_capture,
	// card_show) are filtered out for the chat agent so the multi-step
	// recovery flow stays inside the executor (B3 pilot —
	// internal/executor/envelope_render). See internal/dispatch/chat_surface.go
	// for the canonical exclusion list.
	if dbAgent != nil {
		callerSlug = dbAgent.Slug
		callerDispatchAllowlist = parseParentDispatchAllowlist(dbAgent.ParentDispatchAllowlist)
		if dbAgent.Slug == chatRoleAgentSlug {
			allTools = applyChatSurfaceFilter(allTools, dispatch.DefaultChatToolSurface())
		}
	}

	// Check whether progressive discovery should be used, BEFORE the
	// MaxSelectedTools cap below (CW-20260918-0047). This must run on the
	// full agent_tools/permission/chat-surface-filtered candidate set, not
	// a positionally-truncated one: FinalizeToolSelection's cap is
	// unranked (plain slice truncation on the broker's candidate order),
	// so deciding progressive discovery — and collecting which of the
	// agent's own granted builtin tools survive it — AFTER that cap both
	// undercounts mcpToolCount (real MCP tools already sit past the cap,
	// invisible here) and silently drops any granted builtin tool that
	// sorted past position 15 in that same unranked order, from the
	// progressive-discovery branch's builtin set too. With
	// internalization (ADR-002) the agent-facing surface is uniform; we
	// identify MCP-origin tools by asking the toolclient which names are
	// NOT registered as builtins. The `mcp__` prefix is no longer emitted
	// on the agent surface.
	mcpToolCount := countMCPOriginTools(s.toolClient, allTools)
	if mcpToolCount > ProgressiveDiscoveryThreshold && s.toolClient != nil {
		summaries := s.toolClient.ListToolSummaries()
		catalog := chat.BuildToolCatalog(summaries)

		// Keep builtin tools alongside request_tools meta-tool.
		builtinTools := []llmtypes.ToolDefinition{toolclient.RequestToolsMetaTool()}
		for _, t := range allTools {
			if s.toolClient.IsBuiltinTool(t.Name) {
				builtinTools = append(builtinTools, t)
			}
		}

		// The always_included escape hatch (item 5) must survive
		// progressive discovery's truncated builtin-only surface too --
		// union in anything IsBuiltinTool missed above (e.g.
		// tool_list/tool_describe, which ship via the self MCP server
		// rather than ToolClient's own builtin registry, so the loop
		// above never picks them up).
		always := s.resolveAlwaysIncludedTools(ctx)
		if dbAgent != nil && dbAgent.Slug == chatRoleAgentSlug {
			always = applyChatSurfaceFilter(always, dispatch.DefaultChatToolSurface())
		}
		builtinTools = unionToolsByName(builtinTools, always)

		if s.toolClient != nil {
			caller := describer.CallerAgent{
				ID:                agentID,
				Slug:              callerSlug,
				DispatchAllowlist: callerDispatchAllowlist,
			}
			builtinTools = s.toolClient.RenderDescriptions(ctx, builtinTools, caller)
		}

		slog.Info("service/tool: progressive discovery active",
			"mcp_tools", mcpToolCount, "builtins", len(builtinTools)-1, "catalog_entries", len(summaries))

		return &ToolSelection{
			Tools:       builtinTools,
			Catalog:     catalog,
			Progressive: true,
		}, nil
	}

	// Non-progressive path only below this point: the candidate set is
	// small enough (mcpToolCount <= ProgressiveDiscoveryThreshold) that
	// the MaxSelectedTools cap + token-budget prune is the right
	// mechanism, now that agent_tools/permission filtering and the
	// chat-surface filter are all done (CW-20260815-0011). Applying
	// MaxSelectedTools any earlier — inside broker selection, before this
	// point — could truncate out a tool the agent's own grants above
	// explicitly kept.
	if s.toolClient != nil {
		allTools = s.toolClient.FinalizeToolSelection(allTools, windowSize)
	}

	// known_tools.always_included escape hatch (this task's item 5):
	// request_tools/tool_list/tool_describe must survive selection
	// regardless of agent_tools membership — folded in LAST, after the
	// cap/budget prune, so it can never be silently squeezed out by
	// either. Still respects the chat-surface filter's own deliberate
	// exclusion of tool_describe for the chat-role agent (B4 above)
	// rather than re-adding it.
	if s.toolClient != nil {
		always := s.resolveAlwaysIncludedTools(ctx)
		if dbAgent != nil && dbAgent.Slug == chatRoleAgentSlug {
			always = applyChatSurfaceFilter(always, dispatch.DefaultChatToolSurface())
		}
		allTools = unionToolsByName(allTools, always)
	}

	// Per-call description-render hook (CW-20260512-0105 / SP-20260512-0008
	// W1B). Tools that opted into the Describer registry have their
	// descriptions re-rendered here, with the caller agent's identity +
	// dispatch allowlist threaded through. The DispatchAllowlist is
	// populated from the AgentProfile.ParentDispatchAllowlist JSON column
	// (CW-20260512-0107 W2A) — empty list means the caller has no dispatch
	// permission and the Describer falls back to the baseline description.
	// Static-description tools are unchanged.
	if s.toolClient != nil {
		caller := describer.CallerAgent{
			ID:                agentID,
			Slug:              callerSlug,
			DispatchAllowlist: callerDispatchAllowlist,
		}
		allTools = s.toolClient.RenderDescriptions(ctx, allTools, caller)
	}

	if len(allTools) == 0 {
		slog.Warn("service/tool: 0 tools for agent — proceeding without tools", "agent", agentID)
	} else {
		slog.Info("service/tool: selected tools for agent", "count", len(allTools), "agent", agentID)
	}

	return &ToolSelection{Tools: allTools}, nil
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
		us, err := s.repairConfig.SettingsReader.GetUserSettings(ctx)
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
// "enabled". This matches the ticket's "default always" behavior.
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
// auto-repair pass and the future Tesseract learning hint both key off this
// payload.
//
// On marshaling failure (which would be a programmer bug since all
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
// lesson_capture once D1 ships). repaired_args is intentionally absent
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
// to read repair_note.lesson_hint and persist via lesson_capture when
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
func (s *toolServiceImpl) HandleRequestTools(ctx context.Context, agentID string, input map[string]any) ([]llmtypes.ToolDefinition, string, error) {
	if s.toolClient == nil {
		return nil, "No tool client configured.", fmt.Errorf("no tool client configured")
	}
	tools, summary := s.toolClient.HandleRequestToolsForAgent(ctx, agentID, input)
	caller := describer.CallerAgent{ID: agentID}
	if s.agents != nil {
		agent, err := s.agents.GetAgent(ctx, agentID)
		if err != nil {
			return nil, "Cannot resolve the calling agent for tool discovery.", err
		}
		if agent != nil {
			caller.Slug = agent.Slug
			caller.DispatchAllowlist = parseParentDispatchAllowlist(agent.ParentDispatchAllowlist)
			if agent.Slug == chatRoleAgentSlug {
				filtered := applyChatSurfaceFilter(tools, dispatch.DefaultChatToolSurface())
				if len(filtered) != len(tools) {
					names := make([]string, 0, len(filtered))
					for _, tool := range filtered {
						names = append(names, tool.Name)
					}
					summary = fmt.Sprintf("Loaded tools: %s. Tools excluded from this agent's chat surface were not loaded.", strings.Join(names, ", "))
				}
				tools = filtered
			}
		}
	}
	return s.toolClient.RenderDescriptions(ctx, tools, caller), summary, nil
}

// ListSummaries implements ToolService.
func (s *toolServiceImpl) ListSummaries() []toolclient.ToolSummary {
	if s.toolClient == nil {
		return nil
	}
	return s.toolClient.ListToolSummaries()
}

// GetToolMeta implements ToolService. Returns safety metadata for a tool.
//
// IsReadOnly/IsDestructive use the name-based suffix and substring checks
// below. Those two fields are out of scope for TASKS/phase-4/07-tool-
// concurrency-safety-classification.md, which only covers IsConcurrencySafe
// (see that task file and architecture/03-steering.md's "Two correctness gaps
// carried into implementation").
//
// IsConcurrencySafe reads the tool's DECLARED classification from the
// known_tools catalog (known_tools.concurrency_safe, populated at boot by
// SyncKnownTools from the curated table in tool_concurrency_classification.go
// -- see that file's doc comment for what "declared" means here and why).
// It never inspects toolName. A tool whose known_tools row doesn't exist
// yet, or whose concurrency_safe column is still NULL ("not yet
// classified"), defaults to false -- fail closed, not a name guess. This
// replaces a pure suffix/substring name-heuristic that used to live here;
// TASKS/phase-4/07's Work Log has the audit that found real
// misclassifications under it (e.g. base64_encode, message_inbox,
// tool_describe, and search_tool_result -- the last one because "search"
// was a PREFIX, not a suffix, which the old heuristic could not match at
// all).
func (s *toolServiceImpl) GetToolMeta(ctx context.Context, toolName string) (ToolMetaInfo, bool) {
	meta := ToolMetaInfo{}

	// What the server declared beats what its tool name looks like. The
	// heuristics below are a fallback for tools that annotate nothing; they
	// cannot classify a third-party tool they have never seen, and the way
	// they fail is by calling an unrecognized write "not destructive", which
	// the permission engine then allows without asking.
	if s.mcpManager != nil {
		if readOnly, destructive, ok := s.mcpManager.ToolBehavior(toolName); ok {
			meta.IsReadOnly = readOnly
			meta.IsDestructive = destructive
			return meta, true
		}
	}

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

	meta.IsConcurrencySafe = s.declaredConcurrencySafe(ctx, toolName)

	return meta, true
}

// declaredConcurrencySafe returns the known_tools.concurrency_safe value
// for toolName, or false when no store is wired (chiefly tests), the tool
// has no known_tools row yet, or the row's concurrency_safe column is
// still NULL. Never consults toolName's text -- see GetToolMeta's doc
// comment for the reasoning and the audit that replaced the old heuristic.
func (s *toolServiceImpl) declaredConcurrencySafe(ctx context.Context, toolName string) bool {
	st := s.agentToolsStore()
	if st == nil {
		return false
	}
	row, err := st.GetKnownToolByName(ctx, toolName)
	if err != nil {
		return false
	}
	if row.ConcurrencySafe == nil {
		return false
	}
	return *row.ConcurrencySafe
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
	allTools []llmtypes.ToolDefinition,
	seen map[string]bool,
) ([]llmtypes.ToolDefinition, map[string]bool) {
	var servers []string
	// Silently ignore bad JSON — matches existing engine behavior.
	_ = parseJSONStrings(mcpServersJSON, &servers)

	beforeCount := countMCPOriginTools(s.toolClient, allTools)
	for _, srv := range servers {
		srvTools, err := s.mcpManager.DiscoverServerTools(ctx, srv)
		if err != nil {
			// Say so. An agent scoped to an MCP server that fails discovery
			// here gets an empty tool list and no explanation anywhere — the
			// model then reports "I can't access that tool", which reads as a
			// permissions or configuration problem and is neither. Costing a
			// silent continue one WARN is a good trade.
			slog.Warn("mcp: agent-scoped tool discovery failed — the agent will see none of this server's tools",
				"server", srv, "err", err)
			continue
		}
		if len(srvTools) == 0 {
			slog.Warn("mcp: agent-scoped server advertised no tools",
				"server", srv)
		}
		for _, t := range srvTools {
			// Use the canonical uniform name (no `mcp__server__` prefix).
			name := mcp.UniformToolName(srv, t.Name)
			if name == "" || seen[name] {
				continue
			}
			seen[name] = true
			allTools = append(allTools, llmtypes.ToolDefinition{
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
func countMCPOriginTools(tc *toolclient.ToolClient, tools []llmtypes.ToolDefinition) int {
	if tc == nil {
		// Without a toolclient we cannot distinguish; treat all as MCP-origin
		// to preserve the historical behavior of triggering progressive
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

// applyChatSurfaceFilter narrows the tool list by dropping every tool the
// surface filter excludes. Used only on the chat-role profile per the B4
// Phase 2 graduation (CW-20260429-0033). A nil filter is treated as a
// pass-through so callers can disable filtering without conditionals.
func applyChatSurfaceFilter(tools []llmtypes.ToolDefinition, surface *dispatch.ChatToolSurface) []llmtypes.ToolDefinition {
	if surface == nil || len(tools) == 0 {
		return tools
	}
	before := len(tools)
	filtered := make([]llmtypes.ToolDefinition, 0, before)
	for _, t := range tools {
		if surface.Filter(t.Name) {
			filtered = append(filtered, t)
		}
	}
	if removed := before - len(filtered); removed > 0 {
		slog.Debug("service/tool: chat-surface filter removed tools",
			"before", before, "after", len(filtered), "removed", removed)
	}
	return filtered
}

// parseParentDispatchAllowlist parses the AgentProfile.ParentDispatchAllowlist
// JSON column into a string slice of role slugs. An empty / malformed value
// degrades to a nil slice, which signals "no dispatch permission" to the
// Tool Broker Describer (the baseline task_execute description is rendered).
//
// CW-20260512-0107 (SP-20260512-0008 W2A). Empty "[]" is the migration 059
// column default for every profile that does not own a dispatch role —
// only the chat-role default agent is seeded with a non-empty list. Hot
// path: called once per SelectForAgent invocation, so a small JSON parse
// is cheap. Logs at debug level on malformed JSON so a hand-edited row
// doesn't get silently treated as empty.
func parseParentDispatchAllowlist(raw string) []string {
	if raw == "" || raw == "[]" {
		return nil
	}
	var out []string
	if err := parseJSONStrings(raw, &out); err != nil {
		slog.Debug("service/tool: malformed parent_dispatch_allowlist JSON — treating as empty",
			"raw", raw, "err", err)
		return nil
	}
	return out
}

// filterToolsByAgentTools narrows tools to the set explicitly granted to
// the agent via agent_tools (through known_tools) -- the FK-based
// replacement for the old schema-v2 tools allowlist
// (filterToolsByAllowlist(allTools, agent.Tools), retired from the live
// path by TASKS/phase-4/05-wire-select-for-agent-to-read-agent-tools.md;
// see that task's Work Log for the full deny-semantics decision).
//
// agent_tools is a plain positive-grant join with no glob/deny concept
// (per architecture/01-agent-construction.md) -- a tool not present in
// the agent's granted set is excluded here, full stop. There is no live
// "unrestricted" bypass for an agent with few/zero grants: per that
// task's item 4 decision, agent_tools is the literal, static source of
// truth for roster membership going forward, not a live re-derivation
// of a "no restriction" flag that has no representation anywhere in the
// 04 schema. The known_tools.always_included escape hatch (item 5) is
// folded in separately by the caller (SelectForAgent), NOT inside this
// function, so it survives even when this filter yields zero rows.
//
// st == nil degrades to a no-op (tools pass through unfiltered) -- the
// "machinery not wired" precedent every nil-safe helper in this file
// follows (chiefly tests); production always wires a real *store.Store
// onto ToolClient (cmd/nanite/main.go).
func filterToolsByAgentTools(ctx context.Context, st *store.Store, agentID string, tools []llmtypes.ToolDefinition) []llmtypes.ToolDefinition {
	if st == nil || len(tools) == 0 {
		return tools
	}
	granted, err := st.ListAgentToolNames(ctx, agentID)
	if err != nil {
		slog.Warn("service/tool: agent_tools lookup failed — denying non-escape-hatch tools", "agent", agentID, "err", err)
		return nil
	}
	if len(granted) == 0 {
		slog.Debug("service/tool: zero agent_tools grants", "agent", agentID)
		return nil
	}
	grantedSet := make(map[string]bool, len(granted))
	for _, n := range granted {
		grantedSet[n] = true
	}
	filtered := make([]llmtypes.ToolDefinition, 0, len(tools))
	for _, t := range tools {
		if grantedSet[t.Name] {
			filtered = append(filtered, t)
		}
	}
	slog.Debug("service/tool: agent_tools filtered", "agent", agentID, "before", len(tools), "after", len(filtered))
	return filtered
}

// agentToolsStore returns the *store.Store backing this service's
// ToolClient, or nil when none is wired (chiefly tests) -- see
// filterToolsByAgentTools's and resolveAlwaysIncludedTools' doc comments
// for the resulting nil-safe fallbacks. Production always wires a real
// *store.Store onto ToolClient (cmd/nanite/main.go).
func (s *toolServiceImpl) agentToolsStore() *store.Store {
	if s.toolClient == nil {
		return nil
	}
	return s.toolClient.Store
}

// resolveAlwaysIncludedTools returns the full llmtypes.ToolDefinition for
// every known_tools row flagged always_included=true and status=
// 'available' -- the request_tools/tool_list/tool_describe tool-discovery
// escape hatch (architecture/01-agent-construction.md;
// TASKS/phase-1/04's known_tools.always_included column; item 5 of
// TASKS/phase-4/05). Nil-safe: returns nil when no store-backed
// ToolClient is wired (no known_tools catalog to read).
//
// request_tools has no registration anywhere in the normal builtin/MCP
// catalog — it is synthesized ad hoc by SelectForAgent's progressive-
// discovery branch via toolclient.RequestToolsMetaTool(). This function
// special-cases it the same way so the escape hatch also works OUTSIDE
// progressive discovery. tool_list/tool_describe ship via the self MCP
// server and ARE present in ToolClient.ListTools(), so they're looked up
// there directly.
func (s *toolServiceImpl) resolveAlwaysIncludedTools(ctx context.Context) []llmtypes.ToolDefinition {
	st := s.agentToolsStore()
	if st == nil {
		return nil
	}
	rows, err := st.ListAlwaysIncludedKnownTools(ctx)
	if err != nil {
		slog.Warn("service/tool: list always_included known_tools failed", "err", err)
		return nil
	}
	if len(rows) == 0 {
		return nil
	}
	byName := make(map[string]llmtypes.ToolDefinition, len(rows))
	if s.toolClient != nil {
		for _, t := range s.toolClient.ListTools() {
			byName[t.Name] = t
		}
	}
	out := make([]llmtypes.ToolDefinition, 0, len(rows))
	for _, kt := range rows {
		if kt.Status != "available" {
			continue
		}
		if def, ok := byName[kt.Name]; ok {
			out = append(out, def)
			continue
		}
		if kt.Name == "request_tools" {
			out = append(out, toolclient.RequestToolsMetaTool())
		}
	}
	return out
}

// unionToolsByName appends every tool from extra whose name is not
// already present in base, preserving base's order and appending extra
// tools in extra's own order. Used to fold the always_included escape
// hatch (item 5) into an already-filtered/capped tool list without
// duplicating an entry that survived on its own merits.
func unionToolsByName(base, extra []llmtypes.ToolDefinition) []llmtypes.ToolDefinition {
	if len(extra) == 0 {
		return base
	}
	present := make(map[string]bool, len(base)+len(extra))
	for _, t := range base {
		present[t.Name] = true
	}
	out := base
	for _, t := range extra {
		if present[t.Name] {
			continue
		}
		present[t.Name] = true
		out = append(out, t)
	}
	return out
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
