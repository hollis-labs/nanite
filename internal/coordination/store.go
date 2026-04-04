package coordination

import (
	"errors"
	"time"
)

// ErrNotFound is returned when a key does not exist.
var ErrNotFound = errors.New("key not found")

// KVEntry represents a single key-value pair with optional expiry.
type KVEntry struct {
	Key       string    `json:"key"`
	Value     []byte    `json:"value"`
	ExpiresAt time.Time `json:"expires_at,omitempty"`
}

// CoordStore abstracts the coordination KV layer used for ephemeral,
// high-write-concurrency state (heartbeats, locks, shared inter-agent state).
type CoordStore interface {
	// Put stores a key-value pair with an optional TTL. Pass 0 for no expiry.
	Put(key string, value []byte, ttl time.Duration) error

	// Get retrieves the value for a key. Returns ErrNotFound if the key
	// does not exist or has expired.
	Get(key string) ([]byte, error)

	// Delete removes a key. No error if the key does not exist.
	Delete(key string) error

	// List returns all entries matching the given key prefix.
	List(prefix string) ([]KVEntry, error)

	// Available returns true if the store is operational. Returns false
	// for the noop fallback.
	Available() bool

	// Close releases all resources held by the store.
	Close() error
}
