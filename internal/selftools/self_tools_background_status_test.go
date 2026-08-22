package selftools

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/background"
	"github.com/hollis-labs/nanite/internal/classify"
	"github.com/hollis-labs/nanite/internal/messaging"
)

type immediateBackgroundBackend struct{}

func (immediateBackgroundBackend) Start(_ context.Context, jobID string, _ background.JobRequest, onComplete background.CompletionFunc) error {
	now := time.Now().UTC()
	onComplete(jobID, background.BackendCompletion{
		Status:      background.StatusSucceeded,
		Output:      "done",
		StartedAt:   now.Add(-time.Second),
		CompletedAt: now,
	})
	return nil
}

func (immediateBackgroundBackend) Status(string) (background.JobStatus, error) {
	return "", background.ErrUnknownJob
}

func (immediateBackgroundBackend) Cancel(string) error { return nil }

type discardBackgroundMessenger struct{}

func (discardBackgroundMessenger) SendMessage(context.Context, messaging.SendInput) (*messaging.Message, error) {
	return &messaging.Message{ID: "test-message"}, nil
}

func TestCallBackgroundStatus_ExpiredIsStructuredTerminalResult(t *testing.T) {
	svc := background.NewService(immediateBackgroundBackend{}, discardBackgroundMessenger{})
	ids := make([]string, 0, 101)
	for i := 0; i <= 100; i++ {
		id, err := svc.Submit(context.Background(), classify.PatternBackground, background.JobRequest{
			Task:                 "true",
			OriginatingSessionID: "session",
			OriginatingAgentID:   "agent",
		})
		if err != nil {
			t.Fatalf("Submit %d: %v", i, err)
		}
		ids = append(ids, id)
	}
	var expiredID string
	for _, id := range ids {
		if _, err := svc.Result(id); errors.Is(err, background.ErrExpiredJob) {
			expiredID = id
			break
		}
	}
	if expiredID == "" {
		t.Fatal("hard-cap precondition did not evict a completed result")
	}
	if result, err := svc.Result(expiredID); result.Status != background.StatusExpired || !errors.Is(err, background.ErrExpiredJob) {
		t.Fatalf("precondition Result(%q) = (%+v, %v); want expired", expiredID, result, err)
	}

	transport := &SelfToolsTransport{Background: svc}
	result, err := transport.CallTool(context.Background(), "background_status", map[string]any{"job_id": expiredID})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if result.IsError {
		t.Fatalf("expired background_status returned tool error: %+v", result)
	}
	var payload background.JobResult
	if err := json.Unmarshal([]byte(result.Content[0].Text), &payload); err != nil {
		t.Fatalf("decode result %q: %v", result.Content[0].Text, err)
	}
	if payload.JobID != expiredID || payload.Status != background.StatusExpired {
		t.Fatalf("payload = %+v; want job_id %q and status expired", payload, expiredID)
	}
	if payload.Error != background.ErrExpiredJob.Error() {
		t.Fatalf("payload error = %q; want %q", payload.Error, background.ErrExpiredJob.Error())
	}
}

func TestCallBackgroundStatus_UnknownRemainsToolError(t *testing.T) {
	transport := &SelfToolsTransport{Background: background.NewService(immediateBackgroundBackend{}, discardBackgroundMessenger{})}
	result, err := transport.CallTool(context.Background(), "background_status", map[string]any{"job_id": "never-issued"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !result.IsError {
		t.Fatalf("unknown background_status returned a normal result: %+v", result)
	}
}
