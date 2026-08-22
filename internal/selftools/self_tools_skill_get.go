package selftools

// self_tools_skill_get.go — TASKS/skills/11
// (TASKS/skills/11-api-direct-skill-get-self-tool.md): the API-direct
// entry point into the Resolver -> Materializer -> Policy/Sandbox ->
// Materialized Skill pipeline docs/engineering/architecture/20-skills.md's
// "The invocation gap" section names — "an agent calls it with a slug plus
// parameters, gets back fully materialized content... as a tool result."
//
// callSkillGet is the first real caller wiring the whole pipeline
// together, in order:
//
//  1. internal/skill.Gate.Authorize (task 09) — an unconditional, top-level
//     grant check against the REQUESTED (root) skill's own slug, run before
//     any resolution/materialization work happens at all. This is load-
//     bearing on its own, independent of step 3 below: a skill with no
//     `` !`cmd` `` markers or scripts/ entries — plain instructional
//     content, almost certainly the common case — never reaches the Gate
//     any other way, since ResolveInlineMarkers (step 3) is a documented
//     no-op on a body containing no markers at all. Without this explicit
//     check, an ungranted or stale-approval agent would receive full skill
//     content with zero enforcement for every marker-free skill.
//  2. internal/skill.MaterializeSkill (task 07) — parameter resolution
//     (task 06, via MaterializerDeps.Resolvers/Index) plus inline/fork
//     composition, against the root Definition loaded live from the
//     vendored store (never a value cached at install time, per
//     20-skills.md's "The model" section).
//  3. internal/skill.ResolveInlineMarkers (task 08) — executes any
//     `` !`cmd` `` marker present in the FINAL composed output (the root's
//     own body, or a spliced-in inline dependency's) through the same Gate,
//     attributed to the ROOT skill's slug — see loadRootSkillDefinition's
//     own doc comment for why that attribution is correct, not an
//     oversight.
//
// Note that MaterializeSkill (task 07) does NOT itself call
// ResolveInlineMarkers (task 08) — confirmed directly against
// internal/skill/compose.go's materializeOne, which only resolves
// parameters and splices/delegates dependencies, never touching markers.
// exec.go's own doc comment anticipates exactly this: "a future
// end-to-end Materializer may call [ResolveInlineMarkers] after task 07's
// compose.go has already spliced inline dependencies in." This file is
// that end-to-end caller — steps 2 and 3 above are two separate calls in
// sequence, not one combined pipeline call.
import (
	"context"
	"errors"
	"fmt"

	"github.com/hollis-labs/nanite/internal/mcp"
	"github.com/hollis-labs/nanite/internal/skill"
	"github.com/hollis-labs/nanite/internal/skillvendor"
	"github.com/hollis-labs/nanite/internal/store"
)

// skillMDFileName mirrors internal/skill's own unexported skillFileName
// constant (parser.go) — kept as this package's own local copy rather than
// exporting that package's private convention purely for this one read,
// matching this batch's own established precedent elsewhere for a small,
// call-site-specific duplication (e.g. internal/skill/gate.go's own
// filterSecretEnv vs internal/sandbox's unexported filterSecrets).
const skillMDFileName = "SKILL.md"

// callSkillGet implements skill_get. See this file's package doc for the
// full pipeline this assembles.
func (st *SelfToolsTransport) callSkillGet(ctx context.Context, args map[string]any) (*mcp.ToolResult, error) {
	if st.Store == nil {
		return mcp.ErrorResult("skill_get: no store configured"), nil
	}
	if st.SkillVendor == nil {
		return mcp.ErrorResult("skill_get: no vendored skill store configured"), nil
	}

	slug := strArg(args, "slug", "")
	if slug == "" {
		return mcp.ErrorResult("slug is required"), nil
	}

	// Caller identity is authoritative from ctx, never a caller-supplied
	// arg — matching every other identity-sensitive self-tool in this
	// transport (procedure_get's identical pattern; subagent_spawn's
	// parent_agent_id). A forged/guessed agent ID must not be able to read
	// a skill granted to a different agent.
	agentID := mcp.CallerProfileFromContext(ctx)
	if agentID == "" {
		return mcp.ErrorResult("skill_get: no calling agent in context"), nil
	}

	sk, err := st.Store.GetSkillBySlug(slug)
	if err != nil {
		return mcp.ErrorResult(fmt.Sprintf("skill_get: look up skill %q: %v", slug, err)), nil
	}
	if sk == nil {
		return mcp.ErrorResult(fmt.Sprintf("skill %q not found in the skill catalog", slug)), nil
	}
	if !sk.Enabled {
		return mcp.ErrorResult(fmt.Sprintf("skill %q is disabled", slug)), nil
	}
	if sk.ContentHash == "" {
		return mcp.ErrorResult(fmt.Sprintf("skill %q has not been installed/vendored yet — nothing to fetch", slug)), nil
	}

	// Step 1: the unconditional, top-level grant check — see this file's
	// package doc for why this cannot be skipped even for a marker-free
	// skill. NewGate is cheap (two DI store references, no state of its
	// own) — constructed fresh per call, matching this batch's established
	// "build fresh per invocation" convention for install-adjacent
	// pipeline objects (service.Container's own doc comment on
	// *skillinstall.Installer).
	gate := skill.NewGate(st.Store, st.Store)
	if _, err := gate.Authorize(ctx, slug, agentID); err != nil {
		return mcp.ErrorResult(fmt.Sprintf("skill_get: %v", err)), nil
	}

	def, pkgDir, err := loadRootSkillDefinition(st.SkillVendor, sk)
	if err != nil {
		return mcp.ErrorResult(fmt.Sprintf("skill_get: %v", err)), nil
	}

	// Step 2: Resolver + Materializer (tasks 06/07). Workdir is left empty
	// — an API-direct call has no session boot dir or project working
	// directory to thread through the way chat_boot_drive.go's boot-time
	// resolution does (per docs/engineering/architecture/20-skills.md's
	// "Delivery" section, API-direct agents have no boot dir at all); this
	// only matters for a `cmd`-kind agent_context_resolvers row that
	// itself omits a CWD, which falls back to this value.
	deps := skill.MaterializerDeps{
		Resolvers: st.Store,
		Index:     st.Store,
		Vendor:    st.SkillVendor,
		Subagent:  st.Subagent,
	}
	input := skill.MaterializeInput{
		AgentID:         agentID,
		StaticArgs:      skillGetParamsArg(args),
		ParentSessionID: mcp.SessionIDFromContext(ctx),
		ParentAgentID:   agentID,
		AgentProfileID:  agentID,
		// ForkRole has no established default anywhere in this codebase
		// (internal/skill/compose.go's own doc comment: "the caller ...
		// is the only party that knows what role should execute the
		// delegated skill") — exposed as this tool's own optional
		// fork_role argument rather than inventing one silently. See this
		// task's Work Log for the full record of this design-latitude
		// call.
		ForkRole:           strArg(args, "fork_role", ""),
		ForkTimeoutSeconds: mcp.IntArg(args, "fork_timeout_seconds", 0),
	}

	materialized, err := skill.MaterializeSkill(ctx, deps, *def, input)
	if err != nil {
		return skillGetMaterializeErrorResult(slug, err), nil
	}

	// Step 3: marker execution (task 08), against the FINAL composed
	// content — see this file's package doc for why this is a separate
	// call from MaterializeSkill, and pkgDir (the root skill's own
	// vendored package directory) is used as the exec working directory
	// so a marker can reference the package's own scripts/references by
	// relative path, matching ExecuteScript's own pkgDir convention.
	resolved, err := skill.ResolveInlineMarkers(ctx, gate, *def, agentID, pkgDir, materialized.Content, skill.ExecOptions{})
	if err != nil {
		return mcp.ErrorResult(fmt.Sprintf("skill_get: resolve inline markers: %v", err)), nil
	}

	return mcp.TextResult(resolved), nil
}

// loadRootSkillDefinition reads sk's vendored package and re-parses its
// SKILL.md into a skill.Definition — "Materialization always reads the
// vendored copy live, every time a skill is used" per
// docs/engineering/architecture/20-skills.md's "The model" section.
// Mirrors internal/skill/compose.go's own unexported loadDependencyDefinition
// (used there for a NESTED dependency during composition), applied here to
// the ROOT skill instead, since MaterializeSkill's own entry point takes an
// already-loaded root Definition rather than an address to load one from
// itself.
//
// Every marker ResolveInlineMarkers later finds in the final composed
// output — including one that originated inside an inline-spliced
// dependency's own body — is attributed to THIS root Definition's slug for
// the Gate's grant lookup, never a nested dependency's own slug. This is a
// deliberate property of composition itself (task 07), not something this
// file narrows or widens: "inline" composition is pure content splicing
// ("before the model ever sees either," 20-skills.md's "Composition"
// section) — once spliced, the nested content is indistinguishable from
// the parent's own body, and there is no per-line provenance for
// ResolveInlineMarkers to key a different grant lookup on. A skill package
// that wants its own markers executed under its own, separately-granted
// identity uses `fork` composition instead, which carries a real, separate
// trust boundary (subagent.SpawnRequest's own trust resolution).
func loadRootSkillDefinition(vendor *skillvendor.Store, sk *store.Skill) (def *skill.Definition, pkgDir string, err error) {
	pkgDir, err = vendor.Path(sk.ContentHash)
	if err != nil {
		return nil, "", fmt.Errorf("resolve vendored package path for %q: %w", sk.Slug, err)
	}
	files, err := vendor.ReadFiles(sk.ContentHash)
	if err != nil {
		return nil, "", fmt.Errorf("read vendored package for %q: %w", sk.Slug, err)
	}
	data, ok := files[skillMDFileName]
	if !ok {
		return nil, "", fmt.Errorf("vendored package for %q has no %s", sk.Slug, skillMDFileName)
	}
	def, err = skill.ParseMD(data)
	if err != nil {
		return nil, "", fmt.Errorf("parse %s for %q: %w", skillMDFileName, sk.Slug, err)
	}
	if def.Slug == "" {
		def.Slug = sk.Slug
	}
	def.SourceRef = sk.ContentHash
	return def, pkgDir, nil
}

// skillGetParamsArg decodes the optional `params` object argument into the
// flat map[string]string internal/skill.ResolveSkillParameters expects
// (task 06). A JSON object's values arrive as map[string]any
// (bool/float64/string/nil, per encoding/json's own decoding convention);
// each is rendered via fmt.Sprintf("%v", ...) except a bare string, which
// passes through unquoted so a plain string parameter value round-trips
// exactly as the caller wrote it.
func skillGetParamsArg(args map[string]any) map[string]string {
	raw, ok := args["params"].(map[string]any)
	if !ok || len(raw) == 0 {
		return nil
	}
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		if s, ok := v.(string); ok {
			out[k] = s
			continue
		}
		out[k] = fmt.Sprintf("%v", v)
	}
	return out
}

// skillGetMaterializeErrorResult formats a MaterializeSkill failure for the
// tool result. A *skill.ForkPendingApprovalError anywhere in the error
// chain — the common, by-design outcome for any fork-composed dependency
// under this deployment's default (SubagentApprovalRequired=true) trust
// policy, not a rare failure (internal/skill/compose.go's own doc comment
// on runFork) — gets a distinct, actionable message naming the pending run
// so the caller can poll subagent_status rather than reading a generic
// "materialization failed."
func skillGetMaterializeErrorResult(slug string, err error) *mcp.ToolResult {
	var pending *skill.ForkPendingApprovalError
	if errors.As(err, &pending) {
		return mcp.ErrorResult(fmt.Sprintf(
			"skill_get: materialize skill %q: fork-composed dependency %q is pending human approval (run_id=%s, envelope_instance_id=%s, status=%s) — poll subagent_status(run_id=%q) once approved, then call skill_get again; no content is available yet",
			slug, pending.Slug, pending.RunID, pending.EnvelopeInstanceID, pending.Status, pending.RunID,
		))
	}
	return mcp.ErrorResult(fmt.Sprintf("skill_get: materialize skill %q: %v", slug, err))
}
