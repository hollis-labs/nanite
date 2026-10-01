package main

import (
	"os"

	"github.com/hollis-labs/go-worktree"
)

func newWorkerWorktreeManager(baseDir string) (*worktree.Manager, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	root, err := worktree.FindRepoRoot(cwd)
	if err != nil {
		return nil, err
	}
	return worktree.New(root, worktree.Nanite(baseDir)...)
}
