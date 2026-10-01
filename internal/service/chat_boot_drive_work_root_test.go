package service

// CW-20261001-0020: a CLI agent booted for a session used to start in
// /tmp/nanite-boot-<provider>-… with no view of the project it was started
// for. These pin the two launch paths the agent-os smoke test hit — a
// harness v1 project-scoped session and a durable-agent start with a
// work_root — from the session row a real store holds to the planted
// launch config a booted claude or codex process reads.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/agentkit/agentruntime/runtimekind"
	runtimeagent "github.com/hollis-labs/nanite/internal/runtime/agent"
	"github.com/hollis-labs/nanite/internal/store"
)

func TestBootSessionWorkdir_ProjectSessionLaunchesInRepoPath(t *testing.T) {
	st := newContextResolverTestStore(t)
	ctx := context.Background()
	repo := canonicalTempDir(t)

	project := &store.Project{ID: "smoke-project", Name: "smoke-project", RepoPath: repo}
	if err := st.CreateProject(ctx, project); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	// The harness v1 create path: SessionService.Create with a project id.
	sess, err := NewSessionService(SessionServiceDeps{Sessions: st, Writer: st}).Create(ctx, CreateSessionOpts{ProjectID: project.ID, Provider: "pty-claude", SkipAgentBinding: true})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	s := &chatServiceImpl{store: st}
	got, err := s.bootSessionWorkdir(ctx, sess)
	if err != nil {
		t.Fatalf("bootSessionWorkdir: %v", err)
	}
	if got != repo {
		t.Fatalf("work root = %q, want the project's repo_path %q", got, repo)
	}
	assertClaudeLaunchCarriesWorkRoot(t, sess.ID, got)
	assertCodexLaunchCarriesWorkRoot(t, sess.ID, got)
}

func TestBootSessionWorkdir_DurableStartLaunchesInWorkRoot(t *testing.T) {
	st := newContextResolverTestStore(t)
	ctx := context.Background()
	repo := canonicalTempDir(t)
	workRoot := canonicalTempDir(t)

	project := &store.Project{ID: "durable-project", Name: "durable-project", RepoPath: repo}
	if err := st.CreateProject(ctx, project); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	profile := &store.AgentProfile{Name: "Smoke Claude", Slug: "smoke-claude", SystemPrompt: "x"}
	if err := st.CreateAgent(ctx, profile); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	newInstance := func(slug, root string) *store.DurableAgentInstance {
		t.Helper()
		inst := &store.DurableAgentInstance{
			Name:             slug,
			Slug:             slug,
			ProfileID:        profile.ID,
			LifecycleClass:   store.DurableAgentClassHarness,
			Provider:         "pty-claude",
			Model:            "claude-cli",
			RuntimeKind:      string(runtimekind.StreamingStdio),
			LaunchSourceType: store.DurableAgentLaunchCLIHarness,
			WorkRoot:         root,
		}
		if err := st.CreateDurableAgentInstance(ctx, inst); err != nil {
			t.Fatalf("CreateDurableAgentInstance %s: %v", slug, err)
		}
		return inst
	}
	start := func(inst *store.DurableAgentInstance) *store.Session {
		t.Helper()
		res, err := NewDurableAgentService(st).Start(ctx, inst.ID, DurableAgentStartRequest{ProjectID: project.ID})
		if err != nil {
			t.Fatalf("Start %s: %v", inst.Slug, err)
		}
		if res.Policy.WorkRoot != inst.WorkRoot {
			t.Fatalf("policy work_root = %q, want %q", res.Policy.WorkRoot, inst.WorkRoot)
		}
		return res.Session
	}
	s := &chatServiceImpl{store: st}

	t.Run("work_root wins over the project's repo_path", func(t *testing.T) {
		sess := start(newInstance("smoke-claude-root", workRoot))
		got, err := s.bootSessionWorkdir(ctx, sess)
		if err != nil {
			t.Fatalf("bootSessionWorkdir: %v", err)
		}
		if got != workRoot {
			t.Fatalf("work root = %q, want the durable work_root %q", got, workRoot)
		}
		assertClaudeLaunchCarriesWorkRoot(t, sess.ID, got)
		assertCodexLaunchCarriesWorkRoot(t, sess.ID, got)
	})

	t.Run("a home-relative work_root expands", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		sess := start(newInstance("smoke-claude-home", "~/dev/agent"))
		got, err := s.bootSessionWorkdir(ctx, sess)
		if err != nil {
			t.Fatalf("bootSessionWorkdir: %v", err)
		}
		if want := filepath.Join(home, "dev", "agent"); got != want {
			t.Fatalf("work root = %q, want %q", got, want)
		}
	})

	t.Run("no work_root falls back to the project's repo_path", func(t *testing.T) {
		sess := start(newInstance("smoke-claude-project", ""))
		got, err := s.bootSessionWorkdir(ctx, sess)
		if err != nil {
			t.Fatalf("bootSessionWorkdir: %v", err)
		}
		if got != repo {
			t.Fatalf("work root = %q, want the project's repo_path %q", got, repo)
		}
	})

	t.Run("a relative work_root is refused", func(t *testing.T) {
		sess := start(newInstance("smoke-claude-relative", "dev/agent"))
		if got, err := s.bootSessionWorkdir(ctx, sess); err == nil {
			t.Fatalf("bootSessionWorkdir = %q, want an error for a relative work_root", got)
		}
	})
}

func TestBootSessionWorkdir_ProjectWithoutUsableRepoPath(t *testing.T) {
	st := newContextResolverTestStore(t)
	ctx := context.Background()
	s := &chatServiceImpl{store: st}

	if got, err := s.bootSessionWorkdir(ctx, &store.Session{ID: "no-project"}); err != nil || got != "" {
		t.Fatalf("session with no project = (%q, %v), want no work root", got, err)
	}

	bare := &store.Project{ID: "no-repo", Name: "no-repo"}
	if err := st.CreateProject(ctx, bare); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if got, err := s.bootSessionWorkdir(ctx, &store.Session{ID: "bare", ProjectID: bare.ID}); err != nil || got != "" {
		t.Fatalf("project with no repo_path = (%q, %v), want no work root and no error", got, err)
	}

	gone := &store.Project{ID: "gone-repo", Name: "gone-repo", RepoPath: filepath.Join(t.TempDir(), "missing")}
	if err := st.CreateProject(ctx, gone); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	got, err := s.bootSessionWorkdir(ctx, &store.Session{ID: "gone", ProjectID: gone.ID})
	if err == nil {
		t.Fatalf("project whose repo_path is gone = %q, want an error rather than a directory Boot would create", got)
	}
	if _, statErr := os.Stat(gone.RepoPath); !os.IsNotExist(statErr) {
		t.Fatalf("resolution must not create the missing repo_path (stat err = %v)", statErr)
	}
}

func TestRegenerateBootDirSlots_KeepsProjectFolder(t *testing.T) {
	s := &chatServiceImpl{}
	bootDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(bootDir, ".sandbox"), 0o750); err != nil {
		t.Fatal(err)
	}
	s.activeSessionWorkRoots.Store("sess-regen-root", "/home/x/dev/project")

	if err := s.regenerateBootDirSlots("sess-regen-root", bootDir, &store.AgentProfile{Name: "agent"}); err != nil {
		t.Fatalf("regenerateBootDirSlots: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(bootDir, "CLAUDE.md")) //nolint:gosec // reads a file this test just planted into a temp dir
	if err != nil {
		t.Fatalf("read CLAUDE.md: %v", err)
	}
	if !strings.Contains(string(body), "## Project folder") || !strings.Contains(string(body), "/home/x/dev/project") {
		t.Fatalf("regenerated CLAUDE.md dropped the project folder:\n%s", body)
	}
}

type workRootTestProfiles struct{}

func (workRootTestProfiles) GetOrDefault(string) (*store.AgentProfile, error) {
	return &store.AgentProfile{Name: "worker", Slug: "worker", SystemPrompt: "base prompt"}, nil
}

// assertClaudeLaunchCarriesWorkRoot plants the claude boot dir the same
// way Boot does for a session with Options.Workdir = workRoot, and checks
// what the claude process would read: --add-dir rides argv (pinned in
// internal/runtime/agent's work_root_test.go), the planted settings grant
// the root, and CLAUDE.md names it.
func assertClaudeLaunchCarriesWorkRoot(t *testing.T, sessionID, workRoot string) {
	t.Helper()
	bootDir := plantForWorkRoot(t, "claude", sessionID, workRoot)
	raw, err := os.ReadFile(filepath.Join(bootDir, ".claude", "settings.json")) //nolint:gosec // reads a file this test just planted into a temp dir
	if err != nil {
		t.Fatalf("read settings.json: %v", err)
	}
	var settings struct {
		Permissions struct {
			AdditionalDirectories []string `json:"additionalDirectories"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal(raw, &settings); err != nil {
		t.Fatalf("parse settings.json: %v", err)
	}
	if dirs := settings.Permissions.AdditionalDirectories; len(dirs) == 0 || dirs[0] != workRoot {
		t.Fatalf("claude additionalDirectories = %v, want %q first", dirs, workRoot)
	}
	assertNamesWorkRoot(t, filepath.Join(bootDir, "CLAUDE.md"), workRoot)
}

func assertCodexLaunchCarriesWorkRoot(t *testing.T, sessionID, workRoot string) {
	t.Helper()
	t.Setenv("CODEX_HOME", t.TempDir())
	bootDir := plantForWorkRoot(t, "codex", sessionID, workRoot)
	raw, err := os.ReadFile(filepath.Join(bootDir, "config.toml")) //nolint:gosec // reads a file this test just planted into a temp dir
	if err != nil {
		t.Fatalf("read config.toml: %v", err)
	}
	if want := `writable_roots = ["` + workRoot + `"]`; !strings.Contains(string(raw), want) {
		t.Fatalf("codex config.toml missing %q:\n%s", want, raw)
	}
	assertNamesWorkRoot(t, filepath.Join(bootDir, "AGENTS.md"), workRoot)
}

func plantForWorkRoot(t *testing.T, provider, sessionID, workRoot string) string {
	t.Helper()
	layout, params, err := runtimeagent.ResolveBootdirParams(
		&runtimeagent.Dependencies{Agents: workRootTestProfiles{}},
		runtimeagent.Options{Provider: provider, SessionID: sessionID, Workdir: workRoot},
		sessionID,
	)
	if err != nil {
		t.Fatalf("ResolveBootdirParams: %v", err)
	}
	bootDir, err := layout.Setup(params)
	if err != nil {
		t.Fatalf("%s Setup: %v", provider, err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(bootDir) })
	return bootDir
}

func assertNamesWorkRoot(t *testing.T, path, workRoot string) {
	t.Helper()
	body, err := os.ReadFile(path) //nolint:gosec // reads a file this test just planted into a temp dir
	if err != nil {
		t.Fatalf("read %s: %v", filepath.Base(path), err)
	}
	if !strings.Contains(string(body), "## Project folder") || !strings.Contains(string(body), workRoot) {
		t.Fatalf("%s does not name the work root %q:\n%s", filepath.Base(path), workRoot, body)
	}
}

func canonicalTempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	return dir
}
