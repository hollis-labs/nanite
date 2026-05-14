package bootprofile

import (
	"errors"
	"fmt"
	"sort"
	"strconv"

	"github.com/hollis-labs/nanite/internal/chat"
)

// LaunchSpec is the compiled runtime contract — the data structure
// downstream Nanite code (CW-20260514-0047 dropdown surfacing,
// CW-20260514-0048 runtime hookup) consumes WITHOUT reparsing YAML.
//
// Field rationale:
//
//	ProfileID / LaunchID   — provenance; lets downstream log "started
//	                          via profile X via launch Y" without
//	                          re-deriving from catalog state.
//	Provider               — normalized adapter name. Always the bare
//	                          form ("claude" / "codex" / "opencode"
//	                          / "anthropic" / ...); aliases like
//	                          "pty-claude" / legacy "pty" are resolved
//	                          via chat.NormalizeCLIProvider.
//	ProviderAlias          — the original, un-normalized provider
//	                          string as the launch declared it. Kept
//	                          for telemetry and for the dropdown,
//	                          which still surfaces aliased rows.
//	Workdir                — the project directory the spawned agent
//	                          should reach. Maps onto agent.Options.Workdir
//	                          in 0048.
//	BootPrompt             — the fully-rendered prompt body when every
//	                          slot resolved at compile time. Empty
//	                          when at least one Requirement is unresolved.
//	BootMode               — provider-adapter boot-mode hint (file /
//	                          stdin / inline). Empty = adapter default.
//	UILabel                — dropdown label, derived from launch.UILabel
//	                          → profile.DisplayName → profile.ID.
//	Env / Args             — straight pass-through from Launch.
//	TemplatePath           — surfaces Profile.Template so 0048 can
//	                          plug in a custom template; empty for
//	                          the default 7-section template.
//	MCPServers             — pass-through from Profile.MCPServers; 0048
//	                          will translate to MUX_MCP_SERVERS env.
//	Slots                  — resolved-slot bodies keyed by slot name.
//	                          A consumer that wants to render its own
//	                          template (or test a slot in isolation)
//	                          can use this without re-resolving.
//	Requirements           — deferred slot resolutions (cmd / http /
//	                          role_summary / skill_index). The launch-time
//	                          resolver in 0048 must drain these before
//	                          the session can fully boot.
//	Identity               — the profile's identity block, lifted so
//	                          downstream doesn't have to re-load the
//	                          profile to read it.
//
// LaunchSpec is JSON-serializable so it can be passed across process
// boundaries (FE dropdown surfacing, telemetry export, future cross-app
// extraction). All slice / map fields are guaranteed non-nil after a
// successful Compile so consumers can iterate without nil-checking.
type LaunchSpec struct {
	ProfileID     string            `json:"profile_id"`
	LaunchID      string            `json:"launch_id,omitempty"`
	Provider      string            `json:"provider"`
	ProviderAlias string            `json:"provider_alias,omitempty"`
	Workdir       string            `json:"workdir,omitempty"`
	BootPrompt    string            `json:"boot_prompt,omitempty"`
	BootMode      string            `json:"boot_mode,omitempty"`
	UILabel       string            `json:"ui_label"`
	Env           map[string]string `json:"env"`
	Args          []string          `json:"args"`
	TemplatePath  string            `json:"template_path,omitempty"`
	MCPServers    []string          `json:"mcp_servers,omitempty"`
	Slots         map[string]string `json:"slots"`
	Requirements  []Requirement     `json:"requirements"`
	Identity      Identity          `json:"identity"`
}

// Requirement describes a deferred slot that the compiler could not
// resolve in its pure-IO mode. The launch-time resolver consumes these
// and substitutes the resolved bodies into LaunchSpec.Slots /
// LaunchSpec.BootPrompt before invoking agent.Boot.
//
// Fields are a strict subset of SlotSource — the cmd / http / role /
// skill knobs each deferred type actually needs — so future extraction
// can stabilize Requirement independently of the SlotSource schema.
type Requirement struct {
	Slot           string `json:"slot"`
	Type           string `json:"type"`
	Path           string `json:"path,omitempty"`
	Run            string `json:"run,omitempty"`
	Timeout        string `json:"timeout,omitempty"`
	URL            string `json:"url,omitempty"`
	ResponseFormat string `json:"response_format,omitempty"`
	Limit          int    `json:"limit,omitempty"`
}

// Compile produces a LaunchSpec from a Profile + Launch + Vars. The
// (profile, launch) pair must come from the same catalog (or be hand-
// constructed in tests); the compiler does NOT consult a Catalog because
// downstream callers may want to compile against in-memory profiles
// (e.g. plugin-supplied profiles, dropdown-preview previewing edits).
//
// catalogRoot is the directory used to resolve relative static-slot
// paths. Pass "" when the profile uses only absolute / ~-prefixed paths
// or has no static slots; the loader Catalog.Root is the natural value.
//
// vars is the caller-supplied variable map; the compiler merges it with
// the profile's inline Vars (caller wins on collision) and adds the
// profile's Identity fields under both flat and dotted keys. See
// slots.go Substitute for the substitution policy.
//
// Errors:
//
//   - profile.ID == "" or identity invalid → returned as a clean error
//     (matches the load-time validation, since callers can hand-build
//     Profile values in tests).
//   - any slot resolution failure → returned with the slot name in the
//     message.
//   - any variable substitution failure → returned with the missing
//     variable names.
//   - provider alias normalization is infallible (chat.NormalizeCLIProvider
//     passes unknown names through unchanged).
func Compile(profile Profile, launch *Launch, vars Vars, catalogRoot string) (*LaunchSpec, error) {
	if profile.ID == "" {
		return nil, errors.New("bootprofile: cannot compile profile with empty id")
	}
	if profile.Identity.LineageAlias == "" {
		return nil, fmt.Errorf("bootprofile: profile %q missing identity.lineage_alias", profile.ID)
	}

	mergedVars := buildVars(profile, vars)

	spec := &LaunchSpec{
		ProfileID:    profile.ID,
		UILabel:      uiLabelFor(profile, launch),
		Env:          map[string]string{},
		Args:         nil,
		TemplatePath: profile.Template,
		MCPServers:   append([]string(nil), profile.MCPServers...),
		Slots:        map[string]string{},
		Requirements: nil,
		Identity:     profile.Identity,
	}

	if launch != nil {
		spec.LaunchID = launch.ID
		spec.ProviderAlias = launch.Provider
		spec.Provider = chat.NormalizeCLIProvider(launch.Provider)
		spec.Workdir = launch.Workdir
		spec.BootMode = launch.BootMode
		for k, v := range launch.Env {
			spec.Env[k] = v
		}
		spec.Args = append(spec.Args, launch.Args...)
	}

	// Resolve slots in name-sorted order so the generated boot prompt
	// (and the Requirement list) is deterministic across runs.
	slotNames := make([]string, 0, len(profile.Slots))
	for name := range profile.Slots {
		slotNames = append(slotNames, name)
	}
	sort.Strings(slotNames)

	for _, name := range slotNames {
		src := profile.Slots[name]
		content, req, err := resolveSlot(name, src, catalogRoot, mergedVars)
		if err != nil {
			return nil, err
		}
		if req != nil {
			spec.Requirements = append(spec.Requirements, *req)
			continue
		}
		spec.Slots[name] = content
	}

	// BootPrompt is intentionally left empty when any requirement is
	// unresolved — emitting a half-rendered prompt would mask the
	// missing data downstream. The launch-time resolver in 0048 will
	// drain Requirements, fill spec.Slots, and then call a renderer.
	if len(spec.Requirements) == 0 {
		spec.BootPrompt = renderDefaultPrompt(spec)
	}

	return spec, nil
}

// CompileFromCatalog is a convenience helper for callers that have a
// loaded catalog: look the profile + paired launch up by ID and delegate
// to Compile. Returns ErrProfileNotFound / ErrLaunchNotFound for the
// two not-found cases so callers can branch on errors.Is.
func CompileFromCatalog(cat *Catalog, profileID string, vars Vars) (*LaunchSpec, error) {
	if cat == nil {
		return nil, ErrProfileNotFound
	}
	prof, ok := cat.Profiles[profileID]
	if !ok {
		return nil, fmt.Errorf("%w: id=%q (catalog root=%s)", ErrProfileNotFound, profileID, cat.Root)
	}
	var launchPtr *Launch
	if prof.Launch != "" {
		l, ok := cat.Launches[prof.Launch]
		if !ok {
			return nil, fmt.Errorf("%w: profile %q references launch %q (catalog root=%s)", ErrLaunchNotFound, profileID, prof.Launch, cat.Root)
		}
		launchPtr = &l
	}
	return Compile(prof, launchPtr, vars, cat.Root)
}

// buildVars folds Profile.Vars, caller-supplied vars, and the profile's
// Identity fields into a single map for Substitute. Precedence:
//
//	caller > profile.Vars > identity-derived
//
// Identity is exposed under both flat keys (`work_root`) and dotted
// keys (`identity.work_root`) so authors can use either style and the
// dotted form survives if a future ticket introduces nested variable
// namespaces.
func buildVars(profile Profile, callerVars Vars) Vars {
	out := Vars{}
	addIdentityVars(out, profile.Identity)
	for k, v := range profile.Vars {
		out[k] = v
	}
	for k, v := range callerVars {
		out[k] = v
	}
	return out
}

func addIdentityVars(dst Vars, id Identity) {
	put := func(key, val string) {
		if val == "" {
			return
		}
		dst[key] = val
		dst["identity."+key] = val
	}
	put("lineage_alias", id.LineageAlias)
	put("lineage_id", id.LineageID)
	put("profile_id", id.ProfileID)
	if id.ProfileVersion != 0 {
		v := strconv.Itoa(id.ProfileVersion)
		dst["profile_version"] = v
		dst["identity.profile_version"] = v
	}
	put("role", id.Role)
	put("project", id.Project)
	put("work_root", id.WorkRoot)
	put("tracking_root", id.TrackingRoot)
	put("vanta_primary", id.VantaPrimary)
}

func uiLabelFor(profile Profile, launch *Launch) string {
	if launch != nil && launch.UILabel != "" {
		return launch.UILabel
	}
	if profile.DisplayName != "" {
		return profile.DisplayName
	}
	return profile.ID
}

// renderDefaultPrompt produces a stable, minimal prompt body from a
// fully-resolved LaunchSpec. The compiler intentionally does NOT carry
// the full 7-section Tether template — that template is a downstream
// presentation concern that may diverge between Nanite and Tether (e.g.
// Nanite may want to inject session metadata Tether doesn't have). The
// default rendering is enough to drive boot for chat sessions and is
// trivially overridable by setting Profile.Template + handling it in
// 0048.
//
// Section order matches the canonical 7-section shape so an operator
// reading the rendered output sees the familiar layout. Sections are
// omitted when their slot resolved to empty content.
func renderDefaultPrompt(spec *LaunchSpec) string {
	var b stringsBuilder
	b.writeln("# Boot Prompt — " + spec.UILabel)
	b.writeln("")
	b.writeln("## Identity")
	b.writeln("- lineage_alias: " + spec.Identity.LineageAlias)
	if spec.Identity.Role != "" {
		b.writeln("- role: " + spec.Identity.Role)
	}
	if spec.Identity.Project != "" {
		b.writeln("- project: " + spec.Identity.Project)
	}
	if spec.Identity.WorkRoot != "" {
		b.writeln("- work_root: " + spec.Identity.WorkRoot)
	}
	if spec.Identity.TrackingRoot != "" {
		b.writeln("- tracking_root: " + spec.Identity.TrackingRoot)
	}

	slotOrder := canonicalSlotOrder(spec.Slots)
	for _, name := range slotOrder {
		body := spec.Slots[name]
		if body == "" {
			continue
		}
		b.writeln("")
		b.writeln("## " + name)
		b.writeln(body)
	}
	return b.String()
}

// canonicalSlotOrder returns the slot names in the canonical
// 7-section order followed by any unknown slots in alpha order. Keeping
// the order stable here means downstream rendering tests can pin output
// without depending on map iteration order.
func canonicalSlotOrder(slots map[string]string) []string {
	canonical := []string{"agent", "recap", "delta", "history", "tasks", "status", "memory", "knowledge", "skills", "context", "candidates", "narrative"}
	seen := map[string]struct{}{}
	var out []string
	for _, name := range canonical {
		if _, ok := slots[name]; ok {
			out = append(out, name)
			seen[name] = struct{}{}
		}
	}
	var extras []string
	for name := range slots {
		if _, ok := seen[name]; ok {
			continue
		}
		extras = append(extras, name)
	}
	sort.Strings(extras)
	return append(out, extras...)
}

// stringsBuilder is a tiny internal helper that mirrors strings.Builder
// but adds writeln; kept inline rather than importing strings.Builder to
// keep this file dependency-free below the chat import.
type stringsBuilder struct {
	buf []byte
}

func (s *stringsBuilder) writeln(line string) {
	s.buf = append(s.buf, line...)
	s.buf = append(s.buf, '\n')
}

func (s *stringsBuilder) String() string {
	return string(s.buf)
}
