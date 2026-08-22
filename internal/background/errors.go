package background

import "errors"

// ErrUnknownJob is returned by Backend.Status / Service.Status when
// the queried job id was never issued by this service instance.
// Idempotent Cancel returns nil for unknown ids.
var ErrUnknownJob = errors.New("background: unknown job id")

// ErrExpiredJob is returned by Service.Status / Service.Result when a
// job id was issued by this service instance but its terminal record has
// been evicted by the completed-job retention policy. Callers can use
// errors.Is to distinguish expiry from a job id that was never known.
var ErrExpiredJob = errors.New("background: job result expired")

// ErrPatternNotBackground is returned by Service.Submit when the
// caller passes a classify.ExecutionPattern other than PatternBackground.
// The P3 ScopeTier classifier is the gate — non-background patterns
// must route to peer_query, spawn_subagent, or inline dispatch.
var ErrPatternNotBackground = errors.New(
	"background: ExecutionPattern must be PatternBackground")

// ErrNoBackend is returned when Service was constructed without a
// Backend. Indicates a wiring bug; production callers always wire one.
var ErrNoBackend = errors.New("background: no backend configured")

// ErrNoMessenger is returned by Submit when no messenger is wired.
// The completion envelope path is mandatory — a backgrounded job
// the caller never hears back about is worse than a sync failure.
var ErrNoMessenger = errors.New("background: no messenger configured")
