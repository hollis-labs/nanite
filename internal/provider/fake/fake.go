// Package fake provides a scripted model provider for deterministic tests.
package fake

import (
	"context"
	"fmt"
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
	go func() {
		defer close(out)
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
		if response.Stall {
			<-ctx.Done()
		}
	}()
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
		}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("fake provider: stream closed without terminal event")
}

func (*Provider) Capabilities() llmtypes.ProviderCapabilities { return llmtypes.ProviderCapabilities{} }
