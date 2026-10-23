package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

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
	release, gateErr := s.LockConversation(ctx, viewID)
	if gateErr != nil {
		return "", gateErr
	}
	defer release()
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
	// Stop holds this conversation gate while it archives and cancels, so a turn
	// is either registered before the cancel or sees the session ended here.
	if session.Status == "archived" {
		return "", ErrSessionEnded
	}
	providerID, model := session.Provider, session.Model
	defined := false
	cliView := false
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
			cliView = cfg.HostSettings != nil && cfg.HostSettings.Runtime == "cli"
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
			return "", unsupportedModelCause(CauseRuntimeNotAPI, "agent runtime %q is not native; native chat needs runtime \"api\"", agent.RuntimeKind)
		}
		if providerID == "" {
			providerID = agent.DefaultProvider
		}
		if model == "" {
			model = agent.DefaultModel
		}
	}
	if cliView {
		// A CLI view runs the provider's own CLI, which has no registry entry;
		// launchability and the model were decided at create.
		if providerID == "" {
			return "", unsupportedModelCause(CauseModelNotSelected, "no provider selected for this view; set one in the agent's host settings")
		}
	} else {
		if providerID == "" || model == "" {
			return "", unsupportedModelCause(CauseModelNotSelected, "no provider and model selected for this view; set them on the agent's host settings or the launch request")
		}
		if _, ok := s.providers.Get(providerID); !ok {
			return "", unsupportedModelCause(CauseProviderNotRegistered, "provider %q is not a registered native provider; check its API key status and restart the server", providerID)
		}
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
	return s.handleMessage(context.WithValue(ctx, cognitiveTurnContextKey{}, true), viewID, content)
}
