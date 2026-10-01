package worker

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/go-worktree"
	"github.com/hollis-labs/nanite/internal/chat"
)

type worktreeDelegator func(context.Context, chat.DelegationRequest) (*chat.DelegationResult, error)

func (f worktreeDelegator) DelegateTask(ctx context.Context, req chat.DelegationRequest) (*chat.DelegationResult, error) {
	return f(ctx, req)
}

func worktreeGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=Test", "-c", "user.email=test@example.com", "-c", "commit.gpgsign=false", "-c", "core.hooksPath=/dev/null"}, args...)...) //nolint:gosec // Fixed test git commands in temporary repositories.
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %s: %v", args, out, err)
	}
	return string(out)
}

func TestSpawnFull_WorktreeCleanupPreservesWork(t *testing.T) {
	for _, mode := range []string{"clean", "dirty", "committed", "locked", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			repo := t.TempDir()
			worktreeGit(t, repo, "init", "-b", "main")
			worktreeGit(t, repo, "commit", "--allow-empty", "-m", "initial")
			base := t.TempDir()
			wtMgr, initErr := worktree.New(repo, worktree.Nanite(base)...)
			if initErr != nil {
				t.Fatal(initErr)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var checkout worktree.Worktree
			mgr := newTestManager(worktreeDelegator(func(ctx context.Context, _ chat.DelegationRequest) (*chat.DelegationResult, error) {
				list, err := wtMgr.List(ctx)
				if err != nil || len(list) != 1 {
					t.Fatalf("worker checkout: %+v, %v", list, err)
				}
				checkout = list[0]
				if checkout.Path != filepath.Join(base, checkout.ID) || checkout.Branch != "worker-"+checkout.ID {
					t.Fatalf("Nanite placement/branch: %+v", checkout)
				}
				switch mode {
				case "dirty", "committed":
					if err := os.WriteFile(filepath.Join(checkout.Path, "worker.txt"), []byte("worker output"), 0o600); err != nil {
						t.Fatal(err)
					}
					if mode == "committed" {
						worktreeGit(t, checkout.Path, "add", "worker.txt")
						worktreeGit(t, checkout.Path, "commit", "-m", "worker output")
					}
				case "locked":
					worktreeGit(t, repo, "worktree", "lock", checkout.Path)
				case "canceled":
					cancel()
					return nil, ctx.Err()
				}
				return &chat.DelegationResult{Success: true, WorkerSessionID: "session", Content: "done"}, nil
			}))
			mgr.worktrees = wtMgr
			result, err := mgr.SpawnFull(ctx, SpawnRequest{Isolation: "worktree", AgentID: "agent"})
			if err != nil || result == nil {
				t.Fatalf("spawn: %+v, %v", result, err)
			}
			if result.WorkerID != checkout.ID {
				t.Fatalf("checkout %q does not belong to worker %q", checkout.ID, result.WorkerID)
			}
			_, statErr := os.Stat(checkout.Path)
			if mode == "clean" || mode == "canceled" {
				if !errors.Is(statErr, os.ErrNotExist) {
					t.Fatalf("clean checkout survived cleanup: %v", statErr)
				}
			} else {
				if statErr != nil {
					t.Fatalf("work was deleted during cleanup: %v", statErr)
				}
				// Startup uses this policy; another sweep must also keep work.
				report, err := wtMgr.Sweep(context.Background(), worktree.OrphanedBy(nil), worktree.SweepOptions{})
				if err != nil || len(report.Removed) != 0 || len(report.Kept) != 1 {
					t.Fatalf("startup sweep lost work: %+v, %v", report, err)
				}
				if mode == "dirty" || mode == "committed" {
					data, err := os.ReadFile(filepath.Join(checkout.Path, "worker.txt"))
					if err != nil || string(data) != "worker output" {
						t.Fatalf("worker output lost: %q, %v", data, err)
					}
				}
			}
		})
	}
}

func TestSpawnFull_WorktreeUnavailableFailsBeforeDelegation(t *testing.T) {
	deleg := &stubDelegator{}
	mgr := newTestManager(deleg)
	if result, err := mgr.SpawnFull(context.Background(), SpawnRequest{Isolation: "worktree"}); err == nil || result != nil {
		t.Fatalf("unavailable isolation: %+v, %v", result, err)
	}
	if deleg.calls.Load() != 0 || len(mgr.sem) != 0 {
		t.Fatal("failed isolation delegated work or retained its capacity slot")
	}
	// A failed isolation attempt must not prevent an ordinary worker.
	if _, err := mgr.SpawnFull(context.Background(), SpawnRequest{}); err != nil {
		t.Fatal(err)
	}
}
