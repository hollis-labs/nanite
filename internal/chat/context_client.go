package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	feotel "github.com/hollis-labs/go-otel"
	"go.opentelemetry.io/otel/attribute"

	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/nanite/internal/contextbroker"
	"github.com/hollis-labs/nanite/internal/permission"
	"github.com/hollis-labs/nanite/internal/store"
	wsutil "github.com/hollis-labs/nanite/internal/workspace"
)

// DefaultBudgetPct is the default fraction of the context window to use.
const DefaultBudgetPct = 0.75

// DefaultContextWindow is the fallback context window size in tokens.
const DefaultContextWindow = 200000

// HardCeilingPct is the absolute maximum fraction of context window allowed
// before refusing to send. This is the unified budget that includes system
// prompt + messages + tool definitions.
const HardCeilingPct = 0.80

// ToolResultPruneAge is the number of tool-use iterations after which old
// tool results are replaced with compact references.
const ToolResultPruneAge = 2

// ContextClient assembles and manages context for chat turns.
type ContextClient struct {
	Store         *store.Store
	BudgetPct     float64               // fraction of context window to use (default 0.75)
	ContextBroker *contextbroker.Broker // universal context retrieval (nil = disabled)
	// HintDispatcher, when set, enables v2 dynamic hint selection via the
	// hint-selector peer agent (F5 / CW-20260420-0022). nil means the assembler
	// falls through to the v0/v1 static ThinkToolBlock path. Also requires
	// NANITE_THINK_BLOCK_V2_ENABLED=true in the environment.
	HintDispatcher HintDispatcher

	// PathGrants is the session-scoped explicit-mention grant store,
	// shared with the chat / subagent / dev-tools wiring. Used by
	// AssembleSlotSources to render the SlotPermissions summary
	// (CW-20260512-0118). nil = no session-grant rows in the rendered
	// summary; the binary AllowedPaths + per-profile RuleSet still
	// render normally.
	PathGrants *permission.PathGrants

	// DevToolsAllowedPaths is the binary-scoped allow-list configured via
	// nanite.yaml `dev_tools_allowed_paths`. Threaded here so the
	// permission summary renderer (CW-20260512-0118) surfaces the baseline
	// READ roots the agent operates against. Empty / nil renders no
	// "workspace allow-list" section.
	DevToolsAllowedPaths []string

	// WorkspaceCache is the per-(session, working_dir) AGENTS.md walk-up
	// cache populating SlotWorkspace (CW-20260512-0116, SP-20260512-0009
	// W6). nil disables the walk-up entirely — SlotWorkspace ships empty
	// and the assembly decider treats it as skipped_no_content. Construct
	// via workspace.NewCache(); the cache is process-lifetime and
	// concurrency-safe.
	WorkspaceCache *wsutil.Cache

	// WorkingDirForSession resolves the on-disk working_dir for a
	// session. Today the closest analogue is the session's project
	// repo_path (store.Project.RepoPath). The resolver is injected
	// rather than hard-wired so test paths can supply a t.TempDir() and
	// future evolution (e.g. session-scoped working_dir column) only
	// touches the wiring layer. Returns ("", nil) when no working_dir
	// is resolvable for the session — SlotWorkspace then ships empty.
	WorkingDirForSession func(session *store.Session) (string, error)
}

// NewContextClient creates a new ContextClient with default settings.
func NewContextClient(s *store.Store) *ContextClient {
	return &ContextClient{
		Store:     s,
		BudgetPct: DefaultBudgetPct,
	}
}

// SlotSources carries the raw, per-slot strings sourced for slot-based
// context assembly. The service layer composes these into a ContextWindow.
// Tools content is filled by the service layer after tool selection.
type SlotSources struct {
	// Universal is the position-0 universal-rules slot. Sourced from
	// chat.UniversalRulesBlock() so the Context Broker assembly decider
	// emits it unconditionally for every dispatch type (chat, sync
	// subagent, async subagent, background job). Position 0 keeps the
	// cacheable prefix stable across agents that share the universal
	// rules — Anthropic's `cacheable_prefix_tokens` math depends on this
	// being the leading slot. SP-20260512-0008 W1A reserved position 0;
	// CW-20260512-0114 wires the content here.
	Universal string
	System    string // think-tool block (no agent-specific text)
	Memory    string // formatted ContextBroker items where Source == "memory"
	Agent     string // agent.SystemPrompt + skill list
	// Mode is always "" — Phase 0 item 21 ("Cut Modes, in full") deleted
	// both Session Mode and Legacy Agent Mode. Kept as a field (not
	// removed) so SlotMode keeps a content source to bind to; see INV4 in
	// internal/context/INVARIANTS.md for why the slot itself stays.
	Mode  string
	Rules string // agent tags + tool allowlist (S4a expands)
	// Permissions carries the rendered SlotPermissions block — a
	// human-readable summary of the session's effective path access
	// (binary AllowedPaths + session PathGrants + lineage walk + resolved
	// permission.RuleSet). CW-20260512-0118 (SP-20260512-0010 W2): closes
	// the H1 fabrication gap by making the path-access substrate visible
	// to the LLM. Empty when no constraints are configured for the agent.
	Permissions string
	// Workspace carries the AGENTS.md walk-up payload for the session's
	// working_dir. CW-20260512-0116 (SP-20260512-0009 W6). Sourced from
	// internal/workspace.Cache.Refresh — innermost-first concatenation of
	// AGENTS.md / CLAUDE.md / NANITE.md / .nanite/rules.md from
	// working_dir up to the nearest .git root. Empty when no
	// WorkspaceCache / resolver is wired or no instruction files are
	// found on the walk path.
	Workspace        string
	Session          string                 // session name, mode label
	Context          string                 // formatted ContextBroker items where Source != "memory"
	UserContext      string                 // J10 (CW-20260426-0008): user-authored session context prompt + included docs.
	Messages         []llmtypes.ChatMessage // conversation slot messages
	EnrichmentActive bool                   // true when Context slot was populated by the broker
	// Intent is the resolved per-turn intent used by the broker's
	// assembly decider. Surfaced so the service layer can pass it to
	// contextbroker.DecideAssembly without re-running deriveIntent.
	Intent contextbroker.Intent
}

// AssembleSlotSources builds the raw per-slot content for slot-based assembly.
// Memory and Context are split from the ContextBroker fetch by item.Source.
// The Tools slot is intentionally not populated here — the service layer
// fills it from the selected tool definitions after calling this method.
//
// Phase 0 item 21 ("Cut Modes, in full") removed this function's `mode
// *store.AgentMode` and `sessionMode *store.Mode` parameters — both Legacy
// Agent Mode and Session Mode are gone. SlotMode (see the SlotSources.Mode
// field) is now permanently empty/inert per INV4's post-cut definition in
// internal/context/INVARIANTS.md; its position in SlotOrder is unchanged.
func (cb *ContextClient) AssembleSlotSources(ctx context.Context, session *store.Session, agent *store.AgentProfile) (*SlotSources, error) {
	_, span := feotel.StartSpan(ctx, "nanite.broker.assembleSlotSources")
	defer span.End()
	span.SetAttributes(
		attribute.String("nanite.session.id", session.ID),
		attribute.String("nanite.agent.id", agent.ID),
	)

	// System slot — think-tool block. Agent-specific content lives in the
	// Agent slot; universal rules live in SlotUniversal at position 0
	// (CW-20260512-0114, see below). v0/v1/v2 think-tool selected by
	// feature flags. Phase 0 item 20 (retire workspaces): this used to
	// also carry "Workspace: <name> - <description>" from the now-retired
	// in-app `workspaces` table.
	var sysB strings.Builder
	var thinkBlock string
	if cb.HintDispatcher != nil && IsThinkBlockV2Enabled() {
		thinkBlock = ThinkToolBlockWithDispatch(ctx, cb.HintDispatcher, "", "", "")
	} else {
		thinkBlock = ThinkToolBlock()
	}
	sysB.WriteString(strings.TrimLeft(thinkBlock, "\n"))
	systemSlotContent := sysB.String()

	// Agent slot — composed via prompt templates with skills, falling back to
	// raw agent + mode strings when no template is assigned. The agent slot
	// also carries the post-compaction disclosure (P8A) when one is fresh
	// for this session.
	//
	// Glass-7 (CW-20260502-0016, SP-20260502-0001): the chat-role-harness
	// prompt body itself was deduplicated (~557 → ~485 tokens) by removing
	// two Capability-section bullets that Phase A's architecture
	// (docs/architecture/agent-context-architecture.md) had relocated
	// to tool descriptions. See migration 053 for the in-place DB update.
	// If the agent observably loses capability after this trim, revert and
	// re-evaluate.
	//
	// Intent is derived unconditionally so the service-layer assembly decider
	// can use it even when ContextBroker is nil (no Fetch happens, but the
	// intent still drives slot selection for non-broker slots).
	//
	// Phase 0 item 22 (decision log §11): the Skill Broker that used to
	// consume this intent for per-turn skill ranking is retired — skill
	// selection is now a direct cap (see buildSkillListForSession), so
	// intent no longer needs to reach the skill-list call. It's still
	// derived here for the Context Broker step further down.
	intent := cb.deriveIntent(session, agent)
	skillList := buildSkillListForSession(ctx, cb.Store, agent.ID, session.ID)
	agentPrompt := assembleAgentSlotContent(cb.Store, agent, skillList, session.ID)

	// Rules slot — agent tags + tool allowlist. S4a expands this.
	rules := buildRulesSlotContent(agent)

	// Permissions slot — CW-20260512-0118 (SP-20260512-0010 W2). Render a
	// human-readable summary of the session's effective path access so the
	// LLM reads the constraints it operates under, rather than reasoning
	// about access from priors and fabricating. This is the upstream
	// PREVENTION layer for the c160 turn-16 fabrication regression; the
	// runtime detection added by CW-20260512-0095 (PR #144) stays as the
	// downstream DETECTION backstop.
	//
	// W4 (CW-20260512-0120, PR #154) integration: when an agent-scoped
	// permission RuleSet is wired (no production callers today; reserved
	// for the future), the caller MUST call (*RuleSet).Resolve(workingDir)
	// before passing it to RenderPermissionSummary so workspace-relative
	// `./` patterns appear in resolved absolute form rather than leaking
	// un-resolved shapes into the prompt. The resolve call site lives at
	// the wiring layer (here), not inside the renderer — this preserves
	// the renderer's pure-projection contract.
	permissionsContent := cb.buildPermissionsSlotContent(session, agent)

	// Workspace slot — CW-20260512-0116 (SP-20260512-0009 W6). AGENTS.md
	// walk-up from the session's working_dir UP to the nearest .git
	// root, populating local project conventions into the cacheable
	// prefix. Same agent in different working_dirs receives different
	// rules — the architectural intent the user described in the
	// harness-restoration design session. Wired here (rather than in
	// the assembly decider) so the on-disk filesystem reads happen at
	// source-materialization time and the decider remains
	// I/O-free / deterministic. The Cache layer makes repeat calls
	// cheap: a stat-only mtime check per cached file plus a stat-only
	// "new file appeared" sweep over previously-walked directories.
	// Session start AND post-compaction both rebuild the slot store via
	// AssembleSlots → AssembleSlotSources, so both hook points are
	// covered without a dedicated callsite. Empty content (no walk-up
	// cache wired, no resolver wired, no instruction files found)
	// ships as skipped_no_content via the assembly decider.
	workspaceContent := cb.buildWorkspaceSlotContent(ctx, session)

	// Session slot — small, stable identifiers.
	sessionContent := buildSessionSlotContent(session)

	// Memory + Context — both sourced from ContextBroker; split by item.Source.
	var memoryContent, contextContent string
	enrichmentActive := false
	if cb.ContextBroker != nil {
		packet, err := cb.ContextBroker.Fetch(ctx, intent)
		if err != nil {
			slog.Warn("broker: slot enrichment failed", "err", err)
		} else if packet != nil && len(packet.Items) > 0 {
			memoryContent = formatPacketItemsBySource(packet, true)
			contextContent = formatPacketItemsBySource(packet, false)
			if contextContent != "" {
				enrichmentActive = true
			}
		}
	}

	// Conversation messages.
	messages, err := cb.Store.ListMessages(ctx, session.ID, 200)
	if err != nil {
		return nil, err
	}
	chatMessages := make([]llmtypes.ChatMessage, len(messages))
	for i, m := range messages {
		role := m.Role
		if role == "system" || role == "tool" || role == RoleEnvelopeResponse {
			role = "user"
		}
		chatMessages[i] = llmtypes.ChatMessage{Role: role, Content: replayContent(m.Content)}
	}

	// J10 (CW-20260426-0008): user context prompt + included documents.
	// Both are pinned and NOT compactable (SlotUserContext). The user context
	// prompt is authored in the bottom drawer. Included documents are injected
	// as pointers (name + summary) by default, or full content when
	// full_content=true. This slot composes with HandoffStash (CW-20260420-0024)
	// for compaction-survival — both are non-compactable pinned slots.
	// J11 (CW-20260426-0009) pin tool will extend this same pattern.
	userContextContent := buildUserContextSlot(cb.Store, session.ID)

	// SlotMode — INV4 (internal/context/INVARIANTS.md). Phase 0 item 21
	// ("Cut Modes, in full") deleted Session Mode (sessions.current_mode_id,
	// the modes table, store.GetSessionMode/SetSessionMode). There is no
	// more session-scoped *store.Mode pointer to render, so SlotMode is now
	// permanently empty/inert — content source removed, not the slot
	// itself. SlotOrder (internal/context/slot.go) still carries SlotMode
	// at its existing position so INV1 (stable sent shape) and INV3 (cache
	// marker priority list) are unaffected; the assembly decider ships it
	// as skipped_no_content like any other empty slot.
	const modeContent = ""

	return &SlotSources{
		// SlotUniversal carries the universal-rules block at position 0
		// (CW-20260512-0114). Sourced from UniversalRulesBlock() so the
		// Context Broker assembly decider emits it unconditionally for
		// every dispatch type — chat, sync subagent, async subagent,
		// background job. The block was previously prepended onto
		// SlotSystem via universalRulesPrefix; that helper has been
		// removed (per feedback_no_compat_shims) now that the broker
		// owns the wire-shape decision.
		Universal:        UniversalRulesBlock(),
		System:           systemSlotContent,
		Memory:           memoryContent,
		Agent:            agentPrompt,
		Mode:             modeContent,
		Rules:            rules,
		Permissions:      permissionsContent,
		Workspace:        workspaceContent,
		Session:          sessionContent,
		Context:          contextContent,
		UserContext:      userContextContent,
		Messages:         chatMessages,
		EnrichmentActive: enrichmentActive,
		Intent:           intent,
	}, nil
}

// buildUserContextSlot assembles the SlotUserContext content from:
//  1. The session-scoped user context prompt (sessions.context_prompt).
//  2. Any included documents (documents.included=true), injected as pointer
//     (name + summary) or full content based on documents.full_content.
//  3. Pinned content (pinned_content table) — session + cross_session scopes.
//     J11 (CW-20260426-0009): pinned content rides in the 2000-token budget.
//     Oldest pins are truncated first when over budget.
//
// Returns an empty string when none of the above are set. The slot is excluded
// from the system prompt for that turn when empty (no waste of budget).
func buildUserContextSlot(s *store.Store, sessionID string) string {
	var parts []string

	// Session context prompt.
	if prompt, err := s.GetSessionContextPrompt(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, sessionID); err == nil && strings.TrimSpace(prompt) != "" {
		parts = append(parts, "## Session Context\n"+strings.TrimSpace(prompt))
	}

	// Included documents.
	if docs, err := s.GetIncludedDocuments(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, sessionID); err == nil && len(docs) > 0 {
		var docParts []string
		for _, doc := range docs {
			if doc.FullContent {
				docParts = append(docParts, fmt.Sprintf("### Document: %s\n%s", doc.Name, doc.Content))
			} else {
				// Pointer mode: name + summary only.
				summary := doc.Summary
				if summary == "" {
					summary = fmt.Sprintf("(document ID: %s, size: %d bytes)", doc.ID, doc.SizeBytes)
				}
				docParts = append(docParts, fmt.Sprintf("### Document: %s (pointer)\n%s", doc.Name, summary))
			}
		}
		if len(docParts) > 0 {
			parts = append(parts, "## Session Documents\n"+strings.Join(docParts, "\n\n"))
		}
	}

	// Pinned content (J11, CW-20260426-0009). Session + cross_session scopes.
	// Budget: 2000 tokens shared with the above. Oldest pins truncate first.
	// Turn-scoped pins are ephemeral and not persisted here — they are injected
	// directly into the turn context by the reminder engine.
	if pins, err := s.ListPinnedContent(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, sessionID); err == nil && len(pins) > 0 {
		var pinParts []string
		for _, pin := range pins {
			label := "[pinned]"
			if pin.Scope == store.PinScopeProject {
				label = "[pinned:project]"
			}
			pinParts = append(pinParts, fmt.Sprintf("%s %s", label, pin.Content))
		}
		if len(pinParts) > 0 {
			parts = append(parts, "## Pinned Context\n"+strings.Join(pinParts, "\n"))
		}
	}

	return strings.Join(parts, "\n\n")
}

// buildPermissionsSlotContent renders the SlotPermissions block — a human-
// readable per-session path-access summary (CW-20260512-0118,
// SP-20260512-0010 W2).
//
// Inputs (all optional — empty input renders to empty string, slot is
// silently skipped):
//
//   - cb.DevToolsAllowedPaths: the binary-scoped allow-list configured via
//     nanite.yaml `dev_tools_allowed_paths`. Threaded onto the
//     ContextClient at composition-root time.
//
//   - cb.PathGrants: session-scoped explicit-mention grants. The renderer
//     reads (own-bucket, lineage-walk-union) so the rendered "session
//     grants" vs "inherited from parent session" sections stay
//     attribution-correct.
//
//   - permission.RuleSet: not wired today (no production caller populates
//     a per-session RuleSet). Reserved for future wiring; when added, the
//     resolve step (W4 / CW-20260512-0120) MUST run here so `./`-prefixed
//     patterns appear in their absolute form in the rendered summary.
//
// Session scope qualifier: when the agent is a subagent (slug != chat
// default), the qualifier is "this <slug> subagent's scope" so the
// rendered output matches the agent's identity. Falls back to the generic
// "this session's scope" otherwise.
//
// Determinism: pure projection over the inputs. The slot's SHA-256 cache
// key (internal/context.ComputeCacheKey) is per-session-stable until
// path_grants shift or the resolved RuleSet changes — keeps the slot in
// the Anthropic cacheable prefix.
func (cb *ContextClient) buildPermissionsSlotContent(session *store.Session, agent *store.AgentProfile) string {
	if cb == nil || session == nil {
		return ""
	}

	var ownGrants, inheritedGrants []string
	if cb.PathGrants != nil {
		ownGrants = cb.PathGrants.ListGrants(session.ID)
		inheritedGrants = cb.PathGrants.ListLineageGrants(session.ID)
	}

	scope := "this session's scope"
	if agent != nil && agent.Slug != "" && agent.Slug != "default" {
		scope = "this " + agent.Slug + " subagent's scope"
	}

	// Per-session RuleSet — populated for subagent child sessions via
	// CW-20260512-0119 (SP-20260512-0010 W3). The runner calls
	// DeriveSubagentRuleSet at spawn time and stores the result on
	// PathGrants.derivedRules; the renderer reads it here so the agent
	// sees forwarded parent denies under "You CANNOT access (explicitly
	// denied)" with provenance preserved (Source tagged "(via parent)").
	//
	// W4 contract: by the time the derived ruleset reaches this point
	// it MUST already be in canonical absolute form. The runner
	// resolves the subagent profile rules against the child session's
	// working_dir inside DeriveSubagentRuleSet, and parent rules are
	// assumed already-resolved per the chain invariant (the parent's
	// own derived set was previously resolved when that session was
	// itself spawned, or it was loaded from a YAML file via
	// LoadRulesFromFile → Resolve).
	//
	// For top-level chat sessions that haven't been registered with a
	// per-session ruleset, LookupDerivedRules returns nil and the
	// renderer skips the rule-list sections naturally.
	var resolvedRules *permission.RuleSet
	if cb.PathGrants != nil {
		resolvedRules = cb.PathGrants.LookupDerivedRules(session.ID)
	}

	return permission.RenderPermissionSummary(permission.SummaryInput{
		Rules:           resolvedRules,
		AllowedPaths:    cb.DevToolsAllowedPaths,
		OwnGrants:       ownGrants,
		InheritedGrants: inheritedGrants,
		SessionScope:    scope,
	})
}

// buildWorkspaceSlotContent runs the AGENTS.md walk-up for the session's
// working_dir and returns the SlotWorkspace payload. CW-20260512-0116
// (SP-20260512-0009 W6).
//
// Dependencies (all optional — when any is nil/empty the slot ships empty
// and the assembly decider treats it as skipped_no_content):
//
//   - cb.WorkspaceCache: per-(session, working_dir) cache. nil disables the
//     walk-up entirely. Construct via workspace.NewCache(); the cache is
//     concurrency-safe and process-lifetime.
//   - cb.WorkingDirForSession: session → working_dir resolver. Today the
//     closest analogue is the session's project repo_path. Injected rather
//     than hard-wired so test paths can supply a t.TempDir() and future
//     evolution (session.working_dir column, etc.) only touches the wiring
//     layer.
//
// Errors from the resolver or walk-up are logged but never propagated —
// SlotWorkspace failing should never break the chat turn. The slot
// gracefully degrades to empty content.
//
// Cache placement: SlotWorkspace sits after SlotPermissions and before
// SlotTools in SlotOrder. Both placement and per-slot stability (only
// rebuilds on mtime drift or new-file-appearance) preserve the Anthropic
// cacheable prefix across turns within the same session+working_dir.
func (cb *ContextClient) buildWorkspaceSlotContent(ctx context.Context, session *store.Session) string {
	if cb == nil || session == nil {
		return ""
	}
	if cb.WorkspaceCache == nil || cb.WorkingDirForSession == nil {
		return ""
	}

	workingDir, err := cb.WorkingDirForSession(session)
	if err != nil {
		slog.Warn("workspace: working_dir resolve failed",
			"session_id", session.ID, "err", err)
		return ""
	}
	if workingDir == "" {
		return ""
	}

	result, err := cb.WorkspaceCache.Refresh(session.ID, workingDir)
	if err != nil {
		slog.Warn("workspace: walk-up refresh failed",
			"session_id", session.ID, "working_dir", workingDir, "err", err)
		return ""
	}

	slog.Debug("workspace: walk-up complete",
		"session_id", session.ID,
		"working_dir", workingDir,
		"git_root", result.GitRoot,
		"file_count", len(result.Files),
		"bytes", len(result.Content))
	_ = ctx // reserved for future tracing spans

	return result.Content
}

// deriveIntent extracts the broker intent from the session's recent user turn.
// Mirrors the logic from enrichWithContextBroker so slot- and legacy-paths
// produce identical broker queries.
//
// Loads the most recent 5 messages from the store. Hot-path callers that
// already hold a sufficient tail of session messages should prefer
// deriveIntentFromMessages to avoid the redundant DB round trip.
//
// Auto-recall fields are resolved from the agent profile and plumbed into
// the Intent so MemorySource can honor per-agent disable / limit / min-confidence
// without re-reading the profile itself. AutoRecall is set as an explicit
// pointer so MemorySource can distinguish "no opinion" (defaults) from
// "explicitly off" (skip).
func (cb *ContextClient) deriveIntent(session *store.Session, agent *store.AgentProfile) contextbroker.Intent {
	var msgs []store.Message
	if loaded, err := cb.Store.ListMessages(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, session.ID, 5); err == nil {
		msgs = loaded
	}
	return cb.deriveIntentFromMessages(session, agent, msgs)
}

// deriveIntentFromMessages is the no-DB variant of deriveIntent: callers
// pass a pre-loaded message slice and the helper scans the tail for the
// most recent user turn. Identical output to deriveIntent given the same
// tail. Currently only reached via deriveIntent itself; kept as a separate
// no-DB entry point for any future hot-path caller that already holds a
// sufficient message window and wants to avoid a redundant DB round trip.
//
// Tail-scan semantics match deriveIntent — newest-to-oldest, first user
// message wins, no minimum length on the input slice. Empty input is fine
// (Intent defaults to IntentCustom with empty keywords/query).
func (cb *ContextClient) deriveIntentFromMessages(session *store.Session, agent *store.AgentProfile, messages []store.Message) contextbroker.Intent {
	intentType := contextbroker.IntentCustom
	var keywords []string
	var queryText string
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			_, keywords = ExtractIntent(messages[i].Content)
			intentType = classifyContextIntent(messages[i].Content)
			queryText = messages[i].Content
			break
		}
	}
	autoRecallCfg := ResolveAutoRecallConfig(agent)
	enabled := autoRecallCfg.Enabled
	return contextbroker.Intent{
		Type:                    intentType,
		Keywords:                keywords,
		QueryText:               queryText,
		Scope:                   session.ProjectID,
		SessionID:               session.ID,
		AgentID:                 agent.ID,
		AutoRecall:              &enabled,
		AutoRecallLimit:         autoRecallCfg.Limit,
		AutoRecallMinConfidence: autoRecallCfg.MinConfidence,
		AutoRecallTimeout:       autoRecallCfg.Timeout,
	}
}

// formatPacketItemsBySource filters the packet to items where Source == "memory"
// (when memoryOnly is true) or Source != "memory" (when false), then formats
// the filtered subset using the same renderer as the legacy path.
func formatPacketItemsBySource(packet *contextbroker.ContextPacket, memoryOnly bool) string {
	if packet == nil {
		return ""
	}
	filtered := make([]contextbroker.ContextItem, 0, len(packet.Items))
	for _, it := range packet.Items {
		isMemory := it.Source == "memory"
		if memoryOnly == isMemory {
			filtered = append(filtered, it)
		}
	}
	if len(filtered) == 0 {
		return ""
	}
	sub := &contextbroker.ContextPacket{
		Items:    filtered,
		Manifest: packet.Manifest,
	}
	return contextbroker.FormatPacket(sub)
}

// assembleAgentSlotContent composes the agent-specific portion of the prompt
// (agent.SystemPrompt, skill list) without the workspace or think-tool
// sections that live in the System slot. Phase 0 item 21 ("Cut Modes, in
// full") removed the Legacy AgentMode addendum this used to splice in —
// there is no more per-agent mode to append. Phase 0 item 29 ("Relocate
// compaction-disclosure content, then cut prompt_templates") removed the
// prompt_templates-backed ComposePromptForAgent composition path — every
// agent now uses agent.SystemPrompt directly, unconditionally.
//
// When sessionID is non-empty and a fresh CompactionContract event exists for
// the session, the unified disclosure is appended (P8A, CW-20260420-0025;
// collapsed to a single hardcoded message by Phase 0 item 29).
func assembleAgentSlotContent(s *store.Store, agent *store.AgentProfile, skillList, sessionID string) string {
	composed := agent.SystemPrompt
	if skillList != "" {
		composed += "\n\nAvailable skills:\n" + skillList
	}
	if sessionID != "" {
		if disclosure := renderCompactionDisclosure(s, sessionID); disclosure != "" {
			composed += "\n\n" + disclosure
		}
	}
	return composed
}

// buildRulesSlotContent renders the Rules slot from the agent profile. S4a
// expands this with policy-layer rules; for now it surfaces tags + allowlist.
//
// Tags and tools are stored as JSON arrays in the agent profile; rendering
// them as raw JSON forces the LLM to parse — a Markdown bulleted list is
// cheaper to consume and more robust to surrounding-prose pattern-matching.
func buildRulesSlotContent(agent *store.AgentProfile) string {
	var b strings.Builder
	if items := parseJSONStringArray(agent.Tags); len(items) > 0 {
		b.WriteString("Agent tags:\n")
		for _, t := range items {
			fmt.Fprintf(&b, "- %s\n", t)
		}
	}
	if items := parseJSONStringArray(agent.Tools); len(items) > 0 {
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString("Tool allowlist:\n")
		for _, t := range items {
			fmt.Fprintf(&b, "- %s\n", t)
		}
	}
	return b.String()
}

// parseJSONStringArray parses a JSON string array like `["foo","bar"]` into
// a Go slice. Empty / null / parse-error input returns an empty slice so the
// caller can render nothing without branching on shape.
func parseJSONStringArray(raw string) []string {
	if raw == "" || raw == "[]" || raw == "null" {
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		// Defensive: a malformed tags/tools column shouldn't break the slot
		// render; log once at debug and return empty so the slot shows no
		// rules rather than raw JSON garbage.
		slog.Debug("chat: parseJSONStringArray failed", "raw", raw, "err", err)
		return nil
	}
	return out
}

// buildSessionSlotContent renders the Session slot — small, stable identifiers
// the model uses to anchor itself to the active session. Today's date anchors
// the LLM against drift toward training-cutoff dates in its outputs.
// Workspace is intentionally omitted here because SlotSystem already carries
// it — we don't want to waste tokens on a duplicate.
//
// Phase 0 item 21 ("Cut Modes, in full") removed the `mode *store.AgentMode`
// parameter this used to take and the "Mode: <slug>" line it rendered —
// there is no more mode to report.
func buildSessionSlotContent(session *store.Session) string {
	var b strings.Builder
	now := time.Now()
	fmt.Fprintf(&b, "Today: %s (%s)\n", now.Format("2006-01-02"), now.Format("Monday"))
	// Auto-generated chat titles (c17, c18) add no signal — only surface a
	// title if it looks user-assigned. Heuristic: > 4 chars or contains
	// a space is treated as intentional.
	if t := session.Title; t != "" && (len(t) > 4 || strings.Contains(t, " ")) {
		fmt.Fprintf(&b, "Session: %s\n", t)
	}
	return b.String()
}

// EstimateTokens does a rough chars/4 estimation.
func EstimateTokens(text string) int {
	n := len(text) / 4
	if n == 0 && len(text) > 0 {
		n = 1
	}
	return n
}

// PruneAfterTurn is retired in Phase 3 S3a. The destructive per-turn DB
// rewrite has been superseded by the in-memory slot compaction pipeline at
// internal/context/CompactionPipeline. Callers should no longer rely on
// database-level compaction; message.content rows are the append-only
// source of truth going forward. This method is preserved as a no-op with
// a deprecation log for one release cycle and will be deleted next.
func (cb *ContextClient) PruneAfterTurn(sessionID string) error {
	slog.Warn("broker: PruneAfterTurn is deprecated (slot compaction supersedes); no-op",
		"session_id", sessionID)
	return nil
}

// TokenBreakdown holds the token accounting for a prompt before sending.
type TokenBreakdown struct {
	System   int `json:"system"`
	Messages int `json:"messages"`
	Tools    int `json:"tools"`
	Total    int `json:"total"`
	Ceiling  int `json:"ceiling"`
}

// EstimateToolDefTokens estimates total tokens for provider tool definitions.
func EstimateToolDefTokens(tools []llmtypes.ToolDefinition) int {
	total := 0
	for _, t := range tools {
		data, err := json.Marshal(t)
		if err != nil {
			n := len(t.Name) + len(t.Description)
			if n == 0 {
				n = 4
			}
			total += n / 4
			continue
		}
		n := len(data) / 4
		if n == 0 {
			n = 1
		}
		total += n
	}
	return total
}

// EstimateMessagesTokens estimates total tokens for a message slice, including
// both simple content and content blocks (tool_use/tool_result).
func EstimateMessagesTokens(messages []llmtypes.ChatMessage) int {
	total := 0
	for _, m := range messages {
		total += EstimateTokens(m.Content)
		for _, b := range m.ContentBlocks {
			total += EstimateTokens(b.Text) + EstimateTokens(b.Content)
			if b.Input != nil {
				data, _ := json.Marshal(b.Input)
				total += len(data) / 4
			}
		}
	}
	return total
}

// EnforceTokenBudget is the unified enforcement gate that runs before every
// provider call. It checks the total estimated tokens (system + messages + tools)
// against the hard ceiling and applies a reduction cascade if over budget:
//  1. Prune old tool results from messages (replace with compact references)
//  2. Reduce tool count (drop from end)
//  3. Drop oldest messages
//  4. If still over: return error (refuse to send)
//
// The ceilingOverride parameter allows tighter budgets on retries (pass 0 for default).
// Returns the (possibly modified) messages, tools, and a token breakdown.
func EnforceTokenBudget(
	systemPrompt string,
	messages []llmtypes.ChatMessage,
	tools []llmtypes.ToolDefinition,
	ceilingOverride int,
) ([]llmtypes.ChatMessage, []llmtypes.ToolDefinition, *TokenBreakdown, error) {
	ceiling := int(float64(DefaultContextWindow) * HardCeilingPct)
	if ceilingOverride > 0 {
		ceiling = ceilingOverride
	}

	systemTokens := EstimateTokens(systemPrompt)
	msgTokens := EstimateMessagesTokens(messages)
	toolTokens := EstimateToolDefTokens(tools)
	total := systemTokens + msgTokens + toolTokens

	breakdown := &TokenBreakdown{
		System:   systemTokens,
		Messages: msgTokens,
		Tools:    toolTokens,
		Total:    total,
		Ceiling:  ceiling,
	}

	slog.Debug("broker: token gate",
		"system", systemTokens, "messages", msgTokens, "tools", toolTokens,
		"total", total, "ceiling", ceiling)

	if total <= ceiling {
		return messages, tools, breakdown, nil
	}

	// Step 1: Prune old tool results from messages (keep last 2 tool-use pairs).
	messages = pruneToolResultsInMemory(messages)
	msgTokens = EstimateMessagesTokens(messages)
	total = systemTokens + msgTokens + toolTokens
	breakdown.Messages = msgTokens
	breakdown.Total = total
	slog.Debug("broker: after tool-result pruning", "messages", msgTokens, "total", total, "ceiling", ceiling)

	if total <= ceiling {
		return messages, tools, breakdown, nil
	}

	// Step 2: Reduce tool count — drop from end until under budget or 1 tool left.
	for len(tools) > 1 && total > ceiling {
		tools = tools[:len(tools)-1]
		toolTokens = EstimateToolDefTokens(tools)
		total = systemTokens + msgTokens + toolTokens
	}
	breakdown.Tools = toolTokens
	breakdown.Total = total
	slog.Debug("broker: after tool reduction", "tools", len(tools), "total", total, "ceiling", ceiling)

	if total <= ceiling {
		return messages, tools, breakdown, nil
	}

	// Step 3: Drop oldest messages until under budget or only 1 left.
	for len(messages) > 1 && total > ceiling {
		total -= EstimateTokens(messages[0].Content)
		for _, b := range messages[0].ContentBlocks {
			total -= EstimateTokens(b.Text) + EstimateTokens(b.Content)
		}
		messages = messages[1:]
	}
	msgTokens = EstimateMessagesTokens(messages)
	total = systemTokens + msgTokens + toolTokens
	breakdown.Messages = msgTokens
	breakdown.Total = total
	slog.Debug("broker: after message drop", "messages", len(messages), "total", total, "ceiling", ceiling)

	if total <= ceiling {
		return messages, tools, breakdown, nil
	}

	// Step 4: Still over — refuse to send.
	return messages, tools, breakdown, fmt.Errorf(
		"context exceeds hard ceiling after all reductions: %d tokens > %d ceiling", total, ceiling)
}

// classifyContextIntent maps user message keywords to a ContextBroker intent type.
func classifyContextIntent(userMessage string) string {
	lower := userMessage
	if len(lower) > 500 {
		lower = lower[:500]
	}

	// Simple keyword-based classification.
	switch {
	case matchesAny(lower, "debug", "error", "bug", "fix", "broken", "crash", "fail"):
		return contextbroker.IntentDebugIssue
	case matchesAny(lower, "write", "implement", "add", "create", "build", "code"):
		return contextbroker.IntentWriteCode
	case matchesAny(lower, "plan", "design", "feature", "epic", "roadmap"):
		return contextbroker.IntentPlanFeature
	case matchesAny(lower, "why", "decision", "adr", "chose", "rationale"):
		return contextbroker.IntentRecallDecision
	case matchesAny(lower, "resume", "continue", "pick up", "where we left"):
		return contextbroker.IntentResumeTask
	case matchesAny(lower, "boot", "start", "init", "setup", "project"):
		return contextbroker.IntentBootProject
	case matchesAny(lower, "review", "session", "history", "what happened"):
		return contextbroker.IntentReviewSession
	default:
		return contextbroker.IntentCustom
	}
}

// matchesAny returns true if the text contains any of the given substrings.
func matchesAny(text string, subs ...string) bool {
	lower := strings.ToLower(text)
	for _, sub := range subs {
		if strings.Contains(lower, sub) {
			return true
		}
	}
	return false
}

// pruneToolResultsInMemory replaces tool_result content blocks older than the
// last 2 tool-use rounds with compact references. This operates on the in-memory
// message slice without touching the DB.
func pruneToolResultsInMemory(messages []llmtypes.ChatMessage) []llmtypes.ChatMessage {
	// Count tool-use rounds from the end to find the cutoff.
	toolRounds := 0
	cutoffIdx := len(messages)
	for i := len(messages) - 1; i >= 0; i-- {
		m := messages[i]
		for _, b := range m.ContentBlocks {
			if b.Type == "tool_result" {
				toolRounds++
				break
			}
		}
		if toolRounds > ToolResultPruneAge {
			cutoffIdx = i
			break
		}
	}

	if cutoffIdx >= len(messages) {
		return messages // nothing to prune
	}

	pruned := 0
	result := make([]llmtypes.ChatMessage, len(messages))
	copy(result, messages)

	for i := 0; i <= cutoffIdx; i++ {
		m := &result[i]
		if len(m.ContentBlocks) == 0 {
			continue
		}
		newBlocks := make([]llmtypes.ContentBlock, len(m.ContentBlocks))
		copy(newBlocks, m.ContentBlocks)
		for j := range newBlocks {
			b := &newBlocks[j]
			if b.Type == "tool_result" && len(b.Content) > 200 {
				b.Content = fmt.Sprintf("[pruned: tool result, %d chars]", len(b.Content))
				pruned++
			}
		}
		m.ContentBlocks = newBlocks
	}

	if pruned > 0 {
		slog.Debug("broker: pruned tool results in-memory", "count", pruned, "cutoff_idx", cutoffIdx)
	}
	return result
}
