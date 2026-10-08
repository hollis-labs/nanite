package service

import (
	"context"
	"testing"
	"time"
)

func TestCognitiveCancellationTargetsOnlySelectedTurn(t *testing.T) {
	svc := &chatServiceImpl{activeGen: make(map[string]*inFlightGen)}
	_, first := svc.registerGeneration("view", "first", func() {})
	_, second := svc.registerGeneration("view", "second", func() {})
	_, third := svc.registerGeneration("view", "third", func() {})
	// Claim only the queued second turn. A binding-less cancellation must wait
	// for its predecessor boundary, which we resolve after checking the claims.
	if !svc.CancelCognitiveTurn("view", "second") {
		t.Fatal("selected turn not found")
	}
	if generationCancelAsked(first) || !generationCancelAsked(second) || generationCancelAsked(third) {
		t.Fatal("cancellation escaped selected turn")
	}
	if svc.CancelCognitiveTurn("other-view", "second") || svc.CancelCognitiveTurn("view", "unknown") {
		t.Fatal("wrong view/turn accepted")
	}
	svc.markGenerationCompleted(first)
	close(first.done)
	close(second.done)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if !waitClosed(ctx, second.cancelIssued) {
		t.Fatal("cancellation did not resolve")
	}
	close(third.done)
}
