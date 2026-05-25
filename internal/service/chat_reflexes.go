package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hollis-labs/nanite/internal/agent/reflexes"
	ctxpkg "github.com/hollis-labs/nanite/internal/context"
	"github.com/hollis-labs/nanite/internal/store"
)

func (s *chatServiceImpl) evaluateAndInjectReflexes(ctx context.Context, session *store.Session, agent *store.AgentProfile, slotResult *SlotAssemblyResult) []reflexes.AppliedAction {
	if s.reflexEngine == nil || session == nil || agent == nil || slotResult == nil || slotResult.Window == nil {
		return nil
	}
	class := agent.Class
	if class == "" {
		class = "advisor"
	}
	applied, err := s.reflexEngine.Evaluate(ctx, session.ID, agent.ID, class)
	if err != nil {
		if s.store != nil {
			s.store.LogEvent(session.ID, "reflex_eval_error", "reflex", err.Error(), "{}")
		}
		return nil
	}
	if len(applied.Actions) == 0 {
		return nil
	}
	for _, action := range applied.Actions {
		if s.store != nil {
			meta, _ := json.Marshal(action)
			s.store.LogEvent(session.ID, "reflex_action", "reflex", action.ReflexName, string(meta))
		}
	}
	injection := formatReflexReminder(applied.Actions)
	if injection == "" {
		return applied.Actions
	}
	appendUserContext(slotResult, injection)
	return applied.Actions
}

func formatReflexReminder(actions []reflexes.AppliedAction) string {
	var lines []string
	for _, action := range actions {
		if action.ActionKind != store.ReflexActionInjectReminder {
			continue
		}
		body, _ := action.Spec["body"].(string)
		if strings.TrimSpace(body) == "" {
			continue
		}
		lines = append(lines, fmt.Sprintf("Reflex %s: %s", action.ReflexName, strings.TrimSpace(body)))
	}
	if len(lines) == 0 {
		return ""
	}
	return "<system-reminder>\n" + strings.Join(lines, "\n") + "\n</system-reminder>"
}

func appendUserContext(slotResult *SlotAssemblyResult, content string) {
	if slotResult == nil || slotResult.Window == nil || strings.TrimSpace(content) == "" {
		return
	}
	existing := ""
	if slot := slotResult.Window.Slot(ctxpkg.SlotUserContext); slot != nil {
		existing = slot.Content
	}
	if existing != "" {
		slotResult.Window.SetContent(ctxpkg.SlotUserContext, existing+"\n\n"+content)
	} else {
		slotResult.Window.SetContent(ctxpkg.SlotUserContext, content)
	}
	slotResult.Blocks = slotResult.Window.Assemble()
	slotResult.SystemPrompt = rebuildLegacySystemPrompt(slotResult.Window)
}
