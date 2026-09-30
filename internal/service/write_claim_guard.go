package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"

	hooks "github.com/hollis-labs/go-hooks"

	"github.com/hollis-labs/nanite/internal/chat"
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

// nonWriteTools never count as a write, whatever their annotations or names
// say: they discover, describe, page cached results, keep scratch state, or
// compute. A tool that only computes must not ground an id: `think` and
// `math_eval` echo what the model gave them, including an id it invented.
var nonWriteTools = map[string]bool{
	"request_tools": true, "tool_describe": true, "tool_list": true, "tool_validate": true,
	"whoami": true, "fetch_tool_result": true, "search_tool_result": true,
	"think": true, "math_eval": true, "calc": true, "calculator": true, "datetime": true,
	"current_time": true, "echo": true, "uuid": true, "random": true, "sleep": true,
	"base64_encode": true, "base64_decode": true, "json_format": true, "regex_test": true,
}

// writeVerbs are the name tokens that mark a tool as one that changes state
// somewhere. A tool must be recognizably a writer to ground a claim: a name
// with none of these, and no declared destructive annotation, is treated as
// not writing.
var writeVerbs = map[string]bool{
	"write": true, "create": true, "update": true, "delete": true, "remove": true, "add": true,
	"set": true, "put": true, "post": true, "save": true, "store": true, "capture": true,
	"insert": true, "upsert": true, "append": true, "edit": true, "patch": true, "transition": true,
	"promote": true, "ingest": true, "register": true, "deregister": true, "send": true,
	"publish": true, "commit": true, "push": true, "deploy": true, "merge": true, "close": true,
	"archive": true, "unarchive": true, "move": true, "rename": true, "apply": true, "assign": true,
	"attach": true, "detach": true, "invite": true, "kick": true, "revoke": true, "lease": true,
	"renew": true, "purge": true, "drain": true, "redrive": true, "replay": true, "sync": true,
	"touch": true, "deprecate": true, "toggle": true, "run": true, "exec": true, "execute": true,
	"dispatch": true, "launch": true, "start": true, "stop": true, "resume": true, "cancel": true,
	"respond": true, "submit": true, "enqueue": true, "acknowledge": true, "ack": true,
	"mark": true, "reorder": true, "bulk": true, "tag": true,
}

// nameTokens splits a tool name on separators and camel-case boundaries into
// lowercase tokens: "torque_task_transition" -> [torque task transition],
// "knowledgeWrite" -> [knowledge write], "HTTPPost" -> [http post].
func nameTokens(name string) []string {
	var out []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			out = append(out, strings.ToLower(string(cur)))
			cur = cur[:0]
		}
	}
	runes := []rune(name)
	for i, r := range runes {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			flush()
			continue
		}
		if unicode.IsUpper(r) && len(cur) > 0 {
			prev := runes[i-1]
			nextLower := i+1 < len(runes) && unicode.IsLower(runes[i+1])
			if unicode.IsLower(prev) || unicode.IsDigit(prev) || (unicode.IsUpper(prev) && nextLower) {
				flush()
			}
		}
		cur = append(cur, r)
	}
	flush()
	return out
}

// isWriteCapable reports whether a successful call to name counts as a write.
// Order of evidence: never-write tools, then the tool's own read-only
// declaration, then a destructive declaration, then a write verb in its name.
// A tool with none of these is treated as not writing, so a computing or
// unclassified tool cannot silence the guard or ground an id.
func (s *chatServiceImpl) isWriteCapable(ctx context.Context, name string) bool {
	if nonWriteTools[name] || isScratchpadTool(name) || isResultCacheTool(name) {
		return false
	}
	var meta ToolMetaInfo
	if s.tools != nil {
		meta, _ = s.tools.GetToolMeta(ctx, name)
	}
	if meta.IsReadOnly {
		return false
	}
	if meta.IsDestructive {
		return true
	}
	for _, tok := range nameTokens(name) {
		if writeVerbs[tok] {
			return true
		}
	}
	return false
}

// writeClaimFacts is what the turn actually did.
type writeClaimFacts struct {
	// WroteThisTurn is true when a write-capable tool call succeeded. It labels
	// the decision; it does not allow a claim whose ids are not grounded.
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
	// Every cited id must be grounded in a successful write-capable result.
	// Having written something this turn does not license citing other ids.
	if len(finding.Ungrounded) == 0 {
		d.Reason = wcGroundedInWrite
		if facts.WroteThisTurn {
			d.Reason = wcWriteSucceeded
		}
		return hooks.Output{Decision: hooks.DecisionAllow}, d
	}
	d.Fired, d.Reason = true, wcUnbackedClaim
	reason := fmt.Sprintf("the reply reports a completed write citing %s, but no successful write result this session returned it", strings.Join(finding.Ungrounded, ", "))
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
		", but no write tool result returned that id, so the write did not happen and that id is not one you were given. " +
		"Do not claim it. Either call the write tool now (use request_tools first if it is not loaded) and report the id it " +
		"actually returns, or tell the user plainly that nothing was written."
}

// writeClaimFooter is appended to a reply that still carries an unbacked claim
// after its one retry.
func writeClaimFooter(ids []string) string {
	return "\n\n⚠ Unverified claim: this reply cites " + strings.Join(ids, ", ") +
		" as written, but no successful write returned it. Treat that write as not done."
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

// guardMode is the write-claim guard's mode for the run.
func guardMode(ls *loopState) harnessprofile.GuardMode {
	if ls != nil && ls.harness != nil {
		return ls.harness.Values.WriteClaimGuard
	}
	return harnessprofile.DefaultWriteClaimGuard
}

// narrationClaimFooter checks the prose the model wrote in iterations that also
// called tools (the narration) once the turn is over, against the turn's final
// facts, and returns the footer to append to the reply, if any.
//
// That prose cannot be sent back the way a final reply can: it has already been
// shown, and the loop it belonged to has moved on and run its tools. So a claim
// found there is never blocked or retried. It is logged with action
// "narration_flagged"; under deny the reply gets the visible footer, otherwise a
// status event is emitted. An id the turn's own write later returned grounds it,
// so "saving it now" narration followed by the real write is not flagged.
func (s *chatServiceImpl) narrationClaimFooter(ctx context.Context, sessionID, model string, ls *loopState, narration string, ch chan chat.StreamEvent) string {
	mode := guardMode(ls)
	if narration == "" || mode == harnessprofile.GuardOff {
		return ""
	}
	stop := hooks.StopInput{LastAssistantMessage: narration}
	facts := s.writeClaimFactsFor(ctx, sessionID, ls, false)
	out, d := writeClaimHook(mode, stop, facts)
	if d.Reason == wcUnbackedClaim {
		facts = s.writeClaimFactsFor(ctx, sessionID, ls, true)
		out, d = writeClaimHook(mode, stop, facts)
	}
	if !d.Fired {
		return ""
	}
	s.logWriteClaimDecision(ctx, sessionID, model, d, facts, "narration_flagged")
	if mode == harnessprofile.GuardDeny {
		if ls.wcFooter != "" {
			return "" // the reply already carries a correction
		}
		return writeClaimFooter(d.Finding.Ungrounded)
	}
	ch <- chat.StreamEvent{Type: "status", Content: out.SystemMessage}
	return ""
}
