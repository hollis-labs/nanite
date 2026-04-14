package plugin

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	pluginsdk "github.com/hollis-labs/plugin-sdk"

	"github.com/hollis-labs/nanite/internal/store"
)

// testConnector is a minimal Connector implementation for testing.
//
// Dispatch runs Send on a background goroutine while the test goroutine
// inspects stub state after waitForSend. sendCount is atomic so waitForSend
// can poll it safely, but any other state the test observes (lastPayload)
// must be guarded by mu so reads from the test goroutine don't race with
// writes from the dispatch goroutine.
type testConnector struct {
	name      string
	sendCount atomic.Int32

	mu          sync.Mutex
	lastPayload map[string]interface{}

	sendErr   error
	healthErr error
}

func (c *testConnector) Name() string { return c.name }

func (c *testConnector) Send(_ context.Context, payload map[string]interface{}) error {
	c.mu.Lock()
	c.lastPayload = payload
	c.mu.Unlock()
	// Increment after the protected write so waitForSend observing sendCount>=N
	// guarantees the Nth payload is already visible under the mutex.
	c.sendCount.Add(1)
	return c.sendErr
}

// getLastPayload returns the most recent payload passed to Send, guarded
// by the stub's mutex. Tests should use this helper rather than touching
// lastPayload directly.
func (c *testConnector) getLastPayload() map[string]interface{} {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastPayload
}

func (c *testConnector) Health(_ context.Context) error {
	return c.healthErr
}

// waitForSend polls the connector's sendCount until it reaches the expected value
// or the timeout expires. Dispatch fires goroutines, so we must wait for them.
func waitForSend(t *testing.T, conn *testConnector, expected int32, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if conn.sendCount.Load() >= expected {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func newTestHostWithStore(t *testing.T) (*Host, *store.Store) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("open test store: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	h := NewHost(http.NewServeMux(), NewLogger("test"))
	h.SetStore(s)
	return h, s
}

func TestTriggerDispatch_BasicFlow(t *testing.T) {
	h, s := newTestHostWithStore(t)

	// Register a connector.
	conn := &testConnector{name: "test-webhook"}
	h.RegisterConnector("test-webhook", conn)

	// Create a trigger rule.
	rule := &store.TriggerRule{
		PluginID:        "test-plugin",
		EventType:       "session.end",
		ConnectorName:   "test-webhook",
		PayloadTemplate: "{}",
		Enabled:         true,
	}
	if err := s.CreateTriggerRule(rule); err != nil {
		t.Fatalf("create rule: %v", err)
	}

	// Emit the event.
	event := NewEvent("session.end", "nanite", EventData{SessionID: "s1"})
	h.triggers.Dispatch(event)
	waitForSend(t, conn, 1, 2*time.Second)

	if conn.sendCount.Load() != 1 {
		t.Errorf("expected 1 send, got %d", conn.sendCount.Load())
	}
}

func TestTriggerDispatch_FilterExpr(t *testing.T) {
	h, s := newTestHostWithStore(t)

	conn := &testConnector{name: "filtered-conn"}
	h.RegisterConnector("filtered-conn", conn)

	rule := &store.TriggerRule{
		PluginID:      "p1",
		EventType:     "tool.called",
		ConnectorName: "filtered-conn",
		FilterExpr:    "tool_name=web_fetch",
		Enabled:       true,
	}
	if err := s.CreateTriggerRule(rule); err != nil {
		t.Fatalf("create rule: %v", err)
	}

	// Event that doesn't match the filter.
	noMatch := NewEvent("tool.called", "nanite", EventData{ToolName: "grep"})
	h.triggers.Dispatch(noMatch)
	if conn.sendCount.Load() != 0 {
		t.Error("expected no send for non-matching filter")
	}

	// Event that matches the filter.
	matches := NewEvent("tool.called", "nanite", EventData{ToolName: "web_fetch"})
	h.triggers.Dispatch(matches)
	waitForSend(t, conn, 1, 2*time.Second)
	if conn.sendCount.Load() != 1 {
		t.Errorf("expected 1 send for matching filter, got %d", conn.sendCount.Load())
	}
}

func TestTriggerDispatch_PayloadTemplate(t *testing.T) {
	h, s := newTestHostWithStore(t)

	conn := &testConnector{name: "tmpl-conn"}
	h.RegisterConnector("tmpl-conn", conn)

	rule := &store.TriggerRule{
		PluginID:        "p1",
		EventType:       "session.end",
		ConnectorName:   "tmpl-conn",
		PayloadTemplate: `{"event": "{{.Type}}", "session": "{{.SessionID}}"}`,
		Enabled:         true,
	}
	if err := s.CreateTriggerRule(rule); err != nil {
		t.Fatalf("create rule: %v", err)
	}

	event := NewEvent("session.end", "nanite", EventData{SessionID: "abc-123"})
	h.triggers.Dispatch(event)
	waitForSend(t, conn, 1, 2*time.Second)

	if conn.sendCount.Load() != 1 {
		t.Fatalf("expected 1 send, got %d", conn.sendCount.Load())
	}
	payload := conn.getLastPayload()
	if payload["event"] != "session.end" {
		t.Errorf("expected event=session.end, got %v", payload["event"])
	}
	if payload["session"] != "abc-123" {
		t.Errorf("expected session=abc-123, got %v", payload["session"])
	}
}

func TestTriggerDispatch_EmptyTemplate(t *testing.T) {
	h, s := newTestHostWithStore(t)

	conn := &testConnector{name: "passthrough"}
	h.RegisterConnector("passthrough", conn)

	rule := &store.TriggerRule{
		PluginID:        "p1",
		EventType:       "session.start",
		ConnectorName:   "passthrough",
		PayloadTemplate: "{}",
		Enabled:         true,
	}
	if err := s.CreateTriggerRule(rule); err != nil {
		t.Fatalf("create rule: %v", err)
	}

	event := NewEvent("session.start", "nanite", EventData{SessionID: "s1", AgentID: "a1"})
	h.triggers.Dispatch(event)
	waitForSend(t, conn, 1, 2*time.Second)

	if conn.sendCount.Load() != 1 {
		t.Fatalf("expected 1 send, got %d", conn.sendCount.Load())
	}
	// Empty template passes through event.Data.
	payload := conn.getLastPayload()
	if payload["session_id"] != "s1" {
		t.Errorf("expected session_id=s1 in passthrough payload, got %v", payload["session_id"])
	}
}

func TestTriggerDispatch_DisabledRule(t *testing.T) {
	h, s := newTestHostWithStore(t)

	conn := &testConnector{name: "disabled-conn"}
	h.RegisterConnector("disabled-conn", conn)

	rule := &store.TriggerRule{
		PluginID:      "p1",
		EventType:     "session.end",
		ConnectorName: "disabled-conn",
		Enabled:       false,
	}
	if err := s.CreateTriggerRule(rule); err != nil {
		t.Fatalf("create rule: %v", err)
	}

	event := NewEvent("session.end", "nanite", EventData{SessionID: "s1"})
	h.triggers.Dispatch(event)

	if conn.sendCount.Load() != 0 {
		t.Error("disabled rule should not trigger connector send")
	}
}

func TestTriggerDispatch_UnknownConnector(t *testing.T) {
	h, s := newTestHostWithStore(t)

	// Rule references a connector that isn't registered.
	rule := &store.TriggerRule{
		PluginID:      "p1",
		EventType:     "session.end",
		ConnectorName: "nonexistent",
		Enabled:       true,
	}
	if err := s.CreateTriggerRule(rule); err != nil {
		t.Fatalf("create rule: %v", err)
	}

	// Should not panic.
	event := NewEvent("session.end", "nanite", EventData{SessionID: "s1"})
	h.triggers.Dispatch(event)
}

func TestTriggerDispatch_RetryOnFailure(t *testing.T) {
	h, s := newTestHostWithStore(t)

	conn := &testConnector{
		name:    "flaky",
		sendErr: fmt.Errorf("connection refused"),
	}
	h.RegisterConnector("flaky", conn)

	// Use minimal backoff for fast test.
	h.triggers.MaxRetries = 2
	h.triggers.InitialBackoff = 1 * time.Millisecond
	h.triggers.MaxBackoff = 10 * time.Millisecond

	rule := &store.TriggerRule{
		PluginID:      "p1",
		EventType:     "session.end",
		ConnectorName: "flaky",
		Enabled:       true,
	}
	if err := s.CreateTriggerRule(rule); err != nil {
		t.Fatalf("create rule: %v", err)
	}

	event := NewEvent("session.end", "nanite", EventData{SessionID: "s1"})
	h.triggers.Dispatch(event)
	waitForSend(t, conn, 3, 5*time.Second)

	// 1 initial + 2 retries = 3 total attempts.
	if conn.sendCount.Load() != 3 {
		t.Errorf("expected 3 send attempts (1 + 2 retries), got %d", conn.sendCount.Load())
	}

	// Connector should be marked unhealthy.
	statuses := h.GetConnectorStatuses()
	for _, s := range statuses {
		if s.Name == "flaky" && s.Healthy {
			t.Error("expected flaky connector to be marked unhealthy")
		}
	}
}

func TestConnectorHealth_Healthy(t *testing.T) {
	h := NewHost(http.NewServeMux(), NewLogger("test"))

	conn := &testConnector{name: "healthy-conn", healthErr: nil}
	h.RegisterConnector("healthy-conn", conn)

	status := h.CheckConnectorHealth("healthy-conn")
	if status == nil {
		t.Fatal("expected status, got nil")
	}
	if !status.Healthy {
		t.Error("expected healthy status")
	}
	if status.ConsecutiveFailures != 0 {
		t.Errorf("expected 0 consecutive failures, got %d", status.ConsecutiveFailures)
	}
}

func TestConnectorHealth_Unhealthy(t *testing.T) {
	h := NewHost(http.NewServeMux(), NewLogger("test"))

	conn := &testConnector{name: "sick-conn", healthErr: fmt.Errorf("connection timeout")}
	h.RegisterConnector("sick-conn", conn)

	status := h.CheckConnectorHealth("sick-conn")
	if status == nil {
		t.Fatal("expected status, got nil")
	}
	if status.Healthy {
		t.Error("expected unhealthy status")
	}
	if status.LastError != "connection timeout" {
		t.Errorf("expected 'connection timeout', got %q", status.LastError)
	}
	if status.ConsecutiveFailures != 1 {
		t.Errorf("expected 1 consecutive failure, got %d", status.ConsecutiveFailures)
	}
}

func TestConnectorHealth_RecoveryAfterFailure(t *testing.T) {
	h := NewHost(http.NewServeMux(), NewLogger("test"))

	conn := &testConnector{name: "recovers", healthErr: fmt.Errorf("down")}
	h.RegisterConnector("recovers", conn)

	// First check: unhealthy.
	h.CheckConnectorHealth("recovers")

	// Connector recovers.
	conn.healthErr = nil
	status := h.CheckConnectorHealth("recovers")
	if !status.Healthy {
		t.Error("expected healthy after recovery")
	}
	if status.ConsecutiveFailures != 0 {
		t.Error("expected consecutive failures reset to 0")
	}
}

func TestConnectorHealth_NotFound(t *testing.T) {
	h := NewHost(http.NewServeMux(), NewLogger("test"))

	status := h.CheckConnectorHealth("nonexistent")
	if status != nil {
		t.Error("expected nil for nonexistent connector")
	}
}

func TestCheckAllConnectorHealth(t *testing.T) {
	h := NewHost(http.NewServeMux(), NewLogger("test"))

	h.RegisterConnector("a", &testConnector{name: "a"})
	h.RegisterConnector("b", &testConnector{name: "b", healthErr: fmt.Errorf("error")})

	statuses := h.CheckAllConnectorHealth()
	if len(statuses) != 2 {
		t.Fatalf("expected 2 statuses, got %d", len(statuses))
	}

	healthy := 0
	for _, s := range statuses {
		if s.Healthy {
			healthy++
		}
	}
	if healthy != 1 {
		t.Errorf("expected 1 healthy connector, got %d", healthy)
	}
}

func TestEventStreamSubscribe(t *testing.T) {
	h := NewHost(http.NewServeMux(), NewLogger("test"))

	ch := h.SubscribeEvents()
	defer h.UnsubscribeEvents(ch)

	// Emit an event — it should appear on the channel.
	event := pluginsdk.Event{
		Type:   "test.event",
		Source: "test",
	}
	h.broadcastEvent(event)

	select {
	case received := <-ch:
		if received.Type != "test.event" {
			t.Errorf("expected test.event, got %s", received.Type)
		}
	case <-time.After(1 * time.Second):
		t.Error("timed out waiting for event on subscriber channel")
	}
}

func TestEventStreamUnsubscribe(t *testing.T) {
	h := NewHost(http.NewServeMux(), NewLogger("test"))

	ch := h.SubscribeEvents()
	h.UnsubscribeEvents(ch)

	// UnsubscribeEvents does NOT close the channel (to avoid send-to-closed panics
	// in broadcastEvent). Verify the subscriber was removed by broadcasting an event
	// and confirming it does NOT arrive on the unsubscribed channel.
	h.broadcastEvent(pluginsdk.Event{Type: "after.unsub", Source: "test"})
	select {
	case <-ch:
		t.Error("received event on unsubscribed channel")
	case <-time.After(100 * time.Millisecond):
		// Expected — no event arrives.
	}

	// Should not panic when broadcasting with no subscribers.
	h.broadcastEvent(pluginsdk.Event{Type: "noop"})
}

func TestMatchFilter(t *testing.T) {
	td := &TriggerDispatcher{logger: NewLogger("test")}

	tests := []struct {
		name   string
		expr   string
		data   map[string]interface{}
		expect bool
	}{
		{"empty filter matches all", "", map[string]interface{}{"x": "y"}, true},
		{"single match", "tool_name=grep", map[string]interface{}{"tool_name": "grep"}, true},
		{"single no match", "tool_name=grep", map[string]interface{}{"tool_name": "read"}, false},
		{"missing key", "tool_name=grep", map[string]interface{}{}, false},
		{"multi match", "tool_name=grep,session_id=s1", map[string]interface{}{"tool_name": "grep", "session_id": "s1"}, true},
		{"multi partial", "tool_name=grep,session_id=s2", map[string]interface{}{"tool_name": "grep", "session_id": "s1"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := td.matchFilter(tt.expr, tt.data)
			if got != tt.expect {
				t.Errorf("matchFilter(%q) = %v, want %v", tt.expr, got, tt.expect)
			}
		})
	}
}
