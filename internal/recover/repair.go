// Package recover — C2 LLM-augmented repair pipeline (CW-20260429-0008).
//
// Layer 3 of the self-healing-tool-surface lens. C1 produced the
// deterministic foundation (Classify + RecoverableError); C2 layers an
// LLM-augmented repair pass on top.
//
// Design rules (from the ticket and the lens doc):
//
//  1. Strict structural-only repair. The repair LLM may rearrange / rename
//     / drop fields to match the schema, NEVER fabricate values, NEVER add
//     fields not present in sent_args (unless the schema gives a default).
//     Missing required values are reported, not guessed.
//
//  2. Bounded loop. Hard iteration cap of 1. After a successful repair we
//     retry the tool ONCE. If retry fails, return the ORIGINAL error to
//     the agent — do not compound errors, do not nest repair on retry
//     failure.
//
//  3. Inform the caller. On success, the result envelope carries a
//     repair_note { original_args, repaired_args, lesson_hint, kind, path }
//     so the calling agent learns what was reshaped.
//
//  4. Cost gates. NANITE_AUTO_REPAIR=false bypasses entirely. User pref
//     auto_repair_pref ∈ {always, never} (default always).
//
//  5. Timeout fall-through. On timeout, return the C1 structured error
//     unchanged. The repair budget is small (200-300 ms default).
//
// This file holds only the LLM call + result-shaping primitives. The
// retry-once orchestration and tool re-invocation lives in the harness
// (internal/service/tool.go) so the recover package stays free of
// transport coupling.
package recover

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hollis-labs/go-providers/provider"
)

// DefaultRepairModel is the Haiku-class model used when
// NANITE_REPAIR_MODEL is unset. Resolved at runtime against the model
// registry; "claude-haiku-4-5" is a stable user-friendly ID that maps to
// the latest Haiku 4.5 snapshot.
const DefaultRepairModel = "claude-haiku-4-5"

// DefaultRepairTimeout bounds a single repair LLM call. Haiku is fast,
// but a real Anthropic API call (TCP+TLS handshake, server-side queueing,
// and inference on a repair-sized prompt+schema payload) routinely crosses
// 1.5s end-to-end — the prior 1500ms default fired before Anthropic could
// respond, so the repair pipeline was effectively dead code in production.
// 5s is a conservative ceiling that lets a typical Haiku repair complete
// while still bounding a stuck call. Operators tune via
// NANITE_REPAIR_TIMEOUT_MS (env override) or config (Timeout field).
const DefaultRepairTimeout = 5000 * time.Millisecond

// DefaultRepairMaxTokens is the output-token cap the repair LLM should
// honor when emitting its single JSON object. The repair payload is
// bounded — repaired_args + missing_required + lesson_hint — so 4096
// tokens is a comfortable ceiling that leaves room for a moderately
// large repaired_args while still bounding runaway responses.
//
// CW-20260429-0028: c113 evidence showed the repair LLM emitting
// truncated mid-stream responses that parseRepairResponse could not
// reassemble even after the CW-20260429-0023 markdown-fence stripping
// landed. Setting an explicit cap is the structural fix for the
// truncation symptom; tightening the system prompt (see
// repairSystemPrompt) is the complementary behavioral fix.
//
// DefaultRepairMaxTokens is the per-call output ceiling threaded into
// provider.ChatRequest.MaxTokens for the repair LLM pass. Callers can
// override it via RepairOptions.MaxTokens (used by tests + by callers
// that want a tighter budget for shape-only fixes).
const DefaultRepairMaxTokens = 4096

// MaxArgsBytes caps the size of sent_args we serialize into the repair
// prompt. Pathologically large args are truncated; the repair pass is
// for shape mistakes, not megabyte payloads.
const MaxArgsBytes = 16 * 1024 // 16 KiB

// MaxSchemaBytes caps the size of the schema we send to the repair LLM.
// A repair pass cannot fix a runaway schema, and very large schemas
// would dwarf the args; cap at 32 KiB.
const MaxSchemaBytes = 32 * 1024

// RepairOutcome captures the result of a single LLM repair call. Either
// RepairedArgs is non-nil (callers should retry the tool with these args)
// or MissingRequired is non-empty (no retry — the agent must supply the
// missing values). LessonHint is always populated on success (best-effort).
type RepairOutcome struct {
	// RepairedArgs is the LLM's reshaped argument map. Nil when the
	// repair could not produce a usable shape (e.g. missing required
	// values). Callers MUST check len(MissingRequired) first.
	RepairedArgs map[string]any `json:"repaired_args,omitempty"`

	// MissingRequired lists schema-required fields that were absent
	// from sent_args and could not be supplied without fabrication.
	// When non-empty, callers MUST NOT retry — surface the list to
	// the agent so it can supply real values.
	MissingRequired []string `json:"missing_required,omitempty"`

	// LessonHint is a short (one-sentence) note describing the
	// reshape, suitable for storing as a learning hint. Format is
	// agent-readable but not freeform — the prompt pins it short.
	LessonHint string `json:"lesson_hint,omitempty"`

	// LatencyMS is the wall-clock latency of the repair LLM call,
	// stamped by the orchestrator for telemetry.
	LatencyMS int64 `json:"latency_ms,omitempty"`
}

// HasRepair reports whether the outcome carries a usable repaired-args
// payload. False when MissingRequired is non-empty.
func (o *RepairOutcome) HasRepair() bool {
	if o == nil {
		return false
	}
	return o.RepairedArgs != nil && len(o.MissingRequired) == 0
}

// SchemaProvider is the narrow surface Repair needs to look up the
// schema for the failing tool. ToolService satisfies this in production
// (via GetToolSchema). Tests can pass a stub.
type SchemaProvider interface {
	GetToolSchema(toolName string) map[string]any
}

// RepairOptions is the optional knob bag for Repair. All fields have
// safe defaults; the typical caller can pass RepairOptions{}.
type RepairOptions struct {
	// Provider is the LLM provider used for the repair call. Required
	// — Repair returns an error when nil.
	Provider provider.Provider

	// Model is the repair model name. When empty, DefaultRepairModel.
	Model string

	// Timeout bounds the repair LLM call. When zero, DefaultRepairTimeout.
	Timeout time.Duration

	// MaxTokens caps the repair LLM's output-token budget. When zero,
	// DefaultRepairMaxTokens. Callers that know they need more headroom
	// (e.g. a tool with an unusually large repaired_args) can override
	// the default. CW-20260429-0028.
	//
	// NOTE — known wiring gap: provider.ChatRequest does not currently
	// expose a MaxTokens field, so this value is captured by Repair()
	// but cannot yet be threaded into the outgoing provider request. See
	// the DefaultRepairMaxTokens docstring for the upstream-fix path.
	MaxTokens int

	// SchemaProvider supplies the schema for the failing tool. When nil
	// the repair prompt omits the schema; the LLM still has the error
	// path/reason and may produce a useful reshape, but the strict-mode
	// guarantee is weaker. Production callers should always provide one.
	SchemaProvider SchemaProvider
}

// resolveRepairMaxTokens returns the effective output-token cap for a
// repair LLM call: the caller's override when positive, otherwise the
// package default. Centralized so the resolution rule has a single
// definition and a single test surface (CW-20260429-0028).
func resolveRepairMaxTokens(opts RepairOptions) int {
	if opts.MaxTokens > 0 {
		return opts.MaxTokens
	}
	return DefaultRepairMaxTokens
}

// Repair calls a Haiku-class LLM to reshape args around a recoverable
// schema/type/format error. The function is pure: it does not retry the
// underlying tool, does not log telemetry (the caller stamps that), and
// does not consult the cost gate (the caller does).
//
// Returns:
//   - (*RepairOutcome, nil) on a clean LLM round-trip whose JSON parses.
//     The outcome may carry RepairedArgs (good repair), MissingRequired
//     (no fabrication), or both empty (LLM declined to reshape).
//   - (nil, error) on transport failure, timeout, or unparseable output.
//     The caller should treat this as "fall through to original error".
func Repair(ctx context.Context, rec *RecoverableError, opts RepairOptions) (*RepairOutcome, error) {
	if rec == nil {
		return nil, errors.New("recover.Repair: nil recoverable error")
	}
	if opts.Provider == nil {
		return nil, errors.New("recover.Repair: nil provider")
	}
	model := opts.Model
	if model == "" {
		model = DefaultRepairModel
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultRepairTimeout
	}
	// CW-20260429-0028: cap the repair LLM's output. Without this, the
	// non-streaming Anthropic adapter previously hardcoded 128 tokens, which
	// silently truncated repair responses mid-JSON (chat session c113).
	// go-providers now honors ChatRequest.MaxTokens; we set it here so the
	// repair task gets the budget it actually needs (default 4096 — bounded
	// because the output is a single small JSON object).
	maxTokens := resolveRepairMaxTokens(opts)

	var schemaDoc map[string]any
	if opts.SchemaProvider != nil {
		schemaDoc = opts.SchemaProvider.GetToolSchema(rec.ToolName)
	}

	systemPrompt := repairSystemPrompt()
	userMsg := buildRepairUserMessage(rec, schemaDoc)

	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req := provider.ChatRequest{
		Model:        model,
		SystemPrompt: systemPrompt,
		Messages: []provider.ChatMessage{
			{Role: "user", Content: userMsg},
		},
		MaxTokens: maxTokens,
	}

	start := time.Now()
	raw, err := opts.Provider.Complete(callCtx, req)
	latency := time.Since(start)
	if err != nil {
		return nil, fmt.Errorf("repair LLM call failed: %w", err)
	}

	out, perr := parseRepairResponse(raw)
	if perr != nil {
		return nil, perr
	}
	out.LatencyMS = latency.Milliseconds()
	return out, nil
}

// repairSystemPrompt returns the constrained system prompt. The prompt
// pins the output schema, the no-fabrication rule, and the missing-
// required fall-through. Kept short so it caches well and so the model
// can spend its budget on the actual reshape.
//
// CW-20260429-0028: the OUTPUT FORMAT block was tightened to explicitly
// forbid markdown code-fence wrapping, since c113 evidence showed Haiku
// emitting ```json...``` -wrapped responses despite the original
// "no markdown fences" hint. The "single bare JSON object" phrasing is a
// load-bearing signature — guarded by TestRepairSystemPrompt_AntiMarkdown.
func repairSystemPrompt() string {
	const fence = "```"
	return `You are a JSON repair assistant. The user supplies a tool name, the args they sent, the JSON Schema the args must satisfy, and the validator's error.

Your job: rearrange, rename, or drop fields in sent_args so the result matches the schema.

HARD RULES — violations break the system:
1. NEVER fabricate values. Do not invent strings, numbers, IDs, paths, or content.
2. NEVER add fields that are not present in sent_args, unless the schema declares a default for that field.
3. If a required field is missing from sent_args and you cannot derive it from another field already in sent_args, DO NOT GUESS. Return it in missing_required and leave repaired_args null.
4. Field-name renames are allowed (e.g. "header" → "title") only when the source value clearly fits the target field's type and description.
5. Wrapping a single object in an array (or unwrapping a single-element array) is allowed when the schema declares an array.
6. Type coercion is allowed for adjacent types only: number↔integer, single-element-array↔scalar. Do NOT coerce string↔number; surface as missing_required if a number is required.

OUTPUT FORMAT: emit a single bare JSON object. Do NOT wrap it in markdown code fences (no ` + fence + `json, no ` + fence + `). Do NOT include preamble or explanation outside the JSON. The first character of your response must be ` + "`{`" + ` and the last must be ` + "`}`" + `. The object schema:
{"repaired_args": <object|null>, "missing_required": [<string>...], "lesson_hint": "<short sentence>"}

- repaired_args: the reshaped args object, OR null if you cannot repair without fabrication.
- missing_required: list field names (relative to the args root, dot-delimited for nested) the agent must supply. Empty list when not applicable.
- lesson_hint: one short sentence describing the reshape — phrased for an agent's future reference, e.g. "report-card requires 'metrics', not 'sections'".`
}

// buildRepairUserMessage composes the user-content blob with all the
// context the repair model needs.
func buildRepairUserMessage(rec *RecoverableError, schemaDoc map[string]any) string {
	var b strings.Builder

	fmt.Fprintf(&b, "tool_name: %s\n\n", rec.ToolName)
	fmt.Fprintf(&b, "error_kind: %s\n", rec.Kind.String())
	if rec.ErrorPath != "" {
		fmt.Fprintf(&b, "error_path: %s\n", rec.ErrorPath)
	}
	if rec.ErrorReason != "" {
		fmt.Fprintf(&b, "error_reason: %s\n", rec.ErrorReason)
	}
	if rec.Suggestion != "" {
		fmt.Fprintf(&b, "validator_suggestion: %s\n", rec.Suggestion)
	}
	b.WriteString("\n")

	b.WriteString("sent_args:\n")
	b.WriteString(marshalCapped(rec.SentArgs, MaxArgsBytes))
	b.WriteString("\n\n")

	if schemaDoc != nil {
		b.WriteString("schema:\n")
		b.WriteString(marshalCapped(schemaDoc, MaxSchemaBytes))
		b.WriteString("\n")
	} else if rec.SchemaURI != "" {
		fmt.Fprintf(&b, "schema_uri: %s (full schema not loaded; reason as best you can from the error path)\n", rec.SchemaURI)
	}

	return b.String()
}

// marshalCapped renders v to JSON and truncates the result if it
// exceeds limit bytes. Always returns valid JSON-ish text — if the
// payload exceeds the limit we tag the truncation explicitly so the
// model knows it's looking at a fragment.
func marshalCapped(v any, limit int) string {
	if v == nil {
		return "null"
	}
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprintf("%q", fmt.Sprint(v))
	}
	if len(raw) <= limit {
		return string(raw)
	}
	return string(raw[:limit]) + "\n/* ...truncated */"
}

// parseRepairResponse parses the LLM's JSON output. Tolerant of
// surrounding chatter (markdown fences, preamble) — extracts the first
// balanced top-level object.
//
// The function defends against the c112 failure mode where the repair
// LLM wraps its response in ` ```json ... ``` ` despite the system
// prompt telling it not to: stripCodeFence is applied first, and if
// extraction still fails we make a second pass on the raw input as a
// belt-and-suspenders fallback.
func parseRepairResponse(raw string) (*RepairOutcome, error) {
	defenced := stripCodeFence(raw)
	body := extractJSONObject(defenced)
	if body == "" && defenced != raw {
		// Second pass on the original (un-defenced) input — covers the
		// pathological case where stripCodeFence misreads a nested fence
		// and inadvertently swallows a real closing brace.
		body = extractJSONObject(raw)
	}
	if body == "" {
		return nil, fmt.Errorf("repair LLM returned no JSON object: %q", trimForLog(raw, 200))
	}
	var resp struct {
		RepairedArgs    map[string]any `json:"repaired_args"`
		MissingRequired []string       `json:"missing_required"`
		LessonHint      string         `json:"lesson_hint"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		return nil, fmt.Errorf("repair LLM JSON parse failed: %w (body=%q)", err, trimForLog(body, 200))
	}
	out := &RepairOutcome{
		RepairedArgs:    resp.RepairedArgs,
		MissingRequired: resp.MissingRequired,
		LessonHint:      strings.TrimSpace(resp.LessonHint),
	}
	// If both repaired_args is nil/empty AND missing_required is empty,
	// treat as "model declined" — leave RepairedArgs nil so HasRepair()
	// is false.
	if len(out.RepairedArgs) == 0 {
		out.RepairedArgs = nil
	}
	return out, nil
}

// stripCodeFence removes a surrounding markdown code fence from raw if
// present. Handles both fenced-with-language-tag (` ```json `) and bare
// (` ``` `) fences. Tolerates whitespace and a leading preamble line by
// scanning for the first fence rather than requiring it at byte zero.
//
// Behavior:
//   - If the trimmed body starts with ` ``` ` (with or without a language
//     tag), the opening fence line (everything up to and including the
//     first newline) is removed.
//   - If the resulting body ends with ` ``` `, the closing fence and any
//     trailing whitespace are removed.
//   - If no fence is detected the input is returned unchanged.
//
// The helper does not validate that the inner content is JSON — that is
// extractJSONObject's job. It only peels the markdown wrapper so the
// brace-counting extractor sees a clean payload.
func stripCodeFence(raw string) string {
	trimmed := strings.TrimSpace(raw)
	// Find the first fence in the trimmed body (or in a leading preamble).
	idx := strings.Index(trimmed, "```")
	if idx < 0 {
		return raw
	}
	// Drop everything through the end of the opening fence line. The
	// language tag (e.g. "json") sits between the fence and the first
	// newline, so the simplest correct rule is to skip up to the first
	// newline after the fence marker.
	after := trimmed[idx+3:]
	nl := strings.IndexByte(after, '\n')
	if nl < 0 {
		// Fence with no newline after it — body is a single line; nothing
		// useful to extract, fall back to the original input.
		return raw
	}
	inner := after[nl+1:]
	// Trim trailing closing fence + whitespace.
	inner = strings.TrimRight(inner, " \t\r\n")
	if strings.HasSuffix(inner, "```") {
		inner = strings.TrimSuffix(inner, "```")
		inner = strings.TrimRight(inner, " \t\r\n")
	}
	if inner == "" {
		return raw
	}
	return inner
}

// extractJSONObject returns the first balanced {...} substring of raw.
// Mirrors the helper in internal/tool/intent/llm.go but kept private to
// this package so the recover layer has no upstream dependency on
// intent.
func extractJSONObject(raw string) string {
	start := strings.IndexByte(raw, '{')
	if start < 0 {
		return ""
	}
	depth := 0
	inString := false
	escape := false
	for i := start; i < len(raw); i++ {
		c := raw[i]
		if escape {
			escape = false
			continue
		}
		if c == '\\' {
			escape = true
			continue
		}
		if c == '"' {
			inString = !inString
			continue
		}
		if inString {
			continue
		}
		switch c {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return raw[start : i+1]
			}
		}
	}
	return ""
}

func trimForLog(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
