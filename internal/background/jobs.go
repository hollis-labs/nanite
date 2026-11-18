// Package background owns local job lifetimes and their durable results.
package background

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/hollis-labs/nanite/internal/shell"
	"github.com/hollis-labs/nanite/internal/store"
)

type Outcome struct {
	Output   string
	ExitCode *int
	Err      error
}
type Task struct {
	PID  int
	Wait func() Outcome
}
type Start func(context.Context) (Task, error)

type activeJob struct {
	cancel context.CancelFunc
	done   chan struct{}
}

type Manager struct {
	db     *store.Store
	scrub  func(string) string
	notify func(store.Notice)
	logger *slog.Logger
	mu     sync.Mutex
	active map[string]activeJob
	closed bool
}

func New(db *store.Store, scrub func(string) string, notify func(store.Notice), logger *slog.Logger) *Manager {
	return &Manager{db: db, scrub: scrub, notify: notify, logger: logger, active: make(map[string]activeJob)}
}

func (m *Manager) Start(ctx context.Context, job store.Job, start Start) (store.Job, error) {
	if err := ctx.Err(); err != nil {
		return store.Job{}, err
	}
	m.mu.Lock()
	locked := true
	defer func() {
		if locked {
			m.mu.Unlock()
		}
	}()
	if m.closed {
		return store.Job{}, fmt.Errorf("background jobs are closed")
	}
	dir := m.db.JobDir()
	if err := store.PreparePrivateDir(dir); err != nil {
		return store.Job{}, err
	}
	job.OutputPath = filepath.Join(dir, job.ID+".log")
	file, err := os.OpenFile(job.OutputPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return store.Job{}, err
	}
	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	task, err := start(runCtx)
	if err != nil {
		cancel()
		_ = file.Close()
		_ = os.Remove(job.OutputPath)
		return store.Job{}, err
	}
	job.PID = task.PID
	if err := m.db.StartJob(runCtx, job); err != nil {
		cancel()
		locked = false
		m.mu.Unlock()
		_ = task.Wait()
		_ = file.Close()
		_ = os.Remove(job.OutputPath)
		return store.Job{}, err
	}
	active := activeJob{cancel: cancel, done: make(chan struct{})}
	m.active[job.ID] = active
	go m.finish(runCtx, job, task, file, active)
	job.Status = "running"
	return job, nil
}

func (m *Manager) finish(ctx context.Context, job store.Job, task Task, file *os.File, active activeJob) {
	defer active.cancel()
	defer close(active.done)
	defer func() { m.mu.Lock(); delete(m.active, job.ID); m.mu.Unlock() }()
	got := task.Wait()
	job.Status = "succeeded"
	job.ExitCode = got.ExitCode
	if errors.Is(got.Err, context.Canceled) || ctx.Err() != nil {
		job.Status = "cancelled"
	} else if got.Err != nil || (got.ExitCode != nil && *got.ExitCode != 0) {
		job.Status = "failed"
	}
	output := m.scrub(got.Output)
	if got.Err != nil {
		output += "\n" + m.scrub(got.Err.Error()) + "\n"
	}
	if len(output) > 64<<10 {
		output = strings.ToValidUTF8(output[:64<<10], "") + "\n[output truncated]"
	}
	_, writeErr := file.WriteString(output)
	if err := errors.Join(writeErr, file.Sync(), file.Close()); err != nil {
		m.logger.Warn("write background job output failed", "job", job.ID, "error", m.scrub(err.Error()))
		if job.Status == "succeeded" {
			job.Status = "failed"
		}
	}
	n := store.Notice{ID: store.NewID(time.Now()), ConversationID: job.ConversationID, Kind: job.Kind,
		Body: fmt.Sprintf("%s job %s %s. Output: %s", job.Kind, job.ID, job.Status, job.OutputPath)}
	n.Body = m.scrub(n.Body)
	if err := m.db.CompleteJob(context.WithoutCancel(ctx), job, n); err != nil {
		m.logger.Warn("persist background job completion failed", "job", job.ID, "error", m.scrub(err.Error()))
		return
	}
	m.notify(n)
}

func (m *Manager) Cancel(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	job, ok := m.active[id]
	if ok {
		job.cancel()
	}
	return ok
}

func (m *Manager) Close() {
	m.mu.Lock()
	m.closed = true
	jobs := make([]activeJob, 0, len(m.active))
	for _, job := range m.active {
		jobs = append(jobs, job)
		job.cancel()
	}
	m.mu.Unlock()
	for _, job := range jobs {
		<-job.done
	}
}

// Shell starts an isolated process group and returns a cancellable job task.
func Shell(ctx context.Context, command shell.Command) (Task, error) {
	process, err := shell.Start(ctx, command)
	if err != nil {
		return Task{}, err
	}
	return Task{PID: process.PID(), Wait: func() Outcome {
		result, err := process.Wait()
		process.CleanupGroup()
		var exit *int
		if err == nil {
			exit = &result.ExitCode
		}
		return Outcome{Output: result.Output, ExitCode: exit, Err: err}
	}}, nil
}
