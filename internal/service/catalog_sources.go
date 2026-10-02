package service

import (
	"context"

	"github.com/hollis-labs/nanite/internal/plugin"
	"github.com/hollis-labs/nanite/internal/store"
)

type CatalogSourceStore interface {
	ListCatalogSources(context.Context) ([]store.CatalogSource, error)
	CreateCatalogSource(context.Context, string, string, string, int) (*store.CatalogSource, error)
	GetCatalogSource(context.Context, string) (*store.CatalogSource, error)
	UpdateCatalogSource(context.Context, string, string, string, bool, int) error
	DeleteCatalogSource(context.Context, string) error
}

// CatalogSourceService owns catalog persistence and sparse source patches.
type CatalogSourceService struct{ store CatalogSourceStore }

func NewCatalogSourceService(st CatalogSourceStore) *CatalogSourceService {
	return &CatalogSourceService{store: st}
}
func (s *CatalogSourceService) List(ctx context.Context) ([]store.CatalogSource, error) {
	return s.store.ListCatalogSources(ctx)
}
func (s *CatalogSourceService) Create(ctx context.Context, name, url string, priority int) (*store.CatalogSource, error) {
	return s.store.CreateCatalogSource(ctx, name, url, "custom", priority)
}

type CatalogSourcePatch struct {
	Name, URL string
	Enabled   *bool
	Priority  *int
}
type CatalogSourceMissingError struct{ Err error }

func (e *CatalogSourceMissingError) Error() string { return e.Err.Error() }
func (e *CatalogSourceMissingError) Unwrap() error { return e.Err }
func (s *CatalogSourceService) Patch(ctx context.Context, id string, p CatalogSourcePatch) error {
	row, err := s.store.GetCatalogSource(ctx, id)
	if err != nil {
		return &CatalogSourceMissingError{Err: err}
	}
	if p.Name != "" {
		row.Name = p.Name
	}
	if p.URL != "" {
		row.URL = p.URL
	}
	if p.Enabled != nil {
		row.Enabled = *p.Enabled
	}
	if p.Priority != nil {
		row.Priority = *p.Priority
	}
	return s.store.UpdateCatalogSource(ctx, id, row.Name, row.URL, row.Enabled, row.Priority)
}
func (s *CatalogSourceService) Delete(ctx context.Context, id string) error {
	return s.store.DeleteCatalogSource(ctx, id)
}
func (s *CatalogSourceService) FetchInputs(ctx context.Context) ([]plugin.CatalogSource, error) {
	rows, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	sources := make([]plugin.CatalogSource, len(rows))
	for i, row := range rows {
		sources[i] = plugin.CatalogSource{ID: row.ID, Name: row.Name, URL: row.URL, Priority: row.Priority, Enabled: row.Enabled}
	}
	return sources, nil
}
