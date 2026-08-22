package agent

import (
	"path/filepath"
	"testing"
)

// TestClassification_IsWritablePath_AcceptsFileInsideRoot pins the happy
// path: a file directly inside a configured managed root is writable.
func TestClassification_IsWritablePath_AcceptsFileInsideRoot(t *testing.T) {
	projectRoot := t.TempDir()
	c := NewClassification(projectRoot, "")
	ref := filepath.Join(projectRoot, "agents", "atlas.md")
	if !c.IsWritablePath(ref) {
		t.Fatalf("IsWritablePath(%q) = false, want true (inside %q)", ref, projectRoot)
	}
}

// TestClassification_IsWritablePath_RejectsSiblingButNotEqualDir is
// GO-AGENT-002's own recommended negative case: source_class.go's
// IsWritablePath does a strict dir == root equality check
// (internal/agent/source_class.go:122-127), not a prefix match — a
// sibling directory whose name merely starts with the root's name
// (<root>-evil) must NOT be misclassified as inside root. A naive
// strings.HasPrefix(dir, root) check would wrongly accept this.
func TestClassification_IsWritablePath_RejectsSiblingButNotEqualDir(t *testing.T) {
	projectRoot := t.TempDir()
	c := NewClassification(projectRoot, "")
	siblingDir := projectRoot + "-evil"
	ref := filepath.Join(siblingDir, "agents", "atlas.md")
	if c.IsWritablePath(ref) {
		t.Fatalf("IsWritablePath(%q) = true, want false (sibling of, not inside, %q)", ref, projectRoot)
	}
}

// TestClassification_IsWritablePath_RejectsEmbeddedAndEmpty covers the two
// early-return branches.
func TestClassification_IsWritablePath_RejectsEmbeddedAndEmpty(t *testing.T) {
	c := NewClassification(t.TempDir(), "")
	if c.IsWritablePath("") {
		t.Error("empty ref should not be writable")
	}
	if c.IsWritablePath("embedded:profiles/default.md") {
		t.Error("embedded ref should not be writable")
	}
}

// TestClassification_Classify covers the three source-driven branches
// (internal/plugin are fixed regardless of path; file-backed sources use
// IsWritablePath; a DB-only agent with no SourceRef is always managed) plus
// the "managed provenance, unresolvable path" fallback.
func TestClassification_Classify(t *testing.T) {
	projectRoot := t.TempDir()
	c := NewClassification(projectRoot, "")
	managedRef := filepath.Join(projectRoot, "agents", "atlas.md")
	outsideRef := filepath.Join(projectRoot+"-evil", "agents", "atlas.md")

	cases := []struct {
		name      string
		source    string
		sourceRef string
		want      ManageClass
	}{
		{"internal is always internal", "internal", managedRef, ManageClassInternal},
		{"plugin is always plugin", "plugin", managedRef, ManageClassPlugin},
		{"file inside managed root", "project", managedRef, ManageClassManaged},
		{"managed provenance outside every root still classifies managed", "project", outsideRef, ManageClassManaged},
		{"non-managed provenance outside every root is external", "adapter", outsideRef, ManageClassExternal},
		{"db-only operator agent (no file) is managed", "user", "", ManageClassManaged},
		{"embedded non-internal def is managed", "user", "embedded:profiles/x.md", ManageClassManaged},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := c.Classify(tc.source, tc.sourceRef)
			if got != tc.want {
				t.Errorf("Classify(%q, %q) = %q, want %q", tc.source, tc.sourceRef, got, tc.want)
			}
		})
	}
}

// TestManageClass_EditableAndCopyToManagedAllowed pins the two small
// ManageClass predicate methods.
func TestManageClass_EditableAndCopyToManagedAllowed(t *testing.T) {
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
