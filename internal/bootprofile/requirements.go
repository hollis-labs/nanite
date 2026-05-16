package bootprofile

import (
	"context"
	"errors"
	"fmt"
)

// ErrRequirementUnsupported is returned by ResolveRequirements when a
// LaunchSpec carries a Requirement whose Type has no resolver wired.
//
// CW-20260514-0048 originally shipped with text + static slots
// resolved at compile time and cmd / http / role_summary / skill_index
// surfacing as Requirements that ResolveRequirements rejected with
// this sentinel.
//
// CW-20260515-0026 wires all four deferred kinds through the shared
// go-agent-context resolvers, so a Requirement whose Type is one of
// cmd / http / role_summary / skill_index now resolves rather than
// erroring. The sentinel is KEPT for two reasons:
//
//   - a Requirement carrying a genuinely-unknown Type (a future slot
//     kind, or a malformed catalog entry) still fails fast with a
//     pointed message, and
//   - existing callers branch on errors.Is(err, ErrRequirementUnsupported);
//     keeping the sentinel means that branch stays valid.
var ErrRequirementUnsupported = errors.New("bootprofile: requirement source not yet supported")

// ResolveRequirements drains a compiled LaunchSpec's Requirement list
// at launch time. It is the back-compat entry point — it delegates to
// ResolveRequirementsContext with a background context so the two
// existing call sites (chat_bootprofile_resolve.go,
// chat_bootprofile_recovery.go) do not need to thread a context.
//
// Behavior:
//
//   - spec == nil OR len(spec.Requirements) == 0 → returns nil; the
//     cached BootPrompt rendered at compile time is already complete.
//   - any Requirement present → resolves each deferred slot
//     (cmd / http / role_summary / skill_index) through the shared
//     go-agent-context provider, folds the resolved bodies into
//     spec.Slots, clears spec.Requirements, and re-renders
//     spec.BootPrompt. spec is mutated in place.
//   - a resolver-level failure (cmd non-zero exit, HTTP non-2xx,
//     missing role file, …) returns a pointed error naming the slot;
//     spec is left unmutated in that case.
//   - a Requirement carrying an unknown Type returns an error wrapping
//     ErrRequirementUnsupported.
func ResolveRequirements(spec *LaunchSpec) error {
	return ResolveRequirementsContext(context.Background(), spec)
}

// ResolveRequirementsContext is the context-aware form of
// ResolveRequirements. The context is threaded into the shared cmd /
// http resolvers so a caller can abort a slow boot-slot resolution
// (a hung cmd, a slow HTTP endpoint) by cancelling ctx.
//
// On success, spec.Requirements is emptied, the resolved bodies are
// merged into spec.Slots, and spec.BootPrompt is re-rendered from the
// now-complete slot set. spec.{Substitute}-style {{var}} templating is
// NOT re-applied to the resolved bodies — cmd output, HTTP responses,
// role summaries, and skill indexes are live data, not catalog
// templates, and Nanite's pre-port deferred-resolver contract never
// templated them.
//
// CW-20260515-0026: the actual resolution rides
// agentcontext_adapter.go's assembleRequirements, which wires the
// shared resolvers package. requirements.go owns only the
// LaunchSpec-shaped glue: the unknown-type guard, the slot merge, and
// the BootPrompt re-render.
func ResolveRequirementsContext(ctx context.Context, spec *LaunchSpec) error {
	if spec == nil {
		return nil
	}
	if len(spec.Requirements) == 0 {
		return nil
	}

	// Guard: every Requirement Type must map to a shared resolver.
	// An unknown type fails fast with ErrRequirementUnsupported so a
	// malformed catalog entry surfaces before any resolver runs.
	for _, req := range spec.Requirements {
		switch req.Type {
		case "cmd", "http", "role_summary", "skill_index":
			// supported — handled by the shared provider.
		default:
			return fmt.Errorf("requirement resolution failed: profile %q slot %q via source %q: %w",
				spec.ProfileID, req.Slot, req.Type, ErrRequirementUnsupported)
		}
	}

	resolved, err := assembleRequirements(ctx, spec.Requirements, spec.Workdir)
	if err != nil {
		return fmt.Errorf("boot profile %q requirement resolution: %w", spec.ProfileID, err)
	}

	// Fold the resolved bodies into spec.Slots. A deferred slot name
	// never collides with a compile-time slot name (the compiler
	// routes each profile slot to exactly one of the two paths), so a
	// plain merge is correct.
	for name, body := range resolved {
		spec.Slots[name] = body
	}

	// All requirements drained — clear the list and re-render the
	// prompt from the now-complete slot set. renderDefaultPrompt is
	// the same renderer Compile uses when a profile has no deferred
	// slots, so the "all compile-time" and "had deferred slots" boot
	// prompts are produced by one code path.
	spec.Requirements = spec.Requirements[:0]
	spec.BootPrompt = renderDefaultPrompt(spec)
	return nil
}
