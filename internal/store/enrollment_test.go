package store

import (
	"context"
	"errors"
	"strings"
	"testing"
)

const testURN = "msg://agent/agent-mux/agt_testenroll"

func enrollmentCounts(t *testing.T, s *Store) (bindings, instances, events, audits int) {
	t.Helper()
	return authorityRowCount(t, s, "agent_actor_bindings"), authorityRowCount(t, s, "actor_instances"),
		authorityRowCount(t, s, "actor_instance_events"),
		func() int {
			var n int
			_ = s.DB.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM event_log WHERE event_type LIKE 'actor_%'`).Scan(&n)
			return n
		}()
}

func TestEnrollmentWriterRefusesWithoutPrincipalAndForBadHosts(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	host := nativeHost(t, s, "enroll-refusals")
	w := s.ActorEnrollment()
	if _, err := w.Begin(ctx, "  ", host.ID, "nanite/i/"+host.ID, "r"); !errors.Is(err, ErrEnrollmentPrincipal) {
		t.Fatalf("no principal: %v", err)
	}
	if _, err := w.Begin(ctx, "cred", "no-such-host", "nanite/i/x", "r"); err == nil {
		t.Fatal("unknown host accepted")
	}
	if _, err := w.Complete(ctx, "cred", host.ID, testURN); !errors.Is(err, ErrEnrollmentNotFound) {
		t.Fatalf("complete without a record: %v", err)
	}
	rec, err := w.Begin(ctx, "cred", host.ID, "nanite/i/"+host.ID, "r")
	if err != nil || rec.Status != EnrollmentEnrolling {
		t.Fatalf("begin: %+v %v", rec, err)
	}
	for _, bad := range []string{"", "msg://agent/", "msg://agent/nanite/x"[:3], "https://example.test/x"} {
		if _, err = w.Complete(ctx, "cred", host.ID, bad); !errors.Is(err, ErrEnrollmentURN) {
			t.Fatalf("urn %q: %v", bad, err)
		}
	}
	if _, err = w.Complete(ctx, "", host.ID, testURN); !errors.Is(err, ErrEnrollmentPrincipal) {
		t.Fatalf("complete no principal: %v", err)
	}
	if b, i, e, a := enrollmentCounts(t, s); b+i+e+a != 0 {
		t.Fatalf("refusals wrote rows: %d %d %d %d", b, i, e, a)
	}
	disabled := nativeHost(t, s, "enroll-disabled")
	if _, err = s.DB.ExecContext(ctx, `UPDATE agent_host_settings SET enabled = 0 WHERE id = ?`, disabled.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = w.Begin(ctx, "cred", disabled.ID, "nanite/i/"+disabled.ID, "r"); !errors.Is(err, ErrEnrollmentHostDisabled) {
		t.Fatalf("disabled host: %v", err)
	}
}

func TestEnrollmentCompleteWritesBindingInstanceAuditInOneTransaction(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	host := nativeHost(t, s, "enroll-happy")
	w := s.ActorEnrollment()
	key, err := s.EnrollmentKey(ctx, host.ID)
	if err != nil || !strings.HasPrefix(key, "nanite/") || !strings.HasSuffix(key, "/"+host.ID) {
		t.Fatalf("key = %q %v", key, err)
	}
	if _, err = w.Begin(ctx, "cred-1", host.ID, key, "because"); err != nil {
		t.Fatal(err)
	}
	rec, err := w.Complete(ctx, "cred-1", host.ID, testURN)
	if err != nil || rec.Status != EnrollmentEnrolled || rec.URN != testURN || rec.InstanceID == "" {
		t.Fatalf("complete: %+v %v", rec, err)
	}
	inst, err := s.GetDurableAgentInstanceByURN(ctx, testURN)
	if err != nil || inst.Status != DurableAgentStatusSleeping || inst.CurrentSessionID != "" || inst.ProfileID != testURN || inst.Slug != host.Slug {
		t.Fatalf("instance: %+v %v", inst, err)
	}
	var receipt string
	var enabled int
	if err = s.DB.QueryRowContext(ctx, `SELECT binding_receipt, enabled FROM agent_actor_bindings WHERE actor_uri = ?`, testURN).Scan(&receipt, &enabled); err != nil || enabled != 1 {
		t.Fatalf("binding: %v %d", err, enabled)
	}
	if !strings.Contains(receipt, `"granted_by":"cred-1"`) || !strings.Contains(receipt, EnrollmentAuthority) {
		t.Fatalf("receipt = %s", receipt)
	}
	if b, i, e, a := enrollmentCounts(t, s); b != 1 || i != 1 || e != 1 || a != 1 {
		t.Fatalf("rows: %d %d %d %d", b, i, e, a)
	}
	// Idempotent: same URN is a no-op, a different one is refused.
	again, err := w.Complete(ctx, "cred-2", host.ID, testURN)
	if err != nil || again.InstanceID != rec.InstanceID {
		t.Fatalf("repeat: %+v %v", again, err)
	}
	if _, err = w.Complete(ctx, "cred-2", host.ID, "msg://agent/agent-mux/agt_other"); !errors.Is(err, ErrEnrollmentURNMismatch) {
		t.Fatalf("different urn: %v", err)
	}
	if b, i, _, _ := enrollmentCounts(t, s); b != 1 || i != 1 {
		t.Fatalf("repeat wrote rows: %d %d", b, i)
	}
}

func TestEnrollmentCompleteRollsBackEverythingOnFailure(t *testing.T) {
	for name, failOn := range map[string]string{
		"instance insert": `CREATE TRIGGER inject_fail BEFORE INSERT ON actor_instances BEGIN SELECT RAISE(ABORT,'injected'); END`,
		"event insert":    `CREATE TRIGGER inject_fail BEFORE INSERT ON actor_instance_events BEGIN SELECT RAISE(ABORT,'injected'); END`,
		"audit insert":    `CREATE TRIGGER inject_fail BEFORE INSERT ON event_log WHEN NEW.event_type = 'actor_enrolled' BEGIN SELECT RAISE(ABORT,'injected'); END`,
		"record update":   `CREATE TRIGGER inject_fail BEFORE UPDATE ON agent_enrollments BEGIN SELECT RAISE(ABORT,'injected'); END`,
	} {
		t.Run(name, func(t *testing.T) {
			s := newTestStore(t)
			ctx := context.Background()
			host := nativeHost(t, s, "enroll-rollback")
			w := s.ActorEnrollment()
			if _, err := w.Begin(ctx, "cred", host.ID, "nanite/i/"+host.ID, "r"); err != nil {
				t.Fatal(err)
			}
			if _, err := s.DB.ExecContext(ctx, failOn); err != nil {
				t.Fatal(err)
			}
			if _, err := w.Complete(ctx, "cred", host.ID, testURN); err == nil {
				t.Fatal("complete succeeded despite the injected failure")
			}
			if b, i, e, a := enrollmentCounts(t, s); b+i+e+a != 0 {
				t.Fatalf("rollback left rows: bindings=%d instances=%d events=%d audits=%d", b, i, e, a)
			}
			rec, err := w.Get(ctx, host.ID)
			if err != nil || rec.Status != EnrollmentEnrolling || rec.URN != "" {
				t.Fatalf("record after failure: %+v %v", rec, err)
			}
			// The retry completes once the fault is gone.
			if _, err = s.DB.ExecContext(ctx, `DROP TRIGGER inject_fail`); err != nil {
				t.Fatal(err)
			}
			if rec, err = w.Complete(ctx, "cred", host.ID, testURN); err != nil || rec.Status != EnrollmentEnrolled {
				t.Fatalf("retry: %+v %v", rec, err)
			}
		})
	}
}

func TestOtherPathsStillRefuseToCreateAnActor(t *testing.T) {
	s := newTestStore(t)
	if err := s.CreateDurableAgentInstance(context.Background(), &DurableAgentInstance{}); !errors.Is(err, ErrVerifiedActorRequired) {
		t.Fatalf("CreateDurableAgentInstance = %v, want ErrVerifiedActorRequired", err)
	}
}

func TestRetireArchivesDisablesBindingAndLeavesActorUnroutable(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	host := nativeHost(t, s, "enroll-retire")
	w := s.ActorEnrollment()
	if _, err := w.Retire(ctx, "cred", host.ID, "r"); !errors.Is(err, ErrEnrollmentNotFound) {
		t.Fatalf("retire never-enrolled: %v", err)
	}
	if _, err := w.Begin(ctx, "cred", host.ID, "nanite/i/"+host.ID, "r"); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Complete(ctx, "cred", host.ID, testURN); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.AttachActorSession(ctx, host.ID, "sess-1"); err != nil || !ok {
		t.Fatalf("attach: %v %v", ok, err)
	}
	rec, err := w.Retire(ctx, "cred", host.ID, "done")
	if err != nil || rec.Status != EnrollmentDeregisterPending {
		t.Fatalf("retire: %+v %v", rec, err)
	}
	inst, _ := s.GetDurableAgentInstanceByURN(ctx, testURN)
	if inst.Status != DurableAgentStatusArchived || inst.CurrentSessionID != "" {
		t.Fatalf("instance after retire: %+v", inst)
	}
	var enabled int
	_ = s.DB.QueryRowContext(ctx, `SELECT enabled FROM agent_actor_bindings WHERE actor_uri = ?`, testURN).Scan(&enabled)
	if enabled != 0 {
		t.Fatal("binding still enabled")
	}
	if ok, _ := s.AttachActorSession(ctx, host.ID, "sess-2"); ok {
		t.Fatal("a retired actor accepted a session")
	}
	// Repeat is a no-op; pending list, then confirmation.
	if again, err := w.Retire(ctx, "cred", host.ID, "again"); err != nil || again.Status != EnrollmentDeregisterPending {
		t.Fatalf("repeat retire: %+v %v", again, err)
	}
	pend, err := w.PendingDeregistrations(ctx)
	if err != nil || len(pend) != 1 {
		t.Fatalf("pending: %v %v", pend, err)
	}
	if err = w.MarkDeregistered(ctx, host.ID); err != nil {
		t.Fatal(err)
	}
	if pend, _ = w.PendingDeregistrations(ctx); len(pend) != 0 {
		t.Fatalf("still pending: %v", pend)
	}
	if rec, _ = w.Get(ctx, host.ID); rec.Status != EnrollmentRetired {
		t.Fatalf("status = %s", rec.Status)
	}
	if _, err = w.Complete(ctx, "cred", host.ID, testURN); !errors.Is(err, ErrEnrollmentRetired) {
		t.Fatalf("complete after retire: %v", err)
	}
}

func TestAttachAndDetachActorSession(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	plain := nativeHost(t, s, "no-actor")
	if ok, err := s.AttachActorSession(ctx, plain.ID, "s0"); err != nil || ok {
		t.Fatalf("attach without an actor: %v %v", ok, err)
	}
	host := nativeHost(t, s, "with-actor")
	w := s.ActorEnrollment()
	_, _ = w.Begin(ctx, "cred", host.ID, "nanite/i/"+host.ID, "r")
	if _, err := w.Complete(ctx, "cred", host.ID, testURN); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.AttachActorSession(ctx, host.ID, "s1"); err != nil || !ok {
		t.Fatalf("attach: %v %v", ok, err)
	}
	inst, _ := s.GetDurableAgentInstanceByURN(ctx, testURN)
	if inst.CurrentSessionID != "s1" || inst.Status != DurableAgentStatusActive {
		t.Fatalf("after attach: %+v", inst)
	}
	if ok, _ := s.DetachActorSession(ctx, "other"); ok {
		t.Fatal("detached a session it does not hold")
	}
	if ok, err := s.DetachActorSession(ctx, "s1"); err != nil || !ok {
		t.Fatalf("detach: %v %v", ok, err)
	}
	inst, _ = s.GetDurableAgentInstanceByURN(ctx, testURN)
	if inst.CurrentSessionID != "" || inst.Status != DurableAgentStatusSleeping {
		t.Fatalf("after detach: %+v", inst)
	}
}
