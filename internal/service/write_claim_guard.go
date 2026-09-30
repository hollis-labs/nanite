package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	hooks "github.com/hollis-labs/go-hooks"

	"github.com/hollis-labs/nanite/internal/harnessprofile"
	"github.com/hollis-labs/nanite/internal/writeclaim"
)

// The write-claim guard (D-34) is a go-hooks Stop implementation. When a reply
// is about to be finalized it looks for a completed-write claim citing an id and
// checks it against what the turn actually did. It replaces nothing in the
// reflex engine, whose predicate cannot see the reply being written.
//
// Fields relied on: hooks.StopInput{LastAssistantMessage} and
// hooks.Output{Decision, Reason, SystemMessage, Continue, StopReason}. None is
// among the fields go-hooks flags as unverified upstream. The upstream Stop
// catalog honors continue/stopReason/systemMessage; this host also reads
// Decision, so a deny sets both.

// Reasons recorded for every decision, so the false-positive rate can be tuned
// from the event log.
const (
	wcNoClaim          = "no_claim"
	wcOff              = "guard_off"
	wcWriteSucceeded   = "write_tool_succeeded_this_turn"
	wcGroundedInWrite  = "claim_ids_grounded_in_a_prior_write_result"
	wcUnbackedClaim    = "unbacked_write_claim"
	wcEventType        = "write_claim_guard"
	wcResultIDsEvent   = "write_result_ids"
	wcMaxRetriesPerRun = 1
)

// nonWriteTools never count as a write, whatever their annotations say: they
// discover, describe, page cached results or keep scratch state. Without this a
// turn of request_tools and whoami would look write-capable.
var nonWriteTools = map[string]bool{
	"request_tools": true, "tool_describe": true, "tool_list": true, "tool_validate": true,
	"whoami": true, "fetch_tool_result": true, "search_tool_result": true,
}

// isWriteCapable reports whether a successful call to name counts as a write.
// A tool is write-capable unless it is known not to be: a fixed non-write set,
// scratchpad tools, or a tool the server or the name heuristic marks read-only.
// An unknown tool counts as write-capable, so the guard errs toward silence.
func (s *chatServiceImpl) isWriteCapable(ctx context.Context, name string) bool {
	if nonWriteTools[name] || isScratchpadTool(name) || isResultCacheTool(name) {
		return false
	}
	if s.tools != nil {
		if meta, ok := s.tools.GetToolMeta(ctx, name); ok && meta.IsReadOnly {
			return false
		}
	}
	return true
}

// writeClaimFacts is what the turn actually did.
type writeClaimFacts struct {
	// WroteThisTurn is true when a write-capable tool call succeeded.
	WroteThisTurn bool
	// GroundedIDs are ids that appeared in a successful write-capable tool
	// result, this turn or an earlier turn of the session.
	GroundedIDs map[string]bool
	// ToolsRan lists tool names with "ok" or "error", in order, for the log.
	ToolsRan []string
}

// writeClaimDecision is the guard's verdict and the ground truth behind it.
type writeClaimDecision struct {
	Mode    harnessprofile.GuardMode
	Fired   bool
	Reason  string
	Finding writeclaim.Finding
}

// writeClaimHook is the Stop hook. It is pure: the reply and the turn's facts
// in, a hooks.Output and the recorded decision out.
func writeClaimHook(mode harnessprofile.GuardMode, in hooks.StopInput, facts writeClaimFacts) (hooks.Output, writeClaimDecision) {
	d := writeClaimDecision{Mode: mode, Reason: wcNoClaim}
	if mode == harnessprofile.GuardOff || mode == "" {
		d.Reason = wcOff
		return hooks.Output{Decision: hooks.DecisionAllow}, d
	}
	finding, claimed := writeclaim.Detect(in.LastAssistantMessage, facts.GroundedIDs)
	if !claimed {
		return hooks.Output{Decision: hooks.DecisionAllow}, d
	}
	d.Finding = finding
	switch {
	case facts.WroteThisTurn:
		d.Reason = wcWriteSucceeded
		return hooks.Output{Decision: hooks.DecisionAllow}, d
	case len(finding.Ungrounded) == 0:
		d.Reason = wcGroundedInWrite
		return hooks.Output{Decision: hooks.DecisionAllow}, d
	}
	d.Fired, d.Reason = true, wcUnbackedClaim
	reason := fmt.Sprintf("the reply reports a completed write citing %s, but no write tool succeeded this turn", strings.Join(finding.Ungrounded, ", "))
	out := hooks.Output{Reason: reason, SystemMessage: "write-claim guard: " + reason}
	switch mode {
	case harnessprofile.GuardDeny:
		stop := false
		out.Decision, out.Continue, out.StopReason = hooks.DecisionDeny, &stop, reason
	case harnessprofile.GuardAsk:
		out.Decision = hooks.DecisionAsk
	default: // warn
		out.Decision = hooks.DecisionAllow
	}
	return out, d
}

// writeClaimNudge is the correction sent back to the model on a deny.
func writeClaimNudge(d writeClaimDecision) string {
	return "System check: your last reply reported a completed write and cited " + strings.Join(d.Finding.Ungrounded, ", ") +
		", but no write tool succeeded in this turn, so the write did not happen and that id is not one you were given. " +
		"Do not claim it. Either call the write tool now (use request_tools first if it is not loaded) and report the id it " +
		"actually returns, or tell the user plainly that nothing was written."
}

// writeClaimFooter is appended to a reply that still carries an unbacked claim
// after its one retry.
func writeClaimFooter(ids []string) string {
	return "\n\n⚠ Unverified claim: this reply cites " + strings.Join(ids, ", ") +
		" as written, but no write tool succeeded in this turn. Treat that write as not done."
}

// noteToolResult records one executed tool's outcome for the guard.
func (s *chatServiceImpl) noteToolResult(ctx context.Context, sessionID string, ls *loopState, name, output string, isError bool) {
	if ls == nil {
		return
	}
	if isError {
		ls.wcToolsRan = append(ls.wcToolsRan, name+":error")
		return
	}
	ls.wcToolsRan = append(ls.wcToolsRan, name+":ok")
	if !s.isWriteCapable(ctx, name) {
		return
	}
	ls.wcWrote = true
	ids := writeclaim.IDs(output)
	if len(ids) == 0 {
		return
	}
	if ls.wcWriteIDs == nil {
		ls.wcWriteIDs = map[string]bool{}
	}
	for _, id := range ids {
		ls.wcWriteIDs[id] = true
	}
	if s.store != nil {
		blob, _ := json.Marshal(map[string]any{"tool": name, "ids": ids})
		s.store.LogEvent(context.WithoutCancel(ctx), sessionID, wcResultIDsEvent, wcEventType, name, string(blob))
	}
}

// sessionWriteIDReader is the optional store capability that returns the ids
// earlier turns' write results produced. Stores without it ground on this
// turn's results only.
type sessionWriteIDReader interface {
	SessionWriteResultIDs(ctx context.Context, sessionID string) (map[string]bool, error)
}

// writeClaimFactsFor gathers the turn's facts from memory. withHistory adds the
// ids earlier turns' writes returned, which costs a query; callers ask for it
// only once a claim has been found and is still unbacked.
func (s *chatServiceImpl) writeClaimFactsFor(ctx context.Context, sessionID string, ls *loopState, withHistory bool) writeClaimFacts {
	grounded := map[string]bool{}
	for id := range ls.wcWriteIDs {
		grounded[id] = true
	}
	if !withHistory {
		return writeClaimFacts{WroteThisTurn: ls.wcWrote, GroundedIDs: grounded, ToolsRan: ls.wcToolsRan}
	}
	if r, ok := s.store.(sessionWriteIDReader); ok {
		if prior, err := r.SessionWriteResultIDs(ctx, sessionID); err == nil {
			for id := range prior {
				grounded[id] = true
			}
		}
	}
	return writeClaimFacts{WroteThisTurn: ls.wcWrote, GroundedIDs: grounded, ToolsRan: ls.wcToolsRan}
}

// logWriteClaimDecision records a decision that found a claim, with the ground
// truth behind it.
func (s *chatServiceImpl) logWriteClaimDecision(ctx context.Context, sessionID, model string, d writeClaimDecision, facts writeClaimFacts, action string) {
	if s.store == nil {
		return
	}
	blob, _ := json.Marshal(map[string]any{
		"mode": d.Mode, "reason": d.Reason, "action": action, "fired": d.Fired, "model": model,
		"phrase": d.Finding.Phrase, "ids": d.Finding.IDs, "ungrounded": d.Finding.Ungrounded,
		"wrote_this_turn": facts.WroteThisTurn, "tools": facts.ToolsRan,
	})
	s.store.LogEvent(context.WithoutCancel(ctx), sessionID, wcEventType, "guard", d.Reason, string(blob))
}
