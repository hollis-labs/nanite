package dispatch

import (
	"context"
	"errors"
)

// TrustTier classifies a role's approval-bypass authority within a workspace.
// Per CW-20260421-0014 H1 Decision Log:
//   - untrusted: dispatch refused entirely
//   - normal:    dispatch allowed but approval required (existing flow)
//   - trusted:   dispatch allowed without approval; audit-logged
type TrustTier string

const (
	TrustUntrusted TrustTier = "untrusted"
	TrustNormal    TrustTier = "normal"
	TrustTrusted   TrustTier = "trusted"
)

// IsValid reports whether t is a known tier.
func (t TrustTier) IsValid() bool {
	switch t {
	case TrustUntrusted, TrustNormal, TrustTrusted:
		return true
	}
	return false
}

// TrustResolver resolves the effective trust tier for a (workspace, agent)
// pair. Implementations consult the workspace_role_trust override table
// and fall back to agent_profiles.default_trust_tier.
type TrustResolver interface {
	ResolveTrust(ctx context.Context, workspaceID, agentProfileID string) (TrustTier, error)
}

// ErrUntrustedRole is returned by dispatch when a role's resolved trust is
// `untrusted`. Callers must NOT bypass — `untrusted` means refuse the call,
// not "fall back to approval prompt."
var ErrUntrustedRole = errors.New("dispatch: role is untrusted in this workspace")
