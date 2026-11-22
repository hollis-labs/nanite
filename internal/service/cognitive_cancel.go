package service

import "context"

type RetainedChatCancellation interface {
	CancelRetainedChat(context.Context, string) (bool, error)
}

func (s *chatServiceImpl) CancelRetainedChat(ctx context.Context, viewID string) (bool, error) {
	// Share admission serialization so a native turn cannot appear between
	// this policy check and the retained cancel-all operation.
	s.cognitiveAdmissionMu.Lock()
	defer s.cognitiveAdmissionMu.Unlock()
	if err := requireLegacyTarget(ctx, s.store, viewID); err != nil {
		return false, err
	}
	if s.streams != nil && s.streams.CognitiveTurns().PendingView(viewID) {
		return false, ErrDefinedViewOperation
	}
	return s.CancelActiveGeneration(viewID), nil
}

// CognitiveTurnCancellation targets one accepted message, including a queued
// message. It cannot cancel another turn merely because it shares a view.
type CognitiveTurnCancellation interface {
	CancelCognitiveTurn(sessionID, messageID string) bool
}

func (s *chatServiceImpl) CancelCognitiveTurn(sessionID, messageID string) bool {
	s.activeGenMu.Lock()
	var target *inFlightGen
	for gen := s.activeGen[sessionID]; gen != nil; gen = generationPredecessor(gen) {
		if gen.msgID == messageID && !generationDone(gen) {
			target = gen
			break
		}
	}
	if target == nil {
		s.activeGenMu.Unlock()
		return false
	}
	binding, predecessor, sendStarted, claimed := s.claimGenerationCancellationLocked(target)
	s.activeGenMu.Unlock()
	if claimed {
		s.finishClaimedCancellation(sessionID, target, binding, predecessor, sendStarted)
	}
	return true
}
