package bootprofile

import (
	"context"
	"fmt"
	"time"

	"github.com/hollis-labs/go-agent-context/agentcontext"
	"github.com/hollis-labs/go-agent-context/agentcontext/resolvers"
)

// agentcontext_adapter.go is the bridge between Nanite's boot-profile
// slot model and the shared go-agent-context slot-resolution
// primitives (CW-20260515-0024 Phase 6 port).
//
// Why an adapter and not a wholesale replacement:
//
// The shared agentcontext package owns a complete, generic slot
// pipeline (ContextRequest → DefaultProvider.Assemble → ContextResult)
// with its own SlotSourceKind taxonomy, its own Renderer, and its own
// budget model. Nanite's bootprofile package has a NARROWER,
// app-specific contract that downstream Nanite code (the dropdown
// surface, the chat-runtime hookup, the Registry) depends on exactly:
//
//   - the Nanite SlotSource YAML schema (`type: text|static|cmd|...`),
//   - the LaunchSpec output shape (flat, JSON-serializable),
//   - Nanite's deferred-Requirement model (cmd/http/role_summary/
//     skill_index surface as structured Requirements, not errors),
//   - Nanite's `### filename` static-directory concat format,
//   - Nanite's strict `{{var}}` substitution applied to BOTH inline
//     text and file bodies.
//
// Those are Nanite UX / business-logic semantics and must not change
// (CW-20260515-0024 acceptance criteria: no regression). So the port
// keeps Nanite's schema + LaunchSpec + Requirement model intact and
// moves only the MECHANICAL FILE/INLINE IO onto the shared resolvers:
//
//   - Nanite `text`   slot → agentcontext SlotSourceKindInline   resolver
//   - Nanite `static` file → agentcontext SlotSourceKindStaticFile resolver
//
// The shared resolvers do the os.ReadFile / tilde-expansion /
// workdir-join work; the Nanite-specific bits (var substitution, the
// `### filename` directory format, Requirement lifting) stay here as a
// thin adapter. A `static` slot pointing at a directory still uses
// Nanite's own concat because the shared StaticDirResolver emits a
// plain "\n\n"-joined body without the per-file headings Nanite's
// tests + downstream prompt layout pin.

// sharedResolverEnv builds the agentcontext.ResolverEnv for a slot
// resolution. Nanite resolves relative static paths against the
// catalog root (not a process workdir), so catalogRoot flows in as
// the resolver Workdir.
func sharedResolverEnv(catalogRoot string) agentcontext.ResolverEnv {
	return agentcontext.ResolverEnv{Workdir: catalogRoot}
}

// staticFileResolver / inlineResolver are the shared resolvers Nanite
// delegates mechanical IO to. They are stateless and goroutine-safe,
// so package-level singletons are fine.
var (
	staticFileResolver = resolvers.NewStaticFileResolver()
	inlineResolver     = resolvers.NewInlineResolver()
)

// resolveInlineViaShared resolves a Nanite `text` slot body through
// the shared inline resolver. The shared resolver copies the content
// verbatim; Nanite's `{{var}}` substitution is applied by the caller
// (resolveSlot) AFTER this returns, matching the pre-port behavior
// where text content was substituted at the slot layer.
func resolveInlineViaShared(name, content string) (string, error) {
	spec := agentcontext.SlotSpec{
		Name: name,
		Source: agentcontext.SlotSource{
			Kind:   agentcontext.SlotSourceKindInline,
			Inline: agentcontext.InlineSource{Content: content},
		},
	}
	res, err := inlineResolver.Resolve(context.Background(), spec, agentcontext.ResolverEnv{})
	if err != nil {
		return "", fmt.Errorf("inline slot resolution: %w", err)
	}
	return res.Content, nil
}

// resolveStaticFileViaShared resolves a single-file Nanite `static`
// slot through the shared static_file resolver. catalogRoot is passed
// as the resolver Workdir so a catalog-relative path resolves the same
// way Nanite's old resolvePath did. The shared resolver expands a
// leading "~" and rejects nothing else; Nanite's strict "relative path
// with empty catalog root" guard is enforced by the caller before
// this is invoked, so a relative path always arrives with a non-empty
// catalogRoot here.
func resolveStaticFileViaShared(name, path, catalogRoot string) (string, error) {
	spec := agentcontext.SlotSpec{
		Name: name,
		Source: agentcontext.SlotSource{
			Kind:       agentcontext.SlotSourceKindStaticFile,
			StaticFile: agentcontext.StaticFileSource{Path: path},
		},
	}
	res, err := staticFileResolver.Resolve(context.Background(), spec, sharedResolverEnv(catalogRoot))
	if err != nil {
		return "", err
	}
	return res.Content, nil
}

// -----------------------------------------------------------------------
// CW-20260515-0026 — deferred-Requirement resolution via the shared
// go-agent-context provider.
//
// CW-0024 left cmd / http / role_summary / skill_index slots as
// deferred Requirements that ResolveRequirements rejected with
// ErrRequirementUnsupported. The shared agentcontext/resolvers package
// already ships mechanically-equivalent resolvers for all four kinds:
//
//   - cmd          → resolvers.CmdResolver        (sh -c, stdout capture)
//   - http         → resolvers.HTTPTextResolver   (HTTP GET, body as text)
//                    or HTTPJSONResolver when response_format is "json"
//   - role_summary → resolvers.RoleSummaryResolver (role markdown + section)
//   - skill_index  → resolvers.SkillIndexResolver  (layered skill discovery)
//
// requirementProvider builds a DefaultProvider wired with exactly those
// four kinds (the skill_index resolver is opt-in via
// resolvers.WithSkillIndex). assembleRequirements turns a Requirement
// list into an agentcontext.ContextRequest, runs Assemble, and returns
// the resolved slot bodies keyed by slot name. Nanite-specific bits —
// {{var}} substitution, the deferred-vs-compile-time split, the
// LaunchSpec re-render — stay in requirements.go.
//
// All four resolvers are app-neutral: they import NO Nanite service or
// store internals. That satisfies CW-0026's strict boundary — the
// shared package gains nothing; Nanite simply consumes resolvers that
// were already there.

// requirementResolverEnv builds the ResolverEnv for Requirement
// resolution. Workdir flows in as the base for cmd CWD defaulting and
// any relative role/skill paths the catalog might use.
func requirementResolverEnv(workdir string) agentcontext.ResolverEnv {
	return agentcontext.ResolverEnv{Workdir: workdir}
}

// requirementProvider is the shared DefaultProvider used to drain a
// LaunchSpec's deferred Requirements. It is wired with the cmd / http
// / role_summary / skill_index resolvers and the default Renderer.
//
// The provider is rebuilt per call (cheap — the resolvers are
// stateless) so a future caller that wants to tune resolver options
// (e.g. a tighter cmd timeout) can do so without a package-level
// singleton getting in the way.
func requirementProvider() (*agentcontext.DefaultProvider, error) {
	res := map[agentcontext.SlotSourceKind]agentcontext.Resolver{
		agentcontext.SlotSourceKindCmd:         resolvers.NewCmdResolver(),
		agentcontext.SlotSourceKindHTTPText:    resolvers.NewHTTPTextResolver(),
		agentcontext.SlotSourceKindHTTPJSON:    resolvers.NewHTTPJSONResolver(),
		agentcontext.SlotSourceKindRoleSummary: resolvers.NewRoleSummaryResolver(),
	}
	// skill_index is opt-in (its on-disk skill model is a layered
	// extension over the core contract). Nanite's boot profiles can
	// use skill_index slots, so wire it.
	res = resolvers.WithSkillIndex(res)
	return agentcontext.NewProvider(res, agentcontext.DefaultRenderer{})
}

// requirementToSlotSpec converts a single Nanite Requirement into a
// shared agentcontext.SlotSpec. The Requirement is the narrow subset
// of SlotSource carrying exactly the knobs each deferred kind needs;
// this maps each onto the matching shared SlotSource sub-struct.
//
// Kind mapping (per the CW-0024 handoff §6):
//
//	cmd          → SlotSourceKindCmd
//	http         → SlotSourceKindHTTPJSON when ResponseFormat == "json",
//	               otherwise SlotSourceKindHTTPText
//	role_summary → SlotSourceKindRoleSummary
//	skill_index  → SlotSourceKindSkillIndex
//
// The slots are NOT marked Required: a deferred slot resolving empty
// is tolerated (the section is simply omitted from the re-rendered
// prompt), matching Nanite's pre-port behavior where an empty static
// slot produced an empty — not failed — section.
func requirementToSlotSpec(req Requirement) (agentcontext.SlotSpec, error) {
	spec := agentcontext.SlotSpec{Name: req.Slot}
	switch req.Type {
	case "cmd":
		timeout, err := parseRequirementTimeout(req.Timeout)
		if err != nil {
			return agentcontext.SlotSpec{}, fmt.Errorf("slot %q: %w", req.Slot, err)
		}
		spec.Source = agentcontext.SlotSource{
			Kind: agentcontext.SlotSourceKindCmd,
			Cmd:  agentcontext.CmdSource{Run: req.Run, Timeout: timeout},
		}
	case "http":
		if req.ResponseFormat == "json" {
			spec.Source = agentcontext.SlotSource{
				Kind:     agentcontext.SlotSourceKindHTTPJSON,
				HTTPJSON: agentcontext.HTTPJSONSource{URL: req.URL},
			}
		} else {
			spec.Source = agentcontext.SlotSource{
				Kind:     agentcontext.SlotSourceKindHTTPText,
				HTTPText: agentcontext.HTTPTextSource{URL: req.URL},
			}
		}
	case "role_summary":
		spec.Source = agentcontext.SlotSource{
			Kind:        agentcontext.SlotSourceKindRoleSummary,
			RoleSummary: agentcontext.RoleSummarySource{Path: req.Path},
		}
	case "skill_index":
		spec.Source = agentcontext.SlotSource{
			Kind: agentcontext.SlotSourceKindSkillIndex,
			SkillIndex: agentcontext.SkillIndexSource{
				Roots: req.Roots,
				Limit: req.Limit,
			},
		}
	default:
		return agentcontext.SlotSpec{}, fmt.Errorf("slot %q: requirement type %q has no shared resolver", req.Slot, req.Type)
	}
	return spec, nil
}

// parseRequirementTimeout converts a Nanite cmd-slot Timeout string
// (e.g. "5s", "2m") into a time.Duration for the shared CmdSource.
// Empty means "resolver default" (zero duration → the CmdResolver's
// DefaultCmdTimeout). A malformed string is a hard error so a typo in
// the catalog surfaces immediately rather than silently defaulting.
func parseRequirementTimeout(s string) (time.Duration, error) {
	if s == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid timeout %q: %w", s, err)
	}
	return d, nil
}

// assembleRequirements resolves a Requirement list through the shared
// agentcontext provider and returns the resolved slot bodies keyed by
// slot name (in the input Requirement order, but the map drops order —
// callers re-impose ordering via canonicalSlotOrder).
//
// workdir is the base directory for cmd CWD defaulting and relative
// path resolution; pass LaunchSpec.Workdir.
//
// Any resolver failure on any slot aborts: the slots are marked
// Required=false in requirementToSlotSpec, but a resolver-level error
// (a cmd that exits non-zero, an HTTP non-2xx, a missing role file)
// still surfaces from Assemble as SlotResult.Err for non-required
// slots. assembleRequirements promotes the FIRST such error to a hard
// failure so an operator gets a pointed diagnostic — a half-resolved
// boot prompt is worse than a clean stop.
func assembleRequirements(ctx context.Context, reqs []Requirement, workdir string) (map[string]string, error) {
	if len(reqs) == 0 {
		return map[string]string{}, nil
	}
	provider, err := requirementProvider()
	if err != nil {
		return nil, fmt.Errorf("build requirement provider: %w", err)
	}

	slots := make([]agentcontext.SlotSpec, 0, len(reqs))
	for _, req := range reqs {
		spec, convErr := requirementToSlotSpec(req)
		if convErr != nil {
			return nil, convErr
		}
		slots = append(slots, spec)
	}

	creq := agentcontext.ContextRequest{
		Slots:   slots,
		Workdir: workdir,
	}
	result, err := provider.Assemble(ctx, creq)
	if err != nil {
		return nil, fmt.Errorf("assemble requirements: %w", err)
	}

	out := make(map[string]string, len(result.Slots))
	for _, sr := range result.Slots {
		if sr.Err != nil {
			return nil, fmt.Errorf("slot %q resolution failed: %w", sr.Name, sr.Err)
		}
		out[sr.Name] = sr.Content
	}
	return out, nil
}
