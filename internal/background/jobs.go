// Package background owns local job lifetimes and their durable results.
package background

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/hollis-labs/libs/core/panics"
	"github.com/hollis-labs/libs/security/redact"
	"github.com/hollis-labs/nanite/internal/shell"
	"github.com/hollis-labs/nanite/internal/store"
)

type Outcome struct {
	Output   string
	ExitCode *int
	Err      error
	// Limit is set when the task stopped at a step, token or context limit
	// rather than finishing its work. The job ends with status "limit".
	Limit string
}
type Task struct {
	PID  int
	Wait func() Outcome
}
type Start func(context.Context, io.Writer) (Task, error)

type activeJob struct {
	cancel context.CancelFunc
	done   chan struct{}
}

type Manager struct {
	db     *store.Store
	scrub  *redact.Redactor
	notify func(store.Notice)
	logger *slog.Logger
	panics *panics.Guard
	mu     sync.Mutex
	active map[string]activeJob
	closed bool
}

func New(db *store.Store, scrub *redact.Redactor, notify func(store.Notice), logger *slog.Logger, guard *panics.Guard) *Manager {
	return &Manager{db: db, scrub: scrub, notify: notify, logger: logger, panics: guard, active: make(map[string]activeJob)}
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
	logWriter := m.scrub.Writer(&boundedLog{file: file})
	var task Task
	err = m.panics.Call("start "+job.Kind+" job", func() (err error) {
		task, err = start(runCtx, logWriter)
		return err
	}, panics.With("job", job.ID))
	if err != nil {
		cancel()
		_ = logWriter.Close()
		_ = file.Close()
		_ = os.Remove(job.OutputPath)
		return store.Job{}, err
	}
	job.PID = task.PID
	if task.PID > 0 {
		job.ProcessIdentity = shell.ProcessIdentity(runCtx, task.PID)
	}
	if err := m.db.StartJob(runCtx, job); err != nil {
		cancel()
		locked = false
		m.mu.Unlock()
		_ = task.Wait()
		_ = logWriter.Close()
		_ = file.Close()
		_ = os.Remove(job.OutputPath)
		return store.Job{}, err
	}
	active := activeJob{cancel: cancel, done: make(chan struct{})}
	m.active[job.ID] = active
	started := job // finish owns its copy; job is still updated below
	m.panics.Go("finish "+job.Kind+" job", func() { m.finish(runCtx, started, task, file, logWriter, active) }, nil, panics.With("job", job.ID))
	job.Status = "running"
	return job, nil
}

func (m *Manager) finish(ctx context.Context, job store.Job, task Task, file *os.File, logWriter io.WriteCloser, active activeJob) {
	defer active.cancel()
	defer close(active.done)
	defer func() { m.mu.Lock(); delete(m.active, job.ID); m.mu.Unlock() }()
	// A panic while the job runs fails the job with the ref; the job is still
	// completed, logged and announced.
	var got Outcome
	if err := m.panics.Call("run "+job.Kind+" job", func() error { got = task.Wait(); return nil }, panics.With("job", job.ID)); err != nil {
		got = Outcome{Err: err}
	}
	job.Status = "succeeded"
	job.ExitCode = got.ExitCode
	if errors.Is(got.Err, context.Canceled) || ctx.Err() != nil {
		job.Status = "cancelled"
	} else if got.Err != nil || (got.ExitCode != nil && *got.ExitCode != 0) {
		job.Status = "failed"
	} else if got.Limit != "" {
		job.Status, job.Reason = "limit", got.Limit
	}
	output := got.Output
	if got.Err != nil {
		output += "\n" + got.Err.Error() + "\n"
	}
	_, writeErr := io.WriteString(logWriter, output)
	flushErr := logWriter.Close()
	if err := errors.Join(writeErr, flushErr, file.Sync(), file.Close()); err != nil {
		m.logger.Warn("write background job output failed", "job", job.ID, "error", m.scrub.Text(err.Error()))
		if job.Status == "succeeded" {
			job.Status = "failed"
		}
	}
	status := job.Status
	if job.Status == "limit" {
		status = "stopped at a limit (" + job.Reason + ") before finishing"
	}
	n := store.Notice{ID: store.NewID(time.Now()), ConversationID: job.ConversationID, Kind: job.Kind,
		Body: fmt.Sprintf("%s job %s %s. Output: %s", job.Kind, job.ID, status, job.OutputPath)}
	n.Body = m.scrub.Text(n.Body)
	if err := m.db.CompleteJob(context.WithoutCancel(ctx), job, n); err != nil {
		m.logger.Warn("persist background job completion failed", "job", job.ID, "error", m.scrub.Text(err.Error()))
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
func Shell(ctx context.Context, command shell.Command, output io.Writer) (Task, error) {
	process, err := shell.StartStreaming(ctx, command, output)
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

const logCap = 64 << 10
const truncatedMarker = "\n[output truncated]\n"

type boundedLog struct {
	file      *os.File
	written   int
	truncated bool
}

func (b *boundedLog) Write(p []byte) (int, error) {
	n := min(len(p), logCap-len(truncatedMarker)-b.written)
	written, err := b.file.Write(p[:n])
	b.written += written
	if err != nil {
		return written, err
	}
	if n < len(p) && !b.truncated {
		b.truncated = true
		if _, err := b.file.WriteString(truncatedMarker); err != nil {
			return n, err
		}
	}
	return len(p), nil
}
