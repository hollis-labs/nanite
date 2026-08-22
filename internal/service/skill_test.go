package service

import (
	"context"
	"fmt"
	"testing"

	"github.com/hollis-labs/nanite/internal/skill"
	"github.com/hollis-labs/nanite/internal/store"
)

// --- in-memory skill store for tests ---

type memSkillStore struct {
	skills map[string]store.Skill
}

func newMemSkillStore() *memSkillStore {
	return &memSkillStore{skills: make(map[string]store.Skill)}
}

func (m *memSkillStore) ListSkills(ctx context.Context) ([]store.Skill, error) {
	out := make([]store.Skill, 0, len(m.skills))
	for _, s := range m.skills {
		out = append(out, s)
	}
	return out, nil
}

func (m *memSkillStore) GetSkill(ctx context.Context, id string) (*store.Skill, error) {
	s, ok := m.skills[id]
	if !ok {
		return nil, fmt.Errorf("skill %s not found", id)
	}
	return &s, nil
}

func (m *memSkillStore) GetSkillBySlug(ctx context.Context, slug string) (*store.Skill, error) {
	for _, s := range m.skills {
		if s.Slug == slug {
			return &s, nil
		}
	}
	return nil, fmt.Errorf("skill with slug %s not found", slug)
}

func (m *memSkillStore) CreateSkill(ctx context.Context, sk *store.Skill) error {
	m.skills[sk.ID] = *sk
	return nil
}

func (m *memSkillStore) UpdateSkill(ctx context.Context, sk *store.Skill) error {
	if _, ok := m.skills[sk.ID]; !ok {
		return fmt.Errorf("skill %s not found", sk.ID)
	}
	m.skills[sk.ID] = *sk
	return nil
}

func (m *memSkillStore) DeleteSkill(ctx context.Context, id string) error {
	delete(m.skills, id)
	return nil
}

// --- tests ---

func newTestSkillService(fileDefs []*skill.Definition) (SkillService, *memSkillStore) {
	ms := newMemSkillStore()
	svc := NewSkillService(SkillServiceConfig{
		Skills:     ms,
		FileSkills: fileDefs,
	})
	return svc, ms
}

func TestSkillService_List_FileAndDB(t *testing.T) {
	fileDefs := []*skill.Definition{
		{Name: "Go Lint", Slug: "go-lint", Description: "Lint Go code"},
		{Name: "Go Test", Slug: "go-test", Description: "Test Go code"},
	}
	svc, ms := newTestSkillService(fileDefs)

	// Add a DB skill.
	ms.skills["db-1"] = store.Skill{ID: "db-1", Name: "Custom Skill", Slug: "custom"}

	all, err := svc.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("expected 3 skills, got %d", len(all))
	}

	// Verify file-based skills appear.
	slugs := map[string]bool{}
	for _, s := range all {
		slugs[s.Slug] = true
	}
	for _, want := range []string{"go-lint", "go-test", "custom"} {
		if !slugs[want] {
			t.Errorf("missing slug %q in list", want)
		}
	}
}

func TestSkillService_List_DBDuplicateDeduped(t *testing.T) {
	fileDefs := []*skill.Definition{
		{Name: "Go Lint", Slug: "go-lint", Description: "Lint Go code"},
	}
	svc, ms := newTestSkillService(fileDefs)

	// DB skill with same slug as file skill.
	ms.skills["db-1"] = store.Skill{ID: "db-1", Name: "Old Go Lint", Slug: "go-lint"}

	all, err := svc.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 1 {
		t.Errorf("expected 1 skill (deduped), got %d", len(all))
	}
	if all[0].Name != "Go Lint" {
		t.Errorf("expected file-based name 'Go Lint', got %q", all[0].Name)
	}
}

func TestSkillService_GetBySlug_FilePriority(t *testing.T) {
	fileDefs := []*skill.Definition{
		{Name: "Go Lint", Slug: "go-lint", Description: "file version"},
	}
	svc, ms := newTestSkillService(fileDefs)

	ms.skills["db-1"] = store.Skill{ID: "db-1", Name: "DB Lint", Slug: "go-lint", Description: "db version"}

	sk, err := svc.GetBySlug(context.Background(), "go-lint")
	if err != nil {
		t.Fatalf("GetBySlug: %v", err)
	}
	if sk.Description != "file version" {
		t.Errorf("expected file-based description, got %q", sk.Description)
	}
}

func TestSkillService_GetBySlug_FallbackToDB(t *testing.T) {
	svc, ms := newTestSkillService(nil)

	ms.skills["db-1"] = store.Skill{ID: "db-1", Name: "Custom", Slug: "custom", Description: "db only"}

	sk, err := svc.GetBySlug(context.Background(), "custom")
	if err != nil {
		t.Fatalf("GetBySlug: %v", err)
	}
	if sk.Slug != "custom" {
		t.Errorf("expected slug 'custom', got %q", sk.Slug)
	}
}

func TestSkillService_Delete_FileBasedBlocked(t *testing.T) {
	fileDefs := []*skill.Definition{
		{Name: "Go Lint", Slug: "go-lint"},
	}
	svc, _ := newTestSkillService(fileDefs)

	err := svc.Delete(context.Background(), "file-go-lint")
	if err == nil {
		t.Error("expected error when deleting file-based skill")
	}
}

func TestSkillService_Delete_DBSkill(t *testing.T) {
	svc, ms := newTestSkillService(nil)
	ms.skills["db-1"] = store.Skill{ID: "db-1", Name: "Custom", Slug: "custom"}

	err := svc.Delete(context.Background(), "db-1")
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, exists := ms.skills["db-1"]; exists {
		t.Error("skill should have been deleted from store")
	}
}

func TestSkillService_ListBySource(t *testing.T) {
	fileDefs := []*skill.Definition{
		{Name: "A", Slug: "a", Source: "project"},
		{Name: "B", Slug: "b", Source: "user"},
		{Name: "C", Slug: "c", Source: "project"},
	}
	svc, _ := newTestSkillService(fileDefs)

	result, err := svc.ListBySource(context.Background(), "project")
	if err != nil {
		t.Fatalf("ListBySource: %v", err)
	}
	if len(result) != 2 {
		t.Errorf("expected 2 project skills, got %d", len(result))
	}
}

func TestSkillService_GetDefinition(t *testing.T) {
	fileDefs := []*skill.Definition{
		{Name: "Go Lint", Slug: "go-lint"},
	}
	svc, _ := newTestSkillService(fileDefs)

	def := svc.GetDefinition(context.Background(), "go-lint")
	if def == nil {
		t.Fatal("expected non-nil definition for go-lint")
	}
	if def.Name != "Go Lint" {
		t.Errorf("expected name 'Go Lint', got %q", def.Name)
	}

	// Non-existent slug.
	if svc.GetDefinition(context.Background(), "nope") != nil {
		t.Error("expected nil for non-existent slug")
	}
}

func TestSkillService_ListDefinitions(t *testing.T) {
	fileDefs := []*skill.Definition{
		{Name: "A", Slug: "a"},
		{Name: "B", Slug: "b"},
	}
	svc, _ := newTestSkillService(fileDefs)

	defs := svc.ListDefinitions(context.Background())
	if len(defs) != 2 {
		t.Errorf("expected 2 definitions, got %d", len(defs))
	}
}

func TestRegisterSkillCommands(t *testing.T) {
	fileDefs := []*skill.Definition{
		{Name: "Go Lint", Slug: "go-lint", Description: "Lint Go", ArgumentHint: "[package]"},
		{Name: "Go Test", Slug: "go-test", Description: "Test Go"},
	}
	svc, _ := newTestSkillService(fileDefs)

	reg := &mockRegistrar{}
	RegisterSkillCommands(reg, svc)

	if len(reg.registered) != 2 {
		t.Fatalf("expected 2 registered commands, got %d", len(reg.registered))
	}
	if reg.registered[0].slug != "go-lint" && reg.registered[1].slug != "go-lint" {
		t.Error("expected go-lint to be registered")
	}
}

type registeredCmd struct {
	slug, name, desc, hint string
}

type mockRegistrar struct {
	registered []registeredCmd
}

func (m *mockRegistrar) RegisterSkillCommand(slug, name, description, argumentHint string) {
	m.registered = append(m.registered, registeredCmd{slug, name, description, argumentHint})
}
