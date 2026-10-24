package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type DeployOpts struct {
	App                                         string
	Source                                      Source
	Target                                      string
	DryRun, AcceptMigration, Force, RestartDone bool
	Root                                        string
}

func (d *Deps) root(o string) string {
	if o != "" {
		return o
	}
	if x := os.Getenv("XDG_STATE_HOME"); x != "" {
		return filepath.Join(x, "fleet-install")
	}
	return filepath.Join(d.Home, ".local", "state", "fleet-install")
}

func hostMatches(hosts []string, host string) bool {
	short := strings.ToLower(strings.SplitN(host, ".", 2)[0])
	for _, h := range hosts {
		h = strings.ToLower(h)
		if h == strings.ToLower(host) || h == short {
			return true
		}
	}
	return false
}

// SelectTarget picks the target by id, or by host name plus os/arch.
func SelectTarget(s *Spec, id, host, os_, arch string) (*Target, error) {
	var found []*Target
	for i := range s.Targets {
		t := &s.Targets[i]
		whenOK := (os_ == "" || t.When.OS == os_) && (arch == "" || t.When.Arch == arch)
		if id != "" {
			if t.ID == id {
				if !whenOK {
					return nil, fmt.Errorf("target %s is for %s/%s but the tarball is %s/%s", id, t.When.OS, t.When.Arch, os_, arch)
				}
				return t, nil
			}
			continue
		}
		if whenOK && hostMatches(t.Hosts, host) {
			found = append(found, t)
		}
	}
	if id != "" {
		return nil, fmt.Errorf("no target %q in deploy.yaml (targets: %s)", id, targetIDs(s))
	}
	switch len(found) {
	case 1:
		return found[0], nil
	case 0:
		return nil, fmt.Errorf("no target matches host %q and %s/%s; pass --target (targets: %s)", host, os_, arch, targetIDs(s))
	}
	return nil, fmt.Errorf("several targets match host %q (%s); pass --target", host, targetIDs(s))
}

func targetIDs(s *Spec) string {
	var ids []string
	for _, t := range s.Targets {
		ids = append(ids, t.ID)
	}
	return strings.Join(ids, ", ")
}

func shortSHA(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

func (d *Deps) Deploy(ctx context.Context, o DeployOpts) error {
	root := d.root(o.Root)
	reg, err := LoadRegistry(root, o.App)
	if err != nil {
		return fail(ExitFail, "registry: %v", err)
	}
	src := o.Source
	if src.Repo == "" && reg != nil {
		src.Repo = reg.Repo
	}
	if src.Workflow == "" {
		src.Workflow = "build.yml"
		if reg != nil && reg.Workflow != "" {
			src.Workflow = reg.Workflow
		}
	}
	if src.Artifact == "" {
		src.Artifact = "{app}-{sha12}"
		if reg != nil && reg.Artifact != "" {
			src.Artifact = reg.Artifact
		}
	}
	if src.Branch == "" {
		src.Branch = "main"
	}
	if src.Platform == "" {
		src.Platform = d.OS + "/" + d.Arch
	}
	src.CacheDir = filepath.Join(root, "cache")
	if o.DryRun && src.FromFile == "" {
		d.say("dry-run: downloading the artifact to the cache dir %s (nothing else is touched)", src.CacheDir)
	}
	f, err := d.Fetch(ctx, o.App, src)
	if err != nil {
		return err
	}

	tmp, err := os.MkdirTemp("", "fleet-install-*")
	if err != nil {
		return fail(ExitFail, "%v", err)
	}
	defer os.RemoveAll(tmp)
	if err := ExtractTarGz(f.Tarball, tmp); err != nil {
		return fail(ExitRefused, "%v", err)
	}
	specPath := findFile(tmp, "deploy.yaml")
	if specPath == "" {
		return fail(ExitFail, "tarball has no deploy.yaml")
	}
	data, _ := os.ReadFile(specPath)
	spec, err := ParseSpec(data)
	if err != nil {
		return fail(ExitFail, "%v", err)
	}
	if spec.App != o.App {
		return fail(ExitFail, "tarball is for app %q, not %q", spec.App, o.App)
	}
	stageDir := filepath.Dir(specPath)

	version, tOS, tArch, perr := ParseTarballName(spec.Artifact.Tarball, filepath.Base(f.Tarball))
	if perr != nil {
		return fail(ExitFail, "%v", perr)
	}
	sha12, commit := f.SHA12, ""
	if bi := f.BuildInfo; bi != nil {
		if bi.App != "" && bi.App != o.App {
			return fail(ExitFail, "build-info.json is for app %q", bi.App)
		}
		if bi.Version != "" && bi.Version != version {
			return fail(ExitFail, "build-info version %q does not match the tarball name version %q", bi.Version, version)
		}
		sha12, commit = shortSHA(bi.SHA12), bi.Commit
		if sha12 == "" {
			sha12 = shortSHA(bi.Commit)
		}
	}
	if !versionRe.MatchString(version) {
		return fail(ExitFail, "version %q contains characters not allowed in a directory name", version)
	}
	if tOS == "" {
		tOS, tArch, _ = strings.Cut(src.Platform, "/")
	}
	t, err := SelectTarget(spec, o.Target, d.Hostname, tOS, tArch)
	if err != nil {
		return fail(ExitFail, "%v", err)
	}
	installRoot := d.expand(t.InstallRoot)
	dirName := version
	if version == "dev" && sha12 != "" { // untagged builds all say "dev"; keep commits apart
		dirName = version + "-" + sha12
	}
	stamp := d.Now().UTC().Format("20060102-150405")
	backupDir := filepath.Join(installRoot, ".fleet-install", "backups", stamp+"_"+dirName)
	if t.BackupDir != "" {
		backupDir = d.expand(ExpandTemplate(t.BackupDir, map[string]string{"app": o.App, "version": version, "sha12": sha12}))
	}
	vars := map[string]string{"backup_dir": backupDir, "version": version, "sha12": sha12, "app": o.App,
		"os": tOS, "arch": tArch, "install_dir": filepath.Join(installRoot, dirName)}

	d.say("deploy %s %s (%s) -> target %s on %s [%s/%s]", o.App, version, sha12, t.ID, d.Hostname, tOS, tArch)
	if o.DryRun {
		d.say("dry-run: nothing below is executed or written")
	}

	// Preflight.
	backups, blocked, err := d.preflight(ctx, t, vars, f.BuildInfo, preflightOpts{o.Force, o.AcceptMigration, o.DryRun})
	if err != nil {
		return err
	}

	binSrc := filepath.Join(stageDir, t.Binary)
	if !fileExists(binSrc) {
		return fail(ExitFail, "tarball has no %s", t.Binary)
	}
	files := map[string]string{t.Binary: binSrc, "deploy.yaml": specPath}
	if f.BuildInfo != nil {
		if p := filepath.Join(filepath.Dir(f.Tarball), "build-info.json"); fileExists(p) {
			files["build-info.json"] = p
		}
	}
	newLinks := map[string]string{}
	for _, l := range t.Symlinks {
		newLinks[d.expand(l.Path)] = filepath.Join(installRoot, dirName, t.Binary)
	}

	if o.DryRun {
		d.say("install: would place %s/%s (0755) and keep the last %d versions", vars["install_dir"], t.Binary, keepVersions)
		for p, tg := range newLinks {
			d.say("install: would flip symlink %s -> %s (atomic)", p, tg)
		}
		_, _ = d.writeDropins(t, true)
		_, _ = d.restartAll(ctx, t.Restart, vars, o.RestartDone, true)
		for i, h := range t.Health {
			to, iv := h.Durations()
			d.say("health[%d]: would poll %s expect %v every %s for up to %s version_json=%q", i, h.URL, orDefault(h.ExpectStatus), iv, to, h.VersionJSON)
		}
		if blocked {
			return fail(ExitRefused, "dry-run: a preflight check would refuse this deploy")
		}
		d.say("dry-run complete: ok")
		return nil
	}

	// Install.
	st, err := LoadState(installRoot)
	if err != nil {
		return fail(ExitFail, "state: %v", err)
	}
	if st == nil {
		st = &State{App: o.App, Target: t.ID}
	}
	var prev *Release
	if st.Current != nil {
		prev = st.Previous
	} else {
		legacy := &Release{Version: "legacy", InstalledAt: d.Now().UTC().Format(time.RFC3339), Links: map[string]string{}}
		for p := range newLinks {
			old, err := captureExisting(p)
			if err != nil {
				return fail(ExitFail, "%v", err)
			}
			if old != "" {
				legacy.Links[p] = old
			}
		}
		if len(legacy.Links) > 0 {
			prev = legacy
			d.say("install: recorded the existing %d link target(s) as the legacy rollback point", len(legacy.Links))
		}
	}
	if err := placeVersion(installRoot, dirName, t.Binary, files); err != nil {
		return fail(ExitFail, "install: %v", err)
	}
	d.say("install: %s", vars["install_dir"])
	cur := Release{Version: version, Dir: dirName, SHA12: sha12, Commit: commit, InstalledAt: d.Now().UTC().Format(time.RFC3339), BackupDir: backupDir, Links: newLinks}
	for _, b := range backups {
		cur.Backups = append(cur.Backups, BackupRec{b.Kind, b.Src, b.Dest})
	}
	same := st.Current != nil && st.Current.Dir == dirName
	if same {
		cur.Backups = append(st.Current.Backups, cur.Backups...)
		if len(backups) == 0 {
			cur.BackupDir = st.Current.BackupDir
		}
	} else {
		if st.Current != nil {
			prevCopy := *st.Current
			prev = &prevCopy
		}
		st.History = append([]Release{cur}, st.History...)
	}
	st.Current, st.Previous = &cur, prev
	if _, err := d.writeDropins(t, false); err != nil {
		return fail(ExitFail, "unit drop-in: %v", err)
	}
	if err := flipLinks(newLinks); err != nil {
		return d.autoRollback(ctx, t, installRoot, st, vars, fmt.Errorf("symlink flip: %v", err))
	}
	for p, tg := range newLinks {
		d.say("install: symlink %s -> %s", p, tg)
	}
	if err := SaveState(installRoot, st); err != nil {
		return fail(ExitFail, "state: %v", err)
	}
	nreg := &Registry{App: o.App, Repo: spec.Repo, Artifact: spec.Artifact.Name, Workflow: src.Workflow, Targets: map[string]string{}}
	if reg != nil {
		for k, v := range reg.Targets {
			nreg.Targets[k] = v
		}
		if nreg.Repo == "" {
			nreg.Repo = reg.Repo
		}
	}
	nreg.Targets[t.ID] = installRoot
	if err := SaveRegistry(root, nreg); err != nil {
		return fail(ExitFail, "registry: %v", err)
	}

	// Restart and health.
	pending, err := d.restartAll(ctx, t.Restart, vars, o.RestartDone, false)
	if err != nil {
		return d.autoRollback(ctx, t, installRoot, st, vars, err)
	}
	if pending {
		return fail(ExitAwaiting, "installed %s; waiting for the restart above: run it, then re-run this same command with --restart-done", version)
	}
	if err := d.checkHealth(ctx, t.Health, []string{version, sha12}, false); err != nil {
		return d.autoRollback(ctx, t, installRoot, st, vars, err)
	}
	if removed := prune(installRoot, st); len(removed) > 0 {
		d.say("prune: removed old versions %s", strings.Join(removed, ", "))
	}
	if err := SaveState(installRoot, st); err != nil {
		return fail(ExitFail, "state: %v", err)
	}
	d.say("deployed %s (previous: %s)", version, relName(st.Previous))
	return nil
}

func orDefault(c []int) []int {
	if len(c) == 0 {
		return []int{200}
	}
	return c
}

func relName(r *Release) string {
	if r == nil {
		return "none"
	}
	return r.Version
}

// switchTo relinks to rel, restarts and health-checks it. Used by rollbacks.
func (d *Deps) switchTo(ctx context.Context, t *Target, rel *Release, vars map[string]string, restartDone bool) (pending bool, err error) {
	if err := flipLinks(rel.Links); err != nil {
		return false, err
	}
	for p, tg := range rel.Links {
		d.say("rollback: symlink %s -> %s", p, tg)
	}
	v := map[string]string{}
	for k, x := range vars {
		v[k] = x
	}
	v["version"], v["sha12"] = rel.Version, rel.SHA12
	if rel.Dir != "" {
		v["install_dir"] = filepath.Join(filepath.Dir(vars["install_dir"]), rel.Dir)
	}
	pending, err = d.restartAll(ctx, t.Restart, v, restartDone, false)
	if err != nil || pending {
		return pending, err
	}
	var want []string
	if rel.Version != "legacy" {
		want = []string{rel.Version, rel.SHA12}
	}
	return false, d.checkHealth(ctx, t.Health, want, false)
}

// autoRollback undoes a failed deploy: relink previous, restart, re-check, then print
// the backups. A database restore is never automatic.
func (d *Deps) autoRollback(ctx context.Context, t *Target, installRoot string, st *State, vars map[string]string, cause error) error {
	failed := st.Current
	d.say("FAILED: %v", cause)
	prev := st.Previous
	if prev == nil {
		d.printBackups(failed)
		return fail(ExitRollbackErr, "deploy failed (%v) and there is no previous version to roll back to", cause)
	}
	d.say("rolling back to %s", prev.Version)
	_, err := d.switchTo(ctx, t, prev, vars, false)
	d.printBackups(failed)
	if err != nil {
		return fail(ExitRollbackErr, "deploy failed (%v); ROLLBACK ALSO FAILED: %v", cause, err)
	}
	st.Current, st.Previous = prev, nil
	if len(st.History) > 0 && failed != nil && st.History[0].Dir == failed.Dir {
		st.History = st.History[1:]
	}
	_ = SaveState(installRoot, st)
	return fail(ExitRolledBack, "deploy of %s failed (%v); rolled back to %s", failed.Version, cause, prev.Version)
}

func (d *Deps) printBackups(r *Release) {
	if r == nil || len(r.Backups) == 0 {
		return
	}
	d.say("backups taken before this deploy (a database restore is NEVER automatic):")
	for _, b := range r.Backups {
		d.say("  %s: %s", b.Kind, b.Dest)
	}
	d.say("manual DB restore (only if the new version migrated the data), with the service stopped:")
	for _, b := range r.Backups {
		if b.Kind == "sqlite_backup" {
			d.say("  cp -p '%s' '%s' && rm -f '%s-wal' '%s-shm'", b.Dest, b.Src, b.Src, b.Src)
		}
	}
}

func (d *Deps) loadInstalled(o string, app, targetID string) (*Registry, string, string, error) {
	root := d.root(o)
	reg, err := LoadRegistry(root, app)
	if err != nil {
		return nil, "", "", fail(ExitFail, "registry: %v", err)
	}
	if reg == nil || len(reg.Targets) == 0 {
		return nil, "", "", fail(ExitFail, "%s has not been deployed with fleet-install (no registry in %s)", app, root)
	}
	if targetID == "" {
		if len(reg.Targets) > 1 {
			var ids []string
			for k := range reg.Targets {
				ids = append(ids, k)
			}
			sort.Strings(ids)
			return nil, "", "", fail(ExitUsage, "several targets (%s); pass --target", strings.Join(ids, ", "))
		}
		for k := range reg.Targets {
			targetID = k
		}
	}
	ir, ok := reg.Targets[targetID]
	if !ok {
		return nil, "", "", fail(ExitUsage, "unknown target %q for %s", targetID, app)
	}
	return reg, targetID, ir, nil
}

// installedSpec loads the deploy.yaml of the current release and its target.
func loadInstalledSpec(installRoot, targetID string) (*State, *Target, error) {
	st, err := LoadState(installRoot)
	if err != nil {
		return nil, nil, fail(ExitFail, "state: %v", err)
	}
	if st == nil || st.Current == nil || st.Current.Dir == "" {
		return nil, nil, fail(ExitFail, "no installed version recorded in %s", statePath(installRoot))
	}
	data, err := os.ReadFile(filepath.Join(installRoot, st.Current.Dir, "deploy.yaml"))
	if err != nil {
		return nil, nil, fail(ExitFail, "cannot read the installed deploy.yaml: %v", err)
	}
	spec, err := ParseSpec(data)
	if err != nil {
		return nil, nil, fail(ExitFail, "%v", err)
	}
	for i := range spec.Targets {
		if spec.Targets[i].ID == targetID {
			return st, &spec.Targets[i], nil
		}
	}
	return nil, nil, fail(ExitFail, "installed deploy.yaml has no target %q", targetID)
}

func (d *Deps) Rollback(ctx context.Context, app, targetID, root string, restartDone bool) error {
	_, tid, ir, err := d.loadInstalled(root, app, targetID)
	if err != nil {
		return err
	}
	st, t, err := loadInstalledSpec(ir, tid)
	if err != nil {
		return err
	}
	if st.Previous == nil {
		return fail(ExitFail, "no previous version recorded for %s/%s", app, tid)
	}
	cur, prev := st.Current, st.Previous
	d.say("rollback %s/%s: %s -> %s", app, tid, cur.Version, prev.Version)
	vars := map[string]string{"app": app, "install_dir": filepath.Join(ir, cur.Dir)}
	pending, err := d.switchTo(ctx, t, prev, vars, restartDone)
	if err != nil {
		d.say("rollback failed: %v; restoring %s", err, cur.Version)
		if _, e2 := d.switchTo(ctx, t, cur, vars, restartDone); e2 != nil {
			return fail(ExitRollbackErr, "rollback failed (%v) and restoring %s also failed: %v", err, cur.Version, e2)
		}
		return fail(ExitFail, "rollback failed (%v); %s restored", err, cur.Version)
	}
	st.Current, st.Previous = prev, cur
	if err := SaveState(ir, st); err != nil {
		return fail(ExitFail, "state: %v", err)
	}
	d.printBackups(cur)
	if pending {
		return fail(ExitAwaiting, "relinked %s; waiting for the restart above: run it, then re-run with --restart-done to health-check", prev.Version)
	}
	d.say("rolled back to %s", prev.Version)
	return nil
}

func (d *Deps) Status(ctx context.Context, app, targetID, root string) error {
	reg, tid, ir, err := d.loadInstalled(root, app, targetID)
	if err != nil {
		return err
	}
	st, t, err := loadInstalledSpec(ir, tid)
	if err != nil {
		return err
	}
	d.say("app: %s  repo: %s  target: %s  install_root: %s", app, reg.Repo, tid, ir)
	d.say("installed: %s (%s) at %s", st.Current.Version, st.Current.SHA12, st.Current.InstalledAt)
	d.say("previous:  %s", relName(st.Previous))
	paths := make([]string, 0, len(st.Current.Links))
	for p := range st.Current.Links {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		got, _ := os.Readlink(p)
		mark := "ok"
		if got != st.Current.Links[p] {
			mark = "DIFFERS from state (" + st.Current.Links[p] + ")"
		}
		d.say("symlink:   %s -> %s [%s]", p, got, mark)
	}
	if err := d.checkHealth(ctx, t.Health, []string{st.Current.Version, st.Current.SHA12}, true); err != nil {
		return fail(ExitFail, "unhealthy: %v", err)
	}
	d.say("health: ok")
	return nil
}

func (d *Deps) List(root string) error {
	ents, _ := os.ReadDir(filepath.Join(d.root(root), "apps"))
	if len(ents) == 0 {
		d.say("no apps deployed with fleet-install")
		return nil
	}
	for _, e := range ents {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		app := strings.TrimSuffix(e.Name(), ".json")
		reg, err := LoadRegistry(d.root(root), app)
		if err != nil || reg == nil {
			continue
		}
		ids := make([]string, 0, len(reg.Targets))
		for k := range reg.Targets {
			ids = append(ids, k)
		}
		sort.Strings(ids)
		for _, id := range ids {
			st, _ := LoadState(reg.Targets[id])
			v := "unknown"
			if st != nil && st.Current != nil {
				v = st.Current.Version
			}
			d.say("%s\t%s\t%s\t%s", app, id, v, reg.Targets[id])
		}
	}
	return nil
}
