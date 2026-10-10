package service

import (
	"unicode/utf8"

	"github.com/hollis-labs/nanite/internal/chat"
	llmtypes "github.com/hollis-labs/substrate/llm-core/llmtypes"
)

// This private value records schema assembly ownership in memory. It marshals
// as an ordinary optional string property; JSON from a tool or caller cannot
// manufacture its Go type. It confers no execution or approval authority.
type toolDisplayProperty struct {
	Type        string `json:"type"`
	Description string `json:"description"`
	MaxLength   int    `json:"maxLength"`
}

type toolDisplayLabels struct{ action, summary string }

func injectUXMetadataProperties(schema map[string]any) {
	if schema == nil || schema["type"] != "object" {
		return
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok || props == nil {
		return // Free-form inputs retain their original argument namespace.
	}
	for _, key := range []string{"$ref", "$dynamicRef", "allOf", "anyOf", "oneOf", "not", "if", "then", "else", "patternProperties", "propertyNames", "dependentSchemas", "dependentRequired", "dependencies", "unevaluatedProperties", "minProperties", "maxProperties", "enum", "const"} {
		if _, exists := schema[key]; exists {
			return // Ownership cannot be established through these constraints.
		}
	}
	if schema["additionalProperties"] != false {
		return // Explicit open/dictionary schemas may own either reserved name.
	}
	var required []string
	switch value := schema["required"].(type) {
	case []string:
		required = value
	case []any:
		for _, item := range value {
			name, ok := item.(string)
			if !ok {
				return
			}
			required = append(required, name)
		}
	}
	for _, name := range required {
		if name == "toolAction" || name == "toolSummary" {
			if _, declared := props[name]; !declared {
				return // Do not satisfy an existing native constraint with a label.
			}
		}
	}
	if _, exists := props["toolAction"]; !exists {
		props["toolAction"] = toolDisplayProperty{Type: "string", Description: "Optional brief display label describing the action, such as 'Reading a file'. Display only; never tool arguments or approval authority.", MaxLength: 512}
	}
	if _, exists := props["toolSummary"]; !exists {
		props["toolSummary"] = toolDisplayProperty{Type: "string", Description: "Optional brief display summary, such as 'File contents'. Display only; never tool arguments or approval authority.", MaxLength: 512}
	}
}

// splitToolDisplayInput uses the exact offered, cloned schema. Names alone or
// a lookalike JSON schema do not establish harness ownership. The provider's
// original input map and nested argument values remain untouched.
func splitToolDisplayInput(tools []llmtypes.ToolDefinition, tu llmtypes.ToolUseBlock) (map[string]any, toolDisplayLabels) {
	var labels toolDisplayLabels
	var props map[string]any
	for _, tool := range tools {
		if tool.Name == tu.Name {
			props, _ = tool.InputSchema["properties"].(map[string]any)
			break
		}
	}
	input := tu.Input
	cloned := false
	for _, field := range []string{"toolAction", "toolSummary"} {
		if _, owned := props[field].(toolDisplayProperty); !owned {
			continue
		}
		value, exists := tu.Input[field]
		if !exists {
			continue
		}
		if !cloned {
			cloned = true
			input = make(map[string]any, len(tu.Input))
			for key, item := range tu.Input {
				input[key] = cloneSchemaValue(item)
			}
		}
		delete(input, field)
		text, _ := value.(string)
		if !utf8.ValidString(text) {
			text = ""
		}
		if len(text) > 512 {
			text = text[:512]
			for !utf8.ValidString(text) {
				text = text[:len(text)-1]
			}
		}
		if field == "toolAction" {
			labels.action = text
		} else {
			labels.summary = text
		}
	}
	return input, labels
}

func toolCallDisplayEvent(tu llmtypes.ToolUseBlock, labels ...toolDisplayLabels) chat.StreamEvent {
	event := chat.StreamEvent{Type: "tool_call", Tool: tu.Name, ToolID: tu.ID, Detail: toolCallDetail(tu.Name, tu.Input)}
	if len(labels) > 0 {
		event.ToolAction, event.ToolSummary = labels[0].action, labels[0].summary
	}
	return event
}
