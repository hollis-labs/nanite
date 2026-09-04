// Package assets bundles the Nanite agent framework content (roles, skills,
// commands, docs, templates, vendor files, VERSION) into the Nanite binary
// via go:embed and exposes accessors used by the install pipeline.
package assets

//go:generate go run ./cmd/frameworkgen -source framework -manifest manifests/current.json -tesseract-doc framework/docs/tesseract-v0.9-contract.md -retired docs/ref-conduit-plugin.md -legacy-allow docs/archive/cleanup-tasks.md,docs/tesseract-v0.9-contract.md

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/hollis-labs/nanite/internal/fsutil"
	"github.com/hollis-labs/nanite/internal/pathsafe"
)

//go:embed all:framework manifests/*.json
var frameworkAssets embed.FS

// Version returns the framework content version string from framework/VERSION.
// This is the version of the embedded content set, separate from the Nanite
// binary version.
func Version() string {
	data, err := frameworkAssets.ReadFile("framework/VERSION")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// File reads a single embedded file by its path relative to the framework root
// (e.g., "VERSION", "roles/domain/backend.md").
func File(path string) ([]byte, error) {
	return frameworkAssets.ReadFile("framework/" + path)
}

// ExtractOptions controls ExtractTo behavior.
type ExtractOptions struct {
	// Force overwrites existing files even if they differ from the embedded content.
	Force bool
}

// ExtractReport summarizes what ExtractTo did.
type ExtractReport struct {
	Created       int      // files newly created
	Updated       int      // historical stock files upgraded to current content
	Removed       int      // retired historical stock paths removed
	Unchanged     int      // files that already matched the embedded content
	Skipped       int      // user-modified current or retired files preserved
	Forced        int      // files overwritten or retired paths removed because Force=true
	SkippedFiles  []string // paths of skipped files (relative to targetDir)
	RemovedFiles  []string // retired paths removed (relative to targetDir)
	ConflictFiles []string // deterministic comparison snapshots (relative to targetDir)
}

// ExtractTo extracts the embedded framework tree into targetDir.
// Per-file behavior:
//   - file doesn't exist: create it
//   - file exists and bytes match embedded: no-op
//   - file matches a known historical manifest: upgrade it
//   - file differs from every known shipped version: preserve it and write
//     deterministic comparison snapshots unless Force is true
//   - retired historical stock paths are removed; customized retired paths are
//     preserved unless Force is true
func ExtractTo(targetDir string, opts ExtractOptions) (*ExtractReport, error) {
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir target: %w", err)
	}

	manifests, err := loadManifestSet()
	if err != nil {
		return nil, err
	}
	report := &ExtractReport{}
	for _, rel := range manifests.current.RetiredPaths {
		retireErr := retirePath(targetDir, rel, manifests, opts, report)
		if retireErr != nil {
			return nil, retireErr
		}
	}

	err = fs.WalkDir(frameworkAssets, "framework", func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		// Trim the "framework/" prefix to get the relative path.
		rel := strings.TrimPrefix(path, "framework")
		rel = strings.TrimPrefix(rel, "/")
		if rel == "" {
			return nil
		}
		target, resolveErr := pathsafe.ResolveUnder(targetDir, rel)
		if resolveErr != nil {
			return fmt.Errorf("resolve embedded asset %s: %w", rel, resolveErr)
		}

		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}

		embedded, readEmbedErr := frameworkAssets.ReadFile(path)
		if readEmbedErr != nil {
			return fmt.Errorf("read embed %s: %w", path, readEmbedErr)
		}

		info, statErr := os.Lstat(target)
		if errors.Is(statErr, fs.ErrNotExist) {
			if writeErr := fsutil.AtomicWriteFile(target, embedded, 0o644); writeErr != nil {
				return fmt.Errorf("write %s: %w", target, writeErr)
			}
			report.Created++
			return nil
		}
		if statErr != nil {
			return fmt.Errorf("inspect %s: %w", target, statErr)
		}
		if !info.Mode().IsRegular() {
			if opts.Force {
				// #nosec G122 -- target was confined beneath targetDir immediately before this callback operation.
				removeErr := os.RemoveAll(target)
				if removeErr != nil {
					return fmt.Errorf("remove non-regular asset %s: %w", target, removeErr)
				}
				writeErr := fsutil.AtomicWriteFile(target, embedded, 0o644)
				if writeErr != nil {
					return fmt.Errorf("write %s: %w", target, writeErr)
				}
				report.Forced++
				return nil
			}
			report.Skipped++
			report.SkippedFiles = append(report.SkippedFiles, rel)
			return nil
		}

		// #nosec G304 G122 -- target was confined beneath targetDir immediately before this callback operation.
		existing, readErr := os.ReadFile(target)
		if readErr != nil {
			return fmt.Errorf("read %s: %w", target, readErr)
		}

		if bytes.Equal(existing, embedded) {
			report.Unchanged++
			return nil
		}

		if opts.Force {
			if writeErr := fsutil.AtomicWriteFile(target, embedded, 0o644); writeErr != nil {
				return fmt.Errorf("write %s: %w", target, writeErr)
			}
			report.Forced++
			return nil
		}
		if manifests.isHistoricalStock(rel, existing) {
			if writeErr := fsutil.AtomicWriteFile(target, embedded, 0o644); writeErr != nil {
				return fmt.Errorf("upgrade historical asset %s: %w", target, writeErr)
			}
			report.Updated++
			return nil
		}

		report.Skipped++
		report.SkippedFiles = append(report.SkippedFiles, rel)
		conflicts, conflictErr := writeConflictSnapshots(targetDir, rel, existing, embedded)
		if conflictErr != nil {
			return conflictErr
		}
		report.ConflictFiles = append(report.ConflictFiles, conflicts...)
		return nil
	})

	return report, err
}

func retirePath(targetDir, rel string, manifests manifestSet, opts ExtractOptions, report *ExtractReport) error {
	target, resolveErr := pathsafe.ResolveUnder(targetDir, filepath.FromSlash(rel))
	if resolveErr != nil {
		return fmt.Errorf("resolve retired asset %s: %w", rel, resolveErr)
	}
	info, err := os.Lstat(target)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect retired asset %s: %w", target, err)
	}

	var existing []byte
	if info.Mode().IsRegular() {
		// #nosec G304 -- target was confined beneath targetDir by ResolveUnder above.
		existing, err = os.ReadFile(target)
		if err != nil {
			return fmt.Errorf("read retired asset %s: %w", target, err)
		}
	}
	if opts.Force || (info.Mode().IsRegular() && manifests.isHistoricalStock(rel, existing)) {
		if err := os.RemoveAll(target); err != nil {
			return fmt.Errorf("remove retired asset %s: %w", target, err)
		}
		if opts.Force {
			report.Forced++
		} else {
			report.Removed++
		}
		report.RemovedFiles = append(report.RemovedFiles, rel)
		return nil
	}

	report.Skipped++
	report.SkippedFiles = append(report.SkippedFiles, rel)
	if info.Mode().IsRegular() {
		retiredMarker := []byte("Retired by Nanite framework " + Version() + "; preserve only after reviewing the replacement asset.\n")
		conflicts, err := writeConflictSnapshots(targetDir, rel, existing, retiredMarker)
		if err != nil {
			return err
		}
		report.ConflictFiles = append(report.ConflictFiles, conflicts...)
	}
	return nil
}

func writeConflictSnapshots(targetDir, rel string, existing, replacement []byte) ([]string, error) {
	base := filepath.Join(".framework-conflicts", Version(), filepath.FromSlash(rel))
	existingRel := base + "." + contentDigest(existing)[:12] + ".existing"
	replacementRel := base + "." + contentDigest(replacement)[:12] + ".new"
	for _, snapshot := range []struct {
		rel  string
		data []byte
	}{
		{rel: existingRel, data: existing},
		{rel: replacementRel, data: replacement},
	} {
		path, resolveErr := pathsafe.ResolveUnder(targetDir, snapshot.rel)
		if resolveErr != nil {
			return nil, fmt.Errorf("resolve conflict snapshot for %s: %w", rel, resolveErr)
		}
		// #nosec G301 -- framework comparison files must remain user-readable.
		if mkdirErr := os.MkdirAll(filepath.Dir(path), 0o755); mkdirErr != nil {
			return nil, fmt.Errorf("create conflict directory for %s: %w", rel, mkdirErr)
		}
		// #nosec G304 -- path was confined beneath targetDir by ResolveUnder above.
		got, err := os.ReadFile(path)
		switch {
		case err == nil && bytes.Equal(got, snapshot.data):
			continue
		case err == nil:
			return nil, fmt.Errorf("conflict snapshot %s was modified; refusing to overwrite it", path)
		case !errors.Is(err, fs.ErrNotExist):
			return nil, fmt.Errorf("read conflict snapshot %s: %w", path, err)
		}
		if writeErr := fsutil.AtomicWriteFile(path, snapshot.data, 0o644); writeErr != nil {
			return nil, fmt.Errorf("write conflict snapshot %s: %w", path, writeErr)
		}
	}
	return []string{filepath.ToSlash(existingRel), filepath.ToSlash(replacementRel)}, nil
}
