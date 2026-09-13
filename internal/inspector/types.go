// Package inspector provides the I1 per-turn event aggregator for the
// developer-mode context inspector (CW-20260426-0004, Phase 8).
//
// All data is ephemeral — stored in an in-memory ring buffer keyed by
// session_id. Nothing is persisted to SQLite; this is debug-mode only.
package inspector

import "time"

// TurnSnapshot is the per-turn aggregate surfaced by the inspector.
// Producers call Service.Record* to fill it; consumers read via Snapshot().
type TurnSnapshot struct {
	SessionID string    `json:"session_id"`
	TurnID    string    `json:"turn_id"` // monotonic sequence per session, e.g. "1", "2"
	StartedAt time.Time `json:"started_at"`

	// Context slots — always 8 entries, empty slots have zero tokens.
	Slots []SlotSnapshot `json:"slots"`

	// LLM-facing messages (per CW-20260419-0013 inventory).
	LLMMessages []LLMMessageRecord `json:"llm_messages"`

	// Broker decisions (request_tools intents + loaded tools + outcome).
	BrokerDecisions []BrokerDecision `json:"broker_decisions"`

	// Tool calls — full args, result, latency, cache state.
	ToolCalls []ToolCallRecord `json:"tool_calls"`

	// Playbook / Memory — nil until producers wire up.
	Playbook   *PlaybookRecord `json:"playbook,omitempty"`
	MemoryHits []MemoryRecord  `json:"memory_hits,omitempty"`

	// ScopeTier classification (B2) — empty until producer wires up.
	ScopeTier string `json:"scope_tier,omitempty"`

	// LoopStatus — nil until I2 (loop detection) wires up.
	LoopStatus *LoopRecord `json:"loop_status,omitempty"`

	// Reminders shows the reminder activity for this turn (J11, CW-20260426-0009).
	// Nil until the reminder engine wires up its producer.
	Reminders *RemindersRecord `json:"reminders,omitempty"`
}

// SlotSnapshot describes one context window slot.
//
// The 8 canonical slot names are: system, memory, agent, rules, tools,
// session, context, conversation — defined by context.SlotOrder.
type SlotSnapshot struct {
	// Name is one of the 8 canonical slot identifiers.
	Name string `json:"name"`
	// Tokens is the estimated token count for this slot's content.
	Tokens int `json:"tokens"`
	// Cached is true when the slot's cache key matched the previous turn.
	Cached bool `json:"cached"`
	// CacheKey is the SHA-256 of the slot content.
	CacheKey string `json:"cache_key,omitempty"`
	// Sensitive marks slots that contain personally identifying or agent
	// identity content. The frontend redacts these unless the user toggles
	// "Reveal sensitive". Sensitive=true does NOT prevent the server from
	// returning raw content — redaction is a frontend concern.
	Sensitive bool `json:"sensitive"`
	// Content is the raw slot text. Frontend redacts when Sensitive=true and
	// reveal is off. Never omitted in the API response.
	Content string `json:"content"`
	// TrafficLight is "green" (cached), "yellow" (changed/partial), or "red"
	// (empty). Computed by the service when the slot is recorded.
	TrafficLight string `json:"traffic_light"`
}

// LLMMessageRecord is one internal-classified message visible to the LLM.
type LLMMessageRecord struct {
	// Role is "system", "user", "assistant", or "tool".
	Role string `json:"role"`
	// Content is the message text.
	Content string `json:"content"`
	// Tokens is the estimated token count.
	Tokens int `json:"tokens"`
	// Classification tags this message with its internal origin
	// (e.g. "user_turn", "tool_result", "envelope_response").
	Classification string `json:"classification,omitempty"`
}

// BrokerDecision is one request_tools call captured from the broker pipeline.
type BrokerDecision struct {
	// Intent is the LLM-supplied intent string from the request_tools call.
	Intent string `json:"intent"`
	// Outcome is one of: selected, loaded, empty, halted, reflected.
	Outcome string `json:"outcome"`
	// SelectedTools is the list of tool names returned to the LLM.
	SelectedTools []string `json:"selected_tools"`
	// LayerReached is the farthest layer the broker queried.
	LayerReached string `json:"layer_reached"`
	// ConsecutiveEmpty is the run length of empty broker responses.
	ConsecutiveEmpty int `json:"consecutive_empty"`
	// TotalCalls is the count of request_tools calls this turn.
	TotalCalls int `json:"total_calls"`
	// LoadedCount is the total number of tools currently loaded for the turn.
	LoadedCount int `json:"loaded_count"`
	// ReflectionQuery is the restated goal when outcome == "reflected".
	ReflectionQuery string `json:"reflection_query,omitempty"`
	// Signals is the diagnostic JSON blob from the broker ranking layer.
	Signals string `json:"signals,omitempty"`
}

// ToolCallRecord describes one MCP / self-tool invocation.
type ToolCallRecord struct {
	// ToolID is the provider-assigned tool-use block ID.
	ToolID string `json:"tool_id"`
	// Name is the tool name.
	Name string `json:"name"`
	// Arguments is the raw JSON of the input map.
	Arguments string `json:"arguments"`
	// Result is the full tool output text.
	Result        string  `json:"result"`
	VisibleResult *string `json:"visible_result,omitempty"`
	CacheID       string  `json:"cache_id,omitempty"`
	PreviewFormat string  `json:"preview_format,omitempty"`
	OriginalBytes int     `json:"original_bytes,omitempty"`
	VisibleBytes  int     `json:"visible_bytes,omitempty"`
	BudgetBytes   int     `json:"budget_bytes,omitempty"`
	// IsError is true when the tool returned an error result.
	IsError bool `json:"is_error"`
	// LatencyMs is the wall-clock time for the tool call in milliseconds.
	LatencyMs int64 `json:"latency_ms"`
	// CacheState is inline, cached, retrieval, or n/a for older producers.
	CacheState string `json:"cache_state"`
}

// PlaybookRecord holds the playbook consulted for this turn (E1/E2).
// Nil until the playbook producer wires up.
type PlaybookRecord struct {
	// Name is the matched playbook name.
	Name string `json:"name"`
	// Steps is the list of step descriptions.
	Steps []string `json:"steps,omitempty"`
}

// MemoryRecord is one memory item consulted during context assembly.
// Empty until the memory-hit producer wires up.
type MemoryRecord struct {
	// Source is "memory" or the broker source tag.
	Source string `json:"source"`
	// Content is the recalled memory text.
	Content string `json:"content"`
	// Score is the relevance score from the broker.
	Score float64 `json:"score,omitempty"`
}

// LoopRecord holds loop-detection status (I2).
// Nil until I2 wires up.
type LoopRecord struct {
	// Detected is true when a loop was detected.
	Detected bool `json:"detected"`
	// Reason describes the detection signal.
	Reason string `json:"reason,omitempty"`
}

// RemindersRecord holds reminder activity for one turn (J11, CW-20260426-0009).
type RemindersRecord struct {
	// SetThisTurn lists reminders created by the agent during this turn.
	SetThisTurn []ReminderItem `json:"set_this_turn,omitempty"`
	// FiredThisTurn lists reminders whose trigger condition fired this turn.
	FiredThisTurn []ReminderItem `json:"fired_this_turn,omitempty"`
}

// ReminderItem is one reminder entry in the inspector display.
type ReminderItem struct {
	// ID is the reminder row ID.
	ID string `json:"id"`
	// Text is the reminder message text.
	Text string `json:"text"`
	// TriggerJSON is the raw trigger shape for display.
	TriggerJSON string `json:"trigger_json"`
	// Scope is the reminder's scope (turn / session / project). D1/D2.
	Scope string `json:"scope,omitempty"`
}
