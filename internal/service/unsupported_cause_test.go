package service

import (
	"errors"
	"strings"
	"testing"
)

// The 422 bodies carry err.Error(), so the cause and remedy must be in it.
func TestUnregisteredProviderErrorNamesKeyRemedy(t *testing.T) {
	err := UnregisteredProviderError(ErrUnsupportedModel, "anthropic")
	if !errors.Is(err, ErrUnsupportedModel) || !strings.Contains(err.Error(), "ANTHROPIC_API_KEY") || !strings.Contains(err.Error(), "restart") {
		t.Fatalf("err = %v", err)
	}
	err = UnregisteredProviderError(ErrUnsupportedModel, "nowhere")
	if !strings.Contains(err.Error(), "anthropic, openai") {
		t.Fatalf("unknown provider should list the native ones: %v", err)
	}
}
