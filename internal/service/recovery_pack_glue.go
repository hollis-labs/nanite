package service

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/hollis-labs/nanite/internal/recovery/pack"
	"github.com/hollis-labs/nanite/internal/store"
)

// CW-20260525-0001 Slice 1 — CLI session auto-recovery after daemon restart.
//
// When the host service restarts, chatServiceImpl.activeSessions is emptied, so
// the next user turn on a CLI/boot-profile session cold-boots a fresh agent
// process via driveBootSession with only the current user message — the agent
// loses all prior conversational context (dogfooded twice in c287). This module
// is the thin *chatServiceImpl glue for the Recovery Pack mechanism: it does the
// store lookups and boot-dir file write, then calls into internal/recovery/pack
// for the pure rendering/decision logic (building a bounded "Recovery Pack"
// planted ahead of the user's message on that first post-restart turn so the
// agent resumes from recovered context instead of answering blind).
// *chatServiceImpl stays here rather than moving into internal/recovery/pack —
// it's a large host struct that pack deliberately doesn't depend on.

const (
	// recoveryHistoryMessages is the number of trailing messages (~10 turns of
	// user/assistant) replayed inline in the recovery pack.
	recoveryHistoryMessages = 20

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
	history := pack.ExcludeCurrentTurn(msgs, userContent)
	windowCapped := len(history) > recoveryHistoryMessages
	if windowCapped {
		history = history[len(history)-recoveryHistoryMessages:]
	}
	if !pack.ShouldBuildRecoveryPack(true, len(history)) {
		return ""
	}
	const reason = "host service restart (cold boot with prior history)"
	packPath := ""
	if strings.TrimSpace(bootDir) != "" {
		packPath = filepath.Join(bootDir, recoveryPackFileName)
	}
	built := pack.BuildRecoveryPack(pack.RecoveryPackInput{
		Session:  session,
		Agent:    agent,
		Reason:   reason,
		History:  history,
		PackPath: packPath,
	})
	if packPath != "" {
		if err := os.WriteFile(packPath, []byte(built+"\n"), 0o644); err != nil {
			slog.Warn("recovery: write pack file failed", "session_id", sessionID, "path", packPath, "err", err)
		}
	}
	s.store.LogEvent(sessionID, "recovery_pack_planted", "recovery",
		fmt.Sprintf("planted recovery pack (%d prior turns)", len(history)),
		recoveryPackPlantedMetadata(sessionID, reason, packPath, msgs, history, windowCapped))
	return built
}

// recoveryPackPlantedMeta is the structured event_log.metadata payload for
// event_type="recovery_pack_planted" — a real postmortem of what was
// replayed (message count, any truncation applied), not a bare marker.
// Mirrors the shape convention chat_reflexes.go's "reflex_action" write
// established.
type recoveryPackPlantedMeta struct {
	SourceSessionID       string `json:"source_session_id"`
	Reason                string `json:"reason"`
	MessagesReplayed      int    `json:"messages_replayed"`
	MessagesAvailable     int    `json:"messages_available"`
	HistoryWindowCapped   bool   `json:"history_window_capped"`
	MessagesCharTruncated int    `json:"messages_char_truncated"`
	RecoveryHistoryMax    int    `json:"recovery_history_max"`
	PackPath              string `json:"pack_path,omitempty"`
}

// recoveryPackPlantedMetadata builds the JSON metadata blob for the
// "recovery_pack_planted" event_log row. msgs is the raw pre-exclusion/
// pre-window fetch (used to report how much history existed vs. how much
// was actually replayed); history is the final bounded set BuildRecoveryPack
// rendered. windowCapped reports whether history was longer than
// recoveryHistoryMessages before being sliced down to the trailing window.
func recoveryPackPlantedMetadata(sessionID, reason, packPath string, msgs, history []store.Message, windowCapped bool) string {
	meta := recoveryPackPlantedMeta{
		SourceSessionID:       sessionID,
		Reason:                reason,
		MessagesReplayed:      len(history),
		MessagesAvailable:     len(msgs),
		HistoryWindowCapped:   windowCapped,
		MessagesCharTruncated: pack.CountCharTruncatedMessages(history),
		RecoveryHistoryMax:    recoveryHistoryMessages,
		PackPath:              packPath,
	}
	blob, err := json.Marshal(meta)
	if err != nil {
		return "{}"
	}
	return string(blob)
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
