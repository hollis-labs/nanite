package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Backup records a file produced by a preflight backup step.
type Backup struct {
	Kind, Src, Dest string
}

type preflightOpts struct {
	Force, AcceptMigration, Dry bool
}

func (d *Deps) sqlite(ctx context.Context, args ...string) (string, error) {
	out, err := d.Run.Run(ctx, "sqlite3", args...)
	return strings.TrimSpace(out), err
}

func quoteOK(p string) error {
	if strings.ContainsAny(p, "'\"\n\r") {
		return fmt.Errorf("path %q contains a quote or newline", p)
	}
	return nil
}

// preflight runs the checks and backups in order. Read-only probes also run in a dry
// run; backups never do. blocked is true when something would refuse the deploy.
func (d *Deps) preflight(ctx context.Context, t *Target, vars map[string]string, bi *BuildInfo, o preflightOpts) (backups []Backup, blocked bool, err error) {
	for i, p := range t.Preflight {
		label := fmt.Sprintf("preflight[%d] %s", i, p.Kind)
		db := d.expand(p.DB)
		switch p.Kind {
		case "sql_zero":
			if !fileExists(db) {
				d.say("%s: %s does not exist yet, skipped", label, db)
				continue
			}
			out, qerr := d.sqlite(ctx, "-readonly", "-batch", db, p.SQL)
			if qerr != nil {
				return backups, true, fail(ExitRefused, "preflight sql_zero on %s failed: %v", filepath.Base(db), qerr)
			}
			n, perr := strconv.Atoi(out)
			if perr != nil {
				return backups, true, fail(ExitRefused, "preflight sql_zero on %s returned %q, expected a number", filepath.Base(db), out)
			}
			if n == 0 {
				d.say("%s: %s -> 0, ok", label, filepath.Base(db))
				continue
			}
			if o.Force {
				d.say("%s: %s -> %d, continuing because of --force", label, filepath.Base(db), n)
				continue
			}
			if o.Dry {
				d.say("%s: %s -> %d, WOULD BLOCK (pass --force to override)", label, filepath.Base(db), n)
				blocked = true
				continue
			}
			return backups, true, fail(ExitRefused, "active work: %s probe returned %d (wait, or pass --force)", filepath.Base(db), n)
		case "sqlite_backup":
			dest := d.expand(ExpandTemplate(p.Dest, vars))
			if !fileExists(db) {
				d.say("%s: %s does not exist yet, skipped", label, db)
				continue
			}
			if o.Dry {
				d.say("%s: would run sqlite3 .backup %s -> %s and check integrity", label, db, dest)
				backups = append(backups, Backup{p.Kind, db, dest})
				continue
			}
			if err := d.sqliteBackup(ctx, db, dest, vars["backup_dir"]); err != nil {
				return backups, true, fail(ExitRefused, "backup of %s failed: %v", filepath.Base(db), err)
			}
			d.say("%s: %s -> %s (integrity ok)", label, db, dest)
			backups = append(backups, Backup{p.Kind, db, dest})
		case "file_backup":
			src := d.expand(p.Path)
			dest := d.expand(ExpandTemplate(p.Dest, vars))
			if !fileExists(src) {
				d.say("%s: %s does not exist, skipped", label, src)
				continue
			}
			if o.Dry {
				d.say("%s: would copy %s -> %s", label, src, dest)
				backups = append(backups, Backup{p.Kind, src, dest})
				continue
			}
			if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
				return backups, true, fail(ExitRefused, "file_backup: %v", err)
			}
			if fileExists(dest) {
				return backups, true, fail(ExitRefused, "file_backup: %s already exists", dest)
			}
			if err := copyFile(src, dest, 0o600); err != nil {
				return backups, true, fail(ExitRefused, "file_backup: %v", err)
			}
			d.say("%s: %s -> %s", label, src, dest)
			backups = append(backups, Backup{p.Kind, src, dest})
		case "migration_check":
			if bi == nil || bi.MigrationsMax == nil {
				d.say("%s: build has no migrations_max, skipped", label)
				continue
			}
			if !fileExists(db) {
				d.say("%s: %s does not exist yet, skipped", label, db)
				continue
			}
			q := p.SQL
			if q == "" {
				q = "select max(version_id) from goose_db_version"
			}
			out, qerr := d.sqlite(ctx, "-readonly", "-batch", db, q)
			if qerr != nil {
				if strings.Contains(qerr.Error(), "no such table") {
					d.say("%s: no goose table in %s, skipped", label, filepath.Base(db))
					continue
				}
				return backups, true, fail(ExitRefused, "migration_check on %s failed: %v", filepath.Base(db), qerr)
			}
			cur := 0
			if out != "" {
				cur, err = strconv.Atoi(out)
				if err != nil {
					return backups, true, fail(ExitRefused, "migration_check on %s returned %q, expected a number", filepath.Base(db), out)
				}
			}
			d.say("%s: database goose version %d, build migrations_max %d", label, cur, *bi.MigrationsMax)
			if *bi.MigrationsMax <= cur {
				continue
			}
			if o.AcceptMigration {
				d.say("%s: migration will run (%d -> %d), accepted with --accept-migration", label, cur, *bi.MigrationsMax)
				continue
			}
			if o.Dry {
				d.say("%s: WOULD STOP, this build migrates %d -> %d (pass --accept-migration)", label, cur, *bi.MigrationsMax)
				blocked = true
				continue
			}
			return backups, true, fail(ExitRefused, "this build migrates the database %d -> %d: pass --accept-migration to proceed", cur, *bi.MigrationsMax)
		}
	}
	return backups, blocked, nil
}

func (d *Deps) sqliteBackup(ctx context.Context, db, dest, backupDir string) error {
	if err := quoteOK(dest); err != nil {
		return err
	}
	if err := quoteOK(db); err != nil {
		return err
	}
	dir := filepath.Dir(dest)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if dir == backupDir {
		if err := os.Chmod(dir, 0o700); err != nil {
			return err
		}
	}
	if fileExists(dest) {
		return fmt.Errorf("%s already exists", dest)
	}
	if _, err := d.sqlite(ctx, db, ".backup '"+dest+"'"); err != nil {
		_ = os.Remove(dest)
		return err
	}
	out, err := d.sqlite(ctx, dest, "pragma integrity_check")
	if err != nil || out != "ok" {
		return fmt.Errorf("integrity_check on backup returned %q (%v); kept at %s", out, err, dest)
	}
	return os.Chmod(dest, 0o600)
}
