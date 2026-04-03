package broker

// layerExplicit is Layer 1: deterministic, zero-cost.
// Resolves when the caller (or active skill/agent config) specifies tools directly.
func (b *Broker) layerExplicit(signals IntentSignals) *Selection {
	// Priority 1: Caller specifies tools directly.
	if len(signals.ExplicitTools) > 0 {
		tools := b.registry.GetByNames(signals.ExplicitTools)
		if len(tools) > 0 {
			return &Selection{
				Tools:        tools,
				ToolNames:    toolNames(tools),
				LayerReached: "explicit",
				Intent:       "caller-specified",
			}
		}
	}

	// Priority 2: Active skill declares tool bindings.
	if len(signals.SkillTools) > 0 {
		tools := b.registry.GetByNames(signals.SkillTools)
		if len(tools) > 0 {
			return &Selection{
				Tools:        tools,
				ToolNames:    toolNames(tools),
				LayerReached: "explicit",
				Intent:       "skill-binding",
			}
		}
	}

	// Priority 3: Agent config declares tool set.
	if len(signals.AgentToolSet) > 0 {
		tools := b.registry.GetByNames(signals.AgentToolSet)
		if len(tools) > 0 {
			return &Selection{
				Tools:        tools,
				ToolNames:    toolNames(tools),
				LayerReached: "explicit",
				Intent:       "agent-config",
			}
		}
	}

	return nil // pass through to Layer 2
}
