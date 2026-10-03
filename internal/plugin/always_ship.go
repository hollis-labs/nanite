package plugin

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
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
		normalized := NormalizeAlwaysShipTitle(source.Title)
		if normalized == "session documents" {
			return fmt.Errorf("always-ship title conflicts with core Session Documents")
		}
	}
	return nil
}

// NormalizeAlwaysShipTitle matches the tagged contract's title reservation rule.
func NormalizeAlwaysShipTitle(title string) string {
	return strings.Join(strings.FieldsFunc(strings.ToLower(title), func(r rune) bool { return r == ' ' || r == '.' || r == '_' || r == '-' }), " ")
}

const MaxAlwaysShipOwners = 4

// CheckAlwaysShipAdmission shares install/runtime capacity and title checks.
// Reviewing an update replaces this owner's declarations; runtime add separately
// refuses duplicate owners. Installed owners reserve capacity before activation.
func CheckAlwaysShipAdmission(owner string, declarations []pluginapi.AlwaysShipSource, existing map[string][]pluginapi.AlwaysShipSource) error {
	if len(declarations) == 0 {
		return nil
	}
	owners, count := 0, len(declarations)
	for other, sources := range existing {
		if other == owner || len(sources) == 0 {
			continue
		}
		owners++
		count += len(sources)
		for _, claimed := range sources {
			for _, proposed := range declarations {
				if NormalizeAlwaysShipTitle(claimed.Title) == NormalizeAlwaysShipTitle(proposed.Title) {
					slog.Warn("always-ship title already registered", "owner", owner, "existing_owner", other, "title", proposed.Title)
					return fmt.Errorf("always-ship title %q already owned by %s", proposed.Title, other)
				}
			}
		}
	}
	if owners >= MaxAlwaysShipOwners || count > pluginapi.MaxAlwaysShipSources {
		return fmt.Errorf("always-ship admission requires capacity: at most four owners and four sources")
	}
	return nil
}

// CheckAlwaysShipInstall reserves capacity against accepted installed bundles
// for CLI and API alike, before the operator is asked to approve or bytes commit.
// Updates replace their own reservation; unapproved directories grant no lease.
func CheckAlwaysShipInstall(ctx context.Context, root, owner string, declarations []pluginapi.AlwaysShipSource) error {
	if len(declarations) == 0 {
		return nil
	}
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	existing := map[string][]pluginapi.AlwaysShipSource{}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !entry.IsDir() || entry.Name() == owner || ValidatePluginID(entry.Name()) != nil {
			continue
		}
		accepted, err := ReadInstallApproval(root, entry.Name())
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		hasClass := false
		for _, request := range accepted.Review.Capabilities {
			if request.Name == pluginapi.CapabilityContextAlwaysShip {
				hasClass = true
			}
		}
		if !hasClass {
			continue
		}
		declared, err := ParseManifest(filepath.Join(root, entry.Name(), "plugin.yaml"))
		if err != nil {
			return err
		}
		if declared.ID != entry.Name() || declared.Shared == nil {
			return fmt.Errorf("always-ship installed reservation identity differs")
		}
		block, err := pluginapi.DecodeBlock(declared.Shared.Nanite)
		if err != nil {
			return err
		}
		existing[declared.ID] = block.Registers.AlwaysShipSources
	}
	return CheckAlwaysShipAdmission(owner, declarations, existing)
}
