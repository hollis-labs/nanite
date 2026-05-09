// Package envelope_render is the B3 pilot executor (CW-20260429-0032). It
// recognizes the "render_envelope" intent and produces one of the v1
// passive-renderable envelope types per dispatch.
//
// Per the B1 design (docs/architecture/executor-handoff.md), the executor
// owns the multi-step lens flow that previously bloated the Chat agent's
// surface: schema lookup → field resolution → emit → validate → repair.
// The Chat agent dispatches and is done; the executor returns a typed
// ExecutorResponse.
//
// The B3 pilot ships a deterministic in-process implementation: it
// validates the dispatching caller's already-resolved data against the
// per-type envelope schema, stamps source citations and render-target,
// and returns the envelope. The prompt.md sibling captures the
// LLM-driven version's render judgment for when a future ticket boots
// the executor as a real session — until then the four c119 judgments
// live in the prompt as the canonical reference.
package envelope_render

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/hollis-labs/nanite/internal/dispatch"
	"github.com/hollis-labs/nanite/internal/envelope"
)

// Prompt is the executor's system prompt embedded at build time. The B3
// pilot does not boot a separate session, but the prompt is the
// canonical reference for the four c119 judgments and is loaded by
// future LLM-driven implementations (see Executor.SystemPrompt).
//
//go:embed prompt.md
var Prompt string

// IntentRenderEnvelope is the intent vocabulary token the executor
// recognizes. The classifier (B2 — CW-20260429-0031) emits this token
// when the user request maps to envelope rendering.
const IntentRenderEnvelope = "render_envelope"

// groundedTypes are the envelope types whose payload is prose grounded
// in tool output. Mirrors mcp.groundedShowCardTypes for parity with the
// chat-direct emit path. When the request carries Sources, they are
// stamped onto data["sources"] post-validation.
var groundedTypes = map[string]bool{
	"report-card":     true,
	"document-viewer": true,
}

// Executor implements dispatch.Executor for the "render_envelope"
// intent. The zero value is usable; no construction needed for the
// pilot (no per-instance config). Future revisions may carry trust
// resolvers, panel access checks, etc.
type Executor struct{}

// New returns the pilot executor. Kept as a constructor so future
// dependencies can be wired without changing call sites.
func New() *Executor { return &Executor{} }

// Intents satisfies dispatch.Executor.
func (*Executor) Intents() []string { return []string{IntentRenderEnvelope} }

// SystemPrompt returns the embedded prompt. Exposed so wiring can
// register the executor's profile when (future) the executor boots as
// a real chat-loop session.
func (*Executor) SystemPrompt() string { return Prompt }

// Execute satisfies dispatch.Executor. Validates the request, runs the
// in-process render flow, and returns an ExecutorResponse. Returns a
// non-nil error only on harness-level wiring problems (none in the
// pilot — the function never returns a non-nil error today, but the
// signature reserves the option).
//
// Flow:
//  1. Sanity-check the intent (defensive — DispatchExecutor already
//     filters, but the executor double-checks).
//  2. Reject TargetEnvelopeType outside the v1 allow-list with a
//     typed unrecoverable failure.
//  3. Reject missing Data with missing_context (the pilot does not
//     resolve ContextHandles — that's a future LLM-driven step).
//  4. Validate Data against the per-type envelope schema. On
//     failure: attempt one repair pass for known coercible mistakes,
//     then re-validate. Persistent failures return missing_context
//     with the structured validator output, plus PartialEnvelope so
//     the Chat agent can render a degraded card if appropriate.
//  5. Stamp `generated_at` for report-card; stamp Sources for grounded
//     types.
//  6. Build the dispatch.Envelope, attach the Summary, return.
func (*Executor) Execute(ctx context.Context, req dispatch.ExecutorRequest) (*dispatch.ExecutorResponse, error) {
	if req.Intent != IntentRenderEnvelope {
		return &dispatch.ExecutorResponse{
			Failure: &dispatch.ExecutorFailure{
				Code:    dispatch.ExecutorFailureInvalidIntent,
				Message: fmt.Sprintf("envelope_render does not recognize intent %q", req.Intent),
			},
			Summary: "Dispatch failed: unsupported intent.",
		}, nil
	}

	envType := strings.TrimSpace(req.TargetEnvelopeType)
	if envType == "" {
		return &dispatch.ExecutorResponse{
			Failure: &dispatch.ExecutorFailure{
				Code:    dispatch.ExecutorFailureMissingContext,
				Message: "target_envelope_type is required for the render_envelope intent",
			},
			Summary: "Dispatch failed: no envelope type supplied.",
		}, nil
	}

	if !envelope.IsPassiveRenderable(envType) {
		allow := strings.Join(envelope.PassiveRenderableTypes, ", ")
		return &dispatch.ExecutorResponse{
			Failure: &dispatch.ExecutorFailure{
				Code: dispatch.ExecutorFailureUnrecoverable,
				Message: fmt.Sprintf(
					"envelope type %q is not addressable by envelope_render. v1 allow-list: %s",
					envType, allow,
				),
			},
			Summary: fmt.Sprintf("Cannot render %q: not on the v1 passive-renderable allow-list.", envType),
		}, nil
	}

	if req.Data == nil {
		return &dispatch.ExecutorResponse{
			Failure: &dispatch.ExecutorFailure{
				Code: dispatch.ExecutorFailureMissingContext,
				Message: fmt.Sprintf(
					"data payload is required for %s; the in-process pilot does not resolve ContextHandles",
					envType,
				),
			},
			Summary: "Dispatch failed: no envelope data supplied.",
		}, nil
	}

	// Defensive copy — validation may mutate (it does not today, but
	// future schema-stamping would). Keeping the caller's map intact
	// matches the principle of least surprise for a synchronous
	// dispatch primitive.
	data := cloneShallow(req.Data)

	repaired := false
	if err := envelope.ValidateData(envType, data); err != nil {
		// Repair pass — coerce known shape mistakes (e.g., a string in a
		// field where the schema expects a list of strings). Bounded to
		// one attempt; persistent failures escalate to missing_context.
		if attemptRepair(envType, data) {
			repaired = true
			if reErr := envelope.ValidateData(envType, data); reErr != nil {
				return failValidation(envType, data, reErr), nil
			}
		} else {
			return failValidation(envType, data, err), nil
		}
	}

	// Stamp sources after validation — schemas use additionalProperties
	// false and don't currently model `sources`, matching the chat-direct
	// emit path's post-validation stamp (mcp.callShowCard).
	if groundedTypes[envType] {
		if len(req.Sources) == 0 {
			return &dispatch.ExecutorResponse{
				Failure: &dispatch.ExecutorFailure{
					Code: dispatch.ExecutorFailureMissingContext,
					Message: fmt.Sprintf(
						"%s requires at least one source citation; pass ExecutorRequest.Sources or "+
							"set SyntheticAllowed and supply a synthesized source",
						envType,
					),
				},
				Summary: fmt.Sprintf("Cannot render %q: missing required source citations.", envType),
			}, nil
		}
		data["sources"] = sourcesToMap(req.Sources)
	}

	if envType == "report-card" {
		if _, has := data["generated_at"]; !has {
			data["generated_at"] = time.Now().UTC().Format(time.RFC3339)
		}
	}

	env := &dispatch.Envelope{
		Kind:         "envelope",
		Version:      1,
		Type:         envType,
		Data:         data,
		RenderTarget: envelope.DefaultRenderTarget(envType),
	}
	if title, _ := data["title"].(string); title != "" {
		env.Title = title
	}

	summary := buildSummary(envType, req, repaired)
	return &dispatch.ExecutorResponse{
		Envelope: env,
		Summary:  summary,
	}, nil
}

// failValidation builds the ExecutorResponse for a final-validation
// miss. Returns missing_context (the recoverable path — the dispatching
// caller can re-shape data and re-dispatch) with a structured error
// list and a PartialEnvelope so the Chat agent has the option of a
// degraded render.
func failValidation(envType string, data map[string]any, err error) *dispatch.ExecutorResponse {
	leaves := envelope.FlattenSchemaError(err, nil)
	msg := fmt.Sprintf("data does not match the %q schema", envType)
	if len(leaves) > 0 {
		// Produce a deterministic, one-line summary of the leaf
		// failures. Sorted by Path so logs / replays compare cleanly.
		parts := make([]string, 0, len(leaves))
		for _, leaf := range leaves {
			path := leaf.Path
			if path == "" {
				path = "<root>"
			}
			parts = append(parts, fmt.Sprintf("%s: %s", path, leaf.Reason))
		}
		sort.Strings(parts)
		msg = fmt.Sprintf("%s (%s)", msg, strings.Join(parts, "; "))
	}
	partial := &dispatch.Envelope{
		Kind:    "envelope",
		Version: 1,
		Type:    envType,
		Data:    data,
	}
	return &dispatch.ExecutorResponse{
		Failure: &dispatch.ExecutorFailure{
			Code:            dispatch.ExecutorFailureMissingContext,
			Message:         msg,
			PartialEnvelope: partial,
		},
		Summary: fmt.Sprintf("Could not render %q: schema validation failed.", envType),
	}
}

// attemptRepair performs bounded, conservative coercions for known
// shape mistakes the dispatching caller is likely to make. Returns
// true when at least one coercion was applied (the caller re-validates).
//
// Pilot scope: report-card.metrics is the most common slip — schema
// requires an array of {label, value} objects; callers sometimes pass
// a single object or a flat label/value at the top level. Other types
// can grow their own coercions when failure modes accumulate.
func attemptRepair(envType string, data map[string]any) bool {
	switch envType {
	case "report-card":
		// Coerce a single metric object into a one-element list when
		// data["metrics"] is an object rather than a list.
		if m, ok := data["metrics"].(map[string]any); ok {
			data["metrics"] = []any{m}
			return true
		}
	case "list-card":
		// Coerce a single string into a one-element list.
		if s, ok := data["items"].(string); ok && s != "" {
			data["items"] = []any{s}
			return true
		}
	}
	return false
}

// cloneShallow returns a shallow copy of m. Sufficient for the pilot —
// validation reads but does not mutate nested structures.
func cloneShallow(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// sourcesToMap converts the typed source list into the
// map-of-strings shape data["sources"] uses on the wire. Mirrors the
// shape mcp.parseSourcesArg produces from raw tool arguments so
// chat-direct and executor-emitted envelopes look identical to the FE.
func sourcesToMap(srcs []dispatch.ExecutorSource) []map[string]any {
	out := make([]map[string]any, 0, len(srcs))
	for _, s := range srcs {
		entry := map[string]any{
			"tool_use_id": s.ToolUseID,
			"tool_name":   s.ToolName,
		}
		if s.Note != "" {
			entry["note"] = s.Note
		}
		out = append(out, entry)
	}
	return out
}

// buildSummary composes the executor's narration. Discloses synthesis
// when SyntheticAllowed was set so the Chat agent can pass the disclosure
// through to the user verbatim (c119 judgment 1).
func buildSummary(envType string, req dispatch.ExecutorRequest, repaired bool) string {
	parts := []string{fmt.Sprintf("Rendered %s.", envType)}
	if req.SyntheticAllowed {
		parts = append(parts, "This card uses synthesized demo data — no real metrics were fetched.")
	}
	if repaired {
		parts = append(parts, "Coerced one shape mistake during validation.")
	}
	return strings.Join(parts, " ")
}

// Compile-time assertion that *Executor satisfies dispatch.Executor.
var _ dispatch.Executor = (*Executor)(nil)

// errPilotInProcessOnly is reserved for future failure modes that the
// in-process pilot cannot handle. Kept defined so callers can switch on
// errors.Is even when the value is not yet returned anywhere.
var errPilotInProcessOnly = errors.New("envelope_render: in-process pilot cannot satisfy this request")

var _ = errPilotInProcessOnly // placeholder until LLM-driven path lands
