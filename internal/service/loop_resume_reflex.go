package service

import (
	"context"

	"github.com/hollis-labs/nanite/internal/agent/reflexes"
	"github.com/hollis-labs/nanite/internal/store"
)

// LoopRunResumer is retained for callers of the unavailable mutable-reflex seam.
type LoopRunResumer interface {
	ResumeLoopRun(context.Context, string) error
}

// EvaluateLoopRunResumeReflexes refuses before reading retained loop or reflex
// rows. Runtime reflexes come from an immutable definition pin; loop-resume is
// not a supported action in the adopted handler contract.
func EvaluateLoopRunResumeReflexes(context.Context, *reflexes.Engine, LoopRunResumer, string, reflexes.State) (bool, bool, error) {
	return false, false, store.ErrImmutableAgentProfile
}
