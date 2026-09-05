package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	agentsessions "github.com/hollis-labs/agentkit/agentsessions"
	"github.com/hollis-labs/go-agent-wrapper/acp"
	"github.com/hollis-labs/go-agent-wrapper/adapters"
	runtimeevents "github.com/hollis-labs/go-runtime-events/runtimeevents"
)

type bootACPTestAdapter struct{ client *bootACPTestClient }

func (a *bootACPTestAdapter) Name() string { return "test-acp" }
func (a *bootACPTestAdapter) Describe() adapters.Descriptor {
	return acp.DescriptorFor(a.client, "opencode", adapters.TransportStdio)
}
func (a *bootACPTestAdapter) Resolve(rc adapters.ResolveContext) (adapters.Spec, error) {
	return adapters.Spec{Cwd: rc.Cwd}, nil
}
func (a *bootACPTestAdapter) ACPClient() acp.Client { return a.client }

type bootACPTestClient struct {
	mu sync.Mutex

	events           chan runtimeevents.Event
	launch           acp.LaunchParams
	prompts          []string
	providerID       string
	autoComplete     bool
	closed           bool
	closeCount       int
	cancelCount      int
	launchEntered    chan struct{}
	blockLaunchUntil <-chan struct{}
}

func newBootACPTestClient() *bootACPTestClient {
	return &bootACPTestClient{
		events:       make(chan runtimeevents.Event, 64),
		providerID:   "provider-session-test-1",
		autoComplete: true,
	}
}

func (c *bootACPTestClient) Launch(ctx context.Context, params acp.LaunchParams) error {
	c.mu.Lock()
	c.launch = params
	entered := c.launchEntered
	block := c.blockLaunchUntil
	c.mu.Unlock()
	if entered != nil {
		close(entered)
	}
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	c.emit(runtimeevents.Event{Kind: runtimeevents.KindSessionReady})
	return nil
}

func (c *bootACPTestClient) Prompt(_ context.Context, prompt string) error {
	c.mu.Lock()
	c.prompts = append(c.prompts, prompt)
	auto := c.autoComplete
	c.mu.Unlock()
	c.emit(runtimeevents.Event{Kind: runtimeevents.KindTurnStarted, TurnID: "turn"})
	c.emit(runtimeevents.Event{Kind: runtimeevents.KindAgentDelta, TurnID: "turn", Payload: json.RawMessage(`{"content":"ok"}`)})
	if auto {
		c.emit(runtimeevents.Event{Kind: runtimeevents.KindTurnCompleted, TurnID: "turn"})
	}
	return nil
}

func (c *bootACPTestClient) Cancel(context.Context) error {
	c.mu.Lock()
	c.cancelCount++
	c.mu.Unlock()
	c.emit(runtimeevents.Event{Kind: runtimeevents.KindTurnFailed, TurnID: "turn", Payload: json.RawMessage(`{"error":"canceled"}`)})
	return nil
}

func (c *bootACPTestClient) Events() <-chan runtimeevents.Event { return c.events }
func (c *bootACPTestClient) InterruptCapability() adapters.InterruptCapability {
	return adapters.InterruptTurn
}
func (c *bootACPTestClient) ProviderSessionID() string { return c.providerID }

func (c *bootACPTestClient) Close(context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closeCount++
	if !c.closed {
		c.closed = true
		close(c.events)
	}
	return nil
}

func (c *bootACPTestClient) emit(ev runtimeevents.Event) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.closed {
		c.events <- ev
	}
}

func (c *bootACPTestClient) failProcess(message string) {
	c.emit(runtimeevents.Event{Kind: runtimeevents.KindProcessExited, Payload: json.RawMessage(`{"error":` + mustJSON(message) + `}`)})
	_ = c.Close(context.Background())
}

func mustJSON(value string) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}

func acpBootTestDeps(t *testing.T, client *bootACPTestClient) (*Dependencies, *fakeRuntimeStore) {
	t.Helper()
	profile := storeProfile("opencode")
	profile.Protocol = "acp"
	profile.Transport = "stdio"
	st := newFakeRuntimeStore()
	deps := &Dependencies{
		Agents:         &fakeAgentProfiles{profile: &profile},
		Manager:        NewSessionManager(),
		Store:          st,
		WorkspacesRoot: t.TempDir(),
		ACPAdapterFactory: func(string, adapters.Transport) (adapters.Adapter, error) {
			return &bootACPTestAdapter{client: client}, nil
		},
	}
	return deps, st
}

func waitACPState(t *testing.T, sess *Session, want acp.State) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if snapshot, ok := sess.wr.ACPSnapshot(); ok && snapshot.State == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	snapshot, _ := sess.wr.ACPSnapshot()
	t.Fatalf("ACP state = %q, want %q", snapshot.State, want)
}

func TestBoot_ACPWrapperLifecycle_MultiTurnResumeCancelIdentityAndCleanup(t *testing.T) {
	client := newBootACPTestClient()
	deps, st := acpBootTestDeps(t, client)
	sess, err := Boot(context.Background(), deps, Options{
		Mode:                    ModeLongLived,
		SessionID:               "acp-integration",
		Workdir:                 t.TempDir(),
		ResumeProviderSessionID: "resume-provider-7",
		BootPromptOverride:      "system-sentinel",
		Env:                     map[string]string{"NANITE_SENTINEL": "yes"},
	})
	if err != nil {
		t.Fatalf("Boot: %v", err)
	}
	if !deps.Manager.IsLive(sess.ID) {
		t.Fatal("ACP session was not registered live at Boot readiness")
	}
	if got := sess.ProviderSessionID(); got != client.providerID {
		t.Fatalf("ProviderSessionID = %q, want %q", got, client.providerID)
	}
	if got := st.provIDs[sess.ID]; got != client.providerID {
		t.Fatalf("persisted provider session ID = %q, want %q", got, client.providerID)
	}
	client.mu.Lock()
	launch := client.launch
	client.mu.Unlock()
	if launch.SessionIDPreset != "resume-provider-7" || !strings.HasPrefix(launch.SystemPrompt, "system-sentinel") || launch.Cwd == "" {
		t.Fatalf("ACP launch params = %+v", launch)
	}

	for _, prompt := range []string{"turn one", "turn two"} {
		if err := sess.SendInput([]byte(prompt)); err != nil {
			t.Fatalf("SendInput(%q): %v", prompt, err)
		}
		waitACPState(t, sess, acp.StateReady)
	}
	client.mu.Lock()
	client.autoComplete = false
	client.mu.Unlock()
	if err := sess.SendInput([]byte("cancel me")); err != nil {
		t.Fatalf("SendInput(cancel): %v", err)
	}
	waitACPState(t, sess, acp.StateProcessing)
	if err := deps.Manager.Cancel(context.Background(), sess.ID); err != nil {
		t.Fatalf("Manager.Cancel: %v", err)
	}
	waitACPState(t, sess, acp.StateReady)
	client.mu.Lock()
	cancelCount := client.cancelCount
	client.autoComplete = true
	client.mu.Unlock()
	if cancelCount != 1 {
		t.Fatalf("client Cancel calls = %d, want 1", cancelCount)
	}
	if err := sess.SendInput([]byte("after cancel")); err != nil {
		t.Fatalf("SendInput after cancel: %v", err)
	}
	waitACPState(t, sess, acp.StateReady)

	stopCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := sess.Stop(stopCtx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if err := sess.Wait(stopCtx); err != nil {
		t.Fatalf("Wait after Stop: %v", err)
	}
	client.mu.Lock()
	closeCount := client.closeCount
	prompts := append([]string(nil), client.prompts...)
	client.mu.Unlock()
	if closeCount != 1 {
		t.Fatalf("client Close calls = %d, want exactly 1", closeCount)
	}
	if len(prompts) != 4 {
		t.Fatalf("prompts = %q, want four turns", prompts)
	}
	journal, err := os.ReadFile(filepath.Join(sess.WorkspaceDir, "logs", "runtime-events.jsonl"))
	if err != nil {
		t.Fatalf("read canonical runtime event journal: %v", err)
	}
	for _, kind := range []string{"session.ready", "turn.started", "agent.delta", "turn.completed", "interrupt.requested", "interrupt.acknowledged"} {
		if !strings.Contains(string(journal), `"kind":"`+kind+`"`) {
			t.Errorf("canonical runtime event journal missing %q", kind)
		}
	}
}

func TestBoot_ACPWrapperLifecycle_DisconnectAndProcessExit(t *testing.T) {
	client := newBootACPTestClient()
	deps, _ := acpBootTestDeps(t, client)
	sess, err := Boot(context.Background(), deps, Options{
		Mode: ModeLongLived, SessionID: "acp-disconnect", Workdir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Boot: %v", err)
	}
	client.failProcess("child exited 42")
	waitCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err = sess.Wait(waitCtx)
	var lifecycleErr *acp.LifecycleError
	if !errors.As(err, &lifecycleErr) || lifecycleErr.Kind != acp.OutcomeChildExit {
		t.Fatalf("Wait error = %v, want ACP child-exit lifecycle error", err)
	}
	var exitErr *agentsessions.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("Wait error = %v, want Nanite recovery-compatible ExitError", err)
	}
	if deps.Manager.IsLive(sess.ID) {
		t.Fatal("disconnected ACP session still reported live")
	}
}

func TestBoot_ShutdownCancelsAndWaitsForAdmittedPreReadyACPLaunch(t *testing.T) {
	client := newBootACPTestClient()
	client.launchEntered = make(chan struct{})
	client.blockLaunchUntil = make(chan struct{})
	deps, _ := acpBootTestDeps(t, client)
	bootDone := make(chan error, 1)
	go func() {
		_, err := Boot(context.Background(), deps, Options{
			Mode: ModeLongLived, SessionID: "acp-pre-ready", Workdir: t.TempDir(),
		})
		bootDone <- err
	}()
	select {
	case <-client.launchEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("ACP Launch did not reach deterministic pre-ready barrier")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := deps.Manager.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	select {
	case err := <-bootDone:
		if err == nil {
			t.Fatal("Boot succeeded across closed shutdown admission")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Shutdown returned without draining admitted pre-ready Boot")
	}
	if err := deps.Manager.Store("late", &Session{}); !errors.Is(err, ErrSessionManagerClosed) {
		t.Fatalf("late Store error = %v, want ErrSessionManagerClosed", err)
	}
}

var _ adapters.Adapter = (*bootACPTestAdapter)(nil)
var _ acp.ClientAdapter = (*bootACPTestAdapter)(nil)
var _ acp.Client = (*bootACPTestClient)(nil)
