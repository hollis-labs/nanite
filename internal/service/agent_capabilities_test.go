package service

import (
	"context"
	"errors"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

func newCapabilitiesTestService(t *testing.T) (*AgentCapabilitiesService, *store.Store, string) {
	t.Helper()
	st := newConfigTestStore(t)
	agent := &store.AgentProfile{Name: "Capabilities", Slug: "capabilities-agent", SystemPrompt: "x"}
	if err := st.CreateAgent(context.Background(), agent); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	return NewAgentCapabilitiesService(st), st, agent.ID
}

// The store writes these rows with INSERT OR REPLACE, so every column an
// update does not set must be carried forward by the service or it is lost.

func TestAgentCapabilitiesUpdateKnownSkillPreservesGrantAndUsage(t *testing.T) {
	ctx := context.Background()
	svc, st, agentID := newCapabilitiesTestService(t)

	seeded := store.AgentKnownSkill{
		AgentID:             agentID,
		SkillName:           "granted-skill",
		ActivationCount:     4,
		LastUsedAt:          "2026-09-01 10:00:00",
		AddedAt:             "2026-08-01 09:00:00",
		ApprovedContentHash: "sha256:abc",
		GrantedAt:           "2026-08-02 09:00:00",
		GrantedBy:           "operator-ui",
		CapabilitiesGranted: `{"network":false}`,
	}
	if err := st.InsertAgentKnownSkill(ctx, seeded); err != nil {
		t.Fatalf("InsertAgentKnownSkill: %v", err)
	}

	got, err := svc.UpdateKnownSkill(ctx, agentID, "granted-skill", KnownSkillInput{Pinned: true, TTLSeconds: 60, Reason: "edited"})
	if err != nil {
		t.Fatalf("UpdateKnownSkill: %v", err)
	}
	if !got.Pinned || got.TTLSeconds != 60 || got.Reason != "edited" {
		t.Fatalf("settable columns not applied: %+v", got)
	}
	if got.ActivationCount != seeded.ActivationCount ||
		got.LastUsedAt != seeded.LastUsedAt ||
		got.AddedAt != seeded.AddedAt ||
		got.ApprovedContentHash != seeded.ApprovedContentHash ||
		got.GrantedAt != seeded.GrantedAt ||
		got.GrantedBy != seeded.GrantedBy ||
		got.CapabilitiesGranted != seeded.CapabilitiesGranted {
		t.Fatalf("carried-forward columns lost:\n got  %+v\n want %+v", got, seeded)
	}
}

func TestAgentCapabilitiesUpdateKnownToolPreservesUsage(t *testing.T) {
	ctx := context.Background()
	svc, st, agentID := newCapabilitiesTestService(t)

	seeded := store.AgentKnownTool{
		AgentID:         agentID,
		ToolName:        "torque_task_create",
		ActivationCount: 9,
		LastUsedAt:      "2026-09-01 10:00:00",
		AddedAt:         "2026-08-01 09:00:00",
	}
	if err := st.InsertAgentKnownTool(ctx, seeded); err != nil {
		t.Fatalf("InsertAgentKnownTool: %v", err)
	}

	got, err := svc.UpdateKnownTool(ctx, agentID, "torque_task_create", KnownToolInput{Pinned: true, SortOrder: 3, TTLSeconds: 60, Reason: "edited"})
	if err != nil {
		t.Fatalf("UpdateKnownTool: %v", err)
	}
	if !got.Pinned || got.SortOrder != 3 || got.TTLSeconds != 60 || got.Reason != "edited" {
		t.Fatalf("settable columns not applied: %+v", got)
	}
	if got.ActivationCount != seeded.ActivationCount ||
		got.LastUsedAt != seeded.LastUsedAt ||
		got.AddedAt != seeded.AddedAt {
		t.Fatalf("carried-forward columns lost:\n got  %+v\n want %+v", got, seeded)
	}
}

func TestAgentCapabilitiesUpdateKnowledgeSeedPreservesAppliedAndCreated(t *testing.T) {
	ctx := context.Background()
	svc, st, agentID := newCapabilitiesTestService(t)

	seeded := store.AgentKnowledgeSeed{
		AgentID:   agentID,
		SeedKey:   "conventions",
		Namespace: "project/x",
		Body:      "old",
		TagsJSON:  "[]",
		AppliedAt: "2026-09-01 10:00:00",
		CreatedAt: "2026-08-01 09:00:00",
	}
	if err := st.InsertAgentKnowledgeSeed(ctx, seeded); err != nil {
		t.Fatalf("InsertAgentKnowledgeSeed: %v", err)
	}

	got, err := svc.UpdateKnowledgeSeed(ctx, agentID, "conventions", KnowledgeSeedInput{Namespace: "project/y", Body: "new", TagsJSON: `["a"]`})
	if err != nil {
		t.Fatalf("UpdateKnowledgeSeed: %v", err)
	}
	if got.Namespace != "project/y" || got.Body != "new" || got.TagsJSON != `["a"]` {
		t.Fatalf("settable columns not applied: %+v", got)
	}
	if got.AppliedAt != seeded.AppliedAt || got.CreatedAt != seeded.CreatedAt {
		t.Fatalf("carried-forward columns lost:\n got  %+v\n want %+v", got, seeded)
	}
}

func TestAgentCapabilitiesCreateKnownSkillUpsertsBareAssignment(t *testing.T) {
	ctx := context.Background()
	svc, st, agentID := newCapabilitiesTestService(t)

	// A bare assignment: no known-skill data of its own. ActivationCount and
	// LastUsedAt are zero by definition of bare, so AddedAt is the column
	// that shows the row was upserted rather than replaced.
	bare := store.AgentKnownSkill{AgentID: agentID, SkillName: "assigned", AddedAt: "2026-08-01 09:00:00"}
	if err := st.InsertAgentKnownSkill(ctx, bare); err != nil {
		t.Fatalf("InsertAgentKnownSkill: %v", err)
	}

	got, err := svc.CreateKnownSkill(ctx, agentID, "assigned", KnownSkillInput{Pinned: true, Reason: "promoted"})
	if err != nil {
		t.Fatalf("CreateKnownSkill onto bare row: %v", err)
	}
	if !got.Pinned || got.Reason != "promoted" {
		t.Fatalf("settable columns not applied: %+v", got)
	}
	if got.AddedAt != bare.AddedAt || got.ActivationCount != bare.ActivationCount || got.LastUsedAt != bare.LastUsedAt {
		t.Fatalf("bare row's columns lost:\n got  %+v\n want %+v", got, bare)
	}

	// The row now carries real data, so a second create is a duplicate.
	if _, err := svc.CreateKnownSkill(ctx, agentID, "assigned", KnownSkillInput{Reason: "again"}); !errors.Is(err, ErrCapabilityExists) {
		t.Fatalf("second CreateKnownSkill err = %v, want ErrCapabilityExists", err)
	}
}

func TestAgentCapabilitiesCreateDuplicatesReturnExists(t *testing.T) {
	ctx := context.Background()
	svc, _, agentID := newCapabilitiesTestService(t)

	if _, err := svc.CreateKnownTool(ctx, agentID, "t", KnownToolInput{}); err != nil {
		t.Fatalf("CreateKnownTool: %v", err)
	}
	if _, err := svc.CreateKnownTool(ctx, agentID, "t", KnownToolInput{}); !errors.Is(err, ErrCapabilityExists) {
		t.Fatalf("duplicate known tool err = %v, want ErrCapabilityExists", err)
	}
	if _, err := svc.CreateProcedure(ctx, agentID, "p", ProcedureInput{Body: "b"}); err != nil {
		t.Fatalf("CreateProcedure: %v", err)
	}
	if _, err := svc.CreateProcedure(ctx, agentID, "p", ProcedureInput{Body: "b"}); !errors.Is(err, ErrCapabilityExists) {
		t.Fatalf("duplicate procedure err = %v, want ErrCapabilityExists", err)
	}
	if _, err := svc.CreateKnowledgeSeed(ctx, agentID, "s", KnowledgeSeedInput{Namespace: "n", Body: "b", TagsJSON: "[]"}); err != nil {
		t.Fatalf("CreateKnowledgeSeed: %v", err)
	}
	if _, err := svc.CreateKnowledgeSeed(ctx, agentID, "s", KnowledgeSeedInput{Namespace: "n", Body: "b", TagsJSON: "[]"}); !errors.Is(err, ErrCapabilityExists) {
		t.Fatalf("duplicate seed err = %v, want ErrCapabilityExists", err)
	}
}

func TestAgentCapabilitiesUpdateMissingReturnsStoreNotFound(t *testing.T) {
	ctx := context.Background()
	svc, _, agentID := newCapabilitiesTestService(t)

	if _, err := svc.UpdateKnownTool(ctx, agentID, "missing", KnownToolInput{}); !errors.Is(err, store.ErrAgentKnownToolNotFound) {
		t.Fatalf("UpdateKnownTool err = %v", err)
	}
	if _, err := svc.UpdateKnownSkill(ctx, agentID, "missing", KnownSkillInput{}); !errors.Is(err, store.ErrAgentKnownSkillNotFound) {
		t.Fatalf("UpdateKnownSkill err = %v", err)
	}
	if _, err := svc.UpdateProcedure(ctx, agentID, "missing", ProcedureInput{}); !errors.Is(err, store.ErrAgentProcedureNotFound) {
		t.Fatalf("UpdateProcedure err = %v", err)
	}
	if _, err := svc.UpdateKnowledgeSeed(ctx, agentID, "missing", KnowledgeSeedInput{}); !errors.Is(err, store.ErrAgentKnowledgeSeedNotFound) {
		t.Fatalf("UpdateKnowledgeSeed err = %v", err)
	}
	if _, err := svc.MarkKnowledgeSeedApplied(ctx, agentID, "missing"); !errors.Is(err, store.ErrAgentKnowledgeSeedNotFound) {
		t.Fatalf("MarkKnowledgeSeedApplied err = %v", err)
	}
}

func TestAgentCapabilitiesWriteErrorKeepsStoreMessage(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newCapabilitiesTestService(t)

	// The store rejects an empty agent_id before touching the database.
	_, err := svc.CreateKnownTool(ctx, "", "t", KnownToolInput{})
	var writeErr *CapabilityWriteError
	if !errors.As(err, &writeErr) {
		t.Fatalf("err = %T %v, want *CapabilityWriteError", err, err)
	}
	if err.Error() != writeErr.Err.Error() {
		t.Fatalf("Error() = %q, want the store's own %q", err.Error(), writeErr.Err.Error())
	}
}

func seedCapabilitySkill(t *testing.T, st *store.Store, slug string) *store.Skill {
	t.Helper()
	sk := &store.Skill{Name: slug, Slug: slug, Enabled: true}
	if err := st.CreateSkill(context.Background(), sk); err != nil {
		t.Fatalf("CreateSkill: %v", err)
	}
	return sk
}

func TestAgentCapabilitiesGrantSkillPreservesRowAndSetsGrant(t *testing.T) {
	ctx := context.Background()
	svc, st, agentID := newCapabilitiesTestService(t)

	seeded := store.AgentKnownSkill{
		AgentID:         agentID,
		SkillName:       "grantable",
		Pinned:          true,
		ActivationCount: 3,
		LastUsedAt:      "2026-09-01 10:00:00",
		AddedAt:         "2026-08-01 09:00:00",
		TTLSeconds:      120,
		Reason:          "panel pin",
	}
	if err := st.InsertAgentKnownSkill(ctx, seeded); err != nil {
		t.Fatalf("InsertAgentKnownSkill: %v", err)
	}

	got, err := svc.GrantSkill(ctx, agentID, "grantable", SkillGrant{
		ApprovedContentHash: "skl-vendor-abc",
		GrantedBy:           "operator-ui",
		CapabilitiesGranted: `{"network":false}`,
	})
	if err != nil {
		t.Fatalf("GrantSkill: %v", err)
	}
	if got.ApprovedContentHash != "skl-vendor-abc" || got.GrantedBy != "operator-ui" ||
		got.CapabilitiesGranted != `{"network":false}` || got.GrantedAt == "" {
		t.Fatalf("grant state not written: %+v", got)
	}
	if got.Pinned != seeded.Pinned || got.ActivationCount != seeded.ActivationCount ||
		got.LastUsedAt != seeded.LastUsedAt || got.AddedAt != seeded.AddedAt ||
		got.TTLSeconds != seeded.TTLSeconds || got.Reason != seeded.Reason {
		t.Fatalf("row columns lost:\n got  %+v\n want %+v", got, seeded)
	}
}

func TestAgentCapabilitiesGrantSkillCreatesMissingRow(t *testing.T) {
	ctx := context.Background()
	svc, _, agentID := newCapabilitiesTestService(t)

	got, err := svc.GrantSkill(ctx, agentID, "fresh", SkillGrant{ApprovedContentHash: "h", GrantedBy: "op", CapabilitiesGranted: "{}"})
	if err != nil {
		t.Fatalf("GrantSkill: %v", err)
	}
	if got.SkillName != "fresh" || got.ApprovedContentHash != "h" {
		t.Fatalf("row = %+v", got)
	}
}

func TestAgentCapabilitiesRevokeSkillGrantClearsOnlyGrant(t *testing.T) {
	ctx := context.Background()
	svc, st, agentID := newCapabilitiesTestService(t)

	seeded := store.AgentKnownSkill{
		AgentID:             agentID,
		SkillName:           "revocable",
		Pinned:              true,
		ActivationCount:     2,
		AddedAt:             "2026-08-01 09:00:00",
		Reason:              "keep me",
		ApprovedContentHash: "h",
		GrantedAt:           "2026-08-02 09:00:00",
		GrantedBy:           "op",
		CapabilitiesGranted: "{}",
	}
	if err := st.InsertAgentKnownSkill(ctx, seeded); err != nil {
		t.Fatalf("InsertAgentKnownSkill: %v", err)
	}
	if err := svc.RevokeSkillGrant(ctx, agentID, "revocable"); err != nil {
		t.Fatalf("RevokeSkillGrant: %v", err)
	}
	got, err := st.GetAgentKnownSkill(ctx, agentID, "revocable")
	if err != nil {
		t.Fatalf("GetAgentKnownSkill: %v", err)
	}
	if got.ApprovedContentHash != "" || got.GrantedAt != "" || got.GrantedBy != "" || got.CapabilitiesGranted != "" {
		t.Fatalf("grant state not cleared: %+v", got)
	}
	if !got.Pinned || got.ActivationCount != 2 || got.AddedAt != seeded.AddedAt || got.Reason != "keep me" {
		t.Fatalf("row columns lost: %+v", got)
	}

	// Revoking again: the row exists but carries no grant.
	if err := svc.RevokeSkillGrant(ctx, agentID, "revocable"); !errors.Is(err, ErrNoSkillGrant) {
		t.Fatalf("second revoke err = %v, want ErrNoSkillGrant", err)
	}
	// No row at all.
	if err := svc.RevokeSkillGrant(ctx, agentID, "never-known"); !errors.Is(err, ErrNoSkillGrant) {
		t.Fatalf("missing-row revoke err = %v, want ErrNoSkillGrant", err)
	}
}

func TestAgentCapabilitiesAssignSkillChecksAgentThenSkill(t *testing.T) {
	ctx := context.Background()
	svc, st, agentID := newCapabilitiesTestService(t)
	sk := seedCapabilitySkill(t, st, "assignable")

	// Both missing: the agent is reported first.
	if _, err := svc.AssignSkill(ctx, "no-such-agent", "no-such-skill", ""); !errors.Is(err, ErrAgentNotFound) {
		t.Fatalf("missing agent err = %v, want ErrAgentNotFound", err)
	}
	if _, err := svc.AssignSkill(ctx, agentID, "no-such-skill", ""); !errors.Is(err, ErrSkillNotFound) {
		t.Fatalf("missing skill err = %v, want ErrSkillNotFound", err)
	}

	skills, err := svc.AssignSkill(ctx, agentID, sk.ID, "")
	if err != nil {
		t.Fatalf("AssignSkill: %v", err)
	}
	if len(skills) != 1 || skills[0].Slug != "assignable" {
		t.Fatalf("assigned skills = %+v", skills)
	}
	if err := svc.RemoveSkill(ctx, agentID, sk.ID); err != nil {
		t.Fatalf("RemoveSkill: %v", err)
	}
	if left, err := svc.ListAssignedSkills(ctx, agentID); err != nil || len(left) != 0 {
		t.Fatalf("after remove: %v, %+v", err, left)
	}
}
