package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeRunner records every command; handler may answer or fall through to defaults.
// sqlite3 goes to the real binary when present and wanted.
type fakeRunner struct {
	mu      sync.Mutex
	calls   []string
	handler func(name string, args []string) (string, error, bool)
	real    bool // run real sqlite3
}

func (f *fakeRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	f.mu.Lock()
	f.calls = append(f.calls, name+" "+strings.Join(args, " "))
	h := f.handler
	f.mu.Unlock()
	if h != nil {
		if out, err, ok := h(name, args); ok {
			return out, err
		}
	}
	if name == "sqlite3" && f.real {
		return execRunner{}.Run(ctx, name, args...)
	}
	return "", nil
}

func (f *fakeRunner) count(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

func needSqlite(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not on PATH")
	}
}

func mustSqlite(t *testing.T, db string, sql ...string) {
	t.Helper()
	needSqlite(t)
	for _, s := range sql {
		if out, err := exec.Command("sqlite3", db, s).CombinedOutput(); err != nil {
			t.Fatalf("sqlite3 %s: %v %s", s, err, out)
		}
	}
}

func newDeps(t *testing.T) (*Deps, *fakeRunner, *bytes.Buffer) {
	t.Helper()
	out := &bytes.Buffer{}
	fr := &fakeRunner{}
	d := &Deps{
		Run: fr, HTTP: &http.Client{Timeout: 5 * time.Second}, Out: out, Err: out,
		Home: t.TempDir(), Hostname: "testhost", OS: "linux", Arch: "amd64",
		Sleep: func(time.Duration) { time.Sleep(10 * time.Millisecond) }, Now: time.Now, LocalUser: true,
	}
	return d, fr, out
}

// artifact is a built, checksummed release in the real P1 layout.
type artifact struct {
	Dir, Tarball, Sums, BuildInfo string
}

type buildOpts struct {
	Version, Binary, Spec string
	OS, Arch              string
	Migrations            int // 0: omitted
	Extra                 func(tw *tar.Writer)
	NoBuildInfoInSums     bool
}

// buildArtifact writes nanite_<v>_<os>_<arch>.tar.gz (top dir with binary, deploy.yaml,
// README.md, LICENSE), build-info.json and SHA256SUMS into a fresh temp dir.
func buildArtifact(t *testing.T, o buildOpts) artifact {
	t.Helper()
	if o.OS == "" {
		o.OS, o.Arch = "linux", "amd64"
	}
	dir := t.TempDir()
	top := fmt.Sprintf("nanite_%s_%s_%s", o.Version, o.OS, o.Arch)
	name := top + ".tar.gz"
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	add := func(n, body string, mode int64) {
		tw.WriteHeader(&tar.Header{Name: top + "/" + n, Mode: mode, Size: int64(len(body)), Typeflag: tar.TypeReg})
		tw.Write([]byte(body))
	}
	tw.WriteHeader(&tar.Header{Name: top + "/", Mode: 0o755, Typeflag: tar.TypeDir})
	add("nanite", o.Binary, 0o755)
	add("deploy.yaml", o.Spec, 0o644)
	add("README.md", "readme", 0o644)
	add("LICENSE", "license", 0o644)
	if o.Extra != nil {
		o.Extra(tw)
	}
	tw.Close()
	gz.Close()
	tb := filepath.Join(dir, name)
	os.WriteFile(tb, buf.Bytes(), 0o644)
	sha := strings.Repeat("ab", 20)
	bi := fmt.Sprintf(`{"schema":"fleet-build-info/v1","app":"nanite","commit":%q,"sha12":%q,"version":%q,"go_version":"go1.26","built_at":"2026-10-24T00:00:00Z","targets":["linux/amd64"]`, sha, sha[:12], o.Version)
	if o.Migrations > 0 {
		bi += fmt.Sprintf(`,"migrations_max":%d`, o.Migrations)
	}
	bi += "}\n"
	bip := filepath.Join(dir, "build-info.json")
	os.WriteFile(bip, []byte(bi), 0o644)
	sums := ""
	for _, f := range []string{tb, bip} {
		if o.NoBuildInfoInSums && f == bip {
			continue
		}
		b, _ := os.ReadFile(f)
		h := sha256.Sum256(b)
		sums += hex.EncodeToString(h[:]) + "  " + filepath.Base(f) + "\n"
	}
	sp := filepath.Join(dir, "SHA256SUMS")
	os.WriteFile(sp, []byte(sums), 0o644)
	return artifact{dir, tb, sp, bip}
}

// specFor renders a deploy.yaml for a temp install root and fake service.
type specOpts struct {
	Root, Link, DB, AuthDB string
	Restart, Health        string // yaml fragments (list items) override
	Dropins                string
	Preflight              string
}

func specFor(o specOpts) string {
	restart := o.Restart
	if restart == "" {
		restart = `      - {via: none}`
	}
	health := o.Health
	if health == "" {
		health = `      - {url: "http://127.0.0.1:1/x", expect_status: [200], timeout: 1s}`
	}
	pf := "    preflight: []"
	if o.Preflight != "" {
		pf = "    preflight:\n" + o.Preflight
	}
	dp := "    unit_dropins: []"
	if o.Dropins != "" {
		dp = "    unit_dropins:\n" + o.Dropins
	}
	return fmt.Sprintf(`schema: fleet-deploy/v1
app: nanite
repo: hollis-labs/nanite
artifact:
  name: "nanite-{sha12}"
  tarball: "nanite_{version}_{os}_{arch}.tar.gz"
  sums: SHA256SUMS
targets:
  - id: t1
    when: {os: linux, arch: amd64}
    hosts: [testhost]
    binary: nanite
    install_root: %s
    symlinks:
      - path: %s
%s
%s
    restart:
%s
    health:
%s
`, o.Root, o.Link, dp, pf, restart, health)
}

func mustContain(t *testing.T, s string, subs ...string) {
	t.Helper()
	for _, x := range subs {
		if !strings.Contains(s, x) {
			t.Fatalf("output missing %q:\n%s", x, s)
		}
	}
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	if ee, ok := err.(*ExitError); ok {
		return ee.Code
	}
	return -1
}

// treeSnapshot lists every path (with symlink targets and sizes) under dir.
func treeSnapshot(t *testing.T, dir string) string {
	t.Helper()
	var sb strings.Builder
	filepath.Walk(dir, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		line := p
		if fi.Mode()&os.ModeSymlink != 0 {
			tg, _ := os.Readlink(p)
			line += " -> " + tg
		} else if fi.Mode().IsRegular() {
			line += fmt.Sprintf(" %d %v", fi.Size(), fi.ModTime().UnixNano())
		}
		sb.WriteString(line + "\n")
		return nil
	})
	return sb.String()
}
