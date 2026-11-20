package selftools

import (
	"context"

	"github.com/hollis-labs/nanite/internal/tools"
)

// History is a root conversation's read-only view of its own transcript,
// including messages compaction has summarized. A subtask child uses
// ParentHistory instead. Searching other conversations belongs to the
// shared transcript service, where policy applies, not to Nanite.
type History struct {
	// Search finds up to k of this conversation's messages matching query.
	Search func(ctx context.Context, query string, k int) (string, error)
	// Get returns this conversation's messages numbered from through to.
	Get func(ctx context.Context, from, to int) (string, error)
}

// Tools returns history_search and history_get.
func (h History) Tools() []tools.Tool {
	return []tools.Tool{
		spec("history_search", "Search this conversation's full transcript by words, including messages compaction summarized. "+
			"Returns message numbers with a matching snippet; read messages in full with history_get.",
			`{"query":{"type":"string","description":"Words to look for."},"k":{"type":"integer","minimum":1,"maximum":20,"description":"How many messages to return; default 5."}}`,
			[]string{"query"}, true,
			func(ctx context.Context, args struct {
				Query string
				K     int
			}) (string, error) {
				return h.Search(ctx, args.Query, args.K)
			}),
		spec("history_get", "Read this conversation's messages by number, from through to (at most 20 at a time).",
			`{"from":{"type":"integer","minimum":1},"to":{"type":"integer","minimum":1}}`,
			[]string{"from", "to"}, true,
			func(ctx context.Context, args struct{ From, To int }) (string, error) {
				return h.Get(ctx, args.From, args.To)
			}),
	}
}
