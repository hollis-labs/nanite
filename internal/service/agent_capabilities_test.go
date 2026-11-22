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
	if err := persistTestActor(context.Background(), st, agent); err != nil {
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
	if _, err := st.DB.ExecContext(ctx, `INSERT INTO actor_known_skills(agent_id,skill_name,pinned,activation_count,last_used_at,added_at,ttl_seconds,reason,approved_content_hash,granted_at,granted_by,capabilities_granted) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, seeded.AgentID, seeded.SkillName, seeded.Pinned, seeded.ActivationCount, seeded.LastUsedAt, seeded.AddedAt, seeded.TTLSeconds, seeded.Reason, seeded.ApprovedContentHash, seeded.GrantedAt, seeded.GrantedBy, seeded.CapabilitiesGranted); err != nil {
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

func TestAgentCapabilitiesMutableIntrinsicRefusesWithoutEffects(t *testing.T) {
	ctx := t.Context()
	svc, st, actor := newCapabilitiesTestService(t)
	before, readErr := st.GetAgentForActor(ctx, actor)
	if readErr != nil {
		t.Fatal(readErr)
	}
	attempts := []func() error{
		func() error {
			_, err := svc.CreateProcedure(ctx, actor, "procedure", ProcedureInput{Body: "claimed"})
			return err
		},
		func() error {
			_, err := svc.UpdateProcedure(ctx, actor, "procedure", ProcedureInput{Body: "claimed"})
			return err
		},
		func() error { return svc.DeleteProcedure(ctx, actor, "procedure") },
		func() error {
			_, err := svc.CreateKnowledgeSeed(ctx, actor, "seed", KnowledgeSeedInput{Namespace: "project/private", Body: "claimed", TagsJSON: "[]"})
			return err
		},
		func() error {
			_, err := svc.UpdateKnowledgeSeed(ctx, actor, "seed", KnowledgeSeedInput{Body: "claimed"})
			return err
		},
		func() error { return svc.DeleteKnowledgeSeed(ctx, actor, "seed") },
	}
	for _, attempt := range attempts {
		if err := attempt(); !errors.Is(err, store.ErrImmutableAgentProfile) {
			t.Fatal("mutable intrinsic admitted", err)
		}
	}
	after, err := st.GetAgentForActor(ctx, actor)
	if err != nil || after.SystemPrompt != before.SystemPrompt || after.Revision != before.Revision {
		t.Fatalf("refused intrinsic mutation effects: %+v %v", after, err)
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

func TestAgentCapabilitiesGrantSkillRefusesCallerAuthority(t *testing.T) {
	ctx := t.Context()
	svc, st, actor := newCapabilitiesTestService(t)
	if _, err := svc.GrantSkill(ctx, actor, "unissued", SkillGrant{ApprovedContentHash: "claimed", GrantedBy: "operator-ui", CapabilitiesGranted: "{}"}); !errors.Is(err, store.ErrVerifiedActorRequired) {
		t.Fatal("claim issued authority", err)
	}
	if _, err := st.GetAgentKnownSkill(ctx, actor, "unissued"); !errors.Is(err, store.ErrAgentKnownSkillNotFound) {
		t.Fatal("failed issuance created row", err)
	}
}

func TestAgentCapabilitiesGrantSkillCannotCreateAuthorityRow(t *testing.T) {
	ctx := t.Context()
	svc, st, actor := newCapabilitiesTestService(t)
	if _, err := svc.GrantSkill(ctx, actor, "unissued", SkillGrant{ApprovedContentHash: "claimed", GrantedBy: "operator-ui", CapabilitiesGranted: "{}"}); !errors.Is(err, store.ErrVerifiedActorRequired) {
		t.Fatal("claim issued authority", err)
	}
	if _, err := st.GetAgentKnownSkill(ctx, actor, "unissued"); !errors.Is(err, store.ErrAgentKnownSkillNotFound) {
		t.Fatal("failed issuance created row", err)
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
	if _, err := st.DB.ExecContext(ctx, `INSERT INTO actor_known_skills(agent_id,skill_name,pinned,activation_count,last_used_at,added_at,ttl_seconds,reason,approved_content_hash,granted_at,granted_by,capabilities_granted) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, seeded.AgentID, seeded.SkillName, seeded.Pinned, seeded.ActivationCount, seeded.LastUsedAt, seeded.AddedAt, seeded.TTLSeconds, seeded.Reason, seeded.ApprovedContentHash, seeded.GrantedAt, seeded.GrantedBy, seeded.CapabilitiesGranted); err != nil {
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

func TestAgentCapabilitiesAssignSkillRequiresVerifiedIssuer(t *testing.T) {
	ctx := t.Context()
	svc, st, actor := newCapabilitiesTestService(t)
	skill := seedCapabilitySkill(t, st, "assignable")
	if _, err := svc.AssignSkill(ctx, "claimed-host", "claimed-skill", ""); !errors.Is(err, store.ErrVerifiedActorRequired) {
		t.Fatal(err)
	}
	if _, err := svc.AssignSkill(ctx, actor, "missing-skill", ""); !errors.Is(err, ErrSkillNotFound) {
		t.Fatal(err)
	}
	if _, err := svc.AssignSkill(ctx, actor, skill.ID, ""); !errors.Is(err, store.ErrVerifiedActorRequired) {
		t.Fatal("invented issuer", err)
	}
	if rows, err := svc.ListAssignedSkills(ctx, actor); err != nil || len(rows) != 0 {
		t.Fatal("failed issuance effects", rows, err)
	}
}
