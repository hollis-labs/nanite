package service

import (
	"context"
	"errors"
	"testing"
	"time"

	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/nanite/internal/lifecycle"
)

type cancellationObservingProvider struct {
	started  chan struct{}
	canceled chan struct{}
}

func (*cancellationObservingProvider) StreamChat(context.Context, llmtypes.ChatRequest) (<-chan llmtypes.StreamEvent, error) {
	return nil, nil
}

func (p *cancellationObservingProvider) Complete(ctx context.Context, _ llmtypes.ChatRequest) (string, error) {
	close(p.started)
	<-ctx.Done()
	close(p.canceled)
	return "", ctx.Err()
}

func (*cancellationObservingProvider) Capabilities() llmtypes.ProviderCapabilities {
	return llmtypes.ProviderCapabilities{}
}

func TestAutoTitle_ChatShutdownCancelsAndDrainsJob(t *testing.T) {
	providers := provider.NewRegistry()
	p := &cancellationObservingProvider{started: make(chan struct{}), canceled: make(chan struct{})}
	providers.Register("utility", p)
	svc := &chatServiceImpl{
		providers:       providers,
		utilityProvider: "utility",
		lifecycle:       lifecycle.NewManager("auto-title-test"),
	}

	svc.goTracked("autoTitle", func(ctx context.Context) {
		svc.autoTitle(ctx, "session", "hello")
	})
	select {
	case <-p.started:
	case <-time.After(time.Second):
		t.Fatal("auto-title provider call did not start")
	}

	done := make(chan struct{})
	go func() { svc.Shutdown(); close(done) }()
	select {
	case <-p.canceled:
	case <-time.After(time.Second):
		t.Fatal("auto-title provider did not observe lifecycle cancellation")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("chat shutdown did not drain canceled auto-title job")
	}
	if !errors.Is(svc.lifecycle.Context().Err(), context.Canceled) {
		t.Fatalf("lifecycle context error = %v, want canceled", svc.lifecycle.Context().Err())
	}
}

func TestChatService_ShutdownReportsLifecycleDrainFailure(t *testing.T) {
	owner := lifecycle.NewManager("chat-drain-failure-test")
	started := make(chan struct{})
	release := make(chan struct{})
	owner.Go("blocked-callback", func(context.Context) {
		close(started)
		<-release
	})
	<-started
	svc := &chatServiceImpl{lifecycle: owner}

	err := svc.shutdownWithMaxWait(20 * time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shutdown error = %v, want context deadline exceeded", err)
	}

	close(release)
	if err := owner.Shutdown(time.Second); err != nil {
		t.Fatalf("drain cleanup: %v", err)
	}
}
