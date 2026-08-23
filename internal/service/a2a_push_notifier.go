package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/hollis-labs/nanite/internal/a2a"
	"github.com/hollis-labs/nanite/internal/ssrf"
	"github.com/hollis-labs/nanite/internal/store"
)

// A2APushNotifier handles best-effort push notification delivery for A2A task
// state transitions. This is an accelerant over the durable a2a_tasks record,
// not the source of truth — per docs/architecture/a2a-protocol-design.md.
type A2APushNotifier struct {
	store  *store.Store
	client *http.Client
	logger *slog.Logger

	// resolver and dialer are injectable seams for proving that every DNS
	// answer is checked and the validated literal is what gets dialed.
	resolver ssrf.Resolver
	dialer   func(ctx context.Context, network, addr string) (net.Conn, error)

	// Production defaults are HTTPS-only and deny localhost. Tests that use a
	// loopback httptest server must opt into both exceptions explicitly.
	allowHTTP      bool
	allowLocalhost bool
}

// NewA2APushNotifier constructs an A2APushNotifier with the given store and logger.
func NewA2APushNotifier(st *store.Store, logger *slog.Logger) *A2APushNotifier {
	if logger == nil {
		logger = slog.Default()
	}
	return &A2APushNotifier{
		store: st,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
		logger: logger,
	}
}

// EnqueueDelivery creates a new push delivery record for the given task state
// transition. This is called by TaskManager whenever a task's state changes
// and the task has a registered PushNotificationConfig.
func (pn *A2APushNotifier) EnqueueDelivery(taskID string, state a2a.TaskState) error {
	delivery := &store.A2APushDelivery{
		ID:           ulid.Make().String(),
		TaskID:       taskID,
		TargetState:  string(state),
		AttemptCount: 0,
		NextRetry:    time.Now(), // Immediate first attempt
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}

	if err := pn.store.CreateA2APushDelivery(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, delivery); err != nil {
		pn.logger.Error("a2a push: failed to enqueue delivery",
			"task_id", taskID,
			"state", state,
			"error", err,
		)
		return err
	}

	pn.logger.Debug("a2a push: enqueued delivery",
		"task_id", taskID,
		"state", state,
		"delivery_id", delivery.ID,
	)

	return nil
}

// ProcessPendingDeliveries retrieves and processes all pending push deliveries
// that are due for retry. This is called by the background worker ticker.
// Bounded retries: max 3 attempts, with exponential backoff.
func (pn *A2APushNotifier) ProcessPendingDeliveries(ctx context.Context) error {
	deliveries, err := pn.store.GetPendingPushDeliveries(ctx, time.Now())
	if err != nil {
		return fmt.Errorf("failed to get pending deliveries: %w", err)
	}

	if len(deliveries) == 0 {
		return nil
	}

	pn.logger.Debug("a2a push: processing pending deliveries", "count", len(deliveries))

	for _, delivery := range deliveries {
		if err := pn.processDelivery(ctx, delivery); err != nil {
			pn.logger.Error("a2a push: delivery processing failed",
				"delivery_id", delivery.ID,
				"task_id", delivery.TaskID,
				"error", err,
			)
		}
	}

	return nil
}

// processDelivery attempts to deliver a single push notification.
func (pn *A2APushNotifier) processDelivery(ctx context.Context, delivery *store.A2APushDelivery) error {
	const maxAttempts = 3

	// Fetch the task to get the push config and current state.
	task, err := pn.store.GetA2ATask(ctx, delivery.TaskID)
	if err != nil {
		return fmt.Errorf("failed to get task: %w", err)
	}
	if task == nil {
		pn.logger.Warn("a2a push: task not found, skipping delivery",
			"task_id", delivery.TaskID,
			"delivery_id", delivery.ID,
		)
		// Delete the delivery record since the task is gone.
		return pn.store.DeleteA2APushDelivery(ctx, delivery.ID)
	}

	// Parse the push notification config.
	if !task.PushNotificationConfig.Valid || task.PushNotificationConfig.String == "" {
		pn.logger.Warn("a2a push: task has no push config, skipping delivery",
			"task_id", delivery.TaskID,
			"delivery_id", delivery.ID,
		)
		// Delete the delivery record since there's no config.
		return pn.store.DeleteA2APushDelivery(ctx, delivery.ID)
	}

	var config a2a.PushNotificationConfig
	if err := json.Unmarshal([]byte(task.PushNotificationConfig.String), &config); err != nil {
		pn.logger.Error("a2a push: failed to unmarshal push config",
			"task_id", delivery.TaskID,
			"delivery_id", delivery.ID,
			"error", err,
		)
		// Delete the delivery record since the config is invalid.
		return pn.store.DeleteA2APushDelivery(ctx, delivery.ID)
	}

	// Prepare the notification payload.
	notification := a2a.TaskStateNotification{
		TaskID:    delivery.TaskID,
		State:     a2a.TaskState(delivery.TargetState),
		Timestamp: time.Now(),
	}

	payloadBytes, err := json.Marshal(notification)
	if err != nil {
		return fmt.Errorf("failed to marshal notification: %w", err)
	}

	// Attempt delivery.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, config.URL, bytes.NewReader(payloadBytes))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if config.Token != "" {
		req.Header.Set("Authorization", "Bearer "+config.Token)
	}
	if config.Auth != "" {
		req.Header.Set("Authorization", config.Auth)
	}

	resp, err := pn.secureHTTPClient().Do(req)
	if err != nil {
		delivery.AttemptCount++
		delivery.LastError = err.Error()
		delivery.UpdatedAt = time.Now()

		if delivery.AttemptCount >= maxAttempts {
			pn.logger.Warn("a2a push: max attempts reached, deleting delivery",
				"task_id", delivery.TaskID,
				"delivery_id", delivery.ID,
				"attempts", delivery.AttemptCount,
				"error", err,
			)
			return pn.store.DeleteA2APushDelivery(ctx, delivery.ID)
		}

		// Exponential backoff: 1min, 5min, 15min
		backoff := time.Duration(delivery.AttemptCount*delivery.AttemptCount) * time.Minute
		delivery.NextRetry = time.Now().Add(backoff)

		pn.logger.Warn("a2a push: delivery failed, scheduling retry",
			"task_id", delivery.TaskID,
			"delivery_id", delivery.ID,
			"attempts", delivery.AttemptCount,
			"next_retry", delivery.NextRetry,
			"error", err,
		)

		return pn.store.UpdateA2APushDelivery(ctx, delivery)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		delivery.AttemptCount++
		delivery.LastError = fmt.Sprintf("HTTP %d", resp.StatusCode)
		delivery.UpdatedAt = time.Now()

		if delivery.AttemptCount >= maxAttempts {
			pn.logger.Warn("a2a push: max attempts reached after HTTP error, deleting delivery",
				"task_id", delivery.TaskID,
				"delivery_id", delivery.ID,
				"attempts", delivery.AttemptCount,
				"status", resp.StatusCode,
			)
			return pn.store.DeleteA2APushDelivery(ctx, delivery.ID)
		}

		// Exponential backoff
		backoff := time.Duration(delivery.AttemptCount*delivery.AttemptCount) * time.Minute
		delivery.NextRetry = time.Now().Add(backoff)

		pn.logger.Warn("a2a push: delivery HTTP error, scheduling retry",
			"task_id", delivery.TaskID,
			"delivery_id", delivery.ID,
			"attempts", delivery.AttemptCount,
			"status", resp.StatusCode,
			"next_retry", delivery.NextRetry,
		)

		return pn.store.UpdateA2APushDelivery(ctx, delivery)
	}

	// Success — delete the delivery record.
	pn.logger.Info("a2a push: delivery successful",
		"task_id", delivery.TaskID,
		"delivery_id", delivery.ID,
		"state", delivery.TargetState,
	)

	return pn.store.DeleteA2APushDelivery(ctx, delivery.ID)
}

// roundTripperFunc lets the notifier enforce its endpoint-specific HTTPS rule
// immediately before every request, including redirects, while the shared
// destination-address policy remains owned by internal/ssrf.
type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func (pn *A2APushNotifier) secureHTTPClient() *http.Client {
	client := &http.Client{Timeout: 10 * time.Second}
	if pn.client != nil {
		*client = *pn.client
	}

	resolver := pn.resolver
	if resolver == nil {
		resolver = ssrf.DefaultResolver
	}
	innerDial := pn.dialer
	if innerDial == nil {
		dialer := &net.Dialer{Timeout: 5 * time.Second}
		innerDial = dialer.DialContext
	}

	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			pinned, err := ssrf.ResolveAndPin(ctx, resolver, host, pn.allowLocalhost)
			if err != nil {
				return nil, err
			}
			return innerDial(ctx, network, net.JoinHostPort(pinned.String(), port))
		},
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
		DisableKeepAlives:     true,
	}
	client.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		if err := pn.validateWebhookURL(req.URL); err != nil {
			return nil, err
		}
		return transport.RoundTrip(req)
	})
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return fmt.Errorf("a2a push: too many redirects")
		}
		return pn.validateWebhookURL(req.URL)
	}
	return client
}

func (pn *A2APushNotifier) validateWebhookURL(target *url.URL) error {
	if target == nil || target.Hostname() == "" {
		return fmt.Errorf("a2a push: webhook URL missing host")
	}
	if target.Scheme == "https" {
		return nil
	}
	if pn.allowHTTP && target.Scheme == "http" {
		return nil
	}
	return fmt.Errorf("a2a push: unsupported webhook scheme %q (HTTPS required)", target.Scheme)
}
