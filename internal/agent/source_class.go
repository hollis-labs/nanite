package agent

// ManageClass classifies an agent profile by ownership. Runtime and operator
// mutations are database-backed; SourceRef is provenance only and never makes
// a filesystem path authoritative or writable.
type ManageClass string

const (
	// ManageClassManaged is an operator-owned database profile. GUI/API/CLI
	// callers may edit it in place through the shared AgentConfigService.
	ManageClassManaged ManageClass = "managed"

	// ManageClassInternal is an embedded harness primitive seeded by Nanite.
	// It is read-only and hidden from the management surface.
	ManageClassInternal ManageClass = "internal"

	// ManageClassPlugin is plugin/vendor-provided and read-only in place. It
	// may be copied into a new operator-owned database profile.
	ManageClassPlugin ManageClass = "plugin"

	// ManageClassExternal records explicitly external/imported provenance.
	// It is read-only in place and may be copied to an operator-owned profile.
	ManageClassExternal ManageClass = "external"
)

func (c ManageClass) Editable() bool { return c == ManageClassManaged }

func (c ManageClass) CopyToManagedAllowed() bool {
	return c == ManageClassPlugin || c == ManageClassExternal
}

// Classification is stateless because ownership is stored in the profile's
// Source provenance, not inferred from a path on disk.
type Classification struct{}

func NewClassification() Classification { return Classification{} }

func (Classification) Classify(source string) ManageClass {
	switch source {
	case "internal", "builtin":
		return ManageClassInternal
	case "plugin":
		return ManageClassPlugin
	case "", "api", "cli", "managed_file", "nanite", "project", "user":
		return ManageClassManaged
	default:
		// Adapter or future import provenances are read-only until the
		// importer deliberately records operator ownership above.
		return ManageClassExternal
	}
}
