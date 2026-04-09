package store

import "fmt"

// GetSessionOverrides returns the JSON overrides string for the given session.
// Returns "{}" if no row exists for the session.
func (s *Store) GetSessionOverrides(sessionID string) (string, error) {
	var overrides string
	err := s.DB.QueryRow(
		`SELECT overrides FROM session_agent_overrides WHERE session_id = ?`,
		sessionID,
	).Scan(&overrides)
	if err != nil {
		// sql.ErrNoRows — return the default empty object
		return "{}", nil
	}
	return overrides, nil
}

// SetSessionOverrides upserts the JSON overrides string for the given session.
func (s *Store) SetSessionOverrides(sessionID string, overridesJSON string) error {
	_, err := s.DB.Exec(
		`INSERT OR REPLACE INTO session_agent_overrides (session_id, overrides, updated_at)
		 VALUES (?, ?, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))`,
		sessionID, overridesJSON,
	)
	if err != nil {
		return fmt.Errorf("set session overrides: %w", err)
	}
	return nil
}
