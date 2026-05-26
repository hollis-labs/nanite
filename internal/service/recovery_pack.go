package service

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/hollis-labs/nanite/internal/store"
)

// CW-20260525-0001 Slice 1 — CLI session auto-recovery after daemon restart.
//
// When the host service restarts, chatServiceImpl.activeSessions is emptied, so
// the next user turn on a CLI/boot-profile session cold-boots a fresh agent
// process via driveBootSession with only the current user message — the agent
// loses all prior conversational context (dogfooded twice in c287). This module
// builds a bounded "Recovery Pack" planted ahead of the user's message on that
// first post-restart turn so the agent resumes from recovered context instead
// of answering blind.

const (
	// recoveryHistoryMessages is the number of trailing messages (~10 turns of
	// user/assistant) replayed inline in the recovery pack.
	recoveryHistoryMessages = 20

	// recoveryMessageMaxChars bounds each replayed message so a single long
	// turn can't blow out the pack; the full transcript stays available via
	// chat history / the on-disk pack pointer.
	recoveryMessageMaxChars = 1500

	// recoveryPackFileName is the boot-dir file the full pack is also written
	// to, so the agent can reread recovered context on demand (the pointer).
	recoveryPackFileName = "recovery.md"
)

// shouldRecoverColdBoot decides whether a cold boot should auto-recover. A cold
// boot recovers UNLESS an intentional reboot armed the one-shot fresh-boot flag
// (consumed here, so it only suppresses the very next boot). Non-cold boots and
// fresh reboots do not recover. CW-20260525-0001 Slice 2.
func (s *chatServiceImpl) shouldRecoverColdBoot(sessionID string, coldBooted bool) bool {
	if !coldBooted {
		return false
	}
	if _, fresh := s.freshBootSessions.LoadAndDelete(sessionID); fresh {
		return false
	}
	return true
}

// shouldBuildRecoveryPack reports whether a cold-booted CLI session warrants a
// recovery pack: it cold-booted (no live runtime — the host restarted or the
// runtime was evicted) AND it has prior persisted turns. A brand-new session's
// first turn has no prior messages and is intentionally skipped.
func shouldBuildRecoveryPack(coldBooted bool, priorMessageCount int) bool {
	return coldBooted && priorMessageCount > 0
}

// messagePlainText unwraps a persisted message's content. Assistant turns are
// stored as a StructuredMessage JSON ({"v":1,"text":...}); user turns may be
// plain or wrapped. Returns the human-readable text.
func messagePlainText(content string) string {
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

// excludeCurrentTurn drops the trailing message when it is the current user
// turn (same text as userContent) so the recovery pack replays only PRIOR
// history. No-op when the current turn isn't persisted yet.
func excludeCurrentTurn(msgs []store.Message, userContent string) []store.Message {
	cur := strings.TrimSpace(userContent)
	if cur == "" || len(msgs) == 0 {
		return msgs
	}
	last := msgs[len(msgs)-1]
	if last.Role == "user" && messagePlainText(last.Content) == cur {
		return msgs[:len(msgs)-1]
	}
	return msgs
}

// recoveryPackInput is the bounded, explicit input to buildRecoveryPack.
type recoveryPackInput struct {
	Session  *store.Session
	Agent    *store.AgentProfile
	Reason   string
	History  []store.Message // chronological (ASC), EXCLUDING the current user turn
	PackPath string          // boot-dir path the full pack is written to (the pointer)
}

// buildRecoveryPack renders the recovery context planted ahead of the first
// post-restart user turn. It opens with an explicit "you are resuming"
// instruction so the agent treats it as recovered context (not a new request),
// summarizes session identity, replays the recent turns (unwrapped + bounded),
// and points at the on-disk pack + recovery tools to fill gaps.
func buildRecoveryPack(in recoveryPackInput) string {
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
			text := messagePlainText(m.Content)
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

// buildSessionRecoveryPrefix loads prior history for a cold-booted session and,
// when recovery is warranted, writes the full pack to the boot dir and returns
// the pack text to plant ahead of the user message. Returns "" when no recovery
// is needed (new session) or on a recoverable error (best-effort).
func (s *chatServiceImpl) buildSessionRecoveryPrefix(sessionID string, session *store.Session, agent *store.AgentProfile, bootDir, userContent string) string {
	if s.store == nil {
		return ""
	}
	msgs, err := s.store.ListMessages(sessionID, recoveryHistoryMessages+2)
	if err != nil {
		slog.Warn("recovery: list messages failed", "session_id", sessionID, "err", err)
		return ""
	}
	history := excludeCurrentTurn(msgs, userContent)
	if len(history) > recoveryHistoryMessages {
		history = history[len(history)-recoveryHistoryMessages:]
	}
	if !shouldBuildRecoveryPack(true, len(history)) {
		return ""
	}
	packPath := ""
	if strings.TrimSpace(bootDir) != "" {
		packPath = filepath.Join(bootDir, recoveryPackFileName)
	}
	pack := buildRecoveryPack(recoveryPackInput{
		Session:  session,
		Agent:    agent,
		Reason:   "host service restart (cold boot with prior history)",
		History:  history,
		PackPath: packPath,
	})
	if packPath != "" {
		if err := os.WriteFile(packPath, []byte(pack+"\n"), 0o644); err != nil {
			slog.Warn("recovery: write pack file failed", "session_id", sessionID, "path", packPath, "err", err)
		}
	}
	s.store.LogEvent(sessionID, "recovery_pack_planted", "recovery",
		fmt.Sprintf("planted recovery pack (%d prior turns)", len(history)), "{}")
	return pack
}

// composeBootPayload builds the per-turn payload for the boot-driven CLI path.
// When shouldRecover is true (a post-restart cold boot, not an intentional
// fresh reboot) and prior history exists, it prepends a recovery pack ahead of
// the normal user payload; otherwise it is the normal payload.
func (s *chatServiceImpl) composeBootPayload(sessionID string, session *store.Session, agent *store.AgentProfile, bootDir string, slotResult *SlotAssemblyResult, userContent string, shouldRecover bool) string {
	base := composeUserPayload(slotResult, userContent)
	if !shouldRecover {
		return base
	}
	prefix := s.buildSessionRecoveryPrefix(sessionID, session, agent, bootDir, userContent)
	if prefix == "" {
		return base
	}
	return prefix + "\n\n" + base
}
