package agent

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/hollis-labs/go-agent-wrapper/acp"
)

// SessionManager is Nanite's single binding registry from runtime IDs to the
// wrapper handles returned by Boot. It does not own provider processes or
// duplicate their state: all lifecycle operations delegate to Wrapper, and
// ACP liveness is read from the shared wrapper-owned acp.Manager.
//
// The registry is also used by the chat service to find the long-lived
// wrapper for a conversation. Keeping that binding here avoids the former
// pair of independent sync.Maps in Dependencies and chatServiceImpl, whose
// entries could disagree during recovery replacement.
var (
	ErrSessionManagerClosed = errors.New("agent: session manager is shut down")
	ErrRecoveryInProgress   = errors.New("agent: session recovery is in progress")
)

type recoveryLease uint64

type SessionManager struct {
	mu sync.RWMutex

	sessions   map[string]*Session
	pending    map[*Session]struct{}
	recovering map[string]recoveryLease
	nextLease  recoveryLease
	closed     bool
	launches   sync.WaitGroup

	acp *acp.Manager
}

// AdmitLaunch registers a candidate before Wrapper.Run starts. Shutdown can
// therefore cancel and wait for Boots that have not emitted readiness yet.
func (m *SessionManager) AdmitLaunch(sess *Session) error {
	if m == nil || sess == nil {
		return errors.New("agent: invalid session launch admission")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrSessionManagerClosed
	}
	m.pending[sess] = struct{}{}
	m.launches.Add(1)
	return nil
}

func (m *SessionManager) finishLaunchLocked(sess *Session) {
	if _, ok := m.pending[sess]; !ok {
		return
	}
	delete(m.pending, sess)
	m.launches.Done()
}

// NewSessionManager constructs an empty binding registry with one shared ACP
// manager. Every ACP Wrapper created by Boot receives this manager.
func NewSessionManager() *SessionManager {
	return &SessionManager{
		sessions:   make(map[string]*Session),
		pending:    make(map[*Session]struct{}),
		recovering: make(map[string]recoveryLease),
		acp:        acp.NewManager(),
	}
}

// ACPManager returns the wrapper ACP lifecycle manager shared by all sessions.
func (m *SessionManager) ACPManager() *acp.Manager {
	if m == nil {
		return nil
	}
	return m.acp
}

// RegisterReady admits a Wrapper at its readiness boundary. Recovery
// relaunches remain pending until the broker adopts them, but Shutdown still
// owns them while they are pending.
func (m *SessionManager) RegisterReady(id string, sess *Session, relaunch bool) error {
	if m == nil || id == "" || sess == nil {
		return errors.New("agent: invalid session registration")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrSessionManagerClosed
	}
	if relaunch {
		return nil
	}
	if _, recovering := m.recovering[id]; recovering {
		return fmt.Errorf("%w: %s", ErrRecoveryInProgress, id)
	}
	m.sessions[id] = sess
	m.finishLaunchLocked(sess)
	return nil
}

// Store binds id to sess. Production Boot uses RegisterReady so a relaunch is
// tracked without overwriting its predecessor before broker adoption.
func (m *SessionManager) Store(id string, sess *Session) error {
	return m.RegisterReady(id, sess, false)
}

// Adopt binds a broker-dispatched replacement. It is the sole admission path
// allowed while recovery reserves id.
func (m *SessionManager) Adopt(id string, sess *Session) (*Session, bool, error) {
	if m == nil || id == "" || sess == nil {
		return nil, false, errors.New("agent: invalid replacement adoption")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, false, ErrSessionManagerClosed
	}
	m.finishLaunchLocked(sess)
	delete(m.recovering, id)
	previous, loaded := m.sessions[id]
	m.sessions[id] = sess
	return previous, loaded, nil
}

// Swap retains the pre-cutover replacement helper for non-recovery callers.
func (m *SessionManager) Swap(id string, sess *Session) (*Session, bool, error) {
	if m == nil || id == "" || sess == nil {
		return nil, false, errors.New("agent: invalid session swap")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, false, ErrSessionManagerClosed
	}
	if _, recovering := m.recovering[id]; recovering {
		return nil, false, fmt.Errorf("%w: %s", ErrRecoveryInProgress, id)
	}
	previous, loaded := m.sessions[id]
	m.sessions[id] = sess
	return previous, loaded, nil
}

// Load returns the wrapper handle bound to id.
func (m *SessionManager) Load(id string) (*Session, bool) {
	if m == nil {
		return nil, false
	}
	m.mu.RLock()
	sess, ok := m.sessions[id]
	m.mu.RUnlock()
	return sess, ok
}

// LoadLive returns id's current binding only when that exact generation is
// still live. The liveness check occurs under the binding read lock so a
// concurrent replacement cannot make an older pointer appear live.
func (m *SessionManager) LoadLive(id string) (*Session, bool) {
	if m == nil {
		return nil, false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	sess, ok := m.sessions[id]
	// A nil runDone exists only in lightweight package-external test handles;
	// every Boot-created Session has one. Preserve their historical registry
	// lookup behavior without weakening real terminal detection.
	return sess, ok && (sess.runDone == nil || sess.isLive())
}

// Delete removes any binding for id.
func (m *SessionManager) Delete(id string) {
	if m != nil {
		m.mu.Lock()
		delete(m.sessions, id)
		m.mu.Unlock()
	}
}

// LoadAndDelete atomically removes and returns id's current binding.
func (m *SessionManager) LoadAndDelete(id string) (*Session, bool) {
	if m == nil {
		return nil, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	sess, ok := m.sessions[id]
	if ok {
		delete(m.sessions, id)
	}
	return sess, ok
}

// CompareAndDelete removes id only when it still names expected.
func (m *SessionManager) CompareAndDelete(id string, expected *Session) bool {
	return m.Retire(id, expected)
}

// Retire removes only expected. Session/turn auxiliary state is deliberately
// outside this operation: it can be published before a successor runtime is
// bound, so runtime retirement cannot prove ownership of ID-keyed state.
func (m *SessionManager) Retire(id string, expected *Session) bool {
	if m == nil || expected == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sessions[id] != expected {
		return false
	}
	delete(m.sessions, id)
	return true
}

// BeginRecovery atomically retires expected and reserves id until Adopt or
// EndRecovery. A stale observer cannot recover over an installed successor.
func (m *SessionManager) BeginRecovery(id string, expected *Session) (recoveryLease, bool) {
	if m == nil || expected == nil {
		return 0, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.sessions[id] != expected {
		return 0, false
	}
	delete(m.sessions, id)
	m.nextLease++
	if m.nextLease == 0 {
		m.nextLease++
	}
	lease := m.nextLease
	m.recovering[id] = lease
	return lease, true
}

// EndRecovery releases id only if lease is still current. Adopt clears it
// first when the broker produced a replacement.
func (m *SessionManager) EndRecovery(id string, lease recoveryLease) {
	if m == nil || lease == 0 {
		return
	}
	m.mu.Lock()
	if m.recovering[id] == lease {
		delete(m.recovering, id)
	}
	m.mu.Unlock()
}

// DiscardPending drops a relaunch candidate after it terminates before
// adoption. It never mutates the bound generation.
func (m *SessionManager) DiscardPending(sess *Session) {
	if m == nil || sess == nil {
		return
	}
	m.mu.Lock()
	m.finishLaunchLocked(sess)
	m.mu.Unlock()
}

// IsLive implements LiveSessionChecker without maintaining a second state
// bit. ACP delegates to acp.Manager; native liveness is the Wrapper.Run
// lifetime observed through Session.runDone.
func (m *SessionManager) IsLive(id string) bool {
	sess, ok := m.Load(id)
	return ok && sess.isLive()
}

// Cancel requests turn-scoped cancellation. ACP delegates to Wrapper's
// manager-backed CancelTurn; native adapters expose no distinct turn cancel.
func (m *SessionManager) Cancel(ctx context.Context, id string) error {
	sess, ok := m.Load(id)
	if !ok {
		return nil
	}
	return m.CancelSession(ctx, sess)
}

// CancelSession requests cancellation against an already-captured exact
// generation. It deliberately does not re-load by ID: an asynchronous user
// stop must never cancel a successor that was installed after dispatch.
func (m *SessionManager) CancelSession(ctx context.Context, sess *Session) error {
	if sess == nil {
		return nil
	}
	return sess.CancelTurn(ctx)
}

// Close cooperatively closes id's wrapper-owned runtime.
func (m *SessionManager) Close(ctx context.Context, id string) error {
	sess, ok := m.Load(id)
	if !ok {
		return nil
	}
	return sess.Stop(ctx)
}

// CloseAdmission establishes the no-new-Boot/no-new-adoption shutdown
// boundary without yet stopping current sessions. It is idempotent.
func (m *SessionManager) CloseAdmission() {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.closed = true
	m.mu.Unlock()
}

// Shutdown atomically closes admission before snapshotting both bound and
// ready-but-not-yet-adopted wrappers. Every de-duplicated wrapper is stopped
// and reaped concurrently under the caller's one shared deadline; return is
// therefore after Wrapper.Run's final event/store/sink tail, not merely after
// Stop acknowledged. A concurrent readiness registration is either included
// or rejected and stopped by Boot.
func (m *SessionManager) Shutdown(ctx context.Context) error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	m.closed = true
	sessions := make(map[*Session]struct{}, len(m.sessions)+len(m.pending))
	for _, sess := range m.sessions {
		if sess != nil {
			sessions[sess] = struct{}{}
		}
	}
	admitted := make([]*Session, 0, len(m.pending))
	for sess := range m.pending {
		if sess != nil {
			sessions[sess] = struct{}{}
			admitted = append(admitted, sess)
		}
	}
	m.mu.Unlock()
	for _, sess := range admitted {
		sess.cancelBoot()
	}

	// Stop and Wait each exact generation concurrently. Sequential Stop calls
	// would let one wedged wrapper consume the shared deadline and starve all
	// later sessions of even a stop attempt.
	results := make(chan error, len(sessions))
	for sess := range sessions {
		sess := sess
		go func() {
			stopErr := sess.Stop(ctx)
			waitErr := sess.Wait(ctx)
			m.CompareAndDelete(sess.ID, sess)
			// Runtime terminal errors were already persisted by Boot's Run tail;
			// preserve Shutdown's prior contract by returning Stop failures and
			// only deadline/cancellation failures from the reap itself.
			if waitErr != nil {
				// The session's own terminal error is lifecycle evidence, not a
				// shutdown failure. Only failure to reap within Shutdown's shared
				// caller deadline changes the result.
				waitErr = ctx.Err()
			}
			results <- errors.Join(stopErr, waitErr)
		}()
	}

	launchesDone := make(chan struct{})
	go func() {
		m.launches.Wait()
		close(launchesDone)
	}()
	var errs []error
	remaining := len(sessions)
	launchesDrained := false
	for remaining > 0 || !launchesDrained {
		select {
		case err := <-results:
			remaining--
			if err != nil {
				errs = append(errs, err)
			}
		case <-launchesDone:
			launchesDrained = true
			launchesDone = nil
		case <-ctx.Done():
			errs = append(errs, ctx.Err())
			return errors.Join(errs...)
		}
	}
	return errors.Join(errs...)
}

// Len returns the number of currently bound wrapper handles.
func (m *SessionManager) Len() int {
	if m == nil {
		return 0
	}
	m.mu.RLock()
	n := len(m.sessions)
	m.mu.RUnlock()
	return n
}

var _ LiveSessionChecker = (*SessionManager)(nil)
