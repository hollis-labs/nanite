package agent

import "testing"

func TestClassificationClassifiesDatabaseOwnershipFromProvenance(t *testing.T) {
	c := NewClassification()
	cases := []struct {
		source string
		want   ManageClass
	}{
		{"internal", ManageClassInternal},
		{"builtin", ManageClassInternal},
		{"plugin", ManageClassPlugin},
		{"external", ManageClassExternal},
		{"adapter", ManageClassExternal},
		{"claude", ManageClassExternal},
		{"user", ManageClassManaged},
		{"project", ManageClassManaged},
		{"api", ManageClassManaged},
		{"cli", ManageClassManaged},
		{"nanite", ManageClassManaged},
		{"managed_file", ManageClassManaged}, // retained historical provenance
		{"", ManageClassManaged},
	}
	for _, tc := range cases {
		if got := c.Classify(tc.source); got != tc.want {
			t.Errorf("Classify(%q) = %q, want %q", tc.source, got, tc.want)
		}
	}
}

func TestManageClassPredicates(t *testing.T) {
	if !ManageClassManaged.Editable() {
		t.Error("managed should be editable")
	}
	for _, c := range []ManageClass{ManageClassInternal, ManageClassPlugin, ManageClassExternal} {
		if c.Editable() {
			t.Errorf("%q should not be editable", c)
		}
	}
	if ManageClassInternal.CopyToManagedAllowed() {
		t.Error("internal must never offer copy-to-managed")
	}
	if !ManageClassPlugin.CopyToManagedAllowed() || !ManageClassExternal.CopyToManagedAllowed() {
		t.Error("plugin and external should both offer copy-to-managed")
	}
}
