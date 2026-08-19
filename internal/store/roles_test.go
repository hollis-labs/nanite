package store

import "testing"

func TestRoleCRUD(t *testing.T) {
	s := newTestStore(t)

	r := &Role{
		Slug:            "sme",
		Name:            "Subject Matter Expert",
		SystemPrompt:    "You are a deeply knowledgeable SME.",
		DefaultClass:    "advisor",
		DefaultModel:    "claude-3-7-sonnet",
		DefaultProvider: "anthropic",
		DefaultTools:    `["dev_read","dev_grep"]`,
		DefaultSkills:   `["dev-read"]`,
	}
	if err := s.CreateRole(r); err != nil {
		t.Fatalf("CreateRole: %v", err)
	}
	if r.ID == "" {
		t.Fatal("CreateRole did not assign an ID")
	}
	if r.CreatedAt == "" || r.UpdatedAt == "" {
		t.Fatal("CreateRole did not stamp timestamps")
	}

	got, err := s.GetRole(r.ID)
	if err != nil {
		t.Fatalf("GetRole: %v", err)
	}
	if got == nil {
		t.Fatal("GetRole returned nil for a role that was just created")
	}
	if got.Slug != r.Slug || got.SystemPrompt != r.SystemPrompt || got.DefaultClass != r.DefaultClass {
		t.Errorf("GetRole mismatch: got %+v, want slug/prompt/class from %+v", got, r)
	}

	bySlug, err := s.GetRoleBySlug("sme")
	if err != nil {
		t.Fatalf("GetRoleBySlug: %v", err)
	}
	if bySlug == nil || bySlug.ID != r.ID {
		t.Fatalf("GetRoleBySlug: expected role %s, got %+v", r.ID, bySlug)
	}

	list, err := s.ListRoles()
	if err != nil {
		t.Fatalf("ListRoles: %v", err)
	}
	found := false
	for _, item := range list {
		if item.ID == r.ID {
			found = true
		}
	}
	if !found {
		t.Errorf("ListRoles did not include the created role: %+v", list)
	}

	got.Name = "Renamed SME"
	got.DefaultModel = "gpt-5"
	if err := s.UpdateRole(got); err != nil {
		t.Fatalf("UpdateRole: %v", err)
	}
	updated, err := s.GetRole(r.ID)
	if err != nil {
		t.Fatalf("GetRole after update: %v", err)
	}
	if updated.Name != "Renamed SME" || updated.DefaultModel != "gpt-5" {
		t.Errorf("UpdateRole did not persist: got %+v", updated)
	}

	if err := s.DeleteRole(r.ID); err != nil {
		t.Fatalf("DeleteRole: %v", err)
	}
	gone, err := s.GetRole(r.ID)
	if err != nil {
		t.Fatalf("GetRole after delete: %v", err)
	}
	if gone != nil {
		t.Errorf("expected role to be gone after DeleteRole, got %+v", gone)
	}
}

func TestRoleCreate_Defaults(t *testing.T) {
	s := newTestStore(t)

	r := &Role{Slug: "minimal", Name: "Minimal Role"}
	if err := s.CreateRole(r); err != nil {
		t.Fatalf("CreateRole: %v", err)
	}

	got, err := s.GetRole(r.ID)
	if err != nil {
		t.Fatalf("GetRole: %v", err)
	}
	if got.DefaultTools != "[]" {
		t.Errorf("expected DefaultTools default '[]', got %q", got.DefaultTools)
	}
	if got.DefaultSkills != "[]" {
		t.Errorf("expected DefaultSkills default '[]', got %q", got.DefaultSkills)
	}
	if got.DefaultPermissions != "{}" {
		t.Errorf("expected DefaultPermissions default '{}', got %q", got.DefaultPermissions)
	}
}

func TestRoleCreate_InvalidClassRejected(t *testing.T) {
	s := newTestStore(t)

	r := &Role{Slug: "bad-class", Name: "Bad Class", DefaultClass: "not-a-real-class"}
	if err := s.CreateRole(r); err == nil {
		t.Fatal("expected CreateRole to reject an invalid default_class, got nil error")
	}
}

func TestRoleSlugUnique(t *testing.T) {
	s := newTestStore(t)

	if err := s.CreateRole(&Role{Slug: "dup", Name: "First"}); err != nil {
		t.Fatalf("CreateRole first: %v", err)
	}
	if err := s.CreateRole(&Role{Slug: "dup", Name: "Second"}); err == nil {
		t.Fatal("expected CreateRole to reject a duplicate slug, got nil error")
	}
}
