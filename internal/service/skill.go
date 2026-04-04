package service

import (
	"context"
	"fmt"
	"log"

	"github.com/hollis-labs/nanite/internal/skill"
	"github.com/hollis-labs/nanite/internal/store"
)

// SkillService encapsulates skill CRUD with file-based definitions as primary
// source and DB as fallback for user-created skills.
type SkillService interface {
	Get(ctx context.Context, id string) (*store.Skill, error)
	GetBySlug(ctx context.Context, slug string) (*store.Skill, error)
	List(ctx context.Context) ([]store.Skill, error)
	ListBySource(ctx context.Context, source string) ([]store.Skill, error)
	Create(ctx context.Context, sk *store.Skill) error
	Update(ctx context.Context, sk *store.Skill) error
	Delete(ctx context.Context, id string) error

	// GetDefinition returns the raw file-based definition for a skill slug.
	// Returns nil if the skill is DB-only.
	GetDefinition(ctx context.Context, slug string) *skill.Definition

	// ListDefinitions returns all file-based skill definitions.
	ListDefinitions(ctx context.Context) []*skill.Definition
}

// skillServiceImpl implements SkillService backed by file-based definitions
// (primary) with DB fallback for user-created skills.
type skillServiceImpl struct {
	skills   SkillStore
	fileDefs []*skill.Definition
}

// SkillServiceConfig holds dependencies for constructing a SkillService.
type SkillServiceConfig struct {
	Skills     SkillStore
	FileSkills []*skill.Definition // from skill.Discover() + builtin
}

// NewSkillService creates a SkillService from its required dependencies.
func NewSkillService(cfg SkillServiceConfig) SkillService {
	return &skillServiceImpl{
		skills:   cfg.Skills,
		fileDefs: cfg.FileSkills,
	}
}

func (s *skillServiceImpl) Get(_ context.Context, id string) (*store.Skill, error) {
	// Check file-based skills first.
	if skill.IsFileBasedID(id) {
		slug := skill.SlugFromFileID(id)
		for _, d := range s.fileDefs {
			if d.Slug == slug {
				return d.ToStoreSkill(), nil
			}
		}
	}
	return s.skills.GetSkill(id)
}

func (s *skillServiceImpl) GetBySlug(_ context.Context, slug string) (*store.Skill, error) {
	// File-based skills take priority.
	for _, d := range s.fileDefs {
		if d.Slug == slug {
			return d.ToStoreSkill(), nil
		}
	}
	return s.skills.GetSkillBySlug(slug)
}

func (s *skillServiceImpl) List(_ context.Context) ([]store.Skill, error) {
	// Start with file-based skills.
	seen := make(map[string]bool, len(s.fileDefs))
	var result []store.Skill
	for _, d := range s.fileDefs {
		result = append(result, *d.ToStoreSkill())
		seen[d.Slug] = true
	}

	// Append DB skills whose slug is not already present.
	dbSkills, err := s.skills.ListSkills()
	if err != nil {
		return result, err // return file-based skills even if DB fails
	}
	for _, sk := range dbSkills {
		if !seen[sk.Slug] {
			result = append(result, sk)
		}
	}
	return result, nil
}

func (s *skillServiceImpl) ListBySource(_ context.Context, source string) ([]store.Skill, error) {
	var result []store.Skill

	// File-based skills carry their source in the Definition.
	for _, d := range s.fileDefs {
		if d.Source == source {
			result = append(result, *d.ToStoreSkill())
		}
	}

	// DB skills don't have a source column, so we skip DB for source filters
	// that only apply to file-based sources.
	return result, nil
}

func (s *skillServiceImpl) Create(_ context.Context, sk *store.Skill) error {
	return s.skills.CreateSkill(sk)
}

func (s *skillServiceImpl) Update(_ context.Context, sk *store.Skill) error {
	return s.skills.UpdateSkill(sk)
}

func (s *skillServiceImpl) Delete(_ context.Context, id string) error {
	if skill.IsFileBasedID(id) {
		return fmt.Errorf("cannot delete file-based skill %q — remove the .md file instead", id)
	}
	return s.skills.DeleteSkill(id)
}

func (s *skillServiceImpl) GetDefinition(_ context.Context, slug string) *skill.Definition {
	for _, d := range s.fileDefs {
		if d.Slug == slug {
			return d
		}
	}
	return nil
}

func (s *skillServiceImpl) ListDefinitions(_ context.Context) []*skill.Definition {
	out := make([]*skill.Definition, len(s.fileDefs))
	copy(out, s.fileDefs)
	return out
}

// RegisterSkillCommands registers all file-based skills as slash commands in the
// command registry. Each skill becomes available as /slug [args].
func RegisterSkillCommands(registry SkillCommandRegistrar, svc SkillService) {
	defs := svc.ListDefinitions(context.Background())
	for _, def := range defs {
		d := def // capture for closure
		registry.RegisterSkillCommand(d.Slug, d.Name, d.Description, d.ArgumentHint)
	}
	log.Printf("skill-service: registered %d skill commands", len(defs))
}

// SkillCommandRegistrar is the interface for registering skill-based slash
// commands. Implemented by chat.CommandRegistry.
type SkillCommandRegistrar interface {
	RegisterSkillCommand(slug, name, description, argumentHint string)
}
