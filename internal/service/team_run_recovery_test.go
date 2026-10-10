package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/agentworkflow"
	"github.com/hollis-labs/nanite/internal/store"
)

func assertHeldTeamRecoveryReport(t *testing.T, report TeamRunReconcileReport) {
	t.Helper()
	if len(report.Failures) != 1 || !errors.Is(report.Failures[0], store.ErrVerifiedActorRequired) {
		t.Fatalf("recovery failures=%v", report.Failures)
	}
	if report.PendingInspected != 0 || report.RoutingInspected != 0 || report.SignalsInspected != 0 || report.SignalsCompleted != 0 || report.MembersStopped != 0 || report.Recovered != 0 {
		t.Fatalf("held recovery reported work: %+v", report)
	}
}

func TestHeldTeamRecoveryPreservesPreparedJournalsAndPriorMembers(t *testing.T) {
	st, _, launcher := newTeamRunLauncherTestFixtures(t)
	f := newHeldTeamFixture(t, st)
	host := &a2aLaunchRecordingHost{}
	runtime := &fakeDurableRuntimeController{}
	durable := NewDurableAgentServiceWithRuntime(st, runtime)
	launcher.launcher = NewWorkflowLauncher(agentworkflow.NewRegistry(nil), host, &fakeStepExecutor{}, durable)
	launcher.durable = durable
	before := heldTeamSnapshot(t, st)
	// A completed retained workflow, active prior member, pending launch,
	// member intent and prepared signal cannot authorize recovery effects.
	for _, limit := range []int{1, 100, 0, -1} {
		assertHeldTeamRecoveryReport(t, launcher.ReconcileTeamRuns(t.Context(), limit))
		assertHeldTeamUnchanged(t, st, before)
	}
	members, err := launcher.ListMembers(t.Context(), f.runID)
	if err != nil || len(members) != 1 || members[0].Status != store.TeamRunMemberStatusActive {
		t.Fatalf("held recovery changed prior members=%+v,%v", members, err)
	}
	if len(host.calls)+len(runtime.stopped)+len(runtime.recovered)+len(runtime.sent) != 0 {
		t.Fatal("held recovery invoked workflow or runtime")
	}
	assertHeldTeamRecoveryReport(t, NewTeamRunLauncher(nil, nil, nil).ReconcileTeamRuns(t.Context(), 1))
}

func TestHeldTeamRecoveryCadenceReportsRefusalAndHonorsCancellation(t *testing.T) {
	st, _, launcher := newTeamRunLauncherTestFixtures(t)
	_ = newHeldTeamFixture(t, st)
	before := heldTeamSnapshot(t, st)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var reports []TeamRunReconcileReport
	// Already-canceled lifecycle exits after the immediate report. No ticker
	// sleep or synthetic issuer is needed to exercise the production wrapper.
	launcher.RunReconciler(ctx, time.Hour, 1, func(report TeamRunReconcileReport) { reports = append(reports, report) })
	if len(reports) != 1 {
		t.Fatalf("canceled cadence reports=%+v", reports)
	}
	assertHeldTeamRecoveryReport(t, reports[0])
	assertHeldTeamUnchanged(t, st, before)
}
