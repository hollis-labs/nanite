# Guard migration numbers against collision and back-fill — the one unrecoverable failure class

**Phase:** 1 — Safe parallel work
**Status:** not-started
**Depends on:** none
**Touches:** `lefthook.yml`, a new check script (suggest
`scripts/check-migration-number.sh` — match whatever convention the repo's
other hook-invoked scripts use), and `docs/engineering/tracking-integrity.md`
(check 9). Repo: nanite.

## Context

**This is the task that makes parallel batch work safe.** Everything else the
gate catches is directional and recoverable — a lint regression, a gosec
increase, a race flake, all get found on the next manual gate run and fixed in
a follow-up. A bad migration number is different in kind: it is **a service
that fails to start**, discovered at boot, on every existing deployment.

Verified at `77137106`:

```
grep -n 'goose.NewProvider' internal/store/store.go
#   153:  provider, err := goose.NewProvider(goose.DialectSQLite3, s.DB, migrationsDir, goose.WithVerbose(false))
```

No `WithAllowOutofOrder`. So a migration numbered below a database's highest
applied version is a hard error, not a back-fill — reproduced at goose v3.27.3
with Nanite's exact options as
`detected 1 missing (out-of-order) migration lower than database version (137): version 135`.

Nothing guards this today. `migration-purity` is the only migration-aware hook
and it greps staged *file contents* for a `VALUES` clause — it never reads a
filename:

```
grep -n 'migration-purity' -A 30 lefthook.yml | grep -n 'VALUES\|filename\|basename'
```

And it **structurally cannot** be fixed in place: a staged-file hook sees only
the files in its own worktree, so two agents each creating `149_*.sql` in
separate worktrees both pass, and the collision only exists once both reach
`main`.

Two distinct failure shapes, and a correct check must cover both:

1. **Collision** — two worktrees both claim the next number. Duplicate prefix
   on `main`.
2. **Back-fill into a burned hole** — `135` is empty (`63d79028` shifted Loops'
   `135`–`143` to `138`–`146` to clear a collision with Skills) and *looks*
   available. It is not. The live database's ledger max is `137` with no `135`
   row. **"Next free" is one past the highest, never the lowest unused
   integer.** `[[nanite_migration_holes_permanently_burned]]`

The claiming rule is already published — `TASKS/INDEX.md`'s "Migration
numbering" section and `docs/engineering/tracking-integrity.md` check 9. **This
task turns that documented rule into a mechanism.** A rule with no mechanism
behind it decays, and this one decays into a boot failure.

## What to do

1. Write a check that, for each **added** migration file in the commit,
   compares against `main`'s tree rather than the local worktree:
   ```
   git ls-tree --name-only main -- internal/store/migrations/ | sort -t_ -k1 -n | tail -1
   ```
   Assert both properties: the new number is **not already present** on `main`,
   and it is **strictly greater than `main`'s highest** — the second is what
   covers the burned-hole case, and a check that only tests uniqueness will
   happily accept `135`.
2. Decide and document which hook it belongs on. **`pre-push` is the better
   fit than `pre-commit`** — the check needs `main` to be current, a commit can
   legitimately be made offline, and pre-push is where `go test ./...` already
   lives. Record the reasoning either way; this is an adjustable call, not a
   decided one.
3. Handle the case where `main` is stale or absent (fresh clone, detached
   worktree, offline). **Fail loudly with an actionable message, do not skip
   silently.** A guard that quietly no-ops when it cannot reach `main` is worse
   than no guard, because it reads as protection.
4. Make the failure message tell the author what to do — print `main`'s current
   highest, the number they used, and the number they should use. This check
   fires on someone mid-commit who needs to rename a file, not on someone
   reading a spec.
5. Cross-reference `docs/engineering/tracking-integrity.md` check 9 so the
   documented rule and the mechanism name each other.

## Done means

- A migration numbered at or below `main`'s highest is **rejected**, proven by
  actually attempting it — create `135_probe.sql` in a scratch branch and show
  the hook rejecting it, with the output in the Work log. Do not accept this
  task on code inspection.
- A duplicate of an existing number on `main` is rejected, proven the same way.
- The next legitimate number is **accepted**, proven the same way. A check that
  rejects everything passes the two tests above and is useless.
- The unreachable-`main` path fails with an actionable message, proven by
  running it with a bogus ref.
- The reasoning for pre-commit vs pre-push is recorded in the Work log.
- `docs/engineering/tracking-integrity.md` check 9 names the script, and the
  script names check 9.
- **Note for the reviewer:** this hook only protects clones where
  `lefthook install` has run. That is a real residual gap, not something this
  task can close — see the batch README's scope fence. Do not let it block
  acceptance, and do not let the Work log claim more coverage than that.

## Work log

## Review notes
