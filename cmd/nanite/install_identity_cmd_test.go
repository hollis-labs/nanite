package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

func TestInstallIdentityCLI_ShowRegenRefuseForce(t *testing.T) {
	ctx := context.Background()
	db := filepath.Join(t.TempDir(), "main.db")
	run := func(args ...string) (int, string, string) {
		var o, e bytes.Buffer
		code := runInstallIdentity(append([]string{"--db", db}, args...), &o, &e)
		return code, strings.TrimSpace(o.String()), e.String()
	}

	code, id, errOut := run("show")
	if code != 0 || len(id) != 36 {
		t.Fatalf("show = %d %q %q", code, id, errOut)
	}
	if code, again, _ := run("show"); code != 0 || again != id {
		t.Fatalf("show not stable: %q vs %q", again, id)
	}
	if code, out, errOut := run("regen"); code != 0 || !strings.Contains(out, "previous: "+id) {
		t.Fatalf("regen = %d %q %q", code, out, errOut)
	}
	_, id2, _ := run("show")
	if id2 == id {
		t.Fatal("regen did not change the id")
	}

	s, err := store.New(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`PRAGMA foreign_keys=OFF`,
		`INSERT INTO agent_actor_bindings (actor_uri, host_settings_id, binding_receipt) VALUES ('msg://agent/tether/a','hs1','r')`,
	} {
		if _, err := s.DB.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	_ = s.Close(ctx)

	code, _, errOut = run("regen")
	if code == 0 || !strings.Contains(errOut, "refusing") {
		t.Fatalf("regen with binding = %d %q", code, errOut)
	}
	if _, still, _ := run("show"); still != id2 {
		t.Fatal("refused regen changed the id")
	}
	if code, _, errOut := run("regen", "--force"); code != 0 {
		t.Fatalf("forced regen = %d %q", code, errOut)
	}
	if _, id3, _ := run("show"); id3 == id2 {
		t.Fatal("forced regen did not change the id")
	}
	if code, _, _ := run("regen", "--bogus"); code != 2 {
		t.Fatalf("bad flag code = %d", code)
	}
}
