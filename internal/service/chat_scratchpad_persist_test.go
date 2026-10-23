package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScratchpadSurvivesATurnWithAWorkspace(t *testing.T) {
	state := t.TempDir()
	first := newTestLoopState()
	first.attachScratchpadFile(state)
	if err := first.scratchpadWrite("plan", map[string]any{"step": 2.0}); err != nil {
		t.Fatal(err)
	}
	if err := first.scratchpadWrite("note", "keep"); err != nil {
		t.Fatal(err)
	}
	if cleared, err := first.scratchpadClear("note"); !cleared || err != nil {
		t.Fatalf("clear = %v, %v; want true, nil", cleared, err)
	}

	second := newTestLoopState()
	second.attachScratchpadFile(state)
	got, _ := second.scratchpadRead("")
	if len(got) != 1 || got["plan"].(map[string]any)["step"] != 2.0 {
		t.Fatalf("restored scratchpad = %v, want only plan", got)
	}
	if second.scratchpadBytes == 0 {
		t.Fatal("restored scratchpad did not count its bytes against the cap")
	}
	info, err := os.Stat(filepath.Join(state, scratchpadStateFile))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("scratchpad file mode = %v, want 0600", info.Mode().Perm())
	}
}

func TestScratchpadWithoutAWorkspaceStaysPerTurn(t *testing.T) {
	ls := newTestLoopState()
	ls.attachScratchpadFile("")
	if err := ls.scratchpadWrite("k", "v"); err != nil {
		t.Fatal(err)
	}
	if got, _ := newTestLoopState().scratchpadRead(""); len(got) != 0 {
		t.Fatalf("a fresh loopState carried %v", got)
	}
}

func TestScratchpadIgnoresABadFile(t *testing.T) {
	for name, content := range map[string]string{
		"malformed": "{not json",
		"oversize":  `{"k":"` + strings.Repeat("x", scratchpadMaxValueBytes+1) + `"}`,
	} {
		t.Run(name, func(t *testing.T) {
			state := t.TempDir()
			path := filepath.Join(state, scratchpadStateFile)
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			ls := newTestLoopState()
			ls.attachScratchpadFile(state)
			if got, _ := ls.scratchpadRead(""); len(got) != 0 {
				t.Fatalf("restored %v from a bad file", got)
			}
			if err := ls.scratchpadWrite("k", "v"); err != nil {
				t.Fatalf("write after a bad file: %v", err)
			}
		})
	}
}

func TestScratchpadWriteRefusedWhenItCannotBeSaved(t *testing.T) {
	state := t.TempDir()
	ls := newTestLoopState()
	ls.attachScratchpadFile(state)
	if err := ls.scratchpadWrite("a", "1"); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(state); err != nil {
		t.Fatal(err)
	}
	if err := ls.scratchpadWrite("b", "2"); err == nil {
		t.Fatal("write succeeded although the workspace is gone")
	}
	if got, _ := ls.scratchpadRead(""); len(got) != 1 {
		t.Fatalf("unsaved write left in the buffer: %v", got)
	}
}

func TestScratchpadClearRefusedWhenItCannotBeSaved(t *testing.T) {
	state := t.TempDir()
	ls := newTestLoopState()
	ls.attachScratchpadFile(state)
	if err := ls.scratchpadWrite("keep", "saved"); err != nil {
		t.Fatal(err)
	}
	bytesBefore := ls.scratchpadBytes
	aside := state + ".aside"
	if err := os.Rename(state, aside); err != nil {
		t.Fatal(err)
	}
	if cleared, err := ls.scratchpadClear("keep"); cleared || err == nil {
		t.Fatalf("clear = %v, %v; want false and an error", cleared, err)
	}
	out, isErr := callScratchpadClear(map[string]any{"key": "keep"}, ls)
	if !isErr {
		t.Fatalf("clear tool reported success: %s", out)
	}
	if got, _ := ls.scratchpadRead("keep"); got["keep"] != "saved" || ls.scratchpadBytes != bytesBefore {
		t.Fatalf("failed clear not rolled back: %v, %d bytes (was %d)", got, ls.scratchpadBytes, bytesBefore)
	}
	if err := os.Rename(aside, state); err != nil {
		t.Fatal(err)
	}
	fresh := newTestLoopState()
	fresh.attachScratchpadFile(state)
	if got, _ := fresh.scratchpadRead("keep"); got["keep"] != "saved" {
		t.Fatalf("next turn restored %v, want the value the failed clear kept", got)
	}
	// A missing key is not an error, with or without a workspace.
	if cleared, err := ls.scratchpadClear("nope"); cleared || err != nil {
		t.Fatalf("missing key: %v, %v", cleared, err)
	}
}
