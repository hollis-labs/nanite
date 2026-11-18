package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/hollis-labs/nanite/internal/background"
	"github.com/hollis-labs/nanite/internal/events"
	"github.com/hollis-labs/nanite/internal/selftools"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/subagent"
	"github.com/hollis-labs/nanite/internal/tools"
)

func (x *execution) jobTools() []tools.Tool {
	handlers := selftools.JobTools{Spawn: x.spawn, Status: x.jobStatus, Cancel: x.cancelJob}
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

func (x *execution) spawn(ctx context.Context, request subagent.Start) (string, error) {
	if x.info.ParentID != "" {
		return "", fmt.Errorf("subagent depth limit reached")
	}
	if err := request.Validate(); err != nil {
		return "", err
	}
	e := x.engine
	release, err := e.children.Acquire()
	if err != nil {
		return "", err
	}
	now := time.Now().UTC()
	child := store.Conversation{ID: store.NewID(now), ParentID: x.info.ID, Role: request.Role, Title: "Subagent", Status: "active", CreatedAt: now, UpdatedAt: now}
	if err := e.cfg.Store.CreateConversation(ctx, child); err != nil {
		release()
		return "", err
	}
	start := func(runCtx context.Context) (*Run, error) { return e.Send(runCtx, child.ID, Input{Text: request.Task}) }
	if request.Background {
		spec, _ := json.Marshal(struct{ ChildID, Role string }{child.ID, child.Role})
		job, err := e.jobs.Start(ctx, store.Job{ID: child.ID, ConversationID: x.info.ID, Kind: "subagent", Spec: string(spec)},
			func(runCtx context.Context, _ io.Writer) (background.Task, error) {
				run, err := start(runCtx)
				if err != nil {
					return background.Task{}, err
				}
				return background.Task{Wait: func() background.Outcome {
					defer release()
					result, err := run.Wait()
					return background.Outcome{Output: result.Text, Err: err}
				}}, nil
			})
		if err != nil {
			// The release is idempotent if the job already joined the child.
			release()
			return "", err
		}
		x.emitSubagentStarted(ctx, child)
		return jobJSON(job, "")
	}
	defer release()
	run, err := start(ctx)
	if err != nil {
		return "", err
	}
	x.emitSubagentStarted(ctx, child)
	result, err := run.Wait()
	x.run.emit(ctx, func(meta events.EventMeta) events.Event {
		return events.SubagentFinished{EventMeta: meta, ID: child.ID, Result: result, Err: err}
	})
	if err != nil {
		return "", err
	}
	return jobJSON(store.Job{ID: child.ID, ConversationID: x.info.ID, Kind: "subagent", Status: "succeeded"}, result.Text)
}

func (x *execution) emitSubagentStarted(ctx context.Context, child store.Conversation) {
	x.run.emit(ctx, func(meta events.EventMeta) events.Event {
		return events.SubagentStarted{EventMeta: meta, ID: child.ID, Role: child.Role}
	})
}

func (x *execution) jobStatus(ctx context.Context, kind, id string) (string, error) {
	job, err := x.engine.cfg.Store.Job(ctx, x.info.ID, kind, id)
	if err != nil {
		return "", err
	}
	output := ""
	if job.OutputPath != "" {
		file, err := os.Open(job.OutputPath)
		if err != nil {
			return "", err
		}
		defer file.Close()
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

func (x *execution) cancelJob(ctx context.Context, kind, id string) (string, error) {
	job, err := x.engine.cfg.Store.Job(ctx, x.info.ID, kind, id)
	if err != nil {
		return "", err
	}
	if x.engine.jobs.Cancel(job.ID) {
		return "Cancellation requested.", nil
	}
	return "Job is " + job.Status + ".", nil
}

func jobJSON(job store.Job, output string) (string, error) {
	raw, err := json.Marshal(struct {
		store.Job
		Output string `json:"output,omitempty"`
	}{job, output})
	return string(raw), err
}
