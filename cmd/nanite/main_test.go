package main

import (
	"bytes"
	"context"
	"testing"
)

func TestRootHasCommands(t *testing.T) {
	ctx := context.Background()
	run, err := start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = run.Stop(ctx) }()
	root := newRoot(ctx, run.App)
	for _, name := range []string{"version", "hello"} {
		if c, _, err := root.Find([]string{name}); err != nil || c == root {
			t.Errorf("command %q missing: %v", name, err)
		}
	}
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"hello"})
	if err := root.ExecuteContext(ctx); err != nil {
		t.Fatal(err)
	}
	if out.String() != "hello\n" {
		t.Errorf("hello printed %q", out.String())
	}
}
