package agent

import (
	"path/filepath"
	"strings"
)

// ManageClass classifies an agent profile by who owns its config and whether
// the GUI/API/CLI may manage it in place. It is the single source of truth
// for the file-backed editability decision: the API gates writes on it and
// the frontend gates affordances on it.
type ManageClass string

const (
	// ManageClassManaged is an operator-owned file under a writable managed
	// root (project .nanite/agents or the user nanite data dir). Fully
	// editable in place: GUI/API/CLI may CRUD the profile, reflexes, skills,
	// boot plan, scope, etc. Writes flow through the shared AgentConfigService.
	ManageClassManaged ManageClass = "managed"

	// ManageClassInternal is an embedded harness primitive (source=internal),
	// baked into the binary at build time. Read-only and hidden from the
	// management surface; the harness owns it. Not exposed for editing or
	// copy in the GUI.
	ManageClassInternal ManageClass = "internal"

	// ManageClassPlugin is a plugin/vendor-provided agent (source=plugin).
	// Read-only in place; the GUI offers "copy to managed" to fork an
	// editable copy into the managed layer.
	ManageClassPlugin ManageClass = "plugin"

	// ManageClassExternal is a file-backed agent whose source/path is not a
	// recognized writable managed root (e.g. an adapter-discovered file in a
	// read-only ecosystem location). Read-only in place; offers copy to managed.
	ManageClassExternal ManageClass = "external"
)

// Editable reports whether a class may be edited/deleted in place.
func (c ManageClass) Editable() bool { return c == ManageClassManaged }

// CopyToManagedAllowed reports whether a read-only class should surface a
// "make editable / copy to managed" affordance rather than dead-ending.
// Internal harness primitives are deliberately excluded — they are not user
// content and stay hidden from the management surface.
func (c ManageClass) CopyToManagedAllowed() bool {
	return c == ManageClassPlugin || c == ManageClassExternal
}

// Classification holds the writable managed roots used to decide whether a
// file-backed agent lives somewhere the GUI/API may write in place.
type Classification struct {
	// ManagedRoots are absolute directory paths that hold writable managed
	// agent files (e.g. <ManagedConfigRoot>/agents and <userDataDir>/agents).
	ManagedRoots []string
}

// NewClassification builds a Classification from the project managed config
// root and the user nanite data dir (home). Either may be empty. The
// "agents" subdir is appended to each root.
func NewClassification(managedConfigRoot, userDataDir string) Classification {
	var roots []string
	add := func(base string) {
		if strings.TrimSpace(base) == "" {
			return
		}
		if abs, err := filepath.Abs(filepath.Join(base, "agents")); err == nil {
			roots = append(roots, abs)
		}
	}
	add(managedConfigRoot)
	add(userDataDir)
	return Classification{ManagedRoots: roots}
}

// Classify returns the ManageClass for the given source + on-disk source ref.
// source is the agent_profiles.source provenance ("internal", "plugin",
// "user", "project", "cli", "managed_file", ...); sourceRef is the file path
// (may be relative or "embedded:...").
func (c Classification) Classify(source, sourceRef string) ManageClass {
	switch source {
	case "internal":
		return ManageClassInternal
	case "plugin":
		return ManageClassPlugin
	}
	ref := strings.TrimSpace(sourceRef)
	// A file-backed agent: editable only when its file lives in a writable
	// managed root. A file outside every root (an adapter-discovered agent in
	// a read-only ecosystem location) is external/read-only.
	if ref != "" && !strings.HasPrefix(ref, "embedded:") {
		if c.IsWritablePath(ref) {
			return ManageClassManaged
		}
		switch source {
		case "user", "project", "managed_file", "cli":
			// Managed provenances always classify managed even when the path
			// can't be resolved against a configured root (test fixtures,
			// relative refs from discovery).
			return ManageClassManaged
		}
		return ManageClassExternal
	}
	// No on-disk file (DB-only operator agent) or an embedded non-internal
	// def: operator-owned, editable. Capability edits (reflexes, known
	// tools/skills, procedures) persist against the DB projection; a profile
	// edit materializes a managed file on first write.
	return ManageClassManaged
}

// IsWritablePath reports whether ref resolves to a file directly inside one
// of the configured writable managed roots. Embedded refs ("embedded:...")
// and refs outside every root return false.
func (c Classification) IsWritablePath(ref string) bool {
	ref = strings.TrimSpace(ref)
	if ref == "" || strings.HasPrefix(ref, "embedded:") {
		return false
	}
	abs, err := filepath.Abs(ref)
	if err != nil {
		return false
	}
	dir := filepath.Dir(abs)
	for _, root := range c.ManagedRoots {
		if dir == root {
			return true
		}
	}
	return false
}
