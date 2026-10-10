package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"

	"github.com/hollis-labs/nanite/internal/agentpolicy"
	"github.com/hollis-labs/nanite/internal/store"
)

// definitionNativePolicy reads the pinned view record without assigning an
// actor or reading mutable profile behavior. A defined view with no extension
// still uses documented native defaults, rather than session metadata overrides.
func (s *chatServiceImpl) definitionNativePolicy(ctx context.Context, viewID string) (*agentpolicy.NativePolicy, bool) {
	reader, ok := s.store.(interface {
		GetCognitiveView(context.Context, string) (store.CognitiveViewRecord, error)
	})
	if !ok {
		return nil, false
	}
	record, err := reader.GetCognitiveView(ctx, viewID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false
	}
	if err != nil {
		slog.Warn("definition policy unavailable", "view_id", viewID, "err", err)
		return nil, true
	}
	var cfg ChatDefinitionConfig
	if err := json.Unmarshal([]byte(record.ChatConfigJSON), &cfg); err != nil {
		slog.Warn("invalid stored definition policy", "view_id", viewID, "err", err)
		return nil, true
	}
	p := agentpolicy.NativePolicy{}
	if cfg.NativePolicy != nil {
		if err := cfg.NativePolicy.Validate(); err != nil {
			slog.Warn("invalid stored native policy", "view_id", viewID, "err", err)
			return nil, true
		}
		p = *cfg.NativePolicy
	}
	p = p.Defaults()
	return &p, true
}
