package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRepoDeployYAMLValidForBothTargets(t *testing.T) {
	b, err := os.ReadFile("../../deploy/deploy.yaml")
	if err != nil {
		t.Fatal(err)
	}
	s, err := ParseSpec(b)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"agent-os-hub-nanite", "mac-nanite"} {
		found := false
		for _, tg := range s.Targets {
			if tg.ID == id {
				found = true
				if len(tg.Restart) == 0 || len(tg.Health) == 0 || len(tg.Symlinks) == 0 {
					t.Errorf("%s: incomplete", id)
				}
			}
		}
		if !found {
			t.Errorf("target %s missing", id)
		}
	}
	if _, err := SelectTarget(s, "", "mac", "darwin", "arm64"); err != nil {
		t.Error(err)
	}
	if _, err := SelectTarget(s, "", "agent-os", "linux", "amd64"); err != nil {
		t.Error(err)
	}
}

func TestParseSpecErrors(t *testing.T) {
	base := specFor(specOpts{Root: "/r", Link: "/l"})
	cases := []struct{ name, from, to, want string }{
		{"ok", "", "", ""},
		{"schema version", "fleet-deploy/v1", "fleet-deploy/v2", `unsupported "fleet-deploy/v2"`},
		{"unknown kind", "    preflight: []\n    restart", "    preflight:\n    - {kind: frobnicate, db: x}\n    restart", `unknown kind "frobnicate"`},
		{"missing sql", "    preflight: []\n    restart", "    preflight:\n    - {kind: sql_zero, db: x}\n    restart", "sql_zero requires db and sql"},
		{"missing backup dest", "    preflight: []\n    restart", "    preflight:\n    - {kind: sqlite_backup, db: x}\n    restart", "sqlite_backup requires db and dest"},
		{"unknown template var", "    preflight: []\n    restart", "    preflight:\n    - {kind: sqlite_backup, db: x, dest: \"{nope}/a\"}\n    restart", "unknown template variable {nope}"},
		{"unknown restart mode", "{via: none}", "{via: pigeon}", `unknown mode "pigeon"`},
		{"cerberus no resource", "{via: none}", "{via: cerberus}", "cerberus requires resource"},
		{"cerberus deploy verb", "{via: none}", "{via: cerberus, resource: r, verb: deploy}", "not allowed"},
		{"command no argv", "{via: none}", "{via: command}", "non-empty argv"},
		{"relative root", "install_root: /r", "install_root: rel", "install_root"},
		{"binary path", "binary: nanite", "binary: ../nanite", "plain file name"},
		{"no health url", `url: "http://127.0.0.1:1/x", `, "", "url: required"},
		{"bad duration", "timeout: 1s", "timeout: soon", "bad duration"},
		{"unknown field", "repo: hollis-labs/nanite", "repo: hollis-labs/nanite\nbogus: 1", "bogus"},
		{"no targets", "targets:", "targetz:", "targetz"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := base
			if c.from != "" {
				if !strings.Contains(in, c.from) {
					t.Fatalf("fixture lacks %q", c.from)
				}
				in = strings.Replace(in, c.from, c.to, 1)
			}
			_, err := ParseSpec([]byte(in))
			if c.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("want error containing %q, got %v", c.want, err)
			}
		})
	}
	if _, err := ParseSpec([]byte("")); err == nil {
		t.Fatal("empty spec accepted")
	}
}

func TestRestartHealthSingleOrList(t *testing.T) {
	s := strings.Replace(specFor(specOpts{Root: "/r", Link: "/l"}), "    restart:\n      - {via: none}", "    restart: {via: none}", 1)
	spec, err := ParseSpec([]byte(s))
	if err != nil || len(spec.Targets[0].Restart) != 1 {
		t.Fatalf("single mapping: %v", err)
	}
}

func tarWith(t *testing.T, entries ...tar.Header) string {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, h := range entries {
		h := h
		tw.WriteHeader(&h)
		if h.Typeflag == tar.TypeReg {
			tw.Write(make([]byte, h.Size))
		}
	}
	tw.Close()
	gz.Close()
	p := filepath.Join(t.TempDir(), "x.tar.gz")
	os.WriteFile(p, buf.Bytes(), 0o644)
	return p
}

func TestExtractRejectsTraversalAndLinks(t *testing.T) {
	bad := map[string]tar.Header{
		"dotdot":   {Name: "../evil", Typeflag: tar.TypeReg, Size: 1, Mode: 0o644},
		"nested":   {Name: "a/../../evil", Typeflag: tar.TypeReg, Size: 1, Mode: 0o644},
		"absolute": {Name: "/etc/evil", Typeflag: tar.TypeReg, Size: 1, Mode: 0o644},
		"symlink":  {Name: "a/link", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"},
		"hardlink": {Name: "a/hl", Typeflag: tar.TypeLink, Linkname: "x"},
	}
	for n, h := range bad {
		dest := t.TempDir()
		err := ExtractTarGz(tarWith(t, h), filepath.Join(dest, "out"))
		if err == nil || !strings.Contains(err.Error(), "rejected") {
			t.Errorf("%s: got %v", n, err)
		}
		if fileExists(filepath.Join(dest, "evil")) {
			t.Errorf("%s: file escaped", n)
		}
	}
	dest := filepath.Join(t.TempDir(), "out")
	if err := ExtractTarGz(tarWith(t, tar.Header{Name: "top/", Typeflag: tar.TypeDir, Mode: 0o755}, tar.Header{Name: "top/nanite", Typeflag: tar.TypeReg, Size: 3, Mode: 0o755}), dest); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(filepath.Join(dest, "top", "nanite")); err != nil || fi.Mode().Perm() != 0o755 {
		t.Fatalf("extracted file: %v %v", fi, err)
	}
}

func TestSelectTarget(t *testing.T) {
	s, _ := ParseSpec([]byte(strings.Replace(specFor(specOpts{Root: "/r", Link: "/l"}), "hosts: [testhost]", "hosts: [testhost, other]", 1)))
	s.Targets = append(s.Targets, s.Targets[0])
	s.Targets[1].ID, s.Targets[1].Hosts, s.Targets[1].When = "t2", []string{"mac"}, When{"darwin", "arm64"}
	if tg, err := SelectTarget(s, "", "TestHost.local", "linux", "amd64"); err != nil || tg.ID != "t1" {
		t.Fatalf("host match: %v %v", tg, err)
	}
	if tg, err := SelectTarget(s, "t2", "whatever", "darwin", "arm64"); err != nil || tg.ID != "t2" {
		t.Fatalf("by id: %v %v", tg, err)
	}
	if _, err := SelectTarget(s, "t2", "x", "linux", "amd64"); err == nil || !strings.Contains(err.Error(), "darwin/arm64") {
		t.Fatalf("platform mismatch: %v", err)
	}
	if _, err := SelectTarget(s, "", "stranger", "linux", "amd64"); err == nil || !strings.Contains(err.Error(), "pass --target") {
		t.Fatalf("no host: %v", err)
	}
	if _, err := SelectTarget(s, "nope", "x", "linux", "amd64"); err == nil {
		t.Fatal("unknown id")
	}
}
