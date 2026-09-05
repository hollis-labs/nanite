package service

import (
	"context"
	"fmt"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

type memSkillStore struct{ skills map[string]store.Skill }

func newMemSkillStore() *memSkillStore { return &memSkillStore{skills: make(map[string]store.Skill)} }

func (m *memSkillStore) ListSkills(context.Context) ([]store.Skill, error) {
	out := make([]store.Skill, 0, len(m.skills))
	for _, sk := range m.skills {
		out = append(out, sk)
	}
	return out, nil
}

func (m *memSkillStore) GetSkill(_ context.Context, id string) (*store.Skill, error) {
	sk, ok := m.skills[id]
	if !ok {
		return nil, fmt.Errorf("skill %s not found", id)
	}
	return &sk, nil
}

func (m *memSkillStore) GetSkillBySlug(_ context.Context, slug string) (*store.Skill, error) {
	for _, sk := range m.skills {
		if sk.Slug == slug {
			return &sk, nil
		}
	}
	return nil, fmt.Errorf("skill with slug %s not found", slug)
}

func (m *memSkillStore) CreateSkill(_ context.Context, sk *store.Skill) error {
	m.skills[sk.ID] = *sk
	return nil
}

func (m *memSkillStore) UpdateSkill(_ context.Context, sk *store.Skill) error {
	if _, ok := m.skills[sk.ID]; !ok {
		return fmt.Errorf("skill %s not found", sk.ID)
	}
	m.skills[sk.ID] = *sk
	return nil
}

func (m *memSkillStore) DeleteSkill(_ context.Context, id string) error {
	delete(m.skills, id)
	return nil
}

func newTestSkillService() (SkillService, *memSkillStore) {
	ms := newMemSkillStore()
	return NewSkillService(SkillServiceConfig{Skills: ms}), ms
}

func TestSkillServiceDatabaseCRUD(t *testing.T) {
	svc, ms := newTestSkillService()
	sk := &store.Skill{ID: "db-1", Name: "Custom", Slug: "custom", SourceTier: "user"}
	if err := svc.Create(context.Background(), sk); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := svc.GetBySlug(context.Background(), "custom")
	if err != nil || got.ID != sk.ID {
		t.Fatalf("GetBySlug = %#v, %v", got, err)
	}
	got.Description = "updated"
	if updateErr := svc.Update(context.Background(), got); updateErr != nil {
		t.Fatalf("Update: %v", updateErr)
	}
	all, err := svc.List(context.Background())
	if err != nil || len(all) != 1 || all[0].Description != "updated" {
		t.Fatalf("List = %#v, %v", all, err)
	}
	if err := svc.Delete(context.Background(), sk.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if len(ms.skills) != 0 {
		t.Fatalf("database skill remained after delete: %#v", ms.skills)
	}
}

func TestSkillServiceListBySourceUsesDatabaseIndex(t *testing.T) {
	svc, ms := newTestSkillService()
	ms.skills["a"] = store.Skill{ID: "a", Slug: "a", SourceTier: "project"}
	ms.skills["b"] = store.Skill{ID: "b", Slug: "b", SourceTier: "user"}
	ms.skills["c"] = store.Skill{ID: "c", Slug: "c", SourceTier: "project"}
	got, err := svc.ListBySource(context.Background(), "project")
	if err != nil {
		t.Fatalf("ListBySource: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ListBySource returned %d rows, want 2", len(got))
	}
}

func TestSkillServiceDoesNotInterpretFilePrefixedDatabaseID(t *testing.T) {
	svc, ms := newTestSkillService()
	sk := &store.Skill{ID: "file-historical", Name: "Historical", Slug: "historical"}
	ms.skills[sk.ID] = *sk
	sk.Description = "still editable"
	if err := svc.Update(context.Background(), sk); err != nil {
		t.Fatalf("Update historical ID: %v", err)
	}
	if err := svc.Delete(context.Background(), sk.ID); err != nil {
		t.Fatalf("Delete historical ID: %v", err)
	}
}
