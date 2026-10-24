package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/brand"
)

func TestExtractDBFlag(t *testing.T) {
	cases := []struct {
		in   []string
		db   string
		rest []string
	}{
		{[]string{"install", "-db", "/x/m.db", "d.md"}, "/x/m.db", []string{"install", "d.md"}},
		{[]string{"--db=/x/m.db", "whoami"}, "/x/m.db", []string{"whoami"}},
		{[]string{"-db=/a", "list", "--db", "/b"}, "/b", []string{"list"}},
		{[]string{"install", "-db"}, "", []string{"install", "-db"}},
		{[]string{"list", "--all"}, "", []string{"list", "--all"}},
	}
	for _, c := range cases {
		db, rest := extractDBFlag(c.in)
		if db != c.db || !reflect.DeepEqual(rest, c.rest) {
			t.Errorf("extractDBFlag(%v) = %q %v, want %q %v", c.in, db, rest, c.db, c.rest)
		}
	}
}

// isolate points the XDG roots at a temp dir and clears every database
// override, so a case sets exactly the ones it is about.
func isolate(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	for _, k := range []string{"XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CONFIG_HOME", "XDG_CACHE_HOME"} {
		t.Setenv(k, filepath.Join(home, k))
	}
	t.Setenv("HOME", home)
	for _, k := range []string{"NANITE_DB", "NANITE_DB_PATH", "NANITE_WORKSPACE"} {
		t.Setenv(k, "")
	}
	old := cliDB
	cliDB = ""
	t.Cleanup(func() { cliDB = old })
	return home
}

// The server (-db X) and the CLI (NANITE_DB_PATH=X, -db X) must land on the
// same credential directory.
func TestResolveAuthPathsServerAndCLIAgree(t *testing.T) {
	isolate(t)
	db := filepath.Join(t.TempDir(), "scratch", "main.db")
	want := filepath.Join(filepath.Dir(db), "auth")

	server, err := resolveAuthPaths(db)
	if err != nil || server.dir != want {
		t.Fatalf("serve -db: %+v, %v; want dir %s", server, err, want)
	}

	t.Setenv("NANITE_DB_PATH", db)
	viaEnv, err := resolveAuthPaths("")
	if err != nil || viaEnv != server {
		t.Fatalf("NANITE_DB_PATH: %+v, %v; want %+v", viaEnv, err, server)
	}

	t.Setenv("NANITE_DB_PATH", "")
	t.Setenv("NANITE_DB", db)
	viaLegacy, err := resolveAuthPaths("")
	if err != nil || viaLegacy != server {
		t.Fatalf("NANITE_DB: %+v, %v; want %+v", viaLegacy, err, server)
	}

	t.Setenv("NANITE_DB", "")
	cliDB = db
	viaFlag, err := resolveAuthPaths("")
	if err != nil || viaFlag != server {
		t.Fatalf("subcommand -db: %+v, %v; want %+v", viaFlag, err, server)
	}
}

func TestResolveAuthPathsDefaultsToDataDir(t *testing.T) {
	home := isolate(t)
	got, err := resolveAuthPaths("")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, "XDG_DATA_HOME", "nanite", "auth"); got.dir != want {
		t.Fatalf("dir = %s, want %s", got.dir, want)
	}
	t.Setenv("NANITE_WORKSPACE", "ws1")
	got, _ = resolveAuthPaths("")
	if want := filepath.Join(home, "XDG_DATA_HOME", "nanite", "auth"); got.dir != want {
		t.Fatalf("workspace dir = %s, want %s", got.dir, want)
	}
}

// A NANITE_DB_PATH-only instance that predates the shared rule kept its
// credentials in the data directory; it keeps finding them there.
func TestResolveAuthPathsEnvDBKeepsExistingDataDirCredentials(t *testing.T) {
	home := isolate(t)
	legacy := filepath.Join(home, "XDG_DATA_HOME", "nanite", "auth")
	if err := os.MkdirAll(legacy, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "auth.db"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	db := filepath.Join(t.TempDir(), "main.db")
	t.Setenv("NANITE_DB_PATH", db)
	got, err := resolveAuthPaths("")
	if err != nil || got.dir != legacy {
		t.Fatalf("got %+v, %v; want legacy %s", got, err, legacy)
	}
	// An explicit flag is never redirected.
	got, _ = resolveAuthPaths(db)
	if want := filepath.Join(filepath.Dir(db), "auth"); got.dir != want {
		t.Fatalf("flag dir = %s, want %s", got.dir, want)
	}
	// Once the beside-db store exists it wins.
	if err := os.MkdirAll(filepath.Join(filepath.Dir(db), "auth"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(db), "auth", "auth.db"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	got, _ = resolveAuthPaths("")
	if want := filepath.Join(filepath.Dir(db), "auth"); got.dir != want {
		t.Fatalf("dir = %s, want %s", got.dir, want)
	}
}

func TestResolveDBPathHonoursSubcommandFlag(t *testing.T) {
	isolate(t)
	db := filepath.Join(t.TempDir(), "x", "main.db")
	cliDB = db
	if got := resolveDBPath(); got != db {
		t.Fatalf("resolveDBPath = %s, want %s", got, db)
	}
}

const installFixture = "---\nschema_version: \"2\"\ndefinition_id: def:a1-db-flag\nrevision: \"1\"\nname: a1-db-flag\ndescription: Install target fixture.\nbehavior:\n  purpose: Complete the requested work.\nrequirements: {}\nharness_profile:\n  context: {}\n  permissions:\n    profile: default\ncontinuity:\n  mode: ephemeral\n---\nFixture.\n"

// `agent install -db X` writes the database X, wherever it sits, with no
// NANITE_DB_PATH in the environment.
func TestAgentInstallHonoursDBFlag(t *testing.T) {
	isolate(t)
	dir := t.TempDir()
	def := filepath.Join(dir, "d.md")
	if err := os.WriteFile(def, []byte(installFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	db := filepath.Join(dir, "custom", "main.db")
	var args []string
	cliDB, args = extractDBFlag([]string{"install", "-db", db, def})
	var out strings.Builder
	if err := runPinnedAgentCommand(args, &out); err != nil {
		t.Fatalf("install: %v", err)
	}
	if _, err := os.Stat(db); err != nil {
		t.Fatalf("install did not create %s: %v", db, err)
	}
	if !strings.Contains(out.String(), "def:a1-db-flag") {
		t.Fatalf("output lacks the pin: %s", out.String())
	}
}

func TestLoginLinkBase(t *testing.T) {
	t.Setenv(brand.Env("API_URL"), "http://127.0.0.1:8090")
	t.Setenv(brand.Env("PUBLIC_URL"), "")
	if got := loginLinkBase(); got != "http://127.0.0.1:8090" {
		t.Fatalf("without PUBLIC_URL got %q", got)
	}
	t.Setenv(brand.Env("PUBLIC_URL"), " https://nanite.nanite.cloud/ ")
	if got := loginLinkBase(); got != "https://nanite.nanite.cloud" {
		t.Fatalf("with PUBLIC_URL got %q", got)
	}
}
