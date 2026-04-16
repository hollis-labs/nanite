package messaging

// Kind constants name the wire type of a message — what shape of
// payload rides on it and how receivers should react. Distinct from
// the message Type constants (semantic role) and from S5 envelope
// content types (UI shape). See docs/messaging.md §Vocabulary.
const (
	KindRequest      = "request"
	KindReply        = "reply"
	KindNotification = "notification"
	KindHandoff      = "handoff"
)

// Channel constants name the transport bucket a message travels on.
// Distinct from MessageKind (wire type, T4) and S5 EnvelopeType (UI
// content shape). Channel usage policy lives in docs/messaging.md:
//   - chat  : in-session conversational traffic (primary ↔ secondary)
//   - inbox : async polled; triggers notifications on arrival
//   - alert : agent-triggered one-off ("report ready") — toast + inbox
const (
	ChannelChat  = "chat"
	ChannelInbox = "inbox"
	ChannelAlert = "alert"
)

// InboxFilter bundles optional inbox filters. An empty-string value
// on any field means "no constraint on that dimension". Callers
// construct a filter with whichever fields they want to narrow by.
type InboxFilter struct {
	Status  string
	Channel string
	Kind    string
}

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
	Channel       string  `json:"channel"`
	Kind          string  `json:"kind"`
	PayloadJSON   string  `json:"payload_json"`
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
	// Channel is required; empty defaults to ChannelChat. The Store
	// validates against the CHECK constraint so unknown values reject
	// at insert time rather than silently rewriting themselves.
	Channel string
	// Kind is optional; empty defaults to KindNotification. Same CHECK
	// semantics as Channel — invalid values reject at insert.
	Kind string
	// PayloadJSON carries kind-specific structured payload per S5's
	// ResponseV1 shape. Empty defaults to "{}".
	PayloadJSON string
	Type        string
	Subject     string
	Body        string
	ThreadID    string
	ReplyTo     string
	Metadata    string
	Priority    int
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
