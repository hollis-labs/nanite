package service

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/nanite/internal/chat"
)

// isScratchpadTool reports whether name is one of the P4 scratchpad tools.
func isScratchpadTool(name string) bool {
	switch name {
	case "scratchpad_write", "scratchpad_read", "scratchpad_clear":
		return true
	}
	return false
}

// handleScratchpadTool executes a scratchpad tool call directly against loopState.
// No MCP transport, no DB — pure in-process. Mirrors handleResultCacheMetaTool.
func handleScratchpadTool(
	tu llmtypes.ToolUseBlock,
	ls *loopState,
	ch chan chat.StreamEvent,
	mu *sync.Mutex,
	start time.Time,
) toolExecResult {
	if mu != nil {
		mu.Lock()
	}
	ch <- chat.StreamEvent{Type: "tool_call", Tool: tu.Name, ToolID: tu.ID, Detail: toolCallDetail(tu.Name, tu.Input)}
	if mu != nil {
		mu.Unlock()
	}

	var resultText string
	var isError bool

	switch tu.Name {
	case "scratchpad_write":
		resultText, isError = callScratchpadWrite(tu.Input, ls)
	case "scratchpad_read":
		resultText, isError = callScratchpadRead(tu.Input, ls)
	case "scratchpad_clear":
		resultText, isError = callScratchpadClear(tu.Input, ls)
	default:
		resultText = fmt.Sprintf("unknown scratchpad tool: %s", tu.Name)
		isError = true
	}

	summary := resultText
	if len(summary) > 500 {
		summary = summary[:500] + "... (truncated)"
	}
	ch <- chat.StreamEvent{Type: "tool_result", Tool: tu.Name, ToolID: tu.ID, Summary: summary}

	duration := time.Since(start)
	return toolExecResult{
		resultBlock: llmtypes.ContentBlock{
			Type: "tool_result", ToolUseID: tu.ID, Content: resultText, IsError: isError,
		},
		ref:       chat.ToolCallRef{ID: tu.ID, Name: tu.Name},
		isError:   isError,
		rawOutput: resultText,
		duration:  duration,
	}
}

func callScratchpadWrite(input map[string]any, ls *loopState) (string, bool) {
	key, _ := input["key"].(string)
	if key == "" {
		return "key is required", true
	}
	value, ok := input["value"]
	if !ok {
		return "value is required", true
	}
	if err := ls.scratchpadWrite(key, value); err != nil {
		return err.Error(), true
	}
	out, _ := json.Marshal(map[string]any{"stored": true})
	return string(out), false
}

func callScratchpadRead(input map[string]any, ls *loopState) (string, bool) {
	key, _ := input["key"].(string)
	entries, _ := ls.scratchpadRead(key)
	if entries == nil {
		entries = map[string]any{}
	}
	out, _ := json.Marshal(map[string]any{"entries": entries})
	return string(out), false
}

func callScratchpadClear(input map[string]any, ls *loopState) (string, bool) {
	key, _ := input["key"].(string)
	if key == "" {
		return "key is required", true
	}
	cleared := ls.scratchpadClear(key)
	out, _ := json.Marshal(map[string]any{"cleared": cleared})
	return string(out), false
}
