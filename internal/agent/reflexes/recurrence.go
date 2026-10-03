package reflexes

import (
	"time"

	shared "github.com/hollis-labs/go-reflexes"
	"github.com/hollis-labs/nanite/internal/store"
)

// DefaultReflexCooldown is the least-specific cooldown in the cascade.
const DefaultReflexCooldown = shared.DefaultReflexCooldown

// EffectiveCooldown resolves system, kind and reflex overrides. An explicit
// zero means no cooldown; nil inherits the next less-specific level.
func EffectiveCooldown(kindDefaultSeconds, reflexOverrideSeconds *int64) time.Duration {
	return shared.EffectiveCooldown(kindDefaultSeconds, reflexOverrideSeconds)
}

// RecentlyFired compares the host row's last firing to the cooldown window.
func RecentlyFired(r store.AgentReflex, now time.Time, window time.Duration) bool {
	return shared.RecentlyFired(shared.Reflex(r), now, window)
}
