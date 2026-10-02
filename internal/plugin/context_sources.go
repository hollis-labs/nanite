package plugin

import (
	"github.com/hollis-labs/nanite/internal/plugin/subprocess"
	"github.com/hollis-labs/nanite/pkg/pluginapi"
)

// ContextSourceRegistrar connects reviewed retrieval declarations to the host's
// context broker. The service layer owns the adapter and its active leases.
type ContextSourceRegistrar interface {
	AddPluginContextSources(string, []pluginapi.ContextSource, pluginapi.ContextScope, *subprocess.SubprocessPlugin) error
	RemovePluginContextSources(string)
}

func (h *Host) SetContextSourceRegistrar(reg ContextSourceRegistrar) {
	h.mu.Lock()
	h.contextSources = reg
	h.mu.Unlock()
}

func (h *Host) removePluginContextSources(id string) {
	h.mu.RLock()
	reg := h.contextSources
	h.mu.RUnlock()
	if reg != nil {
		reg.RemovePluginContextSources(id)
	}
}
