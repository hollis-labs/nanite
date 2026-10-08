package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/hollis-labs/nanite/internal/store"
)

type cognitiveTurnContextKey struct{}
type cognitiveModelContextKey struct{}

func cognitiveModelFromContext(ctx context.Context) ModelSelection {
	value, _ := ctx.Value(cognitiveModelContextKey{}).(ModelSelection)
	return value
}

func isCognitiveTurn(ctx context.Context) bool {
	value, _ := ctx.Value(cognitiveTurnContextKey{}).(bool)
	return value
}

const CognitiveQueuedTurnLimit = 16

var ErrCognitiveQueueFull = errors.New("cognitive view queue is full")
var ErrCognitiveAdmissionClosed = errors.New("native turn admission is closed")

// CognitiveTurnAdmission serializes API admission before any user-message or
// stream side effect. Execution remains FIFO on the existing generation chain.
type CognitiveTurnAdmission interface {
	SubmitCognitiveTurn(context.Context, string, string) (string, error)
}

func (s *chatServiceImpl) SubmitCognitiveTurn(ctx context.Context, viewID, content string) (string, error) {
	return s.submitCognitiveTurn(ctx, viewID, content, false)
}

func (s *chatServiceImpl) submitCognitiveTurn(ctx context.Context, viewID, content string, onlyIdle bool) (string, error) {
	s.cognitiveAdmissionMu.Lock()
	defer s.cognitiveAdmissionMu.Unlock()
	if s.cognitiveAdmissionClosed || (s.lifecycle != nil && s.lifecycle.Context().Err() != nil) {
		return "", ErrCognitiveAdmissionClosed
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	// A retained CLI view remains an admin/product resource, not a native
	// cognitive execution target. Refusal precedes queue/transcript mutation.
	session, err := s.sessions.Get(ctx, viewID)
	if err != nil {
		return "", err
	}
	providerID, model := session.Provider, session.Model
	defined := false
	if reader, ok := s.store.(interface {
		GetCognitiveView(context.Context, string) (store.CognitiveViewRecord, error)
	}); ok {
		record, readErr := reader.GetCognitiveView(ctx, viewID)
		if readErr == nil {
			defined = true
			var cfg ChatDefinitionConfig
			if json.Unmarshal([]byte(record.ChatConfigJSON), &cfg) != nil {
				return "", errors.New("invalid verified chat configuration")
			}
			providerID, model = cfg.Model.Provider, cfg.Model.Model
		} else if !errors.Is(readErr, sql.ErrNoRows) {
			return "", readErr
		}
	}
	if !defined {
		agent, resolveErr := s.agents.ResolveForSession(ctx, viewID)
		if resolveErr != nil {
			return "", resolveErr
		}
		if agent.RuntimeKind != "" && agent.RuntimeKind != "api" {
			return "", ErrUnsupportedModel
		}
		if providerID == "" {
			providerID = agent.DefaultProvider
		}
		if model == "" {
			model = agent.DefaultModel
		}
	}
	if providerID == "" || model == "" {
		providerID, model, err = s.store.ResolveProviderAndModel(ctx, providerID, model)
		if err != nil {
			return "", fmt.Errorf("%w: no configured native model", ErrUnsupportedModel)
		}
	}
	if _, ok := s.providers.Get(providerID); !ok {
		return "", ErrUnsupportedModel
	}
	s.activeGenMu.Lock()
	count := 0
	for gen := s.activeGen[viewID]; gen != nil; gen = generationPredecessor(gen) {
		if !generationDone(gen) {
			count++
		}
	}
	s.activeGenMu.Unlock()
	if onlyIdle && count > 0 {
		return "", ErrSessionBusy
	}
	// One execution slot plus sixteen waiting turns. A rejected request has
	// no accepted run and never writes a transcript message.
	if count >= CognitiveQueuedTurnLimit+1 {
		return "", ErrCognitiveQueueFull
	}
	ctx = context.WithValue(ctx, cognitiveModelContextKey{}, ModelSelection{providerID, model})
	return s.HandleMessage(context.WithValue(ctx, cognitiveTurnContextKey{}, true), viewID, content)
}
