package pluginapi

// QuerySessionsData contains recent permitted session metadata. More signals
// that the requested limit omitted additional permitted rows. It never carries
// messages, context prompts or arbitrary session metadata.
type QuerySessionsData struct {
	Sessions []QuerySession `json:"sessions"`
	More     bool           `json:"more"`
}

type QuerySession struct {
	ID         string `json:"id"`
	ShortCode  string `json:"short_code"`
	Title      string `json:"title"`
	CustomName string `json:"custom_name"`
	ProjectID  string `json:"project_id"`
	Provider   string `json:"provider"`
	Model      string `json:"model"`
	Status     string `json:"status"`
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
}

// QueryUsageData is token and cost accounting for the requested session.
type QueryUsageData struct {
	InputTokens         int     `json:"input_tokens"`
	OutputTokens        int     `json:"output_tokens"`
	TotalTokens         int     `json:"total_tokens"`
	ToolInputTokens     int     `json:"tool_input_tokens"`
	CacheCreationTokens int     `json:"cache_creation_tokens"`
	CacheReadTokens     int     `json:"cache_read_tokens"`
	EstimatedCostUSD    float64 `json:"estimated_cost_usd"`
	MessageCount        int     `json:"message_count"`
}

type QueryMetricsData struct {
	Metrics []QueryMetric `json:"metrics"`
	More    bool          `json:"more"`
}

// QueryMetric exposes accounting and runtime identity. Raw error text, debug
// snapshots, tool arguments, prompts and configuration are excluded.
type QueryMetric struct {
	ID                  int64   `json:"id"`
	SessionID           string  `json:"session_id"`
	MessageID           string  `json:"message_id"`
	Provider            string  `json:"provider"`
	Adapter             string  `json:"adapter"`
	Model               string  `json:"model"`
	AgentID             string  `json:"agent_id"`
	AgentSlug           string  `json:"agent_slug"`
	Mode                string  `json:"mode"`
	DurationMs          int64   `json:"duration_ms"`
	ContextMessages     int     `json:"context_messages"`
	ContextTokens       int     `json:"context_tokens"`
	InputTokens         int     `json:"input_tokens"`
	OutputTokens        int     `json:"output_tokens"`
	CacheCreationTokens int     `json:"cache_creation_tokens"`
	CacheReadTokens     int     `json:"cache_read_tokens"`
	EstimatedCostUSD    float64 `json:"estimated_cost_usd"`
	ToolIterations      int     `json:"tool_iterations"`
	ToolCalls           int     `json:"tool_calls"`
	IsUtility           bool    `json:"is_utility"`
	StopReason          string  `json:"stop_reason"`
	Failed              bool    `json:"failed"`
	ProfileName         string  `json:"profile_name,omitempty"`
	ProfileDigest       string  `json:"profile_digest,omitempty"`
	CreatedAt           string  `json:"created_at"`
}

// QuerySlotsData reports the most recent captured context slots, not a fresh
// assembly (which could execute resolvers). Available=false means no captured
// snapshot exists, including when the host inspector is disabled.
type QuerySlotsData struct {
	Available bool        `json:"available"`
	TurnID    string      `json:"turn_id,omitempty"`
	StartedAt string      `json:"started_at,omitempty"`
	Slots     []QuerySlot `json:"slots"`
}

type QuerySlot struct {
	Name         string `json:"name"`
	Tokens       int    `json:"tokens"`
	Cached       bool   `json:"cached"`
	CacheKey     string `json:"cache_key,omitempty"`
	Sensitive    bool   `json:"sensitive"`
	TrafficLight string `json:"traffic_light"`
	// Content is present only when the reviewed scope permits IncludeContent.
	Content string `json:"content,omitempty"`
}
