package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestRealP1Tarball builds one tarball with the P1 script (linux/amd64, UI skipped) and
// checks that fleet-install understands the real layout, the real deploy.yaml and the
// real build-info.json, ending in a dry run for the agent-os target (touches nothing).
func TestRealP1Tarball(t *testing.T) {
	if testing.Short() {
		t.Skip("short")
	}
	repo, _ := filepath.Abs("../..")
	script := filepath.Join(repo, "scripts/ci/build-binaries.sh")
	for _, tool := range []string{"git", "go", "bash"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " not available")
		}
	}
	if _, err := os.Stat(script); err != nil {
		t.Skip("script not present")
	}
	if _, err := os.Stat(filepath.Join(repo, ".git")); err != nil {
		t.Skip("not a git checkout")
	}
	dist := filepath.Join(repo, "dist")
	if _, err := os.Stat(dist); err == nil {
		t.Skip("dist/ exists; not clobbering it")
	}
	t.Cleanup(func() { os.RemoveAll(dist) })
	cmd := exec.Command(script, "linux/amd64")
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), "SKIP_UI=1", "GOWORK=off")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("P1 script could not run here (embedded UI missing?): %v\n%s", err, out)
	}
	ents, _ := filepath.Glob(filepath.Join(dist, "nanite_*_linux_amd64.tar.gz"))
	if len(ents) != 1 {
		t.Fatalf("tarballs: %v", ents)
	}
	d, _, out := newDeps(t)
	d.Hostname = "agent-os"
	f, err := d.Fetch(context.Background(), "nanite", Source{FromFile: ents[0]})
	if err != nil {
		t.Fatal(err)
	}
	if f.BuildInfo == nil || f.BuildInfo.MigrationsMax == nil || *f.BuildInfo.MigrationsMax < 1 {
		t.Fatalf("build-info %+v", f.BuildInfo)
	}
	x := t.TempDir()
	if err := ExtractTarGz(ents[0], x); err != nil {
		t.Fatal(err)
	}
	top := strings.TrimSuffix(filepath.Base(ents[0]), ".tar.gz")
	for _, n := range []string{"nanite", "deploy.yaml", "README.md", "LICENSE"} {
		if !fileExists(filepath.Join(x, top, n)) {
			t.Errorf("tarball lacks %s/%s", top, n)
		}
	}
	data, _ := os.ReadFile(filepath.Join(x, top, "deploy.yaml"))
	spec, err := ParseSpec(data)
	if err != nil {
		t.Fatal(err)
	}
	v, o, a, err := ParseTarballName(spec.Artifact.Tarball, filepath.Base(ents[0]))
	if err != nil || v != f.BuildInfo.Version || o != "linux" || a != "amd64" {
		t.Fatalf("name parse %q %q %q %v", v, o, a, err)
	}
	err = d.Deploy(context.Background(), DeployOpts{App: "nanite", Source: Source{FromFile: ents[0]}, Root: t.TempDir(), DryRun: true, Target: "agent-os-hub-nanite"})
	if err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	mustContain(t, out.String(), "target agent-os-hub-nanite", "would flip symlink /var/lib/fabric/bin/nanite", "systemctl --user restart com.hollis-labs.cerberus.hub-nanite.service", "dry-run complete")
}
