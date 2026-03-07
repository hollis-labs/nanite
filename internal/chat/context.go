package chat

import (
	"fmt"
	"log"
	"strings"

	"github.com/hollis-labs/mentat-chat/internal/store"
)

// assembleSystemPrompt builds the full system prompt from agent profile, mode, and workspace context.
func assembleSystemPrompt(agent *store.AgentProfile, mode *store.AgentMode, workspace *store.Workspace) string {
	var b strings.Builder

	b.WriteString(agent.SystemPrompt)

	if mode != nil && mode.PromptAddendum != "" {
		b.WriteString("\n\n")
		b.WriteString(mode.PromptAddendum)
	}

	if workspace != nil {
		b.WriteString(fmt.Sprintf("\n\nWorkspace: %s", workspace.Name))
		if workspace.Description != "" {
			b.WriteString(" - ")
			b.WriteString(workspace.Description)
		}
	}

	prompt := b.String()
	log.Printf("chat: assembled system prompt (%d chars)", len(prompt))
	return prompt
}
