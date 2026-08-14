package subagent

import (
	"context"
	"strconv"
	"testing"
	"time"
)

// TestHeartbeatIntervalInRange pins the validation window
// [minHeartbeatSeconds, maxHeartbeatSeconds] used by the env-var
// resolver. 0 is handled separately by resolveHeartbeatInterval as
// "disabled", not validated here.
func TestHeartbeatIntervalInRange(t *testing.T) {
	cases := []struct {
		secs int
		want bool
	}{
		{-1, false},
		{0, false},
		{minHeartbeatSeconds, true},
		{30, true}, // DefaultHeartbeatSeconds
		{maxHeartbeatSeconds, true},
		{maxHeartbeatSeconds + 1, false},
		{999999, false},
	}
	for _, c := range cases {
		if got := heartbeatIntervalInRange(c.secs); got != c.want {
			t.Errorf("heartbeatIntervalInRange(%d) = %v, want %v", c.secs, got, c.want)
		}
	}
}

// TestResolveHeartbeatInterval_EnvUnset confirms the compiled-in
// DefaultHeartbeatSeconds cadence is used when the operator override
// env var is absent.
func TestResolveHeartbeatInterval_EnvUnset(t *testing.T) {
	t.Setenv(heartbeatSecondsEnvVar, "")
	if got, want := resolveHeartbeatInterval(), DefaultHeartbeatSeconds*time.Second; got != want {
		t.Fatalf("resolveHeartbeatInterval() with env unset = %v, want %v", got, want)
	}
}

// TestResolveHeartbeatInterval_EnvZeroDisables confirms an operator can
// turn heartbeats off entirely with an explicit "0" — distinct from an
// invalid/out-of-range value, which falls back to the default rather
// than disabling.
func TestResolveHeartbeatInterval_EnvZeroDisables(t *testing.T) {
	t.Setenv(heartbeatSecondsEnvVar, "0")
	if got := resolveHeartbeatInterval(); got != 0 {
		t.Fatalf("resolveHeartbeatInterval() with env=0 = %v, want 0 (disabled)", got)
	}
}

// TestResolveHeartbeatInterval_EnvInRange confirms an operator can
// retune the cadence via the env var without recompiling.
func TestResolveHeartbeatInterval_EnvInRange(t *testing.T) {
	for _, secs := range []int{minHeartbeatSeconds, 10, 60, maxHeartbeatSeconds} {
		t.Run(strconv.Itoa(secs), func(t *testing.T) {
			t.Setenv(heartbeatSecondsEnvVar, strconv.Itoa(secs))
			if got, want := resolveHeartbeatInterval(), time.Duration(secs)*time.Second; got != want {
				t.Fatalf("resolveHeartbeatInterval() = %v, want %v (env override)", got, want)
			}
		})
	}
}

// TestResolveHeartbeatInterval_EnvOutOfRange confirms a typo'd env value
// (too large, non-integer, or negative) is ignored and the cadence falls
// back to DefaultHeartbeatSeconds rather than being silently disabled or
// set to an unsafe value. Only "0" (tested above) disables.
func TestResolveHeartbeatInterval_EnvOutOfRange(t *testing.T) {
	for _, raw := range []string{"-1", "301", "999999", "abc", "30s", "1.5"} {
		t.Run(raw, func(t *testing.T) {
			t.Setenv(heartbeatSecondsEnvVar, raw)
			if got, want := resolveHeartbeatInterval(), DefaultHeartbeatSeconds*time.Second; got != want {
				t.Fatalf("resolveHeartbeatInterval() with bad env %q = %v, want %v (fallback)",
					raw, got, want)
			}
		})
	}
}

// TestExecute_EmitsHeartbeatsWhileRunnerInFlight is the core behavioral
// test for CW-20260519-0068: while a subagent's runner call is blocked
// (the exact "14 minutes, zero output" pathology from the ticket's
// evidence), the parent's stream sink should keep receiving periodic
// "still running" pings, not just the initial dispatch event and the
// eventual terminal event.
func TestExecute_EmitsHeartbeatsWhileRunnerInFlight(t *testing.T) {
	t.Setenv(heartbeatSecondsEnvVar, "1")

	db, _ := newTestDB(t)
	sink := &recordingSink{}
	gate := make(chan struct{})
	runner := gateRunner{release: gate}

	svc := NewService(db, runner, &stubPoster{}, nil, stubSettings{})
	svc.SetStreamSink(sink)

	doneCh := make(chan struct{})
	go func() {
		defer close(doneCh)
		if _, err := svc.Spawn(context.Background(), SpawnRequest{
			ParentSessionID: "sess-1",
			ParentAgentID:   "file-backend",
			Role:            "file-summarizer",
			Prompt:          "p",
			Mode:            ModeSync,
		}); err != nil {
			t.Errorf("Spawn: %v", err)
		}
	}()

	// Wait for at least 2 heartbeat ticks (~2s at the 1s test cadence)
	// while the runner remains blocked on the gate.
	deadline := time.Now().Add(5 * time.Second)
	var heartbeats int
	for time.Now().Before(deadline) {
		heartbeats = 0
		for _, e := range sink.snapshot() {
			if hb, _ := e.Payload["heartbeat"].(bool); hb {
				heartbeats++
			}
		}
		if heartbeats >= 2 {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if heartbeats < 2 {
		t.Fatalf("expected >= 2 heartbeat events while runner in flight, got %d (events: %+v)",
			heartbeats, sink.snapshot())
	}

	// Inspect a heartbeat payload's shape before releasing the gate.
	for _, e := range sink.snapshot() {
		if hb, _ := e.Payload["heartbeat"].(bool); !hb {
			continue
		}
		if got := e.Payload["status"]; got != "running" {
			t.Errorf("heartbeat status = %v, want running", got)
		}
		if e.Payload["run_id"] == nil || e.Payload["run_id"] == "" {
			t.Error("heartbeat run_id missing")
		}
		if got := e.Payload["role"]; got != "file-summarizer" {
			t.Errorf("heartbeat role = %v, want file-summarizer", got)
		}
		break
	}

	close(gate) // let the runner finish
	<-doneCh

	// A terminal event must still follow the heartbeats.
	events := sink.snapshot()
	terminal := events[len(events)-1]
	if got := terminal.Payload["status"]; got != "completed" {
		t.Errorf("final event status = %v, want completed", got)
	}
	if hb, _ := terminal.Payload["heartbeat"].(bool); hb {
		t.Error("final event should be the terminal emit, not a heartbeat")
	}
}

// TestExecute_NoHeartbeatsWhenDisabled confirms
// NANITE_SUBAGENT_HEARTBEAT_SECONDS=0 fully disables the ticker: a run
// still emits exactly the dispatch + terminal events (G-5 baseline
// behavior), with no heartbeat pings in between.
func TestExecute_NoHeartbeatsWhenDisabled(t *testing.T) {
	t.Setenv(heartbeatSecondsEnvVar, "0")

	db, _ := newTestDB(t)
	sink := &recordingSink{}
	svc := NewService(db, EchoRunner{}, &stubPoster{}, nil, stubSettings{})
	svc.SetStreamSink(sink)

	_, err := svc.Spawn(context.Background(), SpawnRequest{
		ParentSessionID: "sess-1",
		ParentAgentID:   "file-backend",
		Role:            "file-summarizer",
		Prompt:          "ok",
		Mode:            ModeSync,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	for _, e := range sink.snapshot() {
		if hb, _ := e.Payload["heartbeat"].(bool); hb {
			t.Errorf("unexpected heartbeat event with heartbeats disabled: %+v", e.Payload)
		}
	}
}
