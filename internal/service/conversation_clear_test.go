package service

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/store"
	ctxpkg "github.com/hollis-labs/substrate/agent/contextwindow"
	llmtypes "github.com/hollis-labs/substrate/llm-core/llmtypes"
)

func TestClearConversation_ContextAndRecoveryKeepStaticSlots(t *testing.T) {
	for _, keep := range []bool{false, true} {
		t.Run(map[bool]string{false: "drop", true: "keep"}[keep], func(t *testing.T) {
			f := newHandleMessageFixture(t, nil)
			ctx := t.Context()
			if err := f.st.SetSessionContextPrompt(ctx, f.session, "pin survives"); err != nil {
				t.Fatal(err)
			}
			if err := f.st.CreateDocument(ctx, &store.Document{SessionID: f.session, Content: "document survives", Name: "pin", Included: true, FullContent: true}); err != nil {
				t.Fatal(err)
			}
			f.svc.agents.(*characterizationAgents).agent.SystemPrompt = "system survives"
			_, err := WriteGlass4Handoff(f.st, f.session, ctxpkg.HandoffPayload{SessionIntent: "prior handoff", NextStepAnchor: "continue old work"})
			if err != nil {
				t.Fatal(err)
			}
			if err = f.st.WriteCompactionEvent(ctx, store.CompactionEvent{ID: "old-compact", SummaryMode: "general", SessionID: f.session, CreatedAt: time.Now().UTC().Format(time.RFC3339)}); err != nil {
				t.Fatal(err)
			}
			c := &Container{Chat: f.svc}
			if _, err = c.ClearSession(ctx, f.session, ClearConversationOptions{KeepHandoff: keep}); err != nil {
				t.Fatal(err)
			}
			session, err := f.st.GetSession(ctx, f.session)
			if err != nil {
				t.Fatal(err)
			}
			agent, err := f.svc.agents.ResolveForSession(ctx, f.session)
			if err != nil {
				t.Fatal(err)
			}
			result, err := f.context.AssembleSlots(ctx, session, agent, nil, "", 200000, "")
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Messages) != 0 || strings.Contains(result.SystemPrompt, "characterize this turn") || strings.Contains(result.SystemPrompt, "Compaction Notice") {
				t.Fatalf("old conversation/disclosure leaked: %+v", result)
			}
			for _, content := range []string{"pin survives", "document survives", "system survives"} {
				if !strings.Contains(result.SystemPrompt, content) {
					t.Errorf("lost %q", content)
				}
			}
			events := make(chan chat.StreamEvent, 1)
			injected, err := InjectGlass4HandoffSlot(f.st, result.Window, f.session, events)
			if err != nil {
				t.Fatal(err)
			}
			if keep != injected {
				t.Fatalf("keep=%v injection=%t", keep, injected)
			}
			if prefix := f.svc.buildSessionRecoveryPrefix(f.session, session, agent, "", "new request"); prefix != "" {
				t.Fatalf("cleared history recovered: %s", prefix)
			}
			if !f.svc.shouldRecoverColdBoot(f.session, true) { // First fresh flag consumption should be false.
				if !f.svc.shouldRecoverColdBoot(f.session, true) {
					t.Fatal("fresh flag not one-shot")
				}
			} else {
				t.Fatal("clear failed to arm fresh boot")
			}
			if err = f.st.CreateMessage(ctx, &store.Message{SessionID: f.session, Role: "user", Content: "new history"}); err != nil {
				t.Fatal(err)
			}
			if prefix := f.svc.buildSessionRecoveryPrefix(f.session, session, agent, "", "next request"); strings.Contains(prefix, "characterize this turn") || !strings.Contains(prefix, "new history") {
				t.Fatalf("recovery window=%s", prefix)
			}
		})
	}
}

func TestClearConversation_ConfirmationCancelsExactNativeQueue(t *testing.T) {
	hold := make(chan struct{})
	release := sync.OnceFunc(func() { close(hold) })
	t.Cleanup(release)
	f := newHandleMessageFixture(t, []characterizationProviderStep{
		{events: []llmtypes.StreamEvent{{Type: "delta", Content: "partial"}}, hold: hold},
		{events: doneEvents("after clear")},
	})
	bindTestDefinedConfiguration(t, f)
	ctx := chat.WithDeltaMode(t.Context(), chat.DeltaModeLive)
	first, err := f.svc.SubmitCognitiveTurn(ctx, f.session, "before clear")
	if err != nil {
		t.Fatal(err)
	}
	stream, _ := f.svc.streams.GetStream(first)
	waitForDelta(t, stream, "first starts")
	queued, err := f.svc.SubmitCognitiveTurn(ctx, f.session, "queued before clear")
	if err != nil {
		t.Fatal(err)
	}
	c := &Container{Chat: f.svc}
	_, err = c.ClearSession(ctx, f.session, ClearConversationOptions{})
	var confirm *ClearConfirmationRequired
	if !errors.As(err, &confirm) || len(confirm.TurnIDs) != 2 || !slices.Contains(confirm.TurnIDs, first) || !slices.Contains(confirm.TurnIDs, queued) {
		t.Fatalf("confirmation=%+v %v", confirm, err)
	}
	if cut, cutErr := f.st.LatestConversationClear(ctx, f.session); cutErr != nil || cut != nil {
		t.Fatalf("refusal mutated history %+v %v", cut, cutErr)
	}
	if _, err = c.ClearSession(ctx, f.session, ClearConversationOptions{ConfirmCancel: true, ExpectedTurnIDs: []string{first}}); !errors.As(err, &confirm) {
		t.Fatalf("stale confirmation=%v", err)
	}
	clearCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if _, err = c.ClearSession(clearCtx, f.session, ClearConversationOptions{ConfirmCancel: true, ExpectedTurnIDs: confirm.TurnIDs}); err != nil {
		t.Fatal(err)
	}
	release()
	drainStream(stream)
	queuedStream, _ := f.svc.streams.GetStream(queued)
	drainStream(queuedStream)
	for _, id := range []string{first, queued} {
		snapshot, lookupErr := f.svc.streams.CognitiveTurns().Get(f.session, id)
		if lookupErr != nil || snapshot.State != "canceled" {
			t.Fatalf("turn=%s snapshot=%+v %v", id, snapshot, lookupErr)
		}
	}
	if f.provider.callCount() != 1 {
		t.Fatalf("queued turn executed: %d", f.provider.callCount())
	}
	messages, err := f.st.ListWorkingMessages(ctx, f.session, 100)
	if err != nil || len(messages) != 0 {
		t.Fatalf("working=%+v %v", messages, err)
	}
	next, err := f.svc.SubmitCognitiveTurn(ctx, f.session, "fresh question")
	if err != nil {
		t.Fatal(err)
	}
	nextStream, _ := f.svc.streams.GetStream(next)
	drainStream(nextStream)
	requests := f.provider.requestsSnapshot()
	if len(requests) != 2 {
		t.Fatalf("requests=%d", len(requests))
	}
	for _, msg := range requests[1].Messages {
		if strings.Contains(msg.Content, "before clear") || strings.Contains(msg.Content, "partial") {
			t.Fatalf("cleared text leaked: %+v", requests[1].Messages)
		}
	}
}

func TestClearConversation_AdmissionGateCancellationAndFailure(t *testing.T) {
	f := newHandleMessageFixture(t, nil)
	ctx := t.Context()
	unlock, err := f.svc.LockConversation(ctx, f.session)
	if err != nil {
		t.Fatal(err)
	}
	blockedCtx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = f.svc.HandleMessage(blockedCtx, f.session, "not admitted"); !errors.Is(err, context.Canceled) {
		t.Fatalf("admission cancellation=%v", err)
	}
	if _, err = f.svc.ClearConversation(blockedCtx, f.session, ClearConversationOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("clear cancellation=%v", err)
	}
	unlock()
	history, err := f.st.ListMessages(ctx, f.session, 100)
	if err != nil || len(history) != 1 {
		t.Fatalf("failed gate wrote: %+v %v", history, err)
	}
	// Another session can proceed even while this session's context is locked.
	unlock, err = f.svc.LockConversation(ctx, f.session)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	otherUnlock, err := f.svc.LockConversation(ctx, "other")
	if err != nil {
		t.Fatal(err)
	}
	otherUnlock()
}

func TestClearConversation_WaitsForExactRuntimeCancelThenFreshStops(t *testing.T) {
	f := newHandleMessageFixture(t, []characterizationProviderStep{{events: doneEvents("neighbor answer")}})
	ctx := t.Context()
	deps := bootRealDeps(t)
	client := newCancelACPClient()
	cancelGate := make(chan struct{})
	releaseCancel := sync.OnceFunc(func() { close(cancelGate) })
	t.Cleanup(releaseCancel)
	client.cancelGate = cancelGate
	client.cancelEnter = make(chan struct{})
	sess := bootCancelACPSession(t, deps, f.session, client)
	f.svc.activeSessions = deps.Manager
	if err := sess.SendInput([]byte("old runtime context")); err != nil {
		t.Fatal(err)
	}
	genCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, gen := f.svc.registerGeneration(f.session, "runtime-turn", cancel)
	binding := &runtimeTurnBinding{session: sess, router: newSessionRouter(make(chan llmtypes.StreamEvent, 1))}
	if !gen.admitRuntimeTurn(ctx, binding, func() bool { return true }) || !gen.beginRuntimeSend(ctx, binding) {
		t.Fatal("bind exact runtime")
	}
	close(binding.sendReturned)
	finished := make(chan struct{})
	go func() { <-genCtx.Done(); close(gen.done); close(finished) }()
	type clearResult struct {
		cut *store.ConversationClear
		err error
	}
	cleared := make(chan clearResult, 1)
	go func() {
		cut, clearErr := f.svc.ClearConversation(ctx, f.session, ClearConversationOptions{ConfirmCancel: true, ExpectedTurnIDs: []string{"runtime-turn"}})
		cleared <- clearResult{cut, clearErr}
	}()
	select {
	case <-client.cancelEnter:
	case <-time.After(time.Second):
		t.Fatal("exact cancellation not requested")
	}
	bounded, boundedCancel := context.WithTimeout(ctx, 30*time.Millisecond)
	defer boundedCancel()
	if _, submitErr := f.svc.SubmitCognitiveTurn(bounded, f.session, "must wait until clear"); !errors.Is(submitErr, context.DeadlineExceeded) {
		t.Fatalf("same-session admission=%v", submitErr)
	}
	if err := f.st.CreateSession(ctx, &store.Session{ID: "neighbor", Provider: "characterization", Model: "characterization-model"}); err != nil {
		t.Fatal(err)
	}
	neighborCtx, neighborCancel := context.WithTimeout(ctx, time.Second)
	defer neighborCancel()
	neighborID, neighborErr := f.svc.SubmitCognitiveTurn(neighborCtx, "neighbor", "independent question")
	if neighborErr != nil {
		t.Fatalf("clear blocked independent admission: %v", neighborErr)
	}
	neighborStream, _ := f.svc.streams.GetStream(neighborID)
	drainStream(neighborStream)
	releaseCancel()
	result := <-cleared
	if result.err != nil || result.cut == nil {
		t.Fatalf("clear=%+v %v", result.cut, result.err)
	}

	<-finished
	client.mu.Lock()
	cancelCalls := client.cancelCount
	client.mu.Unlock()
	select {
	case <-sess.Done():
	default:
		t.Fatal("clear returned before runtime callbacks drained")
	}
	if cancelCalls != 1 {
		t.Fatalf("CancelTurn calls=%d", cancelCalls)
	}
	if _, ok := deps.Manager.Load(f.session); ok {
		t.Fatal("old provider runtime retained")
	}
}

func TestClearConversation_DrainCancellationLeavesBoundaryAndStashUntouched(t *testing.T) {
	f := newHandleMessageFixture(t, nil)
	ctx := t.Context()
	stashID, err := WriteGlass4Handoff(f.st, f.session, ctxpkg.HandoffPayload{SessionIntent: "retained", NextStepAnchor: "continue"})
	if err != nil {
		t.Fatal(err)
	}
	// An already-failed send is a retained unsafe owner. Clear may request
	// cancellation, but cannot pretend its terminal boundary was established.
	gen := newInFlightGen("unsafe", func() {})
	close(gen.done)
	f.svc.activeGen[f.session] = gen
	bounded, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	if _, err = f.svc.ClearConversation(bounded, f.session, ClearConversationOptions{ConfirmCancel: true, ExpectedTurnIDs: []string{"unsafe"}}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("unsafe clear=%v", err)
	}
	cut, err := f.st.LatestConversationClear(ctx, f.session)
	if err != nil || cut != nil {
		t.Fatalf("partial clear=%+v %v", cut, err)
	}
	stash, err := f.st.GetLatestStashForSession(ctx, f.session)
	if err != nil || stash.ID != stashID {
		t.Fatalf("stash lost: %+v %v", stash, err)
	}
}
