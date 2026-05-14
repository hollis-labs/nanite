package bootprofile

// Profile is a boot profile configuration loaded from
// <catalog-root>/boot-profiles/<id>.yaml. Field layout mirrors
// Tether's bootgen.Profile so a catalog authored against either side
// can round-trip without translation. Fields the Nanite compiler does
// not consume yet (e.g. MCPServers) are still parsed and preserved on
// the struct so a future cross-app extraction can promote them without
// a schema break.
type Profile struct {
	// ID is the profile identifier, e.g. "nanite.backend.main". Required.
	ID string `yaml:"id"`

	// DisplayName is a human label for UI dropdowns. Optional — falls
	// back to ID when surfaced through LaunchSpec.UILabel.
	DisplayName string `yaml:"display_name"`

	// Launch is the catalog launch ID this profile pairs with when
	// started via the dropdown / `nanite boot <profile>` flow. Empty
	// means the profile is prompt-generation only.
	Launch string `yaml:"launch,omitempty"`

	// Identity carries the agent identity fields (Agent Identity Model).
	Identity Identity `yaml:"identity"`

	// Slots is the named-slot map driving boot-prompt composition.
	Slots map[string]SlotSource `yaml:"slots"`

	// Template is the optional template override path (relative to the
	// catalog root or absolute). When empty the canonical 7-section
	// template is used. The compiler does NOT execute the template in
	// this ticket; it surfaces the path on LaunchSpec.TemplatePath so
	// downstream prompt assembly can pick it up.
	Template string `yaml:"template,omitempty"`

	// MCPServers is the per-profile MCP server allow-list, passed
	// through to the MUX_MCP_SERVERS env when the launch starts. Empty
	// = no allowlist (proxy default applies).
	MCPServers []string `yaml:"mcp_servers,omitempty"`

	// Vars is an optional inline variable map the compiler merges with
	// the caller's Vars before substitution. Profile-supplied vars are
	// overridden by caller-supplied vars on key collision so a session
	// boot can override e.g. {{work_root}} per launch.
	Vars map[string]string `yaml:"vars,omitempty"`
}

// Identity holds agent identity metadata per the Agent Identity Model.
// Field names match Tether's bootgen.Identity so catalog YAMLs are
// portable across both apps without translation.
type Identity struct {
	LineageAlias   string `yaml:"lineage_alias"`
	LineageID      string `yaml:"lineage_id,omitempty"`
	ProfileID      string `yaml:"profile_id,omitempty"`
	ProfileVersion int    `yaml:"profile_version,omitempty"`
	Role           string `yaml:"role,omitempty"`
	Project        string `yaml:"project,omitempty"`
	WorkRoot       string `yaml:"work_root,omitempty"`
	TrackingRoot   string `yaml:"tracking_root,omitempty"`
	VantaPrimary   string `yaml:"vanta_primary,omitempty"`
}

// SlotSource describes how to populate a single named slot. Type
// determines which other fields are consulted:
//
//	"text"         — Content is the inline slot body (Nanite addition;
//	                 always safe for a pure compiler).
//	"static"       — Path is a file or directory; directory paths use
//	                 Glob (default "*.md") and Limit. The compiler reads
//	                 files relative to the catalog root.
//	"role_summary" — surfaces as a Requirement; the compiler does not
//	                 read role files in this ticket.
//	"skill_index"  — surfaces as a Requirement; skill discovery is a
//	                 runtime concern.
//	"cmd"          — surfaces as a Requirement; shell execution is
//	                 explicitly out of scope for the compiler.
//	"http"         — surfaces as a Requirement; HTTP fetch is explicitly
//	                 out of scope for the compiler.
//
// The rationale for the split: the compiler must be reproducible and
// side-effect-free so the same Profile + Vars always produce the same
// LaunchSpec. cmd/http/role_summary/skill_index pull from the live
// system state and therefore belong in the launch-time resolver
// (CW-20260514-0048).
type SlotSource struct {
	Type string `yaml:"type"`

	// Inline text body for type="text". This field is a Nanite-side
	// addition — Tether's bootgen does not have it — kept here so the
	// pure compiler has at least one zero-IO slot type available.
	Content string `yaml:"content,omitempty"`

	// Static / role_summary path; may be absolute, ~-prefixed, or
	// relative to the catalog root.
	Path string `yaml:"path,omitempty"`

	// Glob applied inside a directory Path (static). Default "*.md".
	Glob string `yaml:"glob,omitempty"`

	// Limit caps the number of files when Glob matches several.
	Limit int `yaml:"limit,omitempty"`

	// Cmd source fields (surface only — not executed by the compiler).
	Run     string `yaml:"run,omitempty"`
	Timeout string `yaml:"timeout,omitempty"`

	// HTTP source fields (surface only — not executed by the compiler).
	URL            string `yaml:"url,omitempty"`
	ResponseFormat string `yaml:"response_format,omitempty"`
}

// Launch is a launch profile loaded from
// <catalog-root>/launches/<id>.yaml. The shape stays close to Tether's
// catalog launches but is simpler — Nanite drives the spawn through
// agent.Boot, which already owns env composition, sandbox profile, and
// supervisor wiring, so the launch only needs to express the bits
// upstream layers cannot infer:
//
//	Provider  — selects the agent profile / CLI adapter to use. May be
//	            an alias (pty / pty-claude / pty-codex / sub-<x>) — the
//	            compiler normalizes via chat.NormalizeCLIProvider.
//	Workdir   — the project directory the spawned agent should reach
//	            via per-provider mechanisms (claude --add-dir, etc.).
//	UILabel   — optional dropdown label override. Defaults to the
//	            Profile's DisplayName, then its ID.
//	Env       — explicit overrides merged on top of agent.Boot's
//	            composed env at launch time.
//	Args      — optional CLI args override; rarely used.
//	BootMode  — optional boot-mode hint ("file" / "stdin" / "inline");
//	            falls through to the provider adapter's default.
type Launch struct {
	ID       string            `yaml:"id"`
	Provider string            `yaml:"provider"`
	Workdir  string            `yaml:"workdir,omitempty"`
	UILabel  string            `yaml:"ui_label,omitempty"`
	Env      map[string]string `yaml:"env,omitempty"`
	Args     []string          `yaml:"args,omitempty"`
	BootMode string            `yaml:"boot_mode,omitempty"`
}

// Catalog is the loaded set of boot profiles and launches keyed by ID.
// A nil / zero Catalog is the "no catalog configured" state the
// acceptance criteria require to be inert — Compile against a missing
// profile returns ErrProfileNotFound; existing Nanite provider behavior
// is unaffected when no catalog is configured because no caller invokes
// Compile in that case.
type Catalog struct {
	// Root is the absolute path the catalog was loaded from. Empty when
	// the catalog is constructed in-memory.
	Root string

	// Profiles is the loaded boot profiles keyed by Profile.ID.
	Profiles map[string]Profile

	// Launches is the loaded launches keyed by Launch.ID.
	Launches map[string]Launch
}

// IsEmpty reports whether the catalog has no profiles or launches.
// Useful for the "no catalog configured" branch in callers that want
// to skip downstream wiring entirely when the operator has not set up
// a catalog directory.
func (c *Catalog) IsEmpty() bool {
	if c == nil {
		return true
	}
	return len(c.Profiles) == 0 && len(c.Launches) == 0
}

// Vars is a string→string map passed to Compile for template variable
// substitution. Keys are the bare variable name (no braces); values
// are the literal replacement. Unknown variables encountered during
// substitution are an error — see slots.go for the rationale.
type Vars = map[string]string
