package service

import (
	"context"

	"github.com/hollis-labs/nanite/internal/selftools"
	"github.com/hollis-labs/nanite/internal/store"
)

// NewSelfToolsTransport wires both daemon and standalone MCP tools through
// domain services without importing service back into selftools.
func NewSelfToolsTransport(st *store.Store) *selftools.SelfToolsTransport {
	return selftools.NewSelfToolsTransport(st, selftools.ReadServices{
		Skills:     NewSkillService(SkillServiceConfig{Skills: st}),
		Sessions:   &selfToolSessionReader{sessions: NewSessionService(SessionServiceDeps{Sessions: st}), shortCodes: st},
		Procedures: NewAgentCapabilitiesService(st),
		Handoffs:   NewHandoffService(st),
	})
}

type selfToolSessionReader struct {
	sessions   SessionService
	shortCodes interface {
		GetSessionByShortCode(context.Context, string) (*store.Session, error)
		GetSession(context.Context, string) (*store.Session, error)
	}
}

func (s *selfToolSessionReader) Get(ctx context.Context, id string) (*store.Session, error) {
	return s.shortCodes.GetSession(ctx, id)
}
func (s *selfToolSessionReader) GetByShortCode(ctx context.Context, code string) (*store.Session, error) {
	return s.shortCodes.GetSessionByShortCode(ctx, code)
}
func (s *selfToolSessionReader) ListMessages(ctx context.Context, id string, limit int) ([]store.Message, error) {
	return s.sessions.ListMessages(ctx, id, limit)
}
func (s *selfToolSessionReader) ListMessagesPage(ctx context.Context, id string, limit, offset int) (*store.MessagePage, error) {
	return s.sessions.ListMessagesPage(ctx, id, limit, offset)
}

// HandoffService persists already-validated stash envelopes. Envelope parsing
// and MCP response formatting remain owned by the caller.
type HandoffService struct{ store HandoffStashStore }

func NewHandoffService(st HandoffStashStore) *HandoffService { return &HandoffService{store: st} }
func (s *HandoffService) Upsert(ctx context.Context, row store.HandoffStash) error {
	return s.store.UpsertHandoffStash(ctx, row)
}
func (s *HandoffService) Get(ctx context.Context, sessionID, id string) (store.HandoffStash, error) {
	return s.store.GetHandoffStash(ctx, sessionID, id)
}
