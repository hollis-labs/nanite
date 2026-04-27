package strategy

// DecisionEntry is the persisted shape of a Strategy decision. The
// shape mirrors Strategy plus the join keys (session_id, turn_id) that
// belong to the row, not the in-memory record.
//
// Used by StrategyLogger.LogStrategyDecision; *store.Store satisfies
// that interface (see internal/store/strategy_log.go).
type DecisionEntry struct {
	// SessionID is the chat session this decision belongs to.
	SessionID string

	// TurnID is the user-turn message ID (when tracked). Optional.
	TurnID string

	// Approach is the chosen Approach (string-encoded).
	Approach Approach

	// Rationale is the planner's reason text.
	Rationale string

	// MaxTurns is the initial turn budget.
	MaxTurns int

	// EscalationBudget is the v2-reserved extension budget.
	EscalationBudget int

	// ReflexMatchID is the matched reflex ID, or "" when none.
	ReflexMatchID string

	// PlaybookHit is the consulted playbook ID, or "" when none.
	// Always "" in v1.
	PlaybookHit string

	// GroundingConsultationIDs is the slice of consultation row IDs.
	// Stored as JSON in the DB.
	GroundingConsultationIDs []int64
}

// StrategyLogger persists strategy decisions. *store.Store satisfies
// this interface via internal/store/strategy_log.go.
//
// Errors should be returned; callers (chat generation) log and
// continue — strategy logging must NEVER gate the dispatch path.
type StrategyLogger interface {
	// LogStrategyDecision persists one strategy decision. Returns the
	// auto-assigned row ID for traceability.
	LogStrategyDecision(entry DecisionEntry) (int64, error)
}

// FromStrategy builds a DecisionEntry from a Strategy and the join keys.
// Convenience for callers that already have a Strategy plus session
// context.
func FromStrategy(s Strategy, sessionID, turnID string) DecisionEntry {
	return DecisionEntry{
		SessionID:                sessionID,
		TurnID:                   turnID,
		Approach:                 s.Approach,
		Rationale:                s.Rationale,
		MaxTurns:                 s.MaxTurns,
		EscalationBudget:         s.EscalationBudget,
		ReflexMatchID:            s.ReflexMatchID,
		PlaybookHit:              s.PlaybookHit,
		GroundingConsultationIDs: s.GroundingConsultationIDs,
	}
}
