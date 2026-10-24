package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// BuildInfo is build-info.json (schema fleet-build-info/v1).
type BuildInfo struct {
	Schema        string   `json:"schema"`
	App           string   `json:"app"`
	Commit        string   `json:"commit"`
	SHA12         string   `json:"sha12"`
	Version       string   `json:"version"`
	GoVersion     string   `json:"go_version"`
	BuiltAt       string   `json:"built_at"`
	Targets       []string `json:"targets"`
	MigrationsMax *int     `json:"migrations_max"`
}

// Source describes where the artifact comes from.
type Source struct {
	FromFile string // local tarball
	Sums     string // SHA256SUMS for FromFile (default: next to the tarball)
	Commit   string
	Run      string
	Latest   bool
	Repo     string
	Branch   string
	Workflow string
	Artifact string // artifact name pattern, {sha12}
	CacheDir string
	Platform string // os/arch of the tarball to pick from a downloaded artifact
}

// Fetched is a downloaded or local artifact whose checksums are verified.
type Fetched struct {
	Tarball   string
	BuildInfo *BuildInfo
	SHA12     string // from the run, when known
	Note      string
}

type ghRun struct {
	DatabaseID int64  `json:"databaseId"`
	HeadSHA    string `json:"headSha"`
	Conclusion string `json:"conclusion"`
	Status     string `json:"status"`
}

// Fetch obtains the tarball (running gh when needed unless dryNoDownload) and verifies it.
func (d *Deps) Fetch(ctx context.Context, app string, s Source) (*Fetched, error) {
	var dir, tarball, sumsPath, sha12 string
	if s.FromFile != "" {
		tarball = s.FromFile
		if _, err := os.Stat(tarball); err != nil {
			return nil, fail(ExitFail, "--from-file: %v", err)
		}
		dir = filepath.Dir(tarball)
		sumsPath = s.Sums
		if sumsPath == "" {
			sumsPath = filepath.Join(dir, "SHA256SUMS")
		}
	} else {
		if s.Repo == "" {
			return nil, fail(ExitUsage, "no repo known for %s: pass --repo owner/name (first install) or use --from-file", app)
		}
		run, err := d.resolveRun(ctx, s)
		if err != nil {
			return nil, err
		}
		sha12 = run.HeadSHA[:12]
		name := ExpandTemplate(s.Artifact, map[string]string{"sha12": sha12, "app": app})
		dir = filepath.Join(s.CacheDir, app, fmt.Sprintf("run-%d", run.DatabaseID))
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fail(ExitFail, "cache dir: %v", err)
		}
		d.say("download: gh run download %d -R %s -n %s -> %s", run.DatabaseID, s.Repo, name, dir)
		if _, err := d.Run.Run(ctx, "gh", "run", "download", fmt.Sprint(run.DatabaseID), "-R", s.Repo, "-n", name, "-D", dir); err != nil {
			return nil, fail(ExitFail, "artifact download failed: %v", err)
		}
		sumsPath = findFile(dir, "SHA256SUMS")
		if sumsPath == "" {
			return nil, fail(ExitFail, "downloaded artifact has no SHA256SUMS")
		}
		dir = filepath.Dir(sumsPath)
	}
	sums, err := ParseSums(sumsPath)
	if err != nil {
		return nil, fail(ExitFail, "SHA256SUMS: %v", err)
	}
	if s.FromFile == "" {
		platform := s.Platform
		var cands []string
		for n := range sums {
			if strings.HasSuffix(n, "_"+strings.ReplaceAll(platform, "/", "_")+".tar.gz") {
				cands = append(cands, n)
			}
		}
		sort.Strings(cands)
		if len(cands) != 1 {
			return nil, fail(ExitFail, "artifact has %d tarballs for %s (want 1)", len(cands), platform)
		}
		tarball = filepath.Join(dir, cands[0])
	}
	if err := VerifySum(sums, tarball); err != nil {
		return nil, fail(ExitRefused, "%v", err)
	}
	f := &Fetched{Tarball: tarball, SHA12: sha12}
	if bi := filepath.Join(dir, "build-info.json"); fileExists(bi) {
		if err := VerifySum(sums, bi); err != nil {
			return nil, fail(ExitRefused, "%v", err)
		}
		b, err := os.ReadFile(bi)
		if err != nil {
			return nil, fail(ExitFail, "%v", err)
		}
		var info BuildInfo
		if err := json.Unmarshal(b, &info); err != nil {
			return nil, fail(ExitFail, "build-info.json: %v", err)
		}
		if info.Schema != "fleet-build-info/v1" {
			return nil, fail(ExitFail, "build-info.json: unsupported schema %q", info.Schema)
		}
		f.BuildInfo = &info
	}
	d.say("verify: %s sha256 ok", filepath.Base(tarball))
	return f, nil
}

func (d *Deps) resolveRun(ctx context.Context, s Source) (*ghRun, error) {
	fields := "databaseId,headSha,conclusion,status"
	switch {
	case s.Run != "":
		out, err := d.Run.Run(ctx, "gh", "run", "view", s.Run, "-R", s.Repo, "--json", fields)
		if err != nil {
			return nil, fail(ExitFail, "gh run view: %v", err)
		}
		var r ghRun
		if err := json.Unmarshal([]byte(out), &r); err != nil || len(r.HeadSHA) < 12 {
			return nil, fail(ExitFail, "gh run view: unexpected output")
		}
		if r.Conclusion != "success" {
			return nil, fail(ExitRefused, "run %s is not a successful run (status %s, conclusion %q)", s.Run, r.Status, r.Conclusion)
		}
		return &r, nil
	case s.Commit != "":
		sha := s.Commit
		if len(sha) < 40 {
			out, err := d.Run.Run(ctx, "gh", "api", fmt.Sprintf("repos/%s/commits/%s", s.Repo, sha), "--jq", ".sha")
			if err != nil {
				return nil, fail(ExitFail, "cannot resolve commit %s: %v", sha, err)
			}
			sha = strings.TrimSpace(out)
		}
		return d.listRuns(ctx, s, "--commit", sha, fmt.Sprintf("commit %s", sha[:12]))
	case s.Latest:
		return d.listRuns(ctx, s, "--branch", s.Branch, "branch "+s.Branch)
	}
	return nil, fail(ExitUsage, "specify one of --commit, --run, --latest-green or --from-file")
}

var shaRe = regexp.MustCompile(`^[0-9a-f]{40}$`)

func (d *Deps) listRuns(ctx context.Context, s Source, flag, val, what string) (*ghRun, error) {
	out, err := d.Run.Run(ctx, "gh", "run", "list", "-R", s.Repo, "--workflow", s.Workflow, flag, val,
		"--status", "success", "--limit", "20", "--json", "databaseId,headSha,conclusion,status")
	if err != nil {
		return nil, fail(ExitFail, "gh run list: %v", err)
	}
	var runs []ghRun
	if err := json.Unmarshal([]byte(out), &runs); err != nil {
		return nil, fail(ExitFail, "gh run list: unexpected output")
	}
	for i := range runs {
		r := runs[i]
		if r.Conclusion == "success" && shaRe.MatchString(r.HeadSHA) {
			return &r, nil
		}
	}
	return nil, fail(ExitRefused, "no successful %s run for %s: refusing (build is not green or has expired)", s.Workflow, what)
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }

// findFile looks for name in dir and one level below.
func findFile(dir, name string) string {
	if p := filepath.Join(dir, name); fileExists(p) {
		return p
	}
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if e.IsDir() {
			if p := filepath.Join(dir, e.Name(), name); fileExists(p) {
				return p
			}
		}
	}
	return ""
}

// ParseTarballName extracts version/os/arch from a tarball file name using the
// artifact.tarball template.
func ParseTarballName(tmpl, file string) (version, os_, arch string, err error) {
	re := regexp.QuoteMeta(tmpl)
	for k, v := range map[string]string{"version": `.+`, "os": `[a-z0-9]+`, "arch": `[a-z0-9]+`, "sha12": `[0-9a-f]{12}`} {
		re = strings.ReplaceAll(re, `\{`+k+`\}`, `(?P<`+k+`>`+v+`)`)
	}
	rx, cerr := regexp.Compile("^" + re + "$")
	if cerr != nil {
		return "", "", "", cerr
	}
	m := rx.FindStringSubmatch(file)
	if m == nil {
		return "", "", "", fmt.Errorf("tarball name %q does not match artifact.tarball %q", file, tmpl)
	}
	for i, n := range rx.SubexpNames() {
		switch n {
		case "version":
			version = m[i]
		case "os":
			os_ = m[i]
		case "arch":
			arch = m[i]
		}
	}
	return
}
