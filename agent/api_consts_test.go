package agent

import (
	"testing"

	"github.com/hollis-labs/nanite/internal/definition"
)

func TestUseDeskMatchesTheDefinition(t *testing.T) {
	if UseDesk != definition.UseDesk {
		t.Fatalf("agent.UseDesk %q, definition %q", UseDesk, definition.UseDesk)
	}
}
