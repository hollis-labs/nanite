package install

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/hollis-labs/nanite/internal/agent"
)

// CleanupReport describes what happened to one adapter's target file
// during cleanupRemovedAdapters. Returned to the caller (install command)
// so it can print user-facing notices.
type CleanupReport struct {
	Adapter  string // adapter slug, e.g., "claude"
	FilePath string // absolute path of the file that was modified
	Action   string // "stripped" (managed section removed, file kept) or "deleted" (file removed entirely)
}

// cleanupRemovedAdapters strips the Nanite-managed section from each
// removed adapter's target file in projectDir. If a file is empty after
// the strip (was managed-section-only), the file is deleted.
//
// Adapters not in the evidence map (e.g., "frobnicate") are silently
// ignored — slug validation happens upstream in ResolveAdapters.
//
// Returns one CleanupReport per adapter that had visible work done.
// Adapters whose target file was already missing are skipped without a
// report.
//
// All errors are I/O errors and propagate immediately. Snapshot for
// rollback is handled by the existing snapshotAdapterTargets() pass that
// runs before cleanup.
func cleanupRemovedAdapters(projectDir string, removed []string) ([]CleanupReport, error) {
	var reports []CleanupReport
	for _, slug := range removed {
		evidence, ok := adapterEvidence[slug]
		if !ok {
			continue
		}
		path := filepath.Join(projectDir, evidence.rootFile)
		stripped, becameEmpty, err := agent.RemoveManagedSection(path)
		if err != nil {
			return reports, fmt.Errorf("cleanup %s: %w", slug, err)
		}
		if !stripped {
			continue
		}
		report := CleanupReport{Adapter: slug, FilePath: path, Action: "stripped"}
		if becameEmpty {
			if err := os.Remove(path); err != nil {
				return reports, fmt.Errorf("delete empty %s: %w", path, err)
			}
			report.Action = "deleted"
		}
		reports = append(reports, report)
	}
	return reports, nil
}
