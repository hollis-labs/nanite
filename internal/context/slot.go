package context

import (
	"crypto/sha256"
	"fmt"
)

// Slot names. Ordered by assembly priority.
const (
	// SlotUniversal is the position-0 universal rules slot emitted by the
	// Context Broker for every dispatch (chat, sync subagent, async
	// subagent, background job). SP-20260512-0008 W1A (CW-20260512-0104)
	// reserved position 0; CW-20260512-0114 wired content via
	// chat.AssembleSlotSources, which sources the block from
	// chat.UniversalRulesBlock(). Non-compactable identity-class slot —
	// keeps the Anthropic cacheable prefix stable across agents.
	SlotUniversal = "universal"
	SlotSystem    = "system"
	SlotMemory    = "memory"
	SlotAgent     = "agent"
	// SlotMode carries the session-level Mode addendum (B1, CW-20260428-0009).
	// Sits between SlotAgent (identity) and SlotRules (policy) — modes
	// modulate the agent's identity but don't override policy. Non-compactable
	// so the active mode survives compaction the same way SlotAgent does.
	// Distinct from the legacy AgentMode addendum that still lands inside
	// SlotAgent — that handles the agent-scoped *store.AgentMode and stays
	// for back-compat. SlotMode is for the session-scoped *store.Mode only.
	SlotMode  = "mode"
	SlotRules = "rules"
	// SlotPermissions carries the per-session path-access summary
	// (CW-20260512-0118, SP-20260512-0010 W2). Sourced via
	// permission.RenderPermissionSummary from the agent's resolved
	// permission.RuleSet, the binary-scoped AllowedPaths list, and the
	// session-scoped PathGrants bucket (own + lineage). The summary makes
	// the path-access substrate VISIBLE to the LLM so subagents read
	// constraints and refuse instead of fabricating against inaccessible
	// paths (the c160 turn-16 regression target). Sibling-split from
	// SlotSystem rather than concatenated into it so the AGENTS.md walk-up
	// work (CW-20260512-0116) can plug into SlotSystem without merge
	// conflict.
	//
	// Non-compactable — sits with SlotRules / SlotAgent / SlotUniversal in
	// the cacheable prefix. Per-session-stable until path_grants shift or
	// the resolved RuleSet changes; the Anthropic cacheable prefix
	// invariant survives every turn that doesn't perturb permissions.
	SlotPermissions = "permissions"
	// SlotWorkspace carries the AGENTS.md walk-up payload — instruction
	// files (AGENTS.md / CLAUDE.md / NANITE.md / .nanite/rules.md)
	// collected by walking from the session's working_dir UP to the
	// nearest .git root (or filesystem root). CW-20260512-0116
	// (SP-20260512-0009 W6). Same agent in different working dirs
	// receives different local conventions — the slot is the mechanism
	// the user described as "project-local agent rules" in the
	// harness-restoration design session.
	//
	// Source: internal/workspace.Cache.Refresh. Walk direction is
	// innermost-first → outermost-last, matching opencode's findUp
	// convention. Headers match opencode's instruction.ts:160 format:
	// `Instructions from: <abs-path>` per block.
	//
	// Sits after SlotPermissions in the cacheable prefix: the workspace
	// slot is per-session-per-working_dir-stable, which in normal
	// operation (working_dir doesn't change mid-session) is the same as
	// per-session-stable. Placement near other policy-class slots keeps
	// cache marker placement deterministic — workspace rules and
	// path-grant permissions are both load-bearing identity/policy for
	// the session's local project.
	//
	// Non-compactable — workspace rules survive compaction the same way
	// SlotRules and SlotAgent do; identity-class for the local project.
	// The earlier SlotPermissions doc-comment noted that AGENTS.md
	// walk-up would plug into SlotSystem — that plan was superseded by
	// this dedicated slot (the ticket asks for a `workspace slot`,
	// distinct from the think-tool + workspace-identity payload that
	// lives in SlotSystem).
	SlotWorkspace   = "workspace"
	SlotTools       = "tools"
	SlotSession     = "session"
	SlotContext     = "context"      // dynamic enrichment (plugins, context broker)
	SlotUserContext = "user_context" // J10 (CW-20260426-0008): user-authored session context prompt.
	// Not compactable — survives compaction like SlotAgent/SlotRules.
	// Populated from sessions.context_prompt. Composes with HandoffStash
	// (CW-20260420-0024) — both are pinned slots that survive compaction.
	// J11 (CW-20260426-0009) pin tool will also use this same pattern.
	// Glass-3 (CW-20260502-0011): self-authored handoff, auto-injected post-compaction.
	// Sits with user-pinned content but is auto-managed by the harness, not by the user.
	// Capped at SlotHandoffMaxTokens (~1500 tokens). Non-compactable — handoff IS the
	// continuity primitive across compaction. AutoInject=true: the harness writes into
	// this slot; the LLM cannot strip it. Glass-4 (CW-20260502-0015) wires the actual flow.
	SlotHandoff      = "handoff"
	SlotConversation = "conversation" // messages — subject to compaction
)

// SlotHandoffMaxTokens caps the handoff payload at ~1500 tokens. Glass-3
// (CW-20260502-0011): feature-sized, NOT a dumping ground. ValidateHandoff
// in handoff.go enforces this against parsed payloads.
const SlotHandoffMaxTokens = 1500

// SlotOrder defines the assembly order. Slots are composed into the
// provider payload in this sequence. Earlier slots are cached more
// aggressively (they change less often).
var SlotOrder = []string{
	SlotUniversal, // SP-20260512-0008 W1A reserved; CW-20260512-0114 wired content.
	SlotSystem,
	SlotMemory,
	SlotAgent,
	SlotMode,
	SlotRules,
	SlotPermissions, // CW-20260512-0118 (SP-20260512-0010 W2): per-session path-access summary.
	SlotWorkspace,   // CW-20260512-0116 (SP-20260512-0009 W6): AGENTS.md walk-up from working_dir.
	SlotTools,
	SlotSession,
	SlotContext,
	SlotUserContext,
	SlotHandoff, // Glass-3 (CW-20260502-0011): sits with user-pinned content but is auto-managed.
	SlotConversation,
}

// Slot holds the content, budget, and cache state for a single named
// region of the context window. Slots are the unit of caching,
// compaction, and budget accounting.
type Slot struct {
	Name        string
	Content     string
	TokenCount  int
	CacheKey    string // SHA-256 hex of Content
	Priority    int    // compaction priority: lower = keep longer
	MaxTokens   int    // budget ceiling (0 = dynamic)
	Compactable bool   // false = pipeline never modifies this slot (system, agent, rules)
	Flags       SlotFlags
}

// DefaultCompactable returns the default compactability per slot. System,
// Agent, Rules, and UserContext survive compaction so the model never loses
// identity, instructions, policy, or the user's session-scoped context mid-conversation.
func DefaultCompactable() map[string]bool {
	return map[string]bool{
		SlotUniversal:    false, // Position-0 universal rules — non-compactable; identity-class.
		SlotSystem:       false,
		SlotMemory:       true,
		SlotAgent:        false,
		SlotMode:         false, // B1: session-mode addendum — survives compaction.
		SlotRules:        false,
		SlotPermissions:  false, // CW-20260512-0118 — path-access summary survives compaction; constraints stay visible.
		SlotWorkspace:    false, // CW-20260512-0116 — workspace rules survive compaction; identity-class for the local project.
		SlotTools:        true,
		SlotSession:      true,
		SlotContext:      true,
		SlotUserContext:  false, // J10: pinned — survives compaction.
		SlotHandoff:      false, // Glass-3 (CW-20260502-0011): handoff survives compaction; it IS the continuity primitive.
		SlotConversation: true,
	}
}

// SlotFlags carry per-slot signals consumed by the compaction and
// assembly pipelines.
type SlotFlags struct {
	UsingTools       bool // tool call/result blocks present in conversation span
	EnrichmentActive bool // context slot has dynamic content
	Stale            bool // content needs refresh before next assembly
	// AutoInject — Glass-3 (CW-20260502-0011): when true, the harness writes this
	// slot from authoritative state (e.g. handoff cache); the LLM cannot strip it.
	// Glass-4 will populate the handoff slot with AutoInject=true post-compaction.
	AutoInject bool
	// LazyLoad — Glass-5 (CW-20260502-0012): when true, the slot ships only
	// LoadHint as content, not the full payload. The agent reaches for an
	// explicit discovery tool (named in LoadHint) to retrieve the full content.
	// LoadHint must be a confidence + invitation pointer, not a warning.
	// When LazyLoad=true and LoadHint is empty, the flag is a no-op.
	LazyLoad bool
	// LoadHint — Glass-5 (CW-20260502-0012): pointer text shown to the agent
	// when LazyLoad=true. Brief, named-tool-call-included, framed as
	// invitation. Replaces Slot.Content in the assembled SlotBlock.
	LoadHint string
}

// EffectiveContent returns the content + cache key the slot should ship
// in this turn. When LazyLoad is set with a non-empty LoadHint the pointer
// is shipped in place of the full content; otherwise the slot's stored
// content is shipped as-is. Glass-5 (CW-20260502-0012).
func (s *Slot) EffectiveContent() (content, cacheKey string) {
	if s.Flags.LazyLoad && s.Flags.LoadHint != "" {
		return s.Flags.LoadHint, ComputeCacheKey(s.Flags.LoadHint)
	}
	return s.Content, s.CacheKey
}

// SlotBlock is the output unit from ContextWindow.Assemble(). Provider
// adapters translate these into API-specific payloads and apply cache
// markers where supported.
type SlotBlock struct {
	SlotName string
	Content  string
	CacheKey string
	Changed  bool // true when CacheKey differs from previous turn
}

// ComputeCacheKey returns the SHA-256 hex digest of content.
func ComputeCacheKey(content string) string {
	h := sha256.Sum256([]byte(content))
	return fmt.Sprintf("%x", h)
}

// DefaultBudgets returns the default per-slot token ceilings.
// A ceiling of 0 means "dynamic — allocated from remaining budget".
func DefaultBudgets() map[string]int {
	return map[string]int{
		SlotUniversal:    550, // Position-0 reserved budget; tight by design — universal rules are concise (CW-20260513-0036).
		SlotSystem:       2000,
		SlotMemory:       2000,
		SlotAgent:        1000,
		SlotMode:         500, // B1: session-mode addendum — small by design.
		SlotRules:        500,
		SlotPermissions:  800,  // CW-20260512-0118 — bounded by deny enumeration + provenance tags; per-session stable.
		SlotWorkspace:    4000, // CW-20260512-0116 — generous; concatenated AGENTS.md/CLAUDE.md/NANITE.md across the walk path. Oversized payloads stash via the assembly decider's per-slot budget check.
		SlotTools:        0,    // proportional to selected tool count
		SlotSession:      1000,
		SlotContext:      0,                    // dynamic
		SlotUserContext:  2000,                 // J10: user context prompt; thin by design.
		SlotHandoff:      SlotHandoffMaxTokens, // Glass-3 (CW-20260502-0011): feature-sized handoff, ~1500 tokens.
		SlotConversation: 0,                    // gets remainder
	}
}
