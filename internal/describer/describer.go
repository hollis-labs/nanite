// Package describer carries the per-call description-render contract
// (CW-20260512-0105 / SP-20260512-0008 W1B) for the Tool Broker.
//
// Most tools have static descriptions: a single string fixed at registration
// time. A small set of dispatch-shaped tools (task_execute, tool_list,
// skill_list) need their descriptions to reflect caller-specific state —
// most importantly, the parent agent's dispatch allowlist for sibling roles.
// The user's framing: the Tool Broker is a cognitive-offload primitive — the
// agent shouldn't burn tokens reasoning about "do I have permission to
// dispatch researcher", the description should render the allowlist directly.
//
// Why a leaf package? Both internal/toolclient (which holds the Registry
// on the ToolClient struct and runs the materialization-site Render call)
// and internal/mcp (which authors the Describers for the initial adopter
// set) need the contract types. Putting them on either side would create
// an import cycle, since toolclient.ToolClient already references mcp.Manager.
// The leaf package is the smallest set of types that breaks the cycle: it
// imports only "context" + "sync" from the stdlib.
//
// Design:
//
//   - Opt-in. Tools that need dynamic descriptions register a Describer
//     keyed by tool name in a Registry. Tools that don't are unchanged.
//   - Per-call. The materialization site (service-layer tool selection)
//     calls Render on the final filtered tool set, passing a CallerAgent
//     that carries the parent agent's identity + dispatch allowlist +
//     extensible Metadata.
//   - Fallback-safe. A Describer that returns "" leaves the static
//     description in place — the agent always sees *something*. Static
//     tools (no Describer registered) emit their definition-time
//     description verbatim.
//
// Sharp edge — cacheable prefix.
// Per-call descriptions land OUTSIDE the cacheable prefix unless the tool
// block is placed accordingly. Coordinate with W3 (CW-20260512-0109,
// cache-marker priority): tool descriptions get cache markers only when
// they're stable enough; opt-in dynamic descriptions should NOT shrink the
// cacheable prefix in practice. The materialization site documents this
// position constraint; W3 codifies it in cache-marker priority.
//
// Downstream consumer: W2A Agent Broker (CW-20260512-0107) will populate
// CallerAgent.DispatchAllowlist from the parent agent's role-derived
// allowlist when spawning subagents. Until that ticket lands, the
// allowlist is empty by default and Describers degrade gracefully (the
// task_execute Describer renders a baseline description without role
// enumeration).
package describer

import (
	"context"
	"sync"
)

// CallerAgent carries the per-call context a Describer needs to render a
// caller-specific description. Fields are additive — adding a new field is
// not a breaking change because Describers ignore fields they don't read.
//
// W2A Agent Broker (CW-20260512-0107) is the downstream consumer that
// populates DispatchAllowlist from the parent agent's role profile.
type CallerAgent struct {
	// ID is the agent profile ID of the caller. Stable identifier; for
	// file-based agents this has the "file-" prefix.
	ID string

	// Slug is the agent profile slug (e.g. "default" for the chat-role
	// agent, "worker" for the worker role). Describers key behavior off
	// the slug, not the opaque ID.
	Slug string

	// DispatchAllowlist enumerates the role slugs this caller may dispatch
	// to (e.g. ["worker", "planner", "researcher"]). Populated by W2A
	// Agent Broker; empty for callers without explicit dispatch policy.
	// Describers that render dispatch-shaped tools (task_execute) read
	// this list directly into their rendered description.
	DispatchAllowlist []string

	// Metadata is an extension point for fields not yet first-class on
	// CallerAgent. Describers should prefer first-class fields; Metadata
	// is for short-lived experimental signals or sibling-sprint trial
	// plumbing. Keys are caller-defined; absent keys are normal.
	Metadata map[string]any
}

// Describer renders a per-call description for a tool. Implementations are
// expected to be fast (read-only field access; no I/O on the hot path) and
// deterministic — for a given (toolName, CallerAgent) the rendered string
// should be stable so cache-marker placement upstream can rely on it.
//
// Returning the empty string is a signal to fall back to the tool's
// static (registration-time) description. This lets a Describer no-op
// for callers it doesn't have specialized rendering for.
type Describer interface {
	Describe(ctx context.Context, caller CallerAgent) string
}

// Func adapts an ordinary function to the Describer interface. Most
// Describers can be written as a single function.
type Func func(ctx context.Context, caller CallerAgent) string

// Describe satisfies the Describer interface.
func (f Func) Describe(ctx context.Context, caller CallerAgent) string {
	return f(ctx, caller)
}

// Registry is a tool-name → Describer map used by the materialization
// site to render per-call descriptions. Safe for concurrent
// Register / Get; tools typically register at startup and the hot
// path is read-only thereafter.
//
// The registry is opt-in: tools without a Describer fall through to
// their static description. There is no global default Describer —
// that would defeat the purpose of opt-in.
type Registry struct {
	mu  sync.RWMutex
	fns map[string]Describer
}

// NewRegistry creates an empty Registry.
func NewRegistry() *Registry {
	return &Registry{fns: make(map[string]Describer)}
}

// Register associates a Describer with a tool name. Last write wins —
// re-registering the same name overwrites the previous Describer. This
// is intentional for test fixtures and mux/devmode overrides; production
// code should register each tool exactly once at startup.
func (r *Registry) Register(toolName string, d Describer) {
	if r == nil || toolName == "" || d == nil {
		return
	}
	r.mu.Lock()
	r.fns[toolName] = d
	r.mu.Unlock()
}

// Get returns the Describer for toolName, if registered. The bool return
// is the opt-in signal: callers use Has-or-Get to decide whether to call
// the Describer or keep the static description.
func (r *Registry) Get(toolName string) (Describer, bool) {
	if r == nil {
		return nil, false
	}
	r.mu.RLock()
	d, ok := r.fns[toolName]
	r.mu.RUnlock()
	return d, ok
}

// Has reports whether toolName has a Describer registered. Equivalent to
// the bool return from Get, but with no Describer-value allocation.
func (r *Registry) Has(toolName string) bool {
	if r == nil {
		return false
	}
	r.mu.RLock()
	_, ok := r.fns[toolName]
	r.mu.RUnlock()
	return ok
}

// Count returns the number of Describers registered. Used by startup
// logging and tests.
func (r *Registry) Count() int {
	if r == nil {
		return 0
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.fns)
}
