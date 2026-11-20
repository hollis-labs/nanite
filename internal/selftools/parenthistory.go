package selftools

import (
	"context"

	"github.com/hollis-labs/nanite/internal/tools"
)

// ParentHistory is a subtask child's read-only view of its parent
// conversation. Only a child is offered these tools.
type ParentHistory struct {
	// Search finds up to k parent messages matching query.
	Search func(ctx context.Context, query string, k int) (string, error)
	// Get returns the parent messages numbered from through to.
	Get func(ctx context.Context, from, to int) (string, error)
}

// Tools returns parent_history_search and parent_history_get.
func (h ParentHistory) Tools() []tools.Tool {
	return []tools.Tool{
		spec("parent_history_search", "Search the parent conversation's messages, including ones you were not given, by words. "+
			"Returns message numbers with a matching snippet; read a message in full with parent_history_get.",
			`{"query":{"type":"string","description":"Words to look for."},"k":{"type":"integer","minimum":1,"maximum":20,"description":"How many messages to return; default 5."}}`,
			[]string{"query"}, true,
			func(ctx context.Context, args struct {
				Query string
				K     int
			}) (string, error) {
				return h.Search(ctx, args.Query, args.K)
			}),
		spec("parent_history_get", "Read parent conversation messages by number, from through to (at most 20 at a time).",
			`{"from":{"type":"integer","minimum":1},"to":{"type":"integer","minimum":1}}`,
			[]string{"from", "to"}, true,
			func(ctx context.Context, args struct{ From, To int }) (string, error) {
				return h.Get(ctx, args.From, args.To)
			}),
	}
}
