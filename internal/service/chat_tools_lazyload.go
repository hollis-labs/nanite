package service

import (
	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/nanite/internal/chat"
)

// loadToolPartitionState returns the previous-turn partition state for a
// session, or the zero value when no state has been stored yet.
func (s *chatServiceImpl) loadToolPartitionState(sessionID string) chat.ToolPartitionState {
	if sessionID == "" {
		return chat.ToolPartitionState{}
	}
	if v, ok := s.toolPartitionStates.Load(sessionID); ok {
		if st, ok := v.(chat.ToolPartitionState); ok {
			return st
		}
	}
	return chat.ToolPartitionState{}
}

// storeToolPartitionState persists this turn's partition state so the next
// turn can apply hysteresis. Empty sessionID is a no-op (defensive — partition
// activation is gated upstream and shouldn't fire without a session).
func (s *chatServiceImpl) storeToolPartitionState(sessionID string, st chat.ToolPartitionState) {
	if sessionID == "" {
		return
	}
	s.toolPartitionStates.Store(sessionID, st)
}

// containsToolNamed reports whether a tool with the given name is already in
// the slice. Used to keep the request_tools meta-tool insertion idempotent
// when LazyLoad partitions a tool surface that already includes it.
func containsToolNamed(tools []llmtypes.ToolDefinition, name string) bool {
	for _, t := range tools {
		if t.Name == name {
			return true
		}
	}
	return false
}
