package subprocess

import (
	"context"
	"encoding/json"
	"io"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/hollis-labs/plugin-sdk"
	sdkplugin "github.com/hollis-labs/plugin-sdk"
)

// captureConsumer records every envelope batch handed to Deliver so the test
// can assert what reached the session-scoped SSE consumer.
type captureConsumer struct {
	mu        sync.Mutex
	delivered []captured
	returnOK  bool
}

type captured struct {
	sessionID string
	envs      []sdkplugin.EnvelopeOut
}

func (c *captureConsumer) Deliver(sessionID string, envs []sdkplugin.EnvelopeOut) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.delivered = append(c.delivered, captured{sessionID: sessionID, envs: envs})
	return c.returnOK
}

// TestEventHook_PostHookDeliversEnvelopesToConsumer drives the B-wave contract
// BLG-20260413-012 closes: a subprocess plugin reacting to a post-hook event
// (message.sent) emits EventHandleResult.Envelopes; the proxy must (1) switch
// from Notify to a request/response call for post-hook events, (2) run the
// returned envelopes through the B.11 filter, and (3) route them to the
// session-scoped envelope consumer.
func TestEventHook_PostHookDeliversEnvelopesToConsumer(t *testing.T) {
	hostToPluginR, hostToPluginW := io.Pipe()
	pluginToHostR, pluginToHostW := io.Pipe()

	wantEnv := sdkplugin.EnvelopeOut{
		Type: "oembed-card",
		Data: map[string]interface{}{"url": "https://example.com/video"},
	}

	var sawRequest atomic.Bool
	handlers := map[string]func(json.RawMessage) (any, *RPCError){
		MethodEventHandle: func(params json.RawMessage) (any, *RPCError) {
			var p EventHandleParams
			if err := json.Unmarshal(params, &p); err != nil {
				return nil, &RPCError{Code: -32602, Message: err.Error()}
			}
			if p.Type != "message.sent" {
				t.Errorf("unexpected event type: %s", p.Type)
			}
			if p.SessionID != "sess-1" {
				t.Errorf("unexpected session id: %s", p.SessionID)
			}
			if p.PreHook {
				t.Errorf("expected post-hook invocation, got PreHook=true")
			}
			sawRequest.Store(true)
			return &EventHandleResult{
				Envelopes: []sdkplugin.EnvelopeOut{wantEnv},
			}, nil
		},
	}
	go mockPlugin(hostToPluginR, pluginToHostW, handlers)

	transport := NewTransport(pluginToHostR, hostToPluginW)
	defer transport.Close()

	// Filter: require the validator to run exactly once and pass the envelope
	// through unchanged. Mirrors the "strict validator — plugin id bound"
	// closure the loader installs in production (SubprocessPlugin.SetEnvelopeFilter).
	var filterCalls atomic.Int32
	filter := func(envs []sdkplugin.EnvelopeOut) []sdkplugin.EnvelopeOut {
		filterCalls.Add(1)
		return envs
	}

	consumer := &captureConsumer{returnOK: true}
	hook := NewEventHook("test-plugin", []string{"message.sent"}, transport, filter, consumer)

	err := hook.Handle(context.Background(), plugin.Event{
		Type:      "message.sent",
		SessionID: "sess-1",
		Source:    "test",
		Data:      map[string]interface{}{"content": "hello"},
	})
	if err != nil {
		t.Fatalf("Handle returned error: %v", err)
	}

	if !sawRequest.Load() {
		t.Fatal("plugin handler never received the event — post-hook path still using Notify?")
	}
	if got := filterCalls.Load(); got != 1 {
		t.Fatalf("filter call count: want 1, got %d", got)
	}

	consumer.mu.Lock()
	defer consumer.mu.Unlock()
	if len(consumer.delivered) != 1 {
		t.Fatalf("delivered batches: want 1, got %d", len(consumer.delivered))
	}
	got := consumer.delivered[0]
	if got.sessionID != "sess-1" {
		t.Errorf("delivery sessionID: want sess-1, got %q", got.sessionID)
	}
	if len(got.envs) != 1 || got.envs[0].Type != "oembed-card" {
		t.Errorf("delivered envelopes: want [oembed-card], got %+v", got.envs)
	}
}

// TestEventHook_PostHookFilterDropsAllEnvelopes asserts that when the B.11
// filter strips every envelope (invalid shape → drop), no delivery attempt
// is made against the consumer. This keeps the host-side invariant that
// Deliver only sees validated envelopes.
func TestEventHook_PostHookFilterDropsAllEnvelopes(t *testing.T) {
	hostToPluginR, hostToPluginW := io.Pipe()
	pluginToHostR, pluginToHostW := io.Pipe()

	handlers := map[string]func(json.RawMessage) (any, *RPCError){
		MethodEventHandle: func(_ json.RawMessage) (any, *RPCError) {
			return &EventHandleResult{
				Envelopes: []sdkplugin.EnvelopeOut{{Type: "bad-shape"}},
			}, nil
		},
	}
	go mockPlugin(hostToPluginR, pluginToHostW, handlers)
	transport := NewTransport(pluginToHostR, hostToPluginW)
	defer transport.Close()

	filter := func(_ []sdkplugin.EnvelopeOut) []sdkplugin.EnvelopeOut { return nil }
	consumer := &captureConsumer{}
	hook := NewEventHook("test-plugin", []string{"message.sent"}, transport, filter, consumer)

	if err := hook.Handle(context.Background(), plugin.Event{
		Type: "message.sent", SessionID: "s", Source: "t",
	}); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	consumer.mu.Lock()
	defer consumer.mu.Unlock()
	if len(consumer.delivered) != 0 {
		t.Fatalf("filter dropped envelopes but consumer still called: %+v", consumer.delivered)
	}
}

// TestEventHook_PreHookUnchanged confirms pre-hook events still flow through
// the cancellation-aware request/response path and do not touch the envelope
// consumer — BLG-012 only changes post-hook semantics.
func TestEventHook_PreHookUnchanged(t *testing.T) {
	hostToPluginR, hostToPluginW := io.Pipe()
	pluginToHostR, pluginToHostW := io.Pipe()

	handlers := map[string]func(json.RawMessage) (any, *RPCError){
		MethodEventHandle: func(params json.RawMessage) (any, *RPCError) {
			var p EventHandleParams
			_ = json.Unmarshal(params, &p)
			if !p.PreHook {
				t.Errorf("expected PreHook=true")
			}
			return &EventHandleResult{Cancel: true}, nil
		},
	}
	go mockPlugin(hostToPluginR, pluginToHostW, handlers)
	transport := NewTransport(pluginToHostR, hostToPluginW)
	defer transport.Close()

	consumer := &captureConsumer{}
	hook := NewEventHook("test-plugin", []string{"message.sending"}, transport, nil, consumer)

	err := hook.Handle(context.Background(), plugin.Event{
		Type: "message.sending", SessionID: "s", Source: "t",
	})
	if err == nil || err != plugin.ErrCancelled {
		t.Fatalf("pre-hook cancel: want ErrCancelled, got %v", err)
	}
	consumer.mu.Lock()
	defer consumer.mu.Unlock()
	if len(consumer.delivered) != 0 {
		t.Fatalf("pre-hook should not deliver envelopes; got %+v", consumer.delivered)
	}
}

// compile-time: verify captureConsumer satisfies EnvelopeConsumer.
var _ EnvelopeConsumer = (*captureConsumer)(nil)
