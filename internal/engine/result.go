package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"
	"unicode/utf8"

	"github.com/hollis-labs/libs/agent/core/toolresult"
	"github.com/hollis-labs/nanite/internal/background"
	"github.com/hollis-labs/nanite/internal/elicitation"
	"github.com/hollis-labs/nanite/internal/events"
	"github.com/hollis-labs/nanite/internal/subagent"
	"github.com/hollis-labs/nanite/internal/third_party/jsonschema"
	"github.com/hollis-labs/nanite/internal/tools"
)

// subtaskState is what the engine holds for one running subtask: the schema
// its result must match and, once the child calls return_result, what it
// returned. The child writes it; the parent reads it after the child's run.
type subtaskState struct {
	schema *jsonschema.Resolved
	// repaired is set once a result failed the schema and the child was
	// given its one repair.
	repaired bool
	returned *returnedResult
}

type returnedResult struct {
	subagent.Return
	// Reason explains a status other than ok, such as a result that still
	// failed the schema after its repair.
	Reason string
}

// openSubtask registers a child before it runs, so its return_result has
// somewhere to land.
func (e *Engine) openSubtask(child string, schema *jsonschema.Resolved) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.subtasks == nil {
		e.subtasks = make(map[string]*subtaskState)
	}
	e.subtasks[child] = &subtaskState{schema: schema}
}

// hasReturned reports whether a running child has handed back its result.
func (e *Engine) hasReturned(child string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	state := e.subtasks[child]
	return state != nil && state.returned != nil
}

// closeSubtask forgets a child and returns what it returned, if anything.
func (e *Engine) closeSubtask(child string) *returnedResult {
	e.mu.Lock()
	defer e.mu.Unlock()
	state := e.subtasks[child]
	delete(e.subtasks, child)
	if state == nil {
		return nil
	}
	return state.returned
}

// maxValidationError bounds the schema error text a child and parent see.
const maxValidationError = 600

// returnResult serves a child's return_result call. A result that fails the
// return spec's schema gets one repair; a second failure is accepted as
// partial with the validation error, and either way the child's turn ends.
// The schema is checked without holding the engine lock.
func (x *execution) returnResult(ctx context.Context, in subagent.Return) (tools.Result, error) {
	switch in.Status {
	case subagent.StatusOK, subagent.StatusPartial, subagent.StatusError:
	case subagent.StatusDenied:
		// Only the engine reports denied, when a person refused an approval
		// the subtask asked for; a child cannot claim it.
		return tools.Result{}, fmt.Errorf("status denied is set by the engine when a person refuses an approval, not by return_result; use ok, partial or error and say what was refused in reason")
	default:
		return tools.Result{}, fmt.Errorf("status must be ok, partial or error")
	}
	if len(bytes.TrimSpace(in.Result)) == 0 {
		return tools.Result{}, fmt.Errorf("result required")
	}
	e := x.engine
	e.mu.Lock()
	state := e.subtasks[x.info.ID]
	var schema *jsonschema.Resolved
	if state != nil {
		schema = state.schema
	}
	e.mu.Unlock()
	if state == nil {
		return tools.Result{}, fmt.Errorf("return_result is only for a running subtask")
	}
	var problem string
	if schema != nil {
		if err := validateResult(ctx, schema, in.Result); err != nil {
			problem = truncateText(err.Error(), maxValidationError)
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if state.returned != nil {
		x.returned.Store(true)
		return tools.Result{Text: "A result was already returned; this one was ignored."}, nil
	}
	reason := ""
	if problem != "" {
		if !state.repaired {
			state.repaired = true
			return tools.Result{IsError: true, Text: "The result does not match the return spec: " + problem +
				". Fix the result and call return_result again; this is your one repair."}, nil
		}
		in.Status, reason = subagent.StatusPartial, "return_spec: "+problem
	}
	if reason == "" && in.Status != subagent.StatusOK {
		reason = "reported " + string(in.Status) + " by the subtask"
	}
	state.returned = &returnedResult{Return: in, Reason: reason}
	x.returned.Store(true)
	return tools.Result{Text: "Returned to the parent."}, nil
}

func validateResult(ctx context.Context, schema *jsonschema.Resolved, raw json.RawMessage) error {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return fmt.Errorf("result is not valid JSON: %w", err)
	}
	return elicitation.Validate(ctx, schema, value)
}

// subtaskCost is a subtask's spend. TokensIn counts every input token,
// cached or not. USD is set once usage carries a price.
type subtaskCost struct {
	TokensIn  int      `json:"tokens_in"`
	TokensOut int      `json:"tokens_out"`
	USD       *float64 `json:"usd,omitempty"`
}

// subtaskResult is what the parent model receives from a subtask.
type subtaskResult struct {
	Status    subagent.Status `json:"status"`
	Result    json.RawMessage `json:"result"`
	Artifacts []string        `json:"artifacts"`
	Notes     string          `json:"notes,omitempty"`
	Reason    string          `json:"reason,omitempty"`
	// ResultRef, ResultBytes, ResultExpiresAt and Preview are set when the
	// return was over its cap: the full result, artifacts and notes are
	// stored behind ResultRef for fetch_tool_result, and Result is null.
	ResultRef       string      `json:"result_ref,omitempty"`
	ResultBytes     int         `json:"result_bytes,omitempty"`
	ResultExpiresAt string      `json:"result_expires_at,omitempty"`
	Preview         string      `json:"preview,omitempty"`
	Cost            subtaskCost `json:"cost"`
	ChildID         string      `json:"child_id"`
	Variant         string      `json:"variant"`
	Steps           int         `json:"steps"`
}

// Bounds on what a subtask hands its parent.
const (
	// maxLastOutput bounds the summary of a child's last output that the
	// parent gets when the child did not call return_result.
	maxLastOutput = 2 << 10
	// subtaskEnvelopeLimit is the most a subtask result may take, whatever
	// the configured cap: a tool result shown in full (toolResultBudget), less
	// room for background_status's job fields around it.
	subtaskEnvelopeLimit = toolResultBudget - 1<<10
	maxArtifacts         = 50
	maxArtifactBytes     = 512
	maxReasonBytes       = 1 << 10
	// subtaskResultTTL is how long an over-cap result stays readable. It is
	// stored, not cached for the hour other tool results get, because a
	// background subtask's parent may read it much later.
	subtaskResultTTL = 30 * 24 * time.Hour
)

// noReturnReason is the reason for a child that ended without return_result.
const noReturnReason = "no_return_result"

// subtaskCapBytes is the cap on the whole result envelope: the variant's or
// agent's cap, never more than subtaskEnvelopeLimit.
func (e *Engine) subtaskCapBytes(variant subagent.Variant) int {
	return subagent.ResultCapBytes(variant, e.cfg.SubtaskResultCap, subtaskEnvelopeLimit)
}

// composeSubtaskResult builds the parent's view of a finished child. status
// and reason are how the child's run ended (subtaskStatus); returned is what
// it handed back, if it called return_result. The marshaled envelope is
// held to the cap for every status. When the return does not fit (or its
// artifacts or notes had to be cut) the whole return is stored under scope
// (the parent conversation) and the parent gets a pointer and a preview.
func (e *Engine) composeSubtaskResult(ctx context.Context, scope, child string, variant subagent.Variant, result events.Result, status subagent.Status, reason string, returned *returnedResult) subtaskResult {
	out := subtaskResult{Status: status, Reason: reason, ChildID: child, Variant: variant.Name, Steps: result.Steps, Artifacts: []string{}}
	u := result.Usage
	out.Cost = subtaskCost{TokensIn: u.InputTokens + u.CacheReadTokens + u.CacheWriteTokens, TokensOut: u.OutputTokens}
	var full struct {
		Result    json.RawMessage `json:"result"`
		Artifacts []string        `json:"artifacts"`
		Notes     string          `json:"notes,omitempty"`
	}
	switch {
	case returned != nil:
		full.Result, full.Artifacts, full.Notes = returned.Result, returned.Artifacts, returned.Notes
		if status == subagent.StatusOK {
			// The run ended normally; the child's own status and reason stand.
			out.Status, out.Reason = returned.Status, returned.Reason
		}
	default:
		if status == subagent.StatusOK {
			out.Status, out.Reason = subagent.StatusPartial, noReturnReason
		}
		if text := lastOutput(result.Text, maxLastOutput); text != "" {
			full.Result, _ = json.Marshal(text)
		}
	}
	if len(full.Result) == 0 {
		full.Result = json.RawMessage("null")
	}
	out.Reason = truncateText(out.Reason, maxReasonBytes)
	capBytes := e.subtaskCapBytes(variant)
	out.Result = full.Result
	out.Notes = truncateText(full.Notes, max(capBytes/8, 256))
	trimmed := out.Notes != full.Notes
	for i, artifact := range full.Artifacts {
		if i == maxArtifacts {
			trimmed = true
			break
		}
		if cut := truncateText(artifact, maxArtifactBytes); cut != artifact {
			artifact, trimmed = cut, true
		}
		out.Artifacts = append(out.Artifacts, artifact)
	}
	if !trimmed && envelopeSize(out) <= capBytes {
		return out
	}
	raw, _ := json.Marshal(full)
	body := e.scrub.Text(string(raw))
	out.Result = json.RawMessage("null")
	out.ResultBytes = len(body)
	out.Preview = truncateText(string(full.Result), min(capBytes/4, 2<<10))
	pointer, err := e.subtaskResults.Put(ctx, scope, toolresult.Meta{Tool: subagent.ToolName, CallID: child}, body)
	if err != nil || !pointer.Stored {
		out.Reason = joinReason(out.Reason, "result over its cap and could not be stored")
		if err != nil {
			e.cfg.Logger.WarnContext(ctx, "store subtask result failed", "child", child, "error", e.scrub.Text(err.Error()))
		}
	} else {
		out.ResultRef = toolresult.DefaultScheme + "://" + pointer.ID
		out.ResultExpiresAt = pointer.ExpiresAt.UTC().Format(time.RFC3339)
		if len(out.Artifacts) == maxArtifacts {
			out.Artifacts = out.Artifacts[:maxArtifacts-1]
		}
		out.Artifacts = append(out.Artifacts, out.ResultRef)
	}
	// Shrink what is shown until the envelope fits; the stored body has it all.
	for envelopeSize(out) > capBytes && out.Preview != "" {
		out.Preview = truncateText(out.Preview, len(out.Preview)/2)
		if len(out.Preview) < 64 {
			out.Preview = ""
		}
	}
	for envelopeSize(out) > capBytes && out.Notes != "" {
		out.Notes = truncateText(out.Notes, len(out.Notes)/2)
		if len(out.Notes) < 32 {
			out.Notes = ""
		}
	}
	for envelopeSize(out) > capBytes && len(out.Artifacts) > 1 {
		// Keep the last entry: with a stored body it is the pointer.
		last := out.Artifacts[len(out.Artifacts)-1]
		out.Artifacts = append(slices.Clone(out.Artifacts[:(len(out.Artifacts)-1)/2]), last)
	}
	return out
}

func envelopeSize(out subtaskResult) int {
	raw, err := json.Marshal(out)
	if err != nil {
		return 0
	}
	return len(raw)
}

// lastOutput is the tail of a child's text, bounded to limit bytes on a
// rune boundary.
func lastOutput(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	start := len(text) - limit
	for start < len(text) && !utf8.RuneStart(text[start]) {
		start++
	}
	return "…" + text[start:]
}

// truncateText keeps the head of text within limit bytes on a rune boundary.
func truncateText(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	end := limit
	for end > 0 && !utf8.RuneStart(text[end]) {
		end--
	}
	return text[:end] + "…"
}

func joinReason(reason, more string) string {
	if reason == "" {
		return more
	}
	return reason + "; " + more
}

// outcomeFor maps a composed subtask result to a background job's outcome.
func outcomeFor(out subtaskResult, runErr error) (background.Outcome, error) {
	raw, err := json.Marshal(out)
	// The output is the whole answer; an error is in its status and reason.
	outcome := background.Outcome{Output: string(raw), Structured: true}
	switch out.Status {
	case subagent.StatusLimit:
		outcome.Limit = out.Reason
	case subagent.StatusPartial:
		outcome.Reason = out.Reason
	case subagent.StatusDenied:
		outcome.Reason = subagent.DeniedPrefix + out.Reason
	case subagent.StatusCancelled:
		outcome.Err = runErr
		if outcome.Err == nil || !errors.Is(outcome.Err, context.Canceled) {
			outcome.Err = context.Canceled
		}
	case subagent.StatusError:
		outcome.Err = runErr
		if outcome.Err == nil {
			outcome.Err = errors.New(out.Reason)
		}
	case subagent.StatusOK:
	}
	return outcome, err
}
