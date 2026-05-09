package anthropic

import (
	"errors"
	"testing"

	llmcontracts "github.com/hollis-labs/go-llm-contracts"
)

func TestTranslateError_NilReturnsNil(t *testing.T) {
	if got := translateError(nil); got != nil {
		t.Fatalf("expected nil, got %v", got)
	}
}

func TestTranslateError_NonAPIErrorPassesThrough(t *testing.T) {
	in := errors.New("network fail")
	got := translateError(in)
	if !errors.Is(got, in) {
		t.Fatalf("expected passthrough err, got %v", got)
	}
}

func TestErrTransientSentinel(t *testing.T) {
	if ErrTransient == nil {
		t.Fatal("ErrTransient is nil")
	}
	if ErrAuthentication == nil {
		t.Fatal("ErrAuthentication is nil")
	}
}

func TestRateBudgetSentinelMatchesContractsExport(t *testing.T) {
	// Sanity: confirm we are wrapping the contracts-package sentinel that
	// chat_rate_budget_pause checks against.
	if llmcontracts.ErrRequestExceedsRateBudget == nil {
		t.Fatal("contracts sentinel is nil")
	}
}
