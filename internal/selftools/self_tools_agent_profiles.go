package selftools

import (
	"context"
	"fmt"

	"github.com/hollis-labs/nanite/internal/agent"
	"github.com/hollis-labs/nanite/internal/store"
)

// AgentProfileStore is the exact persistence surface used by agent profile
// management and source resolution.
type AgentProfileStore interface {
	GetAgent(ctx context.Context, id string) (*store.AgentProfile, error)
	GetAgentBySlug(ctx context.Context, slug string) (*store.AgentProfile, error)
	ListAgents(ctx context.Context) ([]store.AgentProfile, error)
	CreateAgent(ctx context.Context, profile *store.AgentProfile) error
	UpdateAgent(ctx context.Context, profile *store.AgentProfile) error
}

// AgentClassifier resolves the management class of an agent profile. It is a
// narrow local seam because internal/service already imports internal/mcp.
type AgentClassifier interface {
	Classify(profile *store.AgentProfile) agent.ManageClass
}

// AgentProfileTools owns profile CRUD and provenance-based editability policy.
type AgentProfileTools struct {
	Store      AgentProfileStore
	Classifier AgentClassifier
}

func NewAgentProfileTools(store AgentProfileStore, classifier AgentClassifier) *AgentProfileTools {
	return &AgentProfileTools{Store: store, Classifier: classifier}
}

func (at *AgentProfileTools) classifyAgent(profile *store.AgentProfile) agent.ManageClass {
	if at != nil && at.Classifier != nil {
		return at.Classifier.Classify(profile)
	}
	var fallback agent.Classification
	return fallback.Classify(profile.Source)
}

// agentNotEditableError mirrors the REST-layer editability rejection shape.
func agentNotEditableError(slug string, class agent.ManageClass) string {
	msg := "agent is not a writable managed config"
	switch class {
	case agent.ManageClassInternal:
		msg = "agent is an embedded internal harness profile and is managed by Nanite, not editable here"
	case agent.ManageClassPlugin:
		msg = "agent is plugin/vendor-provided (read-only); copy it to the managed layer to edit"
	case agent.ManageClassExternal:
		msg = "agent has external/imported provenance (read-only); copy it to the managed layer to edit"
	}
	return fmt.Sprintf("%s (slug=%q, manage_class=%s)", msg, slug, string(class))
}
