package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/hollis-labs/libs/agent/core/toolresult"
	"github.com/hollis-labs/libs/llm/core/llmtypes"
	"github.com/hollis-labs/nanite/internal/selftools"
	"github.com/hollis-labs/nanite/internal/slots"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/subagent"
)

const (
	// seedBudget bounds the whole forked context in a child's first message,
	// messages and included state together. The newest messages are kept.
	seedBudget = 32000
	// seedBlockChars bounds each text block or tool result copied from a
	// parent message, and seedInputChars each tool call's input.
	seedBlockChars = 2000
	seedInputChars = 500
	// includeChars bounds each context.include item.
	includeChars = 4000
	// parentHistoryBlockChars bounds each block parent_history_get returns;
	// the tool-result cache pages anything longer than the call's budget.
	parentHistoryBlockChars = 8000
	// maxParentHistoryRange is the most messages one parent_history_get returns.
	maxParentHistoryRange = 20
	// snippetChars is how much text a parent_history_search hit shows on each
	// side of its first match.
	snippetChars = 120
)

type llmBlock = llmtypes.ContentBlock

var parentHistoryTools = []string{"parent_history_search", "parent_history_get"}

// childHasHistory reports whether a child running variant will be offered the
// parent history tools: the host offers them and every variant in the
// child's chain allows them.
func (x *execution) childHasHistory(variant subagent.Variant) bool {
	chain := append(slices.Clone(x.chain), variant)
	for _, name := range parentHistoryTools {
		if !x.engine.cfg.Builtins.Offers(name) || !subagent.AllowsTool(false, chain, false, name, true) {
			return false
		}
	}
	return true
}

// forkedContext is the part of a child's first message that comes from its
// parent conversation: the parent's latest messages (or all of them) and the
// parent state the call asked to include, within seedBudget characters and
// redacted. It is empty when there is none.
func (x *execution) forkedContext(ctx context.Context, spec subagent.ContextSpec, variant subagent.Variant) (string, error) {
	scrub := x.engine.scrub.Text
	history := x.childHasHistory(variant)
	var includes []string
	for _, item := range spec.Include {
		text, err := x.includeItem(ctx, item)
		if err != nil {
			return "", err
		}
		tag := includeTag(item)
		includes = append(includes, fmt.Sprintf("<parent_%s>\n%s\n</parent_%s>", tag, cut(scrub(orNone(text)), includeChars, ""), tag))
	}
	budget := seedBudget
	for _, block := range includes {
		budget -= len(block)
	}
	messages, err := x.parentWindow(ctx, spec, variant)
	if err != nil {
		return "", err
	}
	var parts []string
	if len(messages) > 0 {
		lines := renderMessages(messages, nil, seedBlockChars, scrub, history)
		kept := len(lines)
		total := 0
		for kept > 0 && total+len(lines[kept-1]) <= budget {
			total += len(lines[kept-1])
			kept--
		}
		// Keep the newest messages; never start on a result whose call was cut.
		start := kept
		for start < len(messages) && messages[start].Role == "tool" {
			start++
		}
		if start < len(messages) {
			what := fmt.Sprintf("the last %d messages", len(messages)-start)
			if spec.Full && start == 0 {
				what = "the whole conversation"
			}
			if start > 0 {
				what += fmt.Sprintf("; %d older ones left out to fit", start)
			}
			how := "Long blocks are shortened."
			if history {
				how = "Long blocks are shortened; read older or full messages with parent_history_search and parent_history_get, using the #numbers."
			}
			parts = append(parts, fmt.Sprintf("Context from the parent conversation (%s). %s\n<parent_messages>\n%s</parent_messages>",
				what, how, strings.Join(lines[start:], "")))
		}
	}
	parts = append(parts, includes...)
	return strings.Join(parts, "\n\n"), nil
}

// parentWindow loads the parent messages the child starts with: the last N
// settled ones, or every settled one for context.full.
func (x *execution) parentWindow(ctx context.Context, spec subagent.ContextSpec, variant subagent.Variant) ([]store.Message, error) {
	db := x.engine.cfg.Store
	if spec.Full {
		all, err := db.History(ctx, x.info.ID)
		return settled(all), err
	}
	n := spec.Count(variant)
	if n <= 0 {
		return nil, nil
	}
	// One more than N: the parent's own in-flight message is the last one.
	tail, err := db.LastMessages(ctx, x.info.ID, n+1)
	if err != nil {
		return nil, err
	}
	return lastMessages(settled(tail), n), nil
}

// includeItem reads one context.include item from the parent's state.
func (x *execution) includeItem(ctx context.Context, item string) (string, error) {
	db := x.engine.cfg.Store
	key, _ := subagent.IncludeKey(item)
	switch item {
	case "todos":
		todos, err := db.Todos(ctx, x.info.ID)
		var b strings.Builder
		for _, todo := range todos {
			fmt.Fprintf(&b, "- [%s] %s\n", todo.Status, todo.Text)
		}
		return strings.TrimRight(b.String(), "\n"), err
	case "handoff":
		var parts []string
		for _, entry := range x.engine.cfg.Slots.Snapshot().Entries(slots.Handoff) {
			if text := strings.TrimSpace(entry.Content); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n\n"), nil
	case "plan":
		// The plan is the parent's scratchpad entry "plan".
		key = "plan"
	}
	return db.Scratch(ctx, x.info.ID, key)
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

func hasToolUse(m store.Message) bool {
	return slices.ContainsFunc(m.Blocks, func(b llmBlock) bool { return b.Type == "tool_use" })
}

// settled drops the part of history from the first assistant message whose
// tool calls have no results: the parent's in-flight message (the one calling
// subtask) and anything after it. Calls and results are paired by position,
// a tool message answering the assistant message just before it, because
// some providers reuse call IDs across turns.
func settled(history []store.Message) []store.Message {
	for i, m := range history {
		if m.Role == "assistant" && hasToolUse(m) && (i+1 == len(history) || history[i+1].Role != "tool") {
			return history[:i]
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

// toolNames names each tool result in a tool message from the assistant
// message before it: by call ID when that ID is unique there, else by the
// result's position.
func toolNames(call *store.Message, results store.Message) []string {
	var uses []llmBlock
	if call != nil {
		for _, b := range call.Blocks {
			if b.Type == "tool_use" {
				uses = append(uses, b)
			}
		}
	}
	var names []string
	position := 0
	for _, b := range results.Blocks {
		if b.Type != "tool_result" {
			continue
		}
		name := ""
		matches := 0
		for _, u := range uses {
			if u.ID == b.ToolUseID {
				name, matches = u.Name, matches+1
			}
		}
		if matches != 1 && position < len(uses) {
			name = uses[position].Name
		}
		if name == "" {
			name = "a tool"
		}
		names = append(names, name)
		position++
	}
	return names
}

// renderMessages writes each message as one numbered line, scrubbing every
// block before shortening it to limit characters (tool inputs to a quarter of
// that). before is the message just before the first, for naming its tool
// results; history says whether the reader can fetch a shortened message.
func renderMessages(messages []store.Message, before *store.Message, limit int, scrub func(string) string, history bool) []string {
	lines := make([]string, 0, len(messages))
	for i, m := range messages {
		previous := before
		if i > 0 {
			previous = &messages[i-1]
		}
		hint := ""
		if history {
			hint = fmt.Sprintf("parent_history_get from=%d to=%d has it all", m.Seq, m.Seq)
		}
		var b strings.Builder
		fmt.Fprintf(&b, "[#%d %s]", m.Seq, m.Role)
		var names []string
		if m.Role == "tool" {
			names = toolNames(previous, m)
		}
		result := 0
		for _, block := range m.Blocks {
			switch block.Type {
			case "text":
				b.WriteString(" " + cut(scrub(block.Text), limit, hint))
			case "tool_use":
				input := "{}"
				if block.Input != nil {
					raw, _ := json.Marshal(*block.Input)
					input = string(raw)
				}
				fmt.Fprintf(&b, " (called %s %s)", scrub(block.Name), cut(scrub(input), limit/4, hint))
			case "tool_result":
				status := "result"
				if block.IsError {
					status = "error"
				}
				name := "a tool"
				if result < len(names) {
					name = names[result]
				}
				result++
				fmt.Fprintf(&b, " (%s of %s: %s)", status, scrub(name), cut(scrub(block.Content), limit, hint))
			}
		}
		b.WriteString("\n")
		lines = append(lines, b.String())
	}
	return lines
}

// cut shortens already-redacted text to limit characters, saying where the
// rest is when hint is set.
func cut(text string, limit int, hint string) string {
	text = strings.TrimSpace(text)
	runes := []rune(text)
	if limit <= 0 || len(runes) <= limit {
		return text
	}
	if hint == "" {
		return string(runes[:limit]) + " … [shortened]"
	}
	return string(runes[:limit]) + " … [shortened; " + hint + "]"
}

// messageText is a message's searchable text, as the index sees it: text,
// tool results, and tool names and inputs, without thinking.
func messageText(m store.Message) string {
	var parts []string
	for _, b := range m.Blocks {
		switch b.Type {
		case "thinking":
		case "tool_use":
			input := ""
			if b.Input != nil {
				raw, _ := json.Marshal(*b.Input)
				input = string(raw)
			}
			parts = append(parts, strings.TrimSpace(b.Name+" "+input))
		default:
			parts = append(parts, strings.TrimSpace(b.Text+" "+b.Content))
		}
	}
	return strings.Join(parts, " ")
}

// snippet is the redacted text around the first match of any of words, with
// every match in the window marked [like this]. text must already be
// redacted: cutting it cannot then expose part of a secret.
func snippet(text string, words []string) string {
	runes := []rune(text)
	lower := make([]rune, len(runes))
	for i, r := range runes {
		lower[i] = unicode.ToLower(r)
	}
	targets := make([][]rune, 0, len(words))
	for _, w := range words {
		target := []rune(w)
		for i, r := range target {
			target[i] = unicode.ToLower(r)
		}
		targets = append(targets, target)
	}
	at := func(i int) int {
		for _, t := range targets {
			if len(t) > 0 && i+len(t) <= len(lower) && slices.Equal(lower[i:i+len(t)], t) {
				return len(t)
			}
		}
		return 0
	}
	first := -1
	for i := range lower {
		if at(i) > 0 {
			first = i
			break
		}
	}
	if first < 0 {
		first = 0
	}
	start, end := max(0, first-snippetChars), min(len(runes), first+snippetChars)
	var b strings.Builder
	if start > 0 {
		b.WriteString("…")
	}
	for i := start; i < end; {
		if n := at(i); n > 0 && i+n <= end {
			b.WriteString("[" + string(runes[i:i+n]) + "]")
			i += n
			continue
		}
		b.WriteRune(runes[i])
		i++
	}
	if end < len(runes) {
		b.WriteString("…")
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// ancestors is the chain of conversations above this one, nearest first.
func (x *execution) ancestors(ctx context.Context) ([]string, error) {
	var out []string
	parent := x.info.ParentID
	for parent != "" && len(out) < subagent.MaxDepth {
		out = append(out, parent)
		info, err := x.engine.cfg.Store.Conversation(ctx, parent)
		if err != nil {
			return nil, err
		}
		parent = info.ParentID
	}
	return out, nil
}

// resultScope is the scope that holds cached tool result id for this
// conversation: its own, or for a child the nearest ancestor's, so a child
// can follow a pointer in the parent context it was given.
func (x *execution) resultScope(ctx context.Context, id string) string {
	scopes := []string{x.info.ID}
	if up, err := x.ancestors(ctx); err == nil {
		scopes = append(scopes, up...)
	}
	for _, scope := range scopes {
		if _, err := x.engine.cfg.Store.Results().Get(ctx, scope, id); !errors.Is(err, toolresult.ErrNotFound) {
			return scope
		}
	}
	return x.info.ID
}

// parentHistory serves a child's parent_history_search and parent_history_get
// over its parent conversation.
func (x *execution) parentHistory() selftools.ParentHistory {
	db, parent, scrub := x.engine.cfg.Store, x.info.ParentID, x.engine.scrub.Text
	return selftools.ParentHistory{
		Search: func(ctx context.Context, query string, k int) (string, error) {
			if k <= 0 {
				k = 5
			}
			hits, err := db.SearchMessages(ctx, parent, query, min(k, 20))
			if err != nil {
				return "", err
			}
			if len(hits) == 0 {
				return "No parent messages match.", nil
			}
			words := store.QueryWords(query)
			var b strings.Builder
			for _, hit := range hits {
				// Redact the whole message first, then cut and mark the snippet.
				fmt.Fprintf(&b, "[#%d %s %s] %s\n", hit.Seq, hit.Role, hit.CreatedAt.Format(time.RFC3339), snippet(scrub(messageText(hit)), words))
			}
			return b.String(), nil
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
			// One message before the range, to name the tool results that open it.
			messages, err := db.MessagesBetween(ctx, parent, max(1, from-1), to)
			if err != nil {
				return "", err
			}
			var before *store.Message
			if len(messages) > 0 && messages[0].Seq < from {
				before, messages = &messages[0], messages[1:]
			}
			if len(messages) == 0 {
				return "No parent messages in that range.", nil
			}
			return strings.Join(renderMessages(messages, before, parentHistoryBlockChars, scrub, false), "") + note, nil
		},
	}
}
