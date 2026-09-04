package workflowhost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	workflowruntime "github.com/hollis-labs/go-workflow/runtime"
	workflowwait "github.com/hollis-labs/go-workflow/wait"

	"github.com/hollis-labs/nanite/internal/agentworkflow"
	"github.com/hollis-labs/nanite/internal/store"
)

var ErrWorkflowWaitUnauthorized = errors.New("workflow wait responder is not authorized")

type workflowActivationScheduleStore interface {
	ScheduleWorkflowActivation(context.Context, store.WorkflowActivationSchedule) error
	CancelWorkflowActivation(context.Context, string, time.Time) error
}

// ActivationScheduler projects go-workflow's application-neutral activations into
// the same go-scheduler v0.2 one-shot Schedule/Fire engine used by the rest of
// Nanite. It persists no timers or goroutines of its own.
type ActivationScheduler struct {
	Store workflowActivationScheduleStore
	Now   func() time.Time
}

var _ workflowwait.ActivationScheduler = (*ActivationScheduler)(nil)

func NewActivationScheduler(value *store.Store) *ActivationScheduler {
	return &ActivationScheduler{Store: value, Now: time.Now}
}

func (s *ActivationScheduler) Schedule(ctx context.Context, activation workflowwait.Activation) error {
	if ctx == nil || s == nil || s.Store == nil {
		return fmt.Errorf("workflow activation scheduler requires context and store")
	}
	if err := activation.Validate(); err != nil {
		return fmt.Errorf("schedule workflow activation: %w", err)
	}
	activation.FireAt = activation.FireAt.UTC()
	payload, err := json.Marshal(activation)
	if err != nil {
		return fmt.Errorf("schedule workflow activation: encode payload: %w", err)
	}
	now := time.Now().UTC()
	if s.Now != nil {
		now = s.Now().UTC()
	}
	return s.Store.ScheduleWorkflowActivation(context.WithoutCancel(ctx), store.WorkflowActivationSchedule{
		ScheduleID:     store.WorkflowActivationScheduleID(string(activation.ID)),
		ActivationID:   string(activation.ID),
		ActivationJSON: string(payload),
		FireAt:         activation.FireAt.Format(time.RFC3339Nano),
		NextRun:        activation.FireAt.Format(time.RFC3339Nano),
		CreatedAt:      now.Format(time.RFC3339Nano),
	})
}

func (s *ActivationScheduler) Cancel(ctx context.Context, id workflowwait.ActivationID) error {
	if ctx == nil || s == nil || s.Store == nil {
		return fmt.Errorf("workflow activation scheduler requires context and store")
	}
	if strings.TrimSpace(string(id)) == "" {
		return fmt.Errorf("cancel workflow activation: activation id is required")
	}
	now := time.Now().UTC()
	if s.Now != nil {
		now = s.Now().UTC()
	}
	return s.Store.CancelWorkflowActivation(context.WithoutCancel(ctx), string(id), now)
}

type workflowWaitMaterializationStore interface {
	MaterializeWorkflowWait(context.Context, store.WorkflowWaitMaterialization) error
	ResolveWorkflowWait(context.Context, store.WorkflowWaitMaterialization) error
}

// WaitMaterializer exposes durable waits through Nanite-owned persistence.
// Raw resume tokens never enter this adapter or its table.
type WaitMaterializer struct {
	Store workflowWaitMaterializationStore
	Now   func() time.Time
}

var _ workflowwait.Materializer = (*WaitMaterializer)(nil)

func NewWaitMaterializer(value *store.Store) *WaitMaterializer {
	return &WaitMaterializer{Store: value, Now: time.Now}
}

func (m *WaitMaterializer) Materialize(ctx context.Context, material workflowwait.Materialization) error {
	return m.write(ctx, material, false)
}

func (m *WaitMaterializer) Resolve(ctx context.Context, material workflowwait.Materialization) error {
	return m.write(ctx, material, true)
}

func (m *WaitMaterializer) write(ctx context.Context, material workflowwait.Materialization, resolved bool) error {
	if ctx == nil || m == nil || m.Store == nil {
		return fmt.Errorf("workflow wait materializer requires context and store")
	}
	if strings.TrimSpace(material.WaitID) == "" || !material.Kind.Valid() {
		return fmt.Errorf("workflow wait materialization requires wait id and valid kind")
	}
	now := time.Now().UTC()
	if m.Now != nil {
		now = m.Now().UTC()
	}
	row := store.WorkflowWaitMaterialization{
		WaitID: material.WaitID, Kind: string(material.Kind), ResumeURL: material.ResumeURL,
		CreatedAt: now.Format(time.RFC3339Nano),
	}
	if !material.ExpiresAt.IsZero() {
		row.ExpiresAt = material.ExpiresAt.UTC().Format(time.RFC3339Nano)
	}
	if resolved {
		return m.Store.ResolveWorkflowWait(context.WithoutCancel(ctx), row)
	}
	return m.Store.MaterializeWorkflowWait(context.WithoutCancel(ctx), row)
}

// NaniteResponderAuthorizer applies Nanite's explicit authority reference and
// responder provenance policy. The desktop host has no anonymous implicit
// principal: non-timer responses must name the exact authority reference
// pinned into the wait record, and any authority attributes are required to
// match the responder's asserted attributes.
type NaniteResponderAuthorizer struct{}

var _ workflowwait.ResponderAuthorizer = NaniteResponderAuthorizer{}

func (NaniteResponderAuthorizer) AuthorizeResume(ctx context.Context, request workflowwait.AuthorizationRequest) error {
	if ctx == nil {
		return fmt.Errorf("%w: context is required", ErrWorkflowWaitUnauthorized)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if request.Source != request.Record.WakeSource {
		return fmt.Errorf("%w: wake source does not match", ErrWorkflowWaitUnauthorized)
	}
	if request.Record.Kind == workflowwait.KindTimer && request.Source == workflowwait.WakeTimer {
		if request.Record.Authority.Kind != "system_timer" || request.Record.Authority.Reference != "runtime" ||
			request.Responder.Kind != "system" || request.Responder.Reference != "wait-timer" {
			return fmt.Errorf("%w: timer wake requires the runtime system responder", ErrWorkflowWaitUnauthorized)
		}
		return nil
	}
	if request.Record.Authority.Kind != "nanite" {
		return fmt.Errorf("%w: unsupported authority kind %q", ErrWorkflowWaitUnauthorized, request.Record.Authority.Kind)
	}
	if strings.TrimSpace(request.Record.Authority.Reference) == "" || request.Responder.Reference != request.Record.Authority.Reference {
		return fmt.Errorf("%w: responder reference does not match", ErrWorkflowWaitUnauthorized)
	}
	if request.Responder.Kind == "system" {
		return fmt.Errorf("%w: system responder is reserved for timers", ErrWorkflowWaitUnauthorized)
	}
	for key, expected := range request.Record.Authority.Attributes {
		if key == "responder_kind" {
			if request.Responder.Kind != expected {
				return fmt.Errorf("%w: responder kind does not match", ErrWorkflowWaitUnauthorized)
			}
			continue
		}
		if request.Responder.Attributes[key] != expected {
			return fmt.Errorf("%w: responder attribute %q does not match", ErrWorkflowWaitUnauthorized, key)
		}
	}
	return nil
}

type workflowActivationState interface {
	LoadWait(context.Context, workflowruntime.WaitID) (workflowruntime.WaitSnapshot, error)
	LoadRetryActivation(context.Context, string) (workflowruntime.RetryActivationSnapshot, error)
	LoadNodeInvocation(context.Context, workflowruntime.NodeInvocationID) (workflowruntime.NodeInvocationSnapshot, error)
	ActivateNodeRetry(context.Context, workflowruntime.ActivateNodeRetryRequest) (workflowruntime.ActivateNodeRetryResult, error)
}

type workflowActivationEngine interface {
	Resume(context.Context, string, agentworkflow.StepExecutor) (agentworkflow.WorkflowResult, error)
}

// ActivationDispatcher is the application handler behind the scheduler's
// workflow_activation job type. go-workflow's durable wait/retry state is always
// consulted before resuming the host, so scheduler redelivery is idempotent by
// Activation.ID and a completed activation can still recover ready work after
// a crash before the go-scheduler Fire transition.
type ActivationDispatcher struct {
	State    workflowActivationState
	Waits    *workflowruntime.WaitCoordinator
	Engine   workflowActivationEngine
	Executor agentworkflow.StepExecutor
}

func (d *ActivationDispatcher) DispatchWorkflowActivation(
	ctx context.Context,
	activation workflowwait.Activation,
	firedAt time.Time,
) (bool, error) {
	if ctx == nil || d == nil || d.State == nil || d.Waits == nil || d.Engine == nil || d.Executor == nil {
		return false, fmt.Errorf("workflow activation dispatcher is not fully configured")
	}
	if err := activation.Validate(); err != nil {
		return false, err
	}
	firedAt = firedAt.UTC()
	if firedAt.IsZero() || firedAt.Before(activation.FireAt.UTC()) {
		return false, fmt.Errorf("workflow activation %q fired before its durable fire_at", activation.ID)
	}

	switch activation.Kind {
	case workflowruntime.ActivationWaitWake:
		if activation.WaitID == "" {
			return false, fmt.Errorf("workflow timer activation %q has no wait id", activation.ID)
		}
		wait, err := d.State.LoadWait(ctx, workflowruntime.WaitID(activation.WaitID))
		if err != nil {
			return false, err
		}
		switch wait.Status {
		case workflowruntime.WaitOpen:
			if _, err := d.Waits.WakeTimer(ctx, workflowruntime.TimerWakeCommand{WaitID: wait.Ref.ID, FiredAt: firedAt}); err != nil {
				return false, err
			}
		case workflowruntime.WaitCanceled, workflowruntime.WaitTimedOut:
			return false, nil
		case workflowruntime.WaitResumed:
		default:
			return false, fmt.Errorf("workflow timer wait %q has unsupported status %q", wait.Ref.ID, wait.Status)
		}
		return d.resume(ctx, activation.RunID)

	case "wait_timeout":
		if activation.WaitID == "" {
			return false, fmt.Errorf("workflow timeout activation %q has no wait id", activation.ID)
		}
		wait, err := d.State.LoadWait(ctx, workflowruntime.WaitID(activation.WaitID))
		if err != nil {
			return false, err
		}
		if wait.Status == workflowruntime.WaitCanceled || wait.Status == workflowruntime.WaitResumed {
			return false, nil
		}
		if wait.Status == workflowruntime.WaitOpen {
			if _, err := d.Waits.RecoverWaits(ctx, workflowruntime.OpenWaitQuery{
				RunID: workflowruntime.RunID(activation.RunID),
			}, firedAt); err != nil {
				return false, err
			}
		}
		return d.resume(ctx, activation.RunID)

	case "node_retry":
		retry, err := d.State.LoadRetryActivation(ctx, string(activation.ID))
		if err != nil {
			return false, err
		}
		switch retry.Status {
		case workflowruntime.RetryScheduled:
			node, err := d.State.LoadNodeInvocation(ctx, retry.Attempt.Invocation)
			if err != nil {
				return false, err
			}
			_, err = d.State.ActivateNodeRetry(context.WithoutCancel(ctx), workflowruntime.ActivateNodeRetryRequest{
				ActivationID: retry.ID, ExpectedActivationGeneration: retry.Generation,
				ExpectedNodeGeneration: node.Generation,
				IdempotencyKey:         "nanite:workflow-activation:" + activation.DedupKey,
				Now:                    firedAt,
			})
			if err != nil {
				return false, err
			}
		case workflowruntime.RetryCanceled:
			return false, nil
		case workflowruntime.RetryActivated:
		default:
			return false, fmt.Errorf("workflow retry activation %q has unsupported status %q", retry.ID, retry.Status)
		}
		return d.resume(ctx, activation.RunID)
	default:
		return false, fmt.Errorf("unsupported workflow activation kind %q", activation.Kind)
	}
}

func (d *ActivationDispatcher) resume(ctx context.Context, runID string) (bool, error) {
	if strings.TrimSpace(runID) == "" {
		return false, fmt.Errorf("workflow activation has no run id")
	}
	if _, err := d.Engine.Resume(context.WithoutCancel(ctx), runID, d.Executor); err != nil {
		return false, err
	}
	return true, nil
}
