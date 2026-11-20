package engine

import (
	"encoding/json"
	"slices"
	"strings"
	"unicode/utf8"

	ctxpkg "github.com/hollis-labs/libs/agent/core/context"
	"github.com/hollis-labs/libs/llm/core/llmtypes"
)

// DefaultCompactionPrompt is the system prompt of the call that writes a
// compaction summary, unless Config.CompactionPrompt replaces it.
const DefaultCompactionPrompt = `You are writing the running summary of a long conversation between a person and an agent, so that the agent can go on without the earlier messages. The agent will be sent your summary in place of them, then the most recent messages in full. The messages may begin with an earlier summary: carry what still matters from it into yours.

Write what the agent needs in order to continue as if it remembered:
- What the person asked for and decided, and any preferences or corrections they gave.
- What was done, with the specifics: what was changed and where, the ids and names involved.
- Facts found along the way that still matter, with the specifics (names, ids, numbers).
- What is unfinished or was promised.

Leave out pleasantries and anything that was superseded. Do not address anyone and do not add advice. Plain text, with short headings if it helps, at most about 1,500 words.`

// DefaultCompactionInputTokens bounds one summary call's input, in
// estimated tokens, unless Config.CompactionInputTokens sets it.
const DefaultCompactionInputTokens = 100_000

// ownerMessage reports whether a message starts a turn: the person's own
// message, as against tool results, which are also sent as the user's.
func ownerMessage(m llmtypes.ChatMessage) bool {
	if m.Role != "user" {
		return false
	}
	return !slices.ContainsFunc(m.ContentBlocks, func(b llmtypes.ContentBlock) bool { return b.Type == "tool_result" })
}

// messagesTokens estimates messages the way the compaction check does.
func messagesTokens(msgs []llmtypes.ChatMessage) int {
	raw, err := json.Marshal(msgs)
	if err != nil {
		return 0
	}
	return ctxpkg.DefaultEstimator{}.Estimate(string(raw))
}

// keepCut picks where the kept part of the conversation starts: the start
// of a turn such that the turns from it on fit keepTokens, never later than
// the last turn's start. Everything before it is summarized. It returns 0
// when there is nothing before the kept part. ids marks stored messages; a
// message with no id (an earlier summary) never starts a kept turn.
func keepCut(msgs []llmtypes.ChatMessage, ids []string, keepTokens int) int {
	var starts []int
	for i, m := range msgs {
		if i < len(ids) && ids[i] != "" && ownerMessage(m) {
			starts = append(starts, i)
		}
	}
	if len(starts) == 0 {
		return 0
	}
	// The last turn is always kept; earlier ones while they fit.
	cut := starts[len(starts)-1]
	for k := len(starts) - 2; k >= 0; k-- {
		if messagesTokens(msgs[starts[k]:]) > keepTokens {
			break
		}
		cut = starts[k]
	}
	return cut
}

// transcript writes messages as plain text for a summary call, with long
// blocks clipped, keeping the end when the whole is longer than maxChars.
func transcript(msgs []llmtypes.ChatMessage, maxChars int) string {
	var b strings.Builder
	for _, m := range msgs {
		who := "Agent"
		if m.Role == "user" {
			who = "Person"
		}
		if m.Content != "" {
			b.WriteString(who + ": " + clip(m.Content, 6000) + "\n\n")
		}
		for _, blk := range m.ContentBlocks {
			switch blk.Type {
			case "text":
				b.WriteString(who + ": " + clip(blk.Text, 6000) + "\n\n")
			case "tool_use":
				input := ""
				if blk.Input != nil {
					raw, _ := json.Marshal(blk.Input)
					input = clip(string(raw), 800)
				}
				b.WriteString("Agent called " + blk.Name + " " + input + "\n")
			case "tool_result":
				state := "result"
				if blk.IsError {
					state = "failed"
				}
				b.WriteString("  " + state + ": " + clip(blk.Content, 1500) + "\n\n")
			}
		}
	}
	text := b.String()
	if len(text) > maxChars {
		// The end matters most; the start is the part an earlier summary
		// already covers.
		from := len(text) - maxChars
		for from < len(text) && !utf8.RuneStart(text[from]) {
			from++
		}
		text = "[the start of this part is left out]\n" + text[from:]
	}
	return text
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + " [...]"
}

// summaryInput is what a summary call is sent for span: the messages as
// they are while they fit inputTokens, else a clipped transcript.
func summaryInput(span []llmtypes.ChatMessage, inputTokens int) []llmtypes.ChatMessage {
	if inputTokens <= 0 {
		inputTokens = DefaultCompactionInputTokens
	}
	if messagesTokens(span) <= inputTokens {
		return span
	}
	return []llmtypes.ChatMessage{{Role: "user", Content: transcript(span, inputTokens*4)}}
}

// requestTokens is the estimated size of a request with conversation
// messages of conversationTokens: the window's fixed slots and tool schemas
// plus the conversation, scaled up when the provider counted more than the
// last estimate.
func (x *execution) requestTokens(window *ctxpkg.ContextWindow, conversationTokens int) int {
	n := conversationTokens
	if window != nil {
		n += staticTokens(window)
	}
	if x.lastRequestTokens > 0 && x.lastRequestEstimate > 0 && x.lastRequestTokens > x.lastRequestEstimate {
		n = int(float64(n) * float64(x.lastRequestTokens) / float64(x.lastRequestEstimate))
	}
	return n
}

// handoffPresent reports whether the window carries a handoff, which the
// pipeline treats as the continuity instead of a summary.
func handoffPresent(window *ctxpkg.ContextWindow) bool {
	h := window.Slot(ctxpkg.SlotHandoff)
	return h != nil && h.Content != ""
}

// cloneMessages copies messages and their blocks, so pipeline stages that
// rewrite blocks in place (dedupe) never change the live history.
func cloneMessages(msgs []llmtypes.ChatMessage) []llmtypes.ChatMessage {
	out := slices.Clone(msgs)
	for i := range out {
		out[i].ContentBlocks = slices.Clone(out[i].ContentBlocks)
	}
	return out
}
