// Package learnings is Layer 4 of the self-healing-tool-surface lens
// (CW-20260429-0009, D1). It turns C2's repair_note.lesson_hint into
// durable memory and surfaces past learnings on similar tool selection.
//
// Design rules (from the ticket and the lens doc):
//
//  1. Single Vanta integration boundary. The Recorder/Recaller talk to
//     a narrow LearningStore interface; *memory.Service satisfies it in
//     production, tests substitute an in-memory stub. The mcp/transport
//     packages never touch Vanta directly — only through this package.
//
//  2. Namespaces obey Vanta's strict three-form contract. The ticket
//     described a hierarchical namespace
//     ("user/<user>/memory/learnings/tool_use/<tool>"); Vanta v0.4 only
//     accepts `user/{user}/memory`, `user/{user}/project/{p}/memory`,
//     `user/{user}/session/{s}/memory` (see vanta-conduit/internal/
//     memory/namespaces.go). We map the requested scopes onto those
//     forms and carry tool/scope identity in tags + memory_key:
//
//	tool_use → user/<user>/memory                (tag: tool:<tool>)
//	project  → user/<user>/project/<id>/memory
//	session  → user/<user>/session/<id>/memory
//
//     Recall by tool name combines the user namespace with a
//     tag-filter on `tool:<tool>`, so per-tool isolation still works
//     end-to-end. This is documented loudly because it is the single
//     surprise in this layer.
//
//  3. Confidence default 0.85. Agents are reporting their own learnings,
//     not architectural decisions — high enough to be load-bearing on
//     recall, lower than canonical knowledge.
//
//  4. Status defaults to "draft". Promotion is a separate review action
//     (out of scope for v1).
//
//  5. Recall is best-effort. A nil Recaller, a Vanta error, or a timeout
//     yields zero hints — never an error to the caller. Failing-open is
//     the right call here: a missing learning hurts less than a blocked
//     tool execution.
package learnings

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/hollis-labs/nanite/internal/memory"
)

// DefaultConfidence is the confidence value stamped on every learning
// captured via the lesson_capture self-tool. Per the ticket: agents are
// reporting their own learnings, not architectural decisions.
const DefaultConfidence = 0.85

// DefaultStatus is the lifecycle status stamped on every learning at
// capture time. Promotion to "reviewed" / "canonical" is an explicit
// review action handled outside this package.
const DefaultStatus = "draft"

// DefaultUserID is the user namespace root used when the caller did not
// provide one. Mirrors the rest of the Nanite memory layer (see
// memory.UserNamespace).
const DefaultUserID = "default"

// MaxRecallHints caps the number of hints surfaced per recall call. The
// ticket's v1 budget is 1-2 hints, Recall caps at 2 to leave headroom
// for token-budget-aware callers.
const MaxRecallHints = 2

// RecallSimilarityThreshold is the minimum confidence/similarity score a
// hit must clear before Recall surfaces it. Defensive — Vanta returns
// confidence 0 for keyword-only hits when no embedder is present, so a
// strict threshold could surface zero results in tests; we keep it loose
// (>=0) and let MaxRecallHints + Limit do the gating.
const RecallSimilarityThreshold = 0.0

// Scope enumerates the three valid scopes the lesson_capture tool
// accepts. Scopes drive the namespace shape and the required Subject
// field (see ScopeRequiresSubject).
type Scope string

const (
	// ScopeToolUse is for "lessons about how to call a specific tool"
	// — the C2 → D1 closed-loop case. Subject MUST be the tool name.
	ScopeToolUse Scope = "tool_use"
	// ScopeProject scopes a learning to a project. Subject MUST be the
	// project ID.
	ScopeProject Scope = "project"
	// ScopeSession scopes a learning to a single chat session. Subject
	// MUST be the session ID.
	ScopeSession Scope = "session"
)

// IsValid reports whether s is a recognised scope.
func (s Scope) IsValid() bool {
	switch s {
	case ScopeToolUse, ScopeProject, ScopeSession:
		return true
	}
	return false
}

// String returns the scope's wire form (matches the input enum on
// lesson_capture).
func (s Scope) String() string { return string(s) }

// ScopeRequiresSubject reports whether the scope's namespace requires a
// non-empty Subject. All three v1 scopes do — the namespace would
// collapse to an unindexable bucket otherwise — but the predicate
// future-proofs adding scope variants where the subject is implied.
func ScopeRequiresSubject(s Scope) bool {
	return s == ScopeToolUse || s == ScopeProject || s == ScopeSession
}

// LearningStore is the narrow Vanta surface this package needs. The
// production wiring satisfies it with *memory.Service; tests pass an
// in-memory stub. Keeping the interface tight (Store + Recall only)
// means a switch to a non-Vanta backend stays a one-file change.
type LearningStore interface {
	Store(ctx context.Context, m memory.Memory) error
	Recall(ctx context.Context, opts memory.RecallOpts) ([]memory.Memory, error)
}

// CaptureInput is the Recorder.Capture argument. Mirrors the public
// lesson_capture tool signature so the MCP handler is a thin
// translation layer.
type CaptureInput struct {
	// Scope is one of tool_use / project / session.
	Scope Scope

	// Subject identifies the namespace leaf — the tool name for
	// tool_use, the project_id for project, the session_id for session.
	// Required for all v1 scopes (see ScopeRequiresSubject).
	Subject string

	// Hint is the agent-readable lesson body. One short sentence is
	// the documented expectation; the package does not police length,
	// only emptiness.
	Hint string

	// SourceEventID is the optional repair_note source pointer. When
	// non-empty it lands in tags as `source:<id>` so a future
	// review/UI can join learnings back to the originating turn.
	SourceEventID string

	// SessionID is the originating chat session. Required by the
	// underlying Conduit memory store; falls back to "manual:nanite"
	// when empty (matches memory.Service.Store's own fallback).
	SessionID string

	// UserID anchors the namespace root. Empty falls back to "default"
	// so the same key shape works in single-user dogfood.
	UserID string

	// Tags are extra agent-supplied labels merged with the package's
	// canonical tags ("learning", "self_healed",
	// "captured_during_session", scope, optionally source:<id>).
	Tags []string
}

// CaptureOutcome is the Recorder.Capture return shape. The MCP handler
// surfaces it back to the agent as JSON.
type CaptureOutcome struct {
	// MemoryID is the memory_key the namespace was indexed under.
	// Stable across re-writes of the same hint (key is derived
	// deterministically from the hint body).
	MemoryID string `json:"memory_id"`
	// Namespace is the full Conduit namespace string the entry
	// landed in.
	Namespace string `json:"namespace"`
}

// Recorder writes learnings to the LearningStore. Holds no per-call
// state — it is safe to share across goroutines.
type Recorder struct {
	store LearningStore
}

// NewRecorder returns a Recorder backed by store. Nil store is allowed:
// every Capture call returns an error explaining the missing wiring,
// matching the rest of the self-tool surface's nil-safe pattern.
func NewRecorder(store LearningStore) *Recorder {
	return &Recorder{store: store}
}

// Capture writes a learning to the LearningStore at the scope's
// canonical namespace and returns the resulting memory_id + namespace.
// Validation rules:
//
//   - Scope must be one of the three documented values.
//   - Hint must be non-empty after TrimSpace.
//   - Subject must be non-empty when ScopeRequiresSubject(Scope) is true.
//
// Any rule violation returns an error before the store is touched. The
// caller (the MCP handler) surfaces that error as a structured tool
// result so the agent sees a uniform failure shape.
func (r *Recorder) Capture(ctx context.Context, in CaptureInput) (*CaptureOutcome, error) {
	if r == nil || r.store == nil {
		return nil, errors.New("learnings: recorder not configured (no LearningStore wired)")
	}
	if !in.Scope.IsValid() {
		return nil, fmt.Errorf("learnings: invalid scope %q (want tool_use|project|session)", in.Scope)
	}
	hint := strings.TrimSpace(in.Hint)
	if hint == "" {
		return nil, errors.New("learnings: hint is required and must not be blank")
	}
	if ScopeRequiresSubject(in.Scope) && strings.TrimSpace(in.Subject) == "" {
		return nil, fmt.Errorf("learnings: scope %q requires a non-empty subject (tool_name / project_id / session_id)", in.Scope)
	}

	user := in.UserID
	if user == "" {
		user = DefaultUserID
	}
	namespace := Namespace(in.Scope, user, in.Subject)
	memoryKey := DeriveMemoryKey(in.Scope, in.Subject, hint)

	// Tool-use scope rides the user namespace, so the tool identity
	// has to live in tags for recall to bucket correctly. Other scopes
	// already encode their subject in the namespace itself, so the
	// tool-tag is omitted to keep tag noise down.
	toolForTag := ""
	if in.Scope == ScopeToolUse {
		toolForTag = SanitizeSubject(in.Subject)
	}
	tags := buildTags(in.Scope, toolForTag, in.SourceEventID, in.Tags)

	mem := memory.Memory{
		Namespace:  namespace,
		MemoryKey:  memoryKey,
		Summary:    hint,
		Body:       "", // v1: hint goes in summary; body reserved for follow-up enrichment
		Origin:     "feedback",
		Trigger:    "explicit",
		Confidence: DefaultConfidence,
		Tags:       tags,
		SessionID:  in.SessionID,
		Status:     DefaultStatus,
	}

	if err := r.store.Store(ctx, mem); err != nil {
		return nil, fmt.Errorf("learnings: store: %w", err)
	}
	return &CaptureOutcome{
		MemoryID:  memoryKey,
		Namespace: namespace,
	}, nil
}

// Recaller pulls past learnings for a tool name on demand. Tool-use is
// the only scope we surface in v1 (the lens calls out
// "similar tool selection" as the primary recall surface).
type Recaller struct {
	store LearningStore
}

// NewRecaller returns a Recaller backed by store. Nil store is allowed:
// every RecallByToolName call returns nil, nil so callers fail open.
func NewRecaller(store LearningStore) *Recaller {
	return &Recaller{store: store}
}

// Hint is a single recalled learning ready to be rendered into the
// agent's slot context.
type Hint struct {
	// Summary is the lesson body (the original hint sentence).
	Summary string
	// Confidence is the score Vanta returned for the hit.
	Confidence float64
	// MemoryKey is stable across rewrites; useful for debug telemetry.
	MemoryKey string
	// Namespace is the Vanta namespace the entry came from.
	Namespace string
}

// RecallByToolName returns up to MaxRecallHints learnings for the named
// tool, surfaced from the user's tool_use bucket. Failing-open: a nil
// recaller, a missing store, or a Vanta error returns (nil, nil) so
// callers don't have to defensively wrap every call site.
//
// userID empty falls back to DefaultUserID.
//
// Implementation note: tool_use entries land in the user-scoped
// namespace (Vanta's strict three-form contract — see package doc), so
// the tool identity is reconstructed from a `tool:<name>` tag. We pull
// a generous slice from Vanta and tag-filter client-side because
// memory.RecallOpts.Tags is an OR filter (matches any tag), not a
// strict AND — surfacing the wrong tool's lessons would defeat the
// whole layer.
func (r *Recaller) RecallByToolName(ctx context.Context, userID, toolName string) []Hint {
	if r == nil || r.store == nil || strings.TrimSpace(toolName) == "" {
		return nil
	}
	user := userID
	if user == "" {
		user = DefaultUserID
	}
	ns := Namespace(ScopeToolUse, user, toolName)
	toolTag := "tool:" + SanitizeSubject(toolName)
	opts := memory.RecallOpts{
		Namespaces:    []string{ns},
		Ranking:       "activation",
		Limit:         recallFetchLimit,
		Tags:          []string{toolTag},
		MinConfidence: RecallSimilarityThreshold,
	}
	mems, err := r.store.Recall(ctx, opts)
	if err != nil || len(mems) == 0 {
		return nil
	}
	hints := make([]Hint, 0, MaxRecallHints)
	for _, m := range mems {
		// Defensive: even though we asked for the tool-tag, a recall
		// against the broad user namespace can surface other entries
		// that share *any* of the requested tags (Conduit's filter is
		// permissive). Re-check.
		if !hasTag(m.Tags, "learning") || !hasTag(m.Tags, toolTag) {
			continue
		}
		hints = append(hints, Hint{
			Summary:    m.Summary,
			Confidence: m.Confidence,
			MemoryKey:  m.MemoryKey,
			Namespace:  m.Namespace,
		})
		if len(hints) >= MaxRecallHints {
			break
		}
	}
	if len(hints) == 0 {
		return nil
	}
	return hints
}

// recallFetchLimit is how many candidates we ask Vanta for before the
// client-side strict-AND tag filter narrows to MaxRecallHints. Headroom
// of 10x covers the common case where many learnings share the
// "learning" tag but only a few share the per-tool tag.
const recallFetchLimit = 20

// hasTag returns true when tags contains target (case-sensitive — tags
// are stored verbatim).
func hasTag(tags []string, target string) bool {
	for _, t := range tags {
		if t == target {
			return true
		}
	}
	return false
}

// Namespace returns the Conduit namespace string for a given scope +
// subject. Public so the MCP handler can echo it back in the
// CaptureOutcome and so tests can assert on the exact path without
// reimplementing the formatter.
//
// Shape (constrained by Vanta's three-form namespace contract — see
// package doc for the rationale and the `vanta-conduit/internal/memory/
// namespaces.go` policy):
//
//	tool_use → user/<user>/memory                           (subject lives in tags + memory_key)
//	project  → user/<user>/project/<subject>/memory
//	session  → user/<user>/session/<subject>/memory
func Namespace(scope Scope, userID, subject string) string {
	if userID == "" {
		userID = DefaultUserID
	}
	subject = SanitizeSubject(subject)
	switch scope {
	case ScopeProject:
		return fmt.Sprintf("user/%s/project/%s/memory", userID, subject)
	case ScopeSession:
		return fmt.Sprintf("user/%s/session/%s/memory", userID, subject)
	case ScopeToolUse:
		fallthrough
	default:
		return fmt.Sprintf("user/%s/memory", userID)
	}
}

// SanitizeSubject normalizes a subject so it is a safe namespace
// segment: lowercased, characters outside [a-z0-9_-] mapped to
// underscores, runs of underscores collapsed, leading/trailing
// underscores trimmed.
//
// Vanta's namespace key constraint is `a-z 0-9 _` per segment (per the
// global instruction note); we keep `-` here too because tool names
// contain dashes (`my-tool`) and the wider memory layer accepts them.
// Underscore canonicalization happens at the package boundary so
// callers don't have to think about it.
func SanitizeSubject(subject string) string {
	s := strings.ToLower(strings.TrimSpace(subject))
	if s == "" {
		return "_"
	}
	var b strings.Builder
	prevUnderscore := false
	for _, r := range s {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-':
			b.WriteRune(r)
			prevUnderscore = false
		default:
			if !prevUnderscore {
				b.WriteRune('_')
				prevUnderscore = true
			}
		}
	}
	out := strings.Trim(b.String(), "_-")
	if out == "" {
		return "_"
	}
	return out
}

// keyKeepRE matches characters allowed in a Vanta memory_key (per the
// global instruction: `a-z 0-9 _` per segment).
var keyKeepRE = regexp.MustCompile(`[^a-z0-9_]+`)

// MaxMemoryKeyLen is Vanta's per-segment ceiling on memory_key length.
// Pinned here so changes in vanta-conduit/internal/memory/keys.go
// (which currently caps at 64) surface as a deliberate update rather
// than a silent runtime rejection.
const MaxMemoryKeyLen = 64

// DeriveMemoryKey returns a stable, sanitized memory_key for a hint
// scoped to (scope, subject). Same (scope, subject, hint) → same key,
// so re-writing the same lesson updates the existing entry rather than
// spamming duplicates (lesson dedup at the key level — the ticket
// explicitly defers cross-key dedup to a follow-up).
//
// The scope/subject prefix matters because tool_use entries share a
// single user namespace (Vanta's strict three-form rule); without the
// prefix two different tools' identical hint sentences would collapse
// into one row.
//
// The result is truncated to MaxMemoryKeyLen so writes never trip
// Vanta's per-segment length cap. Truncation favours the prefix
// (scope+subject) so different hints under the same (scope, subject)
// still land in distinct keys when at all possible — within budget.
func DeriveMemoryKey(scope Scope, subject, hint string) string {
	body := strings.ToLower(strings.TrimSpace(hint))
	body = keyKeepRE.ReplaceAllString(body, "_")
	body = strings.Trim(body, "_")
	if body == "" {
		body = "learning"
	}
	subjectKey := strings.ReplaceAll(SanitizeSubject(subject), "-", "_")
	var prefix string
	if subjectKey == "" || subjectKey == "_" {
		prefix = "learning_" + string(scope) + "__"
	} else {
		prefix = "learning_" + string(scope) + "_" + subjectKey + "__"
	}
	// If the prefix already overruns the budget, truncate it. This
	// only fires for absurdly long subjects; the resulting key still
	// satisfies the regex constraint.
	if len(prefix) >= MaxMemoryKeyLen {
		prefix = prefix[:MaxMemoryKeyLen]
		return strings.Trim(prefix, "_")
	}
	budget := MaxMemoryKeyLen - len(prefix)
	if len(body) > budget {
		body = body[:budget]
	}
	body = strings.Trim(body, "_")
	if body == "" {
		// Trim ate everything — fall back to a literal token so the
		// key is still indexable.
		body = "x"
	}
	return prefix + body
}

// SystemPromptBlock renders the agent-facing block surfaced when prior
// tool-use learnings exist. Returns "" when hints is empty so callers
// can concatenate without a guard. Its heading-and-list shape keeps the
// chat surface consistent with other injected context blocks.
func SystemPromptBlock(toolName string, hints []Hint) string {
	if len(hints) == 0 {
		return ""
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "## Prior learnings for %s\n", toolName)
	for _, h := range hints {
		sb.WriteString("- ")
		sb.WriteString(h.Summary)
		sb.WriteString("\n")
	}
	return sb.String()
}

// buildTags assembles the canonical tag set for a learning. The order
// matters for downstream consumers that pattern-match on the first tag
// (Conduit treats tags as an unordered set, but human review tools
// often render them in insertion order).
//
// toolForTag is the sanitised tool name to encode as `tool:<name>`.
// Empty for non-tool_use scopes (where the tool identity is either
// absent or implied by a different field).
func buildTags(scope Scope, toolForTag, sourceEventID string, extra []string) []string {
	tags := []string{
		"learning",
		"self_healed",
		"captured_during_session",
		scope.String(),
	}
	if toolForTag != "" {
		tags = append(tags, "tool:"+toolForTag)
	}
	if strings.TrimSpace(sourceEventID) != "" {
		tags = append(tags, "source:"+strings.TrimSpace(sourceEventID))
	}
	for _, t := range extra {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		tags = append(tags, t)
	}
	return tags
}
