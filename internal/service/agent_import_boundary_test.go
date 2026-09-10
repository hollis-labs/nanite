package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	agentpkg "github.com/hollis-labs/nanite/internal/agent"
	"github.com/hollis-labs/nanite/internal/agentimport"
	"github.com/hollis-labs/nanite/internal/store"
)

// CW-20260910-0014: the read-only boundary for imported agents, exercised
// through the real import pipeline rather than a hand-built row, so the two
// halves cannot drift apart. internal/service/agent_config_test.go already
// covers the plugin and internal classes and a generic "adapter" external
// source; what these tests add is the boundary as an OPERATOR meets it,
// starting from a file on disk.

const boundaryDef = `---
name: Imported Reviewer
slug: imported-reviewer
description: Came from outside
roleTools:
  - dev_bash
procedures:
  - name: triage
    body: How to triage.
---
Imported system prompt.
`

// importFixture runs a real import of a real file and returns the resulting
// row plus the source path.
func importFixture(t *testing.T, st *store.Store, body string) (*store.AgentProfile, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "imported-reviewer.md")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	imp := &agentimport.Importer{Store: st, SeedChildren: SeedImportedAgentChildren(st)}
	res, err := imp.Import(context.Background(), agentimport.Source{Path: path})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if created, _, _ := res.Counts(); created != 1 {
		t.Fatalf("want 1 created, got %+v", res.Outcomes)
	}
	row, err := st.GetAgentBySlug(context.Background(), "imported-reviewer")
	if err != nil {
		t.Fatalf("GetAgentBySlug: %v", err)
	}
	// Import resolves the operator-named path to an absolute one.
	abs, _ := filepath.Abs(path)
	return row, abs
}

// TestImportedAgentIsReadOnlyAndPointsAtTheWayOut is the boundary test the
// task asks for: an Update on an imported row must be refused, and the
// refusal must point at CopyToManaged rather than dead-ending.
func TestImportedAgentIsReadOnlyAndPointsAtTheWayOut(t *testing.T) {
	svc, st, _ := newAgentConfigTestService(t)
	row, _ := importFixture(t, st, boundaryDef)

	if class := svc.Classify(row); class != agentpkg.ManageClassExternal {
		t.Fatalf("imported row classifies as %q, want external", class)
	}

	updated := *row
	updated.SystemPrompt = "edited in place — must not land"
	_, err := svc.Update(row, &updated, nil, "")
	if !errors.Is(err, ErrAgentNotManaged) {
		t.Fatalf("Update error = %v, want ErrAgentNotManaged", err)
	}
	if !strings.Contains(err.Error(), "CopyToManaged") {
		t.Errorf("refusal %q does not name the way out; a read-only boundary with no exit reads as a dead end", err)
	}

	after, _ := st.GetAgentBySlug(context.Background(), "imported-reviewer")
	if after.SystemPrompt != "Imported system prompt." {
		t.Errorf("the refused Update still landed: %q", after.SystemPrompt)
	}
}

// TestImportedAgentDeleteIsRefusedToo — Delete goes through the same gate.
// Deleting an imported row would be a quiet way around read-only-in-place.
func TestImportedAgentDeleteIsRefusedToo(t *testing.T) {
	svc, st, _ := newAgentConfigTestService(t)
	row, _ := importFixture(t, st, boundaryDef)

	if err := svc.Delete(row); !errors.Is(err, ErrAgentNotManaged) {
		t.Fatalf("Delete error = %v, want ErrAgentNotManaged", err)
	}
	if _, err := st.GetAgentBySlug(context.Background(), "imported-reviewer"); err != nil {
		t.Errorf("imported row was deleted despite the refusal: %v", err)
	}
}

// TestCopyImportedToManagedProducesAnEditableCopy walks the whole way out:
// import → refused edit → copy → edit the copy. The imported row must be
// unchanged at the end, still standing as the honest record of what arrived.
func TestCopyImportedToManagedProducesAnEditableCopy(t *testing.T) {
	svc, st, _ := newAgentConfigTestService(t)
	row, sourcePath := importFixture(t, st, boundaryDef)

	res, err := svc.CopyToManaged(row, nil)
	if err != nil {
		t.Fatalf("CopyToManaged: %v", err)
	}
	copyRow := res.Profile

	if copyRow.Slug != "imported-reviewer-copy" {
		t.Errorf("copy slug = %q, want %q", copyRow.Slug, "imported-reviewer-copy")
	}
	if copyRow.ID == row.ID {
		t.Error("copy reused the imported row's identity")
	}
	if !svc.Classify(copyRow).Editable() {
		t.Error("the copy must be editable — that is the entire point of the copy path")
	}
	// The copy is authored now, by the operator. It carries no claim about a
	// file on disk, and CopyToManaged never dereferences SourceRef.
	if copyRow.SourceRef != "" {
		t.Errorf("copy retained SourceRef %q; the copy is not from that file", copyRow.SourceRef)
	}
	if copyRow.ImportedAt != "" || copyRow.OriginSystem != "" {
		t.Errorf("copy retained import metadata: imported_at=%q origin_system=%q", copyRow.ImportedAt, copyRow.OriginSystem)
	}

	// Declared children come across, so the copy is usable rather than a
	// prompt with nothing attached.
	procs, err := st.ListAgentProcedures(context.Background(), copyRow.ID)
	if err != nil || len(procs) != 1 || procs[0].Name != "triage" {
		t.Errorf("copied procedures = %+v, %v", procs, err)
	}

	// Editing the copy works.
	edited := *copyRow
	edited.SystemPrompt = "edited on the copy"
	if _, err := svc.Update(copyRow, &edited, nil, ""); err != nil {
		t.Fatalf("Update on the copy: %v", err)
	}

	// And the imported row is exactly as it was.
	after, _ := st.GetAgentBySlug(context.Background(), "imported-reviewer")
	if after.SystemPrompt != "Imported system prompt." || after.Source != agentimport.SourceProvenance {
		t.Errorf("imported row changed: prompt=%q source=%q", after.SystemPrompt, after.Source)
	}
	if after.SourceRef != sourcePath {
		t.Errorf("SourceRef = %q, want the originating path %q preserved as provenance", after.SourceRef, sourcePath)
	}
}

// TestUniqueManagedSlugForRepeatedImportedCopies exercises the slug-uniquing
// path the task calls out. Copying the same imported agent twice must not
// collide, and must not silently overwrite the first copy.
func TestUniqueManagedSlugForRepeatedImportedCopies(t *testing.T) {
	svc, st, _ := newAgentConfigTestService(t)
	row, _ := importFixture(t, st, boundaryDef)

	first, err := svc.CopyToManaged(row, nil)
	if err != nil {
		t.Fatalf("first copy: %v", err)
	}
	second, err := svc.CopyToManaged(row, nil)
	if err != nil {
		t.Fatalf("second copy: %v", err)
	}

	if first.Profile.Slug != "imported-reviewer-copy" {
		t.Errorf("first copy slug = %q", first.Profile.Slug)
	}
	if second.Profile.Slug != "imported-reviewer-copy-2" {
		t.Errorf("second copy slug = %q, want %q", second.Profile.Slug, "imported-reviewer-copy-2")
	}
	if slugErr := agentpkg.ValidateSlug(second.Profile.Slug); slugErr != nil {
		t.Errorf("uniqued slug is not a valid slug: %v", slugErr)
	}
	if first.Profile.ID == second.Profile.ID {
		t.Error("the second copy overwrote the first")
	}

	// Three rows now: the imported original and two copies.
	rows, err := st.ListAgents(context.Background())
	if err != nil {
		t.Fatalf("ListAgents: %v", err)
	}
	var imported, copies int
	for _, r := range rows {
		switch {
		case r.Slug == "imported-reviewer":
			imported++
		case strings.HasPrefix(r.Slug, "imported-reviewer-copy"):
			copies++
		}
	}
	if imported != 1 || copies != 2 {
		t.Errorf("got %d imported + %d copies, want 1 + 2", imported, copies)
	}
}

// TestNothingInTheOwnershipPathTouchesTheSourceFile is the one-way rule held
// across the whole boundary, not only at import time. Import, a refused
// update, a copy, and an edit of the copy — the file on disk is byte-identical
// afterwards, and no minted identity was written into it.
func TestNothingInTheOwnershipPathTouchesTheSourceFile(t *testing.T) {
	svc, st, _ := newAgentConfigTestService(t)
	row, sourcePath := importFixture(t, st, boundaryDef)

	after := func() string {
		t.Helper()
		b, readErr := os.ReadFile(sourcePath) //nolint:gosec // reads the fixture this test just wrote into t.TempDir()
		if readErr != nil {
			t.Fatalf("read source: %v", readErr)
		}
		return string(b)
	}
	if after() != boundaryDef {
		t.Fatalf("the import itself changed the source file:\n%s", after())
	}

	updated := *row
	updated.Description = "nope"
	_, _ = svc.Update(row, &updated, nil, "")

	copied, err := svc.CopyToManaged(row, nil)
	if err != nil {
		t.Fatalf("CopyToManaged: %v", err)
	}
	edited := *copied.Profile
	edited.SystemPrompt = "edited"
	if _, err := svc.Update(copied.Profile, &edited, nil, ""); err != nil {
		t.Fatalf("Update copy: %v", err)
	}

	if got := after(); got != boundaryDef {
		t.Errorf("source file changed during the ownership path:\n%s", got)
	}
	if strings.Contains(after(), row.ID) || strings.Contains(after(), copied.Profile.ID) {
		t.Error("a minted identity was written back into the source file")
	}
}

// TestRefusalWordingByClass pins that a refusal offers the copy path only when
// one exists. Telling an operator to copy an internal harness profile they
// cannot copy is worse than the bare sentinel.
func TestRefusalWordingByClass(t *testing.T) {
	for _, tc := range []struct {
		source      string
		wantClass   agentpkg.ManageClass
		offersCopy  bool
		mustContain string
	}{
		{"import", agentpkg.ManageClassExternal, true, "CopyToManaged"},
		{"plugin", agentpkg.ManageClassPlugin, true, "CopyToManaged"},
		{"internal", agentpkg.ManageClassInternal, false, "no copy-to-managed path"},
	} {
		t.Run(tc.source, func(t *testing.T) {
			svc, st, _ := newAgentConfigTestService(t)
			p := &store.AgentProfile{Name: "X", Slug: "x-" + tc.source, SystemPrompt: "x", Source: tc.source}
			if err := st.CreateAgent(context.Background(), p); err != nil {
				t.Fatalf("seed: %v", err)
			}
			if class := svc.Classify(p); class != tc.wantClass {
				t.Fatalf("Classify(%q) = %q, want %q", tc.source, class, tc.wantClass)
			}

			updated := *p
			updated.Description = "nope"
			_, err := svc.Update(p, &updated, nil, "")
			if !errors.Is(err, ErrAgentNotManaged) {
				t.Fatalf("err = %v, want ErrAgentNotManaged", err)
			}
			if !strings.Contains(err.Error(), tc.mustContain) {
				t.Errorf("err = %q, want it to contain %q", err, tc.mustContain)
			}
			if !strings.Contains(err.Error(), tc.wantClass.Describe()) {
				t.Errorf("err = %q, does not say what is in the way", err)
			}
			if !tc.offersCopy && strings.Contains(err.Error(), "copy it to the managed layer") {
				t.Errorf("err = %q offers a copy path that does not exist for this class", err)
			}
		})
	}
}
