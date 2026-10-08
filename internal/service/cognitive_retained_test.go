package service

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	messaging "github.com/hollis-labs/go-messaging/mailbox"
	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/store"
)

func createTestDefinedView(t *testing.T, f *characterizationFixture) *store.Session {
	t.Helper()
	base, _ := EmbeddedDefinition()
	views := &CognitiveViews{Store: f.st, Resolver: &FileDefinitionResolver{}, Models: ModelAuthorizerFunc(func(context.Context, DefinitionRef, *ModelSelection) (ModelSelection, error) {
		return ModelSelection{"characterization", "characterization-model"}, nil
	})}
	view, err := views.Create(t.Context(), CreateDefinedView{DefinitionRef: base.Ref})
	if err != nil {
		t.Fatal(err)
	}
	return view
}

func TestCognitiveRetainedOperationsRefuseDefinedTargets(t *testing.T) {
	f := newHandleMessageFixture(t, nil)
	view := createTestDefinedView(t, f)
	for _, call := range []func() (string, error){
		func() (string, error) { return f.svc.RetryLastMessage(t.Context(), view.ID) },
		func() (string, error) { return f.svc.SendAgentMessage(t.Context(), f.session, view.ID, "bypass") },
		func() (string, error) { return f.svc.HandleMessage(t.Context(), view.ID, "bypass") },
	} {
		if id, err := call(); id != "" || !errors.Is(err, ErrDefinedViewOperation) {
			t.Fatal("untracked defined target accepted", id, err)
		}
	}
	if canceled, err := f.svc.CancelRetainedChat(t.Context(), view.ID); canceled || !errors.Is(err, ErrDefinedViewOperation) {
		t.Fatal(canceled, err)
	}
	rows, err := f.st.ListMessages(t.Context(), view.ID, 100)
	if err != nil || len(rows) != 0 || f.provider.callCount() != 0 {
		t.Fatal("refused operation mutated transcript/provider", rows, err)
	}
}

func TestCognitiveRetainedCancelProtectsNativeTurnsInLegacyView(t *testing.T) {
	hold := make(chan struct{})
	release := sync.OnceFunc(func() { close(hold) })
	t.Cleanup(release)
	f := newHandleMessageFixture(t, []characterizationProviderStep{{events: doneEvents("answer"), hold: hold}})
	first, err := f.svc.SubmitCognitiveTurn(t.Context(), f.session, "first")
	if err != nil {
		t.Fatal(err)
	}
	second, err := f.svc.SubmitCognitiveTurn(t.Context(), f.session, "second")
	if err != nil {
		t.Fatal(err)
	}
	if canceled, cancelErr := f.svc.CancelRetainedChat(t.Context(), f.session); canceled || !errors.Is(cancelErr, ErrDefinedViewOperation) {
		t.Fatal(canceled, cancelErr)
	}
	f.svc.activeGenMu.Lock()
	for gen := f.svc.activeGen[f.session]; gen != nil; gen = generationPredecessor(gen) {
		if generationCancelAsked(gen) {
			f.svc.activeGenMu.Unlock()
			t.Fatal("retained cancel claimed a native neighbor")
		}
	}
	f.svc.activeGenMu.Unlock()
	release()
	for _, id := range []string{first, second} {
		stream, _ := f.svc.streams.GetStream(id)
		drainStream(stream)
	}
}

func TestCognitiveRetainedLegacyTargetsStillExecute(t *testing.T) {
	f := newHandleMessageFixture(t, []characterizationProviderStep{{events: doneEvents("retry")}, {events: doneEvents("peer")}})
	for _, call := range []func() (string, error){
		func() (string, error) { return f.svc.RetryLastMessage(t.Context(), f.session) },
		func() (string, error) { return f.svc.SendAgentMessage(t.Context(), f.session, f.session, "peer") },
	} {
		id, err := call()
		if err != nil || id == "" {
			t.Fatal(id, err)
		}
		stream, _ := f.svc.streams.GetStream(id)
		drainStream(stream)
		if row, rowErr := f.st.GetMessage(t.Context(), id); rowErr != nil || row.Role != "assistant" {
			t.Fatal(row, rowErr)
		}
	}
	if f.provider.callCount() != 2 {
		t.Fatal("legacy target did not execute", f.provider.callCount())
	}
}

func TestCognitiveRetainedLegacyCancelStillDispatches(t *testing.T) {
	hold := make(chan struct{})
	release := sync.OnceFunc(func() { close(hold) })
	t.Cleanup(release)
	f := newHandleMessageFixture(t, []characterizationProviderStep{{events: doneEvents("partial"), hold: hold}})
	id, err := f.svc.HandleMessage(chat.WithDeltaMode(t.Context(), chat.DeltaModeLive), f.session, "legacy")
	if err != nil {
		t.Fatal(err)
	}
	stream, _ := f.svc.streams.GetStream(id)
	waitForDelta(t, stream, "legacy turn")
	f.svc.activeGenMu.Lock()
	generation := f.svc.activeGen[f.session]
	f.svc.activeGenMu.Unlock()
	if canceled, cancelErr := f.svc.CancelRetainedChat(t.Context(), f.session); !canceled || cancelErr != nil {
		t.Fatal(canceled, cancelErr)
	}
	if !generationCancelAsked(generation) {
		t.Fatal("legacy cancellation was not claimed")
	}
	// This characterization provider deliberately holds its channel open
	// independently of ctx. Release it after verifying cancellation intent.
	release()
	drainStream(stream)
}

func TestCognitiveDefinedWakeUsesTrackedPolicyAndIdleAdmission(t *testing.T) {
	for _, kind := range []string{"subagent", "mailbox"} {
		t.Run(kind, func(t *testing.T) {
			hold := make(chan struct{})
			release := sync.OnceFunc(func() { close(hold) })
			t.Cleanup(release)
			f := newHandleMessageFixture(t, []characterizationProviderStep{{events: doneEvents("wake"), hold: hold}})
			f.svc.streams = NewStreamManager(f.st)
			audit := &captureSessionEventWriter{}
			f.svc.sessionEventWriter = audit
			legacyProfile := f.svc.agents.(*characterizationAgents).agent
			legacyProfile.RuntimeKind, legacyProfile.DefaultModel, legacyProfile.SystemPrompt = "pty", "unapproved-model", "unapproved legacy instructions"
			view := createTestDefinedView(t, f)
			wake := func() (string, error) {
				if kind == "subagent" {
					return f.svc.TriggerHarnessTurn(t.Context(), view.ID, "subagent_completion", "child")
				}
				return f.svc.TriggerMessageWake(t.Context(), view.ID, &messaging.Message{Body: "peer", FromSessionID: "sender", FromAgentID: "agent"})
			}
			id, err := wake()
			if err != nil || id == "" {
				t.Fatal(id, err)
			}
			if _, err = f.svc.streams.CognitiveTurns().Get(view.ID, id); err != nil {
				t.Fatal("wake omitted tracking", err)
			}
			if next, busyErr := wake(); next != "" || !errors.Is(busyErr, ErrSessionBusy) {
				t.Fatal("wake bypassed idle admission", next, busyErr)
			}
			release()
			stream, _ := f.svc.streams.GetStream(id)
			drainStream(stream)
			outcome, err := f.svc.streams.CognitiveTurns().Get(view.ID, id)
			if err != nil || outcome.State != "completed" || outcome.CommittedMessage == nil {
				t.Fatal(outcome, err)
			}
			requests := f.provider.requestsSnapshot()
			if len(requests) != 1 || requests[0].Model != "characterization-model" || strings.Contains(requests[0].EffectiveSystemPrompt(), "unapproved legacy instructions") {
				t.Fatal("defined wake used unapproved legacy configuration", requests)
			}
			wakeEvents := 0
			for _, event := range audit.events() {
				if event.EventType == EventHarnessTriggeredTurn {
					wakeEvents++
					if event.SessionID != view.ID || !strings.Contains(event.PayloadJSON, id) {
						t.Fatal("wake audit lost target/turn provenance", event)
					}
				}
			}
			if wakeEvents != 1 {
				t.Fatal("accepted wake audit missing or refused wake audited", audit.events())
			}
		})
	}
}

type cancelAfterAdmissionStore struct {
	*store.Store
	cancel context.CancelFunc
}

func (s *cancelAfterAdmissionStore) CreateCognitiveTurn(ctx context.Context, user *store.Message, id, initial string) error {
	err := s.Store.CreateCognitiveTurn(ctx, user, id, initial)
	if err == nil {
		s.cancel() // Exact boundary: rows committed, HTTP caller disconnected.
	}
	return err
}

type canceledRequestSessions struct {
	SessionService
	canceledGets atomic.Int32
}

func (s *canceledRequestSessions) Get(ctx context.Context, id string) (*store.Session, error) {
	if err := ctx.Err(); err != nil {
		s.canceledGets.Add(1)
		return nil, err
	}
	return s.SessionService.Get(ctx, id)
}

func TestCognitiveAdmissionDisconnectAfterCommitStillExecutes(t *testing.T) {
	f := newHandleMessageFixture(t, []characterizationProviderStep{{events: doneEvents("accepted answer")}})
	f.svc.streams = NewStreamManager(f.st)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	f.svc.store = &cancelAfterAdmissionStore{Store: f.st, cancel: cancel}
	sessions := &canceledRequestSessions{SessionService: f.svc.sessions}
	f.svc.sessions = sessions
	id, err := f.svc.SubmitCognitiveTurn(ctx, f.session, "question")
	if err != nil || id == "" || ctx.Err() == nil {
		t.Fatal("committed admission lost discoverable turn", id, err)
	}
	stream, ok := f.svc.streams.GetStream(id)
	if !ok {
		t.Fatal("accepted stream not tracked")
	}
	drainStream(stream)
	snapshot, err := f.svc.streams.CognitiveTurns().Get(f.session, id)
	if err != nil || snapshot.State != "completed" || snapshot.CommittedMessage == nil || sessions.canceledGets.Load() != 0 || f.provider.callCount() != 1 {
		t.Fatal("accepted turn left submitted after disconnect", snapshot, err, sessions.canceledGets.Load())
	}
	reopened, err := NewCognitiveTurns(f.st).Get(f.session, id)
	if err != nil || reopened.State != snapshot.State || reopened.EventCheckpoint != snapshot.EventCheckpoint {
		t.Fatal("accepted outcome did not persist after disconnect", reopened, err)
	}
}

func TestCognitiveAdmissionAfterShutdownRefusesBeforeRows(t *testing.T) {
	f := newHandleMessageFixture(t, nil)
	before, _ := f.st.ListMessages(t.Context(), f.session, 100)
	if err := f.svc.Shutdown(); err != nil {
		t.Fatal(err)
	}
	if id, err := f.svc.SubmitCognitiveTurn(t.Context(), f.session, "late"); id != "" || !errors.Is(err, ErrCognitiveAdmissionClosed) {
		t.Fatal("closed admission accepted", id, err)
	}
	after, err := f.st.ListMessages(t.Context(), f.session, 100)
	if err != nil || len(after) != len(before) {
		t.Fatal("closed admission wrote transcript", after, err)
	}
}
