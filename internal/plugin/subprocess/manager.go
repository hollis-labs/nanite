package subprocess

import (
	"context"
	"fmt"
	"log"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/hollis-labs/nanite/internal/safego"
)

// ProcessState tracks the current state of a subprocess.
type ProcessState int

const (
	StateStopped  ProcessState = iota
	StateStarting
	StateRunning
	StateStopping
	StateCrashed
)

func (s ProcessState) String() string {
	switch s {
	case StateStopped:
		return "stopped"
	case StateStarting:
		return "starting"
	case StateRunning:
		return "running"
	case StateStopping:
		return "stopping"
	case StateCrashed:
		return "crashed"
	default:
		return "unknown"
	}
}

// ManagerConfig configures the subprocess manager.
type ManagerConfig struct {
	Command    string   // executable path
	Args       []string // command-line arguments
	Env        []string // "KEY=VALUE" pairs
	WorkDir    string   // working directory (plugin directory)

	// Health check interval. Zero disables periodic health checks.
	HealthInterval time.Duration

	// Maximum number of automatic restarts before giving up.
	// Zero means no restarts.
	MaxRestarts int

	// Backoff settings for restarts.
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
	BackoffFactor  float64

	// Graceful shutdown timeout. Process is killed after this.
	ShutdownTimeout time.Duration

	// Startup timeout. If plugin/init doesn't respond in time, start fails.
	StartupTimeout time.Duration
}

// DefaultManagerConfig returns sensible defaults.
func DefaultManagerConfig(command string, workDir string) ManagerConfig {
	return ManagerConfig{
		Command:         command,
		WorkDir:         workDir,
		HealthInterval:  30 * time.Second,
		MaxRestarts:     3,
		InitialBackoff:  1 * time.Second,
		MaxBackoff:      30 * time.Second,
		BackoffFactor:   2.0,
		ShutdownTimeout: 5 * time.Second,
		StartupTimeout:  10 * time.Second,
	}
}

// Manager manages the lifecycle of a subprocess plugin process.
type Manager struct {
	cfg ManagerConfig

	mu        sync.Mutex
	state     ProcessState
	cmd       *exec.Cmd
	transport *Transport
	restarts  int
	lastStart time.Time
	waitCh    chan error // closed after cmd.Wait() returns; single waiter

	// healthCancel stops the health check goroutine.
	healthCancel context.CancelFunc

	// onCrash is called when the process crashes. The manager will attempt
	// a restart if within limits. Set by SubprocessPlugin.
	onCrash func(err error)
}

// NewManager creates a new subprocess manager.
func NewManager(cfg ManagerConfig) *Manager {
	return &Manager{
		cfg:   cfg,
		state: StateStopped,
	}
}

// Start launches the subprocess and establishes the JSON-RPC transport.
// Returns the transport for the caller to perform the init handshake.
func (m *Manager) Start(ctx context.Context) (*Transport, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.state == StateRunning {
		return m.transport, nil
	}

	m.state = StateStarting

	// Do not use CommandContext — that ties the process lifetime to the
	// caller's context (often a short startup timeout). The process must
	// outlive the startup phase; shutdown is handled by Stop().
	cmd := exec.Command(m.cfg.Command, m.cfg.Args...)
	if m.cfg.WorkDir != "" {
		cmd.Dir = m.cfg.WorkDir
	}
	if len(m.cfg.Env) > 0 {
		cmd.Env = append(cmd.Environ(), m.cfg.Env...)
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		m.state = StateStopped
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		m.state = StateStopped
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}

	// Capture stderr for crash diagnostics (last 4KB).
	stderr := &ringBuffer{buf: make([]byte, 4096)}
	cmd.Stderr = stderr

	// Process group + WaitDelay: the subprocess plugin binary may fork
	// helper processes (language runtimes, e.g. a Node wrapper spawning
	// its own workers). Setpgid places the whole tree in a new process
	// group so Stop's -pid SIGKILL below reaches every descendant. The
	// audit's finding 07 calls this out specifically for orphan
	// grandchildren that outlive the direct child. WaitDelay bounds
	// cmd.Wait so a grandchild holding an inherited stdout pipe cannot
	// pin cmdWait forever.
	configureSubprocAttr(cmd)
	cmd.WaitDelay = 10 * time.Second

	if err := cmd.Start(); err != nil {
		m.state = StateStopped
		return nil, fmt.Errorf("start %s: %w", m.cfg.Command, err)
	}

	transport := NewTransport(stdout, stdin)

	waitCh := make(chan error, 1)
	m.cmd = cmd
	m.transport = transport
	m.lastStart = time.Now()
	m.state = StateRunning
	m.waitCh = waitCh

	// Single goroutine calls cmd.Wait(); both waitForExit and Stop observe waitCh.
	safego.Go(context.Background(), "plugin.subprocess.manager.cmdWait", func() {
		waitCh <- cmd.Wait()
	})
	safego.Go(context.Background(), "plugin.subprocess.manager.waitForExit", func() {
		m.waitForExit(stderr)
	})

	// Start periodic health checks if configured.
	if m.cfg.HealthInterval > 0 {
		hctx, hcancel := context.WithCancel(context.Background())
		m.healthCancel = hcancel
		safego.Go(hctx, "plugin.subprocess.manager.healthLoop", func() {
			m.healthLoop(hctx)
		})
	}

	return transport, nil
}

// Stop gracefully shuts down the subprocess.
func (m *Manager) Stop() error {
	m.mu.Lock()
	if m.state != StateRunning && m.state != StateCrashed {
		m.mu.Unlock()
		return nil
	}
	m.state = StateStopping

	// Stop health checks.
	if m.healthCancel != nil {
		m.healthCancel()
		m.healthCancel = nil
	}

	cmd := m.cmd
	transport := m.transport
	waitCh := m.waitCh
	m.mu.Unlock()

	// Try graceful shutdown via plugin/unload.
	if transport != nil {
		ctx, cancel := context.WithTimeout(context.Background(), m.cfg.ShutdownTimeout)
		_, _ = transport.Call(ctx, MethodUnload, nil)
		cancel()
	}

	// Wait for the process to exit, using the single waitCh from Start().
	select {
	case <-waitCh:
		// Exited cleanly.
	case <-time.After(m.cfg.ShutdownTimeout):
		// Force kill the process group.
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		<-waitCh
	}

	m.mu.Lock()
	m.state = StateStopped
	m.transport = nil
	m.cmd = nil
	m.mu.Unlock()

	return nil
}

// Transport returns the current transport, or nil if not running.
func (m *Manager) Transport() *Transport {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.transport
}

// State returns the current process state.
func (m *Manager) State() ProcessState {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state
}

// Restarts returns the number of automatic restarts performed.
func (m *Manager) Restarts() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.restarts
}

// waitForExit monitors the subprocess and handles unexpected exits.
func (m *Manager) waitForExit(stderr *ringBuffer) {
	err := <-m.waitCh

	m.mu.Lock()
	if m.state == StateStopping || m.state == StateStopped {
		// Expected shutdown — nothing to do.
		m.mu.Unlock()
		return
	}

	m.state = StateCrashed
	restarts := m.restarts
	maxRestarts := m.cfg.MaxRestarts
	m.mu.Unlock()

	exitErr := fmt.Errorf("plugin process exited unexpectedly: %w (stderr: %s)", err, stderr.String())
	log.Printf("subprocess: %s", exitErr)

	if m.onCrash != nil {
		safego.Call(context.Background(), "plugin-hook.subprocess.onCrash", func() {
			m.onCrash(exitErr)
		})
	}

	// Attempt restart if within limits.
	if restarts < maxRestarts {
		m.attemptRestart(restarts)
	}
}

// attemptRestart tries to restart the subprocess with backoff.
func (m *Manager) attemptRestart(attempt int) {
	backoff := m.cfg.InitialBackoff
	for i := 0; i < attempt; i++ {
		backoff = time.Duration(float64(backoff) * m.cfg.BackoffFactor)
		if backoff > m.cfg.MaxBackoff {
			backoff = m.cfg.MaxBackoff
			break
		}
	}

	log.Printf("subprocess: restarting in %s (attempt %d/%d)", backoff, attempt+1, m.cfg.MaxRestarts)
	time.Sleep(backoff)

	ctx, cancel := context.WithTimeout(context.Background(), m.cfg.StartupTimeout)
	defer cancel()

	_, err := m.Start(ctx)
	if err != nil {
		log.Printf("subprocess: restart failed: %v", err)
		m.mu.Lock()
		m.state = StateCrashed
		m.mu.Unlock()
		return
	}

	m.mu.Lock()
	m.restarts++
	m.mu.Unlock()

	log.Printf("subprocess: restart successful (attempt %d/%d)", attempt+1, m.cfg.MaxRestarts)
}

// healthLoop periodically checks the subprocess health.
func (m *Manager) healthLoop(ctx context.Context) {
	ticker := time.NewTicker(m.cfg.HealthInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.healthCheck()
		}
	}
}

// healthCheck sends a plugin/health request and handles failures.
func (m *Manager) healthCheck() {
	m.mu.Lock()
	transport := m.transport
	state := m.state
	m.mu.Unlock()

	if state != StateRunning || transport == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := CallResult[HealthResult](transport, ctx, MethodHealth, nil)
	if err != nil {
		log.Printf("subprocess: health check failed: %v", err)
		return
	}

	if !result.OK {
		log.Printf("subprocess: health check unhealthy: %s", result.Message)
	}
}

// ringBuffer is a simple circular buffer for capturing stderr.
type ringBuffer struct {
	buf []byte
	pos int
	full bool
}

func (rb *ringBuffer) Write(p []byte) (int, error) {
	for _, b := range p {
		rb.buf[rb.pos] = b
		rb.pos++
		if rb.pos >= len(rb.buf) {
			rb.pos = 0
			rb.full = true
		}
	}
	return len(p), nil
}

func (rb *ringBuffer) String() string {
	if !rb.full {
		return string(rb.buf[:rb.pos])
	}
	// Wrap around: data from pos..end + 0..pos
	return string(rb.buf[rb.pos:]) + string(rb.buf[:rb.pos])
}
