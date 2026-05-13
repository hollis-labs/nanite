// Package builtin embeds the internal Nanite agent profiles as the file
// source-of-truth for boot-time sync into the agent_profiles table.
//
// CW-20260512-0111 (SP-20260512-0009 Wave 1).
//
// The internal profiles live at internal/agent/builtin/profiles/*.md. At
// container startup the service layer (internal/service/container.go) calls
// InternalProfiles() and appends the resulting definitions to the discovery
// pass; AutoIngestAgents then upserts each definition by slug. Because the
// upsert preserves the existing row's ID and rewrites the system_prompt,
// description, and other content fields, editing a file under profiles/
// followed by a Nanite restart is equivalent to "replace the row".
//
// Provenance: every internal profile is stamped with Source="internal" so
// that downstream consumers (UI, broker, future Wave-2 cleanup migration)
// can identify the file-sourced rows. The legacy "builtin" source value is
// no longer used by this package.
package builtin

import (
	"embed"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	"github.com/hollis-labs/nanite/internal/agent"
)

// SourceInternal is the provenance marker stamped on every file-sourced
// internal agent profile. The Wave 2 cleanup migration (CW-20260512-0112)
// deletes rows that do NOT carry this value, so boot-time sync MUST set it
// before the cleanup migration runs.
const SourceInternal = "internal"

//go:embed profiles/*.md
var internalProfilesFS embed.FS

// InternalProfiles returns the parsed internal agent definitions embedded
// at build time from internal/agent/builtin/profiles/*.md. The slice is
// returned in deterministic slug order so the boot-time ingestion log and
// any downstream snapshots are stable across runs.
//
// Each Definition carries:
//   - Source    = "internal"  (Wave 2 cleanup keys off this value)
//   - SourceRef = "embedded:profiles/<slug>.md"
//
// Parse failures are returned eagerly — an unparseable internal profile is
// a build-level bug and must not be swallowed at boot.
func InternalProfiles() ([]*agent.Definition, error) {
	entries, err := fs.ReadDir(internalProfilesFS, "profiles")
	if err != nil {
		return nil, fmt.Errorf("builtin: read profiles dir: %w", err)
	}

	defs := make([]*agent.Definition, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".md") {
			continue
		}
		rel := path.Join("profiles", name)
		data, err := internalProfilesFS.ReadFile(rel)
		if err != nil {
			return nil, fmt.Errorf("builtin: read %s: %w", rel, err)
		}
		def, err := agent.ParseMD(data)
		if err != nil {
			return nil, fmt.Errorf("builtin: parse %s: %w", rel, err)
		}
		if def.Slug == "" {
			// Fallback for completeness — parser already enforces
			// frontmatter slug, but a defensive normalization keeps
			// the contract loud if a future file omits it.
			def.Slug = strings.TrimSuffix(name, ".md")
		}
		def.Source = SourceInternal
		def.SourceRef = "embedded:" + rel
		defs = append(defs, def)
	}

	sort.Slice(defs, func(i, j int) bool { return defs[i].Slug < defs[j].Slug })
	return defs, nil
}

// InternalProfileSlugs returns the slugs of the embedded internal profiles
// in deterministic order. Useful for tests, migrations, and the Wave 2
// cleanup migration which needs to assert these slugs survive the wipe.
func InternalProfileSlugs() ([]string, error) {
	defs, err := InternalProfiles()
	if err != nil {
		return nil, err
	}
	slugs := make([]string, len(defs))
	for i, def := range defs {
		slugs[i] = def.Slug
	}
	return slugs, nil
}
