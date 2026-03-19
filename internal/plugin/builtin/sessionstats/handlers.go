package sessionstats

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// SessionStats represents session statistics data
type SessionStats struct {
	ID                string    `json:"id" db:"id"`
	SessionID         string    `json:"session_id" db:"session_id"`
	MessageCount      int       `json:"message_count" db:"message_count"`
	StartTime         *time.Time `json:"start_time" db:"start_time"`
	EndTime           *time.Time `json:"end_time" db:"end_time"`
	TotalInputTokens  int       `json:"total_input_tokens" db:"total_input_tokens"`
	TotalOutputTokens int       `json:"total_output_tokens" db:"total_output_tokens"`
	AgentSwitches     int       `json:"agent_switches" db:"agent_switches"`
	ToolCalls         int       `json:"tool_calls" db:"tool_calls"`
	CreatedAt         time.Time `json:"created_at" db:"created_at"`
	UpdatedAt         time.Time `json:"updated_at" db:"updated_at"`
}

// SessionStatsStore handles database operations for session statistics
type SessionStatsStore struct {
	db *sql.DB
}

// NewSessionStatsStore creates a new session stats store
func NewSessionStatsStore(db *sql.DB) *SessionStatsStore {
	return &SessionStatsStore{db: db}
}

// InitSchema creates the session_stats table if it doesn't exist
func (s *SessionStatsStore) InitSchema() error {
	schema := `
	CREATE TABLE IF NOT EXISTS session_stats (
		id TEXT PRIMARY KEY,
		session_id TEXT NOT NULL UNIQUE,
		message_count INTEGER DEFAULT 0,
		start_time TIMESTAMP,
		end_time TIMESTAMP,
		total_input_tokens INTEGER DEFAULT 0,
		total_output_tokens INTEGER DEFAULT 0,
		agent_switches INTEGER DEFAULT 0,
		tool_calls INTEGER DEFAULT 0,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);

	CREATE INDEX IF NOT EXISTS idx_session_stats_session_id ON session_stats(session_id);
	`

	_, err := s.db.Exec(schema)
	return err
}

// GetSessionStats retrieves statistics for a session
func (s *SessionStatsStore) GetSessionStats(sessionID string) (*SessionStats, error) {
	query := `
	SELECT id, session_id, message_count, start_time, end_time,
		   total_input_tokens, total_output_tokens, agent_switches,
		   tool_calls, created_at, updated_at
	FROM session_stats
	WHERE session_id = ?`

	var stats SessionStats
	err := s.db.QueryRow(query, sessionID).Scan(
		&stats.ID, &stats.SessionID, &stats.MessageCount,
		&stats.StartTime, &stats.EndTime, &stats.TotalInputTokens,
		&stats.TotalOutputTokens, &stats.AgentSwitches, &stats.ToolCalls,
		&stats.CreatedAt, &stats.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		// Create new stats record if it doesn't exist
		return s.CreateSessionStats(sessionID)
	}

	return &stats, err
}

// CreateSessionStats creates a new session stats record
func (s *SessionStatsStore) CreateSessionStats(sessionID string) (*SessionStats, error) {
	stats := &SessionStats{
		ID:        fmt.Sprintf("stats_%s_%d", sessionID, time.Now().Unix()),
		SessionID: sessionID,
		StartTime: &time.Time{},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	query := `
	INSERT INTO session_stats (id, session_id, created_at, updated_at)
	VALUES (?, ?, ?, ?)
	`

	_, err := s.db.Exec(query, stats.ID, stats.SessionID, stats.CreatedAt, stats.UpdatedAt)
	if err != nil {
		return nil, err
	}

	return stats, nil
}

// UpdateSessionStats updates session statistics
func (s *SessionStatsStore) UpdateSessionStats(sessionID string, updates map[string]interface{}) error {
	// Build dynamic update query
	setParts := []string{"updated_at = ?"}
	args := []interface{}{time.Now()}

	for field, value := range updates {
		setParts = append(setParts, fmt.Sprintf("%s = ?", field))
		args = append(args, value)
	}

	args = append(args, sessionID)

	query := fmt.Sprintf("UPDATE session_stats SET %s WHERE session_id = ?",
		strings.Join(setParts, ", "))

	_, err := s.db.Exec(query, args...)
	return err
}

// Plugin Handler Functions

// GetSessionStatsHandler handles GET /api/plugins/session-stats/stats/{session_id}
func GetSessionStatsHandler(store *SessionStatsStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sessionID := r.PathValue("session_id")

		stats, err := store.GetSessionStats(sessionID)
		if err != nil {
			http.Error(w, "Failed to get session stats", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(stats)
	}
}

// UpdateSessionStatsHandler handles POST /api/plugins/session-stats/stats/{session_id}
func UpdateSessionStatsHandler(store *SessionStatsStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sessionID := r.PathValue("session_id")

		var updates map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&updates); err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}

		if err := store.UpdateSessionStats(sessionID, updates); err != nil {
			http.Error(w, "Failed to update session stats", http.StatusInternalServerError)
			return
		}

		// Return updated stats
		stats, err := store.GetSessionStats(sessionID)
		if err != nil {
			http.Error(w, "Failed to get updated stats", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(stats)
	}
}

// ExportSessionStatsHandler handles GET /api/plugins/session-stats/export/{session_id}
func ExportSessionStatsHandler(store *SessionStatsStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sessionID := r.PathValue("session_id")

		stats, err := store.GetSessionStats(sessionID)
		if err != nil {
			http.Error(w, "Failed to get session stats", http.StatusInternalServerError)
			return
		}

		// Export as CSV
		w.Header().Set("Content-Type", "text/csv")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=session_%s_stats.csv", sessionID))

		csvData := fmt.Sprintf("session_id,message_count,total_input_tokens,total_output_tokens,agent_switches,tool_calls,start_time,end_time\n")
		csvData += fmt.Sprintf("%s,%d,%d,%d,%d,%d,%v,%v\n",
			stats.SessionID, stats.MessageCount, stats.TotalInputTokens,
			stats.TotalOutputTokens, stats.AgentSwitches, stats.ToolCalls,
			stats.StartTime, stats.EndTime)

		w.Write([]byte(csvData))
	}
}

// Event Handlers

// OnSessionStart handles session.start event
func OnSessionStart(sessionID string, store *SessionStatsStore) error {
	updates := map[string]interface{}{
		"start_time": time.Now(),
	}
	return store.UpdateSessionStats(sessionID, updates)
}

// OnSessionEnd handles session.end event
func OnSessionEnd(sessionID string, store *SessionStatsStore) error {
	updates := map[string]interface{}{
		"end_time": time.Now(),
	}
	return store.UpdateSessionStats(sessionID, updates)
}

// OnMessageReceived handles message.received event
func OnMessageReceived(sessionID string, role string, store *SessionStatsStore) error {
	updates := map[string]interface{}{
		"message_count": "message_count + 1",
	}

	// If it's a tool message, increment tool calls
	if role == "tool" {
		updates["tool_calls"] = "tool_calls + 1"
	}

	return store.UpdateSessionStats(sessionID, updates)
}