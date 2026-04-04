package coordination

import "time"

// Key prefix conventions for the coordination store.
const (
	// PrefixHeartbeat stores agent/worker heartbeat timestamps.
	// Key: agent:{id}:heartbeat -> JSON{session_id, timestamp}
	PrefixHeartbeat = "agent:"

	// PrefixLock stores resource locks held by sessions.
	// Key: lock:{resource}:{holder} -> JSON{session_id, acquired_at}
	PrefixLock = "lock:"

	// PrefixState stores shared inter-agent state.
	// Key: state:{key} -> arbitrary JSON
	PrefixState = "state:"

	// PrefixTask stores real-time task state.
	// Key: task:{id} -> Task JSON
	PrefixTask = "task:"

	// PrefixWorker stores worker status information.
	// Key: worker:{id}:status -> WorkerStatus JSON
	PrefixWorker = "worker:"
)

// Default TTLs for ephemeral keys.
const (
	HeartbeatTTL = 30 * time.Second
	LockTTL      = 60 * time.Second
	WorkerTTL    = 5 * time.Minute
)
