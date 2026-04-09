// Package assets bundles the agentrc framework content (roles, skills,
// commands, docs, templates, vendor files, VERSION) into the Nanite binary
// via go:embed and exposes accessors used by the install pipeline.
package assets

import (
	"bytes"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

//go:embed all:framework
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
	Created      int      // files newly created
	Unchanged    int      // files that already matched the embedded content
	Skipped      int      // files that differ from embedded (user-modified) and were preserved
	Forced       int      // files overwritten because Force=true
	SkippedFiles []string // paths of skipped files (relative to targetDir)
}

// ExtractTo extracts the embedded framework tree into targetDir.
// Per-file behavior:
//   - file doesn't exist: create it
//   - file exists and bytes match embedded: no-op
//   - file exists and bytes differ: skip (preserve user modifications) unless Force is true
func ExtractTo(targetDir string, opts ExtractOptions) (*ExtractReport, error) {
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir target: %w", err)
	}

	report := &ExtractReport{}
	err := fs.WalkDir(frameworkAssets, "framework", func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		// Trim the "framework/" prefix to get the relative path.
		rel := strings.TrimPrefix(path, "framework")
		rel = strings.TrimPrefix(rel, "/")
		if rel == "" {
			return nil
		}
		target := filepath.Join(targetDir, rel)

		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}

		embedded, err := frameworkAssets.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read embed %s: %w", path, err)
		}

		existing, statErr := os.ReadFile(target)
		if os.IsNotExist(statErr) {
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(target, embedded, 0o644); err != nil {
				return err
			}
			report.Created++
			return nil
		}
		if statErr != nil {
			return fmt.Errorf("stat %s: %w", target, statErr)
		}

		if bytes.Equal(existing, embedded) {
			report.Unchanged++
			return nil
		}

		if opts.Force {
			if err := os.WriteFile(target, embedded, 0o644); err != nil {
				return err
			}
			report.Forced++
			return nil
		}

		report.Skipped++
		report.SkippedFiles = append(report.SkippedFiles, rel)
		return nil
	})

	if err != nil {
		return report, err
	}
	return report, nil
}
