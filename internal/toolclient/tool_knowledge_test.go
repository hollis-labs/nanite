package toolclient

import (
	"testing"
)

func TestDefaultToolKnowledge_HasCategories(t *testing.T) {
	tk := DefaultToolKnowledge()

	requiredCategories := []string{
		"automation",
		"context",
		"developer",
		"general",
	}

	for _, cat := range requiredCategories {
		entries, ok := tk.Categories[cat]
		if !ok {
			t.Errorf("missing required category %q", cat)
			continue
		}
		if len(entries) == 0 {
			t.Errorf("category %q has no entries", cat)
		}
	}

	if len(tk.Categories) < 4 {
		t.Errorf("expected at least 4 categories, got %d", len(tk.Categories))
	}

	// Verify every entry has required fields populated.
	for cat, entries := range tk.Categories {
		for _, e := range entries {
			if e.Name == "" {
				t.Errorf("category %q has entry with empty Name", cat)
			}
			if e.ShortDescription == "" {
				t.Errorf("tool %q has empty ShortDescription", e.Name)
			}
			if len(e.ShortDescription) > 80 {
				t.Errorf("tool %q ShortDescription exceeds 80 chars (%d): %q", e.Name, len(e.ShortDescription), e.ShortDescription)
			}
			if e.Server == "" {
				t.Errorf("tool %q has empty Server", e.Name)
			}
			if len(e.UseCases) < 2 {
				t.Errorf("tool %q has fewer than 2 use cases (%d)", e.Name, len(e.UseCases))
			}
		}
	}
}

func TestForIntent_MatchesRelevantTools(t *testing.T) {
	tk := DefaultToolKnowledge()

	tests := []struct {
		intent    string
		wantAny   []string // at least one of these should appear
		wantNone  []string // none of these should appear
	}{
		{
			intent:  "build pipeline deploy",
			wantAny: []string{"hadron_run_enqueue", "hadron_pipeline_enqueue", "cerberus_build"},
		},
		{
			intent:  "context knowledge store",
			wantAny: []string{"context_write", "context_view"},
		},
		{
			intent:  "service logs debug",
			wantAny: []string{"cerberus_logs"},
		},
		{
			intent: "",
			// empty intent should return nothing
		},
	}

	for _, tc := range tests {
		t.Run(tc.intent, func(t *testing.T) {
			results := tk.ForIntent(tc.intent)

			if tc.intent == "" {
				if len(results) != 0 {
					t.Errorf("empty intent should return no results, got %d", len(results))
				}
				return
			}

			resultNames := make(map[string]bool)
			for _, r := range results {
				resultNames[r.Name] = true
			}

			// Check that at least one wanted tool appears.
			if len(tc.wantAny) > 0 {
				found := false
				for _, want := range tc.wantAny {
					if resultNames[want] {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("intent %q: expected at least one of %v in results, got %v", tc.intent, tc.wantAny, names(results))
				}
			}

			// Check that unwanted tools do not appear.
			for _, noWant := range tc.wantNone {
				if resultNames[noWant] {
					t.Errorf("intent %q: did not expect %q in results", tc.intent, noWant)
				}
			}
		})
	}
}

func TestSummary_IsCompact(t *testing.T) {
	tk := DefaultToolKnowledge()
	summary := tk.Summary()

	if len(summary) == 0 {
		t.Fatal("Summary() returned empty string")
	}

	if len(summary) > 2000 {
		t.Errorf("Summary() is %d chars, expected under 2000", len(summary))
	}

	// Verify it contains category headers.
	requiredHeaders := []string{"[automation]", "[context]", "[developer]", "[general]"}
	for _, header := range requiredHeaders {
		if !containsStr2(summary, header) {
			t.Errorf("Summary() missing category header %q", header)
		}
	}
}

// helpers

func names(entries []ToolEntry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.Name
	}
	return out
}

func containsStr2(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (haystack == needle || len(needle) == 0 || findSubstring(haystack, needle))
}

func findSubstring(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
