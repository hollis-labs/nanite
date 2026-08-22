package service

import (
	"context"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/lifecycle"
)

type blockingPluginEventSink struct {
	PluginEventSink
	started chan struct{}
	release chan struct{}
}

func (s *blockingPluginEventSink) EmitSessionStart(string, string, string) {
	close(s.started)
	<-s.release
}

func TestCompositeEmitter_PluginDispatchDrainsOnLifecycleShutdown(t *testing.T) {
	manager := lifecycle.NewManager("composite-events-test")
	sink := &blockingPluginEventSink{started: make(chan struct{}), release: make(chan struct{})}
	emitter := NewCompositeEmitter(nil, sink, manager)
	emitter.EmitSessionStart(context.Background(), "session", "agent", "model", "mode")

	select {
	case <-sink.started:
	case <-time.After(time.Second):
		t.Fatal("plugin dispatch did not start")
	}

	shutdownDone := make(chan error, 1)
	go func() { shutdownDone <- manager.Shutdown(time.Second) }()
	select {
	case err := <-shutdownDone:
		t.Fatalf("shutdown returned before plugin dispatch drained: %v", err)
	case <-time.After(25 * time.Millisecond):
	}

	close(sink.release)
	select {
	case err := <-shutdownDone:
		if err != nil {
			t.Fatalf("shutdown: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown did not return after plugin dispatch completed")
	}
}
