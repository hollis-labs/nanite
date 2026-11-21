package integrity

import "github.com/hollis-labs/nanite/internal/transcript"

// Input is everything one validation looks at. Only Entries is required.
type Input struct {
	// ConversationID is the conversation the transcript belongs to. When set,
	// every record must name it as its sessionId.
	ConversationID string
	// Entries are the records transcript.Replay returned from the zero cursor.
	Entries []transcript.Entry
	// ReplayErr is the error that Replay returned with Entries, if any. On
	// transcript.ErrTorn, Entries is the verified prefix before the torn record.
	ReplayErr error
	// Index, when set, is what nanite.db holds for the conversation. It is
	// compared with the projection of Entries.
	Index *Index
	// Hold, when set, is the interrupted-turn hold a host saved for the
	// conversation (chatrunner's interrupted_since host state).
	Hold *Hold
}

// Index is the index projection of one conversation, as nanite.db holds it.
type Index struct {
	// Cursor is the transcript cursor the index has projected through, or
	// nil when it has projected nothing.
	Cursor      *transcript.Cursor `json:"cursor,omitempty"`
	Turns       []IndexTurn        `json:"turns,omitempty"`
	Messages    []IndexMessage     `json:"messages,omitempty"` // in seq order
	Compactions []IndexCompaction  `json:"compactions,omitempty"`
	// Notices are not projected from the transcript; they are checked only
	// for the turn their delivery names.
	Notices []IndexNotice `json:"notices,omitempty"`
}

type IndexTurn struct {
	ID            string `json:"id"`
	Status        string `json:"status"`
	UserMessageID string `json:"user_message_id"`
}

// IndexMessage is one message row. Role is the index role: user, assistant,
// or tool for a user-role message whose first block is a tool result.
type IndexMessage struct {
	ID     string `json:"id"`
	TurnID string `json:"turn_id"`
	Role   string `json:"role"`
}

type IndexCompaction struct {
	ID               string `json:"id"`
	TurnID           string `json:"turn_id"`
	ThroughMessageID string `json:"through_message_id"`
}

type IndexNotice struct {
	ID              string `json:"id"`
	DeliveredTurnID string `json:"delivered_turn_id,omitempty"`
}

// Hold is a saved interrupted-turn hold.
type Hold struct {
	TurnID string `json:"turn"`
}
