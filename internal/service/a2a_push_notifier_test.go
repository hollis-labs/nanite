package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/a2a"
	"github.com/hollis-labs/nanite/internal/agentworkflow"
	"github.com/hollis-labs/nanite/internal/ssrf"
	"github.com/hollis-labs/nanite/internal/store"
)

func allowLocalHTTPWebhookTest(notifier *A2APushNotifier) {
	notifier.allowHTTP = true
	notifier.allowLocalhost = true
}

func TestA2APushNotifierRequiresHTTPSByDefault(t *testing.T) {
	notifier := NewA2APushNotifier(nil, nil)
	dialed := false
	notifier.resolver = func(context.Context, string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("203.0.113.10")}, nil
	}
	notifier.dialer = func(context.Context, string, string) (net.Conn, error) {
		dialed = true
		return nil, errors.New("unexpected dial")
	}

	req, err := http.NewRequest(http.MethodPost, "http://callback.example/hook", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := notifier.secureHTTPClient().Do(req); err == nil || !strings.Contains(err.Error(), "HTTPS required") {
		t.Fatalf("HTTP webhook error = %v, want HTTPS-required rejection", err)
	}
	if dialed {
		t.Fatal("HTTP webhook reached the dialer before scheme rejection")
	}
}

func TestA2APushNotifierBlocksSharedSSRFPolicyRanges(t *testing.T) {
	tests := map[string]string{
		"rfc1918-10":       "10.1.2.3",
		"rfc1918-172":      "172.16.1.2",
		"rfc1918-192":      "192.168.1.2",
		"ipv4-loopback":    "127.0.0.1",
		"ipv6-loopback":    "::1",
		"imds-link-local":  "169.254.169.254",
		"ipv6-link-local":  "fe80::1",
		"cgnat":            "100.64.0.1",
		"ipv6-ula":         "fd00::1",
		"ipv4-unspecified": "0.0.0.0",
		"ipv6-unspecified": "::",
	}
	for name, blocked := range tests {
		t.Run(name, func(t *testing.T) {
			notifier := NewA2APushNotifier(nil, nil)
			dialed := false
			notifier.resolver = func(context.Context, string) ([]net.IP, error) {
				return []net.IP{net.ParseIP(blocked)}, nil
			}
			notifier.dialer = func(context.Context, string, string) (net.Conn, error) {
				dialed = true
				return nil, errors.New("unexpected dial")
			}

			req, err := http.NewRequest(http.MethodPost, "https://callback.example/hook", nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := notifier.secureHTTPClient().Do(req); !errors.Is(err, ssrf.ErrBlocked) {
				t.Fatalf("blocked IP %s error = %v, want ssrf.ErrBlocked", blocked, err)
			}
			if dialed {
				t.Fatalf("blocked IP %s reached the dialer", blocked)
			}
		})
	}
}

func TestA2APushNotifierRejectsMixedDNSAnswers(t *testing.T) {
	notifier := NewA2APushNotifier(nil, nil)
	dialed := false
	notifier.resolver = func(context.Context, string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("203.0.113.10"), net.ParseIP("169.254.169.254")}, nil
	}
	notifier.dialer = func(context.Context, string, string) (net.Conn, error) {
		dialed = true
		return nil, errors.New("unexpected dial")
	}

	req, err := http.NewRequest(http.MethodPost, "https://callback.example/hook", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := notifier.secureHTTPClient().Do(req); !errors.Is(err, ssrf.ErrBlocked) {
		t.Fatalf("mixed DNS answer error = %v, want ssrf.ErrBlocked", err)
	}
	if dialed {
		t.Fatal("mixed DNS answer reached the dialer")
	}
}

func TestA2APushNotifierDialsPinnedLiteral(t *testing.T) {
	notifier := NewA2APushNotifier(nil, nil)
	var dialAddr string
	dialErr := errors.New("stop after pinned dial")
	notifier.resolver = func(context.Context, string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("203.0.113.25")}, nil
	}
	notifier.dialer = func(_ context.Context, _ string, addr string) (net.Conn, error) {
		dialAddr = addr
		return nil, dialErr
	}

	req, err := http.NewRequest(http.MethodPost, "https://callback.example/hook", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := notifier.secureHTTPClient().Do(req); !errors.Is(err, dialErr) {
		t.Fatalf("pinned dial error = %v, want sentinel", err)
	}
	if dialAddr != "203.0.113.25:443" {
		t.Fatalf("dial address = %q, want pinned literal", dialAddr)
	}
}

func TestA2APushNotifierRevalidatesRedirectDNS(t *testing.T) {
	redirectServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/redirected", http.StatusFound)
	}))
	defer redirectServer.Close()

	notifier := NewA2APushNotifier(nil, nil)
	notifier.allowHTTP = true
	resolveCalls := 0
	notifier.resolver = func(context.Context, string) ([]net.IP, error) {
		resolveCalls++
		if resolveCalls == 1 {
			return []net.IP{net.ParseIP("203.0.113.10")}, nil
		}
		return []net.IP{net.ParseIP("127.0.0.1")}, nil
	}
	dialCalls := 0
	notifier.dialer = func(ctx context.Context, network, _ string) (net.Conn, error) {
		dialCalls++
		return (&net.Dialer{}).DialContext(ctx, network, redirectServer.Listener.Addr().String())
	}

	req, err := http.NewRequest(http.MethodPost, "http://callback.example/hook", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := notifier.secureHTTPClient().Do(req); !errors.Is(err, ssrf.ErrBlocked) {
		t.Fatalf("redirect rebind error = %v, want ssrf.ErrBlocked", err)
	}
	if resolveCalls != 2 {
		t.Fatalf("resolver calls = %d, want initial request plus redirect", resolveCalls)
	}
	if dialCalls != 1 {
		t.Fatalf("inner dial calls = %d, want only the validated initial request", dialCalls)
	}
}

func TestA2APushNotifierSSRFRejectionUsesRetryBackoff(t *testing.T) {
	ctx := context.Background()
	st, err := store.New(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer st.Close(context.Background())

	configJSON, err := json.Marshal(&a2a.PushNotificationConfig{URL: "https://callback.example/hook"})
	if err != nil {
		t.Fatal(err)
	}
	task := &store.A2ATask{
		ID:                     "test-task-ssrf-retry",
		TargetKind:             "workflow",
		TargetRef:              "test-workflow",
		Message:                "test message",
		State:                  a2a.TaskStateSubmitted,
		PushNotificationConfig: sql.NullString{String: string(configJSON), Valid: true},
	}
	if err := st.CreateA2ATask(ctx, task); err != nil {
		t.Fatalf("CreateA2ATask: %v", err)
	}

	notifier := NewA2APushNotifier(st, nil)
	notifier.resolver = func(context.Context, string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("127.0.0.1")}, nil
	}
	dialed := false
	notifier.dialer = func(context.Context, string, string) (net.Conn, error) {
		dialed = true
		return nil, errors.New("unexpected dial")
	}
	if err := notifier.EnqueueDelivery(task.ID, a2a.TaskStateWorking); err != nil {
		t.Fatalf("EnqueueDelivery: %v", err)
	}

	before := time.Now()
	if err := notifier.ProcessPendingDeliveries(ctx); err != nil {
		t.Fatalf("ProcessPendingDeliveries: %v", err)
	}
	if dialed {
		t.Fatal("blocked callback reached the dialer")
	}
	pending, err := st.GetPendingPushDeliveries(ctx, time.Now().Add(2*time.Minute))
	if err != nil {
		t.Fatalf("GetPendingPushDeliveries: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("pending deliveries = %d, want 1 scheduled retry", len(pending))
	}
	if pending[0].AttemptCount != 1 {
		t.Fatalf("attempt count = %d, want 1", pending[0].AttemptCount)
	}
	if !strings.Contains(pending[0].LastError, ssrf.ErrBlocked.Error()) {
		t.Fatalf("last error = %q, want SSRF classifier", pending[0].LastError)
	}
	if pending[0].NextRetry.Before(before.Add(59 * time.Second)) {
		t.Fatalf("next retry = %s, want existing one-minute backoff", pending[0].NextRetry)
	}
}

// TestA2APushNotifierEnqueueAndDeliver is an integration test that verifies
// the full push notification lifecycle:
// 1. Task has PushNotificationConfig
// 2. State transition triggers EnqueueDelivery
// 3. ProcessPendingDeliveries attempts delivery
// 4. HTTP POST is sent to configured URL
// 5. Successful delivery deletes the record
func TestA2APushNotifierEnqueueAndDeliver(t *testing.T) {
	ctx := context.Background()

	// Set up test store
	s, err := store.New(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close(context.

		// Set up mock webhook server
		Background())

	var receivedNotifications []a2a.TaskStateNotification
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("Expected POST request, got %s", r.Method)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		var notification a2a.TaskStateNotification
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &notification); err != nil {
			t.Errorf("Failed to unmarshal notification: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		receivedNotifications = append(receivedNotifications, notification)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	// Create a task with push notification config
	pushConfig := &a2a.PushNotificationConfig{
		URL:   server.URL,
		Token: "test-token",
	}
	configJSON, _ := json.Marshal(pushConfig)

	task := &store.A2ATask{
		ID:                     "test-task-123",
		TargetKind:             "workflow",
		TargetRef:              "test-workflow",
		Message:                "test message",
		State:                  a2a.TaskStateSubmitted,
		PushNotificationConfig: sql.NullString{String: string(configJSON), Valid: true},
	}

	if err := s.CreateA2ATask(context.Background(), task); err != nil {
		t.Fatalf("CreateA2ATask: %v", err)
	}

	// Create push notifier
	notifier := NewA2APushNotifier(s, nil)
	allowLocalHTTPWebhookTest(notifier)

	// Enqueue a delivery for state transition
	newState := a2a.TaskStateWorking
	if err := notifier.EnqueueDelivery(task.ID, newState); err != nil {
		t.Fatalf("EnqueueDelivery: %v", err)
	}

	// Verify delivery was enqueued
	deliveries, err := s.GetPendingPushDeliveries(context.Background(), time.Now().Add(1*time.Minute))
	if err != nil {
		t.Fatalf("GetPendingPushDeliveries: %v", err)
	}
	if len(deliveries) != 1 {
		t.Fatalf("Expected 1 pending delivery, got %d", len(deliveries))
	}
	if deliveries[0].TaskID != task.ID {
		t.Errorf("Expected task ID %s, got %s", task.ID, deliveries[0].TaskID)
	}
	if deliveries[0].TargetState != string(newState) {
		t.Errorf("Expected target state %s, got %s", newState, deliveries[0].TargetState)
	}

	// Process pending deliveries
	if err := notifier.ProcessPendingDeliveries(ctx); err != nil {
		t.Fatalf("ProcessPendingDeliveries: %v", err)
	}

	// Verify notification was received
	if len(receivedNotifications) != 1 {
		t.Fatalf("Expected 1 notification received, got %d", len(receivedNotifications))
	}
	notification := receivedNotifications[0]
	if notification.TaskID != task.ID {
		t.Errorf("Expected task ID %s, got %s", task.ID, notification.TaskID)
	}
	if notification.State != newState {
		t.Errorf("Expected state %s, got %s", newState, notification.State)
	}

	// Verify delivery was deleted after success
	deliveries, err = s.GetPendingPushDeliveries(context.Background(), time.Now().Add(1*time.Minute))
	if err != nil {
		t.Fatalf("GetPendingPushDeliveries: %v", err)
	}
	if len(deliveries) != 0 {
		t.Errorf("Expected 0 pending deliveries after success, got %d", len(deliveries))
	}
}

// TestA2APushNotifierRetry verifies that failed deliveries are retried with backoff.
func TestA2APushNotifierRetry(t *testing.T) {
	ctx := context.Background()

	// Set up test store
	s, err := store.New(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close(context.

		// Set up mock webhook server that fails initially
		Background())

	attemptCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attemptCount++
		// Fail first 2 attempts, succeed on 3rd
		if attemptCount < 3 {
			w.WriteHeader(http.StatusInternalServerError)
		} else {
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer server.Close()

	// Create task with push config
	pushConfig := &a2a.PushNotificationConfig{
		URL: server.URL,
	}
	configJSON, _ := json.Marshal(pushConfig)

	task := &store.A2ATask{
		ID:                     "test-task-retry",
		TargetKind:             "workflow",
		TargetRef:              "test-workflow",
		Message:                "test message",
		State:                  a2a.TaskStateSubmitted,
		PushNotificationConfig: sql.NullString{String: string(configJSON), Valid: true},
	}

	if err := s.CreateA2ATask(context.Background(), task); err != nil {
		t.Fatalf("CreateA2ATask: %v", err)
	}

	notifier := NewA2APushNotifier(s, nil)
	allowLocalHTTPWebhookTest(notifier)

	// Enqueue delivery
	if err := notifier.EnqueueDelivery(task.ID, a2a.TaskStateWorking); err != nil {
		t.Fatalf("EnqueueDelivery: %v", err)
	}

	// First attempt should fail and schedule retry
	if err := notifier.ProcessPendingDeliveries(ctx); err != nil {
		t.Fatalf("ProcessPendingDeliveries (attempt 1): %v", err)
	}

	// Verify delivery still pending with incremented attempt count
	deliveries, err := s.GetPendingPushDeliveries(context.Background(), time.Now().Add(10*time.Minute))
	if err != nil {
		t.Fatalf("GetPendingPushDeliveries: %v", err)
	}
	if len(deliveries) != 1 {
		t.Fatalf("Expected 1 pending delivery after first failure, got %d", len(deliveries))
	}
	if deliveries[0].AttemptCount != 1 {
		t.Errorf("Expected attempt count 1, got %d", deliveries[0].AttemptCount)
	}

	// Update next_retry to now to allow immediate retry
	deliveries[0].NextRetry = time.Now()
	if err := s.UpdateA2APushDelivery(context.Background(), deliveries[0]); err != nil {
		t.Fatalf("UpdateA2APushDelivery: %v", err)
	}

	// Second attempt should also fail
	if err := notifier.ProcessPendingDeliveries(ctx); err != nil {
		t.Fatalf("ProcessPendingDeliveries (attempt 2): %v", err)
	}

	deliveries, err = s.GetPendingPushDeliveries(context.Background(), time.Now().Add(10*time.Minute))
	if err != nil {
		t.Fatalf("GetPendingPushDeliveries: %v", err)
	}
	if len(deliveries) != 1 {
		t.Fatalf("Expected 1 pending delivery after second failure, got %d", len(deliveries))
	}
	if deliveries[0].AttemptCount != 2 {
		t.Errorf("Expected attempt count 2, got %d", deliveries[0].AttemptCount)
	}

	// Update next_retry for third attempt
	deliveries[0].NextRetry = time.Now()
	if err := s.UpdateA2APushDelivery(context.Background(), deliveries[0]); err != nil {
		t.Fatalf("UpdateA2APushDelivery: %v", err)
	}

	// Third attempt should succeed
	if err := notifier.ProcessPendingDeliveries(ctx); err != nil {
		t.Fatalf("ProcessPendingDeliveries (attempt 3): %v", err)
	}

	// Verify delivery was deleted after success
	deliveries, err = s.GetPendingPushDeliveries(context.Background(), time.Now().Add(10*time.Minute))
	if err != nil {
		t.Fatalf("GetPendingPushDeliveries: %v", err)
	}
	if len(deliveries) != 0 {
		t.Errorf("Expected 0 pending deliveries after success, got %d", len(deliveries))
	}

	// Verify all 3 attempts were made
	if attemptCount != 3 {
		t.Errorf("Expected 3 delivery attempts, got %d", attemptCount)
	}
}

// TestA2APushNotifierMaxRetries verifies that deliveries are deleted after max retries.
func TestA2APushNotifierMaxRetries(t *testing.T) {
	ctx := context.Background()

	// Set up test store
	s, err := store.New(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close(context.

		// Set up mock webhook server that always fails
		Background())

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	// Create task with push config
	pushConfig := &a2a.PushNotificationConfig{
		URL: server.URL,
	}
	configJSON, _ := json.Marshal(pushConfig)

	task := &store.A2ATask{
		ID:                     "test-task-maxretry",
		TargetKind:             "workflow",
		TargetRef:              "test-workflow",
		Message:                "test message",
		State:                  a2a.TaskStateSubmitted,
		PushNotificationConfig: sql.NullString{String: string(configJSON), Valid: true},
	}

	if err := s.CreateA2ATask(context.Background(), task); err != nil {
		t.Fatalf("CreateA2ATask: %v", err)
	}

	notifier := NewA2APushNotifier(s, nil)
	allowLocalHTTPWebhookTest(notifier)

	// Enqueue delivery
	if err := notifier.EnqueueDelivery(task.ID, a2a.TaskStateWorking); err != nil {
		t.Fatalf("EnqueueDelivery: %v", err)
	}

	// Process 3 times (max attempts)
	for i := 0; i < 3; i++ {
		if err := notifier.ProcessPendingDeliveries(ctx); err != nil {
			t.Fatalf("ProcessPendingDeliveries (attempt %d): %v", i+1, err)
		}

		// Update next_retry to allow immediate retry
		deliveries, err := s.GetPendingPushDeliveries(context.Background(), time.Now().Add(1*time.Hour))
		if err != nil {
			t.Fatalf("GetPendingPushDeliveries: %v", err)
		}

		if i < 2 {
			// Should still be pending for first 2 attempts
			if len(deliveries) != 1 {
				t.Fatalf("Expected 1 pending delivery after attempt %d, got %d", i+1, len(deliveries))
			}
			deliveries[0].NextRetry = time.Now()
			if err := s.UpdateA2APushDelivery(context.Background(), deliveries[0]); err != nil {
				t.Fatalf("UpdateA2APushDelivery: %v", err)
			}
		} else {
			// Should be deleted after 3rd attempt
			if len(deliveries) != 0 {
				t.Errorf("Expected 0 pending deliveries after max attempts, got %d", len(deliveries))
			}
		}
	}
}

// TestA2APushNotifier_TickerDrivenPath_EndToEnd verifies the full production
// wiring the cmd/nanite/main.go 30s ticker exercises — CancelTask (a real
// TaskManager state-transition call site) enqueues a delivery via
// TaskManager.enqueuePushNotification, and calling ProcessPendingDeliveries
// (exactly what the ticker calls on every tick) drains it to a real local
// HTTP listener. Unlike the three tests above, this does not call
// EnqueueDelivery directly — it goes through the same production entry
// point (TaskManager.CancelTask -> a2a_task_manager.go:enqueuePushNotification)
// the ticker exists to drain, closing the "extend to cover the
// ticker-driven path end to end" gap named in
// TASKS/phase-0/08-a2a-conformance.md's part (c).
func TestA2APushNotifier_TickerDrivenPath_EndToEnd(t *testing.T) {
	ctx := context.Background()

	st, err := store.New(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer st.Close(context.Background())

	var received []a2a.TaskStateNotification
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var n a2a.TaskStateNotification
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &n); err != nil {
			t.Errorf("failed to unmarshal notification: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		received = append(received, n)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	// Real routing prerequisites for an 'instance'-target task: an
	// AgentProfile + DurableAgentInstance, matching what SubmitTask's real
	// production path requires.
	profile := &store.AgentProfile{Name: "A2A Ticker Path Test Agent", Slug: "a2a-ticker-path-test-agent", SystemPrompt: "x"}
	if err := st.CreateAgent(context.Background(), profile); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	inst := &store.DurableAgentInstance{
		ID:             "inst_ticker_path_test",
		Name:           "ticker-path-test-instance",
		Slug:           "ticker-path-test-instance",
		LifecycleClass: store.DurableAgentClassProcess,
		ProfileID:      profile.ID,
		Status:         store.DurableAgentStatusActive,
	}
	if err := st.CreateDurableAgentInstance(context.Background(), inst); err != nil {
		t.Fatalf("CreateDurableAgentInstance: %v", err)
	}

	pushConfig := &a2a.PushNotificationConfig{URL: server.URL}
	configJSON, err := json.Marshal(pushConfig)
	if err != nil {
		t.Fatalf("marshal push config: %v", err)
	}
	task := &store.A2ATask{
		ID:                     "task_ticker_path_test",
		TargetKind:             "instance",
		TargetRef:              "msg://agent/nanite/" + inst.ID,
		Message:                "test message",
		State:                  a2a.TaskStateWorking,
		DurableAgentInstanceID: sql.NullString{String: inst.ID, Valid: true},
		PushNotificationConfig: sql.NullString{String: string(configJSON), Valid: true},
	}
	if err := st.CreateA2ATask(context.Background(), task); err != nil {
		t.Fatalf("CreateA2ATask: %v", err)
	}

	canceller := &fakeDurableAgentCanceller{}
	tm := NewTaskManager(st, nil, nil, canceller, agentworkflow.NewRegistry(nil), nil)
	allowLocalHTTPWebhookTest(tm.PushNotifier())

	// Real production call site: CancelTask's internal
	// enqueuePushNotification call, not a direct EnqueueDelivery call.
	if _, err := tm.CancelTask(ctx, task.ID); err != nil {
		t.Fatalf("CancelTask: %v", err)
	}

	deliveries, err := st.GetPendingPushDeliveries(context.Background(), time.Now().Add(1*time.Minute))
	if err != nil {
		t.Fatalf("GetPendingPushDeliveries: %v", err)
	}
	if len(deliveries) != 1 {
		t.Fatalf("expected 1 pending delivery enqueued by CancelTask, got %d", len(deliveries))
	}
	if deliveries[0].TargetState != string(a2a.TaskStateCanceled) {
		t.Errorf("delivery target state = %q, want %q", deliveries[0].TargetState, a2a.TaskStateCanceled)
	}

	// Exactly what the cmd/nanite/main.go ticker calls every 30s.
	if err := tm.PushNotifier().ProcessPendingDeliveries(ctx); err != nil {
		t.Fatalf("ProcessPendingDeliveries: %v", err)
	}

	if len(received) != 1 {
		t.Fatalf("expected 1 notification delivered to the local listener, got %d", len(received))
	}
	if received[0].TaskID != task.ID {
		t.Errorf("notification.TaskID = %q, want %q", received[0].TaskID, task.ID)
	}
	if received[0].State != a2a.TaskStateCanceled {
		t.Errorf("notification.State = %q, want %q", received[0].State, a2a.TaskStateCanceled)
	}

	// Delivery record is cleaned up after successful drain.
	remaining, err := st.GetPendingPushDeliveries(context.Background(), time.Now().Add(1*time.Minute))
	if err != nil {
		t.Fatalf("GetPendingPushDeliveries (after drain): %v", err)
	}
	if len(remaining) != 0 {
		t.Errorf("expected 0 pending deliveries after successful drain, got %d", len(remaining))
	}
}
