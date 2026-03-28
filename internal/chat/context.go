package chat

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/hollis-labs/conduit/internal/store"
)

// assembleSystemPrompt builds the full system prompt from agent profile, mode, and workspace context.
// This is the legacy path used when no prompt templates are assigned.
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

// assembleSystemPromptFromTemplates uses ComposePromptForAgent from the prompt template system.
// Falls back to the legacy assembleSystemPrompt if no templates are assigned.
func assembleSystemPromptFromTemplates(s *store.Store, agent *store.AgentProfile, mode *store.AgentMode, workspace *store.Workspace, skillList string) string {
	// Build variables map for template resolution.
	vars := map[string]string{
		"agent_name":        agent.Name,
		"agent_description": agent.Description,
	}

	if mode != nil {
		vars["mode_addendum"] = mode.PromptAddendum
	}

	if workspace != nil {
		vars["workspace_name"] = workspace.Name
		vars["workspace_description"] = workspace.Description
	}

	if skillList != "" {
		vars["skill_list"] = skillList
	}

	// Schema v2 template variables.
	if agent.Tools != "" && agent.Tools != "[]" {
		vars["tools_allowlist"] = agent.Tools
	}
	if agent.Tags != "" && agent.Tags != "[]" {
		vars["agent_tags"] = agent.Tags
	}

	composed, err := s.ComposePromptForAgent(agent.ID, vars)
	if err != nil {
		log.Printf("chat: ComposePromptForAgent failed: %v — falling back to legacy", err)
		return assembleSystemPrompt(agent, mode, workspace)
	}

	if composed == "" {
		// No templates assigned — use legacy path.
		return assembleSystemPrompt(agent, mode, workspace)
	}

	log.Printf("chat: assembled system prompt from templates (%d chars)", len(composed))
	return composed
}

// buildSkillList creates a human-readable list of skills for the tool-awareness template.
func buildSkillList(s *store.Store, agentID string) string {
	skills, err := s.ListAgentSkills(agentID)
	if err != nil {
		log.Printf("chat: failed to load agent skills: %v", err)
		return ""
	}

	if len(skills) == 0 {
		return ""
	}

	var sb strings.Builder
	for _, sk := range skills {
		fmt.Fprintf(&sb, "- %s: %s", sk.Name, sk.Description)
		// Parse tool_bindings to show tools.
		var tools []string
		if err := json.Unmarshal([]byte(sk.ToolBindings), &tools); err == nil && len(tools) > 0 {
			fmt.Fprintf(&sb, " [tools: %s]", strings.Join(tools, ", "))
		}
		sb.WriteString("\n")
	}

	return sb.String()
}
