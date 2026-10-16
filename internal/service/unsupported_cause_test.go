package service

import (
	"errors"
	"fmt"
	"testing"
)

func TestUnsupportedCauseKeepsSentinelAndCode(t *testing.T) {
	def := fmt.Errorf("wrapped: %w", unsupportedCause("runtime_not_api", "runtime %q", "cli"))
	if !errors.Is(def, ErrUnsupportedDefinition) || errors.Is(def, ErrUnsupportedModel) || CauseCode(def) != "runtime_not_api" {
		t.Fatalf("definition cause: %v code=%q", def, CauseCode(def))
	}
	mod := unsupportedModelCause("model_not_selected", "pick one")
	if !errors.Is(mod, ErrUnsupportedModel) || errors.Is(mod, ErrUnsupportedDefinition) || CauseCode(mod) != "model_not_selected" {
		t.Fatalf("model cause: %v", mod)
	}
	if got := mod.Error(); got != ErrUnsupportedModel.Error()+": pick one" {
		t.Fatalf("message changed: %q", got)
	}
	if CauseCode(ErrUnsupportedModel) != "" || CauseCode(nil) != "" {
		t.Fatal("uncoded errors must report no code")
	}
}
