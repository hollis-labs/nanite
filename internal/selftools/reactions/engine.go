// Package reactions implements the harness-reactive self-tools reaction
// engine described in docs/engineering/architecture/
// 11-harness-reactive-self-tools.md ("Where it lives" and "The
// import-cycle constraint, and what it means for each reaction kind"
// sections) and decided by TASKS/reflex-taxonomy/
// 07-harness-reactive-self-tools-design-session.md (decisions 4-6). Built
// by TASKS/harness-reactive-self-tools/03-reaction-engine-core.md.
//
// Fire is the entry point every harness-reactive self-tool handler in
// internal/selftools calls after doing its own (thin) work: it looks up a
// tool's enabled selftool_reactions rows (internal/store/
// selftool_reactions.go) and, per configured reaction kind, either:
//
//   - executes it directly (internal_api_call — a same-process HTTP call
//     via internal_api_call.go's executeInternalAPICall; no import-cycle
//     problem, net/http has no opinion about internal/chat/internal/
//     service), or
//   - resolves it and hands the result back to the caller for delivery
//     (render_card — genuinely needs to reach the SSE/streaming layer
//     above internal/selftools, which this package structurally cannot
//     import: both internal/chat and internal/service already import
//     internal/mcp/internal/selftools, so the reverse import would cycle.
//     This is the same halt_session precedent internal/agent/reflexes/
//     resolve.go's Resolve -> internal/service/chat_generate.go's
//     halt-abort already established: the constrained engine does what it
//     can reach, an unconstrained caller one layer up does the rest).
//
// external_api_call/callback are seeded by migration 126 (implemented=0)
// but not executable yet — Fire recognizes and defensively skips them
// (and any other implemented=false kind) rather than silently pretending
// they fired.
//
// This package deliberately does not write reaction telemetry itself —
// TASKS/harness-reactive-self-tools/05-selftool-reaction-telemetry.md
// builds a parallel EmitReactionTrace wrapper consuming Fire's own Result
// (result.go), mirroring internal/agent/reflexes/telemetry.go's
// EmitFirings/TraceStore split from resolve.go's Resolve. Result is
// designed now with that consumer in mind so 05 doesn't need to change
// Fire's signature.
package reactions

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/hollis-labs/nanite/internal/store"
)

// Reaction kind slugs — must match selftool_reaction_kinds.slug exactly,
// seeded verbatim by internal/store/migrations/126_selftool_reactions.sql
// (TASKS/harness-reactive-self-tools/02-reactive-layer-schema.md).
const (
	KindRenderCard      = "render_card"
	KindInternalAPICall = "internal_api_call"
	KindExternalAPICall = "external_api_call"
	KindCallback        = "callback"
)

// Store is the narrow persistence surface Fire needs: enumerating a
// tool's enabled reaction rows and resolving a reaction_kind slug's
// execution metadata (category/implemented). *store.Store
// (internal/store/selftool_reactions.go) already satisfies this via its
// ListEnabledSelftoolReactions/GetSelftoolReactionKind methods — kept as
// a narrow two-method interface, not a full *store.Store parameter,
// mirroring internal/agent/reflexes/resolve.go's ActionKindLookup/
// CooldownFunc and internal/agent/reflexes/telemetry.go's TraceStore
// narrowing (internal/agent/reflexes/telemetry.go:56-60) — the same
// "narrow the dependency surface to exactly what's needed" precedent
// this task's own "What to do" item 4 explicitly follows. Reaction
// telemetry (05's own, separate TraceStore, mirroring how reflexes'
// TraceStore lives in telemetry.go rather than resolve.go) is deliberately
// not part of this interface — Fire doesn't write telemetry itself.
type Store interface {
	ListEnabledSelftoolReactions(ctx context.Context, toolName string) ([]store.SelftoolReaction, error)
	GetSelftoolReactionKind(ctx context.Context, slug string) (*store.SelftoolReactionKind, error)
}

// defaultHTTPTimeout bounds a single internal_api_call HTTP request. This
// package is explicitly not asked to implement retries/idempotency (see
// TASKS/harness-reactive-self-tools/README.md's "What this batch does NOT
// do") — a bounded timeout is baseline hygiene, not scope creep: without
// one, a hung same-process endpoint would block Fire (and, transitively,
// whichever self-tool handler called it) indefinitely.
const defaultHTTPTimeout = 10 * time.Second

// Engine holds Fire's dependencies: the narrow Store surface above, and an
// *http.Client for the internal_api_call executor (internal_api_call.go).
// Constructed once at wiring time — mirrors internal/agent/reflexes/
// engine.go's NewEngine(*store.Store, *slog.Logger) shape — then Fire is
// called per harness-reactive self-tool invocation.
type Engine struct {
	store      Store
	httpClient *http.Client
	logger     *slog.Logger
}

// NewEngine constructs an Engine. httpClient may be nil (a client with
// defaultHTTPTimeout is used); logger may be nil (slog.Default() is
// used). st must be non-nil.
func NewEngine(st Store, httpClient *http.Client, logger *slog.Logger) *Engine {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultHTTPTimeout}
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Engine{store: st, httpClient: httpClient, logger: logger}
}

// Fire is the entry point every harness-reactive self-tool handler calls
// after doing its own (thin) work — see TASKS/harness-reactive-self-tools/
// 03-reaction-engine-core.md's "What to do" item 1 for the full per-kind
// branching this implements:
//
//   - looks up toolName's enabled selftool_reactions rows;
//   - for each row, looks up its reaction_kind_id against
//     selftool_reaction_kinds for category/implemented;
//   - implemented=false: recorded as OutcomeSkippedNotImplemented, not
//     executed, does not error the whole call;
//   - internal_api_call: executed synchronously (internal_api_call.go);
//   - render_card: resolved (render_card.go) and the resolved envelope
//     JSON is included in the returned Result, never delivered from here;
//   - external_api_call/callback: defensively skipped even if somehow
//     marked implemented=true (no real execution code exists for either
//     yet) — see the switch below for why this is coded explicitly rather
//     than left to the generic implemented=false check alone.
//
// Every enabled row is processed independently: one row failing (a bad
// config, a non-2xx internal_api_call response, an unimplemented kind)
// never blocks a sibling row from also firing. Fire only returns a
// non-nil error when it can't even enumerate toolName's reactions (a
// store error) — never for an individual reaction's own outcome, which is
// always recorded per-entry in Result.Reactions instead.
func (e *Engine) Fire(ctx context.Context, toolName string, payload map[string]any) (Result, error) {
	result := Result{ToolName: toolName, Reactions: []FiredReaction{}}

	rows, err := e.store.ListEnabledSelftoolReactions(ctx, toolName)
	if err != nil {
		return result, fmt.Errorf("reactions.Fire(%q): list enabled reactions: %w", toolName, err)
	}

	for _, row := range rows {
		fr := FiredReaction{
			ReactionID: row.ID,
			Kind:       row.ReactionKindID,
			Config:     row.Config,
		}

		kind, kerr := e.store.GetSelftoolReactionKind(ctx, row.ReactionKindID)
		if kerr != nil {
			fr.Outcome = OutcomeError
			fr.Error = fmt.Sprintf("look up reaction kind %q: %v", row.ReactionKindID, kerr)
			e.logger.Warn("reactions.Fire: reaction_kind lookup failed",
				"tool", toolName, "reaction_id", row.ID, "kind", row.ReactionKindID, "err", kerr)
			result.Reactions = append(result.Reactions, fr)
			continue
		}
		fr.Category = kind.Category

		if !kind.Implemented {
			fr.Outcome = OutcomeSkippedNotImplemented
			e.logger.Warn("reactions.Fire: configured reaction kind is not implemented, skipping",
				"tool", toolName, "reaction_id", row.ID, "kind", row.ReactionKindID)
			result.Reactions = append(result.Reactions, fr)
			continue
		}

		switch row.ReactionKindID {
		case KindInternalAPICall:
			if execErr := e.executeInternalAPICall(ctx, row.Config, payload); execErr != nil {
				fr.Outcome = OutcomeError
				fr.Error = execErr.Error()
				e.logger.Warn("reactions.Fire: internal_api_call execution failed",
					"tool", toolName, "reaction_id", row.ID, "err", execErr)
			} else {
				fr.Outcome = OutcomeSuccess
			}

		case KindRenderCard:
			payloadJSON, resErr := ResolveRenderCard(row.Config, payload)
			if resErr != nil {
				fr.Outcome = OutcomeError
				fr.Error = resErr.Error()
				e.logger.Warn("reactions.Fire: render_card resolution failed",
					"tool", toolName, "reaction_id", row.ID, "err", resErr)
			} else {
				fr.Outcome = OutcomeSuccess
				fr.Payload = payloadJSON
			}

		case KindExternalAPICall, KindCallback:
			// Defensive only, per the task's own instruction: migration
			// 126 seeds both kinds implemented=false, so the
			// !kind.Implemented branch above already intercepts these in
			// practice today. This explicit case exists so a future
			// operator who hand-flips implemented=true on either kind
			// before real execution code lands here still gets a
			// recorded, logged skip instead of silently falling into
			// whatever the default case below would do for a kind this
			// engine has never heard of.
			fr.Outcome = OutcomeSkippedNotImplemented
			e.logger.Warn("reactions.Fire: reaction kind has no execution code in this engine yet, skipping",
				"tool", toolName, "reaction_id", row.ID, "kind", row.ReactionKindID)

		default:
			// A reaction_kind slug this engine doesn't recognize at all —
			// e.g. a future kind added to selftool_reaction_kinds and
			// marked implemented=true without this switch being updated
			// for it yet. Skip, don't guess.
			fr.Outcome = OutcomeSkippedNotImplemented
			e.logger.Warn("reactions.Fire: unrecognized reaction kind, skipping",
				"tool", toolName, "reaction_id", row.ID, "kind", row.ReactionKindID)
		}

		result.Reactions = append(result.Reactions, fr)
	}

	return result, nil
}
