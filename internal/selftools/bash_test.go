package selftools

import (
	"strings"
	"testing"
)

func TestBashPreflight(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{
		{`{"command":"shutdown"}`, "blocked by denylist"},
		{`{"command":"printf hi","timeout_seconds":601}`, "between 1 and 600"},
		{`{"command":"printf hi","timeout_seconds":0}`, "between 1 and 600"},
		{`{"command":"printf hi"}`, ""},
	} {
		_, err := ParseBash([]byte(tc.raw))
		if (tc.want == "" && err != nil) || (tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want))) {
			t.Errorf("%s: %v", tc.raw, err)
		}
	}
}
