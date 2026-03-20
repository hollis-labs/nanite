package trivia

import (
	"sort"
	"sync"
)

// ScoreEntry represents a single entry on the leaderboard.
type ScoreEntry struct {
	UserID string `json:"user_id"`
	Score  int    `json:"score"`
	Rank   int    `json:"rank"`
}

// ScoreTracker provides thread-safe in-memory score tracking for trivia.
type ScoreTracker struct {
	mu     sync.RWMutex
	scores map[string]int
}

// NewScoreTracker creates a new ScoreTracker instance.
func NewScoreTracker() *ScoreTracker {
	return &ScoreTracker{
		scores: make(map[string]int),
	}
}

// AddScore adds points for a user.
func (s *ScoreTracker) AddScore(userID string, points int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scores[userID] += points
}

// GetScore returns the current score for a user.
func (s *ScoreTracker) GetScore(userID string) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.scores[userID]
}

// GetLeaderboard returns all scores ranked by points descending.
func (s *ScoreTracker) GetLeaderboard() []ScoreEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entries := make([]ScoreEntry, 0, len(s.scores))
	for userID, score := range s.scores {
		entries = append(entries, ScoreEntry{
			UserID: userID,
			Score:  score,
		})
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Score > entries[j].Score
	})

	for i := range entries {
		entries[i].Rank = i + 1
	}

	return entries
}
