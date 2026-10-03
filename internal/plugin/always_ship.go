package plugin

import (
	"context"
	"fmt"
	"strings"

	"github.com/hollis-labs/nanite/internal/plugin/subprocess"
	"github.com/hollis-labs/nanite/pkg/pluginapi"
)

// AlwaysShipRegistrar owns accepted persistent-context leases outside the broker.
// The approval check pins each lease to the reviewed bundle on every turn.
type AlwaysShipRegistrar interface {
	AddPluginAlwaysShipSources(string, []pluginapi.AlwaysShipSource, pluginapi.AlwaysShipScope, *subprocess.SubprocessPlugin, func(context.Context) error) error
	RemovePluginAlwaysShipSources(string)
}

func (h *Host) SetAlwaysShipRegistrar(reg AlwaysShipRegistrar) {
	h.mu.Lock()
	h.alwaysShip = reg
	h.mu.Unlock()
}

func (h *Host) removePluginAlwaysShipSources(id string) {
	h.mu.RLock()
	reg := h.alwaysShip
	h.mu.RUnlock()
	if reg != nil {
		reg.RemovePluginAlwaysShipSources(id)
	}
}

// CheckAlwaysShipCoreTitles protects headings still owned by the core builder.
// Documents adoption must remove this reservation with its core renderer.
func CheckAlwaysShipCoreTitles(sources []pluginapi.AlwaysShipSource) error {
	for _, source := range sources {
		normalized := strings.Join(strings.FieldsFunc(strings.ToLower(source.Title), func(r rune) bool { return r == ' ' || r == '.' || r == '_' || r == '-' }), " ")
		if normalized == "session documents" {
			return fmt.Errorf("always-ship title conflicts with core Session Documents")
		}
	}
	return nil
}
