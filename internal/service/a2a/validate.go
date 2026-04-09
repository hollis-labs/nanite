package a2a

import (
	"context"
	"fmt"

	"github.com/hollis-labs/nanite/internal/agent"
	"github.com/hollis-labs/nanite/internal/store"
)

// UserSentinel is the reserved agent_id used to address the human user
// in a session. It short-circuits validation without consulting the
// AgentResolver, so callers may pass a nil resolver when addressing the user.
const UserSentinel = "user"

// AgentResolver resolves an agent ID to a profile. Its sole purpose here is
// to let ValidateAgentID check whether an ID corresponds to a real agent,
// whether that agent is a DB-backed profile or a file-based definition.
//
// The parent package's service.AgentService satisfies this interface
// structurally via its existing Get(ctx, id) method. The interface lives
// here (and not in the parent package) because internal/service/a2a is a
// child package of internal/service, and Go forbids child-to-parent imports.
type AgentResolver interface {
	Get(ctx context.Context, id string) (*store.AgentProfile, error)
}

// ValidateAgentID checks that an agent_id is one of:
//   - the "user" sentinel (always valid, resolver not consulted)
//   - a "file-<slug>" where <slug> references a known file agent
//   - a DB UUID for a known AgentProfile
//
// Returns nil if valid, an error otherwise. The resolver is required for
// everything except the user sentinel; passing nil with any other ID is
// rejected.
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
	if agent.IsFileBasedID(agentID) {
		if _, err := r.Get(ctx, agentID); err != nil {
			return fmt.Errorf("file agent not found: %s", agent.SlugFromFileID(agentID))
		}
		return nil
	}
	// Otherwise assume it's a DB profile ID (UUID).
	if _, err := r.Get(ctx, agentID); err != nil {
		return fmt.Errorf("agent not found: %s", agentID)
	}
	return nil
}
