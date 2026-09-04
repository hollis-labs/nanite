package chat

import (
	"encoding/json"
	"time"

	"github.com/hollis-labs/nanite/internal/store"
)

// AutoRecall settings live in agent_profiles.settings JSON, alongside the
// existing `debug` flag (see isAgentDebugEnabled at internal/service/chat_generate.go).
// Storing in JSON keeps the agent_profiles schema stable; adding a column
// would force a migration for what is effectively a per-profile feature
// dial. Field names mirror the documented contract:
//
//	{ "auto_recall": false, "auto_recall_limit": 10, "auto_recall_min_confidence": 0.5 }
//
// Absent/unset → defaults below. Defaults are chosen to preserve the
// pre-existing MemorySource behavior so unconfigured profiles see no change.
const (
	// DefaultAutoRecallEnabled is the per-turn auto-recall default for any
	// agent profile that doesn't pin the field. Matches the prior implicit
	// behavior (MemorySource registered → memory queried every turn).
	DefaultAutoRecallEnabled = true
	// DefaultAutoRecallLimit matches MemorySource's prior hardcoded limit.
	DefaultAutoRecallLimit = 30
	// DefaultAutoRecallMinConfidence matches MemorySource's prior hardcoded
	// MinConfidence. The implementer prompt suggested 0.5; we keep 0.4 for
	// continuity and let profiles raise the floor.
	DefaultAutoRecallMinConfidence = 0.4
	// DefaultAutoRecallTimeout caps the Tesseract round-trip per turn. Memory
	// is enrichment, not identity — better to render an empty slot than
	// hold the chat loop waiting on a slow recall.
	DefaultAutoRecallTimeout = 2 * time.Second
)

// AutoRecallConfig is the resolved per-agent recall configuration applied
// each time the chat-harness assembles slot sources.
type AutoRecallConfig struct {
	Enabled       bool
	Limit         int
	MinConfidence float64
	Timeout       time.Duration
}

// agentAutoRecallSettingsJSON is the wire shape inside agent.Settings. Only
// these keys are read; unknown keys are ignored. Pointer fields distinguish
// "absent" (use default) from "explicitly zero" (caller meant zero).
type agentAutoRecallSettingsJSON struct {
	AutoRecall              *bool    `json:"auto_recall,omitempty"`
	AutoRecallLimit         *int     `json:"auto_recall_limit,omitempty"`
	AutoRecallMinConfidence *float64 `json:"auto_recall_min_confidence,omitempty"`
}

// ResolveAutoRecallConfig parses an AgentProfile's Settings JSON for
// auto-recall fields, applying defaults for any missing/invalid value.
// A nil profile or empty Settings returns the defaults.
func ResolveAutoRecallConfig(agent *store.AgentProfile) AutoRecallConfig {
	cfg := AutoRecallConfig{
		Enabled:       DefaultAutoRecallEnabled,
		Limit:         DefaultAutoRecallLimit,
		MinConfidence: DefaultAutoRecallMinConfidence,
		Timeout:       DefaultAutoRecallTimeout,
	}
	if agent == nil || agent.Settings == "" {
		return cfg
	}
	var parsed agentAutoRecallSettingsJSON
	if err := json.Unmarshal([]byte(agent.Settings), &parsed); err != nil {
		// Malformed settings → fall back to defaults silently. The caller
		// already logs Settings parse failures elsewhere (debug flag path).
		return cfg
	}
	if parsed.AutoRecall != nil {
		cfg.Enabled = *parsed.AutoRecall
	}
	if parsed.AutoRecallLimit != nil && *parsed.AutoRecallLimit > 0 {
		cfg.Limit = *parsed.AutoRecallLimit
	}
	if parsed.AutoRecallMinConfidence != nil && *parsed.AutoRecallMinConfidence >= 0 && *parsed.AutoRecallMinConfidence <= 1 {
		cfg.MinConfidence = *parsed.AutoRecallMinConfidence
	}
	return cfg
}
