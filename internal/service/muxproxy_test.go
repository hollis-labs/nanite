package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/chrispian/agent-mux/pkg/claudestream"
	agentmux "github.com/hollis-labs/go-agentmux-client"
	"github.com/hollis-labs/nanite/internal/muxproxy"
)

type fakeMuxClient struct {
	launches       []agentmux.Launch
	launchResp     agentmux.LaunchResponse
	launchErr      error
	sendErr        error
	stopErr        error
	stopped        []string
	sentInputs     []string
	sentInputsLock sync.Mutex
}

func (f *fakeMuxClient) ListLaunches(_ context.Context) ([]agentmux.Launch, error) {
	return f.launches, nil
}

func (f *fakeMuxClient) Launch(_ context.Context, _ string) (agentmux.LaunchResponse, error) {
	return f.launchResp, f.launchErr
}

func (f *fakeMuxClient) SendInput(_ context.Context, _ string, data []byte) error {
	f.sentInputsLock.Lock()
	f.sentInputs = append(f.sentInputs, string(data))
	f.sentInputsLock.Unlock()
	return f.sendErr
}

func (f *fakeMuxClient) StopSession(_ context.Context, sessionID string) error {
	f.stopped = append(f.stopped, sessionID)
	return f.stopErr
}

func TestMuxProxy_ListAvailableLaunches(t *testing.T) {
	fake := &fakeMuxClient{launches: []agentmux.Launch{
		{ID: "lx-1", Project: "nanite", Agent: "backend", Provider: "claudestream"},
	}}
	mgr := muxproxy.NewManagerWithStream(nil)
	svc := NewMuxProxy(fake, mgr)

	got, err := svc.ListAvailableLaunches(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "lx-1" {
		t.Fatalf("unexpected: %+v", got)
	}
}

func TestMuxProxy_LaunchSubordinate_RejectsNonClaudestream(t *testing.T) {
	fake := &fakeMuxClient{launchResp: agentmux.LaunchResponse{
		ID:         "sess-A",
		ProviderID: "pty-claude",
	}}
	mgr := muxproxy.NewManagerWithStream(nil)
	svc := NewMuxProxy(fake, mgr)

	_, err := svc.LaunchSubordinate(context.Background(), "lx-1", "A")
	if err == nil || !errors.Is(err, ErrUnsupportedProvider) {
		t.Fatalf("want ErrUnsupportedProvider, got %v", err)
	}
}

func TestMuxProxy_LaunchSubordinate_RegistersNickname(t *testing.T) {
	fake := &fakeMuxClient{launchResp: agentmux.LaunchResponse{
		ID:         "sess-A",
		ProviderID: "claudestream",
	}}
	mgr := muxproxy.NewManagerWithStream(nil)
	svc := NewMuxProxy(fake, mgr)

	out, err := svc.LaunchSubordinate(context.Background(), "lx-1", "Alice")
	if err != nil {
		t.Fatal(err)
	}
	if out.SessionID != "sess-A" {
		t.Fatalf("want sess-A, got %q", out.SessionID)
	}
	if mgr.Nickname("sess-A") != "Alice" {
		t.Fatalf("nickname not registered; got %q", mgr.Nickname("sess-A"))
	}
}

func TestMuxProxy_Send_BlocksUntilDone(t *testing.T) {
	fake := &fakeMuxClient{}
	mgr := muxproxy.NewManagerWithStream(nil)
	svc := NewMuxProxy(fake, mgr)
	svc.sendTimeout = 2 * time.Second

	// Pre-register channel so Send can find it.
	mgr.Register("sess-A", "Alice")

	go func() {
		// Simulate Manager dispatching events to the waiter.
		time.Sleep(50 * time.Millisecond)
		ch := mgr.WaiterChannel("sess-A")
		ch <- claudestream.Event{Kind: claudestream.KindDelta, Text: "hello "}
		ch <- claudestream.Event{Kind: claudestream.KindDelta, Text: "world"}
		ch <- claudestream.Event{Kind: claudestream.KindDone, Usage: &claudestream.Usage{InputTokens: 10, OutputTokens: 2}}
	}()

	out, err := svc.Send(context.Background(), "sess-A", "ping")
	if err != nil {
		t.Fatal(err)
	}
	if out.Transcript != "hello world" {
		t.Fatalf("want transcript=hello world, got %q", out.Transcript)
	}
	if out.ExitStatus != "done" {
		t.Fatalf("want exit_status=done, got %q", out.ExitStatus)
	}
	if fake.sentInputs[0] != "ping\n" {
		t.Fatalf("want input=%q, got %q", "ping\n", fake.sentInputs[0])
	}
}

func TestMuxProxy_Send_Timeout(t *testing.T) {
	fake := &fakeMuxClient{}
	mgr := muxproxy.NewManagerWithStream(nil)
	svc := NewMuxProxy(fake, mgr)
	svc.sendTimeout = 100 * time.Millisecond

	mgr.Register("sess-A", "Alice")

	out, err := svc.Send(context.Background(), "sess-A", "ping")
	if err != nil {
		t.Fatal(err)
	}
	if out.ExitStatus != "timeout" {
		t.Fatalf("want exit_status=timeout, got %q", out.ExitStatus)
	}
}

func TestMuxProxy_Stop(t *testing.T) {
	fake := &fakeMuxClient{}
	mgr := muxproxy.NewManagerWithStream(nil)
	svc := NewMuxProxy(fake, mgr)
	mgr.Register("sess-A", "Alice")

	if err := svc.Stop(context.Background(), "sess-A"); err != nil {
		t.Fatal(err)
	}
	if len(fake.stopped) != 1 || fake.stopped[0] != "sess-A" {
		t.Fatalf("stop not propagated: %+v", fake.stopped)
	}
	if mgr.Nickname("sess-A") != "" {
		t.Fatal("expected nickname cleared")
	}
}
