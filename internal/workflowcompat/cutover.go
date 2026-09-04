package workflowcompat

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hollis-labs/nanite/internal/store"
)

var ErrExplicitLegacyDispositionRequired = errors.New("active legacy workflow runs require explicit disposition")

const defaultCutoverLeaseDuration = 5 * time.Minute

// CutoverRequest identifies one startup attempt. Owner and Token must be
// stable for retries by the same deployment but unique across deployments.
type CutoverRequest struct {
	Owner         string
	Token         string
	Now           time.Time
	LeaseDuration time.Duration
}

// CutoverReport is safe to print during deployment. PendingLegacy is the
// exact durable cohort that prevents shared-engine activation.
type CutoverReport struct {
	State         store.WorkflowCutoverState
	PendingLegacy []store.WorkflowLegacyRunDisposition
}

// LegacyDispositionRequest is an explicit operator decision. No default is
// provided because fabricating a plan or silently abandoning active legacy
// work would destroy recovery provenance.
type LegacyDispositionRequest struct {
	CutoverRequest
	RunID       string
	Disposition store.WorkflowLegacyDisposition
	Actor       string
	Reason      string
}

// LegacyDispositionDecision is one operator-authored row in a startup plan.
// Callers typically load these exact run IDs and reasons from deployment
// configuration, making the recovery path independent of a running HTTP API.
type LegacyDispositionDecision struct {
	RunID       string                          `json:"run_id"`
	Disposition store.WorkflowLegacyDisposition `json:"disposition"`
	Actor       string                          `json:"actor"`
	Reason      string                          `json:"reason"`
}

// CutoverCoordinator performs the fenced, restart-safe startup sequence over
// store primitives. It does not start either engine; composition must call
// PrepareSharedStartup before enabling shared launch/recovery workers.
type CutoverCoordinator struct {
	store *store.Store
}

func NewCutoverCoordinator(product *store.Store) (*CutoverCoordinator, error) {
	if product == nil || product.DB == nil {
		return nil, errors.New("workflow cutover coordinator requires product store")
	}
	return &CutoverCoordinator{store: product}, nil
}

// PrepareSharedStartup quiesces legacy launches and enables the shared engine
// only when every captured active legacy row has an explicit terminal audit.
// Returning ErrExplicitLegacyDispositionRequired is an expected deployment
// stop, not permission to auto-cancel the reported rows.
func (c *CutoverCoordinator) PrepareSharedStartup(ctx context.Context, request CutoverRequest) (CutoverReport, error) {
	request = normalizeCutoverRequest(request)
	if err := validateCutoverRequest(request); err != nil {
		return CutoverReport{}, err
	}
	state, err := c.store.EnsureWorkflowCutoverState(ctx, request.Now)
	if err != nil {
		return CutoverReport{}, err
	}
	if state.Phase == store.WorkflowCutoverSharedOnly {
		return CutoverReport{State: state}, nil
	}
	leased, err := c.store.AcquireWorkflowCutoverLease(ctx, store.AcquireWorkflowCutoverLeaseRequest{
		Owner: request.Owner, Token: request.Token, ExpectedGeneration: state.Generation,
		Now: request.Now, Duration: request.LeaseDuration,
	})
	if err != nil {
		return CutoverReport{}, err
	}
	current := leased
	release := func(at time.Time) error {
		_, releaseErr := c.store.ReleaseWorkflowCutoverLease(ctx, cutoverLeaseMutation(current, request, at))
		return releaseErr
	}
	if current.Phase == store.WorkflowCutoverLegacy {
		current, err = c.store.TransitionWorkflowCutover(ctx, store.TransitionWorkflowCutoverRequest{
			Lease: cutoverLeaseMutation(current, request, request.Now),
			To:    store.WorkflowCutoverQuiescing,
		})
		if err != nil {
			_ = release(request.Now)
			return CutoverReport{}, err
		}
	}
	transitionAt := request.Now.Add(time.Nanosecond)
	shared, transitionErr := c.store.TransitionWorkflowCutover(ctx, store.TransitionWorkflowCutoverRequest{
		Lease: cutoverLeaseMutation(current, request, transitionAt),
		To:    store.WorkflowCutoverSharedOnly,
	})
	if transitionErr != nil {
		dispositions, listErr := c.store.ListWorkflowLegacyRunDispositions(ctx, current.Generation)
		pending := pendingLegacyDispositions(dispositions)
		releaseErr := release(transitionAt)
		if listErr != nil {
			return CutoverReport{State: current}, errors.Join(transitionErr, listErr, releaseErr)
		}
		if errors.Is(transitionErr, store.ErrWorkflowCutoverLegacyRunsPending) {
			return CutoverReport{State: current, PendingLegacy: pending}, errors.Join(
				ErrExplicitLegacyDispositionRequired, transitionErr, releaseErr,
			)
		}
		return CutoverReport{State: current, PendingLegacy: pending}, errors.Join(transitionErr, releaseErr)
	}
	current = shared
	if err := release(transitionAt.Add(time.Nanosecond)); err != nil {
		return CutoverReport{State: current}, err
	}
	current.LeaseOwner = ""
	current.LeaseToken = ""
	current.LeaseExpiresAt = time.Time{}
	return CutoverReport{State: current}, nil
}

// PrepareSharedStartupWithDecisions is the CLI-independent operator path for
// a process that cannot publish HTTP handlers until cutover succeeds. It first
// captures the durable cohort, requires exactly one explicit decision for each
// still-pending run (and no unknown run), records every audit under fencing,
// then retries activation. An empty or partial plan never mutates a run.
func (c *CutoverCoordinator) PrepareSharedStartupWithDecisions(
	ctx context.Context,
	request CutoverRequest,
	decisions []LegacyDispositionDecision,
) (CutoverReport, error) {
	report, err := c.PrepareSharedStartup(ctx, request)
	if err == nil || !errors.Is(err, ErrExplicitLegacyDispositionRequired) {
		return report, err
	}
	byRun := make(map[string]LegacyDispositionDecision, len(decisions))
	for _, decision := range decisions {
		if strings.TrimSpace(decision.RunID) == "" || strings.TrimSpace(decision.Actor) == "" || strings.TrimSpace(decision.Reason) == "" {
			return report, errors.New("workflow legacy disposition plan requires run_id, actor, and reason for every decision")
		}
		switch decision.Disposition {
		case store.WorkflowLegacyDispositionDrained, store.WorkflowLegacyDispositionCanceled, store.WorkflowLegacyDispositionFailed:
		default:
			return report, fmt.Errorf("workflow legacy disposition plan has invalid disposition %q for run %q", decision.Disposition, decision.RunID)
		}
		if _, duplicate := byRun[decision.RunID]; duplicate {
			return report, fmt.Errorf("workflow legacy disposition plan repeats run %q", decision.RunID)
		}
		byRun[decision.RunID] = decision
	}
	for _, pending := range report.PendingLegacy {
		if pending.Disposition != store.WorkflowLegacyDispositionPending {
			continue
		}
		if _, ok := byRun[pending.RunID]; !ok {
			return report, fmt.Errorf("%w: no decision for run %q", ErrExplicitLegacyDispositionRequired, pending.RunID)
		}
	}
	if len(byRun) != len(report.PendingLegacy) {
		return report, fmt.Errorf("workflow legacy disposition plan does not exactly match the pending cohort")
	}
	for index, pending := range report.PendingLegacy {
		decision := byRun[pending.RunID]
		decisionRequest := request
		decisionRequest.Token = fmt.Sprintf("%s:decision:%d", request.Token, index)
		decisionRequest.Now = request.Now.Add(time.Duration(index+1) * time.Nanosecond)
		if _, dispositionErr := c.RecordLegacyDisposition(ctx, LegacyDispositionRequest{
			CutoverRequest: decisionRequest, RunID: decision.RunID,
			Disposition: decision.Disposition, Actor: decision.Actor, Reason: decision.Reason,
		}); dispositionErr != nil {
			return report, dispositionErr
		}
	}
	retry := request
	retry.Token += ":complete"
	retry.Now = request.Now.Add(time.Duration(len(decisions)+1) * time.Nanosecond)
	return c.PrepareSharedStartup(ctx, retry)
}

// RecordLegacyDisposition applies one explicit operator decision under a new
// fence. Call PrepareSharedStartup again afterward to complete activation.
func (c *CutoverCoordinator) RecordLegacyDisposition(ctx context.Context, request LegacyDispositionRequest) (store.WorkflowLegacyRunDisposition, error) {
	request.CutoverRequest = normalizeCutoverRequest(request.CutoverRequest)
	if err := validateCutoverRequest(request.CutoverRequest); err != nil {
		return store.WorkflowLegacyRunDisposition{}, err
	}
	if strings.TrimSpace(request.RunID) == "" || strings.TrimSpace(request.Actor) == "" || strings.TrimSpace(request.Reason) == "" {
		return store.WorkflowLegacyRunDisposition{}, errors.New("workflow legacy disposition requires run_id, actor, and reason")
	}
	state, err := c.store.LoadWorkflowCutoverState(ctx)
	if err != nil {
		return store.WorkflowLegacyRunDisposition{}, err
	}
	if state.Phase != store.WorkflowCutoverQuiescing {
		return store.WorkflowLegacyRunDisposition{}, fmt.Errorf("%w: disposition requires quiescing phase", store.ErrWorkflowCutoverInvalidTransition)
	}
	leased, err := c.store.AcquireWorkflowCutoverLease(ctx, store.AcquireWorkflowCutoverLeaseRequest{
		Owner: request.Owner, Token: request.Token, ExpectedGeneration: state.Generation,
		Now: request.Now, Duration: request.LeaseDuration,
	})
	if err != nil {
		return store.WorkflowLegacyRunDisposition{}, err
	}
	mutation := cutoverLeaseMutation(leased, request.CutoverRequest, request.Now)
	disposition, recordErr := c.store.RecordWorkflowLegacyRunDisposition(ctx, store.RecordWorkflowLegacyDispositionRequest{
		Lease: mutation, RunID: request.RunID, Disposition: request.Disposition,
		Actor: request.Actor, Reason: request.Reason,
	})
	_, releaseErr := c.store.ReleaseWorkflowCutoverLease(ctx, mutation)
	return disposition, errors.Join(recordErr, releaseErr)
}

func normalizeCutoverRequest(request CutoverRequest) CutoverRequest {
	if request.Now.IsZero() {
		request.Now = time.Now().UTC()
	}
	if request.LeaseDuration <= 0 {
		request.LeaseDuration = defaultCutoverLeaseDuration
	}
	return request
}

func validateCutoverRequest(request CutoverRequest) error {
	if strings.TrimSpace(request.Owner) == "" || strings.TrimSpace(request.Token) == "" {
		return errors.New("workflow cutover requires owner and token")
	}
	return nil
}

func cutoverLeaseMutation(state store.WorkflowCutoverState, request CutoverRequest, at time.Time) store.WorkflowCutoverLeaseMutation {
	return store.WorkflowCutoverLeaseMutation{
		Owner: request.Owner, Token: request.Token, ExpectedGeneration: state.Generation,
		Fence: state.Fence, Now: at,
	}
}

func pendingLegacyDispositions(dispositions []store.WorkflowLegacyRunDisposition) []store.WorkflowLegacyRunDisposition {
	pending := make([]store.WorkflowLegacyRunDisposition, 0, len(dispositions))
	for _, disposition := range dispositions {
		if disposition.Disposition == store.WorkflowLegacyDispositionPending {
			pending = append(pending, disposition)
		}
	}
	return pending
}
