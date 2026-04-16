package subagent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/hollis-labs/nanite/internal/messaging"
	"github.com/hollis-labs/nanite/internal/safego"
)

// DefaultTimeoutSeconds bounds a runner when the caller doesn't set
// a timeout. 5 minutes matches the plan's §T9 spawn-request default.
const DefaultTimeoutSeconds = 300

// Runner executes the subagent's work and returns a structured
// Result. Implementations own the actual chat-engine invocation,
// agent role loading, and any MCP tool calls. The subagent package
// stays ignorant of the chat engine so this dependency lives in
// one place (the container wires the real runner; tests wire a
// stub).
//
// Runner MUST respect ctx cancellation — spawn.Cancel propagates
// via context.Cancel to trigger an in-flight runner to bail.
type Runner interface {
	Run(ctx context.Context, run *Run) (*Result, error)
}

// MessagePoster is the subset of messaging.Service that subagent
// needs to deliver a reply back to the parent. Narrow interface
// keeps this package testable without spinning up a whole messaging
// stack; *messaging.Service satisfies it structurally.
type MessagePoster interface {
	SendMessage(ctx context.Context, input messaging.SendInput) (*messaging.Message, error)
}

// Service coordinates the spawn → run → complete → reply flow.
// Safe for concurrent use.
type Service struct {
	db     *sql.DB
	runner Runner
	poster MessagePoster
}

// NewService constructs a Service. The db is used for subagent_runs
// CRUD. The runner is the injected LLM-execution dependency (nil =
// no spawn permitted — Spawn returns an error). The poster delivers
// the reply message on completion (nil = reply skipped, run result
// is still visible via Status).
func NewService(db *sql.DB, runner Runner, poster MessagePoster) *Service {
	return &Service{db: db, runner: runner, poster: poster}
}

// Spawn inserts a subagent_runs row and (for sync/api/async MVP
// modes) dispatches the runner immediately. Returns the runID; sync
// mode blocks until the runner returns, async mode returns straight
// away and the caller can poll Status or watch for the reply
// message.
//
// Happy-path approval: the interactive approval envelope described
// in plan §D13 is DEFERRED. All three modes auto-approve for MVP —
// the distinction between them lives only in the reply-delivery
// channel (sync/api → chat, async → inbox) and blocking semantics
// (sync blocks, async/api return early).
func (svc *Service) Spawn(ctx context.Context, req SpawnRequest) (string, error) {
	if svc.runner == nil {
		return "", errors.New("subagent: no runner configured")
	}
	if req.ParentSessionID == "" {
		return "", errors.New("subagent: parent_session_id required")
	}
	if req.Role == "" {
		return "", errors.New("subagent: role required")
	}
	if req.Prompt == "" {
		return "", errors.New("subagent: prompt required")
	}
	mode := req.Mode
	if mode == "" {
		mode = ModeSync
	}
	switch mode {
	case ModeSync, ModeAsync, ModeAPI:
	default:
		return "", fmt.Errorf("subagent: invalid mode %q", mode)
	}
	timeout := req.TimeoutSeconds
	if timeout <= 0 {
		timeout = DefaultTimeoutSeconds
	}
	inputs := req.InputsJSON
	if inputs == "" {
		inputs = "{}"
	}

	run := &Run{
		ID:              uuid.New().String(),
		ParentSessionID: req.ParentSessionID,
		Role:            req.Role,
		Prompt:          req.Prompt,
		Mode:            mode,
		Status:          StatusRunning, // MVP: skip requested/approved
		InputsJSON:      inputs,
		TimeoutSeconds:  timeout,
		CreatedAt:       time.Now().UTC().Format(time.RFC3339Nano),
	}
	run.StartedAt = run.CreatedAt
	if err := svc.insertRun(ctx, run); err != nil {
		return "", fmt.Errorf("insert run: %w", err)
	}

	switch mode {
	case ModeSync:
		// Blocking: caller holds until the runner returns. The
		// parent's next turn can then reason over the reply.
		svc.execute(ctx, run, req.ParentAgentID)
	case ModeAsync, ModeAPI:
		// Non-blocking: fire-and-forget goroutine. The reply lands
		// in the parent session's inbox (async) or chat (api).
		safego.Go(context.Background(), "subagent.run", func() {
			svc.execute(context.Background(), run, req.ParentAgentID)
		})
	}

	return run.ID, nil
}

// Status returns the current Run row for runID, or ErrNotFound
// (wrapped sql.ErrNoRows) if no such run exists.
func (svc *Service) Status(ctx context.Context, runID string) (*Run, error) {
	row := svc.db.QueryRowContext(ctx, selectSQL+` WHERE id = ?`, runID)
	return scanRun(row)
}

// Cancel marks a run as cancelled. The in-flight runner receives
// the signal via the ctx it was started with (future hook — MVP
// runner doesn't wire a per-run cancel-context yet; the flag is
// strictly advisory). Idempotent.
func (svc *Service) Cancel(ctx context.Context, runID string) error {
	_, err := svc.db.ExecContext(ctx,
		`UPDATE subagent_runs SET status = ? WHERE id = ? AND status IN (?,?,?)`,
		StatusCancelled, runID, StatusRequested, StatusApproved, StatusRunning,
	)
	if err != nil {
		return fmt.Errorf("cancel run: %w", err)
	}
	return nil
}

// execute runs the runner, writes the result back to subagent_runs,
// and (if a poster is configured) delivers a reply message to the
// parent session. Separate from Spawn so async callers can launch
// it as a goroutine.
func (svc *Service) execute(ctx context.Context, run *Run, parentAgentID string) {
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(run.TimeoutSeconds)*time.Second)
	defer cancel()

	result, runErr := svc.runner.Run(runCtx, run)

	now := time.Now().UTC().Format(time.RFC3339Nano)
	if runErr != nil {
		run.Status = StatusFailed
		run.Error = runErr.Error()
	} else {
		run.Status = StatusCompleted
		if result != nil {
			run.ResultJSON = result.ResultJSON
			if run.ResultJSON == "" {
				run.ResultJSON = "{}"
			}
		}
	}
	run.CompletedAt = now

	if err := svc.finalizeRun(ctx, run); err != nil {
		slog.Warn("subagent: finalize run", "err", err, "run_id", run.ID)
	}

	if svc.poster == nil || parentAgentID == "" {
		return
	}
	// Reply-delivery channel: sync + api stay in-chat, async lands
	// in the inbox so the parent doesn't need to be actively
	// listening.
	replyChannel := messaging.ChannelChat
	if run.Mode == ModeAsync {
		replyChannel = messaging.ChannelInbox
	}
	summary := ""
	if result != nil {
		summary = result.Summary
	}
	if runErr != nil {
		summary = fmt.Sprintf("subagent %s failed: %v", run.ID, runErr)
	}
	resultPayload := run.ResultJSON
	if resultPayload == "" {
		resultPayload = "{}"
	}
	if _, err := svc.poster.SendMessage(ctx, messaging.SendInput{
		FromSessionID: run.ParentSessionID,
		FromAgentID:   run.Role, // the subagent is the sender
		ToSessionID:   run.ParentSessionID,
		ToAgentID:     parentAgentID,
		Channel:       replyChannel,
		Kind:          messaging.KindReply,
		Body:          summary,
		PayloadJSON:   resultPayload,
		Type:          messaging.TypeMessage,
		// The subagent is likely newly-seen from messaging's
		// perspective — stamp it as a CLI / internal auto-register
		// candidate. Clean-break: we don't add a new provenance
		// kind, 'external' is fine.
		RegisterAs: "external",
	}); err != nil {
		slog.Warn("subagent: reply delivery", "err", err, "run_id", run.ID)
	}
}

// insertRun persists a freshly-created run.
func (svc *Service) insertRun(ctx context.Context, r *Run) error {
	_, err := svc.db.ExecContext(ctx,
		`INSERT INTO subagent_runs (id, parent_session_id, child_session_id, role, prompt,
		                            mode, status, inputs_json, result_json, error,
		                            timeout_seconds, created_at, started_at, completed_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.ParentSessionID, r.ChildSessionID, r.Role, r.Prompt,
		r.Mode, r.Status, r.InputsJSON, r.ResultJSON, r.Error,
		r.TimeoutSeconds, r.CreatedAt, r.StartedAt, r.CompletedAt,
	)
	return err
}

// finalizeRun updates the terminal fields of a run (status, result,
// error, completed_at). Only these fields change after insert.
func (svc *Service) finalizeRun(ctx context.Context, r *Run) error {
	_, err := svc.db.ExecContext(ctx,
		`UPDATE subagent_runs SET status = ?, result_json = ?, error = ?, completed_at = ?
		 WHERE id = ?`,
		r.Status, r.ResultJSON, r.Error, r.CompletedAt, r.ID,
	)
	return err
}

// selectSQL is the canonical SELECT clause for subagent_runs rows.
const selectSQL = `SELECT id, parent_session_id, child_session_id, role, prompt,
	mode, status, inputs_json, result_json, error,
	timeout_seconds, created_at, started_at, completed_at
	FROM subagent_runs`

func scanRun(row interface{ Scan(...any) error }) (*Run, error) {
	var r Run
	if err := row.Scan(
		&r.ID, &r.ParentSessionID, &r.ChildSessionID, &r.Role, &r.Prompt,
		&r.Mode, &r.Status, &r.InputsJSON, &r.ResultJSON, &r.Error,
		&r.TimeoutSeconds, &r.CreatedAt, &r.StartedAt, &r.CompletedAt,
	); err != nil {
		return nil, err
	}
	return &r, nil
}

// EchoRunner is the stub runner used until the real chat-engine
// runner lands. Returns a synthetic Result containing a short
// summary of the prompt. Good enough to exercise the Spawn flow in
// tests + a manual dogfood smoke without a provider roundtrip.
type EchoRunner struct{}

// Run reflects the prompt back as a summary and wraps it in a
// trivial result JSON payload.
func (EchoRunner) Run(_ context.Context, run *Run) (*Result, error) {
	summary := fmt.Sprintf("echo-runner: role=%s prompt=%q", run.Role, truncate(run.Prompt, 120))
	payload, _ := json.Marshal(map[string]string{
		"echo_role":   run.Role,
		"echo_prompt": run.Prompt,
	})
	return &Result{Summary: summary, ResultJSON: string(payload)}, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
