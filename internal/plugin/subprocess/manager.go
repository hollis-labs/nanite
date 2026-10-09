package subprocess

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	pluginhost "github.com/hollis-labs/libs/plugin-mcp/plugin-host"
	"github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/capability"
	sdksub "github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/subprocess"
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
	// LifecycleContext is the owning host lifetime, independent of Start requests.
	// Standalone managers own their lifetime until Stop when this is nil.
	LifecycleContext context.Context

	ReviewDigest string          // original accepted bundle; immutable for this manager lifetime
	Identity     json.RawMessage // opaque host-verified init claims
	OnUnload     func()          // release host-owned connection resources on unload or failed init

	Command string   // executable path
	Args    []string // command-line arguments
	Env     []string // explicit host-approved "KEY=VALUE" pairs; never ambient inheritance
	Secrets []string // resolved secret values scrubbed from diagnostics
	Granted []string // only capabilities accepted by the host
	// IssueGrants must re-evaluate current host policy for every incarnation.
	// No issuer means reviewed capabilities cannot be launched with protocol 2.
	IssueGrants      func(context.Context, capability.RuntimeIdentity) (capability.GrantSet, error)
	RevalidateGrants func(context.Context, capability.GrantSet) error
	IdentitySecrets  func(json.RawMessage) []string
	InitIdentity     func(context.Context, capability.RuntimeIdentity, *GrantLease) (json.RawMessage, error)
	OnDisconnect     func()
	BeforeSpawn      func(context.Context) error // checks accepted bytes on every start/restart
	ID               string                      // canonical manifest identity; empty only in harnesses
	WorkDir          string                      // working directory (plugin directory)

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
	cfg              ManagerConfig
	mu               sync.Mutex
	state            ProcessState
	supervisor       *pluginhost.Supervisor
	transport        *Transport
	onCrash          func(error)
	onLifecycle      func()
	incarnation      capability.RuntimeIdentity
	grantLease       *GrantLease
	connection       *pluginhost.Conn
	renewalSupported bool
	renewalSequence  uint64
	renewing         *GrantLease
}

func NewManager(cfg ManagerConfig) *Manager { return &Manager{cfg: cfg, state: StateStopped} }

func (m *Manager) Start(ctx context.Context, init InitParams) (*Transport, error) {
	supervisorContext := m.cfg.LifecycleContext
	if supervisorContext == nil {
		supervisorContext = context.Background()
	}
	m.mu.Lock()
	if m.supervisor != nil {
		m.mu.Unlock()
		return nil, fmt.Errorf("plugin manager already started")
	}
	m.state = StateStarting
	if m.cfg.ID == "" || len(init.Grants) != 0 || (len(m.cfg.Granted) > 0 && m.cfg.IssueGrants == nil) {
		m.state = StateStopped
		m.mu.Unlock()
		return nil, fmt.Errorf("plugin launch requires a canonical owner and fresh host-issued grants")
	}
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
		InitFactory: func(ctx context.Context, _ uint64) (InitParams, error) {
			owner, err := NewOwnerIncarnation(ctx, m.cfg.ID)
			if err != nil {
				return InitParams{}, err
			}
			params := init
			params.Incarnation = owner
			renewalVersion := sdksub.GrantsRenewalVersion
			params.GrantsRenewalVersion = &renewalVersion
			if m.cfg.IssueGrants != nil {
				params.Grants, err = m.cfg.IssueGrants(ctx, owner)
				if err != nil {
					return InitParams{}, err
				}
			}
			lease, leaseErr := NewGrantLease(supervisorContext, params.Grants, owner, nil)
			if leaseErr != nil {
				return InitParams{}, leaseErr
			}
			if m.cfg.InitIdentity != nil {
				params.Identity, err = m.cfg.InitIdentity(ctx, owner, lease)
				if err != nil {
					lease.Revoke()
					return InitParams{}, err
				}
			}
			if m.cfg.IdentitySecrets != nil {
				m.mu.Lock()
				transport := m.transport
				m.mu.Unlock()
				transport.addSecrets(m.cfg.IdentitySecrets(params.Identity))
			}
			m.mu.Lock()
			if m.state == StateStopping || m.state == StateStopped || ctx.Err() != nil {
				m.mu.Unlock()
				lease.Revoke()
				return InitParams{}, ErrGrantLeaseEnded
			}
			lease.mu.Lock()
			if lease.ended || lease.ctx.Err() != nil {
				lease.mu.Unlock()
				m.mu.Unlock()
				lease.Revoke()
				return InitParams{}, ErrGrantLeaseEnded
			}
			// Bind expiry to this exact attempt. An old timer must never stop
			// a replacement child or revoke its freshly bound credentials.
			lease.onEnd = func() { go func() { _ = m.stopLease(context.Background(), lease) }() }
			m.grantLease = lease
			m.incarnation = owner
			lease.mu.Unlock()
			m.mu.Unlock()
			return params, nil
		},
		// The existing Nanite restart setting opts into bounded recovery from
		// unexpected child exits. Startup/authorization failures remain terminal.
		ClassifyExit: func(pluginhost.ExitInfo) error {
			if m.cfg.MaxRestarts <= 0 {
				return nil
			}
			return &pluginhost.TransientError{Code: "nanite_unexpected_exit"}
		},
		Policy:         pluginhost.RestartPolicy{MaxRestarts: maxRestarts, Initial: m.cfg.InitialBackoff, Max: m.cfg.MaxBackoff, Factor: m.cfg.BackoffFactor},
		HealthInterval: m.cfg.HealthInterval, KillAfterUnhealthy: 1,
		OnStart: func(p *pluginhost.Process) {
			if m.cfg.ID != "" && p.Info().ID != m.cfg.ID {
				_ = p.Kill()
				m.recordCrash(fmt.Errorf("plugin handshake identity differs from manifest"))
				return
			}
			m.mu.Lock()
			if m.state == StateStopping || m.state == StateStopped || m.grantLease == nil {
				m.mu.Unlock()
				_ = p.Kill()
				return
			}
			if _, err := m.grantLease.Context(); err != nil {
				m.mu.Unlock()
				_ = p.Kill()
				return
			}
			m.connection = p.Client().Conn()
			feature := p.Info().GrantsRenewalVersion
			m.renewalSupported = feature != nil && *feature == sdksub.GrantsRenewalVersion
			m.renewalSequence = 0
			m.renewing = nil
			m.state = StateRunning
			changed := m.onLifecycle
			m.mu.Unlock()
			if changed != nil {
				changed()
			}
		},
		OnExit: func(info pluginhost.ExitInfo, restarting bool) {
			m.endGrantLease()
			if m.cfg.OnDisconnect != nil {
				m.cfg.OnDisconnect()
			}
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
	transport := &Transport{dispatch: m.acquireDispatch, secrets: append([]string(nil), m.cfg.Secrets...), current: func() *pluginhost.Conn {
		p := supervisor.Current()
		if p == nil || (m.cfg.ID != "" && p.Info().ID != m.cfg.ID) {
			return nil
		}
		return p.Client().Conn()
	}}
	m.transport = transport
	m.mu.Unlock()
	if err := supervisor.Start(ctx); err != nil {
		m.endGrantLease()
		m.recordCrash(err)
		return nil, redactPluginError(err, m.secretValues())
	}
	if p := supervisor.Current(); p == nil || (m.cfg.ID != "" && p.Info().ID != m.cfg.ID) {
		_ = supervisor.Stop(context.Background())
		return nil, fmt.Errorf("plugin handshake identity differs from manifest")
	}
	return transport, nil
}

func (m *Manager) Incarnation() capability.RuntimeIdentity {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state != StateRunning {
		return capability.RuntimeIdentity{}
	}
	return m.incarnation
}

func (m *Manager) SetLifecycleObserver(changed func()) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onLifecycle = changed
}

func (m *Manager) recordCrash(err error) {
	err = redactPluginError(err, m.secretValues())
	m.mu.Lock()
	if m.state != StateStopping && m.state != StateStopped {
		m.state = StateCrashed
	}
	callback := m.onCrash
	changed := m.onLifecycle
	m.mu.Unlock()
	if changed != nil {
		changed()
	}
	if callback != nil {
		callback(err)
	}
}

func (m *Manager) Stop() error { return m.stop(context.Background()) }

func (m *Manager) stop(ctx context.Context) error { return m.stopLease(ctx, nil) }

func (m *Manager) stopLease(ctx context.Context, expected *GrantLease) error {
	m.mu.Lock()
	if expected != nil && m.grantLease != expected {
		m.mu.Unlock()
		return nil
	}
	supervisor := m.supervisor
	lease := m.grantLease
	m.grantLease = nil
	m.connection = nil
	m.state = StateStopping
	m.mu.Unlock()
	endLease(lease)
	if m.cfg.OnUnload != nil {
		m.cfg.OnUnload()
		defer m.cfg.OnUnload() // Stop joins the supervisor before final credential cleanup.
	}
	var err error
	if supervisor != nil {
		err = supervisor.Stop(ctx)
	}
	m.mu.Lock()
	m.state = StateStopped
	changed := m.onLifecycle
	m.mu.Unlock()
	if changed != nil {
		changed()
	}
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

// acquireDispatch captures the connection and lease of one live incarnation.
// Policy callbacks run without the manager lock; a restart during revalidation
// refuses this dispatch instead of applying its permit to the next child.
func (m *Manager) acquireDispatch(ctx context.Context) (*pluginhost.Conn, context.Context, func(), error) {
	m.mu.Lock()
	lease, conn := m.grantLease, m.connection
	running := m.state == StateRunning
	m.mu.Unlock()
	if !running || lease == nil || conn == nil {
		return nil, nil, nil, ErrGrantLeaseEnded
	}
	if m.cfg.RevalidateGrants != nil {
		if err := m.cfg.RevalidateGrants(ctx, lease.Grants()); err != nil {
			lease.Revoke()
			return nil, nil, nil, err
		}
	}
	permit, release, err := lease.Acquire(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	m.mu.Lock()
	current := m.state == StateRunning && m.grantLease == lease && m.connection == conn
	m.mu.Unlock()
	if !current {
		release()
		return nil, nil, nil, ErrGrantLeaseEnded
	}
	return conn, permit, release, nil
}
func (m *Manager) endGrantLease() {
	m.mu.Lock()
	lease := m.grantLease
	m.grantLease = nil
	m.connection = nil
	m.mu.Unlock()
	endLease(lease)
}
func endLease(lease *GrantLease) {
	// Suppress the expiry callback for ordinary lifecycle termination.
	if lease != nil {
		lease.mu.Lock()
		lease.onEnd = nil
		lease.mu.Unlock()
		lease.Revoke()
	}
}

func (m *Manager) CurrentGrants() capability.GrantSet {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state != StateRunning || m.grantLease == nil {
		return nil
	}
	return m.grantLease.Grants()
}

func (m *Manager) secretValues() []string {
	m.mu.Lock()
	t := m.transport
	m.mu.Unlock()
	if t == nil {
		return append([]string(nil), m.cfg.Secrets...)
	}
	return t.secretValues()
}
