package api

import "testing"

func TestVisibleAgentSlugs(t *testing.T) {
	cases := []struct {
		name string
		env  string
		want []string // nil means "no filter"
	}{
		{"unset means every agent is listed", "", nil},
		{"whitespace only is also no filter", "   ", nil},
		{"commas with nothing between them are no filter", " , , ", nil},
		{"one slug", "workday-ess-copy", []string{"workday-ess-copy"}},
		{"several, with spacing", " a , b ,c ", []string{"a", "b", "c"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("NANITE_AGENT_SLUGS", tc.env)
			got := visibleAgentSlugs()
			if tc.want == nil {
				if got != nil {
					t.Fatalf("expected no filter, got %v", got)
				}
				return
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for _, s := range tc.want {
				if !got[s] {
					t.Fatalf("%q missing from %v", s, got)
				}
			}
		})
	}
}
