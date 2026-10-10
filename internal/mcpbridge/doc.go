// Package mcpbridge contains unregistered internal candidate machinery for a
// protocol-2 plugin catalog served by one existing Nanite execution owner.
//
// This package supplies no public feature, actor enrollment, credential bootstrap
// or CLI/API registration. Production adoption still requires a genuine
// host-supplied BindingVerifier and reviewed scoped credential handoff, a policy
// and execution owner implementing current actor/effect/schema/approval checks
// plus actual transaction/receipt guards, and settled public DTO/catalog/cancel
// semantics. Missing ports refuse with typed authority/target-unavailable errors;
// an empty verified catalog is distinct from unsupported execution authority.
//
// context_pin, context_unpin and reminder_set remain unavailable unless their
// owning plugins publish accepted manifest-v2 definitions. Core/profile fallback
// is never authority. DEC-063's unavailable disposition does not itself complete
// usable public stdio adoption. Platform SDK/registry/grant adoption and startup
// ordering already landed separately; this candidate does not repeat them.
package mcpbridge
