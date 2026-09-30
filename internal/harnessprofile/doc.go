// Package harnessprofile resolves the harness limits a chat run uses: which
// named profile was selected, what the model implies, what each configuration
// layer overrides, and which layer supplied every final value.
//
// A profile is host-side YAML (the "launch profile" of the agent-fabric
// contract). It carries a limits block in the shared agentcontracts.Limits
// shape and a harness block of knobs only a host that owns the tool loop can
// honor. Built-in profiles are embedded; user profiles are read from a
// directory. Nothing here is seeded through SQL.
//
// Resolution order, lowest to highest:
//
//  1. computed defaults (today's constants; model-derived where a knob scales
//     with the model),
//  2. app settings (the user_settings values that already exist),
//  3. the selected profile and its extends chain, base first,
//  4. per-model blocks of that chain whose pattern matches the model,
//  5. per-agent overrides,
//  6. per-launch overrides,
//  7. environment overrides (NANITE_HARNESS_<KEY>),
//  8. host maximum clamps, applied last to every value.
//
// The most specific layer wins, and a clamp is recorded on the value it
// changed, so a resolved value never lies about where it came from.
//
// The "dev" profile is an ordinary named profile chosen the same way as any
// other. It is deliberately independent of the devmode build tag, which only
// disables plugin signature verification and must never ship: keying a runtime
// profile to a build tag would make shipped and development builds behave
// differently in ways nobody selected.
package harnessprofile
