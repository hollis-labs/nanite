package bootprofile

import (
	"errors"
	"fmt"
)

// ErrRequirementUnsupported is returned by ResolveRequirements when a
// LaunchSpec carries a Requirement entry whose Type the launch-time
// resolver does not implement. CW-20260514-0048 ships with text and
// static slot sources fully resolved at compile time; cmd / http /
// role_summary / skill_index slots surface as Requirements and the
// resolver returns this sentinel rather than silently emitting a
// half-rendered spec.
//
// Callers can branch on errors.Is(err, ErrRequirementUnsupported) to
// surface a pointed configuration message ("this boot profile uses a
// slot source we haven't wired yet").
var ErrRequirementUnsupported = errors.New("bootprofile: requirement source not yet supported")

// ResolveRequirements drains a compiled LaunchSpec's Requirement list
// at launch time. The 0048 scope explicitly defers dynamic resolvers
// (cmd / http / role_summary / skill_index) per the ticket guidance
// "a clean error is better than a half-implemented resolver".
//
// Behavior:
//
//   - len(spec.Requirements) == 0 → returns nil; the cached BootPrompt
//     (rendered at compile time) is already complete.
//   - any Requirement present → returns an error wrapping
//     ErrRequirementUnsupported with the slot name + source type so the
//     operator gets a pointed diagnostic. The first failing slot wins
//     so the error message stays single-cause.
//
// This function does NOT mutate spec. A future ticket that implements
// real resolvers will replace this with an in-place substitution into
// spec.Slots + a re-render of spec.BootPrompt. The signature is
// intentionally compatible with that future shape (it takes the spec
// pointer and returns an error) so the call sites in 0048 do not need
// to change when the implementation grows.
//
// CW-20260514-0048 scope decision pinned here so a future grep for
// "ResolveRequirements" lands on the explanation directly.
func ResolveRequirements(spec *LaunchSpec) error {
	if spec == nil {
		return nil
	}
	if len(spec.Requirements) == 0 {
		return nil
	}
	first := spec.Requirements[0]
	return fmt.Errorf("requirement resolution failed: profile %q slot %q via source %q: %w",
		spec.ProfileID, first.Slot, first.Type, ErrRequirementUnsupported)
}
