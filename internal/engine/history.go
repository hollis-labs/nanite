package engine

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/hollis-labs/nanite/internal/selftools"
	"github.com/hollis-labs/nanite/internal/store"
)

const (
	// historyBlockChars bounds each block history_get returns; the
	// tool-result cache pages anything longer than the call's budget.
	historyBlockChars = 8000
	// maxHistoryRange is the most messages one history_get returns.
	maxHistoryRange = 20
	// maxHistoryHits is the most messages one history search returns.
	maxHistoryHits = 20
	// maxHistoryOutput bounds everything one history_get returns; it stops
	// at a whole message and says where to continue.
	maxHistoryOutput = 32000
	// snippetWindow is how much raw text on each side of a match is redacted
	// to make a snippet, so a long message is not redacted whole. It is far
	// wider than the snippet, so a secret cut at the window's edge never
	// reaches the snippet.
	snippetWindow = 2000
)

// historyTools names the tools a root conversation reads its own transcript
// with.
var historyTools = []string{"history_search", "history_get"}

// HistoryHit is one message a history search found: where it is and a
// redacted snippet around its first match.
type HistoryHit struct {
	ConversationID string
	Seq            int
	Role           string
	At             time.Time
	Snippet        string
}

// SearchHistory finds up to k (default 5, at most 20) of a root
// conversation's messages matching query, best match first, with redacted
// snippets. Only that conversation is searched: searching across
// conversations belongs to the shared transcript service.
func (e *Engine) SearchHistory(ctx context.Context, conversation, query string, k int) ([]HistoryHit, error) {
	if k <= 0 {
		k = 5
	}
	k = min(k, maxHistoryHits)
	if err := e.rootConversation(ctx, conversation); err != nil {
		return nil, err
	}
	found, err := e.cfg.Store.SearchMessages(ctx, conversation, query, k)
	if err != nil {
		return nil, err
	}
	words := store.QueryWords(query)
	out := make([]HistoryHit, 0, len(found))
	for _, m := range found {
		out = append(out, HistoryHit{ConversationID: conversation, Seq: m.Seq, Role: m.Role, At: m.CreatedAt,
			Snippet: windowedSnippet(messageText(m), words, e.scrub.Text)})
	}
	return out, nil
}

// windowedSnippet is snippet over a redacted window of raw text around the
// first match, so a long message is not redacted whole. The window is
// snippetWindow characters on each side, far wider than the snippet, and its
// edges are moved inward to whitespace so it never starts or ends inside a
// token: a secret cut in half might no longer be recognised by redaction. If
// the match is gone after redaction (it was inside a secret), no text from the
// window is shown; a short message is redacted whole.
func windowedSnippet(raw string, words []string, scrub func(string) string) string {
	runes := []rune(raw)
	if len(runes) <= 4*snippetWindow {
		return snippet(scrub(raw), words)
	}
	at := firstMatch(runes, words)
	from, to := max(0, at-snippetWindow), min(len(runes), at+snippetWindow)
	if from > 0 {
		for from < at && !unicode.IsSpace(runes[from-1]) {
			from++
		}
	}
	if to < len(runes) {
		for to > at && !unicode.IsSpace(runes[to]) {
			to--
		}
	}
	window := scrub(string(runes[from:to]))
	if !containsAny(window, words) {
		return redactedMatch
	}
	return snippet(window, words)
}

// redactedMatch is the snippet of a hit whose matching words were inside
// redacted text.
const redactedMatch = "(the match is inside redacted text)"

// containsAny reports whether text contains any of words, ignoring case.
func containsAny(text string, words []string) bool {
	lower := strings.ToLower(text)
	for _, w := range words {
		if w != "" && strings.Contains(lower, strings.ToLower(w)) {
			return true
		}
	}
	return false
}

// firstMatch is the rune index of the first case-insensitive match of any of
// words in runes, or 0.
func firstMatch(runes []rune, words []string) int {
	lower := []rune(strings.ToLower(string(runes)))
	if len(lower) != len(runes) {
		lower = runes // lowercasing changed the length; match case-sensitively
	}
	best := -1
	for _, w := range words {
		target := []rune(strings.ToLower(w))
		if len(target) == 0 {
			continue
		}
		for i := 0; i+len(target) <= len(lower); i++ {
			if slices.Equal(lower[i:i+len(target)], target) {
				if best < 0 || i < best {
					best = i
				}
				break
			}
		}
	}
	return max(best, 0)
}

// rootConversation checks that id names a root conversation of this agent.
func (e *Engine) rootConversation(ctx context.Context, id string) error {
	info, err := e.cfg.Store.Conversation(ctx, id)
	if errors.Is(err, store.ErrNoConversation) {
		return fmt.Errorf("no conversation %q", id)
	}
	if err != nil {
		return err
	}
	if info.ParentID != "" {
		return fmt.Errorf("conversation %q is a subtask's, not a root conversation", id)
	}
	return nil
}

// HistoryMessages renders a root conversation's messages numbered from
// through to (at most 20), redacted and with long blocks shortened, as
// history_get returns them.
func (e *Engine) HistoryMessages(ctx context.Context, conversation string, from, to int) (string, error) {
	if from < 1 || to < from {
		return "", fmt.Errorf("history_get needs 1 <= from <= to")
	}
	if err := e.rootConversation(ctx, conversation); err != nil {
		return "", err
	}
	note := ""
	if to-from+1 > maxHistoryRange {
		to = from + maxHistoryRange - 1
		note = fmt.Sprintf("(only %d messages at a time; continue from %d)\n", maxHistoryRange, to+1)
	}
	// One message before the range, to name the tool results that open it.
	messages, err := e.cfg.Store.MessagesBetween(ctx, conversation, max(1, from-1), to)
	if err != nil {
		return "", err
	}
	var before *store.Message
	if len(messages) > 0 && messages[0].Seq < from {
		before, messages = &messages[0], messages[1:]
	}
	if len(messages) == 0 {
		return "No messages in that range.", nil
	}
	lines := renderMessages(messages, before, historyBlockChars, e.scrub.Text, "")
	var b strings.Builder
	for i, line := range lines {
		// Stop at a whole message once the call's output is full, but always
		// return at least one.
		if i > 0 && b.Len()+len(line) > maxHistoryOutput {
			note = fmt.Sprintf("(output limit reached; continue from %d)\n", messages[i].Seq)
			break
		}
		b.WriteString(line)
	}
	return b.String() + note, nil
}

// history serves a root conversation's history_search and history_get.
func (x *execution) history() selftools.History {
	e, self := x.engine, x.info.ID
	return selftools.History{
		Search: func(ctx context.Context, query string, k int) (string, error) {
			hits, err := e.SearchHistory(ctx, self, query, k)
			if err != nil {
				return "", err
			}
			if len(hits) == 0 {
				return "No messages match.", nil
			}
			var b strings.Builder
			for _, h := range hits {
				fmt.Fprintf(&b, "[#%d %s %s] %s\n", h.Seq, h.Role, h.At.Format(time.RFC3339), h.Snippet)
			}
			return b.String(), nil
		},
		Get: func(ctx context.Context, from, to int) (string, error) {
			return e.HistoryMessages(ctx, self, from, to)
		},
	}
}

// offersHistory reports whether this conversation is offered history_search
// and history_get: only a root conversation, and only with the history
// group.
func (x *execution) offersHistory() bool {
	if x.info.ParentID != "" {
		return false
	}
	for _, name := range historyTools {
		if !x.engine.cfg.Builtins.Offers(name) {
			return false
		}
	}
	return true
}

// historyNote tells the model where the compacted messages are: their
// number range in the transcript, when the store knows it, and the tools
// that read them.
func (x *execution) historyNote(ctx context.Context, compacted []string) string {
	var ids []string
	for _, id := range compacted {
		if id != "" {
			ids = append(ids, id)
		}
	}
	span := ""
	if len(ids) > 0 {
		if seqs, err := x.engine.cfg.Store.MessageSeqs(ctx, x.info.ID, ids[0], ids[len(ids)-1]); err == nil {
			first, okFirst := seqs[ids[0]]
			last, okLast := seqs[ids[len(ids)-1]]
			switch {
			case okFirst && okLast && compacted[0] == "":
				// The first compacted message is an earlier summary, which
				// covers everything before this span too.
				span = fmt.Sprintf(" (messages #1-#%d)", last)
			case okFirst && okLast:
				span = fmt.Sprintf(" (messages #%d-#%d)", first, last)
			}
		}
	}
	return "[The full earlier messages" + span + " are kept: find them with history_search and read them by number with history_get.]"
}
