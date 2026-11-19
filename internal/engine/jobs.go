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

// subtaskResult is what the parent model receives from a foreground subtask.
type subtaskResult struct {
	Status  subagent.Status `json:"status"`
	Result  string          `json:"result"`
	Reason  string          `json:"reason,omitempty"`
	ChildID string          `json:"child_id"`
	Variant string          `json:"variant"`
	Steps   int             `json:"steps"`
	Usage   struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

func (x *execution) subtask(ctx context.Context, request subagent.Request) (tools.Result, error) {
	if !x.canSpawn() {
		return tools.Result{}, fmt.Errorf("subtask depth limit reached: only a variant with can_spawn may start subtasks, and only one level down")
	}
	if err := request.Validate(x.variants); err != nil {
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
	e := x.engine
	release, err := e.children.Acquire()
	if err != nil {
		return tools.Result{}, err
	}
	now := time.Now().UTC()
	child := store.Conversation{ID: store.NewID(now), ParentID: x.info.ID, Role: variant.Name, Title: subtaskTitle(request.Goal), Status: "active", CreatedAt: now, UpdatedAt: now}
	if err := e.cfg.Store.CreateConversation(ctx, child); err != nil {
		release()
		return tools.Result{}, err
	}
	start := func(runCtx context.Context) (*Run, error) { return e.Send(runCtx, child.ID, Input{Text: message}) }
	if request.Background {
		spec, _ := json.Marshal(struct {
			ChildID, Variant, Goal string
			ReturnSpec, Context    json.RawMessage `json:",omitempty"`
		}{child.ID, variant.Name, request.Goal, request.ReturnSpec, request.Context})
		job, err := e.jobs.Start(ctx, store.Job{ID: child.ID, ConversationID: x.info.ID, Kind: subtaskJobKind, Spec: string(spec)},
			func(jobCtx context.Context, _ io.Writer) (background.Task, error) {
				runCtx, stopWall := wallContext(jobCtx, variant)
				run, err := start(runCtx)
				if err != nil {
					stopWall()
					return background.Task{}, err
				}
				return background.Task{Wait: func() background.Outcome {
					defer release()
					defer stopWall()
					result, err := run.Wait()
					status, reason := subtaskStatus(jobCtx, runCtx, result, err)
					e.reportUsage(context.WithoutCancel(jobCtx), x.info.ID, child.ID, result.Usage)
					outcome := background.Outcome{Output: result.Text,
						Summary: fmt.Sprintf("subtask %s (%s) ended %s%s; usage %d input, %d output, %d cache-read, %d cache-write tokens.",
							child.ID, variant.Name, status, parenthesize(reason), result.Usage.InputTokens, result.Usage.OutputTokens, result.Usage.CacheReadTokens, result.Usage.CacheWriteTokens)}
					switch status {
					case subagent.StatusLimit:
						outcome.Limit = reason
					case subagent.StatusPartial:
						outcome.Reason = reason
					case subagent.StatusError, subagent.StatusCancelled:
						outcome.Err = err
					case subagent.StatusOK:
					}
					return outcome
				}}, nil
			})
		if err != nil {
			// The release is idempotent if the job already joined the child.
			release()
			return tools.Result{}, err
		}
		x.emitSubtaskStarted(ctx, child, request)
		text, err := jobJSON(job, "")
		return tools.Result{Text: text}, err
	}
	defer release()
	runCtx, stopWall := wallContext(ctx, variant)
	defer stopWall()
	run, err := start(runCtx)
	if err != nil {
		return tools.Result{}, err
	}
	x.emitSubtaskStarted(ctx, child, request)
	result, err := run.Wait()
	status, reason := subtaskStatus(ctx, runCtx, result, err)
	x.rollUp(ctx, child.ID, result.Usage)
	x.run.emit(ctx, func(meta events.EventMeta) events.Event {
		return events.SubagentFinished{EventMeta: meta, ID: child.ID, Variant: variant.Name, Status: string(status), Result: result, Err: err, Limit: events.LimitReason(result.StopReason)}
	})
	out := subtaskResult{Status: status, Result: result.Text, Reason: reason, ChildID: child.ID, Variant: variant.Name, Steps: result.Steps}
	out.Usage.InputTokens, out.Usage.OutputTokens = result.Usage.InputTokens, result.Usage.OutputTokens
	raw, err := json.Marshal(out)
	return tools.Result{Text: string(raw), IsError: status.Failed()}, err
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
	return "Subtask: " + goal
}

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
		const tailSize = 8 << 10
		stat, err := file.Stat()
		if err != nil {
			return "", err
		}
		if _, err := file.Seek(max(0, stat.Size()-tailSize), io.SeekStart); err != nil {
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
		// A subtask job speaks the subtask vocabulary.
		raw, err := json.Marshal(struct {
			ID     string `json:"id"`
			Kind   string `json:"kind"`
			Status string `json:"status"`
			Reason string `json:"reason,omitempty"`
			Output string `json:"output,omitempty"`
		}{job.ID, "subtask", subagent.StatusOf(job.Status, job.Reason), job.Reason, output})
		return string(raw), err
	}
	raw, err := json.Marshal(struct {
		store.Job
		Output string `json:"output,omitempty"`
	}{job, output})
	return string(raw), err
}
