package service

import (
	"context"

	"github.com/hollis-labs/nanite/internal/store"
)

// nativeForkVerifier implements store.CodeModeForkVerifier for native views. It
// authenticates the execution capability in ctx and never the string it is
// handed: the id argument is only checked for consistency with what the
// capability itself says. Fork creation passes the parent view id and history
// passes the fork view id, so both are accepted only from the holder whose
// capability is for that very view.
type nativeForkVerifier struct {
	limits store.CodeModeContextLimits
}

func newNativeForkVerifier(limits store.CodeModeContextLimits) *nativeForkVerifier {
	return &nativeForkVerifier{limits: limits}
}

var _ store.CodeModeForkVerifier = (*nativeForkVerifier)(nil)

func (v *nativeForkVerifier) VerifyCaller(ctx context.Context, viewID string) (store.CodeModeContextLimits, error) {
	c, ok := executionCapabilityFrom(ctx)
	if !ok || viewID == "" || viewID != c.subjectViewID {
		return store.CodeModeContextLimits{}, store.ErrVerifiedActorRequired
	}
	return v.limits, nil
}

// VerifyFork: only a root capability may fork, and only its own view.
func (v *nativeForkVerifier) VerifyFork(ctx context.Context, parent store.CodeModeParentSnapshot, _ store.CodeModeForkRequest) error {
	c, ok := executionCapabilityFrom(ctx)
	if !ok || parent.ParentViewID != c.subjectViewID {
		return store.ErrVerifiedActorRequired
	}
	if c.parentViewID != "" {
		return store.ErrCodeModeNested
	}
	return nil
}

// VerifyHistory: only the child's own capability, and only for the parent view
// it was forked from. The view relationship is compared; the parent capability
// identity plays no part in it (R2).
func (v *nativeForkVerifier) VerifyHistory(ctx context.Context, snap store.CodeModeHistorySnapshot, _ store.CodeModeHistoryRequest) error {
	c, ok := executionCapabilityFrom(ctx)
	if !ok || snap.ForkViewID != c.subjectViewID {
		return store.ErrVerifiedActorRequired
	}
	if c.parentViewID == "" || c.parentViewID != snap.Parent.ParentViewID {
		return store.ErrVerifiedActorRequired
	}
	return nil
}
