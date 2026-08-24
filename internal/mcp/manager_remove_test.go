package mcp

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"
)

type blockingCloseTransport struct {
	closeStarted chan struct{}
	releaseClose chan struct{}
	closeErr     error
}

func (t *blockingCloseTransport) ListTools(context.Context) ([]Tool, error) {
	return nil, nil
}

func (t *blockingCloseTransport) CallTool(context.Context, string, map[string]any) (*ToolResult, error) {
	return nil, nil
}

func (t *blockingCloseTransport) Close() error {
	close(t.closeStarted)
	<-t.releaseClose
	return t.closeErr
}

type managerTestTransport struct{}

func (managerTestTransport) ListTools(context.Context) ([]Tool, error) { return nil, nil }
func (managerTestTransport) CallTool(context.Context, string, map[string]any) (*ToolResult, error) {
	return nil, nil
}

func waitForManagerSignal(t *testing.T, ch <-chan struct{}, failure string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal(failure)
	}
}

func finishBlockingRemoval(t *testing.T, transport *blockingCloseTransport, removeDone <-chan struct{}) {
	t.Helper()
	select {
	case <-transport.releaseClose:
	default:
		close(transport.releaseClose)
	}
	waitForManagerSignal(t, removeDone, "RemoveServer did not finish after Close was released")
}

func TestManagerRemoveServer_CloseDoesNotHoldRegistryLock(t *testing.T) {
	transport := &blockingCloseTransport{
		closeStarted: make(chan struct{}),
		releaseClose: make(chan struct{}),
	}
	mgr := NewManager()
	if err := mgr.AddServer("blocked", transport, TierThirdPartyHTTP); err != nil {
		t.Fatalf("AddServer(blocked): %v", err)
	}
	entry := &toolEntry{
		serverName:  "blocked",
		uniformName: "blocked_tool",
		tool:        Tool{Name: "blocked_tool"},
	}
	mgr.mu.Lock()
	mgr.tools = append(mgr.tools, entry)
	mgr.uniformIndex[entry.uniformName] = entry
	mgr.mu.Unlock()

	removeDone := make(chan struct{})
	go func() {
		mgr.RemoveServer("blocked")
		close(removeDone)
	}()
	t.Cleanup(func() { finishBlockingRemoval(t, transport, removeDone) })
	waitForManagerSignal(t, transport.closeStarted, "RemoveServer did not enter transport Close")

	// Removal is visible before Close completes, and an unrelated writer can
	// acquire the manager lock while the closer remains blocked.
	if _, err := mgr.DiscoverServerTools(context.Background(), "blocked"); err == nil {
		t.Fatal("removed server remained visible while Close was blocked")
	}
	if _, _, ok := mgr.ToolAttribution("blocked_tool"); ok {
		t.Fatal("removed server's tool remained indexed while Close was blocked")
	}
	if tools := mgr.GetAllToolsUnfiltered(); len(tools) != 0 {
		t.Fatalf("removed server's tool remained in slice while Close was blocked: %+v", tools)
	}
	addDone := make(chan error, 1)
	go func() {
		addDone <- mgr.AddServer("unrelated", managerTestTransport{}, TierBuiltin)
	}()
	select {
	case err := <-addDone:
		if err != nil {
			t.Fatalf("unrelated AddServer: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("unrelated AddServer blocked behind transport Close")
	}

	finishBlockingRemoval(t, transport, removeDone)
}

type closeErrorTransport struct {
	managerTestTransport
	err error
}

func (t closeErrorTransport) Close() error { return t.err }

func TestManagerRemoveServer_CloseFailureStillWarns(t *testing.T) {
	var logs bytes.Buffer
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(previousLogger) })

	mgr := NewManager()
	transport := closeErrorTransport{err: errors.New("close boom")}
	if err := mgr.AddServer("broken-close", transport, TierThirdPartyHTTP); err != nil {
		t.Fatalf("AddServer: %v", err)
	}
	mgr.RemoveServer("broken-close")

	gotLogs := logs.String()
	if !strings.Contains(gotLogs, "mcp: close transport during removal failed") ||
		!strings.Contains(gotLogs, `"server":"broken-close"`) ||
		!strings.Contains(gotLogs, "close boom") {
		t.Fatalf("close warning missing message/server/error: %s", gotLogs)
	}
}
