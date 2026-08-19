// Package pack is the Recovery Pack — one of the four recovery
// mechanisms grouped under internal/recovery/*. It replays trailing
// context into a freshly cold-booted session so it isn't blind on its
// next turn, after the host service restarts and internal/service's
// chatServiceImpl.activeSessions is emptied.
//
// This package holds only the pure rendering/decision logic
// (CW-20260525-0001 Slice 1/2). The thin host-struct glue —
// shouldRecoverColdBoot, buildSessionRecoveryPrefix, composeBootPayload,
// all methods on *chatServiceImpl — stays in
// internal/service/recovery_pack_glue.go, which does the store lookups
// and boot-dir file write, then calls into this package. See
// internal/recovery/broker for the in-process crash-recovery mechanism
// and internal/recovery/orphansweep for stale-runtime-row reconciliation
// — those answer different "what went wrong" questions and are not
// consolidated with this one.
package pack

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hollis-labs/nanite/internal/store"
)

// recoveryMessageMaxChars bounds each replayed message so a single long
// turn can't blow out the pack; the full transcript stays available via
// chat history / the on-disk pack pointer.
const recoveryMessageMaxChars = 1500

// ShouldBuildRecoveryPack reports whether a cold-booted CLI session
// warrants a recovery pack: it cold-booted (no live runtime — the host
// restarted or the runtime was evicted) AND it has prior persisted
// turns. A brand-new session's first turn has no prior messages and is
// intentionally skipped.
func ShouldBuildRecoveryPack(coldBooted bool, priorMessageCount int) bool {
	return coldBooted && priorMessageCount > 0
}

// MessagePlainText unwraps a persisted message's content. Assistant
// turns are stored as a StructuredMessage JSON ({"v":1,"text":...});
// user turns may be plain or wrapped. Returns the human-readable text.
func MessagePlainText(content string) string {
	trimmed := strings.TrimSpace(content)
	if strings.HasPrefix(trimmed, "{") {
		var sm struct {
			V    int    `json:"v"`
			Text string `json:"text"`
		}
		if err := json.Unmarshal([]byte(trimmed), &sm); err == nil && sm.V > 0 {
			return strings.TrimSpace(sm.Text)
		}
	}
	return trimmed
}

// ExcludeCurrentTurn drops the trailing message when it is the current
// user turn (same text as userContent) so the recovery pack replays only
// PRIOR history. No-op when the current turn isn't persisted yet.
func ExcludeCurrentTurn(msgs []store.Message, userContent string) []store.Message {
	cur := strings.TrimSpace(userContent)
	if cur == "" || len(msgs) == 0 {
		return msgs
	}
	last := msgs[len(msgs)-1]
	if last.Role == "user" && MessagePlainText(last.Content) == cur {
		return msgs[:len(msgs)-1]
	}
	return msgs
}

// RecoveryPackInput is the bounded, explicit input to BuildRecoveryPack.
type RecoveryPackInput struct {
	Session  *store.Session
	Agent    *store.AgentProfile
	Reason   string
	History  []store.Message // chronological (ASC), EXCLUDING the current user turn
	PackPath string          // boot-dir path the full pack is written to (the pointer)
}

// BuildRecoveryPack renders the recovery context planted ahead of the
// first post-restart user turn. It opens with an explicit "you are
// resuming" instruction so the agent treats it as recovered context (not
// a new request), summarizes session identity, replays the recent turns
// (unwrapped + bounded), and points at the on-disk pack + recovery tools
// to fill gaps.
func BuildRecoveryPack(in RecoveryPackInput) string {
	var b strings.Builder
	b.WriteString("<recovered-session-context>\n")
	b.WriteString("You are resuming an existing Nanite chat session after the host service restarted. ")
	b.WriteString("Treat everything in this block as RECOVERED CONTEXT, not a new user request — do not re-answer it. ")
	b.WriteString("Use it to continue from the user's latest message, which follows this block. ")
	if strings.TrimSpace(in.PackPath) != "" {
		b.WriteString(fmt.Sprintf("The full pack is also at `%s` — reread it if you need more. ", in.PackPath))
	}
	b.WriteString("You can use chat search/history, session events, and memory tools to fill any gaps.\n\n")

	b.WriteString("## Session\n")
	if in.Session != nil {
		if name := firstNonEmpty(in.Session.CustomName, in.Session.Title); name != "" {
			b.WriteString(fmt.Sprintf("- Title: %s\n", name))
		}
		if in.Session.Provider != "" || in.Session.Model != "" {
			b.WriteString(fmt.Sprintf("- Provider/model: %s / %s\n", in.Session.Provider, in.Session.Model))
		}
	}
	if in.Agent != nil && in.Agent.Name != "" {
		b.WriteString(fmt.Sprintf("- Agent: %s\n", in.Agent.Name))
	}
	if in.Reason != "" {
		b.WriteString(fmt.Sprintf("- Recovery reason: %s\n", in.Reason))
	}

	if len(in.History) > 0 {
		b.WriteString("\n## Recent turns (oldest → newest)\n")
		for _, m := range in.History {
			text := MessagePlainText(m.Content)
			if text == "" {
				continue
			}
			if len(text) > recoveryMessageMaxChars {
				text = text[:recoveryMessageMaxChars] + " …[truncated]"
			}
			b.WriteString(fmt.Sprintf("**%s:** %s\n\n", m.Role, text))
		}
	}
	b.WriteString("</recovered-session-context>")
	return b.String()
}

// firstNonEmpty returns the first non-empty string among values, or "".
// Deliberately duplicated from internal/service's identically-behaved
// helper (durable_agent_recipes.go) rather than imported — internal/
// service imports this package (for the glue-method call sites in
// recovery_pack_glue.go), so the reverse import would cycle. Trivial
// enough that a second copy is cheaper than another shared-utility
// package.
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
