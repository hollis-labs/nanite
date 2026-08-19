package messaging

import (
	"context"
	"fmt"

	"github.com/hollis-labs/nanite/internal/store"
)

// UserSentinel is the reserved agent_id used to address the human user
// in a session. It short-circuits validation without consulting the
// AgentResolver, so callers may pass a nil resolver when addressing
// the user.
const UserSentinel = "user"

// AgentResolver resolves an agent ID to a profile. Its sole purpose
// here is to let ValidateAgentID check whether an ID corresponds to a
// real, DB-backed agent_profiles row.
//
// The parent package's service.AgentService satisfies this interface
// structurally via its existing Get(ctx, id) method. The interface
// lives here (and not in the parent service package) because
// internal/messaging cannot import internal/service (import cycle).
type AgentResolver interface {
	Get(ctx context.Context, id string) (*store.AgentProfile, error)
}

// AgentRegistrar optionally auto-registers an unknown sender on first
// messaging call (T6). Implementations insert a minimal
// agent_profiles row with the specified kind (`external` or `cli`)
// and sensible defaults for everything else. A nil registrar on the
// Service disables auto-register — SendMessage then rejects unknown
// from_agent_ids through ValidateAgentID as before.
//
// *store.Store satisfies this structurally via its existing
// CreateAgent method.
type AgentRegistrar interface {
	CreateAgent(a *store.AgentProfile) error
}

// ValidateAgentID checks that an agent_id is one of:
//   - the UserSentinel (always valid, resolver not consulted)
//   - a DB UUID for a known AgentProfile
//
// TASKS/adhoc/01-eliminate-file-based-agent-runtime.md removed the
// "file-<slug>" synthetic-ID branch this function used to special-case --
// every real agent ID is a DB-backed agent_profiles row now, so a single
// resolver lookup covers every non-sentinel case.
//
// Returns nil if valid, an error otherwise. The resolver is required
// for everything except the user sentinel; passing nil with any other
// ID is rejected.
func ValidateAgentID(ctx context.Context, r AgentResolver, agentID string) error {
	if agentID == "" {
		return fmt.Errorf("empty agent id")
	}
	if agentID == UserSentinel {
		return nil
	}
	if r == nil {
		return fmt.Errorf("cannot validate agent id %q without resolver", agentID)
	}
	if _, err := r.Get(ctx, agentID); err != nil {
		return fmt.Errorf("agent not found: %s", agentID)
	}
	return nil
}
