package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/slots"
)

func TestSlotWriterContract(t *testing.T) {
	s := Slots{store: slots.NewStore()}
	entry := SlotEntry{Owner: "engine", Key: "demo", Content: "context"}
	for _, name := range []string{"engine", "file", "Bad", ""} {
		w := s.Owner(name)
		if err := w.Put(SlotContext, entry); err == nil {
			t.Errorf("Put accepted owner %q", name)
		}
		if err := w.Replace(SlotContext, entry); err == nil {
			t.Errorf("Replace accepted owner %q", name)
		}
	}
	w := s.Owner("valid")
	for _, slot := range []SlotName{SlotAgent, SlotPermissions, SlotUniversal, SlotHandoff, SlotConversation} {
		if err := w.Put(slot, entry); err == nil {
			t.Errorf("Put accepted %q", slot)
		}
		if err := w.Replace(slot, entry); err == nil {
			t.Errorf("Replace accepted %q", slot)
		}
	}
	if err := w.Put(SlotContext, entry); err != nil {
		t.Fatal(err)
	}
	if got := s.store.Snapshot().Entries(SlotContext)[0].Owner; got != "valid" {
		t.Fatalf("owner = %q", got)
	}
	names := SlotNames()
	names[0] = SlotContext
	if SlotNames()[0] != SlotUniversal {
		t.Fatal("slot names are mutable")
	}
}

func TestOpenEnforcesDirectoryLock(t *testing.T) {
	dir := t.TempDir()
	definition := `---
schema_version: "2"
definition_id: def:locking-test
revision: "1"
name: locking-test
description: Directory lock fixture.
behavior:
  purpose: Test the directory lock.
requirements: {}
harness_profile:
  permissions:
    profile: default
continuity:
  mode: ephemeral
---
Test instructions.
`
	for name, raw := range map[string]string{"agent.md": definition, "host.yaml": "kind: embedded\nlifetime: application\napplication_id: test\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	opts := Options{Dir: dir}
	first, err := Open(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if second, err := Open(ctx, opts); err == nil {
		_ = second.Close()
		t.Fatal("second Open succeeded with the lock held")
	} else if !strings.Contains(err.Error(), "lock agent data directory") {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := Open(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestToolAndSkillGroups(t *testing.T) {
	var tools ToolSet
	good := Tool{Name: "lookup", InputSchema: json.RawMessage(`{"type":"object"}`), Handler: func(context.Context, ToolCall) (ToolResult, error) { return ToolResult{}, nil }}
	if err := tools.SetGroup("one", good); err != nil {
		t.Fatal(err)
	}
	if err := tools.SetGroup("two", good); err == nil {
		t.Fatal("accepted duplicate tool across groups")
	}
	bad := good
	bad.InputSchema = json.RawMessage(`{"type":`)
	if err := tools.SetGroup("one", bad); err == nil {
		t.Fatal("accepted invalid schema JSON")
	}

	var skills SkillSet
	old := Skill{Name: "previous", Description: "original"}
	if err := skills.SetGroup("notes", old); err != nil {
		t.Fatal(err)
	}
	replacement := Skill{Name: "next", Description: "replacement"}
	if err := skills.SetGroup("notes", replacement, replacement); err == nil {
		t.Fatal("accepted duplicate skills")
	}
	if got := skills.groups.Listing(); got != "- previous: original" {
		t.Fatalf("failed update changed group: %q", got)
	}
	if err := skills.SetGroup("notes", replacement); err != nil {
		t.Fatal(err)
	}
	if got := skills.groups.Listing(); got != "- next: replacement" {
		t.Fatalf("replacement listing = %q", got)
	}
}
