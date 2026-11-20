package tools

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
)

func TestGroupReplacementIsAtomicAndSnapshotsOwnSchemas(t *testing.T) {
	tool := func(name string) Tool {
		return Tool{Name: name, InputSchema: json.RawMessage(`{"type":"object"}`), Handler: func(context.Context, Call) (Result, error) { return Result{}, nil }}
	}
	var groups Groups
	first := tool("first")
	if err := groups.SetGroup("z", first); err != nil {
		t.Fatal(err)
	}
	first.InputSchema[0] = '?'
	if err := groups.SetGroup("a", tool("second")); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		group   string
		entries []Tool
	}{
		{" ", []Tool{tool("new")}},
		{"a", []Tool{tool("first")}},
		{"a", []Tool{tool("another"), tool("another")}},
		{"a", []Tool{tool("bash")}},
		{"a", []Tool{tool("mcp__server__tool")}},
		{"a", []Tool{{Name: "missing-handler"}}},
		{"a", []Tool{first}},
	} {
		if err := groups.SetGroup(tc.group, tc.entries...); err == nil {
			t.Fatalf("SetGroup(%q, %v) succeeded", tc.group, tc.entries)
		}
	}
	snapshot := groups.Snapshot()
	if len(snapshot) != 2 || snapshot[0].Name != "second" || snapshot[1].Name != "first" || !slices.Equal(snapshot[1].InputSchema, tool("unused").InputSchema) {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	snapshot[1].InputSchema[0] = '?'
	if got := groups.Snapshot()[1].InputSchema[0]; got != '{' {
		t.Fatalf("stored schema starts with %q", got)
	}
	if err := groups.SetGroup("a", tool("third")); err != nil {
		t.Fatal(err)
	}
	groups.RemoveGroup("z")
	if got := groups.Snapshot(); len(got) != 1 || got[0].Name != "third" {
		t.Fatalf("after replace/remove: %+v", got)
	}
}

func TestSubtaskToolNamesAreReserved(t *testing.T) {
	var g Groups
	for _, name := range []string{"subtask", "return_result"} {
		err := g.SetGroup("host", Tool{Name: name, InputSchema: json.RawMessage(`{"type":"object"}`),
			Handler: func(context.Context, Call) (Result, error) { return Result{}, nil }})
		if err == nil {
			t.Errorf("a host tool took the reserved name %q", name)
		}
	}
}
