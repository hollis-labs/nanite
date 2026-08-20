package reflexes

// TASKS/reflex-taxonomy/02-recurrence-cascade.md — Facet 4 (Recurrence),
// docs/engineering/architecture/10-reflex-action-taxonomy.md. Cascading,
// least-to-most-specific: system default -> per-action-kind override ->
// per-reflex override. The system-level default stays a Go constant (not a
// DB row); the kind- and reflex-level overrides are data
// (reflex_action_kinds.default_recurrence_seconds,
// agent_reflexes.recurrence_override_seconds — both added by migration
// 124_reflex_action_taxonomy.sql / TASKS/reflex-taxonomy/
// 01-taxonomy-schema-foundation.md).

import (
	"time"

	"github.com/hollis-labs/nanite/internal/store"
)

// DefaultReflexCooldown is the system-level default cooldown a fired
// reflex must observe before it is eligible to fire again — the
// least-specific level of the Facet 4 cascade. Named here as the single
// source of truth for what was previously a bare 15*time.Minute literal
// inline in EvaluateState's loop.
const DefaultReflexCooldown = 15 * time.Minute

// EffectiveCooldown resolves the Facet 4 cascade for one reflex:
// reflex-level override (reflexOverrideSeconds) wins if set; else the
// action kind's default (kindDefaultSeconds) wins if set; else
// DefaultReflexCooldown.
//
// "Set" means non-nil, not non-zero: a pointer to 0 is an explicit
// "no cooldown" override at that level (time.Duration(0), meaning the
// reflex may re-fire on the very next eligible tick) and must NOT be
// treated the same as an absent (nil) override, which falls through to
// the next, less-specific level of the cascade instead.
func EffectiveCooldown(kindDefaultSeconds, reflexOverrideSeconds *int64) time.Duration {
	if reflexOverrideSeconds != nil {
		return time.Duration(*reflexOverrideSeconds) * time.Second
	}
	if kindDefaultSeconds != nil {
		return time.Duration(*kindDefaultSeconds) * time.Second
	}
	return DefaultReflexCooldown
}

// RecentlyFired reports whether reflex r fired within the last window
// (relative to now), per its stored LastFiredAt. A non-positive window
// (the EffectiveCooldown(...) result for an explicit "no cooldown"
// override, or any kind/reflex resolving to zero) always returns false —
// zero cooldown means "no suppression," not "always suppressed."
//
// This is the one shared time-window comparison every recurrence-aware
// call site uses: Engine.EvaluateState's generic per-turn pass (below),
// and — via this exported form — internal/service/chat_reflex_dispatch.go's
// attemptReflexDispatch and internal/mcp/self_tools_dispatch.go's
// matchDispatchToAgentReflex, so the debounce-suppression check is
// implemented exactly once, not reimplemented a third time at either
// dispatch_to_agent call site.
func RecentlyFired(r store.AgentReflex, now time.Time, window time.Duration) bool {
	if r.LastFiredAt == "" || window <= 0 {
		return false
	}
	ts, err := time.Parse(time.RFC3339, r.LastFiredAt)
	if err != nil {
		return false
	}
	return now.Sub(ts) >= 0 && now.Sub(ts) < window
}
