package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/hollis-labs/libs/llm/core/llmtypes"
	"github.com/hollis-labs/nanite/internal/background"
	"github.com/hollis-labs/nanite/internal/events"
	"github.com/hollis-labs/nanite/internal/selftools"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/subagent"
	"github.com/hollis-labs/nanite/internal/tools"
)

func (x *execution) jobTools() []tools.Tool {
	handlers := selftools.JobTools{Status: x.jobStatus, Cancel: x.cancelJob}
	if x.depth > 0 {
		handlers.Return = x.returnResult
	}
	if x.canSpawn() {
		handlers.Subtask, handlers.Variants = x.subtask, x.variants
		// Only a root chat runs background subtasks, and only where it can poll them.
		handlers.SubtaskBackground = x.depth == 0 && x.engine.cfg.Builtins.Offers("background_status")
	}
	if len(x.engine.cfg.WorkspaceRoots) > 0 {
		handlers.StartShell = x.startShell
	}
	return handlers.Tools()
}

func (x *execution) startShell(ctx context.Context, raw json.RawMessage) (string, error) {
	input, err := selftools.ParseBash(raw)
	if err != nil {
		return "", err
	}
	e := x.engine
	command := input.ShellCommand(e.cfg.WorkspaceRoots[0], x.definition.Runtime.Env)
	job, err := e.jobs.Start(ctx, store.Job{ID: store.NewID(time.Now()), ConversationID: x.info.ID, Kind: "shell", Spec: e.scrub.Text(string(raw))},
		func(runCtx context.Context, output io.Writer) (background.Task, error) {
			return background.Shell(runCtx, command, output)
		})
	if err != nil {
		return "", err
	}
	return jobJSON(job, "")
}

// subtaskJobKind is the jobs-table kind of a background subtask.
const subtaskJobKind = "subagent"

func (x *execution) subtask(ctx context.Context, request subagent.Request) (tools.Result, error) {
	if !x.canSpawn() {
		return tools.Result{}, fmt.Errorf("subtask depth limit reached: only a variant with can_spawn may start subtasks, and only one level down")
	}
	if err := request.Validate(x.variants); err != nil {
		return tools.Result{}, err
	}
	schema, err := request.Schema()
	if err != nil {
		return tools.Result{}, err
	}
	if request.Background && x.depth > 0 {
		// A background grandchild would outlive the lead that is waiting on it.
		return tools.Result{}, fmt.Errorf("a subtask cannot start a background subtask; run it in the foreground")
	}
	variant := x.variants[request.Variant]
	spec, err := request.ContextSpec()
	if err != nil {
		return tools.Result{}, err
	}
	message := request.Message()
	seed, err := x.forkedContext(ctx, spec, variant)
	if err != nil {
		return tools.Result{}, fmt.Errorf("subtask context: %w", err)
	}
	if seed != "" {
		message += "\n\n" + seed
	}
	if !request.Background && x.shared.spent() {
		return tools.Result{}, errBudgetSpent
	}
	e := x.engine
	// A lead (a can_spawn variant one level down) takes a lead slot, every
	// other subtask a child slot, so a lead never holds a slot its own
	// children wait for. A foreground subtask waits for a slot while its
	// turn lasts; a background one must not hold up the turn, so it is
	// refused at once.
	limit := e.children
	if x.depth == 0 && variant.CanSpawn {
		limit = e.leads
	}
	var release func()
	if request.Background {
		release, err = limit.TryAcquire()
	} else {
		release, err = limit.Acquire(ctx)
	}
	if err != nil {
		return tools.Result{}, err
	}
	if !request.Background && x.shared.spent() {
		// The budget ran out while this subtask waited for a slot.
		release()
		return tools.Result{}, errBudgetSpent
	}
	now := time.Now().UTC()
	// The goal is scrubbed before it is stored or reported anywhere.
	child := store.Conversation{ID: store.NewID(now), ParentID: x.info.ID, Role: variant.Name, Title: subtaskTitle(e.scrub.Text(request.Goal)), Status: "active", CreatedAt: now, UpdatedAt: now}
	if err := e.cfg.Store.CreateConversation(ctx, child); err != nil {
		release()
		return tools.Result{}, err
	}
	e.openSubtask(child.ID, schema)
	// A relayed ask names the root's subtask call that started this chain.
	rootCall := callIDFrom(ctx)
	if x.origin != nil {
		rootCall = x.origin.CallID
	}
	e.mu.Lock()
	e.subtaskCalls[child.ID] = rootCall
	e.mu.Unlock()
	// Once the child ends, its call mapping and its own grants go too.
	forget := func() {
		e.mu.Lock()
		delete(e.subtaskCalls, child.ID)
		delete(e.grants, child.ID)
		e.mu.Unlock()
	}
	start := func(runCtx context.Context) (*Run, error) { return e.Send(runCtx, child.ID, Input{Text: message}) }
	if request.Background {
		spec, _ := json.Marshal(struct {
			ChildID, Variant, Goal string
			ReturnSpec, Context    json.RawMessage `json:",omitempty"`
		}{child.ID, variant.Name, request.Goal, request.ReturnSpec, request.Context})
		job, err := e.jobs.Start(ctx, store.Job{ID: child.ID, ConversationID: x.info.ID, Kind: subtaskJobKind, Spec: string(spec)},
			func(jobCtx context.Context, _ io.Writer) (background.Task, error) {
				// A background subtask outlives the turn, so it has only its
				// variant's budget, never the turn's shared one.
				runCtx, stopWall := wallContext(withBudget(jobCtx, nil), variant)
				run, err := start(runCtx)
				if err != nil {
					stopWall()
					return background.Task{}, err
				}
				return background.Task{Wait: func() background.Outcome {
					defer release()
					defer forget()
					defer stopWall()
					result, err := run.Wait()
					status, reason := subtaskStatus(jobCtx, runCtx, result, err)
					returned := e.closeSubtask(child.ID)
					bg := context.WithoutCancel(jobCtx)
					e.reportUsage(bg, x.info.ID, child.ID, result.Usage)
					out := e.composeSubtaskResult(bg, x.info.ID, child.ID, variant, result, status, reason, returned)
					// A person's refusal overrides an ok or partial ending,
					// whatever the child returned.
					out.Status, out.Reason = withDenials(out.Status, out.Reason, e.takeDenials(child.ID))
					outcome, marshalErr := outcomeFor(out, err)
					if marshalErr != nil {
						outcome = background.Outcome{Err: marshalErr}
					}
					outcome.Summary = fmt.Sprintf("subtask %s (%s) ended %s%s; usage %d input, %d output, %d cache-read, %d cache-write tokens.",
						child.ID, variant.Name, out.Status, parenthesize(out.Reason), result.Usage.InputTokens, result.Usage.OutputTokens, result.Usage.CacheReadTokens, result.Usage.CacheWriteTokens)
					return outcome
				}}, nil
			})
		if err != nil {
			// The release is idempotent if the job already joined the child.
			release()
			e.closeSubtask(child.ID)
			forget()
			return tools.Result{}, err
		}
		x.emitSubtaskStarted(ctx, child, request)
		text, err := jobJSON(job, "")
		return tools.Result{Text: text}, err
	}
	defer release()
	defer e.closeSubtask(child.ID)
	defer forget()
	// A foreground child spends from the turn's shared budget, and is
	// stopped mid-step when the budget runs out.
	budgetCtx, endBudget := x.shared.guard(withBudget(ctx, x.shared))
	defer endBudget()
	runCtx, stopWall := wallContext(budgetCtx, variant)
	defer stopWall()
	run, err := start(runCtx)
	if err != nil {
		return tools.Result{}, err
	}
	x.emitSubtaskStarted(ctx, child, request)
	result, err := run.Wait()
	status, reason := subtaskStatus(ctx, runCtx, result, err)
	x.rollUp(ctx, child.ID, result.Usage)
	out := e.composeSubtaskResult(ctx, x.info.ID, child.ID, variant, result, status, reason, e.closeSubtask(child.ID))
	// A person's refusal overrides an ok or partial ending, whatever the
	// child returned.
	out.Status, out.Reason = withDenials(out.Status, out.Reason, e.takeDenials(child.ID))
	x.run.emit(ctx, func(meta events.EventMeta) events.Event {
		return events.SubagentFinished{EventMeta: meta, ID: child.ID, Variant: variant.Name, Status: string(out.Status), Result: result, Err: err, Limit: events.LimitReason(result.StopReason)}
	})
	raw, err := json.Marshal(out)
	return tools.Result{Text: string(raw), IsError: out.Status.Failed()}, err
}

// wallContext bounds a child by its variant's wall-clock budget, so the
// budget holds even in the middle of a step.
func wallContext(ctx context.Context, variant subagent.Variant) (context.Context, context.CancelFunc) {
	if variant.Budgets.Wall <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, variant.Budgets.Wall)
}

// subtaskStatus classifies how a child turn ended, with a reason when it did
// not end ok. parent is the caller's context; run is the child's, which
// carries the wall-clock budget.
func subtaskStatus(parent, run context.Context, result events.Result, err error) (subagent.Status, string) {
	switch {
	case err != nil && errors.Is(context.Cause(run), errTokenBudget):
		// The turn's shared budget ran out while this child was running.
		return subagent.StatusLimit, events.StopTokenBudget
	case err != nil && parent.Err() == nil && errors.Is(run.Err(), context.DeadlineExceeded):
		return subagent.StatusLimit, events.StopWallBudget
	case err != nil && (errors.Is(err, context.Canceled) || parent.Err() != nil):
		return subagent.StatusCancelled, "cancelled"
	case err != nil:
		return subagent.StatusError, err.Error()
	case events.LimitReason(result.StopReason) != "":
		return subagent.StatusLimit, result.StopReason
	case result.StopReason == "" || result.StopReason == llmtypes.StopReasonEndTurn:
		return subagent.StatusOK, ""
	default:
		return subagent.StatusPartial, result.StopReason
	}
}

// withDenials turns an ok or partial ending into denied when a person
// refused an approval the subtask relayed; limits, errors and cancellation
// keep their own status. A refusal no person gave (a default gate, a gate
// failure, an expiry) is only the tool's own error result.
func withDenials(status subagent.Status, reason string, denied []denial) (subagent.Status, string) {
	if len(denied) == 0 || (status != subagent.StatusOK && status != subagent.StatusPartial) {
		return status, reason
	}
	return subagent.StatusDenied, denialReason(denied)
}

// subtaskCall is the root's subtask call that started a child's chain.
func (e *Engine) subtaskCall(child string) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.subtaskCalls[child]
}

func parenthesize(reason string) string {
	if reason == "" {
		return ""
	}
	return " (" + reason + ")"
}

// reportUsage reports a finished background subtask's usage as a Usage event
// marked Background on the parent conversation's running turn, if it has
// one. Background usage is never part of a turn's totals; the completion
// notice carries it too.
func (e *Engine) reportUsage(ctx context.Context, parent, child string, u events.Usage) {
	e.mu.Lock()
	r := e.active[parent]
	e.mu.Unlock()
	if r == nil {
		return
	}
	// A run that finishes meanwhile drops the event (Run.emit after terminal).
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	r.emit(ctx, func(meta events.EventMeta) events.Event {
		return events.Usage{EventMeta: meta, InputTokens: u.InputTokens, OutputTokens: u.OutputTokens, CacheReadTokens: u.CacheReadTokens, CacheWriteTokens: u.CacheWriteTokens, Subtask: child, Background: true}
	})
}

// rollUp adds a finished child's usage to this turn's usage and reports it.
func (x *execution) rollUp(ctx context.Context, child string, u events.Usage) {
	if u.InputTokens+u.OutputTokens+u.CacheReadTokens+u.CacheWriteTokens == 0 {
		return
	}
	x.usageMu.Lock()
	x.result.Usage.InputTokens += u.InputTokens
	x.result.Usage.OutputTokens += u.OutputTokens
	x.result.Usage.CacheReadTokens += u.CacheReadTokens
	x.result.Usage.CacheWriteTokens += u.CacheWriteTokens
	x.usageMu.Unlock()
	x.run.emit(ctx, func(meta events.EventMeta) events.Event {
		return events.Usage{EventMeta: meta, InputTokens: u.InputTokens, OutputTokens: u.OutputTokens, CacheReadTokens: u.CacheReadTokens, CacheWriteTokens: u.CacheWriteTokens, Subtask: child}
	})
}

func subtaskTitle(goal string) string {
	goal = strings.Join(strings.Fields(goal), " ")
	if r := []rune(goal); len(r) > 60 {
		goal = string(r[:59]) + "…"
	}
	return subtaskTitlePrefix + goal
}

// subtaskTitlePrefix starts a child conversation's title, before its goal.
const subtaskTitlePrefix = "Subtask: "

func (x *execution) emitSubtaskStarted(ctx context.Context, child store.Conversation, request subagent.Request) {
	goal := x.engine.scrub.Text(request.Goal)
	x.run.emit(ctx, func(meta events.EventMeta) events.Event {
		return events.SubagentStarted{EventMeta: meta, ID: child.ID, Variant: child.Role, Goal: goal, Background: request.Background}
	})
}

// job finds a background job by id, whether a shell command or a subtask.
func (x *execution) job(ctx context.Context, id string) (store.Job, error) {
	job, err := x.engine.cfg.Store.Job(ctx, x.info.ID, "shell", id)
	if errors.Is(err, sql.ErrNoRows) {
		job, err = x.engine.cfg.Store.Job(ctx, x.info.ID, subtaskJobKind, id)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return store.Job{}, fmt.Errorf("no background job %q in this conversation", id)
	}
	return job, err
}

func (x *execution) jobStatus(ctx context.Context, id string) (tools.Result, error) {
	job, err := x.job(ctx, id)
	if err != nil {
		return tools.Result{}, err
	}
	text, err := x.jobOutput(job)
	// A subtask that did not end ok is an error the model must notice; a
	// shell job's exit status is already in the body.
	status := subagent.Status(subagent.StatusOf(job.Status, job.Reason))
	failed := job.Kind == subtaskJobKind && job.Status != "running" && status.Failed()
	return tools.Result{Text: text, IsError: failed}, err
}

func (x *execution) jobOutput(job store.Job) (string, error) {
	output := ""
	if job.OutputPath != "" {
		file, err := os.Open(job.OutputPath)
		if err != nil {
			return "", err
		}
		defer func() { _ = file.Close() }() // Read-only status log.
		tailSize := int64(8 << 10)
		stat, err := file.Stat()
		if err != nil {
			return "", err
		}
		if job.Kind == subtaskJobKind {
			// A subtask's output is its result JSON, already held under the
			// tool result budget; read it from the start.
			tailSize = subtaskEnvelopeLimit
		} else if _, err := file.Seek(max(0, stat.Size()-tailSize), io.SeekStart); err != nil {
			return "", err
		}
		data, err := io.ReadAll(io.LimitReader(file, tailSize))
		if err != nil {
			return "", err
		}
		output = string(data)
	}
	return jobJSON(job, output)
}

func (x *execution) cancelJob(ctx context.Context, id string) (string, error) {
	job, err := x.job(ctx, id)
	if err != nil {
		return "", err
	}
	if x.engine.jobs.Cancel(job.ID) {
		return "Cancellation requested.", nil
	}
	return "Job is " + job.Status + ".", nil
}

func jobJSON(job store.Job, output string) (string, error) {
	if job.Kind == subtaskJobKind {
		// A subtask job speaks the subtask vocabulary. Its output is the
		// result JSON, held under the tool result budget; embedding it as JSON
		// rather than an escaped string keeps it there.
		view := struct {
			ID     string          `json:"id"`
			Kind   string          `json:"kind"`
			Status string          `json:"status"`
			Reason string          `json:"reason,omitempty"`
			Result json.RawMessage `json:"result,omitempty"`
			Output string          `json:"output,omitempty"`
		}{ID: job.ID, Kind: "subtask", Status: subagent.StatusOf(job.Status, job.Reason), Reason: job.Reason}
		if trimmed := strings.TrimSpace(output); json.Valid([]byte(trimmed)) {
			view.Result = json.RawMessage(trimmed)
		} else {
			view.Output = output
		}
		raw, err := json.Marshal(view)
		return string(raw), err
	}
	raw, err := json.Marshal(struct {
		store.Job
		Output string `json:"output,omitempty"`
	}{job, output})
	return string(raw), err
}
