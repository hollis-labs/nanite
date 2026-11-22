package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/hollis-labs/nanite/internal/agent/reflexes"
	"github.com/hollis-labs/nanite/internal/store"
	ctxpkg "github.com/hollis-labs/substrate/agent/context"
)

func (s *chatServiceImpl) evaluateAndInjectReflexes(ctx context.Context, session *store.Session, agent *store.AgentProfile, slotResult *SlotAssemblyResult) ([]reflexes.AppliedAction, error) {
	if session == nil || agent == nil || slotResult == nil || slotResult.Window == nil {
		return nil, nil
	}
	if agent.DefinitionPolicy == nil {
		return nil, store.ErrImmutableAgentProfile
	}
	return s.evaluateDefinitionReflexes(ctx, session, agent, slotResult)
}

func formatReflexReminder(actions []reflexes.AppliedAction) string {
	var lines []string
	for _, action := range actions {
		switch action.ActionKind {
		case store.ReflexActionInjectReminder:
			body, _ := action.Spec["body"].(string)
			if strings.TrimSpace(body) == "" {
				continue
			}
			lines = append(lines, fmt.Sprintf("Reflex %s: %s", action.ReflexName, strings.TrimSpace(body)))
		case store.ReflexActionForceToolChoice:
			// force_tool_choice is wired SOFT: it renders a tool-directed
			// instruction through the same reminder channel rather than the
			// provider's tool_choice param (go-llm-types.ChatRequest has no
			// tool_choice field yet — hard API enforcement is a follow-up).
			// Default is a preference nudge; a reflex must explicitly opt in
			// (action_spec enforce=true or mode="hard") for the imperative
			// form. inject_reminder remains the preferred lever; this exists
			// for deterministic-step cases (e.g. a process agent like a task
			// writer) where exactly one tool is correct next.
			tool, _ := action.Spec["tool_name"].(string)
			tool = strings.TrimSpace(tool)
			if tool == "" {
				continue
			}
			if reflexForceIsHard(action.Spec) {
				lines = append(lines, fmt.Sprintf("Reflex %s: call the `%s` tool now, before any other response.", action.ReflexName, tool))
			} else {
				lines = append(lines, fmt.Sprintf("Reflex %s: prefer calling the `%s` tool next.", action.ReflexName, tool))
			}
		}
	}
	if len(lines) == 0 {
		return ""
	}
	return "<system-reminder>\n" + strings.Join(lines, "\n") + "\n</system-reminder>"
}

// reflexForceIsHard reports whether a force_tool_choice reflex opted in to the
// imperative (hard) directive. Default (no flag) is the soft preference nudge,
// keeping the capability opt-in.
func reflexForceIsHard(spec map[string]interface{}) bool {
	if v, ok := spec["enforce"].(bool); ok {
		return v
	}
	if s, ok := spec["mode"].(string); ok {
		return strings.EqualFold(strings.TrimSpace(s), "hard")
	}
	return false
}

func appendUserContext(slotResult *SlotAssemblyResult, content string) bool {
	if slotResult == nil || slotResult.Window == nil || strings.TrimSpace(content) == "" {
		return false
	}
	existing := ""
	if slot := slotResult.Window.Slot(ctxpkg.SlotUserContext); slot != nil {
		existing = slot.Content
	}
	expected := content
	if existing != "" {
		expected = existing + "\n\n" + content
	}
	if existing != "" {
		slotResult.Window.SetContent(ctxpkg.SlotUserContext, existing+"\n\n"+content)
	} else {
		slotResult.Window.SetContent(ctxpkg.SlotUserContext, content)
	}
	slotResult.Blocks = slotResult.Window.Assemble()
	slotResult.SystemPrompt = rebuildLegacySystemPrompt(slotResult.Window)
	return slotResult.Window.Slot(ctxpkg.SlotUserContext).Content == expected
}
