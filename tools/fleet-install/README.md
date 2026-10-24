# fleet-install

A generic, static Go binary that installs a CI-built release described by a `deploy.yaml`
that ships inside the release tarball (CW-20261024-0120; design in
`docs/architecture/ci-binaries-0120.md`). Nothing in the code is app-specific: every
path, probe, restart and health check comes from `deploy.yaml`.

Build: `cd tools/fleet-install && GOWORK=off CGO_ENABLED=0 go build -o fleet-install .`
(own `go.mod`; the repository root module does not include it). Test:
`go vet ./... && go test -count=1 ./...` (hermetic: fakes for `gh`, `systemctl`,
`cerberus`; `httptest` for health; real `sqlite3` where tests need a database and
skipped when it is absent).

Runtime tools: `gh` (download only), `sqlite3` (probes, backups), `systemctl` (mode
`systemd-user`). Tarball extraction and sha256 are in-process.

## Commands

```
fleet-install deploy <app> [--commit SHA | --run ID | --latest-green | --from-file TARBALL [--sums FILE]]
              [--repo owner/name] [--target ID] [--dry-run] [--accept-migration] [--force]
              [--restart-done] [--root DIR] [--branch main] [--workflow build.yml]
              [--artifact-name '{app}-{sha12}'] [--platform os/arch]
fleet-install rollback <app> [--target ID] [--restart-done] [--root DIR]
fleet-install status   <app> [--target ID] [--root DIR]
fleet-install list     [--root DIR]
fleet-install version
```

Deploy flow (each step is logged; any failure stops):

1. **Resolve.** `--commit SHA` (a short SHA is resolved with `gh api`) needs a successful
   run of the build workflow (`gh run list --workflow build.yml --commit <sha40> --status
   success`); no green run, no deploy. `--run ID` needs a successful run. `--latest-green`
   takes the newest successful run on `--branch`. The artifact is fetched with
   `gh run download <id> -R <repo> -n nanite-<sha12>` into `<root>/cache/<app>/run-<id>`.
   `--from-file` uses a local tarball and needs no GitHub access.
2. **Verify.** `SHA256SUMS` (next to the tarball, or `--sums`) must list the tarball and
   `build-info.json` (when present) and the hashes must match, or the deploy is refused.
3. **Read** `deploy.yaml` from the tarball (extraction rejects absolute paths, `..`,
   links and devices) and select the target: `--target`, else the one whose `hosts`
   contains this host name and whose `when` matches the tarball's os/arch.
4. **Preflight** in the listed order (see kinds below).
5. **Install**: copy to `<install_root>/<version>/<binary>` (0755, with `deploy.yaml` and
   `build-info.json`), write unit drop-ins, flip every symlink atomically (temp link, then
   rename), record state, keep the last 3 versions.
6. **Restart** the ordered list, then **health-check** the ordered list.
7. **Roll back automatically** on any failure after the first flip: relink the previous
   release, restart, re-check health, print the backup paths and the manual DB restore
   commands. A database restore is never automatic. Exit 4 (rolled back) or 5 (rollback
   failed too).

`--dry-run` prints every step and touches nothing in the install tree. It does run the
read-only probes (`sql_zero`, `migration_check`; `sqlite3 -readonly`) so it can say what
would refuse the deploy (exit 3 then). With `--run/--commit/--latest-green` it downloads
the artifact into the cache dir (it says so); with `--from-file` it extracts into a temp
directory that is removed.

Install directory name is the build version; for an untagged build (`dev`) it is
`dev-<sha12>` so commits do not collide.

### Where the repo and the first install come from

`deploy.yaml` is inside the tarball, so before the first download the tool does not know
the repo. Use `--repo owner/name` on the first GitHub deploy (or `--from-file`); after a
successful deploy the repo, artifact name pattern and install root are remembered in
`<root>/apps/<app>.json`, which `rollback`, `status`, `list` and later deploys use.

`--root DIR` overrides that registry/cache directory (default `$XDG_STATE_HOME/fleet-install`,
else `~/.local/state/fleet-install`). The per-target state lives with the install:
`<install_root>/.fleet-install/state.json`.

## deploy.yaml schema `fleet-deploy/v1`

Unknown fields and unknown values are errors, reported with their location. `~/` is
expanded in paths. `restart` and `health` take a list or, for one entry, a single mapping.

| Field | Meaning |
| --- | --- |
| `schema` | must be `fleet-deploy/v1` |
| `app`, `repo` | required; `repo` is `owner/name` |
| `artifact.tarball` | required; template with `{version}`, `{os}`, `{arch}` (and `{sha12}`); used to parse the tarball name |
| `artifact.name` | workflow artifact name pattern, default `{app}-{sha12}` (remembered for later deploys) |
| `artifact.sums` | checksum file name, informational (always `SHA256SUMS`) |
| `targets[]` | at least one |
| `targets[].id` | required, unique |
| `targets[].when` | `{os, arch}` of the tarball this target takes |
| `targets[].hosts` | host names (case-insensitive, short or full) for automatic selection |
| `targets[].binary` | plain file name inside the tarball |
| `targets[].install_root` | absolute (or `~/`) directory holding `<version>/` dirs |
| `targets[].backup_dir` | optional; default `<install_root>/.fleet-install/backups/<UTC time>_<version>` (mode 0700) |
| `targets[].symlinks[].path` | paths flipped to `<install_root>/<version>/<binary>`; an existing regular file is kept once as `<path>.pre-fleet-install` and becomes the legacy rollback point |
| `targets[].unit_dropins[]` | `{unit, name, content}` written to `~/.config/systemd/user/<unit>.d/<name>.conf` only when the content differs, then `systemctl --user daemon-reload` |
| `targets[].preflight[]` | see kinds |
| `targets[].restart[]` | see modes |
| `targets[].health[]` | see health |

Template variables in `dest`: `{backup_dir} {version} {sha12} {app} {os} {arch} {install_dir}`.

### Preflight kinds

* `sql_zero {db, sql}`: `sqlite3 -readonly` must print `0`; otherwise refuse (exit 3) unless
  `--force`. A missing database is skipped (first install).
* `sqlite_backup {db, dest}`: `sqlite3 <db> ".backup '<dest>'"` (WAL-safe), then
  `pragma integrity_check` must be `ok`; directory 0700, file 0600; an existing `dest` is refused.
* `migration_check {db, [sql]}`: compares `build-info.json` `migrations_max` with
  `select max(version_id) from goose_db_version` (override with `sql`). If the build is
  ahead the deploy stops (exit 3) unless `--accept-migration`; both numbers are printed.
  Skipped when the build has no `migrations_max`, or the database or table does not exist.
* `file_backup {path, dest}`: copy a file to `dest` (0600).

### Restart modes (`restart[].via`, run in order)

* `systemd-user {unit}`: `systemctl --user restart <unit>`.
* `cerberus {resource, [verb], [run]}`: `verb` is `reload` (default), `restart` or `apply`
  (never `deploy`). By default the tool only **prints** the exact request
  `cerberus resource <verb> <resource> --ack` for a human (Approve and Run) and, after
  the install, exits 10 ("waiting"); nothing is rolled back. After the operator ran it,
  re-run the same deploy command with `--restart-done` to continue with the health check
  (the already-installed version is not reinstalled as "previous"). With `run: true`
  and a local user the tool runs it itself.
* `command {argv}`: run an argv list (`{version} {sha12} {app} {install_dir}` expand); for
  custom hosts and tests.
* `none`.

### Health (`health[]`, run in order, each polled until it passes or times out)

`{url, [method GET|HEAD|POST], [expect_status [200]], [timeout 30s], [interval 1s], [version_json]}`.
`expect_status` is a list (for example `[401]` proves liveness of an API that needs a token).
`version_json` is a dotted JSON field path whose value must contain the new version or
its sha12. No credentials are sent.

## State

`<install_root>/.fleet-install/state.json` (0600): `current`, `previous`, `history`
(newest first, three kept, older version directories are removed), each release with its
version, directory, sha12, install time, symlink targets and the backups taken before it.
`rollback` relinks `previous`, restarts, health-checks and swaps current/previous, so
running it again rolls forward. A failed manual rollback relinks the version it left.

## Exit codes

0 ok; 1 failure; 2 usage; 3 refused (active work, migration, checksum, bad tarball,
backup failure, no green build); 4 post-install failure, rolled back automatically;
5 post-install failure and the rollback failed too; 10 installed, waiting for an
operator-run cerberus restart (`--restart-done`). One line is printed to stderr.

## Security notes

* No credentials are stored or printed. `gh` uses whatever login the machine has;
  health checks send no authorization. Nothing reads app env files.
* Integrity is the `SHA256SUMS` check, in-process, mandatory. Attestation verification
  is not implemented (the design makes it optional).
* `--from-file` lets a machine without GitHub access install a tarball copied from one
  that has (copy `SHA256SUMS` along).
* Extraction refuses path traversal, absolute paths, links and devices, and caps the
  total size.
* Backups are 0600 in a 0700 directory. Database restores are manual, by design.
* On macOS/APFS a concurrent reader of a symlink can briefly get `EINVAL` while it is
  renamed over (verified with a bare rename loop); on Linux the flip is atomic for readers.
