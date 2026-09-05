package service

import (
	"context"
	"sync"
	"testing"
	"time"

	agentsessions "github.com/hollis-labs/agentkit/agentsessions"
	"github.com/hollis-labs/go-agent-wrapper/acp"
	"github.com/hollis-labs/go-agent-wrapper/adapters"
	runtimeevents "github.com/hollis-labs/go-runtime-events/runtimeevents"
	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/dispatcher"
	"github.com/hollis-labs/nanite/internal/lifecycle"
	runtimeagent "github.com/hollis-labs/nanite/internal/runtime/agent"
	"github.com/hollis-labs/nanite/internal/store"
)

type cancelACPAdapter struct{ client *cancelACPClient }

func (a *cancelACPAdapter) Name() string { return "cancel-test-acp" }
func (a *cancelACPAdapter) Describe() adapters.Descriptor {
	return acp.DescriptorFor(a.client, "opencode", adapters.TransportStdio)
}
func (a *cancelACPAdapter) Resolve(rc adapters.ResolveContext) (adapters.Spec, error) {
	return adapters.Spec{Cwd: rc.Cwd}, nil
}
func (a *cancelACPAdapter) ACPClient() acp.Client { return a.client }

type cancelACPClient struct {
	mu sync.Mutex

	events      chan runtimeevents.Event
	closed      bool
	cancelCount int
	cancelEnter chan struct{}
	cancelGate  <-chan struct{}
	launchEnter chan struct{}
	launchGate  <-chan struct{}
}

func newCancelACPClient() *cancelACPClient {
	return &cancelACPClient{events: make(chan runtimeevents.Event, 16)}
}

func (c *cancelACPClient) Launch(ctx context.Context, _ acp.LaunchParams) error {
	c.mu.Lock()
	enter, gate := c.launchEnter, c.launchGate
	c.mu.Unlock()
	if enter != nil {
		close(enter)
	}
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	c.emit(runtimeevents.Event{Kind: runtimeevents.KindSessionReady})
	return nil
}
func (c *cancelACPClient) Prompt(context.Context, string) error {
	c.emit(runtimeevents.Event{Kind: runtimeevents.KindTurnStarted})
	return nil
}
func (c *cancelACPClient) Cancel(ctx context.Context) error {
	c.mu.Lock()
	c.cancelCount++
	enter, gate := c.cancelEnter, c.cancelGate
	c.mu.Unlock()
	if enter != nil {
		close(enter)
	}
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	c.emit(runtimeevents.Event{Kind: runtimeevents.KindTurnFailed})
	return nil
}
func (c *cancelACPClient) Events() <-chan runtimeevents.Event { return c.events }
func (c *cancelACPClient) InterruptCapability() adapters.InterruptCapability {
	return adapters.InterruptTurn
}
func (c *cancelACPClient) Close(context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.closed {
		c.closed = true
		close(c.events)
	}
	return nil
}
func (c *cancelACPClient) emit(ev runtimeevents.Event) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.closed {
		c.events <- ev
	}
}

func (c *cancelACPClient) failProcess() {
	c.emit(runtimeevents.Event{Kind: runtimeevents.KindProcessExited, Payload: []byte(`{"error":"boom"}`)})
	_ = c.Close(context.Background())
}

type staleObserverRecovery struct {
	mu    sync.Mutex
	calls int
}

func (*staleObserverRecovery) OnRestart(string, int, *agentsessions.ExitError) {}
func (r *staleObserverRecovery) OnSessionExit(string, *agentsessions.ExitError, map[string]any) {
	r.mu.Lock()
	r.calls++
	r.mu.Unlock()
}

func bootCancelACPSession(t *testing.T, deps *runtimeagent.Dependencies, sessionID string, client *cancelACPClient) *runtimeagent.Session {
	t.Helper()
	profile := &store.AgentProfile{
		ID: "cancel-agent", Slug: "cancel-agent", DefaultProvider: "opencode",
		Protocol: "acp", Transport: "stdio",
	}
	deps.Agents = &fakeAgentProfilesResolver{profile: profile}
	deps.ACPAdapterFactory = func(string, adapters.Transport) (adapters.Adapter, error) {
		return &cancelACPAdapter{client: client}, nil
	}
	sess, err := runtimeagent.Boot(context.Background(), deps, runtimeagent.Options{
		Mode: runtimeagent.ModeLongLived, SessionID: sessionID, Workdir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Boot ACP session: %v", err)
	}
	t.Cleanup(func() { _ = sess.Stop(context.Background()) })
	return sess
}

func TestCancelActiveGeneration_CapturesExactSessionAndTakeoverWaitsForTerminal(t *testing.T) {
	const sessionID = "cancel-exact-generation"
	deps := bootRealDeps(t)
	oldClient := newCancelACPClient()
	cancelGate := make(chan struct{})
	oldClient.cancelEnter = make(chan struct{})
	oldClient.cancelGate = cancelGate
	oldSession := bootCancelACPSession(t, deps, sessionID, oldClient)
	if err := oldSession.SendInput([]byte("old turn")); err != nil {
		t.Fatalf("old SendInput: %v", err)
	}

	svc := &chatServiceImpl{activeSessions: deps.Manager, activeGen: make(map[string]*inFlightGen)}
	_, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, predecessor := svc.registerGeneration(sessionID, "old-message", cancel)
	if !svc.CancelActiveGeneration(sessionID) {
		t.Fatal("CancelActiveGeneration returned false")
	}
	select {
	case <-oldClient.cancelEnter:
	case <-time.After(time.Second):
		t.Fatal("exact predecessor CancelTurn was not called")
	}

	// Install a successor while the old client's Cancel call is deliberately
	// blocked. The already-dispatched request must stay bound to oldSession;
	// it must never re-load by ID and cancel the successor.
	successorDeps := *deps
	successorDeps.Manager = runtimeagent.NewSessionManager()
	successorClient := newCancelACPClient()
	successor := bootCancelACPSession(t, &successorDeps, "cancel-successor-runtime", successorClient)
	if _, _, err := deps.Manager.Adopt(sessionID, successor); err != nil {
		t.Fatalf("Adopt successor: %v", err)
	}
	close(cancelGate)
	select {
	case <-predecessor.cancelIssued:
	case <-time.After(time.Second):
		t.Fatal("bounded CancelTurn request did not finish")
	}
	oldClient.mu.Lock()
	oldCancels := oldClient.cancelCount
	oldClient.mu.Unlock()
	successorClient.mu.Lock()
	successorCancels := successorClient.cancelCount
	successorClient.mu.Unlock()
	if oldCancels != 1 || successorCancels != 0 {
		t.Fatalf("cancel calls old=%d successor=%d, want 1/0", oldCancels, successorCancels)
	}

	gateDone := make(chan bool, 1)
	takeoverCtx, takeoverCancel := context.WithCancel(context.Background())
	defer takeoverCancel()
	go func() { gateDone <- waitForPredecessor(takeoverCtx, context.Background(), predecessor) }()
	select {
	case <-gateDone:
		t.Fatal("takeover crossed predecessor gate before old generation terminated")
	case <-time.After(25 * time.Millisecond):
	}
	close(predecessor.done)
	select {
	case ok := <-gateDone:
		if !ok {
			t.Fatal("takeover gate aborted after predecessor terminated")
		}
	case <-time.After(time.Second):
		t.Fatal("takeover did not resume after predecessor terminal return")
	}
}

func TestObserveSessionForRecovery_StaleErrorCannotCleanOrRecoverOverSuccessor(t *testing.T) {
	const sessionID = "stale-error-observer"
	deps := bootRealDeps(t)
	oldClient := newCancelACPClient()
	oldSession := bootCancelACPSession(t, deps, sessionID, oldClient)
	recovery := &staleObserverRecovery{}
	deps.Recovery = recovery
	svc := &chatServiceImpl{agentDeps: deps, activeSessions: deps.Manager}
	observerWorkdir := t.TempDir()

	observerDone := make(chan struct{})
	go func() {
		defer close(observerDone)
		svc.observeSessionForRecovery(oldSession, sessionID, "agent", "opencode", observerWorkdir, time.Now(), false)
	}()

	successorDeps := *deps
	successorDeps.Manager = runtimeagent.NewSessionManager()
	successorDeps.Recovery = nil
	successorClient := newCancelACPClient()
	successor := bootCancelACPSession(t, &successorDeps, "stale-successor-runtime", successorClient)
	if _, _, err := deps.Manager.Adopt(sessionID, successor); err != nil {
		t.Fatalf("Adopt successor: %v", err)
	}
	svc.activeSessionSlots.Store(sessionID, uint64(77))
	svc.toolPartitionStates.Store(sessionID, "successor-tool-state")

	oldClient.failProcess()
	select {
	case <-observerDone:
	case <-time.After(2 * time.Second):
		t.Fatal("stale observer did not finish")
	}
	if got, ok := deps.Manager.Load(sessionID); !ok || got != successor {
		t.Fatalf("runtime binding = %p, %v; want successor %p", got, ok, successor)
	}
	if got, ok := svc.activeSessionSlots.Load(sessionID); !ok || got != uint64(77) {
		t.Fatalf("successor slot state = %#v, %v; stale observer clobbered it", got, ok)
	}
	if got, ok := svc.toolPartitionStates.Load(sessionID); !ok || got != "successor-tool-state" {
		t.Fatalf("successor tool state = %#v, %v; stale observer clobbered it", got, ok)
	}
	recovery.mu.Lock()
	calls := recovery.calls
	recovery.mu.Unlock()
	if calls != 0 {
		t.Fatalf("stale observer invoked recovery %d time(s), want 0", calls)
	}
}

func TestChatShutdown_ClosesAdmissionAndDrainsPreReadyBoot(t *testing.T) {
	deps := bootRealDeps(t)
	client := newCancelACPClient()
	client.launchEnter = make(chan struct{})
	client.launchGate = make(chan struct{})
	profile := &store.AgentProfile{
		ID: "shutdown-agent", Slug: "shutdown-agent", DefaultProvider: "opencode",
		Protocol: "acp", Transport: "stdio",
	}
	deps.Agents = &fakeAgentProfilesResolver{profile: profile}
	deps.ACPAdapterFactory = func(string, adapters.Transport) (adapters.Adapter, error) {
		return &cancelACPAdapter{client: client}, nil
	}

	bootDone := make(chan error, 1)
	workdir := t.TempDir()
	go func() {
		_, err := runtimeagent.Boot(context.Background(), deps, runtimeagent.Options{
			Mode: runtimeagent.ModeLongLived, SessionID: "shutdown-pre-ready", Workdir: workdir,
		})
		bootDone <- err
	}()
	select {
	case <-client.launchEnter:
	case <-time.After(2 * time.Second):
		t.Fatal("Boot did not reach pre-ready launch barrier")
	}

	svc := &chatServiceImpl{activeSessions: deps.Manager, activeGen: make(map[string]*inFlightGen)}
	if err := svc.shutdownWithMaxWait(2 * time.Second); err != nil {
		t.Fatalf("shutdownWithMaxWait: %v", err)
	}
	select {
	case err := <-bootDone:
		if err == nil {
			t.Fatal("late Boot escaped shutdown admission")
		}
	case <-time.After(time.Second):
		t.Fatal("chat shutdown returned before admitted Boot drained")
	}
	if err := deps.Manager.Store("late", &runtimeagent.Session{}); err == nil {
		t.Fatal("late Store succeeded after chat shutdown")
	}
}

func TestRunGeneration_CanceledWhileTakeoverGatedClosesUnownedStream(t *testing.T) {
	owner := lifecycle.NewManager("test.gated-generation")
	t.Cleanup(func() { _ = owner.Shutdown(time.Second) })
	svc := &chatServiceImpl{lifecycle: owner, activeGen: make(map[string]*inFlightGen)}
	_, current := svc.registerGeneration("session", "new-message", func() {})
	predecessor := newInFlightGen("old-message", func() {})
	genCtx, cancel := context.WithCancel(context.Background())
	cancel()
	stream := make(chan chat.StreamEvent)
	svc.runGeneration("gated", "session", "new-message", "payload", stream, dispatcher.CallerChat, genCtx, func() {}, current, predecessor)
	select {
	case _, ok := <-stream:
		if ok {
			t.Fatal("gated generation emitted an unexpected stream value")
		}
	case <-time.After(time.Second):
		t.Fatal("generation canceled before dispatcher left its stream open")
	}
}

var _ adapters.Adapter = (*cancelACPAdapter)(nil)
var _ acp.ClientAdapter = (*cancelACPAdapter)(nil)
var _ acp.Client = (*cancelACPClient)(nil)
