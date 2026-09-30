package service

import (
	"context"

	"github.com/hollis-labs/nanite/internal/store"
)

// SkillService exposes the database index for explicitly installed skill
// packages. Vendored package content is handled by the install/materialize
// pipeline; there is no virtual file-definition overlay.
type SkillService interface {
	Get(ctx context.Context, id string) (*store.Skill, error)
	GetBySlug(ctx context.Context, slug string) (*store.Skill, error)
	List(ctx context.Context) ([]store.Skill, error)
	ListBySource(ctx context.Context, source string) ([]store.Skill, error)
	Create(ctx context.Context, sk *store.Skill) error
	Update(ctx context.Context, sk *store.Skill) error
	Delete(ctx context.Context, id string) error
}

type skillServiceImpl struct{ skills SkillStore }

type SkillServiceConfig struct{ Skills SkillStore }

func NewSkillService(cfg SkillServiceConfig) SkillService {
	return &skillServiceImpl{skills: cfg.Skills}
}

func (s *skillServiceImpl) Get(ctx context.Context, id string) (*store.Skill, error) {
	return s.skills.GetSkill(ctx, id)
}

func (s *skillServiceImpl) GetBySlug(ctx context.Context, slug string) (*store.Skill, error) {
	return s.skills.GetSkillBySlug(ctx, slug)
}

func (s *skillServiceImpl) List(ctx context.Context) ([]store.Skill, error) {
	return s.skills.ListSkills(ctx)
}

func (s *skillServiceImpl) ListBySource(ctx context.Context, source string) ([]store.Skill, error) {
	all, err := s.skills.ListSkills(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]store.Skill, 0)
	for _, sk := range all {
		if sk.SourceTier == source {
			result = append(result, sk)
		}
	}
	return result, nil
}

func (s *skillServiceImpl) Create(ctx context.Context, sk *store.Skill) error {
	return s.skills.CreateSkill(ctx, sk)
}

func (s *skillServiceImpl) Update(ctx context.Context, sk *store.Skill) error {
	return s.skills.UpdateSkill(ctx, sk)
}

func (s *skillServiceImpl) Delete(ctx context.Context, id string) error {
	return s.skills.DeleteSkill(ctx, id)
}
