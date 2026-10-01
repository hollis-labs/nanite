package broker

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeBootDir captures Repopulate / RegenerateCLAUDEMD calls and lets
// individual tests inject errors per call.
type fakeBootDir struct {
	repopulateCount int32
	regenCount      int32
	repopulateErr   error
	regenErr        error
	wantSessionID   string
	t               *testing.T
}

func (f *fakeBootDir) Repopulate(_ context.Context, sessionID string) error {
	atomic.AddInt32(&f.repopulateCount, 1)
	if f.t != nil && f.wantSessionID != "" && sessionID != f.wantSessionID {
		f.t.Errorf("Repopulate sessionID = %q, want %q", sessionID, f.wantSessionID)
	}
	return f.repopulateErr
}

func (f *fakeBootDir) RegenerateCLAUDEMD(_ context.Context, sessionID string) error {
	atomic.AddInt32(&f.regenCount, 1)
	if f.t != nil && f.wantSessionID != "" && sessionID != f.wantSessionID {
		f.t.Errorf("RegenerateCLAUDEMD sessionID = %q, want %q", sessionID, f.wantSessionID)
	}
	return f.regenErr
}

type fakeMCP struct {
	count int32
	err   error
}

func (f *fakeMCP) RestartTransport(_ context.Context, _ string) error {
	atomic.AddInt32(&f.count, 1)
	return f.err
}

type fakeCreds struct {
	count       int32
	err         error
	wantProfile string
	t           *testing.T
}

func (f *fakeCreds) Refresh(_ context.Context, profile string) error {
	atomic.AddInt32(&f.count, 1)
	if f.t != nil && f.wantProfile != "" && profile != f.wantProfile {
		f.t.Errorf("Refresh profile = %q, want %q", profile, f.wantProfile)
	}
	return f.err
}

// TestRemediateRoutesByRemediation pins each Remediation enum value to
// the dependency call it dispatches into. Verifies the SessionID +
// AgentProfile pass-through.
func TestRemediateRoutesByRemediation(t *testing.T) {
	bd := &fakeBootDir{wantSessionID: "sess-123", t: t}
	mcp := &fakeMCP{}
	creds := &fakeCreds{wantProfile: "claude-sonnet", t: t}

	b := NewBroker(Dependencies{
		BootDir:     bd,
		MCP:         mcp,
		Credentials: creds,
	})

	ev := &FailureEvent{SessionID: "sess-123", AgentProfile: "claude-sonnet"}

	cases := []struct {
		name        string
		remediation Remediation
		wantBootRep int32
		wantBootGen int32
		wantMCP     int32
		wantCreds   int32
	}{
		{"repopulate sandbox", RemediationRepopulateSandbox, 1, 0, 0, 0},
		{"refresh mcp transport", RemediationRefreshMCPTransport, 0, 0, 1, 0},
		{"refresh credentials", RemediationRefreshCredentials, 0, 0, 0, 1},
		{"regenerate claudemd", RemediationRegenerateCLAUDEMD, 0, 1, 0, 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Reset counters between cases by reading current values.
			startRep := atomic.LoadInt32(&bd.repopulateCount)
			startGen := atomic.LoadInt32(&bd.regenCount)
			startMCP := atomic.LoadInt32(&mcp.count)
			startCreds := atomic.LoadInt32(&creds.count)

			err := b.Remediate(context.Background(), ev, Classification{Remediation: tc.remediation})
			if err != nil {
				t.Fatalf("Remediate returned err: %v", err)
			}

			gotRep := atomic.LoadInt32(&bd.repopulateCount) - startRep
			gotGen := atomic.LoadInt32(&bd.regenCount) - startGen
			gotMCP := atomic.LoadInt32(&mcp.count) - startMCP
			gotCreds := atomic.LoadInt32(&creds.count) - startCreds

			if gotRep != tc.wantBootRep {
				t.Errorf("Repopulate calls: got %d, want %d", gotRep, tc.wantBootRep)
			}
			if gotGen != tc.wantBootGen {
				t.Errorf("RegenerateCLAUDEMD calls: got %d, want %d", gotGen, tc.wantBootGen)
			}
			if gotMCP != tc.wantMCP {
				t.Errorf("MCP RestartTransport calls: got %d, want %d", gotMCP, tc.wantMCP)
			}
			if gotCreds != tc.wantCreds {
				t.Errorf("Credentials Refresh calls: got %d, want %d", gotCreds, tc.wantCreds)
			}
		})
	}
}

// TestRemediateNoneIsNoOp verifies RemediationNone returns nil without
// touching any dependency. ClassTransient and ClassPermanent both
// route through RemediationNone.
func TestRemediateNoneIsNoOp(t *testing.T) {
	bd := &fakeBootDir{}
	b := NewBroker(Dependencies{BootDir: bd})

	ev := &FailureEvent{SessionID: "sess-x"}
	if err := b.Remediate(context.Background(), ev, Classification{Remediation: RemediationNone}); err != nil {
		t.Fatalf("RemediationNone should be no-op, got err: %v", err)
	}
	if atomic.LoadInt32(&bd.repopulateCount) != 0 {
		t.Errorf("BootDir touched on RemediationNone")
	}
}

// TestRemediateMissingDepsReturnsError verifies the broker fails fast
// (rather than panicking) when a remediation is requested but the
// associated dependency is nil.
func TestRemediateMissingDepsReturnsError(t *testing.T) {
	b := NewBroker(Dependencies{}) // all nil

	cases := []struct {
		name        string
		remediation Remediation
		wantSubstr  string
	}{
		{"missing bootdir for repopulate", RemediationRepopulateSandbox, "BootDir not wired"},
		{"missing bootdir for regen", RemediationRegenerateCLAUDEMD, "BootDir not wired"},
		{"missing mcp", RemediationRefreshMCPTransport, "MCP not wired"},
		{"missing creds", RemediationRefreshCredentials, "Credentials not wired"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := b.Remediate(context.Background(), &FailureEvent{SessionID: "s"}, Classification{Remediation: tc.remediation})
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantSubstr) {
				t.Errorf("error = %q, want substring %q", err.Error(), tc.wantSubstr)
			}
		})
	}
}

// TestRemediatePropagatesError verifies dependency errors bubble back
// up through Remediate so the broker orchestration (Phase 6) can react.
func TestRemediatePropagatesError(t *testing.T) {
	wantErr := errors.New("boot dir disk full")
	bd := &fakeBootDir{repopulateErr: wantErr}
	b := NewBroker(Dependencies{BootDir: bd})

	err := b.Remediate(context.Background(), &FailureEvent{SessionID: "s"}, Classification{Remediation: RemediationRepopulateSandbox})
	if !errors.Is(err, wantErr) {
		t.Errorf("error = %v, want wrapping %v", err, wantErr)
	}
}

// TestRemediateNilEventReturnsError — defensive guard against caller bugs.
func TestRemediateNilEventReturnsError(t *testing.T) {
	b := NewBroker(Dependencies{BootDir: &fakeBootDir{}})
	err := b.Remediate(context.Background(), nil, Classification{Remediation: RemediationRepopulateSandbox})
	if err == nil {
		t.Fatal("expected error for nil event, got nil")
	}
	if !strings.Contains(err.Error(), "nil failure event") {
		t.Errorf("error = %q, want substring %q", err.Error(), "nil failure event")
	}
}

// TestRemediateUnknownRemediation — defensive guard against enum drift.
func TestRemediateUnknownRemediation(t *testing.T) {
	b := NewBroker(Dependencies{BootDir: &fakeBootDir{}})
	err := b.Remediate(context.Background(), &FailureEvent{SessionID: "s"}, Classification{Remediation: Remediation(999)})
	if err == nil {
		t.Fatal("expected error for unknown remediation, got nil")
	}
	if !strings.Contains(err.Error(), "unknown remediation") {
		t.Errorf("error = %q, want substring %q", err.Error(), "unknown remediation")
	}
}

// TestRemediateRespectsTimeout — the per-remediation timeout (default
// 10s, configurable via WithRemediationTimeout) bounds the dependency
// call. The fake reads the deadline on the context it is handed, then
// blocks until ctx.Done(). Remediate sets that deadline when it calls
// context.WithTimeout, before the fake runs, so the time remaining at the
// fake's entry can never exceed the configured timeout, however slowly
// the box schedules. With the 10s default it would be about 10s, and with
// no bound there would be no deadline at all. This used to assert a 200ms
// wall-clock elapsed, which measured scheduling rather than the bound and
// failed at 226ms under load (CW-20261001-0146).
func TestRemediateRespectsTimeout(t *testing.T) {
	const timeout = 50 * time.Millisecond
	bd := &blockingBootDir{}
	b := NewBroker(Dependencies{BootDir: bd}, WithRemediationTimeout(timeout))

	err := b.Remediate(context.Background(), &FailureEvent{SessionID: "s"}, Classification{Remediation: RemediationRepopulateSandbox})

	if !bd.hadDeadline {
		t.Fatal("dependency call had no deadline — the remediation timeout was not applied")
	}
	if bd.remaining > timeout {
		t.Errorf("deadline was %v away at the call, want <= the configured %v", bd.remaining, timeout)
	}
	if err == nil {
		t.Fatal("expected ctx-deadline error, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("error = %v, want context.DeadlineExceeded", err)
	}
}

// blockingBootDir records the deadline Repopulate is handed, then blocks
// until ctx is canceled and returns ctx.Err(). Lets the timeout test
// exercise the bounded context.WithTimeout path inside Remediate. With no
// deadline it returns at once rather than block the test forever.
type blockingBootDir struct {
	hadDeadline bool
	remaining   time.Duration
}

func (b *blockingBootDir) Repopulate(ctx context.Context, _ string) error {
	deadline, ok := ctx.Deadline()
	b.hadDeadline = ok
	if !ok {
		return errors.New("blockingBootDir: no deadline")
	}
	b.remaining = time.Until(deadline)
	<-ctx.Done()
	return ctx.Err()
}
func (*blockingBootDir) RegenerateCLAUDEMD(_ context.Context, _ string) error { return nil }
