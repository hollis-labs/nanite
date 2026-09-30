package service

import (
	"context"

	"github.com/hollis-labs/nanite/internal/store"
)

// SessionRuntimeStore is the store surface SessionRuntimeService reads.
type SessionRuntimeStore interface {
	GetSessionHalt(ctx context.Context, sessionID string) (*store.HaltStatus, error)
	ListAgentRuntimeRowsForSession(ctx context.Context, sessionID string) ([]*store.AgentRuntimeRow, error)
}

// SessionRuntimeService reads a session's runtime state: whether it is
// halted and the agent runtimes bound to it. It is read-only and a
// pass-through; halting and runtime rows are written elsewhere.
type SessionRuntimeService struct {
	store SessionRuntimeStore
}

func NewSessionRuntimeService(st SessionRuntimeStore) *SessionRuntimeService {
	return &SessionRuntimeService{store: st}
}

// Halt returns the session's halt status.
func (s *SessionRuntimeService) Halt(ctx context.Context, sessionID string) (*store.HaltStatus, error) {
	return s.store.GetSessionHalt(ctx, sessionID)
}

// RuntimeRows returns the agent runtime rows bound to the session, most
// recent first.
func (s *SessionRuntimeService) RuntimeRows(ctx context.Context, sessionID string) ([]*store.AgentRuntimeRow, error) {
	return s.store.ListAgentRuntimeRowsForSession(ctx, sessionID)
}
