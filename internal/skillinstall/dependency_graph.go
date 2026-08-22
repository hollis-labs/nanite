package skillinstall

// dependency_graph.go — TASKS/skills/07
// (TASKS/skills/07-implement-inline-fork-composition-semantics.md):
// docs/engineering/architecture/20-skills.md's "Composition" section:
// "cycle/recursion-limit detection runs in the Skill Resolver against
// the install-time dependency graph (checked once, at install/sync
// time, against already-installed dependencies) rather than discovered
// live during a materialization pass." This file is that check, wired
// into Installer.Install (install.go) as a validation step between
// Validate and Vendor — task 04's own DefaultValidator only ever checked
// a single package's own internally-consistent declaration (non-empty,
// unique, not self-referential dependency slugs); this file is the
// separate, graph-level check task 04's own Work Log explicitly deferred
// to this task ("real cycle detection against already-installed
// packages... is TASKS/skills/07's job, not this one's").
import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// DefaultMaxDependencyDepth is the install-time cap on how many levels
// deep a skill's composition graph (inline/fork dependencies) may nest,
// enforced once here against the graph of already-installed skills' own
// DeclaredDependencies — 20-skills.md leaves the actual number
// unspecified ("cycle/recursion-limit detection... rather than
// discovered live during a materialization pass" names the mechanism,
// not the limit); this constant is this task's own concrete design
// call.
//
// 5 is chosen because: a SKILL.md package is meant to be a small,
// tightly-scoped procedural unit (the real Agent-Skills-spec's own
// framing — platform.claude.com's Agent Skills docs describe Skills as
// "reduce repetition... specialize Claude" for one focused domain, not a
// deep hierarchy). A composition chain nested deeper than 5 levels is a
// sign the package structure needs decomposing into flatter, more
// directly-declared dependencies rather than a legitimate need for deep
// nesting. 5 also keeps a materialized ProvenanceChain
// (internal/skill/compose.go) human-readable and bounds how large a
// single top-level invocation's fork-composition subagent fan-out tree
// can grow, while staying generous enough for a real multi-level
// composition like "release-review -> changelog -> commit-log ->
// git-log -> repo-metadata" (4 levels, comfortably under the cap).
//
// internal/skill/compose.go's maxCompositionDepth mirrors this value as
// a defensive runtime backstop only (that package cannot import this
// one — internal/skillinstall depends on internal/skill, not the
// reverse). Keep both constants in sync if this value ever changes.
const DefaultMaxDependencyDepth = 5

// CycleError is returned when installing/syncing a package with the
// given declared dependencies would introduce a cycle into the
// already-installed skill dependency graph.
type CycleError struct {
	// Slug is the package being installed/synced.
	Slug string
	// Cycle is the full path from Slug back to itself, e.g.
	// []string{"a", "b", "c", "a"}.
	Cycle []string
}

func (e *CycleError) Error() string {
	return fmt.Sprintf("skillinstall: installing %q would introduce a dependency cycle: %s",
		e.Slug, strings.Join(e.Cycle, " -> "))
}

// RecursionLimitError is returned when installing/syncing a package
// would make its composition graph exceed DefaultMaxDependencyDepth
// levels deep, even without forming a cycle.
type RecursionLimitError struct {
	// Slug is the package being installed/synced.
	Slug string
	// Path is the (acyclic) chain that exceeded Limit.
	Path []string
	// Limit is the depth cap that was exceeded (DefaultMaxDependencyDepth,
	// or an Installer's own override).
	Limit int
}

func (e *RecursionLimitError) Error() string {
	return fmt.Sprintf("skillinstall: installing %q would exceed the maximum composition depth of %d (path: %s)",
		e.Slug, e.Limit, strings.Join(e.Path, " -> "))
}

// checkDependencyGraph walks the already-installed dependency graph
// reachable from slug's own newly-declared deps, rejecting the install
// if any path leads back to slug (a cycle) or exceeds maxDepth levels
// (even acyclically).
//
// The walk starts from deps (the package's own newly-declared
// dependency slugs — not yet persisted anywhere) and, at each
// subsequently-visited node, looks up that node's CURRENTLY installed
// DeclaredDependencies via index.GetSkillBySlug — i.e. this checks the
// new package's declared deps against the dependency graph as it exists
// today, before this install/sync writes anything. This correctly
// detects the case that actually matters regardless of install order:
// if A is being installed declaring a dependency on B, and B (already
// installed, possibly before A existed at all) already declares a
// dependency on A, the walk reaches "A" again via B's already-persisted
// DeclaredDependencies and reports the cycle — A's own not-yet-written
// row plays no part in the walk, only its slug string as the target
// being compared against.
//
// A declared slug encountered mid-walk that doesn't resolve to any
// currently-installed skill (index.GetSkillBySlug returns nil) is a
// dead end, not an error here — whether every declared dependency
// actually resolves to something installed is
// internal/skill.ResolveDependencyAddresses's job at materialization
// time (task 06), not this install-time graph-shape check's job.
func checkDependencyGraph(index IndexStore, slug string, deps []string, maxDepth int) error {
	visited := make(map[string]bool)

	var walk func(current string, path []string) error
	walk = func(current string, path []string) error {
		newPath := append(append([]string{}, path...), current)

		if current == slug {
			return &CycleError{Slug: slug, Cycle: newPath}
		}
		// newPath always starts with slug itself (path[0] == slug on the
		// very first call), so len(newPath)-1 is the number of edges
		// walked from slug to current — that's the composition depth this
		// limit actually governs.
		if len(newPath)-1 > maxDepth {
			return &RecursionLimitError{Slug: slug, Path: newPath, Limit: maxDepth}
		}
		if visited[current] {
			// Already fully explored from an earlier branch with no path
			// back to slug found — short-circuits both infinite loops on
			// an (illegal, pre-existing) unrelated cycle and redundant
			// re-exploration of a shared (diamond) dependency.
			return nil
		}
		visited[current] = true

		sk, err := index.GetSkillBySlug(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, current)
		if err != nil {
			return fmt.Errorf("skillinstall: dependency graph check: lookup %q: %w", current, err)
		}
		if sk == nil {
			return nil // dangling reference — dead end, not this check's job to flag.
		}
		childDeps, err := decodeDependencySlugs(sk.DeclaredDependencies)
		if err != nil {
			return fmt.Errorf("skillinstall: dependency graph check: decode declared dependencies for %q: %w", current, err)
		}
		for _, child := range childDeps {
			if err := walk(child, newPath); err != nil {
				return err
			}
		}
		return nil
	}

	for _, d := range deps {
		if err := walk(d, []string{slug}); err != nil {
			return err
		}
	}
	return nil
}

// decodeDependencySlugs decodes a store.Skill.DeclaredDependencies
// JSON-array-of-strings value. Empty string and the JSON literals "[]"/
// "null" all mean "no dependencies." Mirrors internal/skill.
// parseDeclaredDependencySlugs exactly (unexported in that package, and
// this package already depends on internal/skill for other reasons, but
// duplicating five lines here is simpler and clearer than exporting a
// cross-package helper for this one shared shape).
func decodeDependencySlugs(raw string) ([]string, error) {
	if raw == "" {
		return nil, nil
	}
	var slugs []string
	if err := json.Unmarshal([]byte(raw), &slugs); err != nil {
		return nil, err
	}
	return slugs, nil
}
