package recover

import (
	"encoding/json"
	"strings"
	"testing"
)

// A host tool whose schema refers back to itself fails each call with a clear
// error instead of overflowing the stack inside validation.
func TestCyclicToolSchemaFailsTheCallNotTheProcess(t *testing.T) {
	v := NewValidator(json.RawMessage(`{"type":"object","allOf":[{"$ref":"#"}]}`))
	err := v.Validate(map[string]any{"x": 1})
	if err == nil || !strings.Contains(err.Error(), "invalid tool schema") || !strings.Contains(err.Error(), "refers back to itself") {
		t.Fatalf("Validate = %v", err)
	}
}
