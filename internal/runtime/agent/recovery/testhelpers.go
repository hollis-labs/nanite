package recovery

import "context"

// RegisterActiveRetryForTest seeds an in-flight retry token on the
// broker for tests that exercise Cancel without driving the full
// classify→remediate→dispatch orchestration. Production callers must
// not reach for this — the orchestration loop owns token lifecycle.
//
// Exported so cross-package tests (e.g. internal/api/recovery_test.go)
// can prepare broker state. The comparable internal helper
// (registerActiveRetry) is package-private because production
// orchestration lives inside this package.
func (b *Broker) RegisterActiveRetryForTest(sessionID, token string, cancel context.CancelFunc) {
	b.registerActiveRetry(sessionID, token, cancel)
}
