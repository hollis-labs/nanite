package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/hollis-labs/nanite/internal/selftools"
	"github.com/hollis-labs/nanite/internal/slots"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/subagent"
)

// seedBlockChars bounds each block of a parent message copied into a child's
// first message. The child reads the rest with parent_history_get.
const seedBlockChars = 2000

// forkedContext is the part of a child's first message that comes from its
// parent conversation: the parent's latest messages (or all of them) and the
// parent state the call asked to include. It is empty when there is none.
func (x *execution) forkedContext(ctx context.Context, spec subagent.ContextSpec, variant subagent.Variant) (string, error) {
	e := x.engine
	var b strings.Builder
	history, err := e.cfg.Store.History(ctx, x.info.ID)
	if err != nil {
		return "", err
	}
	window := settled(history)
	if !spec.Full {
		window = lastMessages(window, spec.Count(variant))
	}
	if len(window) > 0 {
		what := fmt.Sprintf("the last %d messages", len(window))
		if spec.Full {
			what = "the whole conversation"
		}
		fmt.Fprintf(&b, "Context from the parent conversation (%s; long blocks are shortened). "+
			"Read older or full messages with parent_history_search and parent_history_get, using the #numbers.\n<parent_messages>\n%s</parent_messages>",
			what, renderMessages(window, history, seedBlockChars))
	}
	for _, item := range spec.Include {
		text, err := x.includeItem(ctx, item)
		if err != nil {
			return "", err
		}
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		fmt.Fprintf(&b, "<parent_%s>\n%s\n</parent_%s>", includeTag(item), text, includeTag(item))
	}
	return e.scrub.Text(b.String()), nil
}

// includeItem renders one context.include item from the parent's state.
func (x *execution) includeItem(ctx context.Context, item string) (string, error) {
	db := x.engine.cfg.Store
	key, _ := subagent.IncludeKey(item)
	switch item {
	case "todos":
		todos, err := db.Todos(ctx, x.info.ID)
		if err != nil || len(todos) == 0 {
			return orNone(""), err
		}
		var b strings.Builder
		for _, todo := range todos {
			fmt.Fprintf(&b, "- [%s] %s\n", todo.Status, todo.Text)
		}
		return strings.TrimRight(b.String(), "\n"), nil
	case "handoff":
		var parts []string
		for _, entry := range x.engine.cfg.Slots.Snapshot().Entries(slots.Handoff) {
			if text := strings.TrimSpace(entry.Content); text != "" {
				parts = append(parts, text)
			}
		}
		return orNone(strings.Join(parts, "\n\n")), nil
	case "plan":
		// The plan is the parent's scratchpad entry "plan".
		key = "plan"
	}
	text, err := db.Scratch(ctx, x.info.ID, key)
	return orNone(text), err
}

func includeTag(item string) string {
	if key, ok := strings.CutPrefix(item, "scratchpad:"); ok {
		return "scratchpad key=" + fmt.Sprintf("%q", key)
	}
	return item
}

func orNone(text string) string {
	if strings.TrimSpace(text) == "" {
		return "(none)"
	}
	return text
}

// settled drops a trailing part of history that still has tool calls without
// results: the parent's in-flight assistant message (the one calling subtask)
// and anything after it.
func settled(history []store.Message) []store.Message {
	answered := make(map[string]bool)
	for _, m := range history {
		for _, block := range m.Blocks {
			if block.Type == "tool_result" {
				answered[block.ToolUseID] = true
			}
		}
	}
	for i, m := range history {
		for _, block := range m.Blocks {
			if block.Type == "tool_use" && !answered[block.ID] {
				return history[:i]
			}
		}
	}
	return history
}

// lastMessages is the last n messages of a settled history, never starting
// on a tool result whose call fell outside the window: such leading results
// are dropped rather than shown without their call.
func lastMessages(history []store.Message, n int) []store.Message {
	if n <= 0 {
		return nil
	}
	if n < len(history) {
		history = history[len(history)-n:]
	}
	for len(history) > 0 && history[0].Role == "tool" {
		history = history[1:]
	}
	return history
}

// renderMessages writes messages as numbered lines, with each block shortened
// to limit characters. all is the full history, for naming tool results.
func renderMessages(messages, all []store.Message, limit int) string {
	names := make(map[string]string)
	for _, m := range all {
		for _, block := range m.Blocks {
			if block.Type == "tool_use" {
				names[block.ID] = block.Name
			}
		}
	}
	var b strings.Builder
	for _, m := range messages {
		fmt.Fprintf(&b, "[#%d %s]", m.Seq, m.Role)
		for _, block := range m.Blocks {
			switch block.Type {
			case "text":
				b.WriteString(" " + shorten(block.Text, limit, m.Seq))
			case "tool_use":
				input := "{}"
				if block.Input != nil {
					raw, _ := json.Marshal(*block.Input)
					input = string(raw)
				}
				fmt.Fprintf(&b, " (called %s %s)", block.Name, shorten(input, limit/4, m.Seq))
			case "tool_result":
				status := "result"
				if block.IsError {
					status = "error"
				}
				fmt.Fprintf(&b, " (%s of %s: %s)", status, orUnknown(names[block.ToolUseID]), shorten(block.Content, limit, m.Seq))
			}
		}
		b.WriteString("\n")
	}
	return b.String()
}

func orUnknown(name string) string {
	if name == "" {
		return "a tool"
	}
	return name
}

// shorten cuts text to limit characters, saying where the rest is.
func shorten(text string, limit, seq int) string {
	text = strings.TrimSpace(text)
	runes := []rune(text)
	if limit <= 0 || len(runes) <= limit {
		return text
	}
	return string(runes[:limit]) + fmt.Sprintf(" … [shortened; parent_history_get from=%d to=%d has it all]", seq, seq)
}

// parentHistoryBlockChars bounds each block parent_history_get returns; the
// tool-result cache pages anything longer than the call's own budget.
const parentHistoryBlockChars = 8000

// maxParentHistoryRange is the most messages one parent_history_get returns.
const maxParentHistoryRange = 20

// parentHistory serves a child's parent_history_search and parent_history_get
// over its parent conversation.
func (x *execution) parentHistory() selftools.ParentHistory {
	db, parent := x.engine.cfg.Store, x.info.ParentID
	return selftools.ParentHistory{
		Search: func(ctx context.Context, query string, k int) (string, error) {
			if k <= 0 {
				k = 5
			}
			k = min(k, 20)
			hits, err := db.SearchMessages(ctx, parent, query, k)
			if err != nil {
				return "", err
			}
			if len(hits) == 0 {
				return "No parent messages match.", nil
			}
			var b strings.Builder
			for _, hit := range hits {
				fmt.Fprintf(&b, "[#%d %s %s] %s\n", hit.Seq, hit.Role, hit.CreatedAt.Format(time.RFC3339), hit.Snippet)
			}
			return x.engine.scrub.Text(b.String()), nil
		},
		Get: func(ctx context.Context, from, to int) (string, error) {
			if from < 1 || to < from {
				return "", fmt.Errorf("parent_history_get needs 1 <= from <= to")
			}
			note := ""
			if to-from+1 > maxParentHistoryRange {
				to = from + maxParentHistoryRange - 1
				note = fmt.Sprintf("(only %d messages at a time; continue from %d)\n", maxParentHistoryRange, to+1)
			}
			messages, err := db.MessagesBetween(ctx, parent, from, to)
			if err != nil {
				return "", err
			}
			if len(messages) == 0 {
				return "No parent messages in that range.", nil
			}
			// Name tool results whose call came just before the range too.
			names, err := db.MessagesBetween(ctx, parent, max(1, from-maxParentHistoryRange), to)
			if err != nil {
				return "", err
			}
			return x.engine.scrub.Text(renderMessages(messages, names, parentHistoryBlockChars) + note), nil
		},
	}
}
