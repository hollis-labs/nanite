package skill

// compose.go — TASKS/skills/07
// (TASKS/skills/07-implement-inline-fork-composition-semantics.md):
// docs/engineering/architecture/20-skills.md's "Composition: inline vs.
// fork" section gives the previously-dead Context: "inline"|"fork"
// frontmatter field (internal/skill/parser.go) its real semantics — the
// Skill Materializer half of the pipeline named in "Materialization
// pipeline": "Skill Resolver -> Skill Materializer -> Policy/Sandbox ->
// Materialized Skill." internal/skill/resolver.go (task 06) built the
// Resolver half (parameter binding, single-level dependency-address
// lookup); this file is the Materializer's composition logic:
//
//   - inline: a nested skill's materialized content is spliced into the
//     parent's materialized output before the model ever sees either.
//     Pure content composition — this file recursively materializes the
//     dependency (its own SKILL.md body, its own parameters resolved,
//     its own further dependencies) and concatenates the result. No new
//     execution path.
//   - fork: the nested skill's invocation is delegated to a real,
//     already-live subagent turn (internal/subagent.Service.Spawn, the
//     exact mechanism internal/selftools' subagent_spawn self-tool rides
//     mid-conversation — not a new spawning mechanism), and only that
//     invocation's *result* folds back into the parent's materialized
//     output, never its full transcript.
//
// Which mode applies to a given nested dependency is read from that
// DEPENDENCY's own Context field (re-parsed from its vendored SKILL.md),
// not the parent's — Context is a per-package, self-declared "how do I
// want to be composed when someone else includes me" attribute (there is
// no per-declared-dependency composition-mode annotation anywhere in the
// package format), so it lives with whichever skill is actually being
// composed, exactly like it already lives on every SKILL.md today.
//
// Cycle/recursion-limit detection against the composition graph is
// deliberately NOT implemented in this file — per 20-skills.md's own
// explicit instruction ("cycle/recursion-limit detection runs in the
// Skill Resolver against the install-time dependency graph... rather
// than discovered live during a materialization pass"), that check runs
// once, at install/sync time, in internal/skillinstall (this task's
// other half — see internal/skillinstall/dependency_graph.go). This
// file's own maxCompositionDepth is a defensive runtime backstop only,
// not the primary enforcement mechanism.
import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/hollis-labs/nanite/internal/skillvendor"
	"github.com/hollis-labs/nanite/internal/subagent"
)

// maxCompositionDepth mirrors internal/skillinstall.DefaultMaxDependencyDepth
// as a defensive runtime backstop only. The primary enforcement of this
// limit is install-time (internal/skillinstall's checkDependencyGraph),
// which guarantees any already-installed dependency graph reachable
// through ResolveDependencyAddresses is both acyclic and within this
// depth — this constant exists so a bug in that gate, or a row edited
// directly in the database outside the install pipeline, fails a
// materialization attempt cleanly instead of recursing unboundedly. Keep
// this value in sync with internal/skillinstall.DefaultMaxDependencyDepth
// if that constant ever changes; the two packages don't share the
// literal constant (skillinstall depends on skill, not the reverse) so
// there's nothing to import here.
const maxCompositionDepth = 5

// paramPlaceholder matches a {{parameter_name}} placeholder in a skill's
// materialized Prompt body. No established Agent-Skills-spec convention
// defines parameter-substitution syntax for a SKILL.md body (confirmed
// against the real spec during this task — platform.claude.com's Agent
// Skills documentation describes Claude choosing to read multiple
// independent SKILL.md files via bash, never a substitution/templating
// mechanism inside one). {{name}} mirrors this codebase's own existing
// internal/service/workflow_engine.go {{input.<key>}} convention (a
// different domain — workflow step config — but the same bracket
// syntax), rather than inventing an unrelated one. This is this task's
// own provisional choice, following the same "genuinely unspecified —
// pick something concrete, document it" precedent TASKS/skills/04 set
// for the dependencies: frontmatter key. A placeholder naming a
// parameter with no resolved value (an omitted optional parameter, or
// literal {{...}} prose in the body that doesn't match any declared
// parameter name) is left untouched rather than blanked out — this is
// plain, non-executing text substitution, not the code-execution class
// of hazard TASKS/skills/08's marker rebuild targets, so no markdown
// code-fence awareness is built here; a documentation example inside a
// fenced block that happens to use a real parameter's name would still
// get substituted. That's an accepted, documented limitation for this
// task, not a security concern (substitution has no side effects), and
// is narrower in scope than task 08's own fence-aware rebuild of actual
// command execution.
var paramPlaceholder = regexp.MustCompile(`\{\{\s*([a-zA-Z0-9_.:-]+)\s*\}\}`)

// substituteParameters replaces every {{name}} placeholder in body with
// params[name] when present. Placeholders with no matching resolved
// value are left as literal text.
func substituteParameters(body string, params map[string]string) string {
	if len(params) == 0 {
		return body
	}
	return paramPlaceholder.ReplaceAllStringFunc(body, func(m string) string {
		sub := paramPlaceholder.FindStringSubmatch(m)
		if len(sub) != 2 {
			return m
		}
		if v, ok := params[sub[1]]; ok {
			return v
		}
		return m
	})
}

// ProvenanceLink is one node in a ProvenanceChain — a skill that was
// touched during a materialization call, and the composition kind that
// pulled it in.
type ProvenanceLink struct {
	// Slug identifies the skill (falls back to whatever skillLabel
	// produces when a Definition has no slug — see resolver.go).
	Slug string
	// Kind is "root" for the top-level, directly-invoked skill, or
	// "inline"/"fork" for a nested dependency, naming the composition
	// mode by which it was pulled in.
	Kind string
}

// ProvenanceChain is the ordered root -> nested -> ... path
// docs/engineering/architecture/20-skills.md's "Composition" section
// describes ("Provenance is tracked as a chain — user -> agent -> root
// skill -> nested skill -> script/materializer -> requested
// capability"). This package owns only the "root skill -> nested skill"
// segment of that chain — the user/agent segment is the caller's own
// context (not reconstructable from a Definition alone), and the
// script/materializer -> requested capability segment is task 08's/09's
// concern for whatever executes *within* a materialized body, not this
// file's composition step.
type ProvenanceChain []ProvenanceLink

// String renders the chain as "slug(kind) -> slug(kind) -> ...", for
// logging and error messages.
func (c ProvenanceChain) String() string {
	parts := make([]string, len(c))
	for i, l := range c {
		parts[i] = fmt.Sprintf("%s(%s)", l.Slug, l.Kind)
	}
	return strings.Join(parts, " -> ")
}

// CompositionError attributes a materialization failure to the specific
// skill in the composition chain that actually produced it — per
// 20-skills.md's provenance requirement, "so a later error or policy
// decision (task 09) can report which skill in the chain actually
// failed/was denied, not just 'materialization failed.'"
type CompositionError struct {
	// Chain is the full root -> ... -> Skill path at the point of
	// failure.
	Chain ProvenanceChain
	// Skill is the slug of the skill whose own processing raised Err
	// (always Chain's last element's Slug).
	Skill string
	Err   error
}

func (e *CompositionError) Error() string {
	return fmt.Sprintf("skill: materialize %s (chain: %s): %v", e.Skill, e.Chain.String(), e.Err)
}

func (e *CompositionError) Unwrap() error { return e.Err }

// VendorReader is the narrow slice of *internal/skillvendor.Store's API
// MaterializeSkill depends on to read a resolved dependency's actual
// bytes ("Materialization always reads the vendored copy live" per
// 20-skills.md's "The model" section). A real *skillvendor.Store
// satisfies this directly; the interface exists purely for test
// injection, matching this batch's established
// Vendorer/IndexStore-style DI-for-testability convention.
type VendorReader interface {
	ReadFiles(address string) (skillvendor.FileMap, error)
}

// SubagentDispatcher is the narrow slice of *internal/subagent.Service's
// real, already-live spawn/status API fork composition rides. This is
// the SAME mechanism internal/selftools.SelfToolsTransport.
// callSpawnSubagent uses to serve the subagent_spawn self-tool
// mid-conversation — not a new spawning mechanism, per 20-skills.md's
// explicit "riding the harness's existing subagent/fork machinery"
// instruction. A real *subagent.Service satisfies this directly.
type SubagentDispatcher interface {
	Spawn(ctx context.Context, req subagent.SpawnRequest) (string, error)
	Status(ctx context.Context, runID string) (*subagent.Run, error)
}

// MaterializerDeps bundles every store/dispatch dependency
// MaterializeSkill needs. Resolvers and Index are the same interfaces
// resolver.go (task 06) already defines and this file reuses as-is — no
// second store-access shape is introduced for the pieces task 06 already
// covers.
type MaterializerDeps struct {
	// Resolvers backs dynamic parameter resolution (ResolveSkillParameters,
	// task 06). Required whenever any touched skill declares a
	// ResolverSlot-bound parameter.
	Resolvers AgentContextResolverStore
	// Index backs dependency-address lookup (ResolveDependencyAddresses,
	// task 06). Required whenever any touched skill declares dependencies.
	Index SkillIndexStore
	// Vendor reads a resolved dependency's vendored bytes so its own
	// SKILL.md can be re-parsed for recursive materialization. Required
	// whenever any touched skill declares dependencies.
	Vendor VendorReader
	// Subagent dispatches a fork-composed dependency's delegated
	// invocation. nil disables fork composition outright — a fork
	// dependency encountered with no Subagent configured fails clearly
	// (a *CompositionError naming the offending skill), never silently
	// degrading to inline splicing.
	Subagent SubagentDispatcher
}

// MaterializeInput carries the invocation-specific values a
// materialization call needs, held constant across the whole recursive
// composition (every touched skill in one call resolves parameters
// against the same agent, the same static args, and the same workdir —
// a composed materialization is one invocation, not a sequence of
// independently-parameterized ones).
type MaterializeInput struct {
	// AgentID is the agent whose agent_context_resolvers rows back any
	// ResolverSlot-bound parameter, for every skill touched during this
	// call (root and nested alike).
	AgentID string
	// StaticArgs are the caller-supplied static arguments, keyed by
	// parameter name. Applied identically at every level of the
	// composition — a parameter name shared by the root skill and a
	// nested dependency both read from this same map. There is no
	// established per-dependency argument-scoping mechanism (no task in
	// this batch defines one), so this is this task's own, documented
	// design choice: one flat argument namespace for the whole composed
	// invocation.
	StaticArgs map[string]string
	// Workdir is the base directory a "cmd"-kind resolver runs in when
	// its own row-level CWD is empty — threaded straight through to
	// ResolveSkillParameters (see that function's own doc comment).
	Workdir string

	// ParentSessionID and ParentAgentID identify the caller's own
	// session/agent for fork composition's subagent.SpawnRequest. Only
	// required when a fork-composed dependency is actually encountered;
	// a purely inline composition never reads either.
	ParentSessionID string
	ParentAgentID   string
	// AgentProfileID is the DB row id backing AgentID, used for fork
	// composition's trust resolution (subagent.SpawnRequest.
	// AgentProfileID). Empty falls back to subagent's own TrustNormal
	// default, matching subagent.Service.Spawn's own documented
	// behavior.
	AgentProfileID string
	// ForkRole is the role slug a fork-composed dependency's subagent is
	// booted with. Required whenever a fork dependency is encountered —
	// 20-skills.md does not specify what role a delegated skill
	// invocation should run as, and this task deliberately does not
	// invent a hardcoded default that might not exist in every
	// deployment; the caller (the real invoking context — e.g. a future
	// skill_get self-tool, task 11) is the only party that knows what
	// role should execute the delegated skill.
	ForkRole string
	// ForkTimeoutSeconds optionally overrides the fork subagent's
	// wall-clock backstop (subagent.SpawnRequest.TimeoutSeconds). Zero
	// means "use subagent.Service's own default resolution."
	ForkTimeoutSeconds int
}

// MaterializedSkill is MaterializeSkill's successful result.
type MaterializedSkill struct {
	// Content is the fully composed materialized output — the root
	// skill's own parameter-substituted body, with every inline
	// dependency's own materialized content spliced in and every fork
	// dependency's delegated result folded in, at each dependency's own
	// insertion point (see composedSectionHeader).
	Content string
	// Chain lists every skill touched during this call, in depth-first
	// visitation order, prefixed by the root. For a composition with
	// more than one dependency this is not a single linear path but the
	// full node-touched set in visitation order — sufficient for
	// 20-skills.md's own stated purpose ("which skill in the chain
	// actually failed/was denied"), since a *failure* instead returns a
	// CompositionError carrying the specific branch's own path.
	Chain ProvenanceChain
}

// MaterializeSkill is the Skill Materializer's composition entry point:
// given a root skill's already-parsed Definition (its own parameters
// resolved, its own dependencies recursively materialized or delegated
// per each dependency's own Context), produces the final materialized
// output plus the provenance chain of every skill touched.
func MaterializeSkill(ctx context.Context, deps MaterializerDeps, def Definition, input MaterializeInput) (*MaterializedSkill, error) {
	var touched ProvenanceChain
	content, err := materializeOne(ctx, deps, def, input, nil, &touched, 0, "root")
	if err != nil {
		return nil, err
	}
	return &MaterializedSkill{Content: content, Chain: touched}, nil
}

// materializeOne materializes def (a single skill in the composition
// graph), recursing into its declared dependencies. path is the
// root -> ... -> def descent so far (copy-appended, never mutated in
// place, so sibling branches don't alias each other's slice backing
// array); touched accumulates every visited node across the whole call
// tree, shared by pointer. viaKind is "root" for the initial call or
// "inline"/"fork" for however the caller decided to pull def in.
func materializeOne(
	ctx context.Context,
	deps MaterializerDeps,
	def Definition,
	input MaterializeInput,
	path ProvenanceChain,
	touched *ProvenanceChain,
	depth int,
	viaKind string,
) (string, error) {
	label := skillLabel(def)
	node := ProvenanceLink{Slug: label, Kind: viaKind}
	nodePath := make(ProvenanceChain, len(path), len(path)+1)
	copy(nodePath, path)
	nodePath = append(nodePath, node)
	*touched = append(*touched, node)

	if depth > maxCompositionDepth {
		return "", &CompositionError{
			Chain: nodePath,
			Skill: label,
			Err:   fmt.Errorf("composition recursion backstop exceeded (max depth %d) — this should have been rejected at install time", maxCompositionDepth),
		}
	}

	params, err := ResolveSkillParameters(ctx, def, input.AgentID, input.StaticArgs, input.Workdir, deps.Resolvers)
	if err != nil {
		return "", &CompositionError{Chain: nodePath, Skill: label, Err: err}
	}
	body := substituteParameters(def.Prompt, params)

	depsJSON, err := json.Marshal(def.Dependencies)
	if err != nil {
		return "", &CompositionError{Chain: nodePath, Skill: label, Err: fmt.Errorf("marshal declared dependencies: %w", err)}
	}
	resolved, err := ResolveDependencyAddresses(deps.Index, string(depsJSON))
	if err != nil {
		return "", &CompositionError{Chain: nodePath, Skill: label, Err: err}
	}

	var out strings.Builder
	out.WriteString(body)
	for _, rd := range resolved {
		if deps.Vendor == nil {
			return "", &CompositionError{Chain: nodePath, Skill: label, Err: fmt.Errorf("resolve dependency %q: no VendorReader configured", rd.Slug)}
		}
		nestedDef, err := loadDependencyDefinition(deps.Vendor, rd)
		if err != nil {
			return "", &CompositionError{Chain: nodePath, Skill: label, Err: fmt.Errorf("load dependency %q: %w", rd.Slug, err)}
		}

		kind := nestedDef.Context
		if kind != "inline" && kind != "fork" {
			// Defensive default matching ParseMD's own default-to-inline
			// behavior — should not occur for an installed package
			// (install-time validation already rejects any other value),
			// but a re-parsed Definition here has no re-validation pass
			// of its own.
			kind = "inline"
		}

		nestedContent, err := materializeOne(ctx, deps, *nestedDef, input, nodePath, touched, depth+1, kind)
		if err != nil {
			return "", err
		}

		switch kind {
		case "fork":
			result, err := runFork(ctx, deps, nestedDef.Slug, nestedContent, input)
			if err != nil {
				return "", &CompositionError{Chain: append(nodePath, ProvenanceLink{Slug: nestedDef.Slug, Kind: "fork"}), Skill: nestedDef.Slug, Err: err}
			}
			out.WriteString(composedSectionHeader(nestedDef.Slug, "fork"))
			out.WriteString(result)
		default: // "inline"
			out.WriteString(composedSectionHeader(nestedDef.Slug, "inline"))
			out.WriteString(nestedContent)
		}
	}

	return out.String(), nil
}

// composedSectionHeader marks the insertion point for a spliced-in
// composed dependency's content. No established Agent-Skills-spec
// convention governs where a composed skill's content is meant to
// appear relative to its parent's own body — confirmed against the real
// spec during this task (platform.claude.com/docs/en/agents-and-tools/
// agent-skills/overview): the spec's own "compose capabilities" language
// describes Claude choosing to read multiple *independent* SKILL.md
// files via bash as separate context loads, not a wire-level splicing/
// insertion convention for embedding one skill's body inside another's.
// This is this task's own concrete choice, following the same
// "genuinely unspecified — pick something concrete, document it"
// precedent TASKS/skills/04 set for the dependencies: frontmatter key:
// append each composed dependency's content after the parent's own
// body, under a clearly-labeled heading naming the dependency's slug and
// the composition kind that pulled it in, so a human or model reading
// the materialized output can always tell "this part is the parent's
// own instructions" from "this part came from a composed dependency,
// and by which mode" without ambiguity.
func composedSectionHeader(slug, kind string) string {
	return fmt.Sprintf("\n\n## Composed skill: %s (%s)\n\n", slug, kind)
}

// loadDependencyDefinition reads a resolved dependency's vendored bytes
// and re-parses its own SKILL.md into a Definition — "Materialization
// always reads the vendored copy live, every time a skill is used"
// (20-skills.md's "The model" section), never trusting a value cached
// at install time. rd.Skill.Slug backstops def.Slug when the
// re-parsed frontmatter is somehow empty (should not happen for an
// installed package, since install-time validation requires a
// resolvable slug, but ParseMD alone has no filename fallback to fall
// back on the way ParseMDFile/ParsePackageDir do for a bare byte slice).
func loadDependencyDefinition(vendor VendorReader, rd DependencyAddress) (*Definition, error) {
	files, err := vendor.ReadFiles(rd.Address)
	if err != nil {
		return nil, err
	}
	data, ok := files[skillFileName]
	if !ok {
		return nil, fmt.Errorf("vendored package at %q has no %s", rd.Address, skillFileName)
	}
	def, err := ParseMD(data)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", skillFileName, err)
	}
	if def.Slug == "" {
		def.Slug = rd.Skill.Slug
	}
	def.SourceRef = rd.Address
	return def, nil
}

// runFork delegates a fork-composed dependency's already-materialized
// content to a real subagent turn and returns only that invocation's
// terminal result — never its full transcript, per 20-skills.md's "Real
// delegation, not text-splicing."
//
// materializedContent (the nested skill's own recursively materialized
// body, exactly as inline composition would have spliced it) becomes
// the subagent's Prompt: the delegated agent turn works through that
// skill's own instructional content in its own isolated context,
// exactly as if it had been the skill's direct invoker, and produces its
// own result — which is what folds back, not the raw instructional text
// itself. This is the real distinguishing behavior between inline and
// fork: both produce the nested skill's materialized content the same
// way; inline splices that text directly into the parent's own context,
// fork instead hands it to a separate agent turn to act on and folds
// back only what that turn produced.
//
// The result is read from the real, durable Run.ResultJSON field
// (internal/subagent.Service's own persisted contract — see that
// package's structuredResultJSON: {"summary": ...} when the runner
// returned only a Result.Summary, or the runner's own structured
// ResultJSON verbatim otherwise), retrieved through the public,
// already-live Status(ctx, runID) API — not a private,
// child-session-message-scraping heuristic. subagent.Service's own
// ModeSync branch already blocks Spawn until the run is terminal, so by
// the time Spawn returns, Status's row is (bar a vanishingly rare race)
// already final.
func runFork(ctx context.Context, deps MaterializerDeps, slug, materializedContent string, input MaterializeInput) (string, error) {
	if deps.Subagent == nil {
		return "", fmt.Errorf("fork composition for skill %q: %w", slug, ErrForkNotConfigured)
	}
	if input.ParentSessionID == "" {
		return "", fmt.Errorf("fork composition requires a ParentSessionID")
	}
	if input.ForkRole == "" {
		return "", fmt.Errorf("fork composition requires a ForkRole (no default role is assumed)")
	}

	req := subagent.SpawnRequest{
		ParentSessionID: input.ParentSessionID,
		ParentAgentID:   input.ParentAgentID,
		Role:            input.ForkRole,
		Prompt:          materializedContent,
		Mode:            subagent.ModeSync,
		AgentProfileID:  input.AgentProfileID,
		TimeoutSeconds:  input.ForkTimeoutSeconds,
	}
	runID, err := deps.Subagent.Spawn(ctx, req)
	if err != nil {
		return "", fmt.Errorf("spawn fork subagent: %w", err)
	}

	run, err := deps.Subagent.Status(ctx, runID)
	if err != nil {
		return "", fmt.Errorf("fetch fork subagent run %q status: %w", runID, err)
	}
	if run == nil {
		return "", fmt.Errorf("fork subagent run %q not found after spawn", runID)
	}
	if !subagent.IsTerminalStatus(run.Status) {
		return "", fmt.Errorf("fork subagent run %q for skill %q did not terminate synchronously (status=%s)", runID, slug, run.Status)
	}
	if run.Status != subagent.StatusCompleted {
		msg := run.Error
		if msg == "" {
			msg = "no error detail captured"
		}
		return "", fmt.Errorf("fork subagent run %q for skill %q ended %s: %s", runID, slug, run.Status, msg)
	}

	result, ok := forkResultText(run)
	if !ok {
		return "", fmt.Errorf("fork subagent run %q for skill %q completed with no result to fold back", runID, slug)
	}
	return result, nil
}

// forkResultText extracts the foldable-back result text from a
// terminal, completed subagent Run's persisted ResultJSON. Mirrors
// internal/subagent.structuredResultJSON's own {"summary": ...} fallback
// shape (unexported in that package, so reproduced here rather than
// exported cross-package purely for this one read) — when the runner
// returned a genuinely structured ResultJSON (not that fallback shape),
// it's folded back verbatim rather than discarded, since "the result"
// isn't necessarily prose.
func forkResultText(run *subagent.Run) (string, bool) {
	if run == nil || run.ResultJSON == "" || run.ResultJSON == "{}" {
		return "", false
	}
	var payload struct {
		Summary string `json:"summary"`
	}
	if err := json.Unmarshal([]byte(run.ResultJSON), &payload); err == nil && payload.Summary != "" {
		return payload.Summary, true
	}
	return run.ResultJSON, true
}

// ErrForkNotConfigured is a sentinel a caller can errors.Is against a
// CompositionError's Unwrap chain (runFork wraps it directly) to
// distinguish "this composition needed fork delegation but no
// SubagentDispatcher was configured" from any other materialization
// failure — a documented, stable classification point for a future
// caller (task 09, or task 11's self-tool) that wants to branch on this
// specific cause without string-matching an error message.
var ErrForkNotConfigured = errors.New("skill: fork composition dependency not configured")
