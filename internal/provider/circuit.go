package provider

import "sync"

// CircuitState represents the circuit breaker state.
type CircuitState int

const (
	// CircuitClosed means normal operation — requests are allowed.
	CircuitClosed CircuitState = iota
	// CircuitOpen means the breaker has tripped — requests should be paused.
	CircuitOpen
)

// CircuitBreaker tracks consecutive failures and trips when a threshold is reached.
// It is safe for concurrent use.
type CircuitBreaker struct {
	mu               sync.Mutex
	state            CircuitState
	consecutiveFails int
	threshold        int // trips after this many consecutive failures
}

// NewCircuitBreaker creates a circuit breaker that trips after `threshold` consecutive failures.
func NewCircuitBreaker(threshold int) *CircuitBreaker {
	if threshold <= 0 {
		threshold = 3
	}
	return &CircuitBreaker{
		state:     CircuitClosed,
		threshold: threshold,
	}
}

// RecordFailure records a failure. Returns true if the circuit just tripped open.
func (cb *CircuitBreaker) RecordFailure() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.consecutiveFails++
	if cb.consecutiveFails >= cb.threshold && cb.state == CircuitClosed {
		cb.state = CircuitOpen
		return true
	}
	return false
}

// RecordSuccess records a success, resetting the consecutive failure counter.
func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.consecutiveFails = 0
	// A success while open also closes the circuit.
	if cb.state == CircuitOpen {
		cb.state = CircuitClosed
	}
}

// Reset reopens the circuit (moves from open back to closed) and resets counters.
func (cb *CircuitBreaker) Reset() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.state = CircuitClosed
	cb.consecutiveFails = 0
}

// IsOpen returns true if the circuit breaker is in the open (tripped) state.
func (cb *CircuitBreaker) IsOpen() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	return cb.state == CircuitOpen
}

// State returns the current circuit state.
func (cb *CircuitBreaker) State() CircuitState {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	return cb.state
}
