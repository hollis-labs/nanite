package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	feotel "github.com/hollis-labs/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"github.com/hollis-labs/conduit/internal/config"
	"github.com/hollis-labs/conduit/internal/filter"
	"github.com/hollis-labs/conduit/internal/mcp"
	"github.com/hollis-labs/conduit/internal/provider"
	"github.com/hollis-labs/conduit/internal/sandbox"
	"github.com/hollis-labs/conduit/internal/store"
	"github.com/hollis-labs/conduit/internal/toolclient"
	"github.com/hollis-labs/conduit/internal/truncate"
)

// PluginEventEmitter is the subset of plugin.Host used by the chat engine
// for event emission. Defined here as an interface to avoid importing the
// plugin package (which would create a circular dependency).
type PluginEventEmitter interface {
	EmitSessionStart(sessionID, agentID, mode string)
	EmitSessionEnd(sessionID string)
	EmitAgentSwitched(sessionID, previousAgentID, newAgentID string)
	EmitMessageSent(sessionID, messageID, content, role string, tokensUsed int)
	EmitMessageReceived(sessionID, messageID, content string, responseTime int64)
	EmitToolCalled(sessionID, toolName string, args, result interface{})
	EmitToolFailed(sessionID, toolName string, args interface{}, err string)
	EmitModeChanged(sessionID, previousMode, newMode string)
	EmitPreHook(eventType, sessionID string, data map[string]interface{}) bool
}

// maxToolIterations is the maximum number of tool-use loop iterations.
// 10 allows complex multi-tool tasks while still preventing runaway loops.
const maxToolIterations = 10

// generateResponseTimeout is the maximum wall-clock time a single
// generateResponse goroutine is allowed to run before being cancelled.
const generateResponseTimeout = 5 * time.Minute

// ProgressiveDiscoveryThreshold is the tool count above which progressive
// discovery is used instead of sending all tool schemas to the LLM.
const ProgressiveDiscoveryThreshold = 5

// AgentConstraints holds parsed runtime constraints from AgentProfile.Constraints.
type AgentConstraints struct {
	MaxIterations  int `json:"max_iterations"`
	MaxTimeSeconds int `json:"max_time_seconds"`
	RetryBudget    int `json:"retry_budget"`

	// Phase 4 — Chat Loop Hardening.
	MaxTurns           int  `json:"max_turns"`             // 0=default(25), -1=unlimited, >0=value
	HardCeiling        int  `json:"hard_ceiling"`          // 0=default(100), absolute max turns
	ConsecutiveFailCap int  `json:"consecutive_fail_cap"`  // 0=default(3), pause after N consecutive failures
	IdleTimeoutSeconds int  `json:"idle_timeout_seconds"`  // 0=default(900), seconds of inactivity before suspend
	DebugMode          bool `json:"debug_mode"`            // enable turn snapshot capture
}

// parseAgentConstraints parses the constraints JSON from an agent profile.
// Returns zero-value struct on empty/invalid input (no constraints enforced).
func ParseAgentConstraints(raw string) AgentConstraints {
	var c AgentConstraints
	if raw == "" || raw == "{}" {
		return c
	}
	json.Unmarshal([]byte(raw), &c)
	return c
}

// requestToolsDef is a meta-tool the LLM can call to request full schemas
// for specific tools by name or by describing intent. Delegates to
// toolclient.RequestToolsMetaTool() for the canonical definition.
var requestToolsDef = toolclient.RequestToolsMetaTool()

// nativeToolGuide is injected into every system prompt to help the LLM
// correctly use native dev/general tools. These tools require specific
// argument formats (absolute paths, separate pattern/directory params)
// that the LLM frequently gets wrong without guidance.
const nativeToolGuide = `

## Native Tool Usage

When using file and search tools, follow these rules:

- **All paths must be absolute** (start with /Users/). Never use ~ or relative paths.
- **dev_glob requires TWO separate params**: pattern (relative glob like **/*.md) and directory (absolute path like /Users/chrispian/Projects-apps/mentat). Do NOT put the full path in the pattern.
- **dev_grep requires TWO separate params**: pattern (regex) and directory (absolute path). Same rule — keep them separate.
- **dev_read/dev_write/dev_edit**: path must be absolute.
- **web_fetch**: many news/social sites block automated requests. Works best with APIs, docs sites, and raw content URLs.
- **Allowed directories**: /Users/chrispian/Projects-apps, /Users/chrispian/Projects. Files outside these paths will be rejected.`

// buildToolCatalog formats tool summaries as a compact catalog string for
// injection into the system prompt during progressive discovery.
func BuildToolCatalog(summaries []toolclient.ToolSummary) string {
	if len(summaries) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("Available tools (use request_tools to get full details):\n")
	for _, s := range summaries {
		desc := s.Description
		if len(desc) > 80 {
			desc = desc[:80] + "..."
		}
		fmt.Fprintf(&sb, "- %s: %s\n", s.Name, desc)
	}
	return sb.String()
}

// ToolWarningPayload is the JSON payload for tool_warning SSE events.
// These events provide user-visible feedback when tool calls fail.
type ToolWarningPayload struct {
	ToolName          string `json:"tool_name"`
	Error             string `json:"error"`
	Iteration         int    `json:"iteration"`
	ConsecutiveErrors int    `json:"consecutive_errors"`
	Level             string `json:"level"` // "warning" or "critical"
}

// StreamEvent is the event sent to SSE clients.
type StreamEvent struct {
	Type            string     `json:"type"`                        // stream_start, delta, stream_end, error, tool_call, tool_result, status, circuit_open, session_takeover, tool_warning
	Content         string     `json:"content,omitempty"`
	MessageID       string     `json:"message_id,omitempty"`
	AgentID         string     `json:"agent_id,omitempty"`
	Usage           *Usage     `json:"usage,omitempty"`
	Error           string     `json:"error,omitempty"`
	StructuredError *ChatError `json:"structured_error,omitempty"`
	Tool            string     `json:"tool,omitempty"`              // tool name for tool_call/tool_result
	ToolID          string     `json:"tool_id,omitempty"`           // tool_use_id
	Summary         string     `json:"summary,omitempty"`           // tool result summary
	Envelope        string     `json:"envelope,omitempty"`          // JSON envelope data for stream_end
	Data            string     `json:"data,omitempty"`              // JSON payload for tool_warning events
}

// Usage contains token usage for a completed response.
type Usage struct {
	InputTokens         int    `json:"input_tokens"`
	OutputTokens        int    `json:"output_tokens"`
	CacheCreationTokens int    `json:"cache_creation_tokens"`
	CacheReadTokens     int    `json:"cache_read_tokens"`
	StopReason          string `json:"stop_reason"`
}

// PresenceEvent is broadcast to all connected presence clients.
type PresenceEvent struct {
	Type      string `json:"type"`                 // stream_start, stream_end, tool_pending, tool_resolved
	SessionID string `json:"session_id"`
	AgentID   string `json:"agent_id,omitempty"`
	ToolName  string `json:"tool_name,omitempty"`
	Timestamp string `json:"timestamp"`
}

// sseConn tracks an active SSE connection for session-level deduplication.
type sseConn struct {
	done chan struct{} // closed to signal takeover to the old connection
}

// Engine orchestrates chat sessions, provider calls, and streaming.
type Engine struct {
	Store           *store.Store
	Providers       *provider.Registry
	UtilityProvider string // provider name for lightweight utility calls (autoTitle, autoTags); defaults to "anthropic"
	UtilityModel    string // model name for utility calls; defaults to "claude-sonnet-4-20250514"
	Broker          *ContextClient
	MCPManager      *mcp.Manager
	ToolClient      *toolclient.ToolClient
	Orchestrator    *Orchestrator
	Activity        *ActivityEmitter
	AppConfig       *config.AppConfig // app-level tunables (presence throttle, artifact detection)
	OutputFilters   *filter.Chain     // post-LLM output filters (nil = no filtering)
	PluginHost          PluginEventEmitter // plugin event emission (nil-safe)
	ProcessTracker      *ProcessTracker
	Commands            *CommandRegistry
	cliActiveLastEmit   sync.Map // map[sessionID]time.Time — throttle cli_active presence
	streams             sync.Map // map[string]chan StreamEvent
	msgToSession    sync.Map // map[messageID]sessionID — tracks which session a stream belongs to
	sessionSSE      sync.Map // map[sessionID]*sseConn — one active SSE connection per session
	presenceClients sync.Map // map[clientID]chan PresenceEvent — presence SSE listeners
	activePresence  sync.Map // map[sessionID]PresenceEvent — currently-streaming sessions (for initial state on connect)
}

// NewEngine creates a new chat engine. It reads utility provider/model from
// database user_settings first, then falls back to environment variables, then
// to hardcoded defaults (anthropic / claude-sonnet-4-20250514).
func NewEngine(s *store.Store, providers *provider.Registry) *Engine {
	utilityProvider := ""
	utilityModel := ""

	// Read from DB user_settings (written by the frontend settings UI).
	if settings, err := s.GetUserSettings(); err == nil {
		utilityProvider = settings.UtilityProvider
		utilityModel = settings.UtilityModel
	}

	// Final defaults.
	if utilityProvider == "" {
		utilityProvider = "anthropic"
	}
	if utilityModel == "" {
		utilityModel = "claude-sonnet-4-20250514"
	}

	cmds := NewCommandRegistry()
	cmds.RegisterServerCommands(s, providers)

	return &Engine{
		Store:           s,
		Providers:       providers,
		UtilityProvider: utilityProvider,
		UtilityModel:    utilityModel,
		Broker:          NewContextClient(s),
		ProcessTracker:  NewProcessTracker(),
		Commands:        cmds,
	}
}

// RefreshUtilitySettings updates the utility provider and model on the running
// engine. Called by the settings API after a user changes preferences.
func (e *Engine) RefreshUtilitySettings(provider, model string) {
	if provider != "" {
		e.UtilityProvider = provider
	}
	if model != "" {
		e.UtilityModel = model
	}
}

// Shutdown kills all tracked CLI processes. Call during server shutdown.
func (e *Engine) Shutdown() {
	if e.ProcessTracker != nil {
		e.ProcessTracker.KillAll()
	}
}

// KillSessionProcesses kills any CLI processes tracked for the given session.
func (e *Engine) KillSessionProcesses(sessionID string) int {
	if e.ProcessTracker == nil {
		return 0
	}
	return e.ProcessTracker.KillSession(sessionID)
}

// HandleMessage processes an incoming user message: persists it, starts async generation, and
// returns the assistant message ID that the client should use to connect to the SSE stream.
func (e *Engine) HandleMessage(sessionID, content string) (string, error) {
	// Persist user message.
	userMsg := &store.Message{
		ID:        uuid.New().String(),
		SessionID: sessionID,
		Role:      "user",
		Content:   content,
	}
	if err := e.Store.CreateMessage(userMsg); err != nil {
		return "", fmt.Errorf("create user message: %w", err)
	}

	// Create assistant message ID and stream channel.
	assistantMsgID := uuid.New().String()
	ch := make(chan StreamEvent, 128)
	e.streams.Store(assistantMsgID, ch)
	e.msgToSession.Store(assistantMsgID, sessionID)

	// Start async generation.
	go e.generateResponse(context.Background(), sessionID, assistantMsgID, content, ch)

	return assistantMsgID, nil
}

// GetStream returns the event channel for a given assistant message ID.
func (e *Engine) GetStream(messageID string) (<-chan StreamEvent, bool) {
	val, ok := e.streams.Load(messageID)
	if !ok {
		return nil, false
	}
	return val.(chan StreamEvent), true
}

// GetSessionForMessage returns the session ID associated with a message stream.
func (e *Engine) GetSessionForMessage(messageID string) (string, bool) {
	val, ok := e.msgToSession.Load(messageID)
	if !ok {
		return "", false
	}
	return val.(string), true
}

// RegisterSSEConnection registers a new SSE connection for a session.
// If another connection already exists for this session, its done channel is
// closed (signaling session_takeover) before being replaced. Returns the new
// connection's done channel that the caller should select on.
func (e *Engine) RegisterSSEConnection(sessionID string) <-chan struct{} {
	conn := &sseConn{done: make(chan struct{})}

	// Swap in the new connection; if an old one exists, signal takeover.
	if prev, loaded := e.sessionSSE.Swap(sessionID, conn); loaded {
		old := prev.(*sseConn)
		close(old.done)
		log.Printf("chat: SSE session takeover for session %s — old connection evicted", sessionID)
	}

	return conn.done
}

// UnregisterSSEConnection removes the SSE connection for a session, but only
// if the done channel matches (i.e., this is still the active connection).
func (e *Engine) UnregisterSSEConnection(sessionID string, done <-chan struct{}) {
	val, ok := e.sessionSSE.Load(sessionID)
	if !ok {
		return
	}
	current := val.(*sseConn)
	// Only delete if we are still the active connection (compare done channels).
	// Use a select to check if the done channel is the same object conceptually.
	// Since we can't compare channels directly to the read-only version, we check
	// if the current connection's done channel is closed (meaning it was taken over).
	select {
	case <-current.done:
		// Already taken over and closed — another connection replaced us.
		// Don't delete; the replacement owns this slot.
	default:
		// Still active — check if our done matches by attempting to delete.
		// We stored *sseConn, so the pointer comparison works.
		e.sessionSSE.CompareAndDelete(sessionID, val)
	}
}

// RegisterPresenceClient registers a new presence listener and returns a client ID
// and a read-only channel for receiving presence events.
func (e *Engine) RegisterPresenceClient() (string, <-chan PresenceEvent) {
	clientID := uuid.New().String()
	ch := make(chan PresenceEvent, 32)
	e.presenceClients.Store(clientID, ch)
	log.Printf("presence: client %s registered", clientID)
	return clientID, ch
}

// UnregisterPresenceClient removes a presence listener.
func (e *Engine) UnregisterPresenceClient(clientID string) {
	if val, ok := e.presenceClients.LoadAndDelete(clientID); ok {
		close(val.(chan PresenceEvent))
		log.Printf("presence: client %s unregistered", clientID)
	}
}

// broadcastPresence sends a presence event to all connected presence clients.
func (e *Engine) broadcastPresence(event PresenceEvent) {
	e.presenceClients.Range(func(key, val any) bool {
		ch := val.(chan PresenceEvent)
		select {
		case ch <- event:
		default:
			// Client is slow — drop the event rather than blocking.
			log.Printf("presence: dropped event for slow client %s", key.(string))
		}
		return true
	})
}

// throttledCLIActivePresence emits a cli_active presence event at most once per
// the configured throttle interval per session. This signals that a PTY process
// is producing output between formal message boundaries.
func (e *Engine) throttledCLIActivePresence(sessionID string) {
	throttle := 5 * time.Second
	if e.AppConfig != nil && e.AppConfig.Presence.CLIActiveThrottleSeconds > 0 {
		throttle = time.Duration(e.AppConfig.Presence.CLIActiveThrottleSeconds) * time.Second
	}

	now := time.Now()
	if last, ok := e.cliActiveLastEmit.Load(sessionID); ok {
		if now.Sub(last.(time.Time)) < throttle {
			return
		}
	}
	e.cliActiveLastEmit.Store(sessionID, now)

	e.broadcastPresence(PresenceEvent{
		Type:      "cli_active",
		SessionID: sessionID,
		Timestamp: now.UTC().Format(time.RFC3339),
	})
}

// BroadcastSessionArchived sends a session_archived presence event to all clients.
func (e *Engine) BroadcastSessionArchived(sessionID string) {
	// Clear any active presence for this session.
	e.activePresence.Delete(sessionID)

	e.broadcastPresence(PresenceEvent{
		Type:      "session_archived",
		SessionID: sessionID,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	})
}

// ActivePresenceState returns a snapshot of all currently-streaming sessions.
func (e *Engine) ActivePresenceState() []PresenceEvent {
	var events []PresenceEvent
	e.activePresence.Range(func(_, val any) bool {
		events = append(events, val.(PresenceEvent))
		return true
	})
	return events
}

// generateResponse loads context, calls the provider, streams events, and saves the result.
func (e *Engine) generateResponse(ctx context.Context, sessionID, assistantMsgID, userContent string, ch chan StreamEvent) {
	startTime := time.Now()

	// Wrap the context with an overall deadline so this goroutine cannot run forever.
	ctx, cancel := context.WithTimeout(ctx, generateResponseTimeout)
	defer cancel()

	ctx, span := feotel.StartSpan(ctx, "conduit.generateResponse")
	span.SetAttributes(
		attribute.String("conduit.session.id", sessionID),
		attribute.String("conduit.message.id", assistantMsgID),
	)
	defer span.End()

	defer func() {
		close(ch)
		e.streams.Delete(assistantMsgID)
		e.msgToSession.Delete(assistantMsgID)

		// Broadcast presence: stream ended.
		e.activePresence.Delete(sessionID)
		e.broadcastPresence(PresenceEvent{
			Type:      "stream_end",
			SessionID: sessionID,
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		})
	}()

	// Load session.
	session, err := e.Store.GetSession(sessionID)
	if err != nil {
		ch <- ErrorEvent(ErrorCodeInternal, "Failed to load session", map[string]interface{}{"raw": err.Error()})
		return
	}

	// Look up the primary agent from session_agents.
	var agentID, modeName string
	sa, err := e.Store.GetSessionPrimaryAgent(sessionID)
	if err != nil {
		// No session_agent record — auto-create one with the default agent.
		log.Printf("chat: no primary agent for session %s, auto-assigning mentat-001", sessionID)
		if err := e.Store.EnsureSessionAgent(sessionID, "mentat-001", "default", true); err != nil {
			log.Printf("chat: failed to auto-assign agent: %v", err)
		}
		agentID = "mentat-001"
		modeName = "default"
		if e.Activity != nil {
			go e.Activity.EmitAgentAssigned(ctx, sessionID, agentID, modeName)
		}
	} else {
		agentID = sa.AgentID
		modeName = sa.Mode
	}

	agent, err := e.Store.GetAgent(agentID)
	if err != nil {
		// Fallback to slug lookup for backwards compatibility.
		agent, err = e.Store.GetAgentBySlug("mentat")
		if err != nil {
			ch <- ErrorEvent(ErrorCodeInternal, "Failed to load agent", map[string]interface{}{"raw": err.Error()})
			return
		}
	}

	// Block disabled agents from responding.
	if agent.Status == "disabled" {
		ch <- ErrorEvent(ErrorCodeInternal, fmt.Sprintf("Agent %q is disabled", agent.Name), nil)
		return
	}

	// Parse agent constraints (schema v2).
	constraints := ParseAgentConstraints(agent.Constraints)

	// Override context timeout if the agent has a max_time_seconds constraint.
	if constraints.MaxTimeSeconds > 0 {
		agentTimeout := time.Duration(constraints.MaxTimeSeconds) * time.Second
		ctx, cancel = context.WithTimeout(ctx, agentTimeout)
		defer cancel()
	}

	// Load agent mode.
	mode, err := e.Store.GetAgentMode(agent.ID, modeName)
	if err != nil {
		log.Printf("chat: could not load agent mode %s/%s: %v (using base prompt)", agent.ID, modeName, err)
		mode = &store.AgentMode{}
	}

	// Load workspace for context.
	var workspace *store.Workspace
	if session.WorkspaceID != "" {
		workspace, err = e.Store.GetWorkspace(session.WorkspaceID)
		if err != nil {
			log.Printf("chat: could not load workspace %s: %v", session.WorkspaceID, err)
		}
	}

	// Assemble context via broker.
	systemPrompt, chatMessages, err := e.Broker.AssembleContext(ctx, session, agent, mode, workspace)
	if err != nil {
		ch <- ErrorEvent(ErrorCodeInternal, "Failed to assemble context", map[string]interface{}{"raw": err.Error()})
		return
	}

	// Resolve model: session > agent default > global fallback.
	model := session.Model
	if model == "" && agent.DefaultModel != "" {
		model = agent.DefaultModel
	}
	if model == "" {
		model = "claude-sonnet-4-20250514"
	}

	// Resolve provider via fallback chain:
	// 1. Session's explicit provider
	// 2. Agent's default provider
	// 3. User's fallback chain (first available)
	// 4. Infer from model name
	// 5. System default ("anthropic")
	providerName, prov := e.resolveProvider(session.Provider, agent.DefaultProvider, model)
	if prov == nil {
		ch <- ErrorEvent(ErrorCodeProviderError, fmt.Sprintf("Provider %q not available — check configuration and restart the server.", providerName), map[string]interface{}{"raw": fmt.Sprintf("provider %q not registered", providerName)})
		return
	}

	// Wire status callback for retry notifications and circuit breaker.
	if ap, ok := prov.(*provider.Anthropic); ok {
		ap.OnStatus = func(message string) {
			ch <- StreamEvent{Type: "status", Content: message}
		}
		ap.OnCircuitOpen = func() {
			ch <- StreamEvent{
				Type:    "circuit_open",
				Content: "Provider rate limited after multiple retries. Would you like to keep trying?",
			}
		}
	}

	// Apply cache hints if the provider supports prompt caching.
	if cacheable, ok := prov.(provider.CacheableProvider); ok {
		cacheable.SetCacheHints(provider.DefaultCacheStrategy())
	}

	// Emit stream_start.
	ch <- StreamEvent{Type: "stream_start", MessageID: assistantMsgID, AgentID: agent.ID}

	// Broadcast presence: stream started.
	presenceStart := PresenceEvent{
		Type:      "stream_start",
		SessionID: sessionID,
		AgentID:   agent.ID,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}
	e.activePresence.Store(sessionID, presenceStart)
	e.broadcastPresence(presenceStart)

	// Notify Volon that this chat session is active.
	if e.Activity != nil {
		go e.Activity.EmitSessionStart(ctx, sessionID, agent.ID, model)
	}
	// Emit plugin event: session started.
	if e.PluginHost != nil {
		go e.PluginHost.EmitSessionStart(sessionID, agent.ID, model)
	}

	// Get available tools — filter by agent's assigned skills, then ToolClient, then MCP Manager.
	selection := e.getToolsForAgent(ctx, agentID, userContent, session.WorkspaceID)
	tools := selection.Tools

	// Warn user if no MCP tools are available — responses will be text-only.
	// Skip this check when progressive discovery is active: the agent HAS tools,
	// they just need to be discovered via request_tools (no mcp__ prefixed tools
	// in the initial set is expected in that case).
	if !selection.Progressive {
		mcpToolCount := 0
		for _, t := range tools {
			if strings.HasPrefix(t.Name, "mcp__") {
				mcpToolCount++
			}
		}
		if mcpToolCount == 0 {
			warningPayload := ToolWarningPayload{
				Error: "This agent has no MCP tools configured. Responses will be text-only.",
				Level: "critical",
			}
			warningJSON, _ := json.Marshal(warningPayload)
			ch <- StreamEvent{Type: "tool_warning", Data: string(warningJSON)}

			// Inject guidance so the LLM doesn't waste iterations guessing tool names.
			systemPrompt += "\n\nIMPORTANT: You have no tools available in this session. Do NOT attempt to call any tools — all tool calls will fail. Respond with text only. If the user's request requires tools (data lookup, task management, code execution, etc.), clearly explain that this agent is not configured with the necessary tools and suggest they switch to an agent that has tools configured."
		}
	}

	// If progressive discovery is active, inject the tool catalog into the system prompt.
	if selection.Progressive && selection.Catalog != "" {
		systemPrompt = systemPrompt + "\n\n" + selection.Catalog
	}

	// Inject native tool usage guide so the LLM understands how to call dev/general tools correctly.
	systemPrompt += nativeToolGuide

	// Track loaded tools for progressive discovery (tools loaded via request_tools).
	// Seed with initial tools so progressive discovery won't re-add them.
	loadedTools := make(map[string]bool, len(tools))
	for _, t := range tools {
		loadedTools[t.Name] = true
	}
	// Consecutive request_tools calls that returned zero new tools.
	consecutiveEmptyRequests := 0
	// Total request_tools calls across all iterations — hard cap to prevent loops.
	totalRequestToolsCalls := 0
	const maxRequestToolsCalls = 3
	// Track repeated tool results to detect stuck loops (tool_name -> last result hash).
	lastToolResults := make(map[string]string)
	toolRepeatCount := make(map[string]int)
	// blockedTools tracks tools that have been hard-blocked due to repeated identical results.
	// Checked BEFORE execution to prevent the tool from running at all.
	blockedTools := make(map[string]bool)
	// retryBudget tracks remaining retries for recoverable errors (agent constraint).
	// -1 means unlimited (no constraint set).
	retryBudget := -1
	if constraints.RetryBudget > 0 {
		retryBudget = constraints.RetryBudget
	}
	// consecutiveToolErrors tracks sequential tool failures to escalate user-visible warnings.
	consecutiveToolErrors := 0
	// pendingEnvelopes collects envelope JSON from KB tools to inject after the agent's response.
	var pendingEnvelopes []string
	// toolCallRefs accumulates structured tool call references for the structured message.
	var toolCallRefs []ToolCallRef
	// wasTruncated tracks whether the response hit max_tokens.
	var wasTruncated bool

	// Tool-use loop: call the provider, handle tool calls, repeat.
	var fullContent strings.Builder
	var finalUsage *Usage
	var breakdown *TokenBreakdown

	// Determine effective iteration cap (agent constraint may lower it).
	iterationCap := maxToolIterations
	if constraints.MaxIterations > 0 && constraints.MaxIterations < iterationCap {
		iterationCap = constraints.MaxIterations
	}

	iteration := 0
	for ; iteration < iterationCap; iteration++ {
		// Check if the overall deadline has been exceeded.
		if ctx.Err() != nil {
			log.Printf("[WARN] generateResponse context cancelled: %v (session=%s)", ctx.Err(), sessionID)
			ch <- ErrorEnvelopeDelta(ErrorCodeInternal, "Response timed out after 5 minutes. Please try again with a simpler request.", map[string]interface{}{
				"timeout": generateResponseTimeout.String(),
				"session": sessionID,
			})
			ch <- ErrorEvent(ErrorCodeInternal, "Response timed out after 5 minutes. Please try again with a simpler request.", map[string]interface{}{
				"timeout": generateResponseTimeout.String(),
				"session": sessionID,
			})
			return
		}

		// Enforce unified token budget before every provider call.
		var budgetErr error
		chatMessages, tools, breakdown, budgetErr = EnforceTokenBudget(systemPrompt, chatMessages, tools, 0)
		if budgetErr != nil {
			log.Printf("chat: token budget enforcement refused to send: %v", budgetErr)
			if e.Activity != nil {
				go e.Activity.EmitContextBudgetExceeded(ctx, sessionID, breakdown.Total, breakdown.Ceiling)
			}
			ch <- ErrorEvent(ErrorCodeInternal, "Context too large after all reductions",
				map[string]interface{}{
					"total":   breakdown.Total,
					"ceiling": breakdown.Ceiling,
					"system":  breakdown.System,
					"msgs":    breakdown.Messages,
					"tools":   breakdown.Tools,
				})
			return
		}

		// Call provider with or without tools.
		provCtx, provSpan := feotel.StartSpan(ctx, "conduit.provider.call")
		provSpan.SetAttributes(
			attribute.String("conduit.model", model),
			attribute.Int("conduit.iteration", iteration),
			attribute.Int("conduit.tools.count", len(tools)),
			attribute.Int("conduit.messages.count", len(chatMessages)),
			attribute.Int("conduit.tokens.total", breakdown.Total),
			attribute.Int("conduit.tokens.ceiling", breakdown.Ceiling),
		)
		// CLI sessions (PTY or subprocess): set up sandbox directory and resume context.
		if IsCLIProvider(providerName) {
			// Check concurrency limit before spawning a new CLI process.
			if e.ProcessTracker != nil && e.ProcessTracker.AtCapacity() {
				ch <- ErrorEvent(ErrorCodeProviderError,
					fmt.Sprintf("CLI process limit reached (%d). Close other CLI sessions or wait for them to finish.", e.ProcessTracker.MaxProcesses),
					map[string]interface{}{"raw": "max concurrent CLI processes exceeded"})
				return
			}
			// Create/resolve sandbox directory and populate reference files.
			if sbDir, sbErr := sandbox.Dir(sessionID); sbErr != nil {
				log.Printf("chat: sandbox dir error: %v", sbErr)
			} else {
				if sbErr := sandbox.Populate(sbDir, agent, mode, sandbox.PopulateOpts{
					SessionID: sessionID,
					DBPath:    e.Store.DBPath(),
				}); sbErr != nil {
					log.Printf("chat: sandbox populate error: %v", sbErr)
				}
				provCtx = provider.WithSandboxDir(provCtx, sbDir)
			}

			// Inject CLI session ID for --resume if one exists.
			var meta map[string]any
			if err := json.Unmarshal([]byte(session.Metadata), &meta); err == nil {
				if cliSID, ok := meta["cli_session_id"].(string); ok && cliSID != "" {
					provCtx = provider.WithCLISessionID(provCtx, cliSID)
				}
			}

			// Wire process tracker so spawned CLI processes are tracked.
			if e.ProcessTracker != nil {
				sid := sessionID
				provCtx = provider.WithProcessCallback(provCtx, func(proc *os.Process, started bool) {
					if started {
						e.ProcessTracker.Track(sid, proc)
					} else {
						e.ProcessTracker.Untrack(sid, proc)
					}
				})
				provCtx = provider.WithActivityCallback(provCtx, func(pid int) {
					e.ProcessTracker.Touch(sid, pid)
					e.throttledCLIActivePresence(sid)
				})
			}
		}

		var provCh <-chan provider.StreamEvent
		if len(tools) > 0 {
			log.Printf("chat: tool-use iteration %d — %d tools, %d messages, ~%d tokens (ceiling=%d)", iteration, len(tools), len(chatMessages), breakdown.Total, breakdown.Ceiling)
			provCh, err = prov.StreamChatWithTools(provCtx, systemPrompt, chatMessages, model, tools)
		} else {
			provCh, err = prov.StreamChat(provCtx, systemPrompt, chatMessages, model)
		}
		if err != nil {
			provSpan.RecordError(err)
			provSpan.SetStatus(codes.Error, err.Error())
			provSpan.End()
			log.Printf("chat: provider stream error on iteration %d: %v", iteration, err)
			e.Store.LogEvent(sessionID, "provider_error", "error",
				fmt.Sprintf("iteration %d: %v", iteration, err),
				fmt.Sprintf(`{"model":%q,"tools":%d,"messages":%d}`, model, len(tools), len(chatMessages)))
			if e.Activity != nil {
				errCode := ClassifyError(err)
				go e.Activity.EmitError(ctx, sessionID, "provider_error", err.Error())
				if errCode == ErrorCodeRateLimit {
					go e.Activity.EmitRateLimitHit(ctx, sessionID, "anthropic", 0)
				}
			}
			// Decrement retry budget on provider errors.
			if retryBudget > 0 {
				retryBudget--
				if retryBudget == 0 {
					log.Printf("chat: retry budget exhausted for session %s", sessionID)
				}
			}
			errDetails := map[string]interface{}{
				"raw":   err.Error(),
				"model": model,
				"tools": len(tools),
			}
			ch <- ErrorEnvelopeDelta(ClassifyError(err), "Provider streaming failed", errDetails)
			ch <- ErrorEvent(ClassifyError(err), "Provider streaming failed", errDetails)
			return
		}

		// Accumulate content and tool_use blocks from this turn.
		var turnContent strings.Builder
		var toolUseBlocks []provider.ToolUseBlock
		var stopReason string
		var lastPTYToolPending string // tracks unresolved PTY tool for presence

		for evt := range provCh {
			switch evt.Type {
			case "delta":
				// Resolve any pending PTY tool (CLI returned text after a tool call).
				if lastPTYToolPending != "" && IsCLIProvider(providerName) {
					e.broadcastPresence(PresenceEvent{
						Type:      "tool_resolved",
						SessionID: sessionID,
						AgentID:   agent.ID,
						ToolName:  lastPTYToolPending,
						Timestamp: time.Now().UTC().Format(time.RFC3339),
					})
					lastPTYToolPending = ""
				}
				turnContent.WriteString(evt.Content)
				fullContent.WriteString(evt.Content)
				ch <- StreamEvent{Type: "delta", Content: evt.Content}
			case "tool_use":
				if evt.ToolUse != nil {
					toolUseBlocks = append(toolUseBlocks, *evt.ToolUse)

					// PTY/subprocess sessions: CLI manages tools internally,
					// so emit tool_pending presence here (API sessions emit
					// this later in the tool execution loop).
					if IsCLIProvider(providerName) {
						// Resolve the previous tool before starting a new one.
						if lastPTYToolPending != "" {
							e.broadcastPresence(PresenceEvent{
								Type:      "tool_resolved",
								SessionID: sessionID,
								AgentID:   agent.ID,
								ToolName:  lastPTYToolPending,
								Timestamp: time.Now().UTC().Format(time.RFC3339),
							})
						}
						lastPTYToolPending = evt.ToolUse.Name
						e.broadcastPresence(PresenceEvent{
							Type:      "tool_pending",
							SessionID: sessionID,
							AgentID:   agent.ID,
							ToolName:  evt.ToolUse.Name,
							Timestamp: time.Now().UTC().Format(time.RFC3339),
						})

						// Auto-detect artifacts from PTY tool calls too.
						e.maybeCreateAutoArtifact(sessionID, assistantMsgID, agent.ID, *evt.ToolUse)
					}
				}
			case "usage":
				if evt.Usage != nil {
					if finalUsage == nil {
						finalUsage = &Usage{}
					}
					if evt.Usage.InputTokens > 0 {
						finalUsage.InputTokens += evt.Usage.InputTokens
					}
					if evt.Usage.OutputTokens > 0 {
						finalUsage.OutputTokens += evt.Usage.OutputTokens
					}
					if evt.Usage.CacheCreationTokens > 0 {
						finalUsage.CacheCreationTokens += evt.Usage.CacheCreationTokens
					}
					if evt.Usage.CacheReadTokens > 0 {
						finalUsage.CacheReadTokens += evt.Usage.CacheReadTokens
					}
					if evt.Usage.StopReason != "" {
						stopReason = evt.Usage.StopReason
						finalUsage.StopReason = evt.Usage.StopReason
					}
				}
			case "error":
				streamErrDetails := map[string]interface{}{
					"raw":   evt.Error,
					"model": model,
				}
				ch <- ErrorEnvelopeDelta(ClassifyError(fmt.Errorf("%s", evt.Error)), "Streaming error from provider", streamErrDetails)
				ch <- ErrorEvent(ClassifyError(fmt.Errorf("%s", evt.Error)), "Streaming error from provider", streamErrDetails)
				return
			case "session_id":
				// PTY bridge emits the CLI session ID from the system init event.
				// Persist it in session metadata so future turns use --resume.
				if evt.SessionID != "" {
					var meta map[string]any
					if err := json.Unmarshal([]byte(session.Metadata), &meta); err != nil || meta == nil {
						meta = make(map[string]any)
					}
					meta["cli_session_id"] = evt.SessionID
					metaJSON, _ := json.Marshal(meta)
					session.Metadata = string(metaJSON)
					if err := e.Store.UpdateSessionMetadata(sessionID, session.Metadata); err != nil {
						log.Printf("chat: failed to persist CLI session ID: %v", err)
					}
				}
			case "done":
				// Will handle below.
			}
		}

		// Resolve any remaining PTY tool presence after stream ends.
		if lastPTYToolPending != "" && IsCLIProvider(providerName) {
			e.broadcastPresence(PresenceEvent{
				Type:      "tool_resolved",
				SessionID: sessionID,
				AgentID:   agent.ID,
				ToolName:  lastPTYToolPending,
				Timestamp: time.Now().UTC().Format(time.RFC3339),
			})
		}

		provSpan.End()

		// If no tool use, we are done.
		if stopReason != "tool_use" || len(toolUseBlocks) == 0 {
			if stopReason == "max_tokens" {
				wasTruncated = true
				log.Printf("chat: response truncated by max_tokens on iteration %d", iteration)
				ch <- StreamEvent{Type: "status", Content: "Response was cut short due to length limits. Some content may be missing."}
				e.Store.LogEvent(sessionID, "max_tokens_truncation", "warning",
					fmt.Sprintf("iteration %d: response truncated by max_tokens", iteration),
					fmt.Sprintf(`{"model":%q,"iteration":%d}`, model, iteration))
				// Emit error envelope so the user sees a visible card.
				ch <- ErrorEnvelopeDelta(ErrorCodeInternal, "Response truncated — hit output token limit", map[string]interface{}{
					"stop_reason": "max_tokens",
					"iteration":   iteration,
					"model":       model,
				})
			}
			break
		}

		// Build the assistant message with content blocks (text + tool_use).
		var assistantBlocks []provider.ContentBlock
		if text := turnContent.String(); text != "" {
			assistantBlocks = append(assistantBlocks, provider.ContentBlock{
				Type: "text",
				Text: text,
			})
		}
		for _, tu := range toolUseBlocks {
			input := tu.Input
			if input == nil {
				input = map[string]any{}
			}
			assistantBlocks = append(assistantBlocks, provider.ContentBlock{
				Type:  "tool_use",
				ID:    tu.ID,
				Name:  tu.Name,
				Input: &input,
			})
		}
		chatMessages = append(chatMessages, provider.ChatMessage{
			Role:          "assistant",
			ContentBlocks: assistantBlocks,
		})

		// Execute each tool and build tool_result blocks.
		var resultBlocks []provider.ContentBlock
		for _, tu := range toolUseBlocks {
			// Handle request_tools meta-tool for progressive discovery.
			if tu.Name == "request_tools" && selection.Progressive && e.ToolClient != nil {
				ch <- StreamEvent{
					Type:   "tool_call",
					Tool:   tu.Name,
					ToolID: tu.ID,
				}

				totalRequestToolsCalls++

				// Hard cap: stop after maxRequestToolsCalls total calls, or 2 consecutive empties.
				if totalRequestToolsCalls > maxRequestToolsCalls || consecutiveEmptyRequests >= 2 {
					reason := fmt.Sprintf("consecutive_empty=%d, total_calls=%d", consecutiveEmptyRequests, totalRequestToolsCalls)
					rtResult := "Tool discovery limit reached (" + reason + "). No more request_tools calls will be processed. Proceed with the tools you already have — do NOT call request_tools again."
					log.Printf("chat: request_tools halted — %s", reason)

					ch <- StreamEvent{
						Type:    "tool_result",
						Tool:    tu.Name,
						ToolID:  tu.ID,
						Summary: rtResult,
					}
					resultBlocks = append(resultBlocks, provider.ContentBlock{
						Type:      "tool_result",
						ToolUseID: tu.ID,
						Content:   rtResult,
					})
					toolCallRefs = append(toolCallRefs, ToolCallRef{ID: tu.ID, Name: tu.Name, Status: "success"})
					continue
				}

				// Delegate to ToolClient.HandleRequestTools which supports
				// both explicit tool_names and intent-based selection.
				newTools, rtResult := e.ToolClient.HandleRequestTools(tu.Input)

				var loaded []string
				for _, nt := range newTools {
					if !loadedTools[nt.Name] {
						loadedTools[nt.Name] = true
						tools = append(tools, nt)
						loaded = append(loaded, nt.Name)
					}
				}

				if len(loaded) == 0 {
					consecutiveEmptyRequests++
					if consecutiveEmptyRequests == 1 {
						// First failure: hint to rephrase.
						rtResult = rtResult + "\n\nNo new tools were loaded for this request. If you believe the right tools exist, try rephrasing your intent with different keywords. Otherwise, proceed with the tools you have."
					}
				} else {
					// Successful load resets the counter.
					consecutiveEmptyRequests = 0
				}

				log.Printf("chat: request_tools loaded %d tools (consecutive_empty=%d): %v", len(loaded), consecutiveEmptyRequests, loaded)

				ch <- StreamEvent{
					Type:    "tool_result",
					Tool:    tu.Name,
					ToolID:  tu.ID,
					Summary: rtResult,
				}

				resultBlocks = append(resultBlocks, provider.ContentBlock{
					Type:      "tool_result",
					ToolUseID: tu.ID,
					Content:   rtResult,
				})
				toolCallRefs = append(toolCallRefs, ToolCallRef{ID: tu.ID, Name: tu.Name, Status: "success"})
				continue
			}

			// Emit tool_call event to the client.
			ch <- StreamEvent{
				Type:   "tool_call",
				Tool:   tu.Name,
				ToolID: tu.ID,
			}

			// Broadcast presence: tool pending.
			e.broadcastPresence(PresenceEvent{
				Type:      "tool_pending",
				SessionID: sessionID,
				AgentID:   agent.ID,
				ToolName:  tu.Name,
				Timestamp: time.Now().UTC().Format(time.RFC3339),
			})

			// Pre-execution check: skip tools that have been blocked due to repeated identical results.
			if blockedTools[tu.Name] {
				blockedResult := fmt.Sprintf("BLOCKED: Tool %q was blocked because it returned identical results multiple times. "+
					"Do NOT call this tool again. Use a different approach or inform the user.", tu.Name)
				log.Printf("chat: tool %s SKIPPED (blocked)", tu.Name)
				ch <- StreamEvent{
					Type:    "tool_result",
					Tool:    tu.Name,
					ToolID:  tu.ID,
					Summary: blockedResult,
				}
				resultBlocks = append(resultBlocks, provider.ContentBlock{
					Type:      "tool_result",
					ToolUseID: tu.ID,
					Content:   blockedResult,
				})
				toolCallRefs = append(toolCallRefs, ToolCallRef{ID: tu.ID, Name: tu.Name, Status: "blocked"})
				continue
			}

			var resultText string
			var toolIsError bool
			toolCtx, toolSpan := feotel.ToolCallSpan(ctx, tu.Name)
			if e.ToolClient != nil {
				// Use ToolClient for permission-checked execution.
				result, execErr := e.ToolClient.CallTool(toolCtx, agentID, tu.Name, tu.Input)
				if execErr != nil {
					resultText = fmt.Sprintf("Error: %v", execErr)
					toolIsError = true
					toolSpan.RecordError(execErr)
					toolSpan.SetStatus(codes.Error, execErr.Error())
					log.Printf("chat: tool %s failed: %v", tu.Name, execErr)
					e.Store.LogEvent(sessionID, "tool_error", "error",
						fmt.Sprintf("%s: %v", tu.Name, execErr), "{}")
					if e.Activity != nil {
						go e.Activity.EmitToolCall(ctx, sessionID, tu.Name, false, 0)
					}
					if e.PluginHost != nil {
						go e.PluginHost.EmitToolFailed(sessionID, tu.Name, tu.Input, execErr.Error())
					}
				} else {
					resultText = result
					toolSpan.SetAttributes(attribute.Int("conduit.tool.result_len", len(result)))
					e.Store.LogEvent(sessionID, "tool_call", "tool",
						tu.Name, fmt.Sprintf(`{"result_len":%d,"agent_id":%q}`, len(result), agentID))
					if e.Activity != nil {
						go e.Activity.EmitToolCall(ctx, sessionID, tu.Name, true, len(result))
					}
					if e.PluginHost != nil {
						go e.PluginHost.EmitToolCalled(sessionID, tu.Name, tu.Input, result)
					}
					// Capture envelope data from any tool result.
					// Tools embed envelopes via <!--ENVELOPE_DATA:...:ENVELOPE_DATA--> markers.
					// KB search results get wrapped via buildKBEnvelope; others pass through as-is.
					if eStart := strings.Index(result, "<!--ENVELOPE_DATA:"); eStart >= 0 {
						tail := result[eStart+len("<!--ENVELOPE_DATA:"):]
						if eEnd := strings.Index(tail, ":ENVELOPE_DATA-->"); eEnd >= 0 {
							envelopePayload := tail[:eEnd]
							if strings.HasSuffix(tu.Name, "__search_kb") {
								if env := BuildKBEnvelope(envelopePayload); env != "" {
									pendingEnvelopes = append(pendingEnvelopes, env)
								}
							} else {
								// Non-KB tools: payload is already a complete envelope JSON.
								pendingEnvelopes = append(pendingEnvelopes, envelopePayload)
							}
						}
					}
				}
			} else if e.MCPManager != nil {
				// Fallback to direct MCP Manager (no permission checks).
				result, execErr := e.MCPManager.ExecuteTool(toolCtx, tu.Name, tu.Input)
				if execErr != nil {
					resultText = fmt.Sprintf("Error: %v", execErr)
					toolIsError = true
					toolSpan.RecordError(execErr)
					toolSpan.SetStatus(codes.Error, execErr.Error())
					log.Printf("chat: tool %s failed: %v", tu.Name, execErr)
					e.Store.LogEvent(sessionID, "tool_error", "error",
						fmt.Sprintf("%s: %v", tu.Name, execErr), "{}")
					if e.Activity != nil {
						go e.Activity.EmitToolCall(ctx, sessionID, tu.Name, false, 0)
					}
					if e.PluginHost != nil {
						go e.PluginHost.EmitToolFailed(sessionID, tu.Name, tu.Input, execErr.Error())
					}
				} else {
					resultText = result
					toolSpan.SetAttributes(attribute.Int("conduit.tool.result_len", len(result)))
					e.Store.LogEvent(sessionID, "tool_call", "tool",
						tu.Name, fmt.Sprintf(`{"result_len":%d}`, len(result)))
					if e.Activity != nil {
						go e.Activity.EmitToolCall(ctx, sessionID, tu.Name, true, len(result))
					}
					if e.PluginHost != nil {
						go e.PluginHost.EmitToolCalled(sessionID, tu.Name, tu.Input, result)
					}
					// Capture envelope data (parity with ToolClient path).
					if eStart := strings.Index(result, "<!--ENVELOPE_DATA:"); eStart >= 0 {
						tail := result[eStart+len("<!--ENVELOPE_DATA:"):]
						if eEnd := strings.Index(tail, ":ENVELOPE_DATA-->"); eEnd >= 0 {
							envelopePayload := tail[:eEnd]
							if strings.HasSuffix(tu.Name, "__search_kb") {
								if env := BuildKBEnvelope(envelopePayload); env != "" {
									pendingEnvelopes = append(pendingEnvelopes, env)
								}
							} else {
								pendingEnvelopes = append(pendingEnvelopes, envelopePayload)
							}
						}
					}
				}
			} else {
				resultText = "Error: no tool client or MCP manager configured"
				toolIsError = true
				toolSpan.SetStatus(codes.Error, "no tool client or MCP manager configured")
			}
			toolSpan.End()

			// Emit tool_warning SSE event on errors so the user sees feedback.
			if toolIsError {
				consecutiveToolErrors++
				if retryBudget > 0 {
					retryBudget--
				}
				level := "warning"
				if consecutiveToolErrors >= 3 {
					level = "critical"
				}
				warningError := resultText
				if len(warningError) > 300 {
					warningError = warningError[:300] + "..."
				}
				warningPayload := ToolWarningPayload{
					ToolName:          tu.Name,
					Error:             warningError,
					Iteration:         iteration,
					ConsecutiveErrors: consecutiveToolErrors,
					Level:             level,
				}
				warningJSON, _ := json.Marshal(warningPayload)
				ch <- StreamEvent{Type: "tool_warning", Data: string(warningJSON)}
			} else {
				consecutiveToolErrors = 0
			}

			// Detect stuck loops: if the same tool returns the exact same result, escalate.
			if prev, ok := lastToolResults[tu.Name]; ok && prev == resultText {
				toolRepeatCount[tu.Name]++
				repeats := toolRepeatCount[tu.Name]
				if repeats >= 2 {
					// Hard block after 3rd identical result: block future calls entirely.
					blockedTools[tu.Name] = true
					resultText = fmt.Sprintf("ERROR: Tool %q has been called %d times with identical results. "+
						"This tool is now BLOCKED for this session turn. "+
						"You MUST stop calling this tool and either try a completely different approach "+
						"or tell the user: \"I was unable to complete this task because the tool returned the same result repeatedly.\"",
						tu.Name, repeats+1)
					log.Printf("chat: tool %s BLOCKED after %d identical results — future calls will be skipped", tu.Name, repeats+1)
				} else {
					// Warning on 2nd identical result.
					resultText = resultText + "\n\nWARNING: This tool has returned the same result " +
						fmt.Sprintf("%d times in a row. You are likely stuck in a loop. ", repeats+1) +
						"Do NOT call this tool again with the same arguments. " +
						"Either provide different arguments or inform the user that this task cannot be completed."
					log.Printf("chat: tool %s repeat detected (%d times)", tu.Name, repeats+1)
				}
			} else {
				toolRepeatCount[tu.Name] = 0
			}
			lastToolResults[tu.Name] = resultText

			// Truncate for the LLM context; save full output to disk if large.
			// If the session has multi-agent capability, hint delegation instead of narrowing.
			canDelegate := e.Orchestrator != nil && e.Orchestrator.HasDecomposer()
			tr := truncate.Output(resultText, tu.Name, truncate.WithDelegationHint(canDelegate))

			// Emit tool_result event to the client (use full result for UI summary).
			summary := resultText
			if len(summary) > 500 {
				summary = summary[:500] + "... (truncated)"
			}
			ch <- StreamEvent{
				Type:    "tool_result",
				Tool:    tu.Name,
				ToolID:  tu.ID,
				Summary: summary,
			}

			// Broadcast presence: tool resolved.
			e.broadcastPresence(PresenceEvent{
				Type:      "tool_resolved",
				SessionID: sessionID,
				AgentID:   agent.ID,
				ToolName:  tu.Name,
				Timestamp: time.Now().UTC().Format(time.RFC3339),
			})

			// Auto-detect artifacts from file-writing tool calls.
			if !toolIsError {
				e.maybeCreateAutoArtifact(sessionID, assistantMsgID, agent.ID, tu)
			}

			if tr.Truncated {
				log.Printf("chat: tool %s result truncated: %d → %d chars (saved to %s)",
					tu.Name, tr.OriginalLen, len(tr.Content), tr.OutputPath)
				e.Store.LogEvent(sessionID, "tool_truncated", "context",
					tu.Name, fmt.Sprintf(`{"original_len":%d,"truncated_len":%d,"output_path":%q}`,
						tr.OriginalLen, len(tr.Content), tr.OutputPath))
			}

			resultBlocks = append(resultBlocks, provider.ContentBlock{
				Type:      "tool_result",
				ToolUseID: tu.ID,
				Content:   tr.Content,
				IsError:   toolIsError,
			})

			// Accumulate tool call ref for structured message.
			tcStatus := "success"
			if toolIsError {
				tcStatus = "error"
			}
			tcRef := ToolCallRef{ID: tu.ID, Name: tu.Name, Status: tcStatus}
			if strings.Contains(resultText, "<!--ENVELOPE_DATA:") {
				tcRef.HasEnvelope = true
			}
			toolCallRefs = append(toolCallRefs, tcRef)
		}

		// Append tool results as a user message.
		chatMessages = append(chatMessages, provider.ChatMessage{
			Role:          "user",
			ContentBlocks: resultBlocks,
		})

		// Brief pause between iterations to avoid rate limit spikes.
		if iteration > 0 {
			time.Sleep(1 * time.Second)
		}

		// Check retry budget before next iteration.
		if retryBudget == 0 {
			log.Printf("chat: retry budget exhausted, stopping tool loop for session %s", sessionID)
			ch <- StreamEvent{Type: "status", Content: "Retry budget exhausted — stopping."}
			break
		}

		// Check circuit breaker before next iteration — stop if tripped.
		if ap, ok := prov.(*provider.Anthropic); ok && ap.CircuitBreaker != nil && ap.CircuitBreaker.IsOpen() {
			log.Printf("chat: circuit breaker open, stopping tool-use loop at iteration %d", iteration)
			if e.Activity != nil {
				go e.Activity.EmitCircuitBreakerTripped(ctx, sessionID, "anthropic")
			}
			ch <- StreamEvent{
				Type:    "circuit_open",
				Content: "Provider rate limited. Tool-use loop stopped. Would you like to retry?",
			}
			break
		}

		// Loop back for the next provider call.
	}

	if iteration >= iterationCap {
		log.Printf("[WARN] Tool loop exhausted after %d iterations for session=%s agent=%s", iteration, sessionID, agent.ID)
		ch <- ErrorEnvelopeDelta(ErrorCodeInternal, fmt.Sprintf("Response may be incomplete — tool step limit (%d) reached. The assistant was still working when the limit was hit.", iterationCap), nil)
	}

	// Apply output filters (e.g. strip emoji) before parsing envelopes.
	responseContent := fullContent.String()
	if e.OutputFilters != nil && e.OutputFilters.Len() > 0 {
		responseContent = e.OutputFilters.Apply(responseContent)
	}

	// Inject pending KB envelopes — deterministic injection from tool results.
	for _, env := range pendingEnvelopes {
		envelopeBlock := "\n\n```conduit-envelope\n" + env + "\n```"
		responseContent += envelopeBlock
		ch <- StreamEvent{Type: "delta", Content: envelopeBlock}
	}

	// Inject ticket confirmation envelope if the user message contains ticket data.
	// The frontend embeds <!--TICKET_DATA:{...}:TICKET_DATA--> in the user message.
	// This marker is produced by TWO components — both must include it:
	//   - ui/src/components/chat/envelopes/TicketInitFlow.tsx  (quick-action path)
	//   - ui/src/components/chat/envelopes/TicketFormCard.tsx   (agent-emitted ticket-form envelope path)
	if tStart := strings.Index(userContent, "<!--TICKET_DATA:"); tStart >= 0 {
		tail := userContent[tStart+len("<!--TICKET_DATA:"):]
		if tEnd := strings.Index(tail, ":TICKET_DATA-->"); tEnd >= 0 {
			ticketJSON := tail[:tEnd]
			env := BuildTicketConfirmationEnvelope(ticketJSON)
			if env != "" {
				envelopeBlock := "\n\n```conduit-envelope\n" + env + "\n```"
				responseContent += envelopeBlock
				ch <- StreamEvent{Type: "delta", Content: envelopeBlock}
			}
		}
	}

	// Parse envelopes from the response content.
	envelopes, cleanContent, envErrors := ParseEnvelopes(responseContent)

	// Log envelope validation errors.
	for _, envErr := range envErrors {
		log.Printf("chat: envelope error (%s): %s", envErr.Reason, TruncateStr(envErr.Raw, 200))
		e.Store.LogEvent(sessionID, "envelope_error", "warning",
			envErr.Reason, fmt.Sprintf(`{"raw":%q}`, TruncateStr(envErr.Raw, 500)))
	}

	// CLI envelope retry: if there were fatal envelope errors (invalid_json)
	// and this is a CLI session with --resume, send one correction prompt.
	if len(envErrors) > 0 && IsCLIProvider(providerName) {
		hasFatal := false
		for _, envErr := range envErrors {
			if envErr.Reason == "invalid_json" {
				hasFatal = true
				break
			}
		}
		if hasFatal {
			retryEnvelopes := e.retryEnvelopeCorrection(ctx, sessionID, session, prov, model, envErrors, ch)
			envelopes = append(envelopes, retryEnvelopes...)
		}
	}

	var envelopeJSON string
	if len(envelopes) > 0 {
		if data, err := json.Marshal(envelopes); err == nil {
			envelopeJSON = string(data)
		}
	}

	// Build envelope refs from parsed envelopes.
	// Use env.Data (inner payload) — not the whole Envelope struct — to avoid double-wrapping.
	var envRefs []EnvelopeRef
	for _, env := range envelopes {
		innerData, _ := json.Marshal(env.Data)
		envRefs = append(envRefs, EnvelopeRef{Type: env.Type, Data: json.RawMessage(innerData)})
	}

	// Determine tier.
	tier := "default"
	if len(toolCallRefs) > 0 {
		tier = "tool"
	}

	// Check for error envelopes.
	hasError := false
	for _, e := range envRefs {
		if e.Type == "error-report" {
			hasError = true
			break
		}
	}

	// Wrap in structured format.
	structured := WrapResponse(cleanContent, tier, toolCallRefs, envRefs, wasTruncated, hasError)
	LogStructuredWarnings(structured)
	structuredJSON := structured.MarshalContent()

	// Save assistant message to DB.
	assistantMsg := &store.Message{
		ID:        assistantMsgID,
		SessionID: sessionID,
		AgentID:   agent.ID,
		Role:      "assistant",
		Content:   structuredJSON,
		Envelope:  envelopeJSON,
	}
	if err := e.Store.CreateMessage(assistantMsg); err != nil {
		log.Printf("chat: failed to save assistant message: %v", err)
		ch <- ErrorEvent(ErrorCodeInternal, "Failed to save response", map[string]interface{}{"raw": err.Error()})
		return
	}

	// Prune old tool messages after saving.
	if err := e.Broker.PruneAfterTurn(sessionID); err != nil {
		log.Printf("chat: prune after turn failed: %v", err)
	}

	// Record token usage.
	if finalUsage != nil && (finalUsage.InputTokens > 0 || finalUsage.OutputTokens > 0) {
		if err := e.Store.RecordUsage(sessionID, assistantMsgID, model, finalUsage.InputTokens, finalUsage.OutputTokens, finalUsage.CacheCreationTokens, finalUsage.CacheReadTokens); err != nil {
			log.Printf("chat: failed to record token usage: %v", err)
		}
	}

	// Record execution metrics snapshot.
	adapterType := "http"
	if IsPTYProvider(providerName) {
		adapterType = "pty"
	} else if strings.HasPrefix(providerName, "sub-") {
		adapterType = "sub"
	}
	metrics := &store.ExecutionMetrics{
		SessionID:       sessionID,
		MessageID:       assistantMsgID,
		Provider:        providerName,
		Adapter:         adapterType,
		Model:           model,
		AgentID:         agent.ID,
		AgentSlug:       agent.Slug,
		Mode:            mode.Slug,
		DurationMs:      time.Since(startTime).Milliseconds(),
		ContextMessages: len(chatMessages),
		ToolIterations:  iteration,
		ToolCalls:       len(toolCallRefs),
		StopReason:      "",
	}
	if breakdown != nil {
		metrics.ContextTokens = breakdown.Total
	}
	if finalUsage != nil {
		metrics.InputTokens = finalUsage.InputTokens
		metrics.OutputTokens = finalUsage.OutputTokens
		metrics.CacheCreationTokens = finalUsage.CacheCreationTokens
		metrics.CacheReadTokens = finalUsage.CacheReadTokens
		metrics.StopReason = finalUsage.StopReason
	}
	if err := e.Store.RecordExecutionMetrics(metrics); err != nil {
		log.Printf("chat: failed to record execution metrics: %v", err)
	}

	// Emit stream_end with envelope data so the frontend can render immediately.
	ch <- StreamEvent{Type: "stream_end", MessageID: assistantMsgID, Usage: finalUsage, AgentID: agent.ID, Envelope: envelopeJSON}

	// Notify Volon that the response is complete.
	if e.Activity != nil && finalUsage != nil {
		go e.Activity.EmitResponseComplete(ctx, sessionID, agent.ID, model, finalUsage.InputTokens, finalUsage.OutputTokens)
	}
	// Emit plugin event: assistant message received for this turn.
	if e.PluginHost != nil {
		elapsed := time.Since(startTime).Milliseconds()
		contentPreview := string(envelopeJSON)
		if len(contentPreview) > 500 {
			contentPreview = contentPreview[:500]
		}
		go e.PluginHost.EmitMessageReceived(sessionID, assistantMsgID, contentPreview, elapsed)
	}

	// Auto-title: if session has no title, generate one asynchronously.
	if session.Title == "" {
		go e.autoTitle(sessionID, userContent)
	}

	// Auto-tags: generate tags after every response (overwrites previous).
	go e.autoTags(sessionID)
}

// SendAgentMessage allows one agent session to send a message to another session.
// The message is stored with the sending agent's ID and processed as if from a user
// but with agent attribution.
func (e *Engine) SendAgentMessage(fromSessionID, toSessionID, content string) (string, error) {
	// Look up the sending agent.
	var fromAgentID string
	sa, err := e.Store.GetSessionPrimaryAgent(fromSessionID)
	if err != nil {
		fromAgentID = "unknown"
	} else {
		fromAgentID = sa.AgentID
	}

	// Create the message in the target session with agent attribution.
	msg := &store.Message{
		ID:        uuid.New().String(),
		SessionID: toSessionID,
		AgentID:   fromAgentID,
		Role:      "user",
		Content:   content,
		Metadata:  fmt.Sprintf(`{"source":"agent","from_session":"%s","from_agent":"%s"}`, fromSessionID, fromAgentID),
	}
	if err := e.Store.CreateMessage(msg); err != nil {
		return "", fmt.Errorf("create agent message: %w", err)
	}

	// Start async generation in the target session.
	assistantMsgID := uuid.New().String()
	ch := make(chan StreamEvent, 128)
	e.streams.Store(assistantMsgID, ch)
	e.msgToSession.Store(assistantMsgID, toSessionID)
	go e.generateResponse(context.Background(), toSessionID, assistantMsgID, content, ch)

	return assistantMsgID, nil
}

// retryEnvelopeCorrection sends a single correction prompt to the PTY session
// when envelope parsing produced fatal errors (invalid_json). Uses --resume
// to continue the same CLI session. Returns any valid envelopes from the retry.
func (e *Engine) retryEnvelopeCorrection(
	ctx context.Context,
	sessionID string,
	session *store.Session,
	prov provider.Provider,
	model string,
	envErrors []EnvelopeError,
	ch chan StreamEvent,
) []Envelope {
	// Build correction prompt with the first fatal error.
	var errDetail EnvelopeError
	for _, ee := range envErrors {
		if ee.Reason == "invalid_json" {
			errDetail = ee
			break
		}
	}

	correction := fmt.Sprintf(
		"Your previous response contained a malformed envelope block that could not be parsed.\n\n"+
			"Raw content:\n```\n%s\n```\n\n"+
			"Error: %s\n\n"+
			"Please re-emit the envelope as a valid JSON object inside a ```conduit-envelope fenced block "+
			"with kind, version (1), and type fields.",
		TruncateStr(errDetail.Raw, 1000), errDetail.Reason,
	)

	log.Printf("chat: envelope retry for session %s — sending correction prompt", sessionID)
	e.Store.LogEvent(sessionID, "envelope_retry", "info",
		"sending correction prompt", fmt.Sprintf(`{"reason":%q}`, errDetail.Reason))

	// Build context with CLI session ID for --resume.
	retryCtx := ctx
	var meta map[string]any
	if err := json.Unmarshal([]byte(session.Metadata), &meta); err == nil {
		if cliSID, ok := meta["cli_session_id"].(string); ok && cliSID != "" {
			retryCtx = provider.WithCLISessionID(retryCtx, cliSID)
		}
	}
	// Also set sandbox dir if available.
	if sbDir, err := sandbox.Dir(sessionID); err == nil {
		retryCtx = provider.WithSandboxDir(retryCtx, sbDir)
	}

	// Send correction as a streaming call — collect the full response.
	correctionMsgs := []provider.ChatMessage{{Role: "user", Content: correction}}
	retryCh, err := prov.StreamChat(retryCtx, "", correctionMsgs, model)
	if err != nil {
		log.Printf("chat: envelope retry stream error: %v", err)
		return nil
	}

	var retryContent strings.Builder
	for evt := range retryCh {
		switch evt.Type {
		case "delta":
			retryContent.WriteString(evt.Content)
			ch <- StreamEvent{Type: "delta", Content: evt.Content}
		case "error":
			log.Printf("chat: envelope retry error: %s", evt.Error)
			return nil
		}
	}

	// Parse the retry response for envelopes — but don't recurse.
	retryEnvelopes, _, retryErrors := ParseEnvelopes(retryContent.String())
	if len(retryErrors) > 0 {
		log.Printf("chat: envelope retry still had %d errors — giving up", len(retryErrors))
	}
	if len(retryEnvelopes) > 0 {
		log.Printf("chat: envelope retry recovered %d envelope(s)", len(retryEnvelopes))
	}
	return retryEnvelopes
}

// truncateStr truncates a string to maxLen, appending "..." if truncated.
func TruncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// inferProvider maps a model name to a provider when the session has no
// explicit provider set. This handles legacy sessions and prevents sending
// unknown model names to the wrong provider API.
// isCLIProvider returns true if the provider name is any CLI adapter variant
// (PTY bridge or subprocess bridge).
func IsCLIProvider(name string) bool {
	return name == "pty" || strings.HasPrefix(name, "pty-") || strings.HasPrefix(name, "sub-")
}

// maybeCreateAutoArtifact checks if a tool call wrote a file and auto-creates
// an artifact record with origin="auto". Uses AppConfig.Artifacts for tool
// matching and path key extraction.
func (e *Engine) maybeCreateAutoArtifact(sessionID, messageID, agentID string, tu provider.ToolUseBlock) {
	if e.AppConfig == nil {
		return
	}
	artCfg := &e.AppConfig.Artifacts
	if !artCfg.IsAutoDetectTool(tu.Name) {
		return
	}
	filePath := artCfg.ExtractFilePath(tu.Input)
	if filePath == "" {
		return
	}

	// Extract the filename from the path.
	name := filePath
	if idx := strings.LastIndex(filePath, "/"); idx >= 0 {
		name = filePath[idx+1:]
	}

	artifact := &store.Artifact{
		SessionID:        sessionID,
		MessageID:        messageID,
		Name:             name,
		MimeType:         "application/octet-stream", // could be refined later
		StoragePath:      filePath,
		Origin:           store.ArtifactOriginAuto,
		SourceToolCallID: tu.ID,
		SourceAgentID:    agentID,
	}
	if err := e.Store.CreateArtifact(artifact); err != nil {
		log.Printf("chat: auto-artifact creation failed for %s: %v", filePath, err)
	} else {
		log.Printf("chat: auto-artifact created: %s (tool=%s, session=%s)", name, tu.Name, sessionID)
	}
}

// PlaceArtifact creates an artifact with origin="placed" for tools/plugins
// that want to deliberately surface a file to the user.
func (e *Engine) PlaceArtifact(sessionID, messageID, agentID, name, mimeType, storagePath string) (*store.Artifact, error) {
	artifact := &store.Artifact{
		SessionID:     sessionID,
		MessageID:     messageID,
		Name:          name,
		MimeType:      mimeType,
		StoragePath:   storagePath,
		Origin:        store.ArtifactOriginPlaced,
		SourceAgentID: agentID,
	}
	if err := e.Store.CreateArtifact(artifact); err != nil {
		return nil, fmt.Errorf("place artifact: %w", err)
	}
	return artifact, nil
}

// isPTYProvider returns true if the provider name is any PTY adapter variant.
func IsPTYProvider(name string) bool {
	return name == "pty" || strings.HasPrefix(name, "pty-")
}

// resolveProvider walks the provider fallback chain and returns the first
// available provider. Resolution order:
//  1. sessionProvider (explicit per-session)
//  2. agentProvider (agent profile default)
//  3. User's fallback chain (from user_settings, first registered wins)
//  4. InferProvider(model) — map model name to provider
//  5. System default ("anthropic")
//
// Returns the provider name and the Provider, or ("name", nil) if none available.
func (e *Engine) resolveProvider(sessionProvider, agentProvider, model string) (string, provider.Provider) {
	// 1. Session-level override — highest priority.
	if sessionProvider != "" {
		if p, ok := e.Providers.Get(sessionProvider); ok {
			return sessionProvider, p
		}
		log.Printf("chat: session provider %q not registered, falling through", sessionProvider)
	}

	// 2. Agent's preferred provider.
	if agentProvider != "" {
		if p, ok := e.Providers.Get(agentProvider); ok {
			return agentProvider, p
		}
		log.Printf("chat: agent provider %q not registered, falling through", agentProvider)
	}

	// 3. User's fallback chain.
	if us, err := e.Store.GetUserSettings(); err == nil && len(us.ProviderFallbackChain) > 0 {
		for _, name := range us.ProviderFallbackChain {
			if p, ok := e.Providers.Get(name); ok {
				return name, p
			}
		}
		log.Printf("chat: no provider in user fallback chain is registered, falling through")
	}

	// 4. Infer from model name.
	inferred := InferProvider(model)
	if p, ok := e.Providers.Get(inferred); ok {
		return inferred, p
	}

	// 5. System default.
	if p, ok := e.Providers.Get("anthropic"); ok {
		return "anthropic", p
	}

	return inferred, nil
}

func InferProvider(model string) string {
	switch {
	case model == "claude-cli":
		return "pty"
	case model == "codex-cli":
		return "pty-codex"
	case model == "gemini-cli":
		return "pty-gemini"
	case strings.HasPrefix(model, "gpt-") || strings.HasPrefix(model, "o1-") || strings.HasPrefix(model, "o3-"):
		return "openai"
	case strings.HasPrefix(model, "llama") || strings.HasPrefix(model, "mistral") || strings.HasPrefix(model, "gemma"):
		return "ollama"
	default:
		return "anthropic"
	}
}

// autoTitle generates a title for a session from the first user message.
// recordUtilityMetrics records an execution metric for a utility call (autoTitle, autoTags, etc.).
func (e *Engine) recordUtilityMetrics(sessionID, callType string, duration time.Duration, callErr error) {
	errMsg := ""
	if callErr != nil {
		errMsg = callErr.Error()
	}
	m := &store.ExecutionMetrics{
		SessionID:  sessionID,
		MessageID:  callType, // use call type as message ID for utility calls
		Provider:   e.UtilityProvider,
		Adapter:    "http",
		Model:      e.UtilityModel,
		DurationMs: duration.Milliseconds(),
		IsUtility:  true,
		Error:      errMsg,
	}
	if err := e.Store.RecordExecutionMetrics(m); err != nil {
		log.Printf("chat: failed to record utility metrics for %s: %v", callType, err)
	}
}

func (e *Engine) autoTitle(sessionID, userContent string) {
	ctx, span := feotel.StartSpan(context.Background(), "conduit.autoTitle")
	span.SetAttributes(attribute.String("conduit.session.id", sessionID))
	defer span.End()
	_ = ctx

	prov, ok := e.Providers.Get(e.UtilityProvider)
	if !ok {
		return
	}

	prompt := "Generate a concise 3-5 word title for this conversation. Respond with ONLY the title, no quotes or punctuation."
	msgs := []provider.ChatMessage{
		{Role: "user", Content: fmt.Sprintf("First message: %s", userContent)},
	}

	start := time.Now()
	title, err := prov.Complete(context.Background(), prompt, msgs, e.UtilityModel)
	duration := time.Since(start)

	e.recordUtilityMetrics(sessionID, "autoTitle", duration, err)

	if err != nil {
		log.Printf("chat: auto-title failed: %v", err)
		return
	}

	title = strings.TrimSpace(title)
	if title == "" {
		return
	}

	sess, err := e.Store.GetSession(sessionID)
	if err != nil {
		log.Printf("chat: auto-title get session failed: %v", err)
		return
	}
	sess.Title = title
	if err := e.Store.UpdateSession(sess); err != nil {
		log.Printf("chat: auto-title update failed: %v", err)
	}
}

// autoTags generates 2-5 tags for a session based on recent messages.
func (e *Engine) autoTags(sessionID string) {
	prov, ok := e.Providers.Get(e.UtilityProvider)
	if !ok {
		return
	}

	// Fetch last 10 messages for context.
	msgs, err := e.Store.ListMessages(sessionID, 10)
	if err != nil {
		log.Printf("chat: auto-tags list messages failed: %v", err)
		return
	}
	if len(msgs) < 2 {
		return // Need at least a user+assistant exchange.
	}

	// Build a digest of the conversation.
	var sb strings.Builder
	for _, m := range msgs {
		if m.Role == "user" || m.Role == "assistant" {
			content := m.Content
			if len(content) > 300 {
				content = content[:300]
			}
			fmt.Fprintf(&sb, "%s: %s\n", m.Role, content)
		}
	}

	prompt := "Generate 2-5 short tags (1-2 words each, lowercase) that describe this conversation's topics. Return ONLY a JSON array of strings, e.g. [\"go\",\"refactoring\",\"api design\"]. No explanation."
	tagMsgs := []provider.ChatMessage{
		{Role: "user", Content: sb.String()},
	}

	start := time.Now()
	raw, err := prov.Complete(context.Background(), prompt, tagMsgs, e.UtilityModel)
	duration := time.Since(start)

	e.recordUtilityMetrics(sessionID, "autoTags", duration, err)

	if err != nil {
		log.Printf("chat: auto-tags generation failed: %v", err)
		return
	}

	raw = strings.TrimSpace(raw)

	// Validate it's a JSON array of strings.
	var tags []string
	if err := json.Unmarshal([]byte(raw), &tags); err != nil {
		log.Printf("chat: auto-tags parse failed: %v (raw: %s)", err, raw)
		return
	}
	if len(tags) == 0 || len(tags) > 5 {
		return
	}

	tagsJSON, _ := json.Marshal(tags)
	if err := e.Store.UpdateSessionTags(sessionID, string(tagsJSON)); err != nil {
		log.Printf("chat: auto-tags update failed: %v", err)
	}
}

// intentStopWords are common words filtered out during intent extraction.
var intentStopWords = map[string]bool{
	"a": true, "an": true, "the": true, "is": true, "are": true, "was": true,
	"were": true, "be": true, "been": true, "being": true, "have": true,
	"has": true, "had": true, "do": true, "does": true, "did": true,
	"will": true, "would": true, "could": true, "should": true, "may": true,
	"might": true, "shall": true, "can": true, "to": true, "of": true,
	"in": true, "for": true, "on": true, "with": true, "at": true,
	"by": true, "from": true, "as": true, "into": true, "about": true,
	"that": true, "this": true, "it": true, "its": true, "i": true,
	"me": true, "my": true, "we": true, "our": true, "you": true,
	"your": true, "he": true, "she": true, "they": true, "them": true,
	"and": true, "or": true, "but": true, "not": true, "no": true,
	"if": true, "then": true, "so": true, "just": true, "also": true,
	"very": true, "too": true, "some": true, "any": true, "all": true,
	"what": true, "how": true, "when": true, "where": true, "which": true,
	"who": true, "why": true, "please": true, "thanks": true, "hi": true,
	"hello": true, "hey": true, "like": true, "want": true, "need": true,
	"thing": true, "things": true, "make": true, "let": true, "get": true,
}

// ExtractIntent derives an intent string and keyword hints from a user message.
// It extracts meaningful words (skipping stop words and short tokens) and
// returns a short intent phrase plus up to 10 keyword hints.
func ExtractIntent(userMessage string) (intent string, hints []string) {
	// Normalize: lowercase, replace common punctuation with spaces.
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

	// Build a short intent from the first 3 keywords.
	intentWords := keywords
	if len(intentWords) > 3 {
		intentWords = intentWords[:3]
	}
	intent = strings.Join(intentWords, " ")

	return intent, keywords
}

// toolSelection holds the result of tool selection, including progressive discovery info.
type toolSelection struct {
	Tools       []provider.ToolDefinition // tools to send to the LLM
	Catalog     string                    // non-empty when progressive discovery is active
	Progressive bool                      // true when using progressive discovery
}

// getToolsForAgent returns the tool list for an agent.
// When the total tool count exceeds ProgressiveDiscoveryThreshold, progressive
// discovery is used: only the request_tools meta-tool is sent, and a compact
// catalog is injected into the system prompt.
func (e *Engine) getToolsForAgent(ctx context.Context, agentID, userMessage, workspaceID string) toolSelection {
	// Extract intent from user message instead of passing raw content or wildcard.
	intent, hints := ExtractIntent(userMessage)
	log.Printf("chat: extracted intent=%q hints=%v from user message", intent, hints)

	// Get all broker-selected tools.
	var allTools []provider.ToolDefinition
	seen := map[string]bool{} // dedup: Anthropic API rejects duplicate tool names
	if e.ToolClient != nil {
		selected, err := e.ToolClient.SelectToolsAsProvider(ctx, intent, hints, workspaceID, agentID)
		if err != nil {
			log.Printf("chat: tool client selection failed: %v — falling back to MCP manager", err)
		} else {
			for _, t := range selected {
				if !seen[t.Name] {
					seen[t.Name] = true
					allTools = append(allTools, t)
				}
			}
		}
	}
	// If no MCP tools were selected but the agent has configured MCP servers,
	// fall back to direct discovery from those servers. This handles plugin-registered
	// servers that the broker's intent matching doesn't know about.
	mcpCount := 0
	for _, t := range allTools {
		if strings.HasPrefix(t.Name, "mcp__") {
			mcpCount++
		}
	}
	if mcpCount == 0 && e.MCPManager != nil && e.Store != nil {
		agent, agentErr := e.Store.GetAgent(agentID)
		if agentErr == nil {
			var servers []string
			json.Unmarshal([]byte(agent.MCPServers), &servers)
			for _, srv := range servers {
				srvTools, err := e.MCPManager.DiscoverServerTools(ctx, srv)
				if err != nil {
					continue
				}
				for _, t := range srvTools {
					name := fmt.Sprintf("mcp__%s__%s", srv, t.Name)
					if seen[name] {
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
			if len(allTools) > mcpCount {
				log.Printf("chat: direct MCP discovery added %d tools for agent %s from configured servers", len(allTools)-mcpCount, agentID)
			}
		}
	}

	// Filter tools by agent.Tools allowlist (schema v2).
	// Empty list ("[]") means no filtering — all tools available.
	if agent, agErr := e.Store.GetAgent(agentID); agErr == nil {
		allTools = filterToolsByAgentAllowlist(allTools, agent.Tools)
	}

	if len(allTools) == 0 {
		log.Printf("chat: WARNING broker returned 0 tools for agent %s — proceeding without tools (LLM can still respond)", agentID)
	} else {
		log.Printf("chat: broker selected %d tools for agent %s (~%d tool tokens)", len(allTools), agentID, EstimateToolDefTokens(allTools))
	}

	// Check if progressive discovery should be used.
	// Count only non-builtin (MCP) tools — builtins are always present and shouldn't
	// trigger progressive discovery on their own.
	mcpToolCount := 0
	for _, t := range allTools {
		if strings.HasPrefix(t.Name, "mcp__") {
			mcpToolCount++
		}
	}
	if mcpToolCount > ProgressiveDiscoveryThreshold && e.ToolClient != nil {
		summaries := e.ToolClient.ListToolSummaries()
		catalog := BuildToolCatalog(summaries)
		// Keep builtin (non-MCP) tools alongside request_tools — they're small
		// and should always be available without progressive discovery lookup.
		builtinTools := []provider.ToolDefinition{requestToolsDef}
		for _, t := range allTools {
			if !strings.HasPrefix(t.Name, "mcp__") {
				builtinTools = append(builtinTools, t)
			}
		}
		log.Printf("chat: progressive discovery active — %d MCP tools (threshold %d), %d builtins kept, %d tools in catalog",
			mcpToolCount, ProgressiveDiscoveryThreshold, len(builtinTools)-1, len(summaries))
		return toolSelection{
			Tools:       builtinTools,
			Catalog:     catalog,
			Progressive: true,
		}
	}

	return toolSelection{
		Tools: allTools,
	}
}

// filterToolsByAgentAllowlist removes tools not matching the agent's tools allowlist.
// An empty or "[]" allowlist means no filtering (all tools pass).
func filterToolsByAgentAllowlist(tools []provider.ToolDefinition, allowlistJSON string) []provider.ToolDefinition {
	if allowlistJSON == "" || allowlistJSON == "[]" {
		return tools
	}
	var allowlist []string
	if err := json.Unmarshal([]byte(allowlistJSON), &allowlist); err != nil || len(allowlist) == 0 {
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
	log.Printf("chat: agent tools allowlist filtered %d → %d tools", len(tools), len(filtered))
	return filtered
}

// RetryLastMessage resets the circuit breaker and re-triggers generation for a session.
// It finds the last user message and re-generates a response.
func (e *Engine) RetryLastMessage(sessionID string) (string, error) {
	// Reset the circuit breaker on the session's provider (if applicable).
	session, err := e.Store.GetSession(sessionID)
	if err == nil {
		provName := session.Provider
		if provName == "" {
			provName = "anthropic"
		}
		if prov, ok := e.Providers.Get(provName); ok {
			if ap, ok := prov.(*provider.Anthropic); ok && ap.CircuitBreaker != nil {
				ap.CircuitBreaker.Reset()
				log.Printf("chat: circuit breaker reset for retry on session %s", sessionID)
			}
		}
	}

	// Find the last user message in this session.
	allMsgs, err := e.Store.ListMessages(sessionID, 50)
	if err != nil {
		return "", fmt.Errorf("list messages for retry: %w", err)
	}

	var userContent string
	for i := len(allMsgs) - 1; i >= 0; i-- {
		if allMsgs[i].Role == "user" {
			userContent = allMsgs[i].Content
			break
		}
	}
	if userContent == "" {
		return "", fmt.Errorf("no user message found in session %s", sessionID)
	}

	// Create a new assistant message and stream channel.
	assistantMsgID := uuid.New().String()
	ch := make(chan StreamEvent, 128)
	e.streams.Store(assistantMsgID, ch)
	e.msgToSession.Store(assistantMsgID, sessionID)

	go e.generateResponse(context.Background(), sessionID, assistantMsgID, userContent, ch)

	return assistantMsgID, nil
}

// RecomposeSystemPrompt recomposes the system prompt after a mode change mid-session.
// This is called when the mode is switched via the API.
func (e *Engine) RecomposeSystemPrompt(sessionID, agentID, newMode string) (string, error) {
	agent, err := e.Store.GetAgent(agentID)
	if err != nil {
		return "", fmt.Errorf("get agent: %w", err)
	}

	mode, err := e.Store.GetAgentMode(agentID, newMode)
	if err != nil {
		log.Printf("chat: mode %s not found for agent %s, using empty mode", newMode, agentID)
		mode = &store.AgentMode{}
	}

	session, err := e.Store.GetSession(sessionID)
	if err != nil {
		return "", fmt.Errorf("get session: %w", err)
	}

	var workspace *store.Workspace
	if session.WorkspaceID != "" {
		workspace, _ = e.Store.GetWorkspace(session.WorkspaceID)
	}

	skillList := buildSkillList(e.Store, agentID)
	prompt := assembleSystemPromptFromTemplates(e.Store, agent, mode, workspace, skillList)
	return prompt, nil
}
