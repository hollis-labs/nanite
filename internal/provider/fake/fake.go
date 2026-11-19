// Package fake provides a scripted model provider for deterministic tests.
package fake

import (
	"context"
	"fmt"
	"github.com/hollis-labs/nanite/internal/provider/failure"
	"slices"
	"strings"
	"sync"

	"github.com/hollis-labs/libs/llm/core/llmcontracts"
	"github.com/hollis-labs/libs/llm/core/llmtypes"
)

type Response struct {
	Events []llmtypes.StreamEvent
	Err    error
	Stall  bool
	// Panic, when set, makes the stream goroutine panic with this value
	// after sending Events, to exercise panic containment.
	Panic any
}

type Provider struct {
	mu    sync.Mutex
	queue []Response
}

var _ llmcontracts.Provider = (*Provider)(nil)

func New(responses ...Response) *Provider {
	queue := slices.Clone(responses)
	for i := range queue {
		queue[i].Events = slices.Clone(queue[i].Events)
	}
	return &Provider{queue: queue}
}

func Text(text string) Response {
	return Response{Events: []llmtypes.StreamEvent{
		{Type: llmtypes.EventDelta, Content: text},
		{Type: llmtypes.EventDone},
	}}
}

func (p *Provider) StreamChat(ctx context.Context, _ llmtypes.ChatRequest) (<-chan llmtypes.StreamEvent, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.mu.Lock()
	if len(p.queue) == 0 {
		p.mu.Unlock()
		return nil, fmt.Errorf("fake provider: response queue exhausted")
	}
	response := p.queue[0]
	p.queue[0] = Response{}
	p.queue = p.queue[1:]
	p.mu.Unlock()
	if response.Err != nil {
		return nil, response.Err
	}
	out := make(chan llmtypes.StreamEvent)
	failure.Guard(nil).Go("fake stream", func() {
		defer close(out)
		defer failure.RecoverStream(ctx, nil, "fake stream", out)
		for _, ev := range response.Events {
			select {
			case out <- ev:
			case <-ctx.Done():
				return
			}
			if llmtypes.IsTurnComplete(ev) {
				return
			}
		}
		if response.Panic != nil {
			panic(response.Panic)
		}
		if response.Stall {
			<-ctx.Done()
		}
	}, nil)
	return out, nil
}

func (p *Provider) Complete(ctx context.Context, req llmtypes.ChatRequest) (string, error) {
	stream, err := p.StreamChat(ctx, req)
	if err != nil {
		return "", err
	}
	var text strings.Builder
	for event := range stream {
		switch event.Type {
		case llmtypes.EventDelta:
			text.WriteString(event.Content)
		case llmtypes.EventError:
			return "", fmt.Errorf("fake provider: %s", event.Error)
		case llmtypes.EventDone:
			return text.String(), nil
		default:
			// Only text and terminal events affect Complete.
		}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("fake provider: stream closed without terminal event")
}

func (*Provider) Capabilities() llmtypes.ProviderCapabilities { return llmtypes.ProviderCapabilities{} }
