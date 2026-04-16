package messaging

// Message is a single agent-to-agent (or agent-to-user) message row.
// Addressing is scoped to a (session_id, agent_id) tuple on both ends
// so that two instances of the same agent running in different
// sessions have distinct inboxes.
//
// T11 (stream-upgrade compat shim): ThreadID is the seed for a future
// stream_id; a later migration will add a participants[] array column
// and retire the denormalized from_*/to_* tuples. Keep the field
// TEXT-sized — no fixed-length assumptions — so that migration is a
// column-add, not a coercion.
type Message struct {
	ID            string  `json:"id"`
	FromSessionID string  `json:"from_session_id"`
	FromAgentID   string  `json:"from_agent_id"`
	ToSessionID   string  `json:"to_session_id"`
	ToAgentID     string  `json:"to_agent_id"`
	ThreadID      string  `json:"thread_id"`
	ReplyTo       string  `json:"reply_to"`
	Type          string  `json:"type"`
	Subject       string  `json:"subject"`
	Body          string  `json:"body"`
	Metadata      string  `json:"metadata"`
	Priority      int     `json:"priority"`
	Status        string  `json:"status"`
	CreatedAt     string  `json:"created_at"`
	ReadAt        *string `json:"read_at"`
	ResolvedAt    *string `json:"resolved_at"`
}

// SendInput is the caller-supplied portion of a new message. Defaults
// for missing fields are filled in by Store.Send — see the Store
// interface doc for the full default list.
type SendInput struct {
	FromSessionID string
	FromAgentID   string
	ToSessionID   string
	ToAgentID     string
	Type          string
	Subject       string
	Body          string
	ThreadID      string
	ReplyTo       string
	Metadata      string
	Priority      int
}

// Message type constants (wire-level "type" column on each row). These
// name the message's semantic role; they are distinct from S5
// envelope content types (UI shapes) and from MessageKind (wire type,
// introduced in T4). See docs/messaging.md §Vocabulary for the three-
// way distinction once that doc lands.
const (
	TypeMessage      = "message"
	TypeHelpRequest  = "help_request"
	TypeDirective    = "directive"
	TypeStatusUpdate = "status_update"
	TypeHandoff      = "handoff"
)

// Status constants for the message row's lifecycle column.
const (
	StatusUnread       = "unread"
	StatusRead         = "read"
	StatusAcknowledged = "acknowledged"
	StatusResolved     = "resolved"
)

// MaxRecentLimit is the absolute upper bound the Store applies to
// Recent(limit). Callers that pass a larger limit — or a service-layer
// default that wraps it — are clamped here. Protects against memory
// DoS from an LLM-controlled catch_up call; aligns with the broader
// Phase 3 pattern of bounding LLM-reachable resource knobs (see S4a
// tool result cache; S4b trust-tier result size caps).
const MaxRecentLimit = 100

// DefaultRecentLimit is the default row count for Recent(0). The Store
// and Service layers agree on the same default so that direct store
// calls and service-wrapped calls return the same row count.
const DefaultRecentLimit = 20
