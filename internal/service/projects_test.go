package service

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

type projectsCtxKey struct{}

// fakeProjects serves projects and sessions from maps and records whether
// every read ran under the caller's ctx.
type fakeProjects struct {
	projects   []store.Project
	sessions   map[string]*store.Session
	getErr     error
	foreignCtx bool
}

func (f *fakeProjects) seen(ctx context.Context) {
	if ctx.Value(projectsCtxKey{}) == nil {
		f.foreignCtx = true
	}
}

func (f *fakeProjects) ListProjects(ctx context.Context) ([]store.Project, error) {
	f.seen(ctx)
	return f.projects, nil
}

func (f *fakeProjects) GetProject(ctx context.Context, id string) (*store.Project, error) {
	f.seen(ctx)
	if f.getErr != nil {
		return nil, f.getErr
	}
	for i := range f.projects {
		if f.projects[i].ID == id {
			p := f.projects[i]
			return &p, nil
		}
	}
	return nil, errors.New("get project " + id + ": sql: no rows in result set")
}

func (f *fakeProjects) CreateProject(context.Context, *store.Project) error { return nil }
func (f *fakeProjects) UpdateProject(context.Context, *store.Project) error { return nil }
func (f *fakeProjects) DeleteProject(context.Context, string) error         { return nil }

func (f *fakeProjects) GetSession(ctx context.Context, id string) (*store.Session, error) {
	f.seen(ctx)
	s, ok := f.sessions[id]
	if !ok {
		return nil, errors.New("no such session")
	}
	return s, nil
}

func TestProjectService_AutocompleteRoot(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	projects := []store.Project{
		{ID: "bare"},
		{ID: "first", RepoPath: "/repos/first"},
		{ID: "own", RepoPath: "/repos/own"},
	}
	sessions := map[string]*store.Session{
		"with-project":    {ID: "with-project", ProjectID: "own"},
		"bare-project":    {ID: "bare-project", ProjectID: "bare"},
		"missing-project": {ID: "missing-project", ProjectID: "gone"},
		"no-project":      {ID: "no-project"},
	}
	for _, c := range []struct{ name, session, want string }{
		{"session's project", "with-project", "/repos/own"},
		{"project without repo falls back to the first with one", "bare-project", "/repos/first"},
		{"unreadable project falls back", "missing-project", "/repos/first"},
		{"session without project falls back", "no-project", "/repos/first"},
		{"unknown session uses cwd", "nope", cwd},
		{"no session uses cwd, not the fallback", "", cwd},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := &fakeProjects{projects: projects, sessions: sessions}
			ctx := context.WithValue(context.Background(), projectsCtxKey{}, true)
			if got := NewProjectService(f, f).AutocompleteRoot(ctx, c.session); got != c.want {
				t.Fatalf("AutocompleteRoot(%q) = %q, want %q", c.session, got, c.want)
			}
			if f.foreignCtx {
				t.Fatal("a lookup ran under a ctx other than the caller's")
			}
		})
	}

	// No session reader wired: cwd.
	f := &fakeProjects{projects: projects, sessions: sessions}
	if got := NewProjectService(f, nil).AutocompleteRoot(context.Background(), "with-project"); got != cwd {
		t.Fatalf("without a session reader = %q, want cwd", got)
	}
}

func TestProjectService_DeleteMissingIsNotFound(t *testing.T) {
	f := &fakeProjects{getErr: errors.New("db down")}
	if err := NewProjectService(f, nil).Delete(context.Background(), "x"); !errors.Is(err, ErrProjectNotFound) {
		t.Fatalf("Delete err = %v, want ErrProjectNotFound", err)
	}
}
