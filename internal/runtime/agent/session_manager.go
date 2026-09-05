package agent

import (
	"context"
	"errors"
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
type SessionManager struct {
	sessions sync.Map // runtime ID (string) -> *Session
	acp      *acp.Manager
}

// NewSessionManager constructs an empty binding registry with one shared ACP
// manager. Every ACP Wrapper created by Boot receives this manager.
func NewSessionManager() *SessionManager {
	return &SessionManager{acp: acp.NewManager()}
}

// ACPManager returns the wrapper ACP lifecycle manager shared by all sessions.
func (m *SessionManager) ACPManager() *acp.Manager {
	if m == nil {
		return nil
	}
	return m.acp
}

// Store binds id to sess. Recovery replacement intentionally has replacement
// semantics; the exiting predecessor uses CompareAndDelete and therefore
// cannot unregister the newly stored handle.
func (m *SessionManager) Store(id string, sess *Session) {
	if m == nil || id == "" || sess == nil {
		return
	}
	m.sessions.Store(id, sess)
}

// Swap binds sess and returns the prior handle, if any.
func (m *SessionManager) Swap(id string, sess *Session) (*Session, bool) {
	if m == nil || id == "" || sess == nil {
		return nil, false
	}
	previous, loaded := m.sessions.Swap(id, sess)
	if !loaded {
		return nil, false
	}
	prior, ok := previous.(*Session)
	return prior, ok
}

// Load returns the wrapper handle bound to id.
func (m *SessionManager) Load(id string) (*Session, bool) {
	if m == nil {
		return nil, false
	}
	v, ok := m.sessions.Load(id)
	if !ok {
		return nil, false
	}
	sess, ok := v.(*Session)
	return sess, ok
}

// Delete removes any binding for id.
func (m *SessionManager) Delete(id string) {
	if m != nil {
		m.sessions.Delete(id)
	}
}

// LoadAndDelete atomically removes and returns id's current binding.
func (m *SessionManager) LoadAndDelete(id string) (*Session, bool) {
	if m == nil {
		return nil, false
	}
	v, ok := m.sessions.LoadAndDelete(id)
	if !ok {
		return nil, false
	}
	sess, ok := v.(*Session)
	return sess, ok
}

// CompareAndDelete removes id only when it still names expected.
func (m *SessionManager) CompareAndDelete(id string, expected *Session) bool {
	return m != nil && m.sessions.CompareAndDelete(id, expected)
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

// Shutdown closes a stable snapshot of all currently bound wrappers.
func (m *SessionManager) Shutdown(ctx context.Context) error {
	if m == nil {
		return nil
	}
	var sessions []*Session
	m.sessions.Range(func(_, value any) bool {
		if sess, ok := value.(*Session); ok && sess != nil {
			sessions = append(sessions, sess)
		}
		return true
	})
	var errs []error
	for _, sess := range sessions {
		if err := sess.Stop(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Len returns the number of currently bound wrapper handles.
func (m *SessionManager) Len() int {
	if m == nil {
		return 0
	}
	n := 0
	m.sessions.Range(func(_, _ any) bool {
		n++
		return true
	})
	return n
}

var _ LiveSessionChecker = (*SessionManager)(nil)
