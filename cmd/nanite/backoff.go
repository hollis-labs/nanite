package main

import "time"

// nextBackoffDelay doubles delay, capped at maxDelay — returning initial the
// first time (delay <= 0). Shared exponential-backoff arithmetic between
// nanite chat's auto-start health poll (CW-20260813-0007,
// pollHealthUntilReady) and its harness-v1 connection-establishment retry
// (CW-20260813-0008, retryConnect in harness_client.go) — the two loops'
// stop conditions differ (poll-until-healthy vs. bounded attempt count), but
// "grow the wait, cap it" is identical, so it's the one piece worth sharing
// rather than writing two backoff loops.
func nextBackoffDelay(delay, initial, maxDelay time.Duration) time.Duration {
	if delay <= 0 {
		return initial
	}
	delay *= 2
	if delay > maxDelay {
		delay = maxDelay
	}
	return delay
}
