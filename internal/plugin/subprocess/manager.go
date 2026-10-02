package subprocess

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	pluginhost "github.com/hollis-labs/plugin-host"
)

// ProcessState tracks the current state of a subprocess.
type ProcessState int

const (
	StateStopped ProcessState = iota
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
	Identity json.RawMessage // opaque host-verified init claims
	OnUnload func()          // release host-owned connection resources on unload or failed init

	Command     string                      // executable path
	Args        []string                    // command-line arguments
	Env         []string                    // explicit host-approved "KEY=VALUE" pairs; never ambient inheritance
	Secrets     []string                    // resolved secret values scrubbed from diagnostics
	Granted     []string                    // only capabilities accepted by the host
	BeforeSpawn func(context.Context) error // checks accepted bytes on every start/restart
	ID          string                      // canonical manifest identity; empty only in harnesses
	WorkDir     string                      // working directory (plugin directory)

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

// Manager translates Nanite policy into the shared plugin lifecycle driver.
// Framing, restart, health, process groups and shutdown belong to plugin-host.
type Manager struct {
	cfg        ManagerConfig
	mu         sync.Mutex
	state      ProcessState
	supervisor *pluginhost.Supervisor
	transport  *Transport
	onCrash    func(error)
}

func NewManager(cfg ManagerConfig) *Manager { return &Manager{cfg: cfg, state: StateStopped} }

func (m *Manager) Start(ctx context.Context, init InitParams) (*Transport, error) {
	m.mu.Lock()
	if m.supervisor != nil {
		m.mu.Unlock()
		return nil, fmt.Errorf("plugin manager already started")
	}
	m.state = StateStarting
	maxRestarts := m.cfg.MaxRestarts
	if maxRestarts == 0 {
		maxRestarts = -1
	}
	supervisor := pluginhost.Supervise(pluginhost.Spec{
		ID: m.cfg.ID, Command: m.cfg.Command, Args: m.cfg.Args, Dir: m.cfg.WorkDir,
		Env: pluginEnvironment(os.Environ(), m.cfg.Env), Init: init, Secrets: m.cfg.Secrets,
		HandshakeTimeout: m.cfg.StartupTimeout, UnloadTimeout: m.cfg.ShutdownTimeout,
		BeforeSpawn: m.cfg.BeforeSpawn,
		ConnOptions: []pluginhost.ConnOption{pluginhost.WithDefaultTimeout(MaxCallDuration), pluginhost.WithMaxFrame(8 << 20), pluginhost.WithMaxInboundFrame(16 << 20)},
	}, pluginhost.SuperviseOptions{
		Policy:         pluginhost.RestartPolicy{MaxRestarts: maxRestarts, Initial: m.cfg.InitialBackoff, Max: m.cfg.MaxBackoff, Factor: m.cfg.BackoffFactor},
		HealthInterval: m.cfg.HealthInterval, KillAfterUnhealthy: 1,
		OnStart: func(p *pluginhost.Process) {
			if m.cfg.ID != "" && p.Info().ID != m.cfg.ID {
				_ = p.Kill()
				m.recordCrash(fmt.Errorf("plugin handshake identity differs from manifest"))
				return
			}
			m.mu.Lock()
			m.state = StateRunning
			m.mu.Unlock()
		},
		OnExit: func(info pluginhost.ExitInfo, restarting bool) {
			m.recordCrash(fmt.Errorf("plugin exited: code %d, signal %s", info.Code, info.Signal))
		},
		OnGiveUp: func(err error) {
			m.recordCrash(err)
			if m.cfg.OnUnload != nil {
				m.cfg.OnUnload()
			}
		},
	})
	m.supervisor = supervisor
	transport := &Transport{secrets: append([]string(nil), m.cfg.Secrets...), current: func() *pluginhost.Conn {
		p := supervisor.Current()
		if p == nil || (m.cfg.ID != "" && p.Info().ID != m.cfg.ID) {
			return nil
		}
		return p.Client().Conn()
	}}
	m.transport = transport
	m.mu.Unlock()
	if err := supervisor.Start(ctx); err != nil {
		m.recordCrash(err)
		return nil, redactPluginError(err, m.cfg.Secrets)
	}
	if p := supervisor.Current(); p == nil || (m.cfg.ID != "" && p.Info().ID != m.cfg.ID) {
		_ = supervisor.Stop(context.Background())
		return nil, fmt.Errorf("plugin handshake identity differs from manifest")
	}
	return transport, nil
}

func (m *Manager) recordCrash(err error) {
	err = redactPluginError(err, m.cfg.Secrets)
	m.mu.Lock()
	if m.state != StateStopping && m.state != StateStopped {
		m.state = StateCrashed
	}
	callback := m.onCrash
	m.mu.Unlock()
	if callback != nil {
		callback(err)
	}
}

func (m *Manager) Stop() error { return m.stop(context.Background()) }

func (m *Manager) stop(ctx context.Context) error {
	if m.cfg.OnUnload != nil {
		m.cfg.OnUnload()
		defer m.cfg.OnUnload() // Stop joins the supervisor before final credential cleanup.
	}
	m.mu.Lock()
	supervisor := m.supervisor
	m.state = StateStopping
	m.mu.Unlock()
	var err error
	if supervisor != nil {
		err = supervisor.Stop(ctx)
	}
	m.mu.Lock()
	m.state = StateStopped
	m.mu.Unlock()
	return err
}

func (m *Manager) process() *pluginhost.Process {
	m.mu.Lock()
	supervisor := m.supervisor
	m.mu.Unlock()
	if supervisor == nil {
		return nil
	}
	return supervisor.Current()
}

func (m *Manager) State() ProcessState { m.mu.Lock(); defer m.mu.Unlock(); return m.state }
func (m *Manager) Restarts() int {
	m.mu.Lock()
	supervisor := m.supervisor
	m.mu.Unlock()
	if supervisor == nil {
		return 0
	}
	return supervisor.Restarts()
}
