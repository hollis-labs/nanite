package intent

import (
	"context"
	"reflect"
	"sort"
	"testing"
)

var allCats = []string{"search", "code-exec", "core-io", "http", "agent", "session", "context", "mode"}

func TestRulesClassifier_MatchesAndClears(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		wantHit  bool
		wantCats []string
	}{
		{"search intent", "please search for the foo function", true, []string{"search"}},
		{"look up", "can you look up the ticket", true, []string{"search"}},
		{"run command", "run make test", true, []string{"code-exec"}},
		{"bash", "give me the bash output", true, []string{"code-exec"}},
		{"read file", "read the readme file", true, []string{"core-io"}},
		{"edit file", "edit main.go", true, []string{"core-io"}},
		{"fetch url", "fetch https://example.com", true, []string{"http"}},
		{"recall memory", "recall what we said about auth", true, []string{"context"}},
		{"switch mode", "switch to plan mode", true, []string{"mode"}},
		{"empty", "", false, nil},
		{"ambient", "thanks!", false, nil},
		{"weak single", "find it", false, nil}, // "find" alone is 0.5 < 0.6 threshold
	}

	c := NewRulesClassifier()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, err := c.Classify(context.Background(), Input{UserTurn: tc.input, AvailableCategories: allCats})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if r.Hydrate != tc.wantHit {
				t.Fatalf("hydrate: got %v, want %v (reasoning=%q)", r.Hydrate, tc.wantHit, r.Reasoning)
			}
			if tc.wantHit {
				sort.Strings(r.Categories)
				if !reflect.DeepEqual(r.Categories, tc.wantCats) {
					t.Fatalf("categories: got %v, want %v", r.Categories, tc.wantCats)
				}
			}
		})
	}
}

func TestRulesClassifier_OnlyConsidersAvailableCategories(t *testing.T) {
	c := NewRulesClassifier()
	// "read" would normally score core-io, but we pretend only "search" is available.
	r, err := c.Classify(context.Background(), Input{
		UserTurn:            "read the file",
		AvailableCategories: []string{"search"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.Hydrate {
		t.Fatalf("should not hydrate when matching category isn't available; got %+v", r)
	}
}

func TestRulesClassifier_MultipleCategories(t *testing.T) {
	c := NewRulesClassifier()
	r, err := c.Classify(context.Background(), Input{
		UserTurn:            "search for the script and then run it",
		AvailableCategories: allCats,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !r.Hydrate {
		t.Fatalf("expected hydrate=true, got %+v", r)
	}
	sort.Strings(r.Categories)
	want := []string{"code-exec", "search"}
	if !reflect.DeepEqual(r.Categories, want) {
		t.Fatalf("categories: got %v, want %v", r.Categories, want)
	}
}

// TestRulesClassifier_HTTPMethodCaseInsensitive locks in the fix for Copilot
// review #3095049742 — the HTTP method pattern must be lowercase so that
// uppercase inputs still match after the classifier lowercases the text.
func TestRulesClassifier_HTTPMethodCaseInsensitive(t *testing.T) {
	c := NewRulesClassifier()
	cases := []string{
		"GET /api/users",
		"POST the data",
		"PUT /resource",
		"DELETE /thing",
		"get the page",
		"do a post request",
	}
	for _, input := range cases {
		t.Run(input, func(t *testing.T) {
			r, err := c.Classify(context.Background(), Input{UserTurn: input, AvailableCategories: allCats})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !r.Hydrate {
				t.Fatalf("HTTP method in %q should hydrate; got %+v", input, r)
			}
			found := false
			for _, cat := range r.Categories {
				if cat == "http" {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("HTTP category should be picked for %q; got %v", input, r.Categories)
			}
		})
	}
}

func TestRulesClassifier_ConfidenceReflectsBestScore(t *testing.T) {
	c := NewRulesClassifier()
	// "find" alone is 0.5 — under threshold but reported as confidence.
	r, err := c.Classify(context.Background(), Input{UserTurn: "find it", AvailableCategories: allCats})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.Hydrate {
		t.Fatalf("should not hydrate on single-weak match")
	}
	if r.Confidence <= 0 || r.Confidence >= DefaultRulesThreshold {
		t.Fatalf("expected confidence in (0, %v), got %v", DefaultRulesThreshold, r.Confidence)
	}
}
