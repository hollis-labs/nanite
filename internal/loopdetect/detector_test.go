package loopdetect

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"
)

// helper builds a Signal with minimal required fields.
func sig(sessionID, turnID, toolName string, args json.RawMessage) Signal {
	return Signal{
		SessionID: sessionID,
		TurnID:    turnID,
		ToolName:  toolName,
		Args:      args,
		Timestamp: time.Now(),
	}
}

func raw(s string) json.RawMessage { return json.RawMessage(s) }

// ─── Basic detection ─────────────────────────────────────────────────────────

func TestSingleRecord_NoDetection(t *testing.T) {
	d := New()
	_, detected := d.Record(sig("s1", "t1", "dev_bash", raw(`{"command":"ls"}`)))
	if detected {
		t.Fatal("single record must not trigger detection")
	}
}

func TestThreeIdentical_DetectsLoop(t *testing.T) {
	d := New()
	s := sig("s1", "t1", "dev_bash", raw(`{"command":"ls"}`))

	d.Record(s)
	d.Record(s)
	det, ok := d.Record(s)
	if !ok {
		t.Fatal("third identical call must trigger detection")
	}
	if det.ToolName != "dev_bash" {
		t.Errorf("want tool dev_bash, got %q", det.ToolName)
	}
	if det.Count != 3 {
		t.Errorf("want count=3, got %d", det.Count)
	}
	if det.Reason == "" {
		t.Error("reason must not be empty")
	}
	if det.DetectedAtTurn != "t1" {
		t.Errorf("want turn t1, got %q", det.DetectedAtTurn)
	}
}

func TestThreeDifferent_NoDetection(t *testing.T) {
	d := New()
	d.Record(sig("s1", "t1", "dev_bash", raw(`{"command":"ls"}`)))
	d.Record(sig("s1", "t1", "dev_bash", raw(`{"command":"pwd"}`)))
	_, ok := d.Record(sig("s1", "t1", "dev_bash", raw(`{"command":"echo hi"}`)))
	if ok {
		t.Fatal("three different fingerprints must not trigger detection")
	}
}

// ─── Suppression ─────────────────────────────────────────────────────────────

func TestSuppressionAfterDetection(t *testing.T) {
	d := New()
	s := sig("s1", "t1", "dev_bash", raw(`{"command":"ls"}`))

	d.Record(s)
	d.Record(s)
	_, ok1 := d.Record(s) // fires here
	if !ok1 {
		t.Fatal("should fire on 3rd")
	}

	// Additional identical calls should not re-fire while the fingerprint is
	// still in the window.
	for i := 0; i < 5; i++ {
		_, ok := d.Record(s)
		if ok {
			t.Fatalf("suppression should prevent re-fire (iteration %d)", i)
		}
	}
}

func TestDetectionRefires_AfterFingerprintExitsWindow(t *testing.T) {
	// Window size 5, threshold 3.
	d := New(WithWindowSize(5), WithThreshold(3))
	repeat := sig("s1", "t1", "dev_bash", raw(`{"command":"ls"}`))
	other := sig("s1", "t2", "dev_read", raw(`{"path":"/foo"}`))

	// Fire once.
	d.Record(repeat)
	d.Record(repeat)
	_, ok := d.Record(repeat) // fires
	if !ok {
		t.Fatal("should detect on 3rd")
	}

	// Push 5 different calls to flush the old fingerprint out of the window.
	for i := 0; i < 5; i++ {
		o := other
		o.Args = raw(fmt.Sprintf(`{"path":"/foo/%d"}`, i))
		d.Record(o)
	}

	// Now repeating the original fingerprint 3 times should fire again.
	d.Record(repeat)
	d.Record(repeat)
	_, ok2 := d.Record(repeat)
	if !ok2 {
		t.Fatal("should fire again after fingerprint aged out of window")
	}
}

// ─── Args normalization ───────────────────────────────────────────────────────

func TestArgsNormalization_KeyOrderIndependent(t *testing.T) {
	d := New()
	// Same args, different key order — should produce the same fingerprint.
	s1 := sig("s1", "t1", "tool", raw(`{"a":1,"b":2}`))
	s2 := sig("s1", "t2", "tool", raw(`{"b":2,"a":1}`))
	s3 := sig("s1", "t3", "tool", raw(`{"b":2,"a":1}`))

	d.Record(s1)
	d.Record(s2)
	det, ok := d.Record(s3)
	if !ok {
		t.Fatal("reordered args should produce same fingerprint → detect loop")
	}
	if det.Count != 3 {
		t.Errorf("want count=3, got %d", det.Count)
	}
}

func TestArgsNormalization_DifferentValues_NoFalsePositive(t *testing.T) {
	d := New()
	d.Record(sig("s1", "t1", "tool", raw(`{"a":1}`)))
	d.Record(sig("s1", "t2", "tool", raw(`{"a":2}`)))
	_, ok := d.Record(sig("s1", "t3", "tool", raw(`{"a":3}`)))
	if ok {
		t.Fatal("different arg values should not match")
	}
}

func TestArgsNormalization_NonJSON_TreatedOpaque(t *testing.T) {
	d := New()
	// Non-JSON args: same opaque string = same fingerprint.
	s := sig("s1", "t1", "tool", json.RawMessage("not json"))
	d.Record(s)
	d.Record(s)
	_, ok := d.Record(s)
	if !ok {
		t.Fatal("identical non-JSON args should still produce same fingerprint")
	}
}

// ─── Session isolation ────────────────────────────────────────────────────────

func TestSessionIsolation(t *testing.T) {
	d := New()
	s := sig("sessionA", "t1", "dev_bash", raw(`{"command":"ls"}`))

	for i := 0; i < 10; i++ {
		s.SessionID = "sessionA"
		d.Record(s)
	}

	// sessionB starts fresh — should need 3 calls to detect.
	s.SessionID = "sessionB"
	d.Record(s)
	d.Record(s)
	_, ok := d.Record(s)
	if !ok {
		t.Fatal("sessionB should detect independently of sessionA")
	}
}

func TestDetector_EvictsOldestSessionAtConfiguredCap(t *testing.T) {
	d := New(WithMaxSessions(2))
	repeated := sig("s1", "t1", "dev_bash", raw(`{"command":"ls"}`))
	d.Record(repeated)
	d.Record(repeated)
	d.Record(sig("s2", "t1", "dev_read", raw(`{"path":"/two"}`)))
	d.Record(sig("s3", "t1", "dev_read", raw(`{"path":"/three"}`)))

	if len(d.windows) != 2 {
		t.Fatalf("retained windows = %d, want cap 2", len(d.windows))
	}
	if _, ok := d.windows["s1"]; ok {
		t.Fatal("oldest session s1 was not evicted")
	}
	if _, ok := d.windows["s2"]; !ok {
		t.Fatal("newer session s2 was unexpectedly evicted")
	}
	if _, ok := d.windows["s3"]; !ok {
		t.Fatal("newest session s3 was unexpectedly evicted")
	}

	// Recreating s1 starts with a fresh window; its two pre-eviction records
	// must not contribute to a detection.
	if _, detected := d.Record(repeated); detected {
		t.Fatal("recreated session inherited evicted detection state")
	}
	if _, detected := d.Record(repeated); detected {
		t.Fatal("recreated session detected before three fresh records")
	}
	if _, detected := d.Record(repeated); !detected {
		t.Fatal("recreated session did not detect after three fresh records")
	}
}

func TestDetector_ResetRemovesSessionFromFIFO(t *testing.T) {
	d := New(WithMaxSessions(2))
	d.Record(sig("s1", "t1", "tool", raw(`{}`)))
	d.Record(sig("s2", "t1", "tool", raw(`{}`)))
	d.Reset("s1")
	d.Record(sig("s3", "t1", "tool", raw(`{}`)))

	if _, ok := d.windows["s2"]; !ok {
		t.Fatal("reset left a stale FIFO entry that evicted live session s2")
	}
	if _, ok := d.windows["s3"]; !ok {
		t.Fatal("new session s3 missing after reset")
	}
}

// ─── Reset ───────────────────────────────────────────────────────────────────

func TestReset_ClearsWindow(t *testing.T) {
	d := New()
	s := sig("s1", "t1", "dev_bash", raw(`{"command":"ls"}`))

	d.Record(s)
	d.Record(s)
	d.Reset("s1")

	// After reset, 2 more calls should not detect (need 3 fresh ones).
	d.Record(s)
	d.Record(s)
	_, ok := d.Record(s)
	// This third call WILL detect — which is correct; reset just cleared the
	// window, not the threshold. Verify detection fires cleanly post-reset.
	if !ok {
		t.Fatal("should detect after 3 fresh calls post-reset")
	}
}

// ─── Callback ────────────────────────────────────────────────────────────────

func TestCallback_CalledOnDetection(t *testing.T) {
	var got Detection
	d := New(WithCallback(func(det Detection) {
		got = det
	}))
	s := sig("s1", "t1", "tool", raw(`{}`))
	d.Record(s)
	d.Record(s)
	d.Record(s)

	if got.ToolName != "tool" {
		t.Errorf("callback not called or wrong tool: %+v", got)
	}
}

// ─── Concurrency ─────────────────────────────────────────────────────────────

func TestConcurrentRecord_RaceSafe(t *testing.T) {
	d := New()
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			sessionID := fmt.Sprintf("s%d", n%5) // 5 concurrent sessions
			s := sig(sessionID, "t1", "dev_bash", raw(`{"command":"ls"}`))
			d.Record(s)
		}(i)
	}
	wg.Wait()
	// No race = success. The -race flag will catch any issues.
}

// ─── normalizeArgs unit tests ─────────────────────────────────────────────────

func TestNormalizeArgs_EmptyInput(t *testing.T) {
	got := normalizeArgs(nil)
	if got != "" {
		t.Errorf("want empty string, got %q", got)
	}
}

func TestNormalizeArgs_SimpleObject(t *testing.T) {
	got := normalizeArgs(raw(`{"z":3,"a":1,"m":2}`))
	want := `{"a":1,"m":2,"z":3}`
	if got != want {
		t.Errorf("want %q, got %q", want, got)
	}
}

func TestNormalizeArgs_Array(t *testing.T) {
	// Arrays are not re-sorted (opaque fallback).
	got := normalizeArgs(raw(`[1,2,3]`))
	if got != `[1,2,3]` {
		t.Errorf("array should be returned as-is, got %q", got)
	}
}

// ─── compute fingerprint consistency ─────────────────────────────────────────

func TestCompute_SameInputSameFingerprint(t *testing.T) {
	fp1 := compute("tool", raw(`{"b":2,"a":1}`))
	fp2 := compute("tool", raw(`{"a":1,"b":2}`))
	if fp1 != fp2 {
		t.Errorf("reordered keys should produce same fingerprint: %q vs %q", fp1, fp2)
	}
}

func TestCompute_DifferentTools_DifferentFingerprint(t *testing.T) {
	fp1 := compute("toolA", raw(`{"x":1}`))
	fp2 := compute("toolB", raw(`{"x":1}`))
	if fp1 == fp2 {
		t.Error("different tool names must produce different fingerprints")
	}
}
