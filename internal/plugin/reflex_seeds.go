package plugin

import "github.com/hollis-labs/nanite/pkg/pluginapi"

// ReflexSeedRegistrar retains durable defaults separately from active plugin
// eligibility. Activation follows all other successful registrations.
type ReflexSeedRegistrar interface {
	PreparePluginReflexSeeds(string, []pluginapi.ReflexSeed, pluginapi.ReflexScope) error
	ActivatePluginReflexSeeds(string) error
	RemovePluginReflexSeeds(string)
}

func (h *Host) SetReflexSeedRegistrar(reg ReflexSeedRegistrar) {
	h.mu.Lock()
	h.reflexSeeds = reg
	h.mu.Unlock()
}
func (h *Host) removePluginReflexSeeds(owner string) {
	h.mu.RLock()
	reg := h.reflexSeeds
	h.mu.RUnlock()
	if reg != nil {
		reg.RemovePluginReflexSeeds(owner)
	}
}
