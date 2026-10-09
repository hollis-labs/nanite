package service

import (
	"log/slog"

	fplugin "github.com/hollis-labs/libs/plugin-mcp/plugin-sdk"
	"github.com/hollis-labs/nanite/internal/plugin"
	"github.com/hollis-labs/nanite/internal/store"
)

// PluginCleanupService supplies store-backed uninstall cleanup to the
// management transport, including the already-disabled plugin path.
type PluginCleanupService struct{ store *store.Store }

func NewPluginCleanupService(st *store.Store) *PluginCleanupService {
	return &PluginCleanupService{store: st}
}
func (s *PluginCleanupService) Uninstall(manifestPath string) {
	manifest, err := plugin.ParseManifest(manifestPath)
	if err != nil {
		return
	}
	if constructor, ok := plugin.LookupConstructor(manifest.Name); ok {
		if u, ok := constructor().(fplugin.Uninstallable); ok {
			if uninstallErr := u.Uninstall(plugin.NewHostWithStore(s.store)); uninstallErr != nil {
				slog.Warn("plugin-api: uninstall cleanup failed", "name", manifest.Name, "err", uninstallErr)
			}
		}
	}
	plugin.NewHostWithStore(s.store).SweepPluginAgentProfiles(manifest.Identifier())
}
