package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRemoveAgentrcSection_Absent(t *testing.T) {
	input := "# Project\n\nSome content.\n"
	got, removed := RemoveAgentrcSection(input)
	if got != input {
		t.Errorf("unchanged input was modified: %q", got)
	}
	if removed != "" {
		t.Errorf("removed non-empty: %q", removed)
	}
}

func TestRemoveAgentrcSection_AtEnd(t *testing.T) {
	input := "# Project\n\nUser stuff.\n\n## agentrc\n\n" +
		"- If `.agentrc/boot-prompt.md` exists, read it first.\n" +
		"- More agentrc instructions.\n"
	got, removed := RemoveAgentrcSection(input)
	want := "# Project\n\nUser stuff.\n"
	if got != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
	if !strings.Contains(removed, "agentrc/boot-prompt") {
		t.Errorf("removed content missing expected text: %q", removed)
	}
}

func TestRemoveAgentrcSection_FollowedByAnotherHeading(t *testing.T) {
	input := "# Project\n\n## agentrc\n\nStuff.\n\n## Other\n\nKeep me.\n"
	got, _ := RemoveAgentrcSection(input)
	want := "# Project\n\n## Other\n\nKeep me.\n"
	if got != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
}

func TestRemoveAgentrcSection_CaseInsensitive(t *testing.T) {
	input := "# Project\n\n## AGENTRC\n\nstuff\n"
	got, _ := RemoveAgentrcSection(input)
	want := "# Project\n"
	if got != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
}

func TestRemoveAgentrcSection_H3Level(t *testing.T) {
	input := "# Project\n\n## Setup\n\n### agentrc\n\nstuff\n\n## Other\n\nkeep\n"
	got, _ := RemoveAgentrcSection(input)
	want := "# Project\n\n## Setup\n\n## Other\n\nkeep\n"
	if got != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
}

func TestUpdateCLAUDEmd_FreshFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "CLAUDE.md")
	managed := "## Nanite agents\n\nSee .nanite/ for agent config."

	report, err := UpdateCLAUDEmd(path, managed, nil)
	if err != nil {
		t.Fatalf("UpdateCLAUDEmd: %v", err)
	}
	if !report.Created {
		t.Error("expected Created=true for missing file")
	}

	content, _ := os.ReadFile(path)
	if !strings.Contains(string(content), "<!-- nanite:start -->") {
		t.Errorf("missing start marker: %q", content)
	}
	if !strings.Contains(string(content), "See .nanite/ for agent config") {
		t.Errorf("missing managed content: %q", content)
	}
}

func TestUpdateCLAUDEmd_RemovesAgentrcSection(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "CLAUDE.md")
	original := "# Project\n\nUser stuff.\n\n## agentrc\n\n- old loader instructions\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	snapshotDir := t.TempDir()
	managed := "New managed content."
	report, err := UpdateCLAUDEmd(path, managed, &CLAUDESnapshotOpts{Dir: snapshotDir})
	if err != nil {
		t.Fatalf("UpdateCLAUDEmd: %v", err)
	}
	if !report.RemovedAgentrcSection {
		t.Error("expected RemovedAgentrcSection=true")
	}
	if report.Created {
		t.Error("expected Created=false for existing file")
	}

	content, _ := os.ReadFile(path)
	if strings.Contains(string(content), "## agentrc") {
		t.Errorf("agentrc section not removed: %q", content)
	}
	if !strings.Contains(string(content), "User stuff.") {
		t.Errorf("user content lost: %q", content)
	}
	if !strings.Contains(string(content), "<!-- nanite:start -->") {
		t.Errorf("managed section not added: %q", content)
	}

	// Snapshot file should exist with the removed section.
	snapshot, err := os.ReadFile(filepath.Join(snapshotDir, "removed-claude-section.md"))
	if err != nil {
		t.Fatalf("snapshot missing: %v", err)
	}
	if !strings.Contains(string(snapshot), "old loader instructions") {
		t.Errorf("snapshot missing removed content: %q", snapshot)
	}
}

func TestUpdateCLAUDEmd_PreservesUserContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "CLAUDE.md")
	original := "# Project\n\n## Our conventions\n\n- use gofmt\n- no globals\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	report, err := UpdateCLAUDEmd(path, "managed body", nil)
	if err != nil {
		t.Fatal(err)
	}
	if report.RemovedAgentrcSection {
		t.Error("RemovedAgentrcSection true when there was none")
	}

	content, _ := os.ReadFile(path)
	// All original headings and bullets must survive.
	for _, want := range []string{"# Project", "## Our conventions", "- use gofmt", "- no globals", "<!-- nanite:start -->"} {
		if !strings.Contains(string(content), want) {
			t.Errorf("missing expected content %q in:\n%q", want, content)
		}
	}
}

func TestUpdateCLAUDEmd_Idempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "CLAUDE.md")
	os.WriteFile(path, []byte("# Project\n\nUser content.\n"), 0o644)

	if _, err := UpdateCLAUDEmd(path, "managed", nil); err != nil {
		t.Fatal(err)
	}
	first, _ := os.ReadFile(path)

	if _, err := UpdateCLAUDEmd(path, "managed", nil); err != nil {
		t.Fatal(err)
	}
	second, _ := os.ReadFile(path)

	if string(first) != string(second) {
		t.Errorf("non-idempotent:\nfirst:\n%q\nsecond:\n%q", first, second)
	}
}
