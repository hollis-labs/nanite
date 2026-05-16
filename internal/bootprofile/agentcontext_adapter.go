package bootprofile

import (
	"context"
	"fmt"

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
