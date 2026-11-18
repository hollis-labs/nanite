package skills

import "testing"

func TestListingContainsOnlyNameAndDescription(t *testing.T) {
	var groups Groups
	if err := groups.SetGroup("project", Skill{Name: "demo", Description: "A sample", Body: "private instructions"}); err != nil {
		t.Fatal(err)
	}
	if got := groups.Listing(); got != "- demo: A sample" {
		t.Fatalf("listing = %q", got)
	}
}
