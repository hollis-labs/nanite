package chat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestActivityEmitter_DisabledWhenNoURL(t *testing.T) {
	// When created with no URL and no env var, emitter should be disabled.
	t.Setenv("ENGINE_ACTIVITY_URL", "")
	em := NewActivityEmitter("")
	if !em.disabled {
		t.Fatal("expected emitter to be disabled when no URL is set")
	}

	// Emit should be a no-op (no panic, no HTTP call).
	em.Emit(context.Background(), activityEvent{
		EventType:  EventSessionCreated,
		EntityType: "chat_session",
		EntityID:   "test-session",
	})
}

func TestActivityEmitter_SendsToEngine(t *testing.T) {
	var mu sync.Mutex
	var received []activityEvent

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/activity/events" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Method != http.MethodPost {
			t.Errorf("unexpected method: %s", r.Method)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var ev activityEvent
		if err := json.NewDecoder(r.Body).Decode(&ev); err != nil {
			t.Errorf("decode error: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		mu.Lock()
		received = append(received, ev)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	em := NewActivityEmitter(srv.URL)
	if em.disabled {
		t.Fatal("emitter should not be disabled when URL is provided")
	}

	ctx := context.Background()

	// Emit various events.
	em.EmitSessionCreated(ctx, "sess-1", "ws-1")
	em.EmitSessionEnded(ctx, "sess-1")
	em.EmitAgentAssigned(ctx, "sess-1", "agent-1", "default")
	em.EmitToolCall(ctx, "sess-1", "grep", true, 500)
	em.EmitToolCall(ctx, "sess-1", "write", false, 0)
	em.EmitRateLimitHit(ctx, "sess-1", "anthropic", 30*time.Second)
	em.EmitCircuitBreakerTripped(ctx, "sess-1", "anthropic")
	em.EmitContextBudgetExceeded(ctx, "sess-1", 250000, 200000)
	em.EmitSessionStart(ctx, "sess-1", "agent-1", "claude-sonnet-4-20250514")
	em.EmitResponseComplete(ctx, "sess-1", "agent-1", "claude-sonnet-4-20250514", 1000, 500)
	em.EmitError(ctx, "sess-1", "provider_error", "connection refused")

	mu.Lock()
	defer mu.Unlock()

	if len(received) != 11 {
		t.Fatalf("expected 11 events, got %d", len(received))
	}

	// Verify event types.
	expectedTypes := []string{
		EventSessionCreated,
		EventSessionEnded,
		EventAgentAssigned,
		EventToolExecuted,
		EventToolExecuted,
		EventRateLimitHit,
		EventCircuitBreakerTripped,
		EventContextBudgetExceeded,
		EventSessionActive,
		EventResponseComplete,
		EventError,
	}
	for i, ev := range received {
		if ev.EventType != expectedTypes[i] {
			t.Errorf("event %d: expected type %q, got %q", i, expectedTypes[i], ev.EventType)
		}
		if ev.ProjectID != "nanite" {
			t.Errorf("event %d: expected project_id 'nanite', got %q", i, ev.ProjectID)
		}
	}
}

func TestActivityEmitter_GracefulOnServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	em := NewActivityEmitter(srv.URL)
	// Should not panic when server returns 500.
	em.EmitSessionCreated(context.Background(), "sess-1", "ws-1")
}

func TestActivityEmitter_GracefulOnUnreachable(t *testing.T) {
	em := NewActivityEmitter("http://127.0.0.1:1") // port 1 — guaranteed unreachable
	// Should not panic, just log a warning.
	em.EmitSessionCreated(context.Background(), "sess-1", "ws-1")
}

func TestActivityEmitter_ViaEnvVar(t *testing.T) {
	t.Setenv("ENGINE_ACTIVITY_URL", "http://example.com:9999")
	em := NewActivityEmitter("")
	if em.disabled {
		t.Fatal("emitter should not be disabled when ENGINE_ACTIVITY_URL is set")
	}
	if em.baseURL != "http://example.com:9999" {
		t.Errorf("expected baseURL from ENGINE_ACTIVITY_URL, got %q", em.baseURL)
	}
}

func TestEventTypeConstants(t *testing.T) {
	// Ensure all constants are non-empty and unique.
	constants := []string{
		EventSessionCreated,
		EventSessionEnded,
		EventSessionActive,
		EventAgentAssigned,
		EventToolExecuted,
		EventRateLimitHit,
		EventCircuitBreakerTripped,
		EventContextBudgetExceeded,
		EventResponseComplete,
		EventError,
	}
	seen := make(map[string]bool)
	for _, c := range constants {
		if c == "" {
			t.Error("event type constant is empty")
		}
		if seen[c] {
			t.Errorf("duplicate event type constant: %s", c)
		}
		seen[c] = true
	}
}
