package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/hollis-labs/nanite/internal/store"
)

// tetherWaker starts a session for an enrolled actor when Tether mail arrives
// and the actor has none (CW-20261024-0018, MVP push). Tether never wakes a
// substrate=nanite actor itself, so Nanite does: it creates a session from the
// actor's host settings exactly as POST /api/agent/v1/sessions would, attaches
// the actor to it, and the bridge then injects the mail, which the existing
// message-wake reactor turns into a turn. Concurrent wakes of one actor
// collapse into one session.
type tetherWaker struct {
	st    *store.Store
	views *CognitiveViews
	enrol *EnrollmentService

	mu     sync.Mutex
	flight map[string]*wakeCall
}

type wakeCall struct {
	done chan struct{}
	sid  string
	err  error
}

func newTetherWaker(st *store.Store, views *CognitiveViews, enrol *EnrollmentService) *tetherWaker {
	return &tetherWaker{st: st, views: views, enrol: enrol, flight: map[string]*wakeCall{}}
}

// Wake returns the actor's session id, starting one when there is none.
func (w *tetherWaker) Wake(ctx context.Context, actorURN string) (string, error) {
	w.mu.Lock()
	if c, ok := w.flight[actorURN]; ok {
		w.mu.Unlock()
		select {
		case <-c.done:
			return c.sid, c.err
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	c := &wakeCall{done: make(chan struct{})}
	w.flight[actorURN] = c
	w.mu.Unlock()

	c.sid, c.err = w.wake(ctx, actorURN)

	w.mu.Lock()
	delete(w.flight, actorURN)
	w.mu.Unlock()
	close(c.done)
	return c.sid, c.err
}

func (w *tetherWaker) wake(ctx context.Context, actorURN string) (string, error) {
	inst, err := w.st.GetDurableAgentInstanceByURN(ctx, actorURN)
	if err != nil {
		return "", fmt.Errorf("tether wake: actor %s: %w", actorURN, err)
	}
	if inst.CurrentSessionID != "" {
		return inst.CurrentSessionID, nil
	}
	hostID, err := w.st.HostSettingsIDForActor(ctx, actorURN)
	if err != nil {
		return "", fmt.Errorf("tether wake: host settings for %s: %w", actorURN, err)
	}
	if hostID == "" {
		return "", fmt.Errorf("tether wake: actor %s is not enrolled from host settings; nothing to start", actorURN)
	}
	host, err := w.st.GetAgentHostSettings(ctx, hostID)
	if err != nil {
		return "", fmt.Errorf("tether wake: load host settings: %w", err)
	}
	if !host.Enabled {
		return "", errors.New("tether wake: host settings are disabled")
	}
	ref := DefinitionRef{DefinitionID: host.DefinitionRef.ID, Revision: host.DefinitionRef.Revision, SemanticDigest: host.DefinitionRef.Digest}
	sess, err := w.views.Create(ctx, CreateDefinedView{
		DefinitionRef: ref,
		HostSettings:  &HostSettingsRef{ID: host.ID, Revision: host.Revision},
		Title:         "Tether: " + host.Title,
		Metadata:      `{"source":"tether_wake"}`,
	})
	if err != nil {
		return "", fmt.Errorf("tether wake: create session: %w", err)
	}
	if w.enrol != nil {
		w.enrol.AttachSession(ctx, host.ID, sess.ID)
	}
	slog.Info("tether wake: started session for enrolled actor", "actor", actorURN, "session_id", sess.ID, "host", host.Slug)
	return sess.ID, nil
}
