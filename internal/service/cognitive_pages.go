package service

import (
	"context"
	"errors"

	"github.com/hollis-labs/nanite/internal/store"
)

// CognitivePages is the bounded transcript read seam used by the agent API.
// Hosts without this storage capability refuse it explicitly.
type CognitivePages interface {
	ListCognitiveSessionPage(context.Context, string, string, int, bool) ([]store.Session, error)
	ListCognitiveMessagePage(context.Context, string, string, string, int) ([]store.Message, error)
}

var ErrCognitivePagesUnavailable = errors.New("bounded cognitive history is unavailable")

func (s *sessionServiceImpl) ListCognitiveSessionPage(ctx context.Context, beforeCreated, beforeID string, limit int, includeArchived bool) ([]store.Session, error) {
	p, ok := s.sessions.(CognitivePages)
	if !ok {
		return nil, ErrCognitivePagesUnavailable
	}
	return p.ListCognitiveSessionPage(ctx, beforeCreated, beforeID, limit, includeArchived)
}
func (s *sessionServiceImpl) ListCognitiveMessagePage(ctx context.Context, view, beforeCreated, beforeID string, limit int) ([]store.Message, error) {
	p, ok := s.sessions.(CognitivePages)
	if !ok {
		return nil, ErrCognitivePagesUnavailable
	}
	return p.ListCognitiveMessagePage(ctx, view, beforeCreated, beforeID, limit)
}
