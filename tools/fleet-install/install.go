package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
)

const keepVersions = 3

// BackupRec is a database or file backup taken before a release was installed.
type BackupRec struct {
	Kind string `json:"kind"`
	Src  string `json:"src"`
	Dest string `json:"dest"`
}

// Release is one installed version. Links maps each symlink path to its target, so a
// rollback relinks exactly what was linked. Version "legacy" is whatever was at the
// symlink paths before fleet-install first managed them.
type Release struct {
	Version     string            `json:"version"`
	Dir         string            `json:"dir,omitempty"`
	SHA12       string            `json:"sha12,omitempty"`
	Commit      string            `json:"commit,omitempty"`
	InstalledAt string            `json:"installed_at"`
	BackupDir   string            `json:"backup_dir,omitempty"`
	Backups     []BackupRec       `json:"backups,omitempty"`
	Links       map[string]string `json:"links"`
}

type State struct {
	Schema   string    `json:"schema"`
	App      string    `json:"app"`
	Target   string    `json:"target"`
	Current  *Release  `json:"current,omitempty"`
	Previous *Release  `json:"previous,omitempty"`
	History  []Release `json:"history"`
}

func statePath(installRoot string) string {
	return filepath.Join(installRoot, ".fleet-install", "state.json")
}

func LoadState(installRoot string) (*State, error) {
	b, err := os.ReadFile(statePath(installRoot))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var s State
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("%s: %v", statePath(installRoot), err)
	}
	return &s, nil
}

func SaveState(installRoot string, s *State) error {
	s.Schema = "fleet-install-state/v1"
	return writeJSONAtomic(statePath(installRoot), s)
}

func writeJSONAtomic(p string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	tmp := fmt.Sprintf("%s.tmp-%d", p, os.Getpid())
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// Registry maps an app to its repo and install roots so rollback/status/list work
// without a tarball.
type Registry struct {
	App      string            `json:"app"`
	Repo     string            `json:"repo"`
	Artifact string            `json:"artifact_name,omitempty"`
	Workflow string            `json:"workflow,omitempty"`
	Targets  map[string]string `json:"targets"` // target id -> install_root
}

func registryPath(root, app string) string { return filepath.Join(root, "apps", app+".json") }

func LoadRegistry(root, app string) (*Registry, error) {
	b, err := os.ReadFile(registryPath(root, app))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var r Registry
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

func SaveRegistry(root string, r *Registry) error {
	return writeJSONAtomic(registryPath(root, r.App), r)
}

// flipLinks atomically points every path at its target (temp symlink, then rename).
func flipLinks(links map[string]string) error {
	paths := make([]string, 0, len(links))
	for p := range links {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		if err := flipLink(p, links[p]); err != nil {
			return err
		}
	}
	return nil
}

func flipLink(path, target string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := fmt.Sprintf("%s.fleet-tmp-%d", path, os.Getpid())
	_ = os.Remove(tmp)
	if err := os.Symlink(target, tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("flip %s: %v", path, err)
	}
	return nil
}

// captureExisting records what is at path before the first flip and, for a regular
// file, keeps a copy at <path>.pre-fleet-install so it can be restored. Returns the
// target a rollback should relink ("" when nothing was there).
func captureExisting(path string) (string, error) {
	fi, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		t, err := os.Readlink(path)
		if err != nil {
			return "", err
		}
		if !filepath.IsAbs(t) {
			t = filepath.Join(filepath.Dir(path), t)
		}
		return t, nil
	}
	if !fi.Mode().IsRegular() {
		return "", fmt.Errorf("%s exists and is not a file or symlink", path)
	}
	keep := path + ".pre-fleet-install"
	if _, err := os.Lstat(keep); err == nil {
		return keep, nil
	}
	if err := os.Link(path, keep); err != nil {
		if err := copyFile(path, keep, fi.Mode().Perm()); err != nil {
			return "", err
		}
	}
	return keep, nil
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// placeVersion installs files into <installRoot>/<dir>/ via a staging dir and rename.
// files maps destination name -> source path; the binary is mode 0755.
func placeVersion(installRoot, dir, binary string, files map[string]string) error {
	dest := filepath.Join(installRoot, dir)
	if old := filepath.Join(dest, binary); fileExists(old) {
		a, _ := SHA256File(old)
		b, _ := SHA256File(files[binary])
		if a != "" && a == b {
			return nil // identical binary already in place
		}
	}
	stage := filepath.Join(installRoot, ".fleet-install", fmt.Sprintf("stage-%d", os.Getpid()))
	_ = os.RemoveAll(stage)
	if err := os.MkdirAll(stage, 0o755); err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	for name, src := range files {
		mode := os.FileMode(0o644)
		if name == binary {
			mode = 0o755
		}
		if err := copyFile(src, filepath.Join(stage, name), mode); err != nil {
			return err
		}
	}
	if err := os.RemoveAll(dest); err != nil {
		return err
	}
	return os.Rename(stage, dest)
}

// prune removes version directories that fall out of the newest keepVersions, never
// the current or previous release. It only touches directories recorded in state.
func prune(installRoot string, s *State) []string {
	keep := map[string]bool{}
	for _, r := range []*Release{s.Current, s.Previous} {
		if r != nil && r.Dir != "" {
			keep[r.Dir] = true
		}
	}
	var kept []Release
	var removed []string
	for _, r := range s.History {
		if r.Dir == "" {
			continue
		}
		if len(kept) < keepVersions || keep[r.Dir] {
			kept = append(kept, r)
			keep[r.Dir] = true
			continue
		}
		if filepath.Base(r.Dir) == r.Dir && r.Dir != "." && r.Dir != ".." && r.Dir != ".fleet-install" {
			_ = os.RemoveAll(filepath.Join(installRoot, r.Dir))
			removed = append(removed, r.Dir)
		}
	}
	s.History = kept
	return removed
}

// writeDropins writes unit drop-ins idempotently; it reports whether any file changed.
func (d *Deps) writeDropins(t *Target, dry bool) (bool, error) {
	changed := false
	for _, dp := range t.UnitDropins {
		name := dp.Name
		if filepath.Ext(name) != ".conf" {
			name += ".conf"
		}
		dir := filepath.Join(d.Home, ".config", "systemd", "user", dp.Unit+".d")
		p := filepath.Join(dir, name)
		if cur, err := os.ReadFile(p); err == nil && string(cur) == dp.Content {
			d.say("dropin: %s unchanged", p)
			continue
		}
		changed = true
		if dry {
			d.say("dropin: would write %s", p)
			continue
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return changed, err
		}
		if err := os.WriteFile(p, []byte(dp.Content), 0o644); err != nil {
			return changed, err
		}
		d.say("dropin: wrote %s", p)
	}
	if changed && !dry {
		if _, err := d.Run.Run(context.Background(), "systemctl", "--user", "daemon-reload"); err != nil {
			return changed, fmt.Errorf("daemon-reload: %v", err)
		}
		d.say("dropin: systemctl --user daemon-reload")
	}
	return changed, nil
}
