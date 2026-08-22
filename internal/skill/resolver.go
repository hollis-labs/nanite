package skill

// resolver.go — TASKS/skills/06
// (TASKS/skills/06-build-skill-resolver-and-parameter-binding.md):
// docs/engineering/architecture/20-skills.md's "Materialization pipeline"
// section names the pipeline "Skill Resolver → Skill Materializer →
// Policy/Sandbox → Materialized Skill." This file builds the Resolver half
// only:
//
//   - ResolveSkillParameters: given a skill's declared Parameters (task 04's
//     []ParameterSpec, on Definition) and a caller's invocation-time static
//     arguments, produces the flat parameter-value map a future Skill
//     Materializer (TASKS/skills/07/08, not yet landed) will splice into the
//     skill's final rendered output.
//   - ResolveDependencyAddresses: given a skill's DeclaredDependencies
//     (store.Skill's column, task 02, populated by task 04's install
//     pipeline), resolves each declared dependency slug to its own
//     installed Skill catalog row and Skill vendor store address — a
//     single-level lookup exposed as a building block. Composition
//     semantics (inline splice vs. fork delegate) and install-time
//     cycle/recursion-limit detection against the graph this function's
//     input feeds are both TASKS/skills/07's job, not this file's — see
//     that task's own scope split, already confirmed in task 04's Work Log
//     ("task 07's Installer-pipeline extension... can read this column
//     directly for every already-installed skill to build its graph").
//
// # Reuse, not reimplementation
//
// agent_context_resolvers (internal/store/agent_context_resolvers.go) is a
// flat per-agent slot registry — UNIQUE(agent_id, slot_name), resolved at
// launch time via internal/runtime/agent.ResolveContextBlocks. It has no
// concept of "this skill's parameter X, for this specific invocation." The
// real design decision this file makes (per 20-skills.md's own text): a
// skill's declared ParameterSpec can name an existing agent_context_resolvers
// row by its slot_name (via ParameterSpec.ResolverSlot) as its dynamic-value
// source. At resolution time, this file looks up the invoking agent's
// enabled resolver rows, filters to just the ones this skill's own
// parameters actually reference, and resolves them through the *same*
// runtimeagent.ResolveContextBlocks function every other dynamic-context
// consumer in this codebase already calls — no second resolver-provider
// system, per 20-skills.md's explicit instruction ("No second
// resolver-provider system gets built for skills specifically").
//
// ParameterSpec.ResolverSlot and store.AgentContextResolver.SlotName are
// different Go identifiers with different YAML/JSON tags — the
// correspondence between them is semantic (both name the same kind of
// thing: an agent_context_resolvers row's slot name), not a literal
// field-name match. (This wording is deliberately precise per task 04's own
// review finding on this exact point — see that task's Work Log's "Wording
// correction" entry.)
//
// # Precedence
//
// When a parameter has both a caller-supplied static argument and a
// ResolverSlot binding, the static argument wins. This is the more
// conventional choice — an explicit, per-invocation value overriding a
// standing per-agent default — and the one ResolveSkillParameters
// implements below.
//
// # Failure semantics
//
// A single matching resolver row's failure aborts the whole
// ResolveSkillParameters call, matching runtimeagent.ResolveContextBlocks's
// own documented all-or-nothing behavior — this file does not build a
// partial-success mode the underlying mechanism doesn't support. Dynamic
// resolution only ever queries the resolver rows this skill's own
// parameters actually reference (not every resolver row the agent happens
// to have configured for unrelated purposes), so a failure on some other,
// unrelated slot never affects this skill's resolution at all.
import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	runtimeagent "github.com/hollis-labs/nanite/internal/runtime/agent"
	"github.com/hollis-labs/nanite/internal/store"
)

// AgentContextResolverStore is the narrow slice of *store.Store's API
// ResolveSkillParameters depends on for looking up an agent's dynamic
// resolver rows. A real *store.Store satisfies this directly; the
// interface exists purely for test injection (matching this batch's
// established internal/skillinstall.Vendorer/IndexStore
// DI-for-testability convention — there is only ever one real
// implementation in production).
type AgentContextResolverStore interface {
	ListEnabledAgentContextResolvers(ctx context.Context, agentID string) ([]store.AgentContextResolver, error)
}

// SkillIndexStore is the narrow slice of *store.Store's API
// ResolveDependencyAddresses depends on. A real *store.Store satisfies
// this directly.
type SkillIndexStore interface {
	GetSkillBySlug(slug string) (*store.Skill, error)
}

// MissingSkillParameterError is returned by ResolveSkillParameters when one
// or more of a skill's Required parameters have neither a caller-supplied
// static argument nor a resolvable dynamic binding. Typed (rather than a
// bare fmt.Errorf) so a Materializer, self-tool, or admin preview surface
// can render a clear, specific message — and so callers can distinguish
// "this invocation was missing input" from any other resolution failure
// via errors.As.
type MissingSkillParameterError struct {
	// Skill is the skill slug/name this resolution was for, when known.
	Skill string
	// Names is the sorted, deduplicated list of missing required
	// parameter names.
	Names []string
}

func (e *MissingSkillParameterError) Error() string {
	who := e.Skill
	if who == "" {
		who = "skill"
	}
	if len(e.Names) == 1 {
		return fmt.Sprintf("%s: missing required parameter %q (no static argument supplied and no resolvable dynamic binding)", who, e.Names[0])
	}
	return fmt.Sprintf("%s: missing required parameters: %s (no static argument supplied and no resolvable dynamic binding)", who, strings.Join(e.Names, ", "))
}

// ResolveSkillParameters resolves def's declared Parameters into a flat
// map of parameter-name -> resolved value, for one specific invocation.
//
// For each declared ParameterSpec:
//  1. If staticArgs supplies a value for Name, that value is used — a
//     static, caller-supplied argument always takes precedence over a
//     dynamic resolver binding when both are present for the same
//     parameter (see this file's package doc, "Precedence").
//  2. Else, if ResolverSlot names an existing, enabled
//     agent_context_resolvers row belonging to agentID, that row's
//     resolved content supplies the value.
//  3. Else, if Required, the parameter's name is collected and returned
//     (after every parameter has been considered, so a caller sees every
//     missing parameter in one pass) as a *MissingSkillParameterError.
//  4. Else (optional, unresolved), the parameter is omitted from the
//     returned map entirely — never set to an empty string — so a
//     Materializer can distinguish "resolved to empty" from "never
//     supplied."
//
// workdir is threaded through to runtimeagent.ResolveContextBlocks as the
// base directory a "cmd"-kind resolver runs in when its own CWD is empty,
// matching internal/service/chat_boot_drive.go's
// resolveAgentContextForBoot's use of the same parameter for the
// equivalent boot-time call.
//
// resolvers may be nil when def declares no ResolverSlot bindings (or
// every such parameter is already covered by a static argument) — nothing
// dynamic is ever looked up in that case. Passing nil while a binding is
// actually needed is a configuration error, not a panic: it comes back as
// a plain, named error.
func ResolveSkillParameters(
	ctx context.Context,
	def Definition,
	agentID string,
	staticArgs map[string]string,
	workdir string,
	resolvers AgentContextResolverStore,
) (map[string]string, error) {
	if len(def.Parameters) == 0 {
		return map[string]string{}, nil
	}

	resolvedBlocks, err := resolveNeededDynamicBindings(ctx, def, agentID, staticArgs, workdir, resolvers)
	if err != nil {
		return nil, err
	}

	out := make(map[string]string, len(def.Parameters))
	var missing []string
	for _, p := range def.Parameters {
		if v, ok := staticArgs[p.Name]; ok {
			out[p.Name] = v
			continue
		}
		if p.ResolverSlot != "" {
			if v, ok := resolvedBlocks[p.ResolverSlot]; ok {
				out[p.Name] = v
				continue
			}
		}
		if p.Required {
			missing = append(missing, p.Name)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, &MissingSkillParameterError{Skill: skillLabel(def), Names: missing}
	}
	return out, nil
}

// resolveNeededDynamicBindings collects the ResolverSlot names def's
// parameters actually reference (skipping any parameter already satisfied
// by a static argument), looks up agentID's enabled
// agent_context_resolvers rows, filters to just the matching ones, and
// resolves them all through one runtimeagent.ResolveContextBlocks call.
// Returns (nil, nil) when no parameter needs dynamic resolution at all —
// the common case for a skill with no ResolverSlot bindings, or one whose
// static args already cover every declared parameter — without ever
// touching resolvers or the database.
func resolveNeededDynamicBindings(
	ctx context.Context,
	def Definition,
	agentID string,
	staticArgs map[string]string,
	workdir string,
	resolvers AgentContextResolverStore,
) (map[string]string, error) {
	neededSlots := make(map[string]bool)
	for _, p := range def.Parameters {
		if _, ok := staticArgs[p.Name]; ok {
			continue
		}
		if p.ResolverSlot != "" {
			neededSlots[p.ResolverSlot] = true
		}
	}
	if len(neededSlots) == 0 {
		return nil, nil
	}
	if resolvers == nil {
		return nil, fmt.Errorf("skill: %s declares a resolver-bound parameter but no AgentContextResolverStore was configured", skillLabel(def))
	}

	rows, err := resolvers.ListEnabledAgentContextResolvers(ctx, agentID)
	if err != nil {
		return nil, fmt.Errorf("skill: %s: list agent context resolvers for agent %q: %w", skillLabel(def), agentID, err)
	}

	matched := make([]store.AgentContextResolver, 0, len(neededSlots))
	for _, row := range rows {
		if neededSlots[row.SlotName] {
			matched = append(matched, row)
		}
	}
	if len(matched) == 0 {
		// None of the referenced slots have a configured row for this
		// agent. Not an error here — the caller's Required check (in
		// ResolveSkillParameters) surfaces this as a named
		// MissingSkillParameterError instead of a generic resolver-lookup
		// failure, and an optional parameter with an unconfigured binding
		// is legitimately just omitted.
		return nil, nil
	}

	// A single row's failure aborts the whole call here, by construction:
	// ResolveContextBlocks itself has no partial-success mode (see its own
	// doc comment), and this file deliberately does not build one on top
	// of it.
	blocks, err := runtimeagent.ResolveContextBlocks(ctx, matched, workdir)
	if err != nil {
		return nil, fmt.Errorf("skill: %s: resolve dynamic parameter bindings: %w", skillLabel(def), err)
	}
	return blocks, nil
}

func skillLabel(def Definition) string {
	if def.Slug != "" {
		return def.Slug
	}
	return "skill"
}

// DependencyAddress is one of a skill's declared dependencies (task 02's
// store.Skill.DeclaredDependencies column), resolved to the installed
// store.Skill row it names and that row's vendored content address. This
// is a building block only — TASKS/skills/07's Materializer decides what
// to actually do with a resolved dependency (splice its own materialized
// content for Context=="inline", delegate to a subagent/fork invocation
// for Context=="fork"); this file's job stops at "here is the installed
// skill this slug names, and here is where its bytes live."
type DependencyAddress struct {
	// Slug is the declared dependency's slug, exactly as it appeared in
	// DeclaredDependencies.
	Slug string
	// Skill is the resolved dependency's own Skill catalog row.
	Skill store.Skill
	// Address is Skill.ContentHash — the vendored store address a
	// Materializer reads (internal/skillvendor.Store.Path/ReadFiles) to
	// get the dependency's actual bytes.
	Address string
}

// ResolveDependencyAddresses resolves declaredDependenciesJSON — a JSON
// array of skill slugs, exactly the shape stored in a store.Skill's own
// DeclaredDependencies column (task 02, populated at install time by
// internal/skillinstall's extractDeclaredDependencies) — through idx to
// each dependency's installed Skill catalog row and vendored content
// address.
//
// This is a single-level lookup only: it does not walk transitively into
// a resolved dependency's own DeclaredDependencies, and it does not detect
// cycles. Both are TASKS/skills/07's job, run once against the full
// installed-skill graph at install/sync time
// (docs/engineering/architecture/20-skills.md's "Composition" section:
// "cycle/recursion-limit detection runs in the Skill Resolver against the
// install-time dependency graph... rather than discovered live during a
// materialization pass") — this function is the per-call building block
// that graph-level logic is layered on top of, not a reimplementation of
// it.
//
// A declared dependency slug that doesn't resolve to any installed skill
// is a hard error, named exactly like runtimeagent.ResolveContextBlocks
// names an unresolvable resolver slot: a skill declaring a dependency it
// can't actually reach is a broken composition, not something to silently
// skip.
func ResolveDependencyAddresses(idx SkillIndexStore, declaredDependenciesJSON string) ([]DependencyAddress, error) {
	slugs, err := parseDeclaredDependencySlugs(declaredDependenciesJSON)
	if err != nil {
		return nil, fmt.Errorf("skill: parse declared dependencies %q: %w", declaredDependenciesJSON, err)
	}
	if len(slugs) == 0 {
		return nil, nil
	}
	if idx == nil {
		return nil, fmt.Errorf("skill: resolve declared dependencies: no SkillIndexStore configured")
	}

	out := make([]DependencyAddress, 0, len(slugs))
	for _, slug := range slugs {
		sk, err := idx.GetSkillBySlug(slug)
		if err != nil {
			return nil, fmt.Errorf("skill: resolve declared dependency %q: %w", slug, err)
		}
		if sk == nil {
			return nil, fmt.Errorf("skill: declared dependency %q is not installed in the skill catalog", slug)
		}
		out = append(out, DependencyAddress{Slug: slug, Skill: *sk, Address: sk.ContentHash})
	}
	return out, nil
}

// parseDeclaredDependencySlugs decodes a store.Skill.DeclaredDependencies
// JSON-array-of-strings value. Empty string and "[]" both return (nil,
// nil) — a dependency-free skill, the common case.
func parseDeclaredDependencySlugs(raw string) ([]string, error) {
	if raw == "" {
		return nil, nil
	}
	var slugs []string
	if err := json.Unmarshal([]byte(raw), &slugs); err != nil {
		return nil, err
	}
	return slugs, nil
}
